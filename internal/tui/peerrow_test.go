package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守着「对方的话被圈在一个气泡里」这件事。
//
// 背景（用户提的第 1 条）：这一版把邮件做成了按层级的 IM 聊天，双方的话
// 混在一条时间轴上，光靠「左对齐 / 右对齐」在余光里不够用 —— 来回几轮
// 之后就得去读名字。给对方的正文套一个气泡，谁在说一眼就分得开。
//
// 底色的铺法是这个功能里唯一有技术含量的部分：行里已经套着各种前景色
// 样式，它们自己的 SGR 重置会把外层的底色一起清掉，所以「在每个重置之后
// 重新压上底色」这件事必须有判据守着 —— 否则哪天渲染层多出一种样式，
// 底色就会在那一行里断成几截，而且只在有那种样式的行上才看得出来。
//
// ⚠️ 这里的判据在重做视觉那一轮**全部重写过一遍**。上一版守的是「对方的
// 每一行都铺满整栏宽」（通栏灰带，像表格的行）。那个契约已经废了：现在
// 气泡**贴合内容宽度**，于是「铺满整栏」从"必须"变成了"必须不是"。

// peerAndOwnRows 挑出「对方发的」和「自己发的」消息各自的渲染结果。
func peerAndOwnRows(t *testing.T, m Model, width int) (peer, own [][]string) {
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

// bubbleBody 从一条消息的那些行里挑出**气泡正文**（铺了底色的行）。
//
// 消息头现在在气泡外面，所以不能拿"这一条消息的第 0 行"当正文 —— 那个
// 假设正是上一版留下的（那时头也在带上）。
func bubbleBody(rows []string) []string {
	var out []string
	for _, r := range rows {
		if strings.Contains(r, "\x1b[48;") {
			out = append(out, r)
		}
	}
	return out
}

// 对方的气泡必须铺着一条**连续**的底色：行内每个 SGR 重置之后底色都要
// 立刻回来，行尾要恢复默认背景。
func TestChat_PeerBubblePaintSurvivesResets(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}

	const (
		bgOn  = "\x1b[48;" // 任何底色序列都以它开头
		reset = "\x1b[0m"  // 渲染层用的唯一一种重置序列
		bgOff = "\x1b[49m" // 恢复终端默认背景
	)

	for _, rows := range peer {
		body := bubbleBody(rows)
		if len(body) == 0 {
			t.Errorf("对方这条消息一行气泡都没有：%q", rows)
			continue
		}
		for _, row := range body {
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
func TestChat_OwnRowsHaveNoBubble(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	_, own := peerAndOwnRows(t, m, 80)
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

// 气泡**贴合内容**，不通栏。
//
// 这是这一版和上一版最核心的一处分歧，所以判据要写得能真的分辨两者：
//
//   - 通栏的版本：每行宽度 == 整栏宽（上一版是 80-2 == 78）。
//   - 贴合的版本：短消息的气泡比整栏窄，长消息封顶在四分之三栏宽。
//
// 断言的是「**至少有气泡严格窄于上限**」而不是某个具体宽度 —— 具体宽度
// 随文案走，绑死它就是绑死措辞。
func TestChat_PeerBubbleHugsItsContent(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}

	// 上限：四分之三栏宽，和 renderMessage 里的算法同源。
	limit := 80 * 3 / 4
	sawNarrow := false
	for _, rows := range peer {
		for _, row := range bubbleBody(rows) {
			w := lipgloss.Width(row)
			if w > limit {
				t.Errorf("气泡宽 %d 列，超过了四分之三栏宽 %d: %q", w, limit, row)
			}
			if w < limit {
				sawNarrow = true
			}
		}
	}
	if !sawNarrow {
		t.Errorf("每一条气泡都顶满了上限 %d 列 —— 说明宽度不是按内容算的", limit)
	}
}

// 同一个气泡里的每一行**一样宽**。
//
// 每行各自贴合（比如给每行单独 padRight 到它自己的长度）看着像一条
// 参差不齐的色带，而不是一个框。这条判据用同一气泡内宽度的**一致性**
// 来表达"这是一个框"。
//
// 夹具里的正文都是短的单行，所以这条自己造一条多行的对方消息 ——
// 借「一条正文放不下、会折成两行」这个真实前提。
func TestChat_BubbleRowsShareOneWidth(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	th, ok := m.activeThread()
	if !ok {
		t.Fatal("没有打开的会话")
	}
	var target thread.Header
	for _, msg := range th.Messages {
		if msg.From != m.cfg.Self() {
			target = msg
			break
		}
	}
	if target.MessageID == "" {
		t.Fatal("测试数据里没有对方发来的邮件")
	}

	// 造一句"一定折行、但折出来的几行长度各不相同"的正文：
	// 折行宽度是 80*3/4-2 == 58，所以下面这串会折成 3 行，且末行明显更短。
	m.bodies = map[string]app.Body{
		target.MessageID: {Text: strings.Repeat("一二三四五六七八九十", 10) + "\n短"},
	}

	body := bubbleBody(m.renderMessage(target, 80))
	if len(body) < 2 {
		t.Fatalf("正文没折成多行（%d 行），量不了这条", len(body))
	}
	want := lipgloss.Width(body[0])
	for i, row := range body {
		if got := lipgloss.Width(row); got != want {
			t.Errorf("同一个气泡里第 %d 行宽 %d、第 0 行宽 %d —— 不是一个框: %q",
				i, got, want, row)
		}
	}
}

// 消息头**在气泡外面**。
//
// 上一版头和正文同铺一条灰带，「谁在说」也被圈了进去 —— 而头是**标注**，
// 不是内容。头留在外面，气泡里就只剩"这个人说的话"，那一段才是完整的
// 一个引用块。这条判据从反面钉住这件事。
func TestChat_PeerHeadSitsOutsideTheBubble(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}
	rows := peer[0]
	head := rows[0]

	if strings.Contains(head, "\x1b[48;") {
		t.Errorf("对方的消息头铺了气泡底色 —— 它该在气泡外面: %q", head)
	}
	if len(rows) < 2 || !strings.Contains(rows[1], "\x1b[48;") {
		t.Errorf("消息头下面那一行不是气泡的开头: %q", rows)
	}
}

// 气泡左沿留一格不铺色的空隙。
//
// 它不是为了好看：会话流的左沿紧挨着列表栏的底色块（235），气泡底色是
// 238 —— 直接贴上去，两块只差 3 格的面板就挤在一起、看不出边界。留一格
// 终端背景，气泡才"浮"在会话流里。
func TestChat_BubbleLeavesAGapOnTheLeft(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	body := bubbleBody(peer[0])
	if len(body) == 0 {
		t.Fatal("对方这条消息没有气泡行")
	}

	// 第一个可见字符画在什么底色上 —— 那是个空格，底色就是它左边那一格
	// 的状态。空串表示那一列没有底色，也就是那格留白还在。
	if got := bgAtFirstCell(body[0]); got != "" {
		t.Errorf("气泡从第 0 列就开始铺色了（底色 %q）—— 左边那一格空隙没了: %q",
			got, body[0])
	}
}

// 用真实模型走一遍：聊天视图里既要有气泡（对方），也要有无底色的行
// （自己）—— 防止哪天上面那几条判据在 renderMessage 上绿着，视图却另有
// 一条路。
//
// ⚠️ 量的是**会话流那一栏**，不是整幅 View。整幅里每一行的最左边都压着
// 导航列的底色、列表栏也是一整块底色，于是「这一行有没有底色」既可能
// 来自气泡、也可能来自那两块面板 —— 判据会从「有没有气泡」退化成一条
// 永远为真的空壳。上一版就是这么红的。
//
// 「切掉左边再看」这条路也走不通：实测 ansi.TruncateLeft 会把被切掉的
// 那些 SGR 以**空序列**的形式留在原处（`\x1b[48;5;235m\x1b[49m`），
// 底色照样"在"。所以直接问那一栏要它自己渲染出来的东西。
func TestChat_ViewHasBothBubblesAndPlainRows(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	pane := m.renderChat(m.chatPaneWidth(), m.bodyHeight())
	var bubbled, plain []string
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, "\x1b[48;") {
			bubbled = append(bubbled, line)
			continue
		}
		if strings.TrimSpace(plainText(line)) != "" {
			plain = append(plain, line)
		}
	}

	if len(bubbled) == 0 {
		t.Error("聊天视图里没有一条铺了底色的行（对方的话）")
	}
	if len(plain) == 0 {
		t.Error("聊天视图里没有一条无底色的行（自己的话 / 信息行）")
	}

	// 光量那一栏还不够：整幅画面完全可能不用它（以前就是各自画各自的）。
	// 挑一条铺了底色的行，确认它原样出现在整幅 View 里。
	if len(bubbled) > 0 && !strings.Contains(m.View(), bubbled[0]) {
		t.Errorf("会话流那一栏的渲染结果没进整幅画面：%q", bubbled[0])
	}
}

// 窄终端下气泡也要收得住 —— 不能因为"四分之三栏宽"算出来比剩下的宽度
// 还大，就把行撑破。
//
// 撑破的表现是终端自己折行，于是下面的行号全错位、鼠标全点错。这类
// 错误在真实终端里比在测试里难看出得多，所以在这里挡住。
//
// ⚠️ 宽度从 **2 列**起量，不是从"看起来还算正常"的 8 列起。8 列时这组
// 判据全绿过，而更窄的几档当时是错的：
//
//   - 头部按「整栏宽」算预算，对方那一行的前面还垫着一格留白 —— **12 列**
//     时头部就撑到 13 列。
//   - 气泡先按理想宽度折行、再被硬上限压窄，正文不跟着变，**8 列**时
//     气泡里的内容比气泡还宽。
//   - 气泡的「最小宽度」是留白 + 内边距×2 + **一个最宽的字符**，最后那
//     一项漏掉时 **4 列**下多出一列（折行的硬断点一次推进一个字符，而
//     一个汉字占 2 列）。
//
// 三处都只在特定宽度下露出来，所以判据在**所有宽度**上走一遍。
//
// ⚠️ 两个夹具都要走：纯文本那一份量不出「按字节算宽度」这类错
// （见 TestChat_BubbleMeasuresDisplayWidthNotBytes）—— 带超链接和表格的
// 那份才是它的回归面。
func TestChat_BubbleNeverOverflowsNarrowPane(t *testing.T) {
	forceColor(t)

	plain, _ := openChat(t)
	cases := []struct {
		name string
		m    Model
	}{
		{"纯文本夹具", plain},
		{"带 HTML / 超链接 / 表格的夹具", openSampleChat(t)},
	}

	for _, c := range cases {
		th, ok := c.m.activeThread()
		if !ok {
			t.Fatalf("%s：没有打开的会话", c.name)
		}
		for _, width := range []int{2, 3, 4, 8, 12, 20, 40, 80} {
			for _, msg := range th.Messages {
				for i, row := range c.m.renderMessage(msg, width) {
					if w := lipgloss.Width(row); w > width {
						t.Errorf("%s：宽 %d 时第 %d 行宽 %d 列，撑破了: %q",
							c.name, width, i, w, row)
					}
				}
			}
		}
	}
}

// 气泡宽度按**显示宽度**算，不是按字节算。
//
// 正文里可能有 OSC 8 超链接（`\x1b]8;;url\x1b\\文字\x1b]8;;\x1b\\`），
// 一段 39 列可见的行，字节数能到 80 出头。用逐 rune 求和的 textWidth 去量
// 它，量到的是「转义序列有多长」，气泡会跟着涨到整栏都装不下。
//
// 这条**真的坏过**：带链接的那封 HTML 邮件让气泡撑到 105 列（栏宽才 61），
// 而当时整套判据都是绿的 —— 因为唯一被量过的夹具里全是纯文本。上一版
// 是被一句「气泡宽不许超过上限」的钳制盖住的：气泡宽被钳回去了，正文行
// 还是那么宽。钳制盖住的是症状，"量错了尺子"才是病。
func TestChat_BubbleMeasuresDisplayWidthNotBytes(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	th, ok := m.activeThread()
	if !ok {
		t.Fatal("没有打开的会话")
	}
	var target thread.Header
	for _, msg := range th.Messages {
		if msg.From != m.cfg.Self() {
			target = msg
			break
		}
	}
	if target.MessageID == "" {
		t.Fatal("测试数据里没有对方发来的邮件")
	}

	const width = 80
	limit := width * 3 / 4
	m.bodies = map[string]app.Body{
		target.MessageID: {Text: "会议链接：[meeting.example.com/abc-def-ghi](https://meeting.example.com/abc-def-ghi)"},
	}

	body := bubbleBody(m.renderMessage(target, width))
	if len(body) == 0 {
		t.Fatal("对方这条消息没有气泡行")
	}
	for i, row := range body {
		if w := lipgloss.Width(row); w > limit {
			t.Errorf("气泡第 %d 行宽 %d 列，超过四分之三栏宽 %d —— 宽度多半是按字节算的: %q",
				i, w, limit, plainText(row))
		}
	}

	// 反向再钉一下：**贴合内容**和"顶到上限"要分得开。只断言「没超过上限」
	// 的话，一个把气泡一律撑满上限的实现照样绿 —— 而那种实现把这个 bug
	// 藏得更好（它看起来"收住了"）。
	if w := lipgloss.Width(body[0]); w >= limit {
		t.Errorf("气泡顶满了上限 %d 列，看不出它贴合内容: %q", limit, plainText(body[0]))
	}
}

// 很短的一条消息也要有个像样的气泡，而不是一格宽的补丁。
//
// 气泡宽度是「最长那一行 + 内边距」反推的，于是一句「好」会算出一个 4 列
// 宽的块 —— 那看着像渲染坏了，不像一个引用块。所以有一条最小宽度下限。
//
// 绑的是 minBubbleW 这个常量而不是某个列数：改下限是设计决定，判据只拦
// 「下限被拿掉」这一种改动。
func TestChat_ShortBubbleStillLooksLikeABubble(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	th, ok := m.activeThread()
	if !ok {
		t.Fatal("没有打开的会话")
	}
	var target thread.Header
	for _, msg := range th.Messages {
		if msg.From != m.cfg.Self() {
			target = msg
			break
		}
	}
	if target.MessageID == "" {
		t.Fatal("测试数据里没有对方发来的邮件")
	}
	m.bodies = map[string]app.Body{target.MessageID: {Text: "好"}}

	body := bubbleBody(m.renderMessage(target, 80))
	if len(body) == 0 {
		t.Fatal("对方这条消息没有气泡行")
	}
	// 量的是整行（含左沿那一格留白），所以它必定 ≥ 下限 + 留白。
	if w := lipgloss.Width(body[0]); w < minBubbleW {
		t.Errorf("单字消息的气泡只有 %d 列（下限 %d）—— 像一块补丁，不像气泡: %q",
			w, minBubbleW, plainText(body[0]))
	}
}

// 消息头里的时间用灰色，不用发件人的颜色。
//
// 名字和时间挤在同一个样式里的话，时间会和名字一样亮，一行里有两个重点
// —— 而重点太多等于没有重点。
func TestChat_HeadTimeIsMutedNotSenderColored(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}
	head := peer[0][0]

	timeSeq := bgSeqOf(styleTime)
	if timeSeq == "" {
		t.Fatal("拿不到时间样式的序列 —— 这个用例大概忘了 forceColor")
	}
	if !strings.Contains(head, timeSeq) {
		t.Errorf("消息头里的时间没套 styleTime（期望序列 %q）：%q", timeSeq, head)
	}
}
