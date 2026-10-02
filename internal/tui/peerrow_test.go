package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守着「对方的内容行铺灰底」这件事。
//
// 背景（用户提的第 1 条）：这一版把邮件做成了按层级的 IM 聊天，双方的话
// 混在一条时间轴上，光靠「左对齐 / 右对齐」在余光里不够用 —— 来回几轮
// 之后就得去读名字。给对方的行铺一条灰底，谁在说一眼就分得开。
//
// 底色的铺法是这个功能里唯一有技术含量的部分：行里已经套着各种前景色
// 样式，它们自己的 SGR 重置会把外层的底色一起清掉，所以「在每个重置之后
// 重新压上底色」这件事必须有判据守着 —— 否则哪天渲染层多出一种样式，
// 底色就会在那一行里断成几截，而且只在有那种样式的行上才看得出来。

// peerBandRow 是在只有 pip（对方）和 me（自己）两个会话成员的假模型里，
// 挑出「对方发的消息渲染出来的所有行」。
func peerBandRows(t *testing.T, m Model, width int) (peer, own [][]string) {
	t.Helper()
	th, ok := m.activeThread()
	if !ok {
		t.Fatal("没有打开的会话")
	}
	for _, msg := range th.Messages {
		rows := m.renderMessage(msg, width)
		if msg.From == m.cfg.Self() {
			own = append(own, rows)
		} else {
			peer = append(peer, rows)
		}
	}
	return peer, own
}

// 只有正文的空行（消息之间的分隔）不铺底，其余每一行都要铺。
func isPeerSeparator(row string) bool {
	return strings.TrimSpace(plainText(row)) == ""
}

// 对方的消息行必须铺着一条**连续**的灰底。
func TestChat_PeerRowsAreBandPainted(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerBandRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}

	const (
		bgOn  = "\x1b[48;" // 任何底色序列都以它开头
		reset = "\x1b[0m"  // 渲染层用的唯一一种重置序列
		bgOff = "\x1b[49m" // 恢复终端默认背景
	)

	for _, rows := range peer {
		for _, row := range rows {
			if isPeerSeparator(row) {
				continue
			}
			if !strings.Contains(row, bgOn) {
				t.Errorf("对方的行没有底色: %q", row)
				continue
			}
			// 行内每一个 SGR 重置之后，底色都必须立刻被压回来 ——
			// 断了的话，那一段之后的文字就掉回终端自己的背景上了。
			for _, tail := range strings.Split(row, reset)[1:] {
				if !strings.HasPrefix(tail, bgOn) {
					t.Errorf("重置之后底色断了（后面跟着 %q）: %q", truncate(tail, 20), row)
				}
			}
			// 收尾恢复默认背景，否则底色会漏到下一行去。
			if !strings.HasSuffix(row, bgOff) {
				t.Errorf("这一行的底色没有收尾，会漏到下一行: %q", row)
			}
		}
	}
}

// 自己发的行不能有底色。
//
// 这一条划的是「只对方」的边界：两边都铺，就等于两边都没铺。
func TestChat_OwnRowsAreNotBandPainted(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	_, own := peerBandRows(t, m, 80)
	if len(own) == 0 {
		t.Fatal("测试数据里没有自己发出的邮件")
	}

	for _, rows := range own {
		for _, row := range rows {
			if strings.Contains(row, "\x1b[48;") || strings.Contains(row, "\x1b[49m") {
				t.Errorf("自己发的行不该有底色: %q", row)
			}
		}
	}
}

// 灰底要一直铺到版面右沿，不能只在文字后面铺一小块。
//
// 铺到 width-2 是和「自己的行右对齐」停在同一列 —— 两边的边界对齐了，
// 频道才像一条条的行，而不是一堆长度不一的色块。
func TestChat_PeerBandReachesPaneEdge(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerBandRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}

	for _, rows := range peer {
		for _, row := range rows {
			if isPeerSeparator(row) {
				continue
			}
			if w := lipgloss.Width(row); w != 80-2 {
				t.Errorf("对方的行铺了 %d 列，应该铺满 %d 列: %q", w, 80-2, row)
			}
		}
	}
}

// 用真实模型走一遍：聊天视图里既要有灰底的行（对方），也要有无底色的行
// （自己）—— 防止哪天上面那几条判据在 renderMessage 上绿着，视图却另
// 走了一条路。
//
// ⚠️ 量的是**会话流那一栏**，不是整幅 View。整幅里每一行的最左边都压着
// 导航列的底色（侧栏是从上到下贯通的），于是「这一行有没有底色」既可能
// 来自对方消息的灰底、也可能来自左边那条导航条 —— 判据会从「有没有铺
// 灰底」退化成一条永远为真的空壳。上一版就是这么红的。
//
// 「切掉左边再看」这条路也走不通：实测 ansi.TruncateLeft 会把被切掉的
// 那些 SGR 以**空序列**的形式留在原处（`\x1b[48;5;235m\x1b[49m`），
// 底色照样"在"。所以直接问那一栏要它自己渲染出来的东西。
func TestChat_ViewHasBothBandsAndPlainRows(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	pane := m.renderChat(m.chatPaneWidth(), m.bodyHeight())
	var banded, plain []string
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, "\x1b[48;") {
			banded = append(banded, line)
			continue
		}
		if strings.TrimSpace(plainText(line)) != "" {
			plain = append(plain, line)
		}
	}

	if len(banded) == 0 {
		t.Error("聊天视图里没有一条铺了灰底的行（对方的话）")
	}
	if len(plain) == 0 {
		t.Error("聊天视图里没有一条无底色的行（自己的话 / 边框）")
	}

	// 光量那一栏还不够：整幅画面完全可能不用它（以前就是各自画各自的）。
	// 挑一条铺了底色的行，确认它原样出现在整幅 View 里。
	if len(banded) > 0 && !strings.Contains(m.View(), banded[0]) {
		t.Errorf("会话流那一栏的渲染结果没进整幅画面：%q", banded[0])
	}
}

// 对方那几行的消息头也要在灰底上。
//
// 头一行和正文如果一半有底一半没有，看着像两块拼错了的贴纸。
func TestChat_PeerHeadSitsOnTheBand(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	th, ok := m.activeThread()
	if !ok {
		t.Fatal("没有打开的会话")
	}
	var peerMsg thread.Header
	for _, msg := range th.Messages {
		if msg.From != m.cfg.Self() {
			peerMsg = msg
			break
		}
	}
	if peerMsg.MessageID == "" {
		t.Fatal("测试数据里没有对方发来的邮件")
	}

	head := m.renderMessage(peerMsg, 80)[0]
	if !strings.Contains(head, "\x1b[48;") {
		t.Errorf("对方的消息头没铺底色，和正文断成两块: %q", head)
	}
}
