package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// 终端宽度低于这个值就从两栏降级成单栏。
//
// 96 是在**还有一条常驻导航列**的时候定的（那一列 14 宽）。导航列改成
// 按需唤出之后，双栏在 96 列下能给到「列表 24 + 竖线 1 + 正文 71」，
// 比原来宽裕得多。
//
// 没有跟着调低：这个数还管着禅模式的宽窄分支（见 zen.go 的 zenWide），
// 两个界面共用一个阈值比各自留一个数更不容易看走眼；而且降到 84 之后
// 正文只剩 59 列，恰恰回到"导航列还在"时的那种挤 —— 那个体验不该被
// 当成"双栏的下限"。要动它，先一起看禅模式那几张截图。
const singlePaneWidth = 96

// inputBlockHeight 是输入区占的行数：上下各留一行空白，中间一行是输入行。
//
// 那两行留白不是浪费，是**这个方案成立的前提**：字符网格里没有圆角、没有
// 阴影、没有 z 轴，"这里可以打字"只能靠「被空白围住的一行」来表达。它现在
// 连底色都不铺了（下一条注释说为什么），留白就从"辅助"变成了**唯一的手段**
// —— 去掉留白，输入行就退回成又一条贴底的横带，和状态栏糊在一起。
const inputBlockHeight = 3

// 引用的几何。两个数是一组，改一个要连着看另一个。
//
//	 ←inset→│▎←gap→正文……
//
// paneInset 是**正文栏里所有内容的左内缩**：引用条、会话标题、输入行、
// 状态栏都从栏左沿 +1 格开始。它必须存在：不留这一格的话，引用条就直接
// 压在会话流的左沿上，而那一格终端背景是「这一段话独立于版面」的唯一
// 表达 —— 也是标题、输入行能和它左对齐的那条基准线。
//
// **同一个数管着四样东西**，它们才会左对齐。各写各的缩进（引用条 1 格、
// 标题 0 格、输入行 0 格）看上去每处都"差不多"，合起来就是一条参差不齐
// 的左边缘 —— 而参差没人会专门提，只会觉得"说不上哪儿别扭"。
//
// quoteGap 是引用条和正文之间的那一格间隔。不留它的话，汉字会贴着那条
// 竖条，读起来像被划了一道。
//
// ⚠️ 这里原本还有 bubblePadX / minBubbleW 两个数，是「对方的话围在一块
// 底色块里」那一版留下的：气泡的宽度是「最长那一行 + 两侧内边距」反推的，
// 太短还得有个下限，否则一句「好」就是个 4 列宽的补丁。现在对方的话靠
// **一条竖条**（见 sidelineMark）划分，宽度跟着内容自然走 —— 两个数一起
// 退休了。留着它们只会让下一个读代码的人以为还有一块底色要算宽度。
//
// minNameW 是消息头里留给名字的最小列数，见 renderMessage。
const (
	paneInset = 1
	quoteGap  = 1
	minNameW  = 4
)

// 灰阶：界面上所有「次要文字」的颜色。
//
// ⚠️ **不要用 Faint(true)**，这一版把它整个换掉了。
//
// Faint 是 SGR 2（"半亮"），它的实现是**把当前前景色往背景色方向压一半**，
// 所以效果完全取决于终端和字体 —— 而 Windows Terminal 长期不支持 SGR 2，
// 实测截图里所有本该变灰的文字（分组标题、非当前标签页、快捷键提示、
// 状态栏）**一点都没变灰**，全是默认前景色。后果不是"不够淡"，是
// **层次塌了**：标题、条目、说明文字亮度一样，一片平。这正是那张
// 截图"没有设计感"的一半来源。
//
// 显式给一个灰色就没这个问题：它是确定的、跨终端一致的，也不依赖字体
// 有没有做半亮字形。
var (
	// fgMuted 是次要信息（说明文字、非当前标签页、状态栏）。
	fgMuted = lipgloss.AdaptiveColor{Light: "244", Dark: "244"}
	// fgDim 更弱一档，给分组标题这类"只是分区、不需要读"的文字。
	fgDim = lipgloss.AdaptiveColor{Light: "246", Dark: "242"}
	// fgTime 比 fgMuted 再暗一点：列表里的时间是**背景信息**，
	// 它要能被右对齐的基准线看到，但不该和名字抢注意力。
	fgTime = lipgloss.AdaptiveColor{Light: "247", Dark: "241"}
	// fgPlaceholder 是输入框里的占位提示文字。
	//
	// ⚠️ 它的来历要记住，但**值不用再跟着它**了：上一版输入卡铺着 240 的
	// 底色，而 bubbles 的默认占位灰降级到 256 色正好也是 240 —— 两者一撞，
	// 「输入消息，回车发送」八个字在界面上整个消失，只剩一个孤零零的提示符
	// （当时有 TestPlaceholder_ReadsOnItsOwnBackground 守着）。
	//
	// 输入卡撤掉之后底色没了，那个撞色不可能再发生；这个值只是让占位提示
	// 比正文弱一档、同时比 fgMuted 亮一点 —— 它要读得清，因为那是输入框
	// 唯一的用法说明。
	fgPlaceholder = lipgloss.AdaptiveColor{Light: "238", Dark: "250"}
)

var (
	styleTitle    = lipgloss.NewStyle().Bold(true)
	styleMuted    = lipgloss.NewStyle().Foreground(fgMuted)
	styleTime     = lipgloss.NewStyle().Foreground(fgTime)
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleLink     = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))

	// styleError 是错误提示的颜色。
	//
	// 用**降过饱和**的红，和 Zen 那边同一个值：纯红（203）在一个安静的
	// 界面里像一盏警报灯，而这里要说的往往只是「这一次同步没成功」。
	// 「不正常」这件事靠**颜色出现**本身表达就够了，不需要它刺眼。
	styleError = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "131", Dark: "174"})

	// styleAccent 是界面里**唯一**的强调色。
	//
	// 它只承担一个语义：「这里是你现在所在的位置」。撒开用（标题一个色、
	// 分组一个色、图标再一个色）画面会花，而花和「有设计」恰好是反的 ——
	// 层次是**克制**出来的，不是堆出来的。
	//
	// 位置本身由底色块表达（选中项、当前标签页都铺一块），强调色只是在
	// 底色之上再点一下，给眼睛一个明确的落点。
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	// styleUnread 是列表行首那个未读标记。
	//
	// 也用强调色，不另开一个红/橙：**位置靠底色，状态靠颜色**。选中项已经
	// 用底色块说了「你在这儿」，标记再抢一个颜色，就是两种语义抢同一个
	// 位置，两个都会变弱。
	styleUnread = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	// stylePrompt 是输入框前面那个提示符。
	stylePrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	// styleMDRule 是正文里分隔线（---）被渲染成的那条横线。
	styleMDRule = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	// styleTag 是消息头上那个「HTML」小标记。
	//
	// 用灰色而不是亮色：它是一条**只读说明**（这条正文是转出来的），
	// 不是状态、更不是警告，亮起来会跟发件人名字抢注意力。灰色却仍然
	// 看得见，足够了。
	styleTag = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	// styleSideline 是对方消息左边那条竖条。
	//
	// 它替代的是上一版的**消息底色块**。用户的原话是「不要用背景色块来
	// 区分聊天，割裂感太强」—— 一条消息一块底色，一屏里就有十几块亮度
	// 不同的矩形在互相切割，而且长消息的气泡宽、短消息的气泡窄，右边缘
	// 参差不齐，看着像一屏打乱的表格。
	//
	// 换成一条竖条之后，画面里只剩终端背景这一个"面"：谁的正文被划在
	// 哪一段里，靠左边那条竖条回答，不靠亮度。这也让上面那条分工（底色
	// 只表达"可操作"）在整幅界面里没有例外。
	//
	// ⚠️ 用 fgMuted 而不是 fgDim：这条竖条是**内容的一部分**（它要说
	// "这段是别人说的话"），得看得清；fgDim 那一档是给分组小标题、时间戳
	// 这类"扫一眼就够"的东西的。用 fgDim 会让引用块散架，读者看不出那几
	// 行是一段。
	//
	// （这一条原先还有个"比面板分隔线亮一档"的说法 —— 那条线已经撤掉了，
	// 见 viewTwoPane。层次还在，只是参照物没了。）
	styleSideline = lipgloss.NewStyle().Foreground(fgMuted)

	// styleRowSelected 是会话列表里选中项那条底色块。
	//
	// 替代了原先的整行反白（styleSelected）。反白是终端里最大的对比度，
	// 一行反白会在视野里炸开；底色块只是「比周围亮几档」，选中位置照样
	// 一眼看得见，但不会把整个列表压下去。
	//
	// ⚠️ 块的两侧各内缩一格（见 renderListRow）：通栏的色块看着像表格的
	// 斑马纹，内缩之后才像「一条被选中的东西」。导航项的选中态和当前
	// 标签页用的也是这一块 —— 见 styleNavActive。
	styleRowSelected = lipgloss.NewStyle().Background(rowSelectedBg).Bold(true)
	// styleGroupTitle 是列表里「未读 / 已读」这类分组小标题。
	styleGroupTitle = lipgloss.NewStyle().Foreground(fgDim)

	// styleInputRow 原先在这里（浮起输入卡的底色）。
	//
	// ⚠️ 它和 paintRowBg 一起退休了。用户的要求是「不要太繁杂」，原话点名
	// 了「输入卡底色的重感」—— 一屏里唯一一块实心色把视线全吸到最底下去。
	// 现在输入区只有提示符 + 上下两行留白（和 Zen 一样），一个色块都不铺。
	// 判据见 TestInputBlock_IsSurroundedByBlankLines / TestPanes_AreNotPainted。

	// stylePlaceholder 是输入框没内容时那句提示。
	//
	// 必须在**赋值时**就把它挂到 textinput 上（见 newTextInput）：它的
	// 默认色是一个写死的 hex，在深色终端上偏暗。
	stylePlaceholder = lipgloss.NewStyle().Foreground(fgPlaceholder)

	// styleTabActive / styleTabIdle 是会话栏顶部那排标签页的两态。
	//
	// 当前那一页铺底色块（和列表选中同一块），其余只留灰字、不铺底 ——
	// 标签页是一排并列的东西，靠底色指明「你在哪一页」比靠颜色更稳。
	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("141")).Background(rowSelectedBg)
	styleTabIdle = lipgloss.NewStyle().Foreground(fgMuted)

	// styleScrollBar / styleScrollThumb 是会话正文右侧那条滚动条。
	//
	// 轨道用低调的灰、滑块用亮一些的灰：它是**背景信息**，告诉用户
	// 「这里还有内容、能滚」，不该比正文本身更抢眼。
	styleScrollBar   = lipgloss.NewStyle().Foreground(scrollBarFg)
	styleScrollThumb = lipgloss.NewStyle().Foreground(scrollThumbFg)
)

// 强调用的底色。
//
// 这一版只剩**一块**：
//
//	终端背景（不铺）  最暗  ← 列表、正文、标签页、状态栏、输入区都在这一层上
//	选中块 / 当前标签页   240   ← 唯一的「抬升层」
//
// ⚠️ 上一版这里有三块（列表 235 / 文件夹栏 237 / 输入卡 240），再上一版是
// 四级梯子。三级之后再砍掉输入卡，是因为**「抬升」这个动作现在只有一个
// 意思**：列表里那一行是你当前选中的东西。输入区不再抬升 —— 它靠留白
// 和提示符被认出来，不靠亮度。
//
// 分区（列表 / 正文）交给两栏之间那一格留白（见 view.go 的 rail）。
var (
	// rowSelectedBg 是列表选中项那一块。
	rowSelectedBg = lipgloss.AdaptiveColor{Light: "249", Dark: "240"}
)

// scrollBarFg / scrollThumbFg 是滚动条轨道与滑块的前景色。
//
// 两者都刻意和输入卡的底色错开：轨道贴着背景（浅底给浅灰、深底给深灰），
// 滑块反着来，这样一条细线在两种终端上都能看出「有」。
var (
	scrollBarFg   = lipgloss.AdaptiveColor{Light: "250", Dark: "239"}
	scrollThumbFg = lipgloss.AdaptiveColor{Light: "243", Dark: "250"}
)

// 列表行首的那两个标记字符。
//
// ⚠️ **只用 Block Elements 区（U+2580–259F）和 ASCII。**
//
// 这一条是拿截图换来的。原来用的是 ● (U+25CF) 和 ★ (U+2605) —— 两者的
// 默认呈现确实是"文本"，看着很安全。但 Windows 上等宽字体（Cascadia
// Code 这一族）**没有这两个字形**，Windows Terminal 于是回退到
// Segoe UI Emoji，结果是列表里冒出一堆**不受 SGR 控制**的彩色图形：实测
// 截图里那个星标被画成了黄绿色，跟整屏的灰阶体系当场打架。
//
// 补一个 VS15 (U+FE0E) 也救不了：字体栈里根本没有"文本呈现"的字形，
// 选择器没有东西可选，回退照样发生。
//
// Block Elements 是终端字体的硬性覆盖范围（画框、画进度条都要用），
// 而且这一个区**没有任何 emoji 变体**，不会触发回退。ASCII 同理。
const (
	// blockMark 是左半块（U+258C）。
	//
	// **同一个字符在两个地方用，语义不同**：侧栏里它是「你现在在这儿」
	// 的标条，列表里它是「这条没读过」的标记。两者隔着一条完整的列表栏，
	// 永远不会出现在同一行的同一列上，所以不会混淆 —— 也正是因为语义
	// 不同，这里只留一个常量、两处各写各的意思，而不是造两个同名alias。
	//
	// 半块比全块轻：摆在行首像一条竖标条，不像一个方块。
	blockMark = "▌"
	// 注：这里原本还有个 ruleMark（│，列表栏和正文栏之间那条分隔竖线）。
	// 那条线被撤掉了 —— 分区改成靠两栏之间那一格留白（见 view.go 的 rail）。
	// 留着这个常量只会让下一个读代码的人以为还有一条线要画。

	// sidelineMark 是对方消息左边那条引用竖条（U+258E，左四分之一块）。
	//
	// 用**块**而不是**线**：两者都用 │ 的话，「这是一段话的边」和「这是
	// 两栏的边」在余光里是同一个东西 —— 引用块会散架成"被划了一道的话"。
	// （两栏那条线已经撤掉了，但**块区** (U+2580–259F) 和**制表区** (U+2500–257F)
	// 在视觉上本来就不是一族，这个选择仍然对：一张脸里全屏只该有一条细线，
	// 而那一条该属于内容。）左四分之一块的笔画比左半块（blockMark）细，
	// 做引用条正好：看得见，又不至于像一堵墙。
	//
	// 位置也刻意错开：竖条画在栏左沿 +1 格，不贴着栏边。
	//
	// 和 blockMark 一样出自 Block Elements 区，同样没有 emoji 变体。
	sidelineMark = "▎"
	// starMark 是星标会话的标记。用 ASCII 星号：星形字符全在
	// Geometric Shapes 区，那个区里有 emoji 变体的成员不少，不值得赌。
	starMark = "*"
	// markWidth 是行首标记区的宽度（标记 + 一格间隔）。两个标记都靠
	// 左摆在这个区里，于是有没有它们后面的名字都不会错位。
	markWidth = 2
)

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

// 注：这里原本有个 paintRowBg —— 给一整行铺底色，而且**在每个 SGR 重置
// 之后重新压一遍**（行里的名字、标题、链接各带一个 \x1b[0m，会把外层的
// 底色一起清掉，不重压的话一行花成一段一段的）。
//
// 它是为浮起的输入卡写的，输入卡撤掉之后没有任何调用点了，故删除。留着
// 它比删掉更危险：那个「重压」的写法看起来通用，下一个人想给别的地方铺
// 底色时会直接拿去用 —— 而它有个已经记在注释里的硬约束（**铺的是「一行」
// 不是「一块」**：末尾那个 \x1b[49m 一出现底色就断了，想用一个序列管住
// 多行只有第一行有底色）。既要铺多行又要躲开那个坑的写法还没人写过。

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

// styleSender 是消息头上发件人名字的样式。
//
// ⚠️ 这里原先是一个**十个颜色的调色板**（senderPalette），按地址哈希稳定地
// 给每个发言人分配一个颜色，好让群聊里一眼认人。它被删掉了，理由有两条：
//
//  1. **和一个界面只有一个强调色的规矩打架。** 为了让 141 保持"只表示你
//     现在的位置"，调色板得小心翼翼地绕开它 —— 靠一条注释和一次人工核对
//     来维持，而"颜色够不够用"这件事每隔一阵就会有人想往里加一个色。
//
//  2. **它换来的信息，版面已经给了。** 「谁说的」由**对齐**回答：自己发的
//     靠右，别人的靠左（见 renderMessage 的 padLeft / padRight）。名字的
//     **亮度**只用来和右边的灰色时间分档。颜色在这里是第三个维度，而它
//     承载的信息是零 —— 十种色相没有一种对应"这个人是谁"。
//
// 所以现在所有发件人**同一个样式**：粗体、终端自己的前景色。想认出是谁，
// 读名字（名字就在旁边）。这正是 Zen 那套「去掉色相、只留明度」的做法。
var styleSender = lipgloss.NewStyle().Bold(true)

// maxCellW 是一个字符在终端里最多占的列数（CJK 汉字、全角标点、emoji 都是 2）。
//
// 它是一条**布局下限**的来源：折行的硬断点是以「一个字符」为单位推进的
// （见 markdown.go 的 wrapLine，`end = start + 1`），所以宽度给到 1 列时
// 一个汉字照样占满 2 列 —— 任何「至少要 N 列才画得出来的东西」都得把这个
// 数算进去，否则会在极窄的窗口里多出一列。
const maxCellW = 2

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
