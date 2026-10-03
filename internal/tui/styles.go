package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 终端宽度低于这个值就从两栏降级成单栏。
const singlePaneWidth = 80

var (
	styleTitle    = lipgloss.NewStyle().Bold(true)
	styleMuted    = lipgloss.NewStyle().Faint(true)
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleUnread   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	stylePrompt   = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	styleMine     = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleLink     = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	// styleMDRule 是正文里分隔线（---）被渲染成的那条横线。
	styleMDRule = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	// styleTag 是消息头上那个「HTML」小标记。
	//
	// 用灰色而不是亮色：它是一条**只读说明**（这条正文是转出来的），
	// 不是状态、更不是警告，亮起来会跟发件人名字抢注意力。灰色却仍然
	// 看得见，足够了。
	styleTag = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	// styleChatTag 是消息头上那个「clichat」标记。
	//
	// 和 styleTag 同为只读说明，但不共用颜色：这两个标记回答的是两件
	// 不同的事（「排版被重排过」vs「对面也是这个客户端」），一眼要能分开。
	// 用 141 而不是更亮的色 —— 名字本身已经按发件人上色，标记再抢就乱了。
	styleChatTag = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))

	// styleScrollBar / styleScrollThumb 是会话正文右侧那条滚动条。
	//
	// 轨道用低调的灰、滑块用亮一些的灰：它是**背景信息**，告诉用户
	// 「这里还有内容、能滚」，不该比正文本身更抢眼。
	//
	// 深浅自适应：轨道在浅色终端上要用浅灰（250），在深色终端上要用
	// 深灰（238），写死一个在这边会糊掉。
	styleScrollBar   = lipgloss.NewStyle().Foreground(scrollBarFg)
	styleScrollThumb = lipgloss.NewStyle().Foreground(scrollThumbFg)
)

// scrollBarFg / scrollThumbFg 是滚动条轨道与滑块的前景色。
//
// 轨道贴着背景（浅底给浅灰、深底给深灰），滑块反着来，这样一条细线在
// 两种终端上都能看出「有」。
var (
	scrollBarFg   = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	scrollThumbFg = lipgloss.AdaptiveColor{Light: "243", Dark: "250"}
)

// hyperlink 把文本包成 OSC 8 终端超链接。
//
// 支持的终端（iTerm2 / WezTerm / kitty / Windows Terminal / VS Code 内置
// 终端等）里可以直接点击打开；不支持的终端会忽略这段转义序列，只显示文本
// 本身，所以退化是安全的。
//
// ⚠️ 有一条硬约束：**包裹的样式不能带 Underline。**
//
// 这是踩了一整轮才挖出来的。现象是链接「看着完全正常，但点不动」：
//
//   - 给 styleLink 加上 .Underline(true) 之后，URL 的 **每一个字符** 都被
//     单独套一串 SGR，抓原始字节能看到
//     \x1b[4;38;5;45;4mh\x1b[0m\x1b[4;38;5;45;4mt\x1b[0m\x1b[4;... 这样。
//   - 于是 \x1b]8;; 后面紧跟的是 \x1b[4;...，OSC 8 被当场截断，
//     终端根本认不出这是个链接。
//   - 去掉下划线后，URL 变回一整段 \x1b[38;5;45mhttps://...\x1b[0m，
//     OSC 8 完整存活。顺带整个画面的字节数从 2292 降到 1090。
//
// 机制在 2026-10-02 又测了一遍，纠正了原先的一处误判：逐字符 SGR 是
// **lipgloss 的 Style.Render 自己干的**，纯 Go 层、跟 bubbletea 无关 ——
// 连不裹 OSC 8 的纯文本 styleLink.Underline(true).Render("点我") 也是
// 每字一串。所以它**能**在 Go 层单测里抓到：TestStyleLink_MustNotCarryAttributes
// 就抓得住，markdown_test.go 里那两条链接判据也抓得住。
//
// 顺带纠正另一处过度概括：元凶只有 Underline 一个。实测 Bold / Italic /
// Reverse 都不会触发逐字符输出，转义序列完整（\x1b[1;38;5;45m…）。但既然
// 不差这点视觉，规矩就取最简的一条：链接的样式只带前景色。
func hyperlink(url, text string) string {
	if url == "" {
		return text
	}
	// 结束序列不能省 —— 省了的话它后面所有文字都会被当成同一个链接。
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// senderPalette 是群聊里区分不同发言人的颜色。
//
// 挑的是在中深色终端背景上都能看清的中间调，避开接近黑和接近白的两端。
var senderPalette = []lipgloss.Color{
	"42", "45", "51", "78", "87", "114", "141", "177", "213", "220",
}

// senderStyle 按地址稳定地挑一个颜色。
//
// 「稳定」是重点：同一个人在这次会话里和下次启动时必须是同一个颜色，
// 否则群聊里一眼认人会失效。
func senderStyle(addr string) lipgloss.Style {
	h := fnv.New32a()
	_, _ = h.Write([]byte(addr))
	return lipgloss.NewStyle().Foreground(senderPalette[int(h.Sum32())%len(senderPalette)])
}

// chatForeground 把对方随信带来的颜色翻译成终端能画的前景色。
//
// 传 nil（这封不是 clichat 发的）或对方没给颜色，都返回空串，调用方
// 退回默认配色。
//
// ⚠️ 合法性**不在这里判** —— 收信那侧已经过了一遍 mail.cleanChatColor，
// 到这里的值一定是 #rgb / #rrggbb / #rrggbbaa 之一。这里只负责翻译：
// 终端的前景色没有透明度这个概念，所以八位写法要丢掉后两位，否则
// termenv 解析不出来，结果是「对方设了颜色却什么都没发生」。
//
// 刻意**不**校验对比度：不知道对方终端的底色，也不知道「我」这边是亮底
// 还是暗底，没有能算的基准（判成「太暗就不显示」反而会把亮底终端上
// 正常的深色名字误杀）。代价是对方能把名字设成和背景同色 —— 但那只让
// 人认不出是谁，正文和时间戳还在，骗不了人去做别的。
func chatForeground(m *thread.ClichatMeta) lipgloss.Color {
	if m == nil {
		return ""
	}
	s := m.NameColor
	switch len(s) {
	case 4, 7: // #rgb / #rrggbb，直接能用
	case 9: // #rrggbbaa，丢掉 alpha
		s = s[:7]
	default:
		return ""
	}
	return lipgloss.Color(s)
}

// chatNameStyle 是渲染一条消息的发送者名字要用的样式。
//
// 优先级：对方带来的 nameColor > 自己的固定色 / 按地址稳定挑的色。
//
// nameColor 排最前，因为它的用途就是这个 —— 它跟着 meta 传过来，为的
// 就是让「我是谁」在别人的屏幕上长得一样。自己发出去的消息也用它：
// 那才是对方看到的样子，两边不一致的话，用户会以为颜色没生效。
func chatNameStyle(msg thread.Header, mine bool) lipgloss.Style {
	if c := chatForeground(msg.Clichat); c != "" {
		// 自己的名字保持加粗：对齐方向 + 加粗是「这是我说的」的主要线索，
		// 而颜色是对方挑的，不能假定它一定比对方的名字显眼。
		return lipgloss.NewStyle().Foreground(c).Bold(mine)
	}
	if mine {
		return styleMine
	}
	return senderStyle(msg.From)
}

// cellWidth 是一个字符在终端里占的列数（纯文本口径）。
//
// 关键在于**不能用 runewidth.RuneWidth 的默认条件**：那套条件会看当前
// locale —— 本机是中文 locale，于是 • —— … ① 这些 East Asian Ambiguous
// 字符被判成 2 列，而 lipgloss.Width（也就是 padLeft / padRight 用的那把
// 尺子，走 x/ansi 的字素簇算法）判成 1 列。两把尺子在同一台机器上给出不同
// 答案，折行就会多算一格，而且**同一份代码在不同 locale 的机器上结论不同**。
//
// 实测除 Ambiguous 之外两者完全一致（ASCII 1、CJK 2、emoji 2、
// 组合符/制表符/控制符 0），所以这里只把 Ambiguous 钉成 1 列。
// 「textWidth 对纯文本必须等于 lipgloss.Width」这条有测试守着：
// TestTextWidthMatchesLipglossOnPlainText。
func cellWidth(r rune) int {
	if runewidth.IsAmbiguousWidth(r) {
		return 1
	}
	return runewidth.RuneWidth(r)
}

// textWidth 是 cellWidth 的整串版本。
//
// 按 rune 求和，不做字素簇归并 —— 于是 ZWJ 连字（👨‍👩‍👧 这类）会被算得比
// lipgloss.Width 宽，也就是「宁可早折一行」，不会折出超过给定宽度的行。
func textWidth(s string) int {
	w := 0
	for _, r := range s {
		w += cellWidth(r)
	}
	return w
}

// truncate 按**显示宽度**截断字符串。
//
// 不能用 len() 或 []rune 的长度：中文一个字占两列，按 rune 数截断
// 会让中文行比英文行宽一倍，右边的边框就歪了。
//
// 这里逐 rune 用 cellWidth 量、而不是整串丢给 lipgloss.Width：截断要知道
// 「切在哪一个字符之前」，只有逐字符的宽度才答得上来。
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if textWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := cellWidth(r)
		if w+rw > width-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	// 省略号自己只占 1 列（cellWidth 口径），所以整串宽度不会超过 width。
	return b.String() + "…"
}

// 注：这里原本还有个 wrapLines（把纯文本按显示宽度折行）。正文改用
// markdown.go 的 renderMarkdown 之后就没人调它了 —— 而它按 rune 逐个算宽度、
// 不认识 ANSI，喂带样式的字符串会把折行位置算错。留着这个名字跟 wrapLine
// 只差一个字母的函数，迟早有人拿它去折带颜色的正文。故删除。

// padRight 用空格把字符串补齐到指定显示宽度。
//
// 用 lipgloss.Width 而不是 runewidth.StringWidth：后者既会把 ANSI 转义
// 序列也算进宽度（带样式的行会被补歪，右边的竖线跟着歪），又会看 locale
// 把 East Asian Ambiguous 字符多算一列 —— 详见 cellWidth 的注释。
func padRight(s string, width int) string {
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// padLeft 用空格把字符串左侧补齐到指定显示宽度（右对齐用）。
func padLeft(s string, width int) string {
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return strings.Repeat(" ", gap) + s
}

// shortErr 把错误压成一行，方便塞进状态栏。
func shortErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	return truncate(s, 60)
}
