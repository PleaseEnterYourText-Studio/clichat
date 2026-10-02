package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// View 渲染整个界面。
func (m Model) View() string {
	if !m.ready {
		return "正在启动 clichat…"
	}

	// Zen 是一套**平级**的排版，不是 Normal 的参数化版本。
	// 判定条件在 zenActive 里：layout 选了 Zen **且**当前在会话里。
	//
	// 它排在 mode 分派**之前**：进了 Zen 之后这一屏就是 Zen 自己的空间，
	// 由 zen.go 里的 zenScreen 管层次，不再借用 Normal 的页面排版。
	if m.zenActive() {
		return m.viewZen()
	}

	switch m.mode {
	case modeUnlock:
		return m.viewUnlock()
	case modeSetup:
		return m.viewSetup()
	case modeHelp:
		return m.viewHelp()
	case modeFolder:
		return m.viewFolderPicker()
	}

	l := m.measureLayout()
	if !l.twoPane {
		return m.viewSinglePane(l)
	}
	return m.viewTwoPane(l)
}

// ---- 尺寸与布局 ----

// maxTabs 是顶部会话标签页最多显示几个。
//
// 4 个是「一眼扫完」的上限：再多就得靠读，而这一行的全部意义就是
// 不用读 —— 想切会话的人按 Ctrl+↑/↓ 更快。
const maxTabs = 4

// layout 是整个界面的几何：每个区域在屏幕上的行与列。
//
// **渲染和鼠标命中测试共用这一份。** 这件事不是洁癖 —— 两处各算一遍
// 坐标，迟早有一处被改而另一处忘了，表现是「点到的位置和看到的不一致」，
// 而且只在某些窗口尺寸下才错。那种 bug 极难复现，所以从源头掐掉：
// 只要画的时候用的是这个结构，点的时候也用这个结构，就不可能漂移。
type layout struct {
	twoPane bool

	// tabsRow 是顶部会话标签页所在的行；-1 表示这一行不存在
	// （单栏模式、或者最近没开过第二个会话）。
	tabsRow int

	// bodyTop / bodyH 是主体区（各栏）的行范围。
	//
	// bodyTop 同时也是**列表栏标题行**所在的行：双栏时它是列表栏的第一
	// 行（有标签页时那一行在它上面），点它换文件夹。
	bodyTop int
	bodyH   int

	// inputTop / inputRows 是浮起输入区的行范围。
	inputTop  int
	inputRows int

	statusRow int

	// 横向：会话列表列 / 一列留白 / 会话流。
	//
	// 单栏模式下只有 list 有效（list 和 chat 都是整幅宽，互斥显示），
	// ruleX 是 -1（没有那一列）。
	listX, listW int
	// ruleX 是那 1 列留白的**唯一一列**（终端坐标）。
	//
	// ⚠️ 名字留着，但它现在**不画任何字符** —— 名字指的是"那一列的规矩"，
	// 不是"那条线"（见 view.go 的 rail：分区改成靠留白，一个字符都不画）。
	//
	// 它仍然是一列实打实占位的**格**，不是"画在某一栏的边上"：列表栏和
	// 正文栏都不占这一列，于是两栏各自的宽度算法里不必再各减一次。
	//
	// **这一列不能收掉。** 它同时是鼠标命中测试划分"点的是列表还是正文"
	// 的依据（见 model.go 的 handleClick 和 layout_test.go 的
	// TestMouse_EveryBodyColumnBelongsToAPane）：收掉它两栏就贴在一起，
	// 边界变成 0 列，点在交界处会漏判。
	ruleX int
	// chatX 是正文栏的左沿（= ruleX + 1）。
	chatX, chatW int

	// contentX 是内容区左边缘，contentW 是它到最右一列之间的列数（末列
	// 留给一格右边距）。
	//
	// 内容区里的三样东西 —— 顶部标签页、输入行、状态栏 —— 都从
	// 这里起步。各自少缩进一格的话，一眼就能看出左边缘参差不齐，而
	// 「对齐」这种事没人会专门去提 bug，只会觉得「说不上哪里别扭」。
	//
	// 双栏时它和 chatX 同一个值（内容区就是正文栏，列表栏不算）；单栏
	// 模式下它是 0。
	contentX, contentW int

	// paneX / paneW 是**正文栏里内容的起点与可用宽度**（终端坐标）。
	//
	// 和 contentX 差在哪：contentX 是「正文栏左沿」，底部那几行原来从
	// 那里起步，于是输入行紧贴着两栏的分界；paneX 是「左沿再内缩一格」，
	// 底部三样（标签页 / 输入行 / 状态栏）都排在这里，和上面的引用块、
	// 标题左对齐。
	//
	// 单栏模式下它就是整屏（paneX=0），和 contentX 等价。
	paneX, paneW int
}

// measureLayout 算一遍界面几何。
//
// ⚠️ 它**不能**叫 `layout()`：Model 上有个同名字段 `layout`（类型是
// layoutMode，Normal / Zen 那个视觉维度 —— 见 model.go）。Go 里字段和
// 方法不能重名，撞上就是编译错误。改名时选了动这一侧：`zen.go` 整个是
// 上游的、我们一行没动，在那 8 处引用上改名会让以后每次合并上游都在
// 同一个地方冲突。类型名 `layout` 没动 —— 包级类型和结构体字段不冲突。
func (m Model) measureLayout() layout {
	var l layout
	l.twoPane = m.width >= singlePaneWidth

	row := 0
	l.tabsRow = -1
	// 标签页只在双栏、并且真的有得切的时候才出现。
	// 只有一个会话时它既没内容也没用，白占一行。
	if l.twoPane && len(m.tabThreads()) >= 2 {
		l.tabsRow = row
		row++
	}
	l.bodyTop = row

	l.statusRow = m.height - 1
	l.inputRows = m.inputBlockRows()
	l.inputTop = l.statusRow - l.inputRows
	if l.inputTop < l.bodyTop+1 {
		// 终端太矮，留白先让给正文：输入区退回单行贴底。
		l.inputRows = 1
		l.inputTop = l.statusRow - 1
	}

	l.bodyH = l.inputTop - l.bodyTop
	if l.bodyH < 1 {
		l.bodyH = 1
	}

	if l.twoPane {
		// 两栏 + 中间一条竖线。列表栏从第 0 列起步（导航列没有了，
		// 见 styles.go 里 singlePaneWidth 那段说明）。
		l.listX = 0
		l.listW = m.listWidth()
		l.ruleX = l.listX + l.listW
		l.chatX = l.ruleX + 1
		l.chatW = m.width - l.chatX
		if l.chatW < 1 {
			l.chatW = 1
		}
	} else {
		l.listX, l.listW = 0, m.width
		l.ruleX = -1
		l.chatX, l.chatW = 0, m.width
	}

	l.contentX = l.chatX
	l.contentW = m.width - l.contentX - 1
	if l.contentW < 1 {
		l.contentW = 1
	}

	// 正文栏内容的起点。单栏模式下正文栏就是整屏（paneX=0、paneW 等于
	// contentW），底部那几行的算法因此只有一套。
	if l.twoPane {
		l.paneX = l.chatX + paneInset
		l.paneW = m.width - l.paneX - 1
	} else {
		l.paneX, l.paneW = 0, l.contentW
	}
	if l.paneW < 1 {
		l.paneW = 1
	}
	if l.paneX < 0 {
		l.paneX = 0
	}
	return l
}

// inputBlockRows 是浮起输入区占的行数。
//
// 高度不够时退回 1 行（贴底的单行）—— 留白是奢侈品，正文优先。
// 12 行是「3 行输入区之后还剩 8 行给正文」的下限，再矮就没法读了。
func (m Model) inputBlockRows() int {
	if m.height < 12 {
		return 1
	}
	return inputBlockHeight
}

// listWidth 是左侧会话列表栏的宽度。
//
// 取 width/4（22..30）。这个数原来是「width/3 减去一条 14 列的导航列」
// 折出来的；导航列删掉之后算式可以放宽回 width/3，但**没有放宽**：
// 列表只要能放下「Alice, Bob」这样的名字就够，主题本来就会被截断，
// 而多出来的列给正文 —— 正文是所有东西里唯一会被折行的那一栏。
func (m Model) listWidth() int {
	w := m.width / 4
	if w < 22 {
		w = 22
	}
	if w > 30 {
		w = 30
	}
	return w
}

// bodyHeight 是主体区的高度（含每栏自己的标题行）。
func (m Model) bodyHeight() int {
	return m.measureLayout().bodyH
}

// chatPaneWidth 是会话流那一栏的总宽度（含最右那一列滚动条）。
//
// 双栏时它是右栏 —— 列表栏和中间那根竖线之外的全部；
// 单栏时就是整幅。抽出来是因为「正文折成几行」和「正文画多宽」必须是
// 同一个数，两边各算一遍的话，改了布局里的一处、另一处就开始骗人。
func (m Model) chatPaneWidth() int {
	return m.measureLayout().chatW
}

// chatBodyWidth 是正文文字可用的宽度 —— 从 chatPaneWidth 里让出最右一列
// 给滚动条。
//
// 让出的一列是**恒定**的，哪怕当前根本不需要滚动条：宽度一变折行位置
// 就跟着变，正文行数随之变，于是「有没有滚动条」可能因为行数变少而翻转，
// 宽度再变回来 —— 一个来回抖动的反馈回路。恒定预留一列没有这个问题。
func (m Model) chatBodyWidth() int {
	if w := m.chatPaneWidth() - 1; w >= 1 {
		return w
	}
	return 1
}

// chatViewHeight 是会话正文区能显示的行数。
//
// 传进来的是 bodyH（标题 + 正文 + 补白那一整块），减 1 是顶部那行
// 会话标题。单栏双栏都一样：renderChat 永远先画一行标题。
func chatViewHeight(bodyH int) int {
	if bodyH < 2 {
		return 1
	}
	return bodyH - 1
}

// ---- 主布局 ----

// viewTwoPane 拼两栏：会话列表 / 一列留白 / 会话流。
//
// **没有底色块，也没有线。** 两块区域的分界是中间那一格留白：
//
//	[ 会话列表：终端背景 ][ ][ 会话流：终端背景 ]
//
// ⚠️ 这是**连推翻两版**的结果，两版各自错在不同的地方，值得都记下来。
//
// 上一版走"用底色代替线条"：列表铺 235、文件夹栏铺 237，靠深浅分家。推导
// 没错（要靠画线才能说明分区，说明层次没做出来），但落地后一屏里同时坐着
// 三块亮度不同的灰板，眼睛进来先读到"三个格子"而不是"一堆邮件"。用户的
// 原话是「割裂」。
//
// 再上一版（也就是刚撤掉的那版）走"用线代替底色"：一条 1 列宽的 │ 从头
// 贯到底。它确实比三块灰板安静，但一条贯穿整屏的实线**仍然是一笔画出来的
// 框** —— 它把画面切成两块硬边，只是比底色板轻一点。
//
// 现在两者都不要，分区交给**留白**：两栏之间隔一格空白，一个字符都不画。
// 一屏里只剩终端背景这一个"面"，可操作的东西才浮起来 —— 深色终端上这
// 正是它该有的样子：安静是默认值，强调是例外。
//
// 现在唯一还铺底色的地方是**选中的那一行**（rowSelectedBg）。底色只表达
// 「这里是一块你现在能动的东西」，不再承担任何分区职责。
//
// ⚠️ 中间那一格**仍然占 1 列**（见 rail / withRail）。它不画字符，但它是
// layout.ruleX —— 鼠标命中测试按它划分"点的是列表还是正文"。收掉它两栏
// 就贴在一起，边界变成 0 列，点在交界处会漏判。
func (m Model) viewTwoPane(l layout) string {
	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderThreadList(l.listW, l.bodyH),
		m.rail(l.bodyH),
		m.renderChat(l.chatW, l.bodyH),
	)
	// 正文栏的每一行内容都从 paneX 起步（= chatX + paneInset），所以这里
	// 统一补那 1 格 —— 补在前面而不是让 renderTabs / renderStatus 各自加，
	// 是为了让"左对齐"只有一处出处。
	pad := strings.Repeat(" ", paneInset)

	out := make([]string, 0, m.height)
	if l.tabsRow >= 0 {
		out = append(out, m.withRail(pad+m.renderTabs(l), l))
	}
	out = append(out, strings.Split(panes, "\n")...)
	out = append(out, m.renderInputBlock(l)...)
	out = append(out, m.withRail(pad+m.renderStatus(l.paneW), l))
	return strings.Join(out, "\n")
}

func (m Model) viewSinglePane(l layout) string {
	var body string
	if m.mode == modeChat || m.mode == modeNewChat {
		body = m.renderChat(l.chatW, l.bodyH)
	} else {
		body = m.renderThreadList(l.listW, l.bodyH)
	}

	out := strings.Split(body, "\n")
	out = append(out, m.renderInputBlock(l)...)
	out = append(out, m.renderStatus(l.contentW+1))
	return strings.Join(out, "\n")
}

// rail 是列在列表栏和正文栏之间的那一格。
//
// 它单独立成一块、由 JoinHorizontal 拼进去，而不是让某一栏自己在边上多画
// 一列：两栏各自的宽度算式里因此都不必再减一次那一格，也就不会出现
// 「减了两遍」或者「一边减一边没减」这种只在某一种窗口宽度下才露头的错。
//
// ⚠️ 这一版**不画竖线**了，那一格是**空白**。
//
// 上一版在这里画一条贯到底的 │，理由是「底色只表达『这里可操作』，分区
// 交给一条线」。那条分工本身还成立 —— 撤掉线是因为用户的原话是「不要太
// 繁杂」：一列从头贯到尾的实线是一道**贯穿整屏的笔画**，它把画面切成两块
// 硬边，比两块底色板的割裂感轻，但仍然是「画出来的框」。两栏之间隔一格
// 空白，眼睛照样分得清是两块 —— 留白分区，一个字符都不画。
//
// ⚠️ 这一格**必须继续存在**（仍然是 1 列宽），不能收掉让两栏贴在一起：
// 它同时是 layout.ruleX —— 鼠标命中测试按它划分「点的是列表还是正文」
// （见 model.go 的 handleClick）。收掉它，两栏的边界就变成 0 列，
// 点在交界处会漏判。
func (m Model) rail(height int) string {
	if height <= 0 {
		return ""
	}
	col := make([]string, height)
	for i := range col {
		col[i] = " "
	}
	return strings.Join(col, "\n")
}

// withRail 给一行的左边补上「列表栏 + 中间那一格」两段。
//
// 底下的标签页、输入区、状态栏不走 renderThreadList，不补这一下，它们
// 就会整体左移一格，和上面的内容错开 —— 而「对齐」这类事没人会专门提
// 一个 bug，只会觉得「说不上哪儿别扭」。
//
// 补的是「listW 格空白 + 一格空白」，正好把 line 推到 chatX + paneInset。
func (m Model) withRail(line string, l layout) string {
	if l.ruleX < 0 {
		return line
	}
	return strings.Repeat(" ", l.listW+1) + line
}

// ---- 文件夹选择器 ----
// 注：这里原本是「左侧导航列」的整套渲染（navRows / navItemRow /
// navRowIndexAt 加上组标题与项的两级缩进）。那一列被删掉了 —— 它只有
// 四五个固定入口，却常驻占掉 14 列，用户的原话是「单独占一列完全浪费」。
//
// 现在同一批数据（navGroups）由**按需唤出的文件夹选择器**呈现：内容一样
// （含未读计数），只是不再常驻。渲染在 renderFolderPicker 那一段。
// ---- 顶部标签页 ----

// renderTabs 画顶部那一排会话标签页。
//
// 按**列**拼而不是用 JoinHorizontal：每个标签的起点来自 tabChips，
// 和鼠标命中测试用的是同一份坐标。中间的空隙也照坐标补，于是
// 「看到的」和「点到的」在结构上就不可能错开。
func (m Model) renderTabs(l layout) string {
	chips := m.tabChips(l)
	if len(chips) == 0 {
		return ""
	}

	var b strings.Builder
	col := 0
	for _, c := range chips {
		for col < c.x0 {
			b.WriteByte(' ')
			col++
		}
		if c.marker != "" {
			// 溢出指示：那一侧还有标签没显示。用最暗的一档，它是个路标
			// 而不是内容 —— 亮起来会和真标签抢注意力。
			b.WriteString(styleMuted.Render(c.marker))
			col = c.x1
			continue
		}
		text := padRight(" "+c.label+" ", c.x1-c.x0)
		if c.id == m.activeID {
			b.WriteString(styleTabActive.Render(text))
		} else {
			b.WriteString(styleTabIdle.Render(text))
		}
		col = c.x1
	}
	// 不在这里补左边的面板带：调用方（viewTwoPane）补，因为那 1 格内缩
	// 是正文栏内容共有的，标签页只是其中一个使用者。
	return b.String()
}

// tabChipAt 找出落在列 x 上的那一格（可能是标签，也可能是溢出指示符）。
//
// x 是**屏幕列**（鼠标传来的是什么就是什么）。标签的坐标是正文栏里的
// 相对列，所以这里减一次 paneX —— 和 viewTwoPane 里 withPaneStrip 补的
// 面板带 + 那 1 格内缩加起来正好抵消。少了这一步，标签画在哪儿和点得着
// 哪儿就差一个「侧栏 + 列表栏 + 1」，而只比对内部字段的判据全绿。
func (m Model) tabChipAt(l layout, x int) (tabChip, bool) {
	x -= l.paneX
	for _, c := range m.tabChips(l) {
		if x >= c.x0 && x < c.x1 {
			return c, true
		}
	}
	return tabChip{}, false
}

// ---- 会话列表 ----

// listRow 是会话列表里画出来的一行。
//
// 之所以要有「行」这一层、而不是直接按下标画 m.visible：分组（未读 / 已读）
// 和每条会话占两行这两件事，都会让「第几行」和「第几条会话」不再是一回事。
// 渲染、窗口滚动、鼠标命中测试三处都必须用**同一套换算**，所以换算只有
// 这一个出处 —— 各自算一遍的话，会分头漂移，而且只在有未读、或者窗口
// 刚够放下某几条的时候才错。
type listRow struct {
	// group 非空表示这是一行分组标题。
	group string
	// idx 是这一行对应的会话在 m.visible 里的下标；标题行是 -1。
	idx int
	// first 表示这是该会话的第一行（标题行，第二行是主题）。
	first bool
}

// listRows 把当前列表摊成待渲染的行。
//
// 分组只在**两组都有内容**时才画标题：全是未读或全是已读的时候，
// 一条孤零零的「未读 37」只是白占一行。
func (m Model) listRows() []listRow {
	if len(m.visible) == 0 {
		return nil
	}

	var unread, read []int
	for i, th := range m.visible {
		if th.Unread > 0 {
			unread = append(unread, i)
		} else {
			read = append(read, i)
		}
	}

	out := make([]listRow, 0, len(m.visible)*2+2)
	both := len(unread) > 0 && len(read) > 0
	add := func(title string, idxs []int) {
		if both {
			out = append(out, listRow{group: title, idx: -1})
		}
		for _, i := range idxs {
			out = append(out, listRow{idx: i, first: true}, listRow{idx: i})
		}
	}
	add(fmt.Sprintf("未读 %d", len(unread)), unread)
	add(fmt.Sprintf("已读 %d", len(read)), read)
	return out
}

// cursorRowIndex 是光标所在会话的**标题行**在 rows 里的下标。
//
// 拿标题行而不是主题行：窗口滚动时要把它作为一对的起点。
// 找不到返回 -1（光标还没落到任何一条上）。
func (m Model) cursorRowIndex(rows []listRow) int {
	for i, r := range rows {
		if r.first && r.idx == m.cursor {
			return i
		}
	}
	return -1
}

// listWindow 算出列表该画 rows 的哪一段 [start, end)。
//
// height 是整个列表栏的高度（含标题行）。窗口跟着光标走，保证光标那
// 一对（标题 + 主题）始终在窗口里。
func (m Model) listWindow(rows []listRow, height int) (int, int) {
	capacity := height - 1 // 第一行是「会话 · INBOX」那个标题
	if capacity < 1 {
		capacity = 1
	}
	if len(rows) <= capacity {
		return 0, len(rows)
	}

	start := 0
	if cr := m.cursorRowIndex(rows); cr >= 0 {
		if cr >= capacity {
			start = cr - capacity + 1
		}
		// 光标那一对的两行都得进来：只露标题、主题被切掉，
		// 看着就像这封邮件少了一行。
		if cr+2 > start+capacity {
			start = cr + 2 - capacity
		}
		if start > cr {
			start = cr
		}
	}
	if start < 0 {
		start = 0
	}
	// 起点对齐到某条会话的标题行 —— 从主题行开始画，上面那封邮件的
	// 标题就被切掉了，而它并没有被滚过去，只是被截断。
	for start > 0 && start < len(rows) && !rows[start].first {
		start++
	}
	end := start + capacity
	if end > len(rows) {
		end = len(rows)
	}
	if start >= end {
		start = 0
		end = capacity
		if end > len(rows) {
			end = len(rows)
		}
	}
	return start, end
}

// renderThreadList 画左侧的会话列表。
//
// **不铺底色。** 整栏直接坐在终端背景上，和右边的正文栏是同一个"面" ——
// 两栏之间只有一条 1 列宽的竖线（见 viewTwoPane 里那段：上一版的
// 列表 235 / 侧栏 237 两层灰板一屏同现，读起来像被切成了三块）。
// 列表行**内部**仍然有底色，但那是「这一条被选中了」的信号，不是分区。
//
// 标题行是**当前文件夹**，而且可以点（点它弹文件夹选择器，和 Tab 等价）。
// 带上未读总数是因为过滤生效时如果不显示，用户会以为邮件丢了；搜索词
// 也一样 —— 搜索是可以回车保留的，不留痕迹的话，用户会以为「列表里就剩
// 这几封了」。
func (m Model) renderThreadList(width, height int) string {
	lines := make([]string, 0, height)
	lines = append(lines, styleTitle.Render(truncate(m.listHeader(), width-2)))

	if len(m.visible) == 0 {
		lines = append(lines, styleMuted.Render(truncate("  "+m.emptyListHint(), width-2)))
		return fillPane(lines, width, height)
	}

	rows := m.listRows()
	start, end := m.listWindow(rows, height)
	for _, r := range rows[start:end] {
		lines = append(lines, m.renderListRow(r, width))
	}
	return fillPane(lines, width, height)
}

// listRowInset / listRowPad 是列表行的两道横向余量。
//
//	│← listRowInset →│            │← listRowInset →│
//	│   ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓   │   ← 选中的底色块
//	│   ↑ listRowPad             ↑ │   ← 文字和块边之间
//
// inset 让底色块**两侧各内缩一格**：通栏的色块看着像表格的斑马纹，
// 内缩之后才像「一条被选中的东西」。pad 是块内的内边距，让文字不贴着
// 块的边缘。
const (
	listRowInset = 1
	listRowPad   = 1
)

// listRowInner 是列表行里**文字**可用的列数。
func listRowInner(width int) int {
	inner := width - 2*listRowInset - 2*listRowPad
	if inner < 4 {
		inner = 4
	}
	return inner
}

// renderListRow 画列表里的一行。
//
// 两种形态：
//   - 选中的那一条：两行**都**铺同一块底色，块内文字用**同一个**样式。
//     块里不做分段着色 —— 块内的字色深浅不一，整块底色的边界就看不清了，
//     反而比反白更乱。
//   - 其余：按段上色（标记用强调色、名字按未读与否、时间用灰）。
//
// 两条路径都从 listRowText 取得**纯文本**再补宽，所以「块有多宽」这件事
// 只有一个出处 —— 分段版本自己算一遍宽度的话，选中/未选中切换时文字会
// 左右跳一格。
func (m Model) renderListRow(r listRow, width int) string {
	inner := listRowInner(width)
	inset := strings.Repeat(" ", listRowInset)

	if r.group != "" {
		// 分组标题也走同一套内缩，于是它和条目共用左边那条基准线；
		// 计数右对齐到同一列，右边缘也齐。
		return inset + styleGroupTitle.Render(padRight(listGroupText(r.group, inner), inner+2*listRowPad)) + inset
	}

	th := m.visible[r.idx]
	if r.idx == m.cursor {
		return inset + styleRowSelected.Render(
			padRight(m.listRowText(th, r.first, inner), inner)) + inset
	}
	return inset + padRight(m.listRowStyled(th, r.first, inner), inner) + inset
}

// listGroupText 拼分组标题的纯文本：标题靠左、计数靠右，中间留白。
//
// 右对齐不是为了好看：分组标题和条目共用同一条右基准线之后，整栏的右边缘
// 才是一条直线，而不是长短不一的一排。上一版「未读 5」四个字挤在一起，
// 右边缘是毛的。
func listGroupText(group string, inner int) string {
	// group 的形状是「未读 5」—— 标题和计数之间是最后一个空格。
	title, count, found := strings.Cut(group, " ")
	if !found {
		return truncate(group, inner)
	}
	gap := inner - textWidth(title) - textWidth(count)
	if gap < 1 {
		// 太窄就退回原来的连写形式，别把标题截成一个字。
		return truncate(title+" "+count, inner)
	}
	return title + strings.Repeat(" ", gap) + count
}

// 列表行的两列宽度预算。
//
// 那一行是「标记 名字 …… 时间」，右边两样都是**定宽列**。上一版不是：
// 名字的宽度是 `inner - markWidth - textWidth(timeStr) - 2` —— 时间串多长，
// 名字就少多长。上一版的时间是定长的（5 列或 4 列），勉强看不出问题；
// 改成「前天 15:30」「2026-4-1 15:23」之后长度从 5 摇到 15，**同一个列表
// 里每一行的名字宽度都不一样**，名字右边参差不齐、时间也被挤到各处 ——
// 用户说的「元素堆砌挤压」有一半是这么来的。
//
// 现在的规矩：**两个都在宽度固定的列里，右对齐到栏的右边缘**。名字那一
// 列的宽度对所有行都相同，时间的右边缘对所有行都相同，中间的空隙随
// 时间串长短变化 —— 空隙变化是看不见的，参差是看得见的。
const (
	// listTimeW 是时间列的目标宽度。11 列够放「前天 15:04」（10 列）
	// 和「2026-9-25」（9 列），也就是退到**带日子锚点**的最后一档。
	listTimeW = 11
	// listTimeMinW 是时间列能让到的最小宽度：5 列，正好一个 `15:04`。
	// 再窄就连时分都放不下，时间整列让掉（见 listTimeField 的返回 0）。
	listTimeMinW = 5
	// listNameW 是名字列的**下限**（12 列 ≈ 6 个汉字）。
	//
	// 半角名字在这一档能放 12 个字符（`HTML2MD Geri`），日常的用户名都
	// 够；中文名 6 个字也够。再长的靠 truncate 出省略号。
	listNameW = 12
)

// listTimeField 返回列表行里**时间列**占的列数（0 表示这一行不画时间）。
//
// 它只取决于栏宽，**不取决于时间串的长短** —— 这是上面那两条定宽列的
// 前提。谁在别处"顺手"改成按 timeStr 算宽度，参差就会回来。
func listTimeField(width int) int {
	// 标记区 + 名字和时间之间那两格间隔。
	avail := width - markWidth - 2
	// 连「名字的下限 + 一个时分」都放不下时，这一行只画名字：名字是
	// 这一行的主语，时间只是修饰，窄到这一步该让的是时间。
	if avail < listNameW+listTimeMinW {
		return 0
	}
	w := avail - listNameW
	if w > listTimeW {
		w = listTimeW
	}
	return w
}

// listCells 是列表第一行被切成的三段。
//
// 三个字段都是**已经补好宽度的定宽格子**（mark 除外，它本身就是 markWidth
// 列）。plain 拼出来的就是这一行的纯文本，上色那一版逐段套样式即可 ——
// 两处共用同一套几何，不会出现「改了配色顺手改了宽度」。
type listCells struct {
	mark string // 2 列：未读块 + 星标
	name string // nameW 列，右补空格
	time string // timeW 列，左补空格（右对齐）；没有时间时是空串
}

// plain 拼出这一行的纯文本。
func (c listCells) plain() string {
	out := c.mark + " " + c.name
	if c.time != "" {
		out += " " + c.time
	}
	return out
}

// listRowSplit 把「标记 + 名字 + 时间」这三格算出来，返回**纯文本**的各段。
//
// timeW 只由 width 决定（见 listTimeField），所以同一列表里每一行的时间
// 右边缘都是栏的右边缘，名字右边缘也都对齐在同一列 —— 相邻两行不会互相
// 错开。这是这一版列表能"读得动"的几何基础。
func listRowSplit(th thread.Thread, width int) listCells {
	timeW := listTimeField(width)
	timeStr := ""
	if timeW > 0 {
		// 时间的写法按预算取档（listTime），且**右对齐到定宽的列里**：
		// 不同档长短不一（`15:04` 和 `2026-9-25`），左对齐会让这一列
		// 看起来像被啃过。
		timeStr = padLeft(listTime(th.LastDate, timeW), timeW)
	}
	nameW := width - markWidth - 2 - timeW
	if nameW < 1 {
		nameW = 1
	}
	return listCells{
		mark: listMark(th),
		name: padRight(truncate(threadTitle(th), nameW), nameW),
		time: timeStr,
	}
}

// listRowText 是列表某一行的**纯文本**（未上色）。
//
// 先拼纯文本、再上色是硬约束：带上转义序列之后再截断，会把序列剪断
// （styles.go 里那条）。
//
// ⚠️ 它和 listRowStyled 必须**逐字对齐**：同一个 th、同一个 width，
// 两者去样式之后必须一模一样。TestList_StyledRowMatchesItsPlainText
// 守着这一点 —— 这是唯一能挡住「改了配色顺手把宽度也改了」的判据。
func (m Model) listRowText(th thread.Thread, first bool, width int) string {
	if !first {
		// 副行缩到**名字那一列**（标记区 + 一格间隔），和名字共用一条
		// 左边缘。
		//
		// 上一版这里是写死的两个空格，而名字在 markWidth+1 = 3 列处 ——
		// 也就是副行比名字**靠左一格**。一格而已，但一栏里十条会话就有
		// 十条这样的错位，整栏的左边缘是毛的。用户说的「元素堆砌挤压」，
		// 一半是灰度没拉开（见 listRowStyled），另一半就是这个：两行
		// 本该是一个块，却各自站开了一格。
		indent := strings.Repeat(" ", markWidth+1)
		return truncate(indent+m.listSubLine(th), width)
	}
	return listRowSplit(th, width).plain()
}

// listSubLine 是列表副行的文字：**这条会话里最新一条消息说了什么**。
//
// 它以前放的是主题，现在放正文摘要 —— 用户的原话是「消息列表展示最新
// 一条消息」。两者回答的问题不一样：主题回答「这段对话叫什么」，摘要
// 回答「现在讲到哪儿了」。群里聊到第三十轮的时候，主题还停在第一轮的
// 那个名字上，而列表真正该告诉你的是刚来的那句。
//
// 三级兜底，顺序不能换：
//
//  1. 摘要 —— 已经拉到的那一条正文（见 Model.previews）。
//  2. 主题 —— 摘要还没到、拉不到、或者那封本来就没有正文时。
//  3. 「(无主题)」—— 连主题都没有（生成端理论上不会产出这种，存量数据
//     里可能有）。副行留空比放一句占位更糟：空白看起来像「这条会话坏了」。
//
// ⚠️ 摘要没到就退回主题，是这一条**必须**成立的性质：摘要是一次网络
// 往返，而列表在它回来之前就已经画出来了。没有兜底的话，开机的头几百
// 毫秒里所有副行都是空的 —— 那正是用户对「新版本」的第一印象。
//
// 自己发的那条前面标「我: 」：摘要是正文原文，不标的话，一句「好的」
// 会看起来像对方说的（名字那一行是对方的名字）。
func (m Model) listSubLine(th thread.Thread) string {
	id := listPreviewID(th)
	if p := m.previews[id]; p != "" {
		if m.cfg != nil && th.Messages[len(th.Messages)-1].From == m.cfg.Self() {
			return "我: " + p
		}
		return p
	}
	if th.Subject != "" {
		return th.Subject
	}
	return "(无主题)"
}

// listMark 是列表第一行行首那两列的标记。
//
// 两个标记各有各的格子：未读在左、星标在右。**列宽固定**，所以有没有
// 标记都不会让后面的名字错位 —— 用「拼接」而不是「条件拼接」就是为这个。
func listMark(th thread.Thread) string {
	left, right := " ", " "
	if th.Unread > 0 {
		left = blockMark
	}
	if th.IsStarred() {
		right = starMark
	}
	return left + right
}

// listRowStyled 是列表第一行的**带样式**版本。
//
// 分段上色，三段各是一个**层级**，用的正是用户要的那套「深浅灰阶」：
//
//	标记    强调色（141）+ 粗体   ← 唯一的"状态"用色，一眼能扫到
//	名字    未读：默认前景 + 粗体  ← 最亮，这是这一行的主语
//	        已读：fgMuted          ← 明显弱一档，铺开看就是「这一屏哪些没读」
//	时间    fgTime                 ← 最弱，背景信息
//
// ⚠️ 已读的名字**必须**降一档，这一条是这一版的重点。
//
// 上一版未读用 styleTitle（粗体），已读用**默认前景色**——于是"读过"和
// "没读过"只差一个粗体，在满屏都是默认色的列表里根本看不出来。用户的原话
// 是「元素堆砌挤压」，而"堆砌"的第一层含义就是**所有字一样重**：名字、时间、
// 摘要挤在一起，眼睛得逐字读才知道哪个是哪个。拉开灰度之后，"哪几条还没
// 读"是一眼的事，"哪一段是名字"也是一眼的事。
//
// 副行（listRowStyled 的 !first 分支）用 fgDim，比已读名字再弱半档 ——
// 它排在名字之下，层级也该在名字之下。
func (m Model) listRowStyled(th thread.Thread, first bool, width int) string {
	if !first {
		return stylePreview.Render(m.listRowText(th, first, width))
	}

	c := listRowSplit(th, width)

	// 标记区两格，各自按有无上色；空格原样留着 —— 它占位，保证有没有
	// 标记后面的名字都不会错位。
	left, right := " ", " "
	if th.Unread > 0 {
		left = styleUnread.Render(blockMark)
	}
	if th.IsStarred() {
		right = styleUnread.Render(starMark)
	}

	// 名字：未读最亮、已读降一档。注意**不是**「未读才上色」—— 那样
	// 已读会落到默认前景（也就是最亮的那一档），层次正好反了。
	//
	// `c.name` 已经补过宽度，直接上色就行；样式里不能带背景，否则
	// 右边那些补位空格会变成一条色带（见 styleRowName* 的注释）。
	nameStyle := styleRowNameRead
	if th.Unread > 0 {
		nameStyle = styleRowNameUnread
	}

	out := left + right + " " + nameStyle.Render(c.name)
	if c.time != "" {
		out += " " + styleTime.Render(c.time)
	}
	return out
}

// timeForms 返回同一个时间点**由详到简**的几种写法。
//
// 第 0 个是完整形式（消息头用 —— 那一行有地方）；后面的是窄栏的退路
// （会话列表右侧那一列只有 10 来列）。
//
// ⚠️ 所有形式由**同一个 switch** 生成，而且性质是**逐级做减法**：
// 后一个必须是前一个的子串（`2026-9-25` 是 `2026-9-25 02:19` 的子串，
// `9-25` 又是 `2026-9-25` 的子串）。这一条比"看着像"要紧得多 ——
// 它保证列表和消息头**永远说的是同一天**，不会出现列表说「昨天」而
// 消息头说「09-30」这种分家。TestTimeForms_ShrinkBySubtraction 守着它。
//
// 时间点从参数进来（不是内部取 time.Now）—— 判据才能一次造出「昨天」
// 和「前天」两个时间点来对表。
func timeForms(t time.Time, now time.Time) []string {
	l := t.Local()
	switch {
	case sameDay(l, now):
		return []string{l.Format("15:04")}
	case sameDay(l, now.AddDate(0, 0, -1)):
		return []string{"昨天 " + l.Format("15:04"), "昨天"}
	case sameDay(l, now.AddDate(0, 0, -2)):
		return []string{"前天 " + l.Format("15:04"), "前天"}
	default:
		// `2006-1-2` 而不是 `2006-01-02`：用户给的样例是 `2026-4-1 15:23`。
		// 月日不带前导零还有一个技术好处：`9-25` 也是它的子串，
		// 于是最窄的那一档可以就是「把年份去掉」。
		return []string{
			l.Format("2006-1-2 15:04"),
			l.Format("2006-1-2"),
			l.Format("1-2"),
		}
	}
}

// shortTime 把一个时间压成一个很短的标签。
//
// 三个地方共用它：会话列表右侧那一列，以及 Normal / Zen 两种会话里每条
// 消息的头部。共用的理由和别处一样 —— 「什么时候」这件事只该有一条规则，
// 各写一套必然漂成「列表说昨天、消息头说 09-28」。
//
// 由近及远地降精度，但**每一档都带时分**。用户的原话：
//
//	「不是显示"昨天"，而是还要显示时间。一般是"前天 15:30""15:55""2026-4-1 15:23"」
//
// 上一版是今天给时分、昨天只给「昨天」、更早只给月日 —— 一封信显示成
// 「昨天」，读的人知道是哪天，却不知道是昨天上午还是深夜；显示成 `06-01`
// 更是连年份都得回翻日历。时分是**最便宜的那条信息**，不该在第一档之后
// 就被丢掉。
//
// 梯子：
//
//	今天   → `15:55`
//	昨天   → `昨天 15:30`
//	前天   → `前天 15:30`
//	更早   → `2026-4-1 15:23`（带年份、不带前导零）
//
// ⚠️ 消息头那边**不能只写时分**：一条三天前的消息显示 `02:25`，读的人
// 会以为它是今天凌晨发的。列表里那个位置本来就窄，看不出问题；消息头
// 独立成行，这个谎就露出来了。所以前三档各自都带一个**日子锚点**
// （时分本身 / 「昨天」/「前天」/ 年月日），裸的时分只在今天出现。
//
// ⚠️ 长度**不固定**：`15:04` 5 列、`昨天 15:04` 10 列、`2026-4-1 15:23`
// 15 列。所以**列表那边不能直接拿这个串去排版** —— 一列宽 15 的时间会
// 把名字挤成 4 列。列表走 listTime（同一个梯子，按预算取一档）。
func shortTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return timeForms(t, time.Now())[0]
}

// listTime 是会话列表右侧那一列要写的时间：同一个梯子上**放得进 budget
// 的那一档**。
//
// 为什么列表需要单独一档：那一列只有 25–30 列，标记占 2 格、名字最少要
// 12 格，留给时间的只有十来列。`2026-4-1 15:23` 是 15 列 —— 直接摆进去
// 名字就只剩 4 格（`HTM…`），这恰恰是用户说的「元素堆砌挤压」。
//
// 退到哪一档是有讲究的：**从右往左砍，先砍时分、再砍年份**。时分是这一
// 档里最便宜的信息（同一天的邮件差几个小时没那么要紧），年份恰恰相反 ——
// 少了它日期就成了一个悬在半空的 `9-25`。
//
// 最后一个兜底是 `forms` 的最后一档，**即使它仍然超预算也返回它**：
// 返回值不承诺一定窄于 budget，宽度的事由调用方按 listTimeField 保证
// （nameW 和 timeW 是一起算出来的，不是各算各的）。
func listTime(t time.Time, budget int) string {
	if t.IsZero() {
		return ""
	}
	forms := timeForms(t, time.Now())
	for _, f := range forms {
		if textWidth(f) <= budget {
			return f
		}
	}
	return forms[len(forms)-1]
}

// sameDay 说两个时间是不是同一天（按本地时区）。
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// listRowAt 把列表栏里的一行（相对主体区顶部的偏移）映射回一条会话。
//
// 和 renderThreadList 共用 listRows + listWindow，所以点到的行就是
// 看到的那一行。
func (m Model) listRowAt(width, height, off int) (int, bool) {
	rows := m.listRows()
	start, end := m.listWindow(rows, height)
	i := start + off - 1 // 减 1 是那行标题
	if i < 0 || i >= end {
		return 0, false
	}
	if rows[i].idx < 0 {
		return 0, false
	}
	return rows[i].idx, true
}

// listHeader 是会话列表的标题行。
//
// ⚠️ 内容是**当前文件夹**，不是"会话"。
//
// 上一版它写「会话 · 收件箱（3 个未读）」—— 前半截是从"有个标题总比没有好"
// 里来的，而"这一栏是会话列表"这件事整个界面都在说。文件夹那一列被删掉
// 之后（见 styles.go），这个位置是唯一能回答「我现在在哪个文件夹」的
// 地方，那 3 个字的预算该给它。而且它现在**可以点**（弹出文件夹选择器），
// 所以标题本身就是那个入口的名字。
func (m Model) listHeader() string {
	title := m.folderLabel()
	if m.query != "" {
		title += "「" + m.query + "」"
	}

	unread := 0
	for _, th := range m.visible {
		if th.Unread > 0 {
			unread++
		}
	}
	if unread > 0 {
		title += fmt.Sprintf("（%d 个未读）", unread)
	}
	return title
}

// emptyListHint 说明列表为什么是空的 —— 是真空，还是被过滤掉了。
func (m Model) emptyListHint() string {
	switch {
	case m.query != "":
		return "没有匹配「" + m.query + "」的会话"
	case m.activeFolder != "":
		return m.folderLabel() + " 里还没有会话"
	default:
		return "还没有会话，按 n 新建一个"
	}
}

// ---- 聊天流 ----

// renderChat 画会话流那一栏。
//
// width 的契约是「这一栏的总宽度」，也就是 chatPaneWidth()（双栏时右栏、
// 单栏时整幅）；正文文字实际用 width-1 列，最右一列留给滚动条。
// TestChat_BodyWidthMatchesLayout 守着这条契约 —— 算行数的地方
// （Model.chatBodyLines）走的是 chatBodyWidth()，两者差一格就会出现
// 「能滚，但滚到底还差半行」这种对不上的怪现象。
func (m Model) renderChat(width, height int) string {
	contentW := width - 1
	if contentW < 1 {
		contentW = 1
	}

	title := m.chatTitle()
	lines := make([]string, 0, height)
	// 这一行是**信息行**，不是标题：加粗的大字会和顶部标签页、列表栏的
	// 名字抢注意力，而"我在跟谁说话"这件事已经由标签页（有底色块）说了。
	// 灰一点、补上封数，它就和标签页分工了 —— 标签页说"是谁"，这里说
	// "有多少"。
	//
	// 前面那 1 格内缩（paneInset）是必须的：它让标题、引用块、输入卡共享
	// 同一条左边缘。标题不缩的话它比下面的正文靠左一格，而这一格的参差
	// 正是那种"说不上哪里别扭、但就是不像设计过"的来源。
	titleW := contentW - paneInset
	if titleW < 1 {
		titleW = 1
	}
	lines = append(lines, strings.Repeat(" ", paneInset)+styleMuted.Render(truncate(title, titleW)))

	th, ok := m.activeThread()
	if !ok {
		lines = append(lines, "")
		// 右边这片空白在列表模式下是常态，什么都不说会让人以为界面坏了。
		// 三种情况分开提示，别给一句放之四海而皆准的废话。
		var hint string
		switch {
		case len(m.newChatTo) > 0:
			hint = "  输入内容后回车，就会给 " + strings.Join(m.newChatTo, ", ") + " 发出第一封邮件"
		case len(m.visible) == 0:
			hint = "  左边还没有会话。按 n 新建一个。"
		default:
			hint = "  按回车打开左边选中的会话。"
		}
		lines = append(lines, styleMuted.Render(truncate(hint, contentW)))
		return fillPane(lines, width, height)
	}

	all := m.renderChatBody(th, contentW)
	// 只显示底部放得下的部分；scroll 是从底部往上卷的行数。
	body := tailWindow(all, chatViewHeight(height), m.scroll)
	body = addScrollBar(body, len(all), m.scroll, contentW)

	lines = append(lines, body...)
	return fillPane(lines, width, height)
}

// renderChatBody 把一个会话里的全部消息渲染成行（不裁剪）。
//
// 渲染和裁剪分开是因为「最多能卷多少行」（Model.maxChatScroll）要用到
// 未裁剪的总行数，而裁剪要用到同一个 scroll 口径。两件事共用这一份行。
func (m Model) renderChatBody(th thread.Thread, width int) []string {
	var body []string
	for _, msg := range th.Messages {
		body = append(body, m.renderMessage(msg, width)...)
	}
	return body
}

// addScrollBar 给正文的每一行右侧接上一列滚动条。
//
// width 是正文文字宽度；返回的每行宽 width+1。
//
// 正文一屏放得下（total <= len(lines)）时什么也不画，只接一个空格 ——
// 用不上的滚动条不该在版面边上白竖一根线。反过来说，它的存在本身就是
// 提示：用户按 ↑/↓ 没反应时，先看一眼右边有没有这条线，就知道「是已经
// 到底了」还是「这个界面根本不能滚」。
//
// 位置换算：scroll 是**从底部往上**卷的行数，滚动条要的是**从顶部往下**
// 的偏移，所以 off = total - viewH - scroll。scroll 到顶（= max）时
// off == 0，滑块停在最上面 —— 和「往上卷到尽头就是最早的内容」一致。
func addScrollBar(lines []string, total, scroll, width int) []string {
	viewH := len(lines)
	if viewH == 0 {
		return lines
	}

	var track, thumb string
	thumbTop, thumbH := 0, 0
	if total > viewH {
		track = styleScrollBar.Render("│")
		thumb = styleScrollThumb.Render("┃")

		// 滑块高度按「可见部分占全文的比例」给，至少留一格 —— 否则
		// 长会话里的滑块会被整除抹成 0，滚动条就只剩轨道，等于没画。
		thumbH = viewH * viewH / total
		if thumbH < 1 {
			thumbH = 1
		}
		if thumbH > viewH {
			thumbH = viewH
		}

		off := total - viewH - scroll
		if off < 0 {
			off = 0
		}
		if maxOff := total - viewH; off > maxOff {
			off = maxOff
		}
		thumbTop = off * (viewH - thumbH) / (total - viewH)
	}

	out := make([]string, 0, viewH)
	for i, l := range lines {
		cell := " "
		switch {
		case track == "":
		case i >= thumbTop && i < thumbTop+thumbH:
			cell = thumb
		default:
			cell = track
		}
		out = append(out, padRight(l, width)+cell)
	}
	return out
}

func (m Model) chatTitle() string {
	if m.activeID == "" && len(m.newChatTo) > 0 {
		return "新会话 → " + strings.Join(m.newChatTo, ", ")
	}
	th, ok := m.activeThread()
	if !ok {
		return "会话"
	}
	// 群聊在会话页里把人全列出来：这一行比列表宽得多，而列表里那个
	// 「等 N 人」只是为了省地方。
	name := threadTitle(th)
	if th.IsGroup() {
		name = fmt.Sprintf("%s（%d 人）", strings.Join(peerNames(th), ", "), len(th.Peers))
	}
	// 封数让它和顶部标签页（同样写着这个名字）分开承担信息 —— 两边写
	// 一模一样的字，看着就是重复，而不是"同一个东西在两个地方"。
	if n := len(th.Messages); n > 0 {
		name += fmt.Sprintf(" · %d 封", n)
	}
	return name
}

// renderMessage 把一个消息渲染成若干行。
//
// 版面规则（width 是会话栏里文字可用的列数）：
//
//	[对方]  消息头左对齐、**不上底色**；正文左边跟着一条竖条
//	[自己]  消息头和正文都右对齐、全程无底色
//
// 三轮下来这块版面上只剩一条规矩：**对方的正文左边有竖条，自己的没有。**
// 前面两版守的是别的东西，都推翻了，值得记下来为什么：
//
//  1. 第一版头和正文同铺一条灰带 —— 「谁在说」也被圈进去了。头是**标注**，
//     不是内容，它该在外面。
//  2. 第二版把正文围进一个贴合内容的底色块（气泡）。宽度那套账很难算对
//     （四分之三上限、最小宽度、按显示宽度而不是字节量……），但真正的问题
//     不在实现：**一条消息一块底色，一屏里就有十几块亮度不同的矩形在互相
//     切割**，而长消息的块宽、短消息的块窄，右边缘参差得像一屏打乱的表格。
//     用户的原话是「割裂感太强」。
//
// 换成竖条之后：宽度不再需要"算" —— 引用块就是「一条竖条 + 它右边的字」，
// 字多长块就多长，右边自然参差反而是对的（那是一段文字，不是一个框）。
// 而"谁在说"由三件事同时回答：头里的名字、左/右对齐、有没有那条竖条。
func (m Model) renderMessage(msg thread.Header, width int) []string {
	mine := msg.From == m.cfg.Self()

	// 引用的几何（paneInset / quoteGap / minNameW）在 styles.go 里，
	// 和别的布局数字放在一起 —— 判据要绑那几个常量。

	// quoteIndent 是引用条那一行里、正文之前占掉的列数：
	// 栏左沿那一格留白 + 竖条 1 格 + 一格间隔。
	const quoteIndent = paneInset + 1 + quoteGap

	// 引用正文的折行宽度。
	//
	// 两条上限：**四分之三栏宽**（一整栏宽的正文每行七十多个字，眼睛回到
	// 下一行时容易串行），以及「栏宽 - quoteIndent」（撑破一行的代价见下面
	// 头部那段）。取更紧的那一条。
	//
	// ⚠️ 折行宽度必须**在这里定死**：四分之三那个上限是按**理想值**算的，
	// 而窄窗口下它比实际能给的列数还大 —— 先按它折行、再在外层压回来，
	// 正文不会跟着变，比引用条还宽的那几行照样撑破，而且只在特定宽度下
	// 才露出来（这一条踩过）。
	room := width - quoteIndent
	if room < 1 {
		room = 1
	}
	textW := width*3/4 - quoteIndent
	if textW > room {
		textW = room
	}
	if textW < 8 && room >= 8 {
		textW = 8
	}

	body := m.bodies[msg.MessageID]
	switch {
	case body.Text != "":
	case msg.UID == 0:
		body.Text = "（本地待同步）"
	default:
		body.Text = "…"
	}

	// fit 把一行收进给定的列数：先按显示宽度截断，再补空格。
	//
	// ⚠️ **别假设 renderMarkdown 出来的行一定不超过给定的宽度。**
	//
	// 折行是按显示宽度算的，但列表项的前缀（「1. 」占 3 列）和续行的悬挂
	// 缩进是**先占位再装内容**的 —— 栏宽给到 4 列时，「前缀 3 列 + 一个
	// 汉字 2 列」就是 5 列，比给定的 4 列还宽。极窄的宽度下这是必然会
	// 发生的，不是 renderMarkdown 的 bug。
	//
	// 用 ansi.Truncate 而不是纯文本的 truncate：**它认识 ANSI**，纯文本那
	// 把尺子会把转义序列的字节当成字符数，截出半个序列（比撑破更难查）。
	//
	// 这条是量出来的，不是想出来的：宽度 4 的那一栏里，列表项的行是 5 列。
	// 撑破一行的代价见下面头部那段 —— 整幅画面从此错行。
	fit := func(l string, w int) string {
		return padRight(ansi.Truncate(l, w, ""), w)
	}

	// 栏宽不足以画出一条引用条时，只输出按栏宽折过的正文。
	//
	// 下限是算出来的，不是拍的：引用条最窄的形态 = 左沿那一格留白 + 竖条
	// 1 格 + 间隔 1 格 + **一个最宽的字符**（见 styles.go 的 maxCellW）。
	// 最后那一项不能省，理由同上。
	//
	// 这一档里消息头也不画：4 列宽的名字只剩「Al…」，它带来的信息量还不
	// 如让给正文。
	if width < quoteIndent+maxCellW {
		var out []string
		for _, l := range renderMarkdown(body.Text, width) {
			out = append(out, fit(l, width))
		}
		return out
	}

	// 消息头 = 谁 + 什么时候 [+ HTML 标记]。
	//
	// 那个标记是「为什么这段排版和邮件原文不一样」的答案：HTML 邮件在
	// 入库前被转成了 Markdown（见 mail.HTMLToMarkdown），按钮、表格、
	// 标题都被重排过。没有它的话，用户看到排版差异只会以为是我们
	// 渲染坏了，然后去提一个查不出来源的 bug。
	tag := ""
	if body.HTML {
		tag = "HTML"
	}
	// 时间和列表右边那一列走的是同一条降精度梯子（见 shortTime）。
	//
	// 这里原先写死 `Format("15:04")`，和 zen 那边的头一样犯过同一个错：
	// 一条三天前的消息写 `02:25`，读的人会以为它是今天凌晨发的。列表里
	// 那个位置本来就窄，看不出问题；消息头独立成行，这个谎就露出来了。
	//
	// ⚠️ 别改回「只取时分」——上面 headAvail 那段宽度预算依赖的是
	// **时间这一段的实际宽度**（写的是 textWidth(timeTxt)），所以换了
	// 格式也不用动那些数字。
	timeTxt := shortTime(msg.Date)
	// 名字的样式**只区分"我"和对方**，不区分是哪一个对方。
	//
	// 「谁说的」靠读名字本身，「是不是我」靠对齐（自己发的靠右）—— 这两件
	// 事都已经有更结实的表达。剩下的这份颜色只做一件事：**把对方的名字
	// 和「我」拉开一档**。
	//
	// 为什么该拉开：「我」是这一屏里最不需要读的两个字 —— 每一屏都有，
	// 而且永远在右边。对方的名字才是信息。两个都占着最亮的那一档，等于
	// 把正文的注意力分给一个已知项；把「我」压到灰字之后，一屏里最亮的
	// 名字就都是**别人**的名字了。
	//
	// 这也和列表那边一个道理（见 styleRowNameRead）：界面上的层级是靠
	// 「谁比谁弱一档」划出来的，不是靠一个统一的最亮色。
	nameStyle := styleSender
	if mine {
		nameStyle = styleSenderSelf
	}
	// 先按可用宽度截断**再**上色 —— 反过来的话 lipgloss 会把转义序列
	// 算进宽度，右边的边界就歪了（styles.go 里那条硬约束）。显示名是
	// 对方自己写的，长度没有上限，所以这一步不是多余的。
	//
	// ⚠️ 窄栏下头部也会撑破一行（曾经就是）。所以预算要按「名字 + 两格 +
	// 时间 [+ 一格 + 标记]」的实际宽度算，放不下时**从右往左让**：
	// 先让标记（它只是在解释"排版为什么和邮件原文不一样"，最不急），
	// 再让时间，名字至少保住 minNameW 列。
	//
	// 为什么肯让到这一步：头只是这一行的**标注**，缺半截还读得懂；而
	// 一行撑破会让终端自己折行，下面每一行的行号都错开一格 —— 鼠标的
	// 命中测试会整体点错位置。两害相权，宁可少显示。
	//
	// 对方的头后面还跟着一格留白（见下面 padRight(" "+head, width)），
	// 所以它的预算比整栏少一格。
	headAvail := width
	if !mine {
		headAvail = width - 1
	}
	nameW := headAvail - 2 - textWidth(timeTxt)
	if tag != "" {
		nameW -= textWidth(tag) + 1
	}
	if nameW < minNameW && tag != "" {
		nameW += textWidth(tag) + 1
		tag = ""
	}
	if nameW < minNameW {
		nameW = headAvail
		timeTxt = ""
	}
	if nameW < 1 {
		nameW = 1
	}
	head := nameStyle.Render(truncate(displayName(msg, mine), nameW))
	if timeTxt != "" {
		head += "  " + styleTime.Render(timeTxt)
	}
	if tag != "" {
		head += " " + styleTag.Render(tag)
	}

	// 正文里存的是 Markdown（见 mail.HTMLToMarkdown），这里渲染成终端样式。
	//
	// 别改成「先上色再折行」—— 折行必须在 renderMarkdown 内部、在**上色之前**
	// 完成：按 rune 算宽度的折行会把转义序列的每个字符当成一列，折点落错位置，
	// 还会把序列拦腰切断。详见 markdown.go 里的说明。
	lines := renderMarkdown(body.Text, textW)

	out := make([]string, 0, len(lines)+2)
	if mine {
		out = append(out, padLeft(head, width))
		for _, l := range lines {
			out = append(out, padLeft(ansi.Truncate(l, width, ""), width))
		}
		return append(out, "")
	}

	out = append(out, padRight(" "+head, width))

	// ⚠️ 折行宽度要从**实际上限**反推，不能用上面那个理想值（这里再折
	// 一次，是因为头那一段算下来可能要更窄 —— 而 textW 已经按 room 钳过，
	// 所以这一步只是把同一件事写在最靠近用它的地方）。
	lines = renderMarkdown(body.Text, textW)

	// 引用块 = 那一格留白 + 竖条 + 一格间隔 + 正文。
	//
	// **不给右边补白。** 上一版必须补，是因为底色块要方；现在没有块了，
	// 补白只会让每一行都带上几十个看不见的尾随空格（复制、选中都会带上），
	// 而画面上没有任何东西因此变化。
	//
	// 空行（段落之间）也画竖条：那一段竖条断了的话，读者会把「两段话」
	// 读成「两段引用」—— 而竖条正是"这是同一个人说的"那条连线。
	//
	// 正文行只截断、不补齐（fit 那一步的补白只属于窄栏兜底：那里连
	// 竖条都没有，行宽得自己收住）。
	bar := styleSideline.Render(sidelineMark)
	inset := strings.Repeat(" ", paneInset)
	for _, l := range lines {
		out = append(out, inset+bar+strings.Repeat(" ", quoteGap)+ansi.Truncate(l, textW, ""))
	}
	return append(out, "")
}

// ---- 输入区与状态栏 ----

// renderInputBlock 画底部那一块浮起的输入区。
//
// 返回的行数**恒等于** l.inputRows：界面的行号是布局算出来的，画的时候
// 少一行多一行，下面的状态栏和鼠标的命中测试会整体错开一格 ——
// 那种错只在某些窗口尺寸下出现，最难查。
//
// 「浮起」在字符网格里只能这么表达：上下各留一整行空白，左右各留一列
// 边距，中间那一行铺一条比周围亮一档的底色。终端里没有圆角、没有阴影、
// 没有 z 轴，但「被空白包围的独立色块」已经足够让眼睛把它读成一层浮在
// 内容之上的东西 —— 而不是又一条贴底的横带。留白不是浪费，是这个方案
// 成立的前提。
//
// ⚠️ 它只占**正文栏**（l.paneW），不是从列表栏左沿横跨到屏幕右边。通栏
// 的横带既没有左右留白可依托（"浮起"无从谈起），又会把列表栏从中间截断
// —— 而那两块面板是要一路铺到底的（见 viewTwoPane）。
//
// 列表模式那一行**不适用上一条**：它是常驻快捷键提示，一次要报八个键，
// 正文栏那六十来列根本放不下 —— 收进去的话「q 退出」立刻被截掉，而那
// 正是用户最需要的一条（这条曾经真的发生过）。提示不是输入框，横跨整个
// 内容区；两者也不会同时出现，左边缘对不齐无从看起。
func (m Model) renderInputBlock(l layout) []string {
	// 列表模式那一行提示横跨整个内容区（含列表栏）：它的宽度是
	// 「屏幕 - 分隔竖线左边那一段」。**不减 navW** —— 那一列没有了，
	// 而少减一次会让提示的宽度和它实际能占的列数对不上，末尾那几个键
	// 被 padRight 静默吃掉（正是下面注释里说的「q 退出从来没显示出来」
	// 那个老毛病换了个成因）。
	full := m.width - l.listW - 1
	if l.ruleX < 0 {
		full = m.width
	}
	if full < 1 {
		full = 1
	}
	pad := strings.Repeat(" ", paneInset)

	if !m.inputFloats() {
		// 列表模式没有输入框，这一行留给常驻快捷键提示。**不套底色块**：
		// 给一行提示铺上输入框的底色，等于告诉用户那里能打字。
		if l.inputRows <= 1 {
			return []string{m.withRail(m.renderInputLine(full), l)}
		}
		blank := m.withRail(strings.Repeat(" ", full), l)
		return []string{blank, m.withRail(m.renderInputLine(full), l), blank}
	}

	plain := l.paneW
	if plain < 1 {
		plain = 1
	}

	if l.inputRows <= 1 {
		// 终端太矮，留白让给了正文。退回单行贴底 —— 和以前一样。
		return []string{m.withRail(pad+m.renderInputLine(plain), l)}
	}

	blank := m.withRail(pad+strings.Repeat(" ", plain), l)

	// 上下各一整行空白，中间才是输入行 —— 输入区靠这三行「被留白围住」
	// 和左边那一格内缩（paneInset）说明「这里可以打字」。
	//
	// ⚠️ 这一版**不再铺底色**。原先中间那一行压着一条抬升的色块
	// （paintRowBg + styleInputRow），用户的原话是「输入卡底色的重感」：
	// 一屏里唯一一块实心色把视线全吸到最底下去，而输入框恰恰是屏幕上
	// 最不需要被看见的东西。现在和 Zen 一样 —— 只有提示符 + 周边留白，
	// 一个色块都不铺，两边的分界交给留白。
	//
	// 那一行仍然要**补满**到 plain：右边缘是布局算出来的，少几列不影响
	// 看，但会让「输入行占满它的宽度」那条对齐判据失去依据（而且哪天有人
	// 给它加回底色，宽度就会短一截）。
	line := m.withRail(pad+padRight(m.renderInputLine(plain), plain), l)
	return []string{blank, line, blank}
}

// inputFloats 说当前这一行要不要画成浮起的输入框。
//
// 只有真的在等用户打字的那几个模式才画：确认框、文件夹选择这些
// 只是借这一行显示一句提示，套上输入框的底色会让人以为还能打字。
func (m Model) inputFloats() bool {
	switch m.mode {
	case modeChat, modeNewChat, modeSearch, modeForward:
		return true
	}
	return false
}

// renderInputLine 是输入区那一行的内容（不含底色）。
//
// width 是这一行可用的列数：单行贴底时是全宽，浮起时还要减掉块内两端
// 的空白。提示文字的截断必须按这个宽度来 —— 以前用 m.width，长输入会把
// 提示顶出屏幕，右边那栏的边框跟着歪。
//
// **返回的显示宽度不会超过 width。** 下面那块的宽度是布局算出来的，
// 这一行多出去几列，右边那条边距就没了，浮起的效果也跟着散掉。
func (m Model) renderInputLine(width int) string {
	if width < 1 {
		width = 1
	}

	switch m.mode {
	case modeConfirm:
		// 确认框占用这一行：不显示输入框，免得用户以为还能打字。
		return styleError.Render(truncate(m.confirmPrompt(), width))

	case modeSearch:
		return m.withNote(width, "   （回车保留过滤，Esc 清空）")

	case modeForward:
		return m.withNote(width, "   （转发最后一条，回车发送，Esc 取消）")

	case modeNewChat:
		return m.withNote(width, "   （回车确定，Esc 取消）")

	case modeChat:
		return m.withNote(width, m.chatHint())
	}

	// 列表模式没有输入框，这一行留给常驻快捷键提示。
	// 界面上的功能之所以长期"不存在"，就是因为它们只活在代码里。
	return styleMuted.Render(truncate(listHints(), width))
}

// minInputWidth 是浮起输入框最少要留多少列。
//
// 24 列 ≈ 12 个汉字，够写一句话。比这更窄的话，把提示全丢掉也不够用，
// 不如就让输入框占满 —— 一个只能看见四个字的输入框等于不能用。
const minInputWidth = 24

// withNote 拼「输入框 + 右边一句提示」，两者一起正好不超过 width 列。
//
// 提示放不下就截短，剩不下 8 列就不显示：残句（「   （Tab：发」）比
// 没有更碍眼，还会让人以为界面坏了。
func (m Model) withNote(width int, note string) string {
	inW := width - textWidth(note) - 1
	if inW < minInputWidth {
		inW = minInputWidth
	}
	if inW+textWidth(note) > width {
		note = truncate(note, width-inW-1)
		if textWidth(note) < 8 {
			return m.sizedInput(width)
		}
	}
	return m.sizedInput(inW) + styleMuted.Render(note)
}

// sizedInput 把输入框渲染成正好 width 列宽。
//
// 为什么要「量一遍再补差」：textinput 的 Width 口径和 lipgloss.Width 差着
// 三列 —— 它把提示符（2 列）和光标（1 列）算在 Width 之外（实测
// Width=40 → View 宽 43）。那 3 列是它的实现细节，不该抄到这里当成常数；
// 直接量一遍、把它多占的列数补回去，它哪天变了这里也不会错。
//
// 宽度必须准：这一行是拼在浮起的底色块里的，宽了几列就会把右边那列
// 边距顶掉，整块看着就「漏」了。
func (m Model) sizedInput(width int) string {
	if width < 1 {
		width = 1
	}
	in := m.input
	in.Width = width
	if got := lipgloss.Width(in.View()); got > width {
		in.Width -= got - width
		if in.Width < 1 {
			in.Width = 1
		}
	}
	// 包一层而不是改 textinput 自己的 Prompt：它的 Prompt 会参与宽度计算
	// 和光标定位，从外面套样式进去，它算的还是原样。
	return stylePrompt.Render(in.View())
}

// chatHint 是会话内输入行右侧那串提示。
//
// 它是会话里唯一能直接看见动作键的地方 —— 输入框有焦点，动作键全被
// 逼成了 Ctrl 组合（见 handleChatKey 顶部的说明），而帮助页在会话里
// 得按 F1 才打得开。所以「刷新」这种天天要用的键必须写在这一行上：
// 只写在帮助页里，等于它是一个只有按过 F1 的人才知道的功能。
func (m Model) chatHint() string {
	if m.activeID == "" {
		// 新会话，还没落地 —— Tab 在这里没有意义（没有会话可回）。
		return "   （回车发出第一封 · F1 帮助）"
	}
	scope := "只回发件人"
	if m.replyAll {
		scope = "发给所有人"
	}
	// 这里必须带上 Ctrl+↑/↓：它是「输入框有焦点时唯一能换会话的键」，
	// 而用户抱怨的正是找不到它。会话内的完整清单在帮助页（F1）。
	return "   （Tab：" + scope + " · Ctrl+↑/↓ 切会话 · Ctrl+R 刷新 · F1 帮助）"
}

// confirmPrompt 是确认框上的一句话。
func (m Model) confirmPrompt() string {
	switch m.confirmKind {
	case confirmDelete:
		if th, ok := m.pendingThread(); ok {
			return "删除「" + threadTitle(th) + "」？会移到「已删除」文件夹，可恢复。  y 确认 · n 取消"
		}
		return "删除这个会话？会移到「已删除」文件夹。  y 确认 · n 取消"

	case confirmAllMail:
		// 关掉时说的是实话：这个开关只管「以后还拉不拉历史」。
		// 已经同步下来的邮件不会因为关掉它就消失，那得走别的操作。
		if m.app != nil && m.app.AllMail() {
			return "关闭「接收全部邮件」？已经同步下来的邮件会留着，只是以后不再拉历史。  y 确认 · n 取消"
		}
		return "打开「接收全部邮件」？会把服务器上的历史邮件全部拉下来，可能要几分钟。  y 确认 · n 取消"
	}
	return "确认？  y 确认 · n 取消"
}

// viewHelp 渲染全屏帮助页。
func (m Model) viewHelp() string {
	lines := helpLines(m.width)

	bodyH := m.height - 1
	if bodyH < 1 {
		bodyH = 1
	}

	scroll := m.helpScroll
	max := len(lines) - bodyH
	if max < 0 {
		max = 0
	}
	if scroll > max {
		scroll = max
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + bodyH
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, bodyH+1)
	out = append(out, lines[scroll:end]...)
	for len(out) < bodyH {
		out = append(out, "")
	}
	out = append(out, m.footerLine(helpFooter, styleMuted, m.width))
	return strings.Join(out, "\n")
}

// helpFooter 是帮助页页脚左边那句。
//
// 说到「Esc 关闭」而不只是让右下角那格按钮去说：键盘用户也该在这一行里
// 找到答案，而按钮（`[ 关闭 ]`）是同一件事在鼠标那一侧的说法。
const helpFooter = "↑/↓ 或 PgUp/PgDn 滚动 · ? 或 Esc 关闭"

// viewFolderPicker 渲染文件夹选择器。
//
// 它取代了原来那条常驻的左侧导航列。内容基本一样（固定入口 + 服务端多出来
// 的文件夹 + 每项未读计数 + 两级分组），差别是**不再常驻** —— 那一列只服务
// 四五个入口，却每天都在占掉 12 列左右的列表宽度。
//
// 版式上它是一列**居中的窄栏**（和禅模式同一个思路，见 zen.go 里那段：
// 通栏的一行行看着像表格）。48 列刚好放下「垃圾邮件  12」这种最长组合。
//
// 鼠标：点某一项直接切过去，**点其它任何地方都取消**（组标题、空行、
// 上下留白、页脚）。这就是 Esc 在鼠标这一侧的等价物 —— 复选框式的
// "点确定按钮才能离开"在这个界面里没有位置。
func (m Model) viewFolderPicker() string {
	g := m.pickerGeom()
	rows := m.folderPickRows()

	indent := strings.Repeat(" ", g.left)
	lines := []string{
		indent + styleTitle.Render(truncate("换个文件夹", g.colW)),
		"",
	}
	for i := g.start; i < g.end; i++ {
		r := rows[i]
		lead := strings.Repeat(" ", folderItemIndent)
		switch {
		case g.itemOf[i] < 0 && r.group != "":
			lines = append(lines, indent+" "+
				styleGroupTitle.Render(padRight(truncate(r.group, g.inner), g.inner))+" ")
		case g.itemOf[i] < 0:
			lines = append(lines, "")
		case g.itemOf[i] == m.folderCursor:
			label, pad, count := folderPickSegs(*r.item, g.inner-folderItemIndent)
			lines = append(lines, indent+" "+
				styleRowSelected.Render(lead+label+pad+count)+" ")
		default:
			label, pad, count := folderPickSegs(*r.item, g.inner-folderItemIndent)
			lines = append(lines, indent+" "+
				padRight(lead+label+pad+styleTime.Render(count), g.inner)+" ")
		}
	}

	// 补白到页脚那行上面。这段空白**是取消区**（点别处取消），所以它是
	// 有功能的空白，不是凑数。
	//
	// 补到 g.bodyH 而不是 g.bodyH-1：页脚要落在**屏幕最后一行**（第
	// height-1 行），而正文区占的是它上面的 height-1 行。差一行的话页脚会
	// 停在第 height-2 行，最后那一行空着 —— 而 footerSlot 说的是 height-1，
	// 于是「看到的那一行」和「点得到的那一行」差一格，右边那格按钮点了没
	// 反应、底下那一格空行反而能取消。这种错只在选择器上出现，因为只有它
	// 是自成一屏、不走 layout 的。
	for len(lines) < g.bodyH {
		lines = append(lines, "")
	}
	if len(lines) > g.bodyH {
		lines = lines[:g.bodyH]
	}
	lines = append(lines, m.footerLine(pickerFooter, styleMuted, m.width))
	return strings.Join(lines, "\n")
}

// folderPickWidth 是选择器那一列的目标宽度。
//
// 48 是「垃圾邮件  12」这类最长组合加两侧内缩之后还留得下余量的宽度；
// 窄终端上会退到整幅（见 pickerGeom）。
const folderPickWidth = 48

// folderItemIndent 是组里的项比组标题多缩进的列数。
//
// **差 2 格是硬要求，不是审美偏好。** 差 1 格的时候，「邮箱」和它下面的
// 「全部」几乎压在同一条左边缘上，隔着一层文字根本读不出谁属谁 ——
// 侧栏时代这个差也是 2（navTitleIndent 1 对 navItemIndent 3）。把缩进
// 收成 1 格的对齐改动，在画面上等于把「两级」做没了，而画面上没有任何
// 东西会报错。判据 TestNav_HasTwoLevels 专门盯着这个差，且要求 ≥2。
//
// 缩进**做在底色块之内**（项那一整块仍然从栏边起，只是文字往里让两格）：
// 缩进做在块外面的话，选中项那块底色会比没选中的窄两格，选中的一瞬间
// 整行会左右跳一下。
const folderItemIndent = 2

// pickerFooter 是选择器末尾那行操作提示（左边那一半）。
//
// 「点别处取消」不再写在这里：右下角那格 `[ 取消 ]` 把同一件事说清楚了，
// 而且说得更硬 —— 一句提示只是"说了"，一格按钮是"能按"。原来的长句在窄
// 终端上还会先被截掉尾巴，恰好把"怎么出去"那半句丢掉。
const pickerFooter = "↑/↓ 选 · 回车确定 · 点一项直接换"

// pickerGeom 是选择器的几何。
//
// ⚠️ 渲染和鼠标命中测试**共用这一份**，理由和 layout 那套一样：两处各算
// 一遍，改了内缩或者行距就会漂，症状是「点第一项切到了第二项」，而且只在
// 组标题的个数变化时才错。
type pickerGeom struct {
	left, colW, inner int
	// itemOf[i] 是第 i 行对应的可选项下标；组标题和空行是 -1。
	// 光标只索引可选项，所以"第几行"和"第几项"必须分开。
	itemOf []int
	// start / end 是当前可见的行窗口（[start, end)）。窗口跟着光标走。
	start, end int
	bodyH      int
}

func (m Model) pickerGeom() pickerGeom {
	rows := m.folderPickRows()
	var g pickerGeom

	g.itemOf = make([]int, len(rows))
	cursorRow := 0
	next := 0
	for i, r := range rows {
		if r.item == nil {
			g.itemOf[i] = -1
			continue
		}
		g.itemOf[i] = next
		if next == m.folderCursor {
			cursorRow = i
		}
		next++
	}

	g.bodyH = m.height - 1
	if g.bodyH < 1 {
		g.bodyH = 1
	}
	// 正文区（标题 + 空行 + 条目 + 取消用的空白）要给页脚上面那 bodyH 行
	// 装下：标题、空行各占一行，余下的给条目和空白。
	avail := g.bodyH - 4
	if avail < 1 {
		avail = 1
	}

	// 窗口跟着光标走。有些服务商的文件夹多到一屏放不下。
	g.start = 0
	if cursorRow >= avail {
		g.start = cursorRow - avail + 1
	}
	if g.start+avail > len(rows) {
		g.start = len(rows) - avail
	}
	if g.start < 0 {
		g.start = 0
	}
	g.end = g.start + avail
	if g.end > len(rows) {
		g.end = len(rows)
	}

	g.colW = folderPickWidth
	if g.colW > m.width {
		g.colW = m.width
	}
	if g.colW < m.width {
		g.left = (m.width - g.colW) / 2
	}
	g.inner = g.colW - 2 // 左右各内缩一格，底色块不贴栏边
	if g.inner < 8 {
		g.inner = 8
	}
	return g
}

// pickerItemPos 是 pickerItemAt 的逆运算：给一个可选项下标，给出它的
// 屏幕坐标（取那一格的中间）。
//
// 放在生产代码里而不是测试辅助里，是因为它是**这份几何的第二个出口** ——
// 判据要能"点在自己声明的地方"，而声明必须和渲染共用一份（同 tabChip
// 那条注释里的道理：各算一遍就会漂，漂了还测不出来）。
//
// 返回 ok=false 表示那一项此刻不在可见窗口里。
func (m Model) pickerItemPos(idx int) (x, y int, ok bool) {
	g := m.pickerGeom()
	for i, v := range g.itemOf {
		if v != idx {
			continue
		}
		if i < g.start || i >= g.end {
			return 0, 0, false
		}
		return g.left + 1 + g.inner/2, i - g.start + 2, true
	}
	return 0, 0, false
}

// pickerItemAt 把选择器里的一个坐标映射到一个可选项下标。
//
// ok=false 表示**这一下点的是"别处"**：组标题、空行、留白、页脚、
// 或者那一列左右的空白。调用方据此取消选择器 —— 也就是 Esc 在鼠标
// 这一侧的等价物。
func (m Model) pickerItemAt(x, y int) (int, bool) {
	g := m.pickerGeom()

	// 行号：第 0 行是标题、第 1 行是空行，之后才开始数 rows。
	row := y - 2 + g.start
	if y < 2 || row < 0 || row >= g.end || row >= len(g.itemOf) {
		return 0, false
	}
	// 列：只有那一列的内缩区间算数，点左右的留白等于点"别处"。
	if x < g.left+1 || x >= g.left+g.colW-1 {
		return 0, false
	}
	if idx := g.itemOf[row]; idx >= 0 {
		return idx, true
	}
	return 0, false
}

// folderPickSegs 把一项拆成「标签 / 补白 / 计数」三段。
//
// 两种渲染（选中与未选中）共用它，于是去样式之后两者**逐字相同** ——
// 选中项铺底色、未选中项给计数上灰色，都只是在同一串文本上换样式，
// 高亮一移动文字不会左右跳。
func folderPickSegs(it navItem, inner int) (label, pad, count string) {
	if it.unread > 0 {
		count = fmt.Sprint(it.unread)
	}
	label = truncate(it.label, inner)
	if w := inner - textWidth(label) - textWidth(count); w >= 1 {
		return label, strings.Repeat(" ", w), count
	}
	// 太窄：退回连写，别把标签截成一个字。
	return truncate(it.label+" "+count, inner), "", ""
}

// renderStatus 画最底下那行状态栏。
//
// width 是这一行可用的列数 —— 双栏时它比终端窄「列表栏 + 那条竖线」，
// 因为竖线要贯到底，不能在这一行断掉（见 withRail）。
//
// **只有状态那个词是带色的，其余全是灰。** 上一版是整行一个颜色：连上
// 服务器的时候，账号、上次同步时间、"接收全部邮件"一起变成绿色，一条
// 底栏比正文还亮。状态栏是**背景信息**，它的职责是在你扫到它的时候
// 告诉你一切正常 —— 而"一切正常"不该是一句需要读的高亮。
//
// 这一行同时也是**动作栏**（见 action.go）的宿主：左边是状态，右边是
// 「怎么离开这一屏」。两样东西挤一行是有意为之 —— 状态是背景信息、
// 出路是操作，它们抢的不是同一个注意力。
func (m Model) renderStatus(width int) string {
	if width < 1 {
		width = 1
	}

	// 动作栏的位子先留出来，左边这些状态文字按剩下的列数排。反过来的话，
	// 一条长状态（「上次同步 15:04:05 · someone@very-long-domain.example」）
	// 就能把出路挤出屏幕，而屏幕上看不出少了什么。
	full := width
	width = m.actionRoom(width)

	// 状态词 + 它的颜色。
	//
	// ⚠️ 这里**不加圆点**（原来是 ● U+25CF）。它是 emoji 变体字符，
	// Windows 上等宽字体没有它的字形，终端会回退到 Segoe UI Emoji ——
	// 和列表里那个星标是同一个病。颜色本身已经把状态说清楚了。
	lead, leadStyle := "", styleMuted
	switch {
	case m.busy:
		lead, leadStyle = "同步中…", styleAccent
	case m.app == nil:
		// 还没连上，不显示连接状态。
	case m.connected:
		// 「在线」用灰色，不另开一支绿。正常状态应该**不说话** ——
		// 常驻的绿色「在线」是在为一个不需要知道的时刻准备颜色，
		// 而它的代价是整幅界面多一个色相。不正常的那一边（离线）
		// 才值得用颜色说出来。
		lead, leadStyle = "在线", styleMuted
	default:
		lead, leadStyle = "离线", styleError
	}

	var segs []statusSeg
	if lead != "" {
		segs = append(segs, statusSeg{lead, leadStyle})
	}
	// 「接收全部邮件」是个会改变同步代价的模式，得一直在状态栏上看得见。
	// 只在确认框里露一次的话，用户按完 y 就再也想不起来自己开过它了。
	if m.app != nil && m.app.AllMail() {
		segs = append(segs, statusSeg{"接收全部邮件", styleMuted})
	}
	if !m.lastSync.IsZero() {
		segs = append(segs, statusSeg{"上次同步 " + m.lastSync.Format("15:04:05"), styleMuted})
	}
	if m.cfg != nil && m.cfg.Account.Email != "" {
		segs = append(segs, statusSeg{m.cfg.Account.Email, styleMuted})
	}

	// 拼的时候**在段边界上截断**，不做整体截断。
	//
	// 整体截断（truncate(整行, width)）在字符串上下刀，一刀下去可能正好
	// 落在某段的转义序列中间，把 "\x1b[38;5;42m" 剪成半个 —— 终端会把它
	// 后面所有文字都当成那条序列的残余参数。段边界截断最坏情况是少显示
	// 最后一段，代价是"看不到邮箱地址"，可以接受。
	var b strings.Builder
	col := 0
	put := func(text string, sty lipgloss.Style) bool {
		sep := ""
		if col > 0 {
			sep = " · "
		}
		need := textWidth(sep) + textWidth(text)
		if col+need > width {
			return false
		}
		if sep != "" {
			b.WriteString(styleMuted.Render(sep))
			col += textWidth(sep)
		}
		b.WriteString(sty.Render(text))
		col += textWidth(text)
		return true
	}
	for _, s := range segs {
		if !put(s.text, s.style) {
			break
		}
	}

	// 上一条操作的反馈。它单独截断（内容本身就是纯文本，截断是安全的），
	// 放不下就整个不显示 —— 半句「已归档 3」比不显示更容易让人误解。
	if m.status != "" {
		room := width - col - 3
		if room > 0 {
			st := m.status
			if textWidth(st) > room {
				st = truncate(st, room)
			}
			b.WriteString(styleMuted.Render("   "))
			b.WriteString(styleError.Render(st))
		}
	}
	return m.withActions(b.String(), full)
}

// statusSeg 是状态栏里的一段：文字 + 它要用的样式。
type statusSeg struct {
	text  string
	style lipgloss.Style
}

// ---- 解锁与配置向导 ----
//
// 这两个页面是**一次性**的：开机回答问题，答完就再也不出现。所以它们不
// 参与主界面的分栏布局，而是独立成一屏，而且**摆在屏幕中间**。
//
// 为什么要居中：贴左上角的样子和主界面（左边列表、右边正文）一模一样，
// 用户会以为界面已经渲染完了、只是内容还没出来。居中的块在视觉上明确
// 表示"当前是一个需要你回答的问题"，答完才进主界面。
//
// 对齐只做**水平居中 + 偏上**（不是几何正中）：内容高度会随步骤变
// （选服务商那一屏很高，填邮箱那一屏只有几行），几何正中会让整个块
// 上上下下地跳；偏上一点，块的顶端移动得少，读起来稳。

// blockTopDivisor 决定内容块顶部留白的比例 —— 高度的 1/3 处是常见的
// "视觉中心"（几何正中的块看起来会偏低）。
const blockTopDivisor = 3

func (m Model) viewUnlock() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("clichat") + "\n\n")
	b.WriteString("账号：" + m.cfg.Account.Email + "\n\n")
	b.WriteString("输入主密码解锁本地凭据：\n\n")
	b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
	if hint := m.revealHint(); hint != "" {
		b.WriteString(styleMuted.Render(hint) + "\n")
	}
	b.WriteString(styleMuted.Render("Ctrl+R 重新配置账号 · Ctrl+C 退出"))
	if m.status != "" {
		b.WriteString("\n\n" + styleError.Render(m.status))
	}
	return centerScreen(b.String(), m.width, m.height)
}

func (m Model) viewSetup() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("clichat 首次配置") + "\n\n")

	switch m.setup.step {
	case stepProvider:
		b.WriteString("选择邮箱服务商：\n\n")
		for i, p := range config.Providers {
			label := p.Name
			// 在列表里就把「这个选项选了也走不通」标出来。等用户按回车
			// 被拒才说，等于让他白选一次。
			if p.Auth == config.AuthOAuth2 {
				label += "（需 OAuth2，本版本不支持）"
			}
			if i == m.setup.provider {
				b.WriteString("  " + styleSelected.Render("▸ "+label) + "\n")
			} else {
				b.WriteString("    " + label + "\n")
			}
		}
		if p := m.currentProvider(); p != nil {
			b.WriteString("\n" + renderProviderSettings(p))
			if p.Auth == config.AuthOAuth2 {
				b.WriteString("\n" + renderOAuthHelp(p))
			} else {
				b.WriteString("\n" + renderCredentialHelp(p))
			}
		}
		b.WriteString("\n" + styleMuted.Render("↑/↓ 选择 · 回车确定 · Ctrl+C 退出"))

	case stepEmail:
		b.WriteString(m.stepHeader("邮箱地址") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n")

	case stepCustomIMAP:
		b.WriteString(m.stepHeader("IMAP 服务器") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		b.WriteString(styleMuted.Render("  主机:端口，例如 imap.example.com:993") + "\n")
		b.WriteString(styleMuted.Render(fmt.Sprintf(
			"  已按你的邮箱域名预填，不对就直接改（省略端口时默认 %d）", config.DefaultIMAPPort)) + "\n")

	case stepCustomSMTP:
		b.WriteString(m.stepHeader("SMTP 服务器") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		b.WriteString(styleMuted.Render("  主机:端口，例如 smtp.example.com:465") + "\n")
		b.WriteString(styleMuted.Render(fmt.Sprintf(
			"  已按你的邮箱域名预填，不对就直接改（省略端口时默认 %d）", config.DefaultSMTPPort)) + "\n")

	case stepCustomTLS:
		b.WriteString(m.stepHeader("加密方式") + "\n\n")
		for i, opt := range tlsOptions {
			if i == m.setup.tls {
				b.WriteString("  " + styleSelected.Render("▸ "+padRight(opt.Label, 24)) + "\n")
			} else {
				b.WriteString("    " + padRight(opt.Label, 24) + "\n")
			}
		}
		b.WriteString("\n" + styleMuted.Render("↑/↓ 选择 · 回车确定"))

	case stepPassword:
		p := m.currentProvider()
		name := "该服务商"
		if p != nil {
			name = p.Name
		}
		b.WriteString(m.stepHeader(name+" 的凭据") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		if hint := m.revealHint(); hint != "" {
			b.WriteString(styleMuted.Render("  "+hint) + "\n\n")
		}
		if p != nil && len(p.Guide) > 0 {
			b.WriteString(renderCredentialHelp(p))
		}

	case stepMaster:
		b.WriteString(m.stepHeader("主密码") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		if hint := m.revealHint(); hint != "" {
			b.WriteString(styleMuted.Render("  "+hint) + "\n\n")
		}
		b.WriteString(styleMuted.Render("  凭据会用这个密码加密后存在本地。") + "\n")
		b.WriteString(styleMuted.Render("  它不会被发送到任何地方，忘了就只能重新配置。") + "\n")
	}

	if m.setup.err != "" {
		b.WriteString("\n" + styleError.Render(m.setup.err) + "\n")
	}
	return centerScreen(b.String(), m.width, m.height)
}

// centerScreen 把一个内容块摆到屏幕中间（水平居中、偏上三分之一）。
//
// 收的是**渲染好的字符串**（带 ANSI 也无妨），因为要居中就得按**显示
// 宽度**算，而不是字节长度 —— 用 textWidth 量，和别处同一把尺子。
//
// 每行右端补空格补到整宽：不补的话，上一屏留下的字符会露在这一屏的
// 行尾（新的一行更短，终端只覆盖写过的部分）。
//
// 尺寸还没拿到（width/height 是 0）时原样返回 —— 那种情况下"居中"
// 没有意义，硬算会把内容推到一个凭空的宽度里。
func centerScreen(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")

	padTop := (height - len(lines)) / blockTopDivisor
	if padTop < 0 {
		padTop = 0
	}
	out := make([]string, 0, len(lines)+padTop)
	for i := 0; i < padTop; i++ {
		out = append(out, strings.Repeat(" ", width))
	}
	for _, l := range lines {
		out = append(out, centerLine(l, width))
	}
	return strings.Join(out, "\n")
}

// centerLine 把一行水平摆到宽度 width 的中间，并补足到整宽。
func centerLine(line string, width int) string {
	w := textWidth(line)
	if w >= width {
		return line
	}
	left := (width - w) / 2
	return strings.Repeat(" ", left) + line + strings.Repeat(" ", width-w-left)
}

func (m Model) stepHeader(title string) string {
	for i, s := range m.setupFlow() {
		if s == m.setup.step {
			return fmt.Sprintf("第 %d 步 · %s", i+1, title)
		}
	}
	return title
}

// ---- 渲染辅助 ----

// renderProviderSettings 把当前预设**将要写进配置**的服务器地址摊开给用户看。
//
// 为什么要显示而不是藏起来：点一下回车之后，这些地址就固化进配置了，
// 之后所有"连不上"的排查都得从它们开始。摆在眼前有两个用处 ——
// 出了问题用户至少知道自己用的是哪台服务器；而且他能和服务商官方文档
// 对一眼，看出我们填的有没有错。
//
// 收发两行**分开**写，加密方式各写各的。这不是排版洁癖：iCloud 就是
// "收 993 隐式 / 发 587 STARTTLS" 的组合，挤成一行只写一个加密方式
// 会把这件事糊掉，而它恰恰是最容易配错的地方。
func renderProviderSettings(p *config.Provider) string {
	if p == nil || p.ID == config.CustomProviderID {
		return ""
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("将写入的服务器") + "\n")
	b.WriteString(styleMuted.Render("  收信  ") +
		padRight(p.IMAPHost+":"+strconv.Itoa(p.IMAPPort), 28) +
		styleMuted.Render(tlsShort(p.IMAPTLS)) + "\n")
	b.WriteString(styleMuted.Render("  发信  ") +
		padRight(p.SMTPHost+":"+strconv.Itoa(p.SMTPPort), 28) +
		styleMuted.Render(tlsShort(p.SMTPTLS)) + "\n")
	if p.SMTPNote != "" {
		b.WriteString(styleMuted.Render("        "+p.SMTPNote) + "\n")
	}
	return b.String()
}

// renderOAuthHelp 渲染「只能用 OAuth2」那一段。
//
// 它和 renderCredentialHelp 是**互斥**的两条路：后者教你"去哪拿一个能填
// 进密码栏的密钥"，前者告诉你"不存在这样的密钥"。区别写得这么显眼，是因为
// 用户对 Outlook 的直觉就是"去生成一个应用密码就行了" —— 而那条路微软
// 已经关掉了。不说清楚，他会一直找那个不存在的入口。
func renderOAuthHelp(p *config.Provider) string {
	if p == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(styleError.Render("这个服务商不能用密码登录") + "\n")
	if p.OAuthURL != "" {
		b.WriteString("  " + styleMuted.Render("技术说明：") +
			styleLink.Render(hyperlink(p.OAuthURL, p.OAuthURL)) + "\n")
	}
	for i, g := range p.Guide {
		b.WriteString(styleMuted.Render(fmt.Sprintf("  %d. %s", i+1, g)) + "\n")
	}
	return b.String()
}

// renderCredentialHelp 渲染「去哪拿凭据」这一段：直达链接 + 分步说明。
//
// 链接用 OSC 8 包成可点击的，在支持的终端里直接点开就能去拿授权码。
// 约束见 styles.go 里 hyperlink 的注释：样式不能带属性，否则序列会被切碎。
//
// 文字步骤同时保留作为退路：链接会失效（服务商改版），步骤不会。
// 这是配置向导里最容易卡住的一步，把直达链接摆出来能省掉用户自己找入口。
func renderCredentialHelp(p *config.Provider) string {
	if p == nil {
		return ""
	}

	var b strings.Builder

	if p.OpenURL != "" {
		b.WriteString(styleTitle.Render("直接去这里拿") + "\n")
		b.WriteString("  " + styleLink.Render(hyperlink(p.OpenURL, p.OpenURL)) + "\n")
		if p.HelpURL != "" {
			b.WriteString("  " + styleMuted.Render("官方说明：") +
				styleLink.Render(hyperlink(p.HelpURL, p.HelpURL)) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(styleTitle.Render("获取"+p.PasswordLabel) + "\n")
	for i, g := range p.Guide {
		b.WriteString(styleMuted.Render(fmt.Sprintf("  %d. %s", i+1, g)) + "\n")
	}
	return b.String()
}

// fillPane 把行数补齐到 height，并给每行补足宽度，避免拼栏时错位。
func fillPane(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = padRight(l, width)
	}
	return strings.Join(lines, "\n")
}

// tailWindow 取出列表末尾的一段。scroll 是从底部往上卷的行数。
//
// 越界的 scroll 在这里被收进来：负数按 0，超过总行数按「卷到顶」。
// 这是最后一道保险 —— 负责收敛的是 Model.clampChatScroll，但只要有
// 一条路径漏了收敛（比如窗口刚变小、正文刚被替换），这里也能保证画面
// 仍然是**正文**，而不是一片空白。空白会让人以为邮件没了。
//
// 以前这里没有收敛，`end < height` 时的兜底是 `end = height`，等价于
// 「scroll 悄悄降到 len-height」；现在把这件事写明，并且两个方向都收。
func tailWindow(lines []string, height, scroll int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) <= height {
		return lines
	}
	if maxScroll := len(lines) - height; scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	return lines[len(lines)-height-scroll : len(lines)-scroll]
}

// threadTitle 给会话起一个短标题。
//
// 会话的名字是「和谁」，不是「聊什么」—— 这是 IM 的语义，也是把聚合键
// 换成参与人集合之后自然的结果。主题降级成列表里的副行。
//
// 1:1 显示对方的名字（显示名优先，没有就用地址 @ 前那一段）；
// 群聊显示「Alice 等 3 人」—— 没人给群起名字时 IM 就是这么写的。
func threadTitle(th thread.Thread) string {
	if len(th.Peers) == 0 {
		return "(只有你)"
	}
	name := peerLabel(th, th.Peers[0])
	if len(th.Peers) == 1 {
		return name
	}
	return fmt.Sprintf("%s 等 %d 人", name, len(th.Peers))
}

// peerNames 是会话里全部对方的名字，顺序与 Peers 一致（按地址排序）。
func peerNames(th thread.Thread) []string {
	names := make([]string, 0, len(th.Peers))
	for _, p := range th.Peers {
		names = append(names, peerLabel(th, p))
	}
	return names
}

// peerLabel 是单个对方在界面上显示的名字。
//
// 地址本身是兜底：显示名只有在对方作为发件人露过面时才有（Header 里
// 只有 FromName），所以群聊成员里有人只有地址是正常的。
func peerLabel(th thread.Thread, addr string) string {
	if n := th.NameOf(addr); n != "" {
		return n
	}
	return shortAddr(addr)
}

// displayName 决定消息上显示谁的名字。
func displayName(msg thread.Header, mine bool) string {
	if mine {
		return "我"
	}
	if msg.FromName != "" {
		return msg.FromName
	}
	return shortAddr(msg.From)
}

// shortAddr 取地址 @ 前面的部分，省地方。
func shortAddr(addr string) string {
	if i := strings.IndexByte(addr, '@'); i > 0 {
		return addr[:i]
	}
	return addr
}
