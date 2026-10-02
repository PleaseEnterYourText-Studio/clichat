package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// 终端宽度低于这个值就从两栏降级成单栏。
//
// 从 80 提到 96，是因为双栏现在多了一列导航（navWidth）。80 列时硬塞
// 导航 + 列表会只剩 30 来列给正文，邮件正文在那个宽度下折得没法读 ——
// 与其挤，不如老实降级。
const singlePaneWidth = 96

// navWidth 是左侧导航列的宽度。
//
// 12 列 = 两格缩进 + 最多 8 列的标签 + 数字计数。8 列刚好放下
// 「已发送」（3 个汉字 = 6 列）、「垃圾邮件」（4 个汉字 = 8 列）这类
// 最常见的文件夹名；再长的会被截断成「病毒文件…」，那没关系 ——
// 服务端文件夹那一组里通常只有一两个长名字。
//
// 再宽就是浪费：它只是一个文件夹切换器，每多一列都是从正文身上拿的。
const navWidth = 12

// navGutter 是侧栏右边那一格空隙。
//
// 它不铺任何底色 —— 这**正是它的作用**：整屏没有一条竖着贯通的硬线，
// 「侧栏到哪儿结束」全靠这一格留白。参照的设计里那条缝也是留白，只是
// 浏览器里能做得更宽；终端里一格已经够，再宽就是从正文身上拿。
const navGutter = 1

// inputBlockHeight 是浮起输入区占的行数：上下各留一行空白，中间一行
// 是输入行。
//
// 那两行留白不是浪费，是**这个方案成立的前提**：字符网格里没有圆角、
// 没有阴影、没有 z 轴，"浮起"只能靠「被空白包围的独立色块」来表达。
// 去掉留白，输入行就退回成又一条贴底的横带——也就是现在这样。
const inputBlockHeight = 3

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

	// ---- 分层用的底色 ----
	//
	// 这一组是「用底色代替线条」那套方案的落点。参照的设计里整屏没有
	// 一条分割线，层次全靠底的深浅 —— 终端里同样能做，只是要克制：
	// 底色只用来区分**区域**，不参与强调。
	//
	// 每一对都贴着各自的终端背景走（深色终端用比背景亮一两档的灰，
	// 浅色终端用比背景暗一两档的灰），因为写死一个灰必然在另一种
	// 终端上糊掉。

	// styleNav      是导航列的底色，让它在最左边自成一条。
	styleNav = lipgloss.NewStyle().Background(navBg)
	// styleNavActive / styleNavIdle 是导航项的两态。
	//
	// 选中态用的是和列表、标签页**同一块**底色（rowSelectedBg）：整个界面
	// 里「选中」只有这一种表达方式，用户学会一次就够了。加上加粗和亮色，
	// 是因为导航列的底色本身已经在参与分层，单靠一块底色不够醒目。
	styleNavActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("141")).Background(rowSelectedBg)
	styleNavIdle = lipgloss.NewStyle().Faint(true)

	// styleRowSelected 是会话列表里选中项那条底色块。
	//
	// 替代了原先的整行反白（styleSelected）。反白是终端里最大的对比度，
	// 一行反白会在视野里炸开；底色块只是「比周围亮一档」，选中位置照样
	// 一眼看得见，但不会把整个列表压下去。参照的设计用的就是这种。
	//
	// 导航项的选中态和顶部标签页的当前页用的也是这一块 —— 见 styleNavActive。
	styleRowSelected = lipgloss.NewStyle().Background(rowSelectedBg).Bold(true)
	// styleGroupTitle 是列表里「未读 / 已读」这类分组小标题。
	styleGroupTitle = lipgloss.NewStyle().Faint(true)

	// styleInputRow 是浮起输入行的底色。
	styleInputRow = lipgloss.NewStyle().Background(inputRowBg)

	// styleTabActive / styleTabIdle 是顶部会话标签页的两态。
	//
	// 当前那一页铺底色块（和列表选中同一块），其余淡着 —— 标签页是一排
	// 并列的东西，靠底色指明「你在哪一页」比靠颜色更稳。
	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("141")).Background(rowSelectedBg)
	styleTabIdle = lipgloss.NewStyle().Faint(true)

	// styleScrollBar / styleScrollThumb 是会话正文右侧那条滚动条。
	//
	// 轨道用低调的灰、滑块用亮一些的灰：它是**背景信息**，告诉用户
	// 「这里还有内容、能滚」，不该比正文本身更抢眼。
	styleScrollBar   = lipgloss.NewStyle().Foreground(scrollBarFg)
	styleScrollThumb = lipgloss.NewStyle().Foreground(scrollThumbFg)

	// stylePeerRow 是对方消息那几行的底色。
	//
	// 只给**对方**的行铺灰底，自己的保持无底色 —— 自己那边靠右对齐、
	// 名字用另一个颜色，已经够区分了。这样余光一扫就能分出「谁在说」，
	// 不用去读名字。
	//
	// 自适应深浅：浅色终端给浅灰，深色终端给深灰。写死一个灰，在另一种
	// 背景的终端上要么糊成一片、要么刺眼。
	stylePeerRow = lipgloss.NewStyle().Background(peerRowBg)
)

// 分层用的底色（各区域自成一档，互不相同）。
//
// 深色终端上是一条「越靠前越亮」的梯子：导航 235 < 对方的消息 236 <
// 输入框 237 < 选中 238，都在背景之上，相邻两档只差一格 —— 差得出来、
// 又不刺眼。浅色终端同理，只是方向反过来（越靠前越暗）。
var (
	navBg         = lipgloss.AdaptiveColor{Light: "255", Dark: "235"}
	inputRowBg    = lipgloss.AdaptiveColor{Light: "252", Dark: "237"}
	rowSelectedBg = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
)

// peerRowBg 是对对方消息行铺的那个灰。
//
// 253 = 很浅的灰（浅色终端上刚刚看得出来），236 = 很深的灰（深色终端上
// 同样只差一档）。两边都刻意贴着各自的背景走：这是**分区**用的底色，
// 不是高亮，抢了正文的对比度就本末倒置了。
//
// 它和 inputRowBg 各占梯子上的一格：两块灰离得很远（一封邮件 vs 底部
// 输入区），共用一档本来也看不出来，但既然是一条要维护的梯子，就不留
// 「这两个常量值一样，是巧合还是故意的」这种要靠猜的空档。
var peerRowBg = lipgloss.AdaptiveColor{Light: "253", Dark: "236"}

// scrollBarFg / scrollThumbFg 是滚动条轨道与滑块的前景色。
//
// 两者都刻意和 stylePeerRow 的底色错开：轨道贴着背景（浅底给浅灰、
// 深底给深灰），滑块反着来，这样一条细线在两种终端上都能看出「有」。
var (
	scrollBarFg   = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	scrollThumbFg = lipgloss.AdaptiveColor{Light: "243", Dark: "250"}
)

// peerRowBgSeq 返回当前终端上该用的底色序列；终端不支持颜色时返回空串。
func peerRowBgSeq() string {
	return bgSeqOf(stylePeerRow)
}

// navBgSeq 是导航列底色对应的序列。
func navBgSeq() string {
	return bgSeqOf(styleNav)
}

// bgSeqOf 从一个样式的渲染结果里取出它的「设置」序列（探针见下）。
//
// 借 lipgloss 把自适应色解析成具体序列，而不是自己去判断终端深浅 ——
// 深浅判断（COLORFGBG / OSC 11 查询）和色彩降级（真彩 → 256 → 16）都是
// lipgloss/termenv 的活，手抄一遍迟早和别处的上色对不上。
func bgSeqOf(s lipgloss.Style) string {
	// 探针字符只是为了把「样式前缀」和「内容」分开；用一个宽度为 0、
	// 不可能出现在正文里的字符，就不用担心它在别处出现。
	const probe = "\x00"
	rendered := s.Render(probe)
	if i := strings.Index(rendered, probe); i > 0 {
		return rendered[:i]
	}
	return ""
}

// paintRowBg 给一整行铺上底色序列 seq。
//
// ⚠️ 不能简单地写 style.Render(line)。行里已经套着各种前景色样式
// （发件人名字、标题、链接、行内代码），每一段结尾都带一个 SGR 重置
// \x1b[0m，它会把外层刚设好的底色**一起清掉**。实测：
//
//	inner    "\x1b[1;38;5;39m我\x1b[0m  14:32"
//	wrapped  "\x1b[48;5;236m\x1b[1;38;5;39m我\x1b[0m  14:32\x1b[0m"
//	                                              ↑ 从这里起底色就没了
//
// 于是底色只在「没上过色的那几段」后面看得见，一行花成一段一段的 ——
// 比不铺还难看。
//
// 所以底色是**在每个重置之后重新压上去**的，末了用 \x1b[49m 恢复终端的
// 默认背景，免得底色漏到下一行去。
//
// 这条做法依赖一个实测过的事实：渲染层吐出的重置序列只有 \x1b[0m 一种，
// 其余都是 \x1b[1m / \x1b[3m / \x1b[38;5;Nm 这类「设参数」序列，不会反向
// 清掉底色。TestChat_PeerRowsAreBandPainted 守着这一点。
//
// ⚠️ 还有一条：**这里铺的是「一行」，不是「一块」。** 末尾那个 \x1b[49m
// 一出现，底色就断了 —— 想拿一个序列管住多行的话，只有第一行有底色，
// 剩下全是裸的。所以侧栏那条贯通的竖带、浮起的输入行，都是自己按行循环
// 调本函数铺出来的，**没有**「铺一整块」的包装：那个名字会让人以为存在
// 一个能一次铺完的写法，而实际并不存在。
func paintRowBg(line, seq string) string {
	if seq == "" {
		return line
	}
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+seq)
	return seq + line + "\x1b[49m"
}

// paintPeerRow 给对方消息的整整一行铺上底色（右侧一直铺到版面边沿）。
func paintPeerRow(line string) string {
	return paintRowBg(line, peerRowBgSeq())
}

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
