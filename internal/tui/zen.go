package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// layoutMode 是**视觉/布局**维度，和 mode（交互状态）正交。
//
// 为什么不干脆加一个 modeZen：mode 回答的是「现在在跟哪个界面打交道」
// （列表、会话、搜索、确认框），layout 回答的是「同一件事画成什么样」。
// 把两个维度压进一个枚举，立刻出现两笔债 ——
//
//  1. 组合爆炸：modeChatZen / modeListZen / modeSearchZen……每加一个界面
//     都要问「它有没有 Zen 形态」。
//  2. 「按 Esc 该回哪儿」说不清：Esc 在 Zen 里是「退出 Zen」，在 Normal
//     的会话里是「退回列表」。混成一个枚举就得分身。
//
// 拆开之后状态是 (modeChat, layoutZen) 这样的二元组，每条转移规则只动
// 其中一个轴：Esc 动 mode，F2 动 layout。
type layoutMode uint8

const (
	layoutNormal layoutMode = iota
	layoutZen
)

// zenScreen 是 Zen 空间里的三屏。
//
// 和 layout 的分工：layout 决定「在不在 Zen 里」，zenScreen 决定「在 Zen 的
// 哪一屏」。进入 Zen 之前 mode 记着 Normal 世界停在哪，退出时原样回去 ——
// 所以进 Zen 不会丢掉「我本来在哪个界面」这件事。
//
// 三屏是一个栈，Esc 一次退一层：
//
//	home ──Tab──▶ list ──Enter──▶ chat
//	 ▲             │               │
//	 └────Esc──────┘◀─────Esc──────┘
type zenScreen uint8

const (
	// zenHome 是首页：字标 + 一个输入框。进 Zen 的落点。
	zenHome zenScreen = iota
	// zenList 是会话列表。
	zenList
	// zenChat 是单个会话的读写界面。
	zenChat
)

const (
	// zenInputPrompt 是 Zen 下的输入提示符。
	//
	// 用 › 而不是 >：它更细、更安静，而且和 Normal 的 "> " 一眼能分开 ——
	// 帮助页和截图里说「› 就是 Zen」的时候不会和别处混淆。
	zenInputPrompt = "› "

	// normalInputPrompt 是 Normal 下的提示符，切回去时要还原。
	normalInputPrompt = "> "

	// zenInputPlaceholder 是 Zen 下输入框的占位文字。
	//
	// 比 Normal 那句短：Zen 的输入区是一个写作的地方，不是一张告诉你怎么
	// 用的表单。「回车发送」在 Normal 里值得写（那里同时挤着好几个提示），
	// 到 Zen 里就只剩噪音了 —— 而且回车发送是这个界面上唯一会做的事。
	zenInputPlaceholder = "输入消息…"

	// chatInputPlaceholder 是 Normal 下会话输入框的占位文字。
	//
	// 提成常量是因为它现在有三个地方要写：进会话时设、会话里重设、
	// 以及从 Zen 切回来时还原。三处各写一遍字面量，改一处漏两处。
	chatInputPlaceholder = "输入消息，回车发送"

	// zenHint 是刚进 Zen 时那一行一次性提示 —— 每屏一句，见 zenHintFor。
	//
	// Zen 为了安静把常驻提示全删了 —— 删过头了：连「怎么换会话」都没人
	// 知道（NoWint 直接来问了一遍，这就是证据）。所以留一行，但**只留到
	// 用户第一次按键为止**：他要找的就是这一下，找到之后这行就该消失，
	// 不该变成又一个常驻 footer。
)

// zenGroupWindow 是同一个人连着说的几句话被并成一组的时间窗。
//
// 15 分钟：短于它的连续发言算「一口气说的」，长于它的就该重新标一次时间。
// 太短会让每条消息都顶一个名字（那还不如不分组），太长会把上午说的和
// 下午说的并成一组，组头那个时间戳就成了谎话。
const zenGroupWindow = 15 * time.Minute

// zenHintFor 返回某一屏上那行一次性提示。
//
// 分屏写而不是共用一句：三屏能做的事完全不同，一句「Ctrl+↑/↓ 切会话」
// 摆在首页上是在回答一个用户还没问的问题。
func zenHintFor(s zenScreen) string {
	switch s {
	case zenHome:
		return "Tab 会话列表 · 输入后回车发送 · F2 退出"
	case zenList:
		return "↑/↓ 选 · 回车打开 · Esc 回首页"
	default:
		return "Esc 会话列表 · F2 首页 · Ctrl+↑/↓ 换会话"
	}
}

// zenChromeHeight 是 Zen **会话屏**里除正文之外固定占掉的行数。
//
//	头部 1 + 头部后的空行 1 + 正文后的空行 1 + 分隔线 1 + 分隔线后的空行 1 + 输入 1
//
// 这六行是会话屏的全部「界面」。Normal 在这个高度里要塞两栏、标题、
// 状态栏 —— 差的就是这些留白。
const zenChromeHeight = 6

// zenActive 说当前是不是真的在用 Zen 布局。
//
// 只看 layout 这一个开关。mode 不参与判断 —— 它是 Normal 世界的状态，
// 在 Zen 里被原样保留着，退出时用来回到原来的界面。
func (m Model) zenActive() bool {
	return m.layout == layoutZen
}

// zenInChat 说当前是不是在 Zen 的**会话屏**上。
//
// 滚动相关的几个函数只关心这一屏：首页和列表没有可滚的正文，拿会话屏的
// 可见行数去算它们的高度只会得出一个没有意义的数。
func (m Model) zenInChat() bool {
	return m.layout == layoutZen && m.zenScreen == zenChat
}

// setZenFocus 按当前 Zen 屏决定输入框要不要焦点。
//
// 首页和会话屏上用户是要打字的，必须聚焦；列表上没有输入，失焦。
//
// ⚠️ 这条是补一个**实测出来的 bug**：输入框的焦点原先一直「继承」着进 Zen
// 之前的状态 —— 从 Normal 会话进 Zen 恰好是聚焦的（那边本来就在打字），所以
// 一路没暴露；从 Normal **列表**进 Zen 就是失焦的，首页上打字毫无反应。
//
// 表现就是用户报的「f2 后 tab 有 bug」：F2 → Tab（去列表）→ Esc（回首页），
// 输入框彻底哑了，敲什么都没用。而且失焦的输入框**看不出来**（提示符和占位
// 文字都照画），所以只能靠按了没反应才发现。
func (m *Model) setZenFocus() {
	if m.zenScreen == zenList {
		m.input.Blur()
		return
	}
	m.input.Focus()
}

// applyInputStyle 按当前布局设置输入框的提示符与占位文字。
//
// 集中在一处：进 Zen、在 Zen 里换屏、从 Zen 里打开一个会话（enterThread 会
// 把占位文字设回 Normal 那套）—— 三条路都要设对，散着写必然漏一条。
func (m *Model) applyInputStyle() {
	if !m.zenActive() {
		m.input.Prompt = normalInputPrompt
		m.input.PromptStyle = lipgloss.NewStyle()
		m.input.Placeholder = chatInputPlaceholder
		return
	}
	m.input.Prompt = zenInputPrompt
	m.input.PromptStyle = zenAccent
	m.input.Placeholder = zenInputPlaceholder
}

// toggleZen 在 Normal 与 Zen 之间切换。
//
// 进 Zen 一律落在首页；出 Zen 回到进之前那个界面（mode 没被动过）。
func (m *Model) toggleZen() {
	if m.layout == layoutZen {
		m.layout = layoutNormal
		m.zenShowHint = false
		m.applyInputStyle()
		// 出 Zen 时把焦点还原成「Normal 世界该有的样子」：会话里要聚焦，
		// 列表里不该有。不还原的话，从 Zen 首页退回 Normal 列表会留下一个
		// 隐形的聚焦输入框。
		if m.mode == modeChat || m.mode == modeNewChat {
			m.input.Focus()
		} else {
			m.input.Blur()
		}
		m.syncInputWidth()
		m.clampChatScroll()
		return
	}

	m.layout = layoutZen
	m.zenScreen = zenHome
	// 进来先亮一下怎么用，用户按第一个键就收掉。
	m.zenShowHint = true
	m.applyInputStyle()
	m.setZenFocus()
	m.syncInputWidth()
	m.clampChatScroll()
}

// goZen 切到 Zen 里的某一屏。
func (m *Model) goZen(s zenScreen) {
	m.zenScreen = s
	m.applyInputStyle()
	m.setZenFocus()
	m.syncInputWidth()
	m.clampChatScroll()
}

// syncInputWidth 让输入框的宽度跟着当前布局走。
//
// Zen 的正文列比终端窄，输入框不跟着收的话，敲到二三十个字就会顶出正文列的
// 右边界，把居中排版撑歪。Normal 下恢复成 0（不限制），保持原有行为不变。
func (m *Model) syncInputWidth() {
	if !m.zenActive() {
		m.input.Width = 0
		return
	}
	avail := m.zenContentWidth()
	w := avail - textWidth(m.input.Prompt)
	if w < 10 {
		w = 10
	}
	m.input.Width = w
}

// zenLogoWord 是字标拼的词。
const zenLogoWord = "CLICHAT"

// zenLogoArt 是首页上那个 CLICHAT 字标。
//
// 用的是 figlet 的 ANSI Shadow 那一路框线字：实心块 █ 做主干、╗ ╔ ╝ ╚ ═ ║
// 做转角。比纯点阵更接近印刷体的观感 —— 转角是「斜切」出来的，不是硬碰硬
// 的直角，所以大字号下才立得住。
//
// 6 行 × 最长 52 列。各行宽度不等（T 的顶横比它的竖笔宽），所以居中要按
// **最宽的那一行**算，不能逐行居中 —— 逐行居中会让每行的起始列都不一样，
// 字立刻散开。见 viewZenHome 里那段。
//
// ⚠️ **第 0 行和第 5 行开头的那个空格是内容，不是缩进。**
//
// ANSI Shadow 的阴影相对字身右下偏移一格，所以 'C' 的顶横（`██████╗`）和底横
// （`╚═════╝`）都比它的竖笔（`██║`）右移一列 —— 官方输出里这两行就以一个空格
// 开头，其余四行从第 0 列起。这正是 figlet 原样：
//
//	 ██████╗...   ← 第 0 行
//	██╔════╝...   ← 第 1 行
//	 ╚═════╝...   ← 第 5 行
//
// 丢掉它，'C' 的上下两横会比竖笔左移一格，整个字像被斜切了一刀；行尾的空格
// 则无所谓 —— 每行前面补的留白是**整块共用**的（见 viewZenHome），行尾多少
// 由终端补。所以这里只保留前导空格、行尾空格一律去掉，免得被编辑器的
// 「去尾随空白」反复改来改去。
//
// 这条踩过一次：从终端里复制字标时，第 0/5 行行首那个空格很容易跟着丢
// （很多地方会顺手 trim 掉），而丢掉之后画面只是「有点歪」，不像坏了。
// TestZenLogo_LeadingSpacesMatchFiglet 守着它。
//
// 改字形时注意：这些框线字符（U+2550 区）和实心块一样属于 East Asian
// Ambiguous，本仓库的 cellWidth 把它们钉成 1 列，和 lipgloss.Width 同一把
// 尺子（styles.go 里 cellWidth 的注释记着这条）。
var zenLogoArt = []string{
	" ██████╗██╗     ██╗ ██████╗██╗  ██╗ █████╗ ████████╗",
	"██╔════╝██║     ██║██╔════╝██║  ██║██╔══██╗╚══██╔══╝",
	"██║     ██║     ██║██║     ███████║███████║   ██║",
	"██║     ██║     ██║██║     ██╔══██║██╔══██║   ██║",
	"╚██████╗███████╗██║╚██████╗██║  ██║██║  ██║   ██║",
	" ╚═════╝╚══════╝╚═╝ ╚═════╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝",
}

// zenLogoWidth 是字标整块的宽度（最宽那一行）。
func zenLogoWidth() int {
	w := 0
	for _, l := range zenLogoArt {
		if n := textWidth(l); n > w {
			w = n
		}
	}
	return w
}

// zenLogo 返回要画的字标；正文列塞不下时退回一行纯文字。
//
// 退回是必需的：字标是装饰，装饰不该撑破版面。52 列在 54 列以下的正文列里
// 放不下（终端宽度约 54 以下），硬画会横向溢出，把整个居中排版撑坏。
// 退回时用「字母 + 空格」拉开的写法（13 列），窄终端上仍然像一个字标。
func zenLogo(contentW int) []string {
	if zenLogoWidth() > contentW {
		return []string{strings.Join(strings.Split(zenLogoWord, ""), " ")}
	}
	return zenLogoArt
}

// ---- 尺寸 ----

// zenContentWidth 是 Zen 正文列的宽度。
//
// 无论终端多宽，正文都不铺满。一行超过七八十个字符之后，眼睛从行尾回扫到
// 下一行开头就开始费劲 —— 这是排版常识，也是 Zen 和 Normal 最直观的差别：
// Normal 用满整幅（信息密度优先），Zen 主动留白（可读性优先）。
//
// 四段式：先按「两边各留 8 列」算，窄终端上**垫到 zenMinContent**（别让
// 正文列缩成一条），再按**配置里的上限**收窄，最后兜一道「绝不能超出屏幕」。
// 最后那道是必需的 —— 前面为了凑下限会把宽度顶上去，40 列的终端上直接
// 溢出，横向滚动条就出来了。
//
// ⚠️ 配置那道**必须排在下限之后**。反过来的话，用户把宽度设成 48（配置允许）
// 就永远落不了地：下限 56 会把它顶回去。让「用户明确选的数」赢过「实现自己
// 定的经验下限」，只在窄终端上（连 48 列都放不下）才轮到屏幕兜底。
//
// ⚠️ 上限不是写死在这里的常量，而是 `cfg.ZenWidthLimit()`。写死的话，
// 设置页改了 zen_width 就是个**只在界面上生效**的假开关 —— 这一类「配置
// 项被读了一半」的 bug，症状是「我明明改成 60 了，还是那么宽」。
func (m Model) zenContentWidth() int {
	const zenMinContent = 56
	limit := m.cfg.ZenWidthLimit()

	w := m.width - 16
	if w < zenMinContent {
		w = zenMinContent
	}
	if w > limit {
		w = limit
	}
	if w > m.width-2 {
		w = m.width - 2
	}
	if w < 1 {
		w = 1
	}
	return w
}

// zenLeftPad 是正文列左侧的留白，用来把内容列居中。
func zenLeftPad(width, contentW int) int {
	if pad := (width - contentW) / 2; pad > 0 {
		return pad
	}
	return 0
}

// zenRightAlign 说自己的消息要不要靠右。
//
// 窄终端上取消。靠右对齐意味着每一行左侧都要补一大段空白，而窄终端上
// 正文列本来就只剩三四十列，再砍掉一半，读起来比左对齐还累。
// 宁可放弃这个视觉习惯，也不能牺牲可读性 —— 这就是「自动退化为单列」。
func (m Model) zenRightAlign() bool {
	return m.width >= singlePaneWidth
}

// ---- 渲染层的消息模型 ----

// zenItem 是一条消息在**渲染层**的样子。
//
// 它和 thread.Header 是两回事：Header 是业务数据（谁、什么时候、引用链），
// zenItem 是视觉分组（这一条是不是一组发言的开头 / 结尾）。分组只活在
// 这一层，绝不回写业务数据 —— 改分组规则不该动到任何一封邮件。
type zenItem struct {
	Mine   bool
	Sender string
	Time   time.Time
	Body   string // Markdown 原文
	HTML   bool

	GroupStart bool
	GroupEnd   bool
}

// zenStartsGroup 判断 cur 是不是该另起一组。
//
// 三个条件任缺其一就分组：换人、换方向（我 / 对方）、隔得太久。
// 纯函数，好单测。
func zenStartsGroup(prev, cur zenItem) bool {
	if prev.Mine != cur.Mine || prev.Sender != cur.Sender {
		return true
	}
	gap := cur.Time.Sub(prev.Time)
	if gap < 0 {
		gap = -gap // 时钟漂移或乱序到达时别把它当成「刚说过」
	}
	return gap > zenGroupWindow
}

// zenPresentation 把会话转成渲染层的消息序列。
func (m Model) zenPresentation(th thread.Thread) []zenItem {
	self := m.cfg.Self()
	items := make([]zenItem, 0, len(th.Messages))

	for _, msg := range th.Messages {
		mine := msg.From == self
		body := m.bodies[msg.MessageID]

		text := body.Text
		switch {
		case text != "":
		case msg.UID == 0:
			text = "（本地待同步）"
		default:
			text = "…"
		}

		items = append(items, zenItem{
			Mine:   mine,
			Sender: displayName(msg, mine),
			Time:   msg.Date,
			Body:   text,
			HTML:   body.HTML,
		})
	}

	// 分组是**视觉**的（这一条要不要重新标一次名字和时间），所以开关也活在
	// 这一层：ZenSplit 开着就是「每条都单独标」，关掉（默认）才按人+时间窗并组。
	//
	// 不并组时每条自己既是一组的开头也是结尾 —— 渲染那边只看这两个布尔，
	// 不需要知道「分组被关掉了」这回事。
	if m.cfg.Appearance.ZenSplit {
		for i := range items {
			items[i].GroupStart, items[i].GroupEnd = true, true
		}
		return items
	}

	for i := range items {
		items[i].GroupStart = i == 0 || zenStartsGroup(items[i-1], items[i])
		items[i].GroupEnd = i == len(items)-1 || zenStartsGroup(items[i], items[i+1])
	}
	return items
}

// ---- 渲染 ----

// zenFrame 是 Zen 三屏共用的画布参数。
//
// 抽出来的直接原因是「每行补齐到终端全宽」那件事：它之前只在会话屏上做了，
// 漏掉就会让居中排版在按最长行开窗的渲染器里整个塌掉（README 截图歪过一次）。
// 三屏各写一遍，迟早再漏一遍。
type zenFrame struct {
	width    int // 终端宽
	contentW int // 正文列宽
	left     int // 正文列左侧留白
}

func (m Model) zenFrame() zenFrame {
	contentW := m.zenContentWidth()
	return zenFrame{width: m.width, contentW: contentW, left: zenLeftPad(m.width, contentW)}
}

// indent 把一行挪进正文列。
func (f zenFrame) indent(s string) string {
	if f.left <= 0 || s == "" {
		return s
	}
	return strings.Repeat(" ", f.left) + s
}

// center 在正文列里把一行居中。
//
// ⚠️ s 必须是**没上色的纯文本**，样式从 style 参数进。理由和折行那条一样：
// 补白是按 textWidth 算的，而 textWidth 逐 rune 量、不认 ANSI —— 把一个已经
// 上色的串喂进来，转义序列里的 `[`、`3`、`8`、`m` 这些**可打印字符**会被
// 当成可见字符算进宽度。
//
// 这不是假想：列表标题原先写的是 `f.center(zenFaint.Render("会话"))`，
// 「会话」4 列被算成 17 列（256 色下 `\x1b[38;5;240m` + `\x1b[0m` 一共 13 个
// 可打印字符），补白从 37 列缩到 30 列，标题整体偏左 7 列。而且**偏多少取决于
// 色深** —— 16 色下序列更短，偏得也少，所以肉眼看只是「好像有点歪」，
// 换个终端又不一样。
func (f zenFrame) center(s string, style lipgloss.Style) string {
	pad := (f.contentW - textWidth(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + style.Render(s)
}

// fit 把行数补齐到 height、每行补齐到终端全宽，然后拼成一整块。
func (f zenFrame) fit(lines []string, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, ln := range lines {
		lines[i] = padRight(ln, f.width)
	}
	return strings.Join(lines, "\n")
}

// viewZen 是 Zen 空间的入口，按 zenScreen 分派。
func (m Model) viewZen() string {
	switch m.zenScreen {
	case zenHome:
		return m.viewZenHome()
	case zenList:
		return m.viewZenList()
	default:
		return m.viewZenChat()
	}
}

// viewZenHome 是首页：字标 + 一个输入框，两样都居中。
//
// 只有这两样。没有状态栏、没有最近会话、没有版本号 —— 首页是「安静地
// 开始写一句话」的地方，多一个元素就多一次分心。
func (m Model) viewZenHome() string {
	f := m.zenFrame()

	logo := zenLogo(f.contentW)

	// 输入框 + 它**下面**那条横线。
	//
	// 不用边框圈起来。原先套了一圈圆角框，理由是「表单控件需要可见的边界」——
	// 但那是网页表单的解法，放在这里有两个问题：一是 Zen 通篇不要容器，
	// 唯独首页破例一次反而显得突兀；二是那个框把输入框压成「一个要填的
	// 表单字段」，而首页想要的是「一个可以开始写字的地方」。
	//
	// 一条下划线就够了 —— 它标出「写到哪儿为止」，又不把这块地方关起来。
	// 和会话屏的输入区同构，只是那边线在上（正文在上）、这边线在下（空处在下）。
	input := []string{
		m.input.View(),
		zenRule.Render(strings.Repeat("─", f.contentW)),
	}

	// 字标 + 空行 + 输入框算作一块，整块垂直居中。
	const gap = 2
	blockH := len(logo) + gap + len(input)
	top := (m.height - blockH) / 2
	if top < 1 {
		top = 1
	}

	lines := make([]string, 0, m.height)
	for i := 0; i < top; i++ {
		lines = append(lines, "")
	}
	// 字标按**整块**居中：先取最宽那一行的宽度算一次补白，所有行共用。
	//
	// 不能逐行 center()：这个字标的各行宽度不一样（T 的顶横比竖笔宽，
	// 上下两端那两行的转角又比中间宽），逐行居中会让每行的起始列都不同，
	// 字立刻散开 —— 看上去像三四个互不相干的词。
	logoPad := (f.contentW - zenLogoWidth()) / 2
	if logoPad < 0 {
		logoPad = 0
	}
	for _, l := range logo {
		lines = append(lines, f.indent(strings.Repeat(" ", logoPad)+zenText.Render(l)))
	}
	for i := 0; i < gap; i++ {
		lines = append(lines, "")
	}
	for _, l := range input {
		lines = append(lines, f.indent(l))
	}

	// 提示单独钉在底部，不参与上面那块居中 —— 否则它一出现整块就会往上跳。
	if status := m.renderZenStatus(f.contentW); status != "" {
		for len(lines) < m.height-1 {
			lines = append(lines, "")
		}
		lines = append(lines, f.indent(status))
	}

	return f.fit(lines, m.height)
}

// viewZenList 是会话列表。
//
// 比 Normal 的列表少掉全部元数据：没有文件夹标签、没有星标、没有时间、
// 没有「等 N 人」。留下的只有「未读 / 已读」这一条信息 —— 因为它决定了
// 你下一眼看哪一条。选中用行首一个 › 标出来，不用反白：反白是整行的底色，
// 正是 Zen 要去掉的那种东西。
func (m Model) viewZenList() string {
	f := m.zenFrame()

	lines := make([]string, 0, m.height)
	lines = append(lines, "")
	lines = append(lines, f.indent(f.center("会话", zenFaint)))
	lines = append(lines, "")

	if len(m.visible) == 0 {
		lines = append(lines, f.indent(f.center("还没有会话", zenFaint)))
		return f.fit(lines, m.height)
	}

	// 正文区能放几行：扣掉上面 3 行和底部 2 行（空行 + 提示）。
	room := m.height - 5
	if room < 1 {
		room = 1
	}
	top := m.zenListTop(room)

	for i := top; i < len(m.visible) && len(lines) < m.height-2; i++ {
		th := m.visible[i]
		selected := i == m.cursor

		// 一行装下「谁 · 在聊什么」。
		//
		// Normal 的列表是两行（名字一行、主题一行），这里压成一行：Zen 的
		// 列表是「挑一个进去读」，不是「逐个核对」。但主题不能省 —— 只写
		// 参与者的话，一眼扫过去根本不知道哪个会话是哪个（实测截图里
		// 十条全是人名，等于没有信息）。
		label := threadTitle(th)
		if th.Subject != "" {
			label += " · " + th.Subject
		}

		if !selected && th.Unread == 0 {
			// 已读且未选中：压到最弱的一档，让未读那几条自己跳出来。
			lines = append(lines, f.indent(zenFaint.Render(truncate("  "+label, f.contentW))))
			continue
		}

		marker := "  "
		if selected {
			marker = "› "
		}
		style := zenText
		if th.Unread == 0 {
			style = zenMeta
		}
		lines = append(lines, f.indent(style.Render(truncate(marker+label, f.contentW))))
	}

	if status := m.renderZenStatus(f.contentW); status != "" {
		lines = append(lines, "")
		lines = append(lines, f.indent(status))
	}
	return f.fit(lines, m.height)
}

// zenListTop 是列表当前该从第几条开始画（跟随光标滚动）。
func (m Model) zenListTop(room int) int {
	top := 0
	if m.cursor >= room {
		top = m.cursor - room + 1
	}
	if max := len(m.visible) - room; top > max {
		top = max
	}
	if top < 0 {
		top = 0
	}
	return top
}

// viewZenChat 是会话屏。
//
// 它和 viewTwoPane / viewSinglePane 是**平级**的另一套排版，不是它们
// 的参数化版本 —— 两边除了「都把正文按内容折行」之外没有共同结构：
// Normal 是「列表 + 会话 + 状态栏」，Zen 是「一行头部 + 一列居中正文 +
// 一条分隔线 + 一个输入框」。硬合并只会得到一堆 if。
func (m Model) viewZenChat() string {
	f := m.zenFrame()

	status := m.renderZenStatus(f.contentW)
	// 可见行数走 chatViewport()，和 maxChatScroll / chatPageStep 用的是
	// 同一个数 —— 各算一遍迟早对不上，表现是「滚到底还差半行」。
	msgH := m.chatViewport()

	lines := make([]string, 0, m.height)

	th, ok := m.activeThread()
	if ok {
		lines = append(lines, f.indent(m.renderZenHeader(th, f.contentW)))
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, "")

	// 正文。tailWindow 只取末尾 msgH 行 —— 和 Normal 共用同一套
	// 「scroll 是从底部往上卷的行数」口径，两边的滚动键因此不用各写一遍。
	var body []string
	if ok {
		body = tailWindow(m.renderZenBody(th), msgH, m.scroll)
	} else {
		body = []string{f.indent(zenFaint.Render(truncate(m.zenEmptyHint(), f.contentW)))}
	}

	// 不够一屏时留白留在**上面**，正文贴着输入框。
	//
	// 这是聊天窗口的惯例，也是眼睛的落点问题：最新的一句话应该在「你正在
	// 打字的地方」正上方。反过来（内容顶着头、下面一片空）会让视线先落在
	// 空白区，再往上找字，每次进会话都要重来一遍。
	for i := len(body); i < msgH; i++ {
		lines = append(lines, "")
	}
	for _, l := range body {
		lines = append(lines, f.indent(l))
	}

	lines = append(lines, "")
	lines = append(lines, f.indent(zenRule.Render(strings.Repeat("─", f.contentW))))
	lines = append(lines, "")
	lines = append(lines, f.indent(m.input.View()))
	if status != "" {
		lines = append(lines, f.indent(status))
	}

	return f.fit(lines, m.height)
}

// zenEmptyHint 是没有打开会话时正文区那一句话。
func (m Model) zenEmptyHint() string {
	switch {
	case len(m.newChatTo) > 0:
		return "输入内容后回车，就会给 " + strings.Join(m.newChatTo, ", ") + " 发出第一封邮件"
	case len(m.visible) == 0:
		return "还没有会话。按 F2 回到普通视图，再按 n 新建一个。"
	default:
		return "按 F2 回到普通视图挑一个会话。"
	}
}

// renderZenHeader 是顶部那一行 —— 只有谁、几条。
//
// 居中、最弱的一档灰、没有边框没有图标。它是路标不是标题栏：
// 用户扫一眼知道「这是跟谁在说话」就够了，不需要更多。
func (m Model) renderZenHeader(th thread.Thread, width int) string {
	if len(th.Peers) == 0 {
		return ""
	}
	plain := strings.Join(peerNames(th), ", ")
	if n := len(th.Messages); n > 1 {
		plain += "  ·  " + strconv.Itoa(n) + " 条"
	}
	plain = truncate(plain, width)

	// 先按纯文本算居中的补白，再上色 —— 反过来 lipgloss.Width 会把
	// 转义序列算进宽度，居中的位置就偏了（styles.go 那条硬约束）。
	pad := (width - textWidth(plain)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + zenFaint.Render(plain)
}

// renderZenBody 把整个会话渲染成行（不裁剪）。
//
// 和 renderChatBody 一样是「渲染」而不是「渲染 + 裁剪」，因为
// 「最多能卷多少行」（maxChatScroll）要用未裁剪的总行数。
func (m Model) renderZenBody(th thread.Thread) []string {
	width := m.zenContentWidth()

	var out []string
	prevBlocky := false
	for i, it := range m.zenPresentation(th) {
		body := renderMarkdownThemed(it.Body, width, zenMDTheme)

		// 组内换条时，只要**两条里有一条不止一行**就补一个空行。
		//
		// 分组的意义是「一口气说的几句共用一个名字」，不是「把几条并成
		// 一个段落」。终端里没有气泡，唯一的消息边界就是留白：一串单行
		// 短句贴在一起仍然读得通（那正是分组的本意），但只要其中一条换过
		// 行 —— 标题、列表、代码块、长句折行 —— 两段正文就会粘成一段，
		// 读的人分不清哪句是上一条的结尾。
		//
		// 判据要**两边都看**：只看上一条会漏掉「单行短句后面跟一个标题」
		// 这种情形，而它恰恰是最容易读错的一种。
		if i > 0 && !it.GroupStart && (prevBlocky || len(body) > 1) {
			out = append(out, "")
		}

		out = append(out, m.renderZenItem(it, body, width)...)
		prevBlocky = len(body) > 1
	}
	return out
}

// renderZenItem 渲染一条消息。
//
// 分组的视觉全靠两件事：组头只出现一次，组尾留一个空行。
// 中间那几句既不重复名字也不加分隔 —— 它们本来就是一口气说的。
//
// body 是**已经渲染好的正文行**，由调用方传进来：它同时决定「这条占几行」，
// 而那个数字要在渲染下一条时用来决定要不要补空行。让两边各算一遍既浪费，
// 又会在改了配色或宽度之后悄悄对不上。
func (m Model) renderZenItem(it zenItem, body []string, width int) []string {
	right := it.Mine && m.zenRightAlign()
	place := func(line string) string {
		if right {
			return padLeft(line, width)
		}
		return line
	}

	out := make([]string, 0, len(body)+3)

	if it.GroupStart {
		head := it.Sender + " · " + it.Time.Local().Format("15:04")
		if it.HTML {
			// HTML 标记留着。它是「为什么这段排版和邮件原文不一样」的
			// 答案，属于内容来源，不是界面装饰 —— 去掉了用户看到排版差异
			// 只会以为是我们渲染坏了。用最弱的一档灰，不抢名字。
			head += "  HTML"
		}
		out = append(out, place(zenMeta.Render(truncate(head, width))))
		// 组头和正文之间空一行。分组靠的就是这个「名字出现一次、
		// 后面几句都归它」的视觉块，不留白就散成一条条了。
		out = append(out, "")
	}

	// 正文走的是和 Normal 完全同一份解析与折行，只有配色换成 Zen 那套
	// （见 mdTheme）。别在这里另写一套折行：markdown.go 里的折行是在上色
	// 之前做的，重写一遍必然踩转义序列那个坑。
	for _, line := range body {
		out = append(out, place(line))
	}

	if it.GroupEnd {
		out = append(out, "")
	}
	return out
}

// renderZenStatus 是底部那行**临时**状态。没有要说的就返回空串，
// 那一行会被正文吃掉。
//
// 常驻状态栏正是 Zen 要删掉的东西：在线状态、同步时间、账号名在
// 「读信」这个场景里一条都不需要，它们只在**出问题时**才有意义。
// 所以这里只在真的有事（同步中 / 报错 / 掉线）时才说话。
func (m Model) renderZenStatus(contentW int) string {
	switch {
	case m.busy:
		return zenFaint.Render("syncing…")
	case m.status != "":
		return zenError.Render(truncate(m.status, contentW))
	case m.app != nil && !m.connected:
		return zenFaint.Render("offline")
	// 提示排在最后：真实状态（同步中 / 报错 / 掉线）优先于它。
	// 反过来的话，一进 Zen 就掉线会看不到掉线。
	case m.zenShowHint:
		return zenFaint.Render(truncate(zenHintFor(m.zenScreen), contentW))
	}
	return ""
}

// ---- 按键 ----

// handleZenKey 处理 Zen 空间里的按键。
//
// 会话屏把大部分键让给 handleChatKey —— 输入、滚动、发信、刷新那些行为
// 和 Normal 会话完全一样，没有理由写第二遍。
func (m Model) handleZenKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 进 Zen 后那行提示只留到用户第一次按键为止。
	//
	// 放在分派**之前**：几乎所有按键都会在下面 return，写在后面就永远
	// 执行不到。f2 除外 —— 它自己会把提示重新点亮。
	if m.zenShowHint && msg.String() != "f2" {
		m.zenShowHint = false
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	// F2 = 回家。在会话屏或列表上按是回首页，**在首页按才是离开 Zen**。
	//
	// 为什么不做成「哪儿按都直接退出」：F2 是从会话里按进来的，进去之后
	// 再按同一个键，用户的预期是「回到刚才那个起点」而不是「把整个 Zen
	// 关掉」—— 关掉之后他就得重新按 F2、重新找会话。分两步退（会话→首页
	// →离开）和 Esc 那条「一次退一层」是同一个规矩。
	case "f2":
		if m.zenScreen == zenHome {
			m.toggleZen()
			return m, nil
		}
		m.goZen(zenHome)
		return m, nil
	}

	switch m.zenScreen {
	case zenHome:
		return m.handleZenHomeKey(msg)
	case zenList:
		return m.handleZenListKey(msg)
	default:
		return m.handleZenChatKey(msg)
	}
}

// handleZenHomeKey 是首页的按键。
//
// 首页只有一件事可做：写一句话。Tab 去列表，Esc / F2 退出 Zen。
func (m Model) handleZenHomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab":
		m.goZen(zenList)
		return m, nil
	case "esc":
		m.toggleZen()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		th, ok := m.zenHomeTarget()
		if !ok {
			m.status = "还没有会话可以发 —— 先按 Tab 挑一个"
			return m, nil
		}
		m.input.SetValue("")
		m.lastSentText = text
		m.busy = true
		// 发出去之后顺手把这个会话打开，让用户看见自己刚说的话落到了哪儿。
		m.activeID = th.ID
		m.scroll = 0
		return m, tea.Batch(m.replyCmd(text), m.loadActiveBodiesCmd())
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleZenListKey 是列表的按键。
//
// 只留导航和打开：文件夹、删除、星标这些「管理」动作仍然在 Normal 的列表上，
// Zen 的列表是「挑一个进去读」，不是「打理收件箱」。
func (m Model) handleZenListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "tab":
		m.goZen(zenHome)
		return m, nil
	case "up", "k":
		m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.moveCursor(1)
		return m, nil
	case "home", "g":
		m.cursor = 0
		m.clampCursor()
		return m, nil
	case "end", "G":
		m.cursor = len(m.visible) - 1
		m.clampCursor()
		return m, nil
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.visible) {
			return m, nil
		}
		next, cmd := m.enterThread(m.visible[m.cursor], false, false)
		if mm, ok := next.(Model); ok {
			// enterThread 会把输入框的占位文字设回 Normal 那套，这里补回来。
			mm.goZen(zenChat)
			return mm, cmd
		}
		return next, cmd
	}
	return m, nil
}

// handleZenChatKey 是会话屏的按键。
func (m Model) handleZenChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// 退一层：会话 → 列表。**不关会话** —— activeID 留着，回列表再按
		// 回车就回来了。这条和 Normal 那边「Esc 不丢会话」是同一个原则。
		m.goZen(zenList)
		return m, nil
	}
	return m.handleChatKey(msg)
}

// zenHomeTarget 是首页输入框发出去时该发给谁。
//
// 优先当前打开的会话；没有的话退回列表光标选中的那条 —— 首页上不该出现
// 「你还没选会话所以不能发」这种死路。
func (m Model) zenHomeTarget() (thread.Thread, bool) {
	if th, ok := m.activeThread(); ok {
		return th, true
	}
	if m.cursor >= 0 && m.cursor < len(m.visible) {
		return m.visible[m.cursor], true
	}
	return thread.Thread{}, false
}
