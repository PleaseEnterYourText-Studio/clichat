package tui

import (
	"strconv"
	"strings"
	"time"

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
)

// zenGroupWindow 是同一个人连着说的几句话被并成一组的时间窗。
//
// 15 分钟：短于它的连续发言算「一口气说的」，长于它的就该重新标一次时间。
// 太短会让每条消息都顶一个名字（那还不如不分组），太长会把上午说的和
// 下午说的并成一组，组头那个时间戳就成了谎话。
const zenGroupWindow = 15 * time.Minute

// zenChromeHeight 是 Zen 界面里除正文之外固定占掉的行数。
//
//	头部 1 + 头部后的空行 1 + 正文后的空行 1 + 分隔线 1 + 分隔线后的空行 1 + 输入 1
//
// 这六行是 Zen 的全部「界面」。Normal 在这个高度里要塞两栏、标题、
// 状态栏 —— 差的就是这些留白。
const zenChromeHeight = 6

// ---- 状态 ----

// zenActive 说当前是不是真的在用 Zen 布局。
//
// 两个条件都要满足：layout 选了 Zen，**且**当前处在会话里。
//
// 列表、搜索、文件夹选择器、确认框这些界面没有 Zen 形态 —— 它们本来就是
// 「管理」界面而不是「读」界面，做成留白排版只会把信息密度白白扔掉。
// 所以从会话退回列表时，layout 那个开关留在原地不动（用户按 F2 再进会话
// 时还是 Zen），但画出来的已经是 Normal 的列表。
func (m Model) zenActive() bool {
	return m.layout == layoutZen && (m.mode == modeChat || m.mode == modeNewChat)
}

// toggleZen 在 Normal 与 Zen 之间切换。
func (m *Model) toggleZen() {
	if m.layout == layoutZen {
		m.layout = layoutNormal
		m.input.Prompt = normalInputPrompt
		m.input.PromptStyle = lipgloss.NewStyle()
		m.input.Placeholder = chatInputPlaceholder
	} else {
		m.layout = layoutZen
		m.input.Prompt = zenInputPrompt
		m.input.PromptStyle = zenAccent
		m.input.Placeholder = zenInputPlaceholder
	}
	m.syncInputWidth()

	// 两种布局的可见行数不同，旧的滚动位置在新布局下可能越界。不收敛的话
	// 切过去会看到一片空白 —— clampChatScroll 的注释里记着同一个坑在
	// 窗口缩放时也踩过。
	m.clampChatScroll()
}

// syncInputWidth 让输入框的宽度跟着当前布局走。
//
// Zen 的正文列比终端窄，输入框不跟着收的话，敲到二三十个字就会顶出
// 正文列的右边界，把居中排版撑歪。Normal 下恢复成 0（不限制），
// 保持原有行为不变。
func (m *Model) syncInputWidth() {
	if !m.zenActive() {
		m.input.Width = 0
		return
	}
	w := zenContentWidth(m.width) - textWidth(m.input.Prompt)
	if w < 10 {
		w = 10
	}
	m.input.Width = w
}

// ---- 尺寸 ----

// zenContentWidth 是 Zen 正文列的宽度。
//
// 无论终端多宽，正文都不铺满。一行超过七八十个字符之后，眼睛从行尾回扫到
// 下一行开头就开始费劲 —— 这是排版常识，也是 Zen 和 Normal 最直观的差别：
// Normal 用满整幅（信息密度优先），Zen 主动留白（可读性优先）。
//
// 三段式：先按「两边各留 8 列」算，窄终端上退到「至少 56 列」，
// 超宽终端上封顶 78 列，最后兜一道「绝不能超出屏幕」。
// 最后那道是必需的 —— 前面为了凑 56 列会把宽度顶上去，40 列的终端上
// 直接溢出，横向滚动条就出来了。
func zenContentWidth(width int) int {
	const (
		maxContent = 78
		minContent = 56
	)
	w := width - 16
	if w < minContent {
		w = minContent
	}
	if w > maxContent {
		w = maxContent
	}
	if w > width-2 {
		w = width - 2
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

	for i := range items {
		items[i].GroupStart = i == 0 || zenStartsGroup(items[i-1], items[i])
		items[i].GroupEnd = i == len(items)-1 || zenStartsGroup(items[i], items[i+1])
	}
	return items
}

// ---- 渲染 ----

// viewZen 是 Zen 布局的全部。
//
// 它和 viewTwoPane / viewSinglePane 是**平级**的另一套排版，不是它们
// 的参数化版本 —— 两边除了「都把正文按内容折行」之外没有共同结构：
// Normal 是「列表 + 会话 + 状态栏」，Zen 是「一行头部 + 一列居中正文 +
// 一条分隔线 + 一个输入框」。硬合并只会得到一堆 if。
func (m Model) viewZen() string {
	width := m.width
	contentW := zenContentWidth(width)
	left := zenLeftPad(width, contentW)

	// 内容列居中：每行统一左移 left 格。
	indent := func(s string) string {
		if left <= 0 || s == "" {
			return s
		}
		return strings.Repeat(" ", left) + s
	}

	status := m.renderZenStatus(contentW)
	// 可见行数走 chatViewport()，和 maxChatScroll / chatPageStep 用的是
	// 同一个数 —— 各算一遍迟早对不上，表现是「滚到底还差半行」。
	msgH := m.chatViewport()

	lines := make([]string, 0, m.height)

	th, ok := m.activeThread()
	if ok {
		lines = append(lines, indent(m.renderZenHeader(th, contentW)))
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
		body = []string{indent(zenFaint.Render(truncate(m.zenEmptyHint(), contentW)))}
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
		lines = append(lines, indent(l))
	}

	lines = append(lines, "")
	lines = append(lines, indent(zenRule.Render(strings.Repeat("─", contentW))))
	lines = append(lines, "")
	lines = append(lines, indent(m.input.View()))
	if status != "" {
		lines = append(lines, indent(status))
	}

	// 兜底：终端特别矮时上面那些固定行可能已经超出，硬裁掉，
	// 免得 bubbletea 按超出的行数去排光标。
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for len(lines) < m.height {
		lines = append(lines, "")
	}

	// ⚠️ 每行都要补齐到**终端全宽**，不能只画到正文列右沿就收手。
	//
	// 不补的话，最长的那行（分隔线）只有「左留白 + 正文列」那么宽，
	// 右半边是**不存在**的空白 —— 于是：
	//
	//   - 真实终端里靠终端背景兜着，看着还行；
	//   - 但任何按「最长行」开窗的渲染器（截图链路里的 freeze 就是）
	//     会把窗口开成「左留白 + 正文列」宽，正文列立刻贴到右边去，
	//     整个居中排版全废。README 里那几张截图就是这么歪的。
	//
	// Normal 布局没这个问题：它的两栏加起来正好铺满，天然就是全宽。
	// Zen 主动留白，所以必须自己把这层留白**写出来**。
	for i, ln := range lines {
		lines[i] = padRight(ln, m.width)
	}
	return strings.Join(lines, "\n")
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
	width := zenContentWidth(m.width)

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
	}
	return ""
}
