package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守着「对方说的话在版面上靠什么和别的东西分开」。
//
// 三轮下来这块版面上只剩一条规矩：**对方的正文左边有一条竖条，自己的没有。**
// 前面两版守的是别的东西，都推翻了，值得记下来为什么：
//
//  1. 第一版头和正文同铺一条灰带 —— 「谁在说」也被圈进去了。头是**标注**，
//     不是内容，它该在外面。
//  2. 第二版把正文围进一个贴合内容的底色块（气泡）。宽度那套账很难算对
//     （四分之三上限、最小宽度、按显示宽度而不是字节量……），但真正的问题
//     不在实现：**一条消息一块底色，一屏里就有十几块亮度不同的矩形在互相
//     切割**，而长消息的块宽、短消息的块窄，右边缘参差得像一屏打乱的表格。
//     用户的原话是「不要用背景色块来区分聊天，割裂感太强！可以用 sideline
//     这种」。
//
// 换成竖条之后判据的落点整体变了：宽度**不再需要"算"**（引用块就是「一条
// 竖条 + 它右边的字」，字多长块就多长），于是上一版那几条「气泡贴合内容」
// 「同一气泡内各行等宽」「最小气泡宽度」全部作废 —— 反过来，「右边不补白」
// 和「每一行都带那条竖条」成了新的契约。下面每一条都注明了它替代了什么。

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

// firstPeerHeader 取出当前会话里第一条**对方发来的**消息。
func firstPeerHeader(t *testing.T, m Model) thread.Header {
	t.Helper()
	th, ok := m.activeThread()
	if !ok {
		t.Fatal("没有打开的会话")
	}
	for _, msg := range th.Messages {
		if msg.From != m.cfg.Self() {
			return msg
		}
	}
	t.Fatal("测试数据里没有对方发来的邮件")
	return thread.Header{}
}

// peerBody 从一条消息的那些行里挑出**引用正文**（带竖条的行）。
//
// 上一版这里看的是 `\x1b[48;`（有没有底色）。竖条方案下对方正文一行底色
// 都没有，那个尺子会把整条消息判成「没有正文」。
//
// 之所以不能简单地取 rows[1:]：消息**头**在引用块外面，而末行是 renderMessage
// 补的那个空行分隔符。两头都得让开，剩下的才是正文。
func peerBody(rows []string) []string {
	var out []string
	for _, r := range rows {
		if strings.Contains(r, sidelineMark) {
			out = append(out, r)
		}
	}
	return out
}

// 引用块的左侧几何：第 0 列留白、第 paneInset 列是那条竖条。
//
// 这两条合起来是「引用块浮在版面上」的唯一表达：竖条不贴栏边，右边不铺底。
func TestChat_SidelineLeavesThePaneEdgeBlank(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}
	body := peerBody(peer[0])
	if len(body) == 0 {
		t.Fatalf("对方这条消息一行引用正文都没有：%q", peer[0])
	}

	// 竖条画在 paneInset 那一列 —— 和列表栏的选中块两侧内缩同一个道理：
	// 贴在栏边上的竖条读起来是"这里有条边界"，内缩一格才是"这一段话"。
	if got := plainText(ansiSlice(body[0], paneInset, paneInset+1)); got != sidelineMark {
		t.Errorf("第 %d 列该是那条引用竖条 %q，实际是 %q：%q",
			paneInset, sidelineMark, got, body[0])
	}
	// 竖条左边那一格必须留给终端背景。
	if got := plainText(ansiSlice(body[0], 0, paneInset)); strings.TrimSpace(got) != "" {
		t.Errorf("竖条左边那 %d 列不是空白（%q）—— 引用块贴上栏边就不「浮」了: %q",
			paneInset, got, body[0])
	}
}

// 消息头**在引用块外面**。
//
// 头是**标注**（谁、什么时候），不是**内容**（这个人说了什么）。头也被划进
// 竖条里的话，「谁在说」和「说了什么」就混成一段，而引用块的意义正是把
// "这个人说的那一段话"圈出来。
func TestChat_PeerHeadSitsOutsideTheQuote(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	if len(peer) == 0 {
		t.Fatal("测试数据里没有对方发来的邮件")
	}
	rows := peer[0]
	if len(rows) < 3 {
		t.Fatalf("这条消息只有 %d 行，看不出头和正文的分界：%q", len(rows), rows)
	}

	if strings.Contains(rows[0], sidelineMark) {
		t.Errorf("对方的消息头带着那条竖条 —— 它该在引用块外面: %q", rows[0])
	}
	if !strings.Contains(rows[1], sidelineMark) {
		t.Errorf("消息头下面那一行不是引用正文：%q", rows)
	}
}

// 引用正文**不补白**。
//
// 上一版必须补：底色块要方，每行不补到同宽就成了参差不齐的色带。现在没有
// 块了，补白只会给每一行攒下几十个看不见的尾随空格（复制、选中都会带上），
// 而画面上没有任何东西因此变化。
//
// 判据把每一行的**尾随空格去掉再量一遍**：两个宽度相等，就说明这一行
// 一个多余的列都没有。它替代的是上一版的 TestChat_PeerBubbleHugsItsContent
// （那条守的是「气泡比整栏窄」）—— 对竖条方案来说"比整栏窄"太弱了：补白
// 到整栏和补白到半栏一样错，而两者都能满足"比整栏窄"。
//
// ⚠️ 第一版这条判据是**空转**的（变异验证抓到的）：它拿
// `quoteIndent + 可见文字宽度` 去比整行宽，而"可见文字"里连补出来的空格
// 一起算了进去，于是任何补白都成立。量补白就必须先把补白摘掉。
func TestChat_PeerBodyIsNotPaddedToFullWidth(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	peer, _ := peerAndOwnRows(t, m, 80)
	body := peerBody(peer[0])
	if len(body) == 0 {
		t.Fatalf("对方这条消息一行引用正文都没有：%q", peer[0])
	}

	const quoteIndent = paneInset + 1 + quoteGap // 留白 + 竖条 + 一格间隔
	checked := 0
	for _, row := range body {
		vis := plainText(row)
		trimmed := strings.TrimRight(vis, " ")
		// 段落之间那个空行整行只有「留白 + 竖条 + 间隔」，末尾那一格是
		// 间隔不是补白 —— 这一行量不了，跳过（但下面要确认不是全跳过了）。
		if len([]rune(trimmed)) <= quoteIndent {
			continue
		}
		checked++
		if got, want := lipgloss.Width(row), lipgloss.Width(trimmed); got != want {
			t.Errorf("引用正文行宽 %d 列、可见文字只占 %d 列 —— 右边多出 %d 格看不见的补白: %q",
				got, want, got-want, row)
		}
	}
	if checked == 0 {
		t.Errorf("没有一行量得了（正文全是空行？）—— 这条判据没测到东西：%q", body)
	}
}

// 正文里的**空行**也带竖条。
//
// 这一条看着像吹毛求疵，其实守的是"两段话"和"两段引用"的分界：竖条断在
// 段落之间的话，那两段话在余光里就成了两块各自独立的引用，而它们本来是
// 同一个人一口气说完的。
//
// 它替代的是上一版的 TestChat_BubbleRowsShareOneWidth（那条用"同一气泡内
// 各行等宽"表达"这是一个框"）。竖条方案下"框"不存在了，但"这一段到哪儿
// 为止"仍然必须一眼看得出来 —— 换成从正面钉"中间不许断"。
func TestChat_PeerQuoteHasNoGaps(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)
	target := firstPeerHeader(t, m)

	// 一段一定要产生空行的正文：段落之间那个空行正是要量的地方。
	m.bodies = map[string]app.Body{
		target.MessageID: {Text: "第一段说完了。\n\n第二段接着说。"},
	}

	rows := m.renderMessage(target, 80)
	if len(rows) < 4 {
		t.Fatalf("正文只渲染出 %d 行，中间不可能有空行，量不了："+
			"（renderMarkdown 是不是不吐段落空行了？）%q", len(rows), rows)
	}
	// 首行是消息头、末行是 renderMessage 补的分隔空行，中间全都属于引用块。
	for i, row := range rows[1 : len(rows)-1] {
		if !strings.Contains(row, sidelineMark) {
			t.Errorf("引用块第 %d 行断了竖条 —— 段落之间那一段必须连着: %q", i+1, row)
		}
	}
}

// 自己发的行不能有竖条。
//
// 这一条划的是「只对方」的边界：两边都画，就等于两边都没画 —— 那正是上一版
// 「两边都铺底色」的翻版错误。
func TestChat_OwnRowsHaveNoSideline(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	_, own := peerAndOwnRows(t, m, 80)
	if len(own) == 0 {
		t.Fatal("测试数据里没有自己发出的邮件")
	}

	for _, rows := range own {
		for _, row := range rows {
			if strings.Contains(row, sidelineMark) {
				t.Errorf("自己发的行不该有竖条: %q", row)
			}
		}
	}
}

// 用真实模型走一遍：聊天视图里既要有**带竖条**的行（对方），也要有不带的
// 行（自己 / 信息行）—— 防止哪天上面那几条判据在 renderMessage 上绿着，
// 视图却另有一条路。
//
// ⚠️ 量的是**会话流那一栏**，不是整幅 View。整幅里每一行的最左边还压着
// 列表栏和那条分隔竖线，而分隔竖线本身也是个 Box Drawing 区的字符 ——
// 「这一行有没有竖条」在那里根本分不出来，判据会退化成一条永远为真的空壳。
func TestChat_ViewHasBothQuotedAndPlainRows(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)

	pane := m.renderChat(m.chatPaneWidth(), m.bodyHeight())
	var quoted, plain []string
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, sidelineMark) {
			quoted = append(quoted, line)
			continue
		}
		if strings.TrimSpace(plainText(line)) != "" {
			plain = append(plain, line)
		}
	}

	if len(quoted) == 0 {
		t.Error("聊天视图里没有一条带竖条的行（对方的话）")
	}
	if len(plain) == 0 {
		t.Error("聊天视图里没有一条不带竖条的行（自己的话 / 信息行）")
	}

	// 光量那一栏还不够：整幅画面完全可能不用它（以前就是各自画各自的）。
	if len(quoted) > 0 && !strings.Contains(m.View(), quoted[0]) {
		t.Errorf("会话流那一栏的渲染结果没进整幅画面：%q", quoted[0])
	}
}

// 窄终端下正文也要收得住 —— 不能因为"四分之三栏宽"算出来比剩下的宽度
// 还大，就把行撑破。
//
// 撑破的表现是终端自己折行，于是下面的行号全错位、鼠标全点错。这类
// 错误在真实终端里比在测试里难看出得多，所以在这里挡住。
//
// ⚠️ 宽度从 **2 列**起量、而且中间不留空档（5/6/7 这几档最容易被漏），不是
// 从"看起来还算正常"的 8 列起。8 列时这组判据全绿过，而更窄的几档当时是错的：
//
//   - 头部按「整栏宽」算预算，对方那一行的前面还垫着一格留白 —— **12 列**
//     时头部就撑到 13 列。
//   - 正文先按理想宽度折行、再被硬上限压窄，正文不跟着变，**8 列**时
//     正文里的内容比留给它的列数还宽。
//   - 「至少要 N 列才画得出来」的那个 N 漏掉了**一个最宽的字符**（折行的
//     硬断点一次推进一个字符，而一个汉字占 2 列），**4 列**下多出一列。
//
// 三处都只在特定宽度下露出来，所以判据在**所有宽度**上走一遍。
//
// ⚠️ 两个夹具都要走：纯文本那一份量不出「按字节算宽度」这类错
// （见 TestChat_PeerBodyWrapsWithinThreeQuarters）—— 带超链接和表格的那份
// 才是它的回归面。
func TestChat_BodyNeverOverflowsNarrowPane(t *testing.T) {
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
		for _, width := range []int{2, 3, 4, 5, 6, 7, 8, 12, 20, 40, 80} {
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

// 栏宽连「留白 + 竖条 + 间隔 + 一个最宽的字符」都放不下时，**不要画竖条**，
// 退回按栏宽折过的纯正文。
//
// 硬撑着画的下场是每一行的头三列被竖条吃掉，正文只剩一两个字符 —— 与其
// 摆一条把内容挤没的装饰，不如不要它。
//
// 它替代的是上一版的 TestChat_ShortBubbleStillLooksLikeABubble（那条守
// minBubbleW 这个常量，常量已经随气泡一起退休了）。新的契约同样钉在"窄到
// 某个程度就换形态"，但换的是形态而不是宽度：不画竖条、也不画消息头
// （4 列宽的名字只剩「Al…」，信息量还不如让给正文）。
func TestChat_NarrowPaneDropsTheSideline(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)
	target := firstPeerHeader(t, m)

	// 门槛是算出来的：quoteIndent（1+1+1）+ maxCellW（一个汉字 2 列）。
	limit := (paneInset + 1 + quoteGap) + maxCellW

	for _, width := range []int{2, 3, 4} {
		if width >= limit {
			t.Fatalf("夹具坏了：宽 %d 不该比门槛 %d 还大", width, limit)
		}
		var shown string
		for i, row := range m.renderMessage(target, width) {
			if strings.Contains(row, sidelineMark) {
				t.Errorf("宽 %d 时第 %d 行还画着竖条 —— 这一档该退化成纯正文: %q",
					width, i, row)
			}
			if w := lipgloss.Width(row); w > width {
				t.Errorf("宽 %d 时第 %d 行宽 %d 列，撑破了: %q", width, i, w, row)
			}
			shown += plainText(row)
		}
		// 退化不等于把内容丢光 —— 那三条判据（不画竖条 / 不撑破）在
		// 「什么都不画」的实现上全都是绿的。
		if !strings.Contains(shown, "第") {
			t.Errorf("宽 %d 时正文一个字都没画出来: %q", width, shown)
		}
	}
}

// 正文宽度按**显示宽度**算，不是按字节算。
//
// 正文里可能有 OSC 8 超链接（`\x1b]8;;url\x1b\\文字\x1b]8;;\x1b\\`），
// 一段 39 列可见的行，字节数能到 80 出头。用逐 rune 求和的 textWidth 去量
// 它，量到的是「转义序列有多长」，行会跟着涨到整栏都装不下。
//
// 这条**真的坏过**：带链接的那封 HTML 邮件让气泡撑到 105 列（栏宽才 61），
// 而当时整套判据都是绿的 —— 因为唯一被量过的夹具里全是纯文本。
//
// 它同时守着上一版的 TestChat_BubbleRowsShareOneWidth 留下的那个洞的残余：
// 「每行都一样宽」在竖条方案下没有意义了，但「折行宽度必须按显示宽度算」
// 这条仍然要靠带链接的夹具才量得出来。
func TestChat_PeerBodyWrapsWithinThreeQuarters(t *testing.T) {
	forceColor(t)
	m, _ := openChat(t)
	target := firstPeerHeader(t, m)

	const width = 80
	// 上限：四分之三栏宽，和 renderMessage 里的算法同源。
	cap := width * 3 / 4

	// ⚠️ 第二段是**刻意的长**：正文短于上限的话，"没有上限"的实现也会绿，
	// 这条判据就成了空壳（第一版的夹具正是这样，变异验证抓到的）。
	long := strings.Repeat("这是一句足够长的正文，", 8) // 72 列，比上限还宽
	m.bodies = map[string]app.Body{
		target.MessageID: {Text: "会议链接：[meeting.example.com/abc-def-ghi](https://meeting.example.com/abc-def-ghi)\n\n" + long},
	}

	rows := m.renderMessage(target, width)
	body := peerBody(rows)
	if len(body) == 0 {
		t.Fatalf("对方这条消息没有引用正文：%q", rows)
	}
	if len(body) < 3 {
		t.Fatalf("正文只折成 %d 行，长段落那段没走到 —— 量不出上限还在不在：%q", len(body), rows)
	}

	// 先确认链接**真的画出来了**：一段被整段截没的正文，下面那条宽度
	// 断言会在一行空字符串上绿过去。
	var shown string
	for _, row := range body {
		shown += plainText(row)
	}
	if !strings.Contains(shown, "meeting.example.com") {
		t.Fatalf("链接文字没画出来，量不出「按字节还是按显示宽度」: %q", shown)
	}

	for i, row := range body {
		if w := lipgloss.Width(row); w > cap {
			t.Errorf("引用正文第 %d 行宽 %d 列，超过四分之三栏宽 %d —— "+
				"折行宽度没按上限收住（或者量的是字节而不是显示宽度）: %q",
				i, w, cap, plainText(row))
		}
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

