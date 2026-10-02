package tui

import (
	"strings"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// navItem 是左侧导航里的一项。
type navItem struct {
	// label 是显示名。它和 folder 是两件事：服务端上「已发送」可能叫
	// "Sent Messages" 或「已发送邮件」，但界面上永远写「已发送」——
	// 名字是给人看的，folder 是拿去比对的。
	label string
	// folder 是这一项对应的文件夹过滤值；空串表示「不按文件夹过滤」。
	//
	// 导航**不另设一个「当前选中项」的字段**，选中态直接就是 activeFolder：
	// 「哪一项亮着」和「列表在按哪个文件夹过滤」于是永远是同一件事，
	// 不可能出现「导航高亮着已发送、列表却在显示收件箱」这种错位。
	// 也顺带让原来那个文件夹选择器（Tab）自动和导航同步上了。
	folder string
	// unread 是这一项里未读**会话**的条数（0 不显示数字）。
	unread int
}

// navGroup 是导航里的一组。两级侧栏就是「组标题 + 组里的项」。
type navGroup struct {
	title string
	items []navItem
}

// navGroups 组装左侧导航的全部内容。
//
// 计数只按**搜索词**筛过的全集来算，不看当前文件夹 —— 把导航自己的
// 过滤也算进去的话，切到「已发送」之后「收件箱」的数字会变成 0，
// 看着就像邮件被弄丢了。
func (m Model) navGroups() []navGroup {
	base := filterThreads(m.threads, "", m.query)

	fixed := []navItem{
		{label: "全部", folder: ""},
		{label: "收件箱", folder: m.folderName("INBOX")},
		{label: "已发送", folder: m.folderName("SENT")},
		{label: "已删除", folder: m.folderName("TRASH")},
	}
	for i := range fixed {
		fixed[i].unread = countUnread(base, fixed[i].folder)
	}

	groups := []navGroup{{title: "邮箱", items: fixed}}

	// 第二级：服务端上真实存在、上面又没列过的文件夹。
	//
	// 这是「两级」的实处 —— 上面那组是固定的、谁都有的四个入口，
	// 这一组随账号变（126 就有「草稿箱 / 垃圾邮件 / 病毒文件夹 / 订阅邮件」），
	// 两者混在一列里会让人分不清哪些是系统入口、哪些是自己账号的。
	covered := map[string]bool{}
	for _, it := range fixed {
		if it.folder != "" {
			covered[normFolder(it.folder)] = true
		}
	}
	var extra []navItem
	for _, f := range m.folders {
		if f == "" || covered[normFolder(f)] {
			continue
		}
		extra = append(extra, navItem{
			label:  f,
			folder: f,
			unread: countUnread(base, f),
		})
	}
	if len(extra) > 0 {
		groups = append(groups, navGroup{title: "文件夹", items: extra})
	}
	return groups
}

// navItems 把导航摊平成一列（跳过组标题），供循环切换用。
func (m Model) navItems() []navItem {
	var out []navItem
	for _, g := range m.navGroups() {
		out = append(out, g.items...)
	}
	return out
}

// folderLabel 是当前文件夹**给人看的名字**。
//
// 直接显示 activeFolder 是不行的：它是服务端上的真名，收件箱会写成一串
// "INBOX"、已发送可能是 "Sent Messages"。标签（「收件箱」/「已发送」）
// 在 navGroups 里有唯一一份出处，标题、选择器、提示语都从它取 ——
// 各写一份的话，改一处另外几处就开始骗人。
//
// 认不出来时（服务端多出来的文件夹、或者配置里直接写了名字）原样返回：
// 那是它的真名，也是用户唯一能对上的东西。
func (m Model) folderLabel() string {
	if m.activeFolder == "" {
		return "全部"
	}
	for _, g := range m.navGroups() {
		for _, it := range g.items {
			if it.folder == m.activeFolder {
				return it.label
			}
		}
	}
	return m.activeFolder
}

// folderName 把配置里的规范名解析成服务端上真正在用的名字。
//
// 解析不出来时原样返回：列表会是空的，但那一项仍然留在导航里 ——
// 让导航的项数随服务器状态忽增忽减，比显示一个空文件夹更让人困惑
// （东西会自己跳位置）。何况拿不到文件夹列表（LIST 失败）时本来就
// 没法判断，那时不该把任何一项判死。
func (m Model) folderName(spec string) string {
	name, ok := mail.ResolveFolder(spec, m.folders)
	if !ok {
		return spec
	}
	return name
}

// countUnread 数一数 list 里落在 folder 中的未读会话条数。
//
// folder 为空串表示不过滤。数的是**会话**条数，不是邮件封数 ——
// 列表里一条就是一段对话，导航上的数字得和它数得上的行数一致。
func countUnread(list []thread.Thread, folder string) int {
	n := 0
	for _, th := range list {
		if folder != "" && !threadInFolder(th, folder) {
			continue
		}
		if th.Unread > 0 {
			n++
		}
	}
	return n
}

// normFolder 把文件夹名归一成可以互相比较的形式。
//
// 放在这里而不是直接用 EqualFold 逐个比，是因为要去重：服务端既给了
// "INBOX" 也可能给 "Inbox"，两种写法指同一个文件夹，得先把它们压成
// 同一个键才不会在导航里出现两遍。
func normFolder(name string) string {
	return strings.ToLower(name)
}

// cycleNav 把文件夹过滤切到导航里的下一项（末尾绕回开头）。
//
// 为什么是「循环」而不是给每一项配一个键：导航里有六七项，配字母会
// 撞上已有的按键（u 是标未读、* 是星标、a 是全部邮件），配数字又得
// 在界面上把数字画出来才不算隐藏功能。循环只需要知道一个键，方向明确，
// 而且项数变了也不用改绑定。
func (m *Model) cycleNav() {
	items := m.navItems()
	if len(items) == 0 {
		return
	}
	cur := -1
	for i, it := range items {
		if it.folder == m.activeFolder {
			cur = i
			break
		}
	}
	m.setFolder(items[(cur+1)%len(items)].folder)
}

// setFolder 切换当前文件夹过滤，并把光标收回列表顶部。
//
// 光标一定要重置：换文件夹之后 m.visible 是另一批会话，沿用原来的
// 下标会停在一个和刚才那封毫无关系的邮件上。
func (m *Model) setFolder(folder string) {
	m.activeFolder = folder
	m.refreshVisible()
	m.cursor = 0
	m.clampCursor()
}

// ---- 文件夹选择器 ----
//
// 这里原本是「左侧常驻导航列」的数据层。那一列被删掉之后，同一批数据
// （navGroups）改成由**按需唤出的选择器**呈现 —— 内容一样（含未读计数、
// 含两级分组），只是不再常驻占掉 14 列。

// folderPickRow 是选择器里的一行：要么是一个组标题，要么是一个能选中的项。
//
// 摊成"一行一行"而不是直接渲染 navGroups 的两层结构，是因为**鼠标要按行
// 命中**：一个扁平的、带序号的切片让「第几行」到「哪一项」只有一处换算，
// 渲染和命中测试共用它。分成两层各算一遍的话，点「已发送」会切到它下面
// 那一项去，而只在组标题数量变化时才错。
type folderPickRow struct {
	group string   // 非空 = 组标题（不可选）
	item  *navItem // 非空 = 可选项
}

// folderPickRows 组装选择器的全部行（组标题 + 项，含组之间的空行）。
func (m Model) folderPickRows() []folderPickRow {
	var out []folderPickRow
	for gi, g := range m.pickGroups() {
		if gi > 0 {
			out = append(out, folderPickRow{}) // 组之间的空行
		}
		out = append(out, folderPickRow{group: g.title})
		for i := range g.items {
			it := g.items[i]
			out = append(out, folderPickRow{item: &it})
		}
	}
	return out
}

// pickGroups 是选择器要显示的组。
//
// 复用 navGroups 的那套组装（四个固定入口 + 服务端多出来的文件夹 + 每项的
// 未读计数），于是这些信息只有一个出处。唯一的差别是**要把服务端上不存在
// 的固定项滤掉**：
//
// navGroups 里那四项永远在，那是刻意的 —— 常驻侧栏时，项数随服务器状态
// 忽增忽减比显示一个空文件夹更让人困惑。但那个理由不适用于选择器：它是
// 用户专门打开的一张"我要去哪儿"的列表，"列出一个点进去必然空空如也的
// 文件夹"就是纯误导。
//
// ⚠️ 拿不到文件夹列表时（LIST 失败，m.folders 为空）**不过滤**：那时候
// 本来就判断不了谁存在，不该把任何一项判死。
func (m Model) pickGroups() []navGroup {
	groups := m.navGroups()
	if len(m.folders) == 0 {
		return groups
	}

	real := make(map[string]bool, len(m.folders))
	for _, f := range m.folders {
		real[normFolder(f)] = true
	}

	out := make([]navGroup, 0, len(groups))
	for i, g := range groups {
		if i > 0 {
			// 服务端文件夹那一组本来就是从 m.folders 来的，不用筛。
			out = append(out, g)
			continue
		}
		kept := make([]navItem, 0, len(g.items))
		for _, it := range g.items {
			if it.folder == "" || real[normFolder(it.folder)] {
				kept = append(kept, it)
			}
		}
		if len(kept) > 0 {
			out = append(out, navGroup{title: g.title, items: kept})
		}
	}
	return out
}

// folderPickerItems 是选择器里**可选中**的项，按显示顺序（组标题不算）。
//
// folderCursor 索引的就是这个切片，所以光标上下移动不会停到组标题上。
func (m Model) folderPickerItems() []navItem {
	var out []navItem
	for _, r := range m.folderPickRows() {
		if r.item != nil {
			out = append(out, *r.item)
		}
	}
	return out
}

// folderPickIndexAt 把选择器里的一行（相对选择器顶部的偏移）映射回
// folderPickItems 的下标。
//
// 和 folderPickRows 共用同一份行结构 —— 这是「点到的 == 看到的」在这个
// 界面里的落点。自己再走一遍两层循环的话，组标题数一变两者就开始漂。
func (m Model) folderPickIndexAt(off int) (int, bool) {
	if off < 0 {
		return 0, false
	}
	idx := 0
	for i, r := range m.folderPickRows() {
		if i == off {
			if r.item == nil {
				return 0, false // 组标题 / 空行：不可点
			}
			return idx, true
		}
		if r.item != nil {
			idx++
		}
	}
	return 0, false
}

// ---- 顶部标签页 ----

// tabLimit 是 openTabs 保留的上限。
//
// 这个切片的用途就是顶部那排标签页，多留只是让「打开过的会话」永远不释放。
// 溢出时**从最前面丢**（丢最早打开的那个）：当前位置不会因此动 ——
// 丢掉最左边一格，右边那些的相对次序全都不变。
const tabLimit = 8

// openTab 把 id 记进「打开着的标签页」。
//
// ⚠️ 已经在了就**原地不动**，不往前提。这是这一版和上一版最大的区别：
// 上一版是 MRU（切到哪个哪个就挪到最前），于是点一下标签它自己就跑到
// 第一个位置 —— 一排会自己重排的标签页，每次点都得重新找一遍。
// 现在按「打开先后的顺序」排，位置只在你新开 / 关掉一个会话时才变，
// 和浏览器一致。
func (m *Model) openTab(id string) {
	if id == "" {
		return
	}
	for _, x := range m.openTabs {
		if x == id {
			return
		}
	}
	m.openTabs = append(m.openTabs, id)
	if len(m.openTabs) > tabLimit {
		m.openTabs = m.openTabs[len(m.openTabs)-tabLimit:]
	}
}

// tabThreads 返回顶部标签页要显示的会话，**按打开先后的顺序**。
//
// 顺序取「打开过的先后」而不是「列表里的时间序」：标签页回答的是
// 「我在跟谁说话」，那是操作历史；按时间排是左边列表已经在做的事，
// 在上面再抄一遍等于没有信息量。
//
// 返回**全部**打开着的（上限 tabLimit），不由这里做可见裁剪 ——
// 一排标签能不能放下取决于正文栏有多宽，那是 tabChips 的事。
func (m Model) tabThreads() []thread.Thread {
	out := make([]thread.Thread, 0, len(m.openTabs))
	for _, id := range m.openTabs {
		th, ok := m.threadByID(id)
		if !ok {
			// 会话被删了，或者还没进列表。跳过而不是画一个空标签 ——
			// 点上去会打开一个不存在的东西。
			continue
		}
		out = append(out, th)
	}
	return out
}

// threadByID 按 ID 找一个会话。
//
// 先在全量会话里找，找不到再退回「当前打开的那个」：新会话刚发出去时
// 可能还没进 m.threads（要等下一轮同步才会带回来），但它确实已经打开了，
// 标签页里该有它。
func (m Model) threadByID(id string) (thread.Thread, bool) {
	if id == "" {
		return thread.Thread{}, false
	}
	for _, th := range m.threads {
		if th.ID == id {
			return th, true
		}
	}
	if th, ok := m.activeThread(); ok && th.ID == id {
		return th, true
	}
	return thread.Thread{}, false
}

// tabChip 是顶部一个标签页：它显示的文字和它占的列区间。
//
// 位置在这里算**一次**，渲染和鼠标命中测试共用这一份。各算一遍的话，
// 改一个间距就会出现「点到的不是看到的」—— 而且只在标签名字长短不同的
// 情况下才错，极难复现。
type tabChip struct {
	id     string
	label  string
	x0, x1 int // [x0, x1) 是这一格占的列
	// marker 非空表示这一格是「还有标签没显示」的溢出指示（"‹" 或 "›"），
	// 不是真标签。
	//
	// 让它走同一个 tabChip 结构是为了命中测试：指示符也占一格、也在这一
	// 行上，它和标签共用一套坐标就不可能「点偏」。点它的行为是「把那一侧
	// 最近的标签切过去」（见 activateTabAt）—— 我们不存滚动偏移，
	// 因为窗口是跟着当前标签走的，存了反而会和它打架。
	marker string
}

// tabLabelMax 是单个标签最多显示多少列文字。
//
// 14 列 ≈ 7 个汉字，够看出「张三」「王工 等 3 人」的区别。标签页是
// 一眼扫的东西，名字长了该去左边列表看全名。
const tabLabelMax = 14

// tabStripGap 是相邻两个标签之间的空隙（列）。
//
// 1 列刚好够把相邻两块底色分开（隔着终端背景），又不会散架 ——
// 从 2 收到 1 就是因为间隙宽到 2 列时，标签看着像几个各自漂着的色块，
// 而不是"一排并列的页签"。
const tabStripGap = 1

// tabChips 排一排标签页，从内容区最左边开始。
//
// ⚠️ x0 / x1 是**正文栏里的相对列**，不是屏幕列。那一行由 viewTwoPane 的
// withPaneStrip 补上左边的面板带再内缩一格，渲染时整体右移一次，命中测试
// （tabChipAt）再减回去一次。这里不能直接加 l.paneX —— 那样左边的面板带
// 会被算两遍，标签实际画在声明位置右边十几列的地方。症状很隐蔽：标签画得
// 好好的，就是点不动，而只比对内部字段的判据全绿。
//
// 两件事在这里定下来：
//
//  1. **顺序就是打开先后的顺序**（见 openTab），不重排。放不下的从两端
//     裁掉，两侧各留一个 "‹" / "›" 指示。
//  2. **当前标签一定在可见范围内** —— 窗口跟着它走。这是浏览器的做法
//     （scroll into view）：否则开过 6 个会话之后，切到第 6 个的瞬间
//     那一排标签里根本没有它亮着的那一格，看着就像切换失败了。
//
// 窗口取「最小的 start 覆盖当前标签」：在保证当前标签可见的前提下，
// 尽量把左边的标签也带进来（多显示一个是一个）。
func (m Model) tabChips(l layout) []tabChip {
	ths := m.tabThreads()
	if len(ths) == 0 {
		return nil
	}

	// 每格的宽度：文字宽 + 两侧各一格内边距。
	type tabCell struct {
		id    string
		label string
		w     int
	}
	cells := make([]tabCell, 0, len(ths))
	for _, th := range ths {
		label := truncate(threadTitle(th), tabLabelMax)
		cells = append(cells, tabCell{id: th.ID, label: label, w: textWidth(label) + 2})
	}

	avail := l.paneW
	if avail < 1 {
		return nil
	}

	// 连一个完整标签都放不下。返回一个截短的，不要返回空 ——
	// 顶上没标签页是「没开过第二个会话」，和「窄得放不下」是两回事，
	// 后者不该让那一行凭空消失（行号一变，鼠标全点错）。
	if avail < tabLabelMax+4 {
		one := truncate(threadTitle(ths[0]), avail-2)
		if textWidth(one)+2 > avail {
			return nil
		}
		return []tabChip{{id: ths[0].ID, label: one, x0: 0, x1: textWidth(one) + 2}}
	}

	// fit 说「从 start 开始、在 width 列里能放下几个」，返回数量与总宽
	// （总宽含格与格之间的空隙，不含开头的空格）。
	fit := func(start, width int) (n, total int) {
		for i := start; i < len(cells); i++ {
			add := cells[i].w
			if n > 0 {
				add += tabStripGap
			}
			if total+add > width {
				break
			}
			total += add
			n++
		}
		return n, total
	}

	active := 0
	for i, c := range cells {
		if c.id == m.activeID {
			active = i
			break
		}
	}

	// 先按「两侧都不留指示符」试。放得下就到此为止 —— 常见情况是
	// 两三个标签，这里一步就结束。
	start, n := 0, 0
	if n, _ = fit(0, avail); n < len(cells) {
		// 放不下，两侧各留一格给 "‹" / "›"。
		width := avail - 2
		if width < 1 {
			return nil
		}
		// 最小的、能把当前标签覆盖进来的 start。
		for s := 0; s <= active; s++ {
			if sn, _ := fit(s, width); s+sn > active {
				start = s
				break
			}
		}
		n, _ = fit(start, width)
	}

	// 只在那一侧真的有没显示的标签时才画指示符。位置按「贴着相邻的标签」
	// 算，不额外占空隙 —— 留白多一格少一格都无所谓，但**不能让两边的
	// 保留量互相影响**（那样窗口算一遍就变一次，鼠标点到的和看到的会漂）。
	out := make([]tabChip, 0, n+2)
	x := 0
	if start > 0 {
		out = append(out, tabChip{marker: "‹", x0: x, x1: x + 1})
		x++
	}
	for i := start; i < start+n; i++ {
		c := cells[i]
		// 第一格紧贴前一个元素，其余各自隔一个空隙。
		if len(out) > 0 {
			x += tabStripGap
		}
		out = append(out, tabChip{id: c.id, label: c.label, x0: x, x1: x + c.w})
		x += c.w
	}
	if start+n < len(cells) {
		x += tabStripGap
		out = append(out, tabChip{marker: "›", x0: x, x1: x + 1})
	}
	return out
}
