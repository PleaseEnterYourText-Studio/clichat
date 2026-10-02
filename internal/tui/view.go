package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// View 渲染整个界面。
func (m Model) View() string {
	if !m.ready {
		return "正在启动 clichat…"
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

	l := m.layout()
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
	bodyTop int
	bodyH   int

	// inputTop / inputRows 是浮起输入区的行范围。
	inputTop  int
	inputRows int

	statusRow int

	// 横向：导航列 / 会话列表列 / 会话流。
	// 单栏模式下只有 list 有效（list 和 chat 都是整幅宽，互斥显示）。
	navX, navW   int
	listX, listW int
	chatX, chatW int

	// contentX 是内容区左边缘，contentW 是它到最右一列之间的列数（末列
	// 留给一格右边距）。
	//
	// 内容区里的四样东西 —— 顶部标签页、会话列表、浮起的输入框、状态栏
	// —— 都从这里起步。各自少缩进一格的话，一眼就能看出左边缘参差不齐，
	// 而「对齐」这种事没人会专门去提 bug，只会觉得「说不上哪里别扭」。
	//
	// 内容区从侧栏**之后再空一格**开始（navWidth + navGutter），所以
	// 单栏模式下它是 0，和以前一样。
	contentX, contentW int
}

// layout 算一遍界面几何。
func (m Model) layout() layout {
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
		// 导航列自带右边那一格空隙，所以 navW 比侧栏本身宽一格。
		// 让空隙算在导航这一栏里，后面几栏的 x 坐标就不用各自 +1 ——
		// 「谁该缩进」这种账分摊到四处，迟早有一处漏掉。
		l.navX, l.navW = 0, navWidth+navGutter
		l.listX = l.navW
		l.listW = m.listWidth()
		l.chatX = l.listX + l.listW + 1 // +1 是那根竖线
		l.chatW = m.width - l.chatX
		if l.chatW < 1 {
			l.chatW = 1
		}
	} else {
		l.navX, l.navW = 0, 0
		l.listX, l.listW = 0, m.width
		l.chatX, l.chatW = 0, m.width
	}

	l.contentX = l.listX
	l.contentW = m.width - l.contentX - 1
	if l.contentW < 1 {
		l.contentW = 1
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
// 它和导航列是分开算的：导航列固定 navWidth，这里只算列表自己。
// 原来取 width/3（24..36），加上导航列之后会挤掉正文，所以收窄成
// width/4（22..30）—— 列表只要能放下「Alice, Bob」这样的名字就够，
// 主题本来就会被截断。
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
	return m.layout().bodyH
}

// chatPaneWidth 是会话流那一栏的总宽度（含最右那一列滚动条）。
//
// 双栏时它是右栏 —— 左边导航列、列表栏、中间那根竖线之外的全部；
// 单栏时就是整幅。抽出来是因为「正文折成几行」和「正文画多宽」必须是
// 同一个数，两边各算一遍的话，改了布局里的一处、另一处就开始骗人。
func (m Model) chatPaneWidth() int {
	return m.layout().chatW
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

// viewTwoPane 拼三栏：导航 / 会话列表 / 会话流。
//
// **中间不画横线** —— 各区域靠底色与留白分开。层次是：导航列有底色
// （最外一层，而且从上到下贯通）→ 列表栏无底色 → 一条竖线 → 会话流。
//
// 竖线留着，不像参照的设计那样全去掉：终端里纯靠底色分出「列表到哪儿
// 结束」不够可靠（底色深浅随用户的主题走，浅色主题上两档灰几乎一样），
// 而这条竖线是**功能性**的 —— 它同时是滚动条那一列的对齐基准。
func (m Model) viewTwoPane(l layout) string {
	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderNav(l),
		m.renderThreadList(l.listW, l.bodyH),
		"│",
		m.renderChat(l.chatW, l.bodyH),
	)

	out := make([]string, 0, m.height)
	if l.tabsRow >= 0 {
		out = append(out, m.renderTabs(l))
	}
	out = append(out, strings.Split(panes, "\n")...)
	out = append(out, m.renderInputBlock(l)...)
	out = append(out, m.withNavStrip(m.renderStatus(l.contentW), l))
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

// withNavStrip 给一行的左边补上导航列那条底色和它右边的空隙。
//
// 底下的输入区和状态栏不走 renderNav，不补这一下侧栏就在半空中断掉，
// 看着像「导航栏只有一半高」。补的是一段实底空格 + 一格空隙，不是给整行
// 上色 —— 整行上色会把右边的输入框也染成导航的底色。
func (m Model) withNavStrip(line string, l layout) string {
	if l.navW <= 0 {
		return line
	}
	return paintRowBg(strings.Repeat(" ", navWidth), navBgSeq()) +
		strings.Repeat(" ", navGutter) + line
}

// ---- 左侧导航 ----

// renderNav 画左侧导航列（两级：组标题 + 组里的项）。
//
// 底色的铺法：先按纯文本把每一行拼到 navWidth 宽，再**逐行**重压底色 ——
// 不能指望 styleNav.Render(整块)，那样只有第一行有底色（原因见
// paintRowBg 的注释）。空隙那一格**不铺**，见 navGutter。
func (m Model) renderNav(l layout) string {
	if l.navW <= 0 {
		return ""
	}
	rows := m.navRows()
	out := make([]string, 0, l.bodyH)
	for i := 0; i < l.bodyH; i++ {
		line := ""
		if i < len(rows) {
			line = rows[i]
		}
		out = append(out, paintRowBg(padRight(line, navWidth), navBgSeq())+
			strings.Repeat(" ", navGutter))
	}
	return strings.Join(out, "\n")
}

// navRows 返回导航列的每一行（纯渲染内容，不含底色）。
//
// 返回的是**画出来的行**，和点鼠标时的命中测试共用同一套换算 ——
// 见 navRowIndexAt。
func (m Model) navRows() []string {
	var out []string
	for gi, g := range m.navGroups() {
		if gi > 0 {
			// 组与组之间空一行。这个空行就是「两级」在视觉上的分界 ——
			// 没有它，两个组的标题会连成一片，看成六个平级的项。
			out = append(out, "")
		}
		out = append(out, styleGroupTitle.Render(" "+truncate(g.title, navWidth-2)))
		for _, it := range g.items {
			out = append(out, m.navItemRow(it))
		}
	}
	return out
}

// navItemRow 画导航里的一项。
//
// 选中项的那块底色要**铺满整列**，所以顺序是「先补齐宽度、再上色」：
// 反过来的话底色只盖住文字那几个字，看起来像被划了一道而不是一行选中。
func (m Model) navItemRow(it navItem) string {
	label := "  " + it.label
	if it.unread > 0 {
		label += " " + fmt.Sprint(it.unread)
	}
	label = truncate(label, navWidth)
	padded := padRight(label, navWidth)

	if it.folder == m.activeFolder {
		return styleNavActive.Render(padded)
	}
	return styleNavIdle.Render(padded)
}

// navRowIndexAt 把导航列里的一行（相对主体区顶部的偏移）映射回 navRows 的下标。
//
// 返回值 <0 表示这一行不是可点的项（组标题、组之间的空行、超出内容的补白）。
func (m Model) navRowIndexAt(off int) (navItem, bool) {
	if off < 0 {
		return navItem{}, false
	}
	// 与 navRows 的排布一一对应地重走一遍：组标题和空行占位、跳过。
	row := 0
	for gi, g := range m.navGroups() {
		if gi > 0 {
			if row == off {
				return navItem{}, false
			}
			row++
		}
		if row == off {
			return navItem{}, false
		}
		row++
		for _, it := range g.items {
			if row == off {
				return it, true
			}
			row++
		}
	}
	return navItem{}, false
}

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
		text := padRight(" "+c.label+" ", c.x1-c.x0)
		if c.id == m.activeID {
			b.WriteString(styleTabActive.Render(text))
		} else {
			b.WriteString(styleTabIdle.Render(text))
		}
		col = c.x1
	}
	// 后面还有没画出来的会话时留个省略号：一个也不说，用户只会以为
	// 标签页的数量就是全部。
	if remaining := len(m.tabThreads()) - len(chips); remaining > 0 && col+1 < m.width {
		b.WriteString(styleMuted.Render("…"))
	}
	return m.withNavStrip(b.String(), l)
}

// tabChipAt 找出落在列 x 上的那个标签页。
//
// x 是**屏幕列**（鼠标传来的是什么就是什么）。标签的坐标是内容区里的
// 相对列，所以这里减一次侧栏 —— 和 renderTabs 里 withNavStrip 加的那
// 一次正好抵消。少了这一步，标签画在哪儿和点得着哪儿就差一个侧栏宽。
func (m Model) tabChipAt(l layout, x int) (tabChip, bool) {
	x -= l.contentX
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
// 标题行带上当前文件夹和未读总数 —— 过滤生效时如果不显示，用户会
// 以为邮件丢了。搜索词也一样：搜索是可以回车保留的，不留痕迹的话，
// 用户会以为「列表里就剩这几封了」。
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

// renderListRow 画列表里的一行。
//
// 选中的那一条：两行**都**铺底色块，用 padRight 补到满宽才有「整行」
// 的效果。底色外不再叠前景样式 —— 块里字色深浅不一，整块底色的边界
// 就看不清了，反而比反白更乱。
func (m Model) renderListRow(r listRow, width int) string {
	if r.group != "" {
		return styleGroupTitle.Render(padRight(truncate("  "+r.group, width-2), width-2))
	}

	th := m.visible[r.idx]
	text := m.listRowText(th, r.first, width)

	if r.idx == m.cursor {
		return styleRowSelected.Render(padRight(text, width-2))
	}
	switch {
	case !r.first:
		return styleMuted.Render(text)
	case th.Unread > 0:
		return styleUnread.Render(text)
	default:
		return styleTitle.Render(text)
	}
}

// listRowText 是列表某一行的纯文本（未上色）。
//
// 先拼纯文本、再上色是硬约束：带上转义序列之后再截断，会把序列剪断
// （styles.go 里那条）。
func (m Model) listRowText(th thread.Thread, first bool, width int) string {
	if !first {
		// 副行是「现在在聊什么」—— 会话里最新一条的主题。一个会话可以
		// 横跨很多话题，所以它和标题（对方的名字）回答的是两个问题。
		subject := th.Subject
		if subject == "" {
			subject = "(无主题)"
		}
		return truncate("    "+subject, width-2)
	}

	marker := "  "
	if th.Unread > 0 {
		marker = "● "
	}
	// 星标紧跟在未读标记后面：两个标记的列宽都是两列，
	// 所以有没有星标都不会让后面的名字错位。
	star := "  "
	if th.IsStarred() {
		star = "★ "
	}
	return truncate(marker+star+threadTitle(th), width-2)
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
func (m Model) listHeader() string {
	title := "会话"
	if m.activeFolder != "" {
		title += " · " + m.activeFolder
	}
	if m.query != "" {
		title += " · 「" + m.query + "」"
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
		return m.activeFolder + " 里还没有会话"
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
	lines = append(lines, styleTitle.Render(truncate(title, contentW)))

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
	if th.IsGroup() {
		return fmt.Sprintf("%s（%d 人）", strings.Join(peerNames(th), ", "), len(th.Peers))
	}
	return threadTitle(th)
}

// renderMessage 把一个消息渲染成若干行。
func (m Model) renderMessage(msg thread.Header, width int) []string {
	mine := msg.From == m.cfg.Self()

	inner := width - 4
	if inner < 10 {
		inner = 10
	}

	body := m.bodies[msg.MessageID]
	switch {
	case body.Text != "":
	case msg.UID == 0:
		body.Text = "（本地待同步）"
	default:
		body.Text = "…"
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

	name := displayName(msg, mine) + "  " + msg.Date.Local().Format("15:04")
	// 先按可用宽度截断**再**上色 —— 反过来的话 lipgloss 会把转义序列
	// 算进宽度，右边的边框就歪了（styles.go 里那条硬约束）。显示名是
	// 对方自己写的，长度没有上限，所以这一步不是多余的。
	budget := width - 2
	if tag != "" {
		budget -= textWidth(tag) + 1 // +1 是标记前面那个空格
	}
	name = truncate(name, budget)

	if mine {
		name = styleMine.Render(name)
	} else {
		name = senderStyle(msg.From).Render(name)
	}

	head := name
	if tag != "" {
		head += " " + styleTag.Render(tag)
	}

	out := make([]string, 0, 8)
	// 布局：自己的靠右对齐、无底色；对方的左起一格缩进、铺满一条灰底，
	// 一直铺到版面右沿。两边在余光里就能分开（paintPeerRow 里说明了
	// 为什么不能直接把整行丢给 style.Render）。
	if mine {
		out = append(out, padLeft(head, width-2))
	} else {
		out = append(out, paintPeerRow(padRight(" "+head, width-2)))
	}
	// 正文里存的是 Markdown（见 mail.HTMLToMarkdown），这里渲染成终端样式。
	//
	// 别改成「先上色再折行」—— 折行必须在 renderMarkdown 内部、在**上色之前**
	// 完成：按 rune 算宽度的折行会把转义序列的每个字符当成一列，折点落错位置，
	// 还会把序列拦腰切断。详见 markdown.go 里的说明。
	for _, line := range renderMarkdown(body.Text, inner) {
		if mine {
			out = append(out, padLeft(line, width-2))
		} else {
			out = append(out, paintPeerRow(padRight(" "+line, width-2)))
		}
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
func (m Model) renderInputBlock(l layout) []string {
	plain := m.width - l.navW
	if plain < 1 {
		plain = 1
	}

	if l.inputRows <= 1 {
		// 终端太矮，留白让给了正文。退回单行贴底 —— 和以前一样。
		return []string{m.withNavStrip(m.renderInputLine(plain), l)}
	}

	blank := m.withNavStrip(strings.Repeat(" ", plain), l)

	if !m.inputFloats() {
		// 列表模式没有输入框，这一行留给常驻快捷键提示。**不套底色块**：
		// 给一行提示铺上输入框的底色，等于告诉用户那里能打字。
		return []string{blank, m.withNavStrip(m.renderInputLine(plain), l), blank}
	}

	// 浮起输入框的那一块：占满内容区（左边和标签页、列表对齐，右边留
	// 一格），块内两端再各留一列空白，所以文字可用的宽度是 blockW-2。
	blockW := l.contentW
	if blockW < 8 {
		// 窄到放不下边距，就贴满 —— 有边距却只剩三个字，不如不要边距。
		blockW = m.width - l.contentX
	}
	if blockW < 3 {
		blockW = 3
	}

	// 底色**必须**用 paintRowBg 重压，不能 styleInputRow.Render(整行)：
	// 行里有输入框的光标、提示文字这些自带 ^[[0m 的片段，重置会把底色
	// 一起清掉，于是底色只剩几段零零碎碎的（styles.go 的 paintRowBg
	// 里记着这条坑）。
	block := paintRowBg(
		" "+padRight(m.renderInputLine(blockW-2), blockW-2)+" ",
		bgSeqOf(styleInputRow))
	return []string{blank, m.withNavStrip(block, l), blank}
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
	out = append(out, styleMuted.Render(truncate(
		"↑/↓ 或 PgUp/PgDn 滚动 · ? 或 Esc 关闭", m.width)))
	return strings.Join(out, "\n")
}

// viewFolderPicker 渲染文件夹选择器。
func (m Model) viewFolderPicker() string {
	choices := m.folderChoices()

	bodyH := m.height - 1
	if bodyH < 1 {
		bodyH = 1
	}
	// 首行标题、末行操作提示，中间留给选项。
	avail := bodyH - 3
	if avail < 1 {
		avail = 1
	}

	// 有些服务商的文件夹多到一屏放不下，所以也要开窗口。
	start := 0
	if m.folderCursor >= avail {
		start = m.folderCursor - avail + 1
	}
	end := start + avail
	if end > len(choices) {
		end = len(choices)
	}

	lines := []string{
		styleTitle.Render(truncate("选择文件夹", m.width)),
		"",
	}
	for i := start; i < end; i++ {
		label := choices[i]
		if label == "" {
			label = "（全部文件夹）"
		}
		label = truncate("  "+label, m.width-2)
		if i == m.folderCursor {
			lines = append(lines, styleSelected.Render(padRight(label, m.width-2)))
		} else {
			lines = append(lines, label)
		}
	}

	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	lines = append(lines, styleMuted.Render(truncate(
		"↑/↓ 选择 · 回车确定 · Esc 取消", m.width)))
	return strings.Join(lines, "\n")
}

// renderStatus 画最底下那行状态栏。
//
// width 是这一行可用的列数 —— 双栏时它比终端窄 navW 列，因为左边那条
// 导航底色要一直贯到底。
func (m Model) renderStatus(width int) string {
	if width < 1 {
		width = 1
	}
	var parts []string
	switch {
	case m.busy:
		parts = append(parts, "同步中…")
	case m.app == nil:
		// 还没连上，不显示连接状态。
	case m.connected:
		parts = append(parts, "● 在线")
	default:
		parts = append(parts, "● 离线")
	}

	// 「接收全部邮件」是个会改变同步代价的模式，得一直在状态栏上看得见。
	// 只在确认框里露一次的话，用户按完 y 就再也想不起来自己开过它了。
	if m.app != nil && m.app.AllMail() {
		parts = append(parts, "接收全部邮件")
	}

	if !m.lastSync.IsZero() {
		parts = append(parts, "上次同步 "+m.lastSync.Format("15:04:05"))
	}
	if m.cfg != nil && m.cfg.Account.Email != "" {
		parts = append(parts, m.cfg.Account.Email)
	}

	line := strings.Join(parts, " · ")
	if m.status != "" {
		line += "   " + m.status
	}
	// 先截断再上色：截断会破坏 ANSI 转义序列。
	line = truncate(line, width)

	switch {
	case m.status != "":
		return styleError.Render(line)
	case m.app != nil && !m.connected:
		return styleError.Render(line)
	case m.connected:
		return styleOK.Render(line)
	default:
		return styleMuted.Render(line)
	}
}

// ---- 解锁与配置向导 ----

func (m Model) viewUnlock() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("clichat") + "\n\n")
	b.WriteString("账号：" + m.cfg.Account.Email + "\n\n")
	b.WriteString("输入主密码解锁本地凭据：\n\n")
	b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
	b.WriteString(styleMuted.Render("Ctrl+R 重新配置账号 · Ctrl+C 退出"))
	if m.status != "" {
		b.WriteString("\n\n" + styleError.Render(m.status))
	}
	return b.String()
}

func (m Model) viewSetup() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("clichat 首次配置") + "\n\n")

	switch m.setup.step {
	case stepProvider:
		b.WriteString("选择邮箱服务商：\n\n")
		for i, p := range config.Providers {
			if i == m.setup.provider {
				b.WriteString("  " + styleSelected.Render("▸ "+padRight(p.Name, 22)) + "\n")
			} else {
				b.WriteString("    " + padRight(p.Name, 22) + "\n")
			}
		}
		if p := m.currentProvider(); p != nil {
			b.WriteString("\n" + renderCredentialHelp(p))
		}
		b.WriteString("\n" + styleMuted.Render("↑/↓ 选择 · 回车确定 · Ctrl+C 退出"))

	case stepEmail:
		b.WriteString("第 1 步 · 邮箱地址\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n")

	case stepPassword:
		p := m.currentProvider()
		name := "该服务商"
		if p != nil {
			name = p.Name
		}
		b.WriteString("第 2 步 · " + name + " 的凭据\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		if p != nil && len(p.Guide) > 0 {
			b.WriteString(renderCredentialHelp(p))
		}

	case stepMaster:
		b.WriteString("第 3 步 · 主密码\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		b.WriteString(styleMuted.Render("  凭据会用这个密码加密后存在本地。") + "\n")
		b.WriteString(styleMuted.Render("  它不会被发送到任何地方，忘了就只能重新配置。") + "\n")
	}

	if m.setup.err != "" {
		b.WriteString("\n" + styleError.Render(m.setup.err) + "\n")
	}
	return b.String()
}

// ---- 渲染辅助 ----

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
