package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守着「在 CLI 里把 clichat 消息和普通邮件分开」这件事的两半：
//
//   - 列表层：标记 + 「只看 clichat」过滤器（filterThreads / renderThreadList）；
//   - 会话内：消息头上的徽章 + 对方自己设的名字颜色。
//
// 为什么两处都要有：列表层回答「哪些是」，会话内回答「我正看着的这条
// 是不是」。只有列表标记的话，打开会话就分不清了；只有徽章的话，
// 得一条条打开才知道。

// ---- 列表层 ----

// clichat 会话带 @ 标记，普通会话不带；而且这个标记不能让名字错位。
//
// 第二条比第一条更值钱：标记是**两列宽**的，占位的那两个空格如果不是
// 恰好两列，列表里名字的起始列就会跟着有没有标记而变 —— 那一列歪掉
// 比没有标记更难看，而「有标记 / 没标记」这种断言一个都不会红。
func TestList_MarksChatThreads(t *testing.T) {
	m, _ := newMockModel(t)
	m.visible = []thread.Thread{
		mkChatThread("<c1>", "INBOX", "聊两句", "alice@example.com"),
		mkThread("<c2>", "INBOX", "系统通知", "noreply@shop.example.com"),
	}
	m.cursor = 0

	lines := strings.Split(m.renderThreadList(40, 10), "\n")

	// 先钉住这句话的前提：两个标记**都是两列**。前提不成立的话，
	// 下面那条对齐判据红得再对也没法解释（分不清是标记变宽了还是错位了）。
	if a, b := textWidth("@ "), textWidth("· "); a != b {
		t.Fatalf("两个标记的宽度不一样（@ 是 %d 列，· 是 %d 列）", a, b)
	}

	// 从**画出来的那一行里**找，不信声明的位置。
	//
	// ⚠️ 量的是**显示列**，不是 `strings.Index` 给的**字节下标**。标记那一列
	// 一边是 `@`（UTF-8 一字节）、一边是 `·`（两字节），字节下标会**天然差 1**
	// —— 那 1 来自编码长度，不是屏幕上错位。真按字节比，代码完全正确也会红。
	chatCol, plainCol := -1, -1
	var chatRow, plainRow string
	for _, line := range lines {
		p := plainText(line)
		if i := strings.Index(p, "alice"); i >= 0 {
			chatCol, chatRow = textWidth(p[:i]), p
		}
		if i := strings.Index(p, "noreply"); i >= 0 {
			plainCol, plainRow = textWidth(p[:i]), p
		}
	}
	if chatRow == "" || plainRow == "" {
		t.Fatalf("列表里没画出这两个会话:\n%s", strings.Join(lines, "\n"))
	}

	if !strings.Contains(chatRow, "@") {
		t.Errorf("clichat 会话没有 @ 标记: %q", chatRow)
	}
	if strings.Contains(plainRow, "@") {
		t.Errorf("普通会话被标成了 clichat: %q", plainRow)
	}

	// 名字的起始列必须一样 —— 这就是「标记占两列」那句话的判据。
	if chatCol != plainCol {
		t.Errorf("有没有 @ 标记会让名字错位：clichat 会话在第 %d 列，"+
			"普通会话在第 %d 列\n  %q\n  %q", chatCol, plainCol, chatRow, plainRow)
	}
}

// 按 c 打开「只看 clichat」，再按一次关掉。
//
// 判据钉的是**列表真的变了**，不是那个布尔值变了：过滤器写了但忘了
// 重算 visible 的话，布尔值是对的、屏幕上是错的。
func TestList_ChatFilterKeyToggles(t *testing.T) {
	m, _ := newMockModel(t)
	m.mode = modeList
	m.threads = []thread.Thread{
		mkChatThread("<c1>", "INBOX", "聊两句", "alice@example.com"),
		mkThread("<c2>", "INBOX", "系统通知", "noreply@shop.example.com"),
	}
	m.refreshVisible()
	if len(m.visible) != 2 {
		t.Fatalf("起步就是 %d 个会话，想要 2", len(m.visible))
	}

	// 光标先挪开，好验「过滤之后光标归零」：过滤之后原来的下标指向的
	// 是另一个会话，停在原地会让「我明明看着第二条」变成打开另一条。
	m.cursor = 1

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !m.chatOnly {
		t.Error("按 c 没有打开过滤")
	}
	if len(m.visible) != 1 || m.visible[0].ID != "<c1>" {
		t.Errorf("只看 clichat 之后还剩 %d 个会话（%v），想要只有 <c1>",
			len(m.visible), threadIDs(m.visible))
	}
	if m.cursor != 0 {
		t.Errorf("过滤之后光标停在 %d，没有归零", m.cursor)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.chatOnly {
		t.Error("再按一次 c 没有关掉过滤")
	}
	if len(m.visible) != 2 {
		t.Errorf("关掉过滤之后剩 %d 个会话，想要 2", len(m.visible))
	}
}

// 过滤开着的时候，列表标题必须写出来。
//
// 列表变短了却不说为什么，用户会以为邮件丢了 —— 这和空列表的提示是
// 同一件事的两面。
func TestList_HeaderShowsChatFilter(t *testing.T) {
	m, _ := newMockModel(t)
	m.visible = []thread.Thread{mkChatThread("<c1>", "INBOX", "聊两句", "alice@example.com")}

	off := plainText(listFirstLine(m, 60))
	m.chatOnly = true
	on := plainText(listFirstLine(m, 60))

	if strings.Contains(off, chatBadge) {
		t.Errorf("没开过滤，标题里却写着 clichat: %q", off)
	}
	if !strings.Contains(on, chatBadge) {
		t.Errorf("开了「只看 clichat」，标题里却没写: %q", on)
	}
}

// 一条 clichat 都没有时的提示：得告诉用户怎么回到全部邮件。
//
// 判据绑的是**事实**不是措辞：提示里让用户按的那个键，按下去必须真的
// 能看到别的会话。绑死文案（「按 c 看全部」这个字符串）会在文案改进的
// 那一轮变成假红，而假红和假绿一样贵。
func TestList_EmptyHintWithChatFilter(t *testing.T) {
	m, _ := newMockModel(t)
	m.mode = modeList
	m.chatOnly = true
	m.threads = []thread.Thread{
		mkThread("<c2>", "INBOX", "系统通知", "noreply@shop.example.com"),
	}
	m.refreshVisible()

	if len(m.visible) != 0 {
		t.Fatalf("夹具没造出「过滤之后是空的」这个前提，剩 %d 个", len(m.visible))
	}
	hint := m.emptyListHint()
	if !strings.Contains(hint, "c") {
		t.Errorf("空列表的提示里没告诉用户按哪个键回去: %q", hint)
	}
	if !strings.Contains(hint, chatBadge) {
		t.Errorf("空列表的提示里没说清是被什么滤掉的: %q", hint)
	}

	// 提示里那个键按下去，必须真的能看到别的会话。
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if len(m.visible) != 1 {
		t.Errorf("照提示按了 c 还是看不到会话（剩 %d 个）", len(m.visible))
	}
}

// ---- 会话内 ----

// clichat 消息头上有徽章，普通邮件没有。
func TestRenderMessage_ChatBadge(t *testing.T) {
	m, _ := newMockModel(t)

	chat := head("<p@x>", "alice@example.com", "Alice")
	chat.Clichat = &thread.ClichatMeta{Version: "1.2.3", Nick: "衡"}
	plain := head("<q@x>", "bob@example.com", "Bob")

	m.bodies = map[string]app.Body{
		"<p@x>": {Text: "正文"},
		"<q@x>": {Text: "正文"},
	}

	if got := m.renderMessage(chat, 80)[0]; !strings.Contains(got, chatBadge) {
		t.Errorf("clichat 消息头上没有徽章: %q", got)
	} else if got := m.renderMessage(plain, 80)[0]; strings.Contains(got, chatBadge) {
		t.Errorf("普通邮件的消息头被标成了 clichat: %q", got)
	}
}

// 显示名很长的时候，徽章不能被一起截掉。
//
// 和 HTML 标记那条同源（TestRenderMessage_TagSurvivesLongName）：显示名
// 是对方自己写的、长度没有上限，扣宽度时没给徽章留位置，名字一长它就
// 被挤出画面 —— 而长名字的邮件恰恰是最需要一眼分辨来源的那批。
func TestRenderMessage_ChatBadgeSurvivesLongName(t *testing.T) {
	m, _ := newMockModel(t)

	msg := head("<h@x>", "noreply@shop.example.com", strings.Repeat("名", 60))
	msg.Clichat = &thread.ClichatMeta{Version: "1.2.3"}
	m.bodies = map[string]app.Body{"<h@x>": {Text: "正文"}}

	got := m.renderMessage(msg, 80)[0]
	if !strings.Contains(got, chatBadge) {
		t.Errorf("名字很长时徽章被挤掉了: %q", got)
	}
	// 这一行已经上过色了，只能用懂 ANSI 的那把尺子量（styles.go 硬约束）。
	if w := lipgloss.Width(got); w > 80 {
		t.Errorf("消息头宽 %d，超过了可用宽度 80: %q", w, got)
	}
}

// 名字的颜色必须来自对方带来的 nameColor。
//
// 怎么区分「用了 meta 里的颜色」和「用了按地址挑的那套调色板」：
// **两个不同的地址配上同一个 nameColor，画出来必须一模一样**。
// 调色板是按地址算的，只要它还在起作用，这两个就必然不同。
//
// 「名字上有颜色」这种断言做不到这件事 —— 两种实现都绿。
func TestRenderMessage_NameColorComesFromMeta(t *testing.T) {
	forceColor(t)
	m, _ := newMockModel(t)

	nameLine := func(addr string, meta *thread.ClichatMeta) string {
		h := head("<p@x>", addr, "某人")
		h.Clichat = meta
		return m.renderMessage(h, 80)[0]
	}

	// 前提条件：这两个地址在**没有任何 meta** 时必须拿到不同的颜色。
	// 不先确认这一点，下面那句「相同」就说明不了任何事 —— 也可能只是
	// 这个调色板本来就把它们算成同一个色（假绿）。
	alicePlain := fgOf(nameLine("alice@example.com", nil))
	bobPlain := fgOf(nameLine("bob@example.com", nil))
	if alicePlain == "" || bobPlain == "" {
		t.Fatalf("夹具前提不成立：不给 meta 时名字上根本没有前景色（%q / %q）—— "+
			"那这条判据区分不了两种实现", alicePlain, bobPlain)
	}
	if alicePlain == bobPlain {
		t.Fatalf("夹具前提不成立：这两个地址在默认调色板下就是同一个颜色（%q）—— "+
			"换两个地址再试", alicePlain)
	}

	meta := &thread.ClichatMeta{Version: "1.0", NameColor: "#ff0000"}
	alice := fgOf(nameLine("alice@example.com", meta))
	bob := fgOf(nameLine("bob@example.com", meta))
	if alice == "" {
		t.Fatal("带了 nameColor 却没有前景色 —— 颜色压根没生效")
	}
	if alice != bob {
		t.Errorf("同一个 nameColor 在两个地址上画出了两种颜色：%q / %q —— "+
			"说明用的是按地址挑的调色板，meta 里的颜色没生效", alice, bob)
	}

	// 换个颜色必须跟着变，否则上面那句「相同」可能只是画了个固定色。
	other := fgOf(nameLine("alice@example.com", &thread.ClichatMeta{
		Version: "1.0", NameColor: "#0000ff",
	}))
	if other == alice {
		t.Errorf("换了 nameColor 颜色却没变（都是 %q）—— 它没在起作用", alice)
	}
}

// #rrggbbaa 的后两位要在交给终端之前丢掉。
//
// 终端前景色没有透明度这个概念，原样传下去 termenv 解不出来，结果是
// 「对方设了颜色却什么都没发生」—— 这个症状很难让人联想到是 alpha。
// 判据是**行为**的：带不带 alpha 必须画出同一个颜色。
func TestRenderMessage_NameColorDropsAlpha(t *testing.T) {
	forceColor(t)
	m, _ := newMockModel(t)

	nameLine := func(color string) string {
		h := head("<p@x>", "alice@example.com", "某人")
		h.Clichat = &thread.ClichatMeta{Version: "1.0", NameColor: color}
		return m.renderMessage(h, 80)[0]
	}

	six := fgOf(nameLine("#ff8800"))
	eight := fgOf(nameLine("#ff8800ff"))
	if six == "" {
		t.Fatal("六位写法没画出前景色 —— 这条判据没测到颜色")
	}
	if six != eight {
		t.Errorf("八位写法没被正确处理：%q 画成 %q，#ff8800 画成 %q",
			"#ff8800ff", eight, six)
	}
}

// ---- 说明与提示 ----

// 列表里的 @ 和消息头上的 clichat，帮助页里必须查得到。
//
// 这两个符号是用户仅有的线索：@ 只在列表里出现，附近没有一个字说明
// 它是什么意思。不写进帮助页，用户只能猜。
//
// 按**结构**查（帮助页里有一条 label 为 @、说明里提到 clichat 的条目），
// 不去大段文本里 Contains —— 那样碰巧命中别处的可能性太高。
func TestHelp_ExplainsChatMarkers(t *testing.T) {
	for _, label := range []string{"@", chatBadge} {
		found := false
		for _, s := range helpSections {
			for _, it := range s.items {
				if it.label == label && strings.Contains(it.desc, "clichat") {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("帮助页里没有 %q 这一条（说明里要提到 clichat）—— "+
				"用户看到这个符号只能猜", label)
		}
	}
}

// c（只看 clichat）必须出现在底部提示里，并且在常见宽度下活过截断。
//
// 抄的是 a 那条判据的理由：功能写好了但用户找不到，等于没做。
// 只在 40 列以下被截掉不算故障 —— 那种宽度下 a（接收全部邮件）更要紧，
// 而它同样只有一行可用（见 hintKeys 里的顺序说明）。
func TestListHints_ShowsChatFilterKey(t *testing.T) {
	full := listHints()
	if !strings.Contains(full, "c "+chatBadge) {
		t.Fatalf("底部提示里没有 c（只看 clichat）：\n%s", full)
	}
	got := truncate(full, 60)
	if !strings.Contains(got, "c "+chatBadge) {
		t.Errorf("60 列下看不到 c（只看 clichat），用户会以为这个功能不存在：\n%q", got)
	}
}

// ---- 小工具 ----

func threadIDs(list []thread.Thread) []string {
	out := make([]string, 0, len(list))
	for _, th := range list {
		out = append(out, th.ID)
	}
	return out
}

// listFirstLine 是列表栏画出来的第一行（标题行）。
func listFirstLine(m Model, width int) string {
	return strings.SplitN(m.renderThreadList(width, 10), "\n", 2)[0]
}

// fgOf 取出一行里第一个前景色参数的**完整参数串**（比如 38;5;196）。
//
// 用它比「两处用的颜色一样不一样」，而不是比具体色号：色号会随终端
// 色深被降级（ANSI256 下 #ff0000 变成某个索引），把色号钉进判据等于把
// termenv 的降级表也一起钉住 —— 它一升级判据就假红。
//
// ⚠️ 不能写 strings.Contains(s, "38")：色号 138 / 238 里都有 "38"，
// 那是**假绿**；而逐字节比 "\x1b[38;5;196m" 又会在加粗合并成
// "\x1b[1;38;5;196m" 时**假红**。要按参数分段比。
func fgOf(s string) string {
	for i := 0; i+2 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
			j++
		}
		if j >= len(s) || s[j] != 'm' {
			continue
		}
		params := strings.Split(s[i+2:j], ";")
		for k, p := range params {
			if p != "38" {
				continue
			}
			switch {
			case k+2 < len(params) && params[k+1] == "5": // 38;5;N
				return strings.Join(params[k:k+3], ";")
			case k+4 < len(params) && params[k+1] == "2": // 38;2;R;G;B
				return strings.Join(params[k:k+5], ";")
			default:
				return strings.Join(params[k:], ";")
			}
		}
		i = j
	}
	return ""
}
