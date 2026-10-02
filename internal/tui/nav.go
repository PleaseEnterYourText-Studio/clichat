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

// ---- 顶部标签页 ----

// recentLimit 是 recent 保留的上限。
//
// 只留最近这几个：这个切片的唯一用途是顶部那排标签页（最多 maxTabs 个），
// 多留只是让「打开过的会话」永远不释放。留一点余量是为了某个会话被删掉
// 之后还能补上下一个。
const recentLimit = 8

// noteRecent 把 id 挪到「最近打开」的最前面，去掉重复。
func (m *Model) noteRecent(id string) {
	if id == "" {
		return
	}
	out := make([]string, 0, len(m.recent)+1)
	out = append(out, id)
	for _, x := range m.recent {
		if x != id {
			out = append(out, x)
		}
	}
	if len(out) > recentLimit {
		out = out[:recentLimit]
	}
	m.recent = out
}

// tabThreads 返回顶部标签页要显示的会话，最近打开的排在最前。
//
// 顺序取「打开过的先后」而不是「列表里的时间序」：标签页回答的是
// 「我刚才在跟谁说话」，那是操作历史；按时间排是左边列表已经在做的事，
// 在上面再抄一遍等于没有信息量。
func (m Model) tabThreads() []thread.Thread {
	out := make([]thread.Thread, 0, maxTabs)
	for _, id := range m.recent {
		th, ok := m.threadByID(id)
		if !ok {
			// 会话被删了，或者还没进列表。跳过而不是画一个空标签 ——
			// 点上去会打开一个不存在的东西。
			continue
		}
		out = append(out, th)
		if len(out) >= maxTabs {
			break
		}
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
}

// tabLabelMax 是单个标签最多显示多少列文字。
//
// 14 列 ≈ 7 个汉字，够看出「张三」「王工 等 3 人」的区别。标签页是
// 一眼扫的东西，名字长了该去左边列表看全名。
const tabLabelMax = 14

// tabChips 排一排标签页，从内容区最左边开始。
//
// 放不下的标签**从末尾丢**：顺序是 MRU，最后面那些本来就是最少用的。
// 丢下去比把每个都截成「…」好 —— 后者会让所有标签都认不出来。
func (m Model) tabChips(l layout) []tabChip {
	ths := m.tabThreads()
	if len(ths) == 0 {
		return nil
	}
	// 坐标是**正文栏里的相对列**，不是屏幕列：那一行由 viewTwoPane 的
	// withPaneStrip 补上左边那两条面板带（侧栏 + 列表栏）再内缩一格，
	// 渲染时整体右移一次，命中测试再减回去一次。
	//
	// ⚠️ 这里不能直接写 l.paneX —— 那样左边的面板带会被算两遍（renderTabs
	// 自己不再补了，补的活归 viewTwoPane），标签实际画在声明位置右边
	// 37 列的地方。症状很隐蔽：标签画得好好的，就是点不动，而只比对
	// 内部字段的判据全绿。
	startX := 0
	// 每格之外还留 1 列间隙，末列不占。
	//
	// 从 2 收到 1：间隙宽到 2 列时，标签页看着像三个各自漂着的色块，
	// 而不是"一排并列的页签"。1 列刚好够把相邻两块底色分开（隔着终端
	// 背景），又不至于散架。
	const gap = 1
	// 标签能用的横向空间 = 正文栏宽（屏宽减去侧栏、列表栏、右边那一格）。
	avail := l.paneW
	if avail < tabLabelMax+4 {
		// 连一个完整标签都放不下。返回一个截短的，不要返回空 ——
		// 顶上没标签页是「没开过第二个会话」，和「窄得放不下」是两回事，
		// 后者不该让那一行凭空消失（行号一变，鼠标全点错）。
		one := truncate(threadTitle(ths[0]), avail-2)
		if textWidth(one)+2 > avail {
			return nil
		}
		return []tabChip{{id: ths[0].ID, label: one, x0: startX, x1: startX + textWidth(one) + 2}}
	}

	out := make([]tabChip, 0, len(ths))
	x := startX
	for _, th := range ths {
		label := truncate(threadTitle(th), tabLabelMax)
		w := textWidth(label) + 2 // 两侧各一格内边距
		if x+w > avail {
			break
		}
		out = append(out, tabChip{id: th.ID, label: label, x0: x, x1: x + w})
		x += w + gap
	}
	return out
}
