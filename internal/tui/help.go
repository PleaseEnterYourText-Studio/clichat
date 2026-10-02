package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpItem 是帮助页里的一行。label 在「说明」那组里不是按键，
// 所以名字取中性的 label 而不是 key。
type helpItem struct {
	label string
	desc  string
}

// helpSection 是帮助页里的一组条目。
type helpSection struct {
	title string
	items []helpItem
}

// helpSections 是帮助页的全部内容。
//
// 这里是 **唯一** 一份按键清单 —— 底部的常驻提示也从它派生，
// 免得以后加了快捷键只改一处、另一处继续骗人。界面上的功能之所以
// 长期「不存在」，就是因为它们只活在代码里。
var helpSections = []helpSection{
	{
		title: "会话列表",
		items: []helpItem{
			{"↑ / k，↓ / j", "上下移动"},
			{"g / Home，G / End", "跳到首个 / 末个"},
			{"[ / ]", "上一个 / 下一个未读"},
			{"回车", "打开会话"},
			{"R", "打开，并且只回发件人"},
			{"n", "新建会话"},
			{"/", "搜索（匹配参与者、主题、显示名）"},
			{"Tab", "切换文件夹（弹出列表）"},
			{"Ctrl+G", "在所有文件夹之间循环"},
			{"Esc", "退掉一层筛选：先退文件夹，再退搜索词"},
			{"r", "立即同步一次"},
			{"u", "标记为未读"},
			{"*", "星标 / 取消星标"},
			{"d", "删除（移到「已删除」，可恢复）"},
			{"a", "接收全部邮件：连历史一起拉，再按一次关掉"},
			{"f", "转发最后一条消息"},
			{"y", "复制最后一条消息的正文"},
			{"q", "退出"},
		},
	},
	{
		title: "会话内",
		items: []helpItem{
			{"回车", "发送"},
			{"Tab", "发给所有人 / 只回发件人"},
			{"F2", "进禅模式"},
			{"Ctrl+R", "刷新：立即同步一次，并重载这个会话的正文"},
			{"↑ / ↓", "上下滚动正文（输入框是空的时候）"},
			{"PgUp / PgDn", "整屏翻页"},
			{"Ctrl+↑ / Ctrl+↓", "上一个 / 下一个会话"},
			{"Esc", "返回列表。会话不关 —— 再按回车就回来"},
			{"Ctrl+Y", "复制最后一条消息的正文"},
			{"Ctrl+U", "标记未读并返回列表"},
			{"Ctrl+T", "星标 / 取消星标"},
			{"Ctrl+D", "删除（移到「已删除」）"},
		},
	},
	{
		title: "鼠标",
		items: []helpItem{
			{"滚轮", "指针在哪一栏就滚哪一栏：左栏挪光标，右栏滚正文，侧栏换文件夹"},
			{"点侧栏", "切换文件夹"},
			{"点列表", "打开那一条会话"},
			{"点标签页", "切到那个会话（标签页在正文上方那一行）"},
			{"点右边缘", "正文里最右那一列是滚动条，点它跳到那一段"},
		},
	},
	{
		title: "禅模式",
		items: []helpItem{
			{"F2", "进禅模式；在里面是回首页，在首页再按一次才离开"},
			{"Tab", "首页 → 会话列表"},
			{"Enter", "首页：把输入框里的话发给当前会话；列表：打开选中的会话"},
			{"Esc", "退一层：会话 → 列表 → 首页"},
			{"↑ / ↓", "列表里上下选；会话里滚正文（输入框为空时）"},
			{"Ctrl+↑ / Ctrl+↓", "会话里直接换上一个 / 下一个会话"},
		},
	},
	{
		title: "通用",
		items: []helpItem{
			{"?", "打开 / 关闭本页（F1 同）"},
			{"Esc", "取消当前操作"},
			{"Ctrl+C", "随时退出"},
		},
	},
	{
		title: "说明",
		items: []helpItem{
			{"▌ 3", "行首左半块是未读标记，右边那个数字是未读条数"},
			{"*", "会话里有被星标的消息"},
			{"两栏 / 单栏", "窗口宽到 96 列是「列表 + 正文」两栏；再窄就退回单栏。" +
				"文件夹不占一栏：Tab 弹出选择器，Ctrl+G 循环（窄屏下这两条路照样在）"},
			{"标签页", "开过两个以上会话之后，正文上方会出现一行标签页，" +
				"按打开的先后固定排列，位置不会自己变"},
			{"删除", "移到服务端的「已删除」文件夹，不是永久删除"},
			{"搜索", "只搜本地已同步的部分，不联网查历史"},
			{"接收全部邮件", "默认只拉最近的；按 a 打开后才会把服务器上的全部历史拉下来"},
			{"正文格式", "HTML 邮件会转成 Markdown（链接、按钮保留成可点的地址），" +
				"这类消息头上带一个 HTML 标记"},
			{"连不上", "退出后运行 clichat -check，它会报出断在哪一步"},
			{"禅模式", "按 F2。它是一套独立的界面：首页（字标 + 输入框）→ 会话列表 → " +
				"会话。三屏共用一列居中的窄栏，没有侧栏、状态栏和消息底色；" +
				"同一个人连着说的几句并成一组，名字和时间只标一次"},
			{"禅模式的宽度", "正文列固定在 56–78 列之间，超宽终端上不会拉满 —— " +
				"一行太长眼睛回扫会累。窄终端上自己的消息也不再靠右，退回单列"},
		},
	},
}

// hintKey 是底部常驻提示里的一项。
//
// short 留空时从 helpSections 的描述里派生出短标签；给值时用于
// 帮助页描述偏长、底部这一行放不下的情况。
type hintKey struct {
	key   string
	short string
}

// hintKeys 是底部常驻提示要展示的键与顺序。
//
// 只列最常用的 —— 这一行要挤进一行终端宽度，塞满反而什么都看不清。
// 完整清单在帮助页（按 ? 打开）。**这里的键必须都在 helpSections 里**，
// 否则等于告诉用户一个不存在的功能；TestListHints_EveryHintedKeyExistsInHelp
// 守着这条，TestListHints_FitsInOneLine 守着宽度上限。
//
// **键名和文案都从这里/helpSections 来，界面上不再有第三份。**
// 以前这里是一整条硬编码字符串，于是加键时只改 helpSections、忘了改它，
// 底部就一直少一个键 —— 本轮新增的 a（接收全部邮件）就是这么丢的，
// 而未开启时状态栏也不显示它，用户完全看不到这个功能存在。
//
// ⚠️ 这里删掉过 Tab（文件夹选择器）。原因是**宽度**，但它删得对：
// 双栏最窄的终端（96 列）减掉侧栏之后只剩 82 列，而这一行原来要 90 列
// —— 于是末尾那个「q 退出」**从来没显示出来过**。一条每天都在截断自己的
// 提示，最先被切掉的往往正是用户最需要的那条（怎么退出）。
//
// 而且 Tab 那个入口现在是冗余的：文件夹切换就在左边那条侧栏上（可以
// 直接点，也可以 Ctrl+G 循环），选择器留在帮助页里就够了。低频的入口
// 该给"每天要用"的键让位。
var hintKeys = []hintKey{
	{"?", "帮助"},
	{"n", "新建"},
	{"/", ""},
	{"a", "全部邮件"},
	{"回车", "打开"},
	{"d", ""},
	{"u", "未读"},
	{"q", ""},
}

// listHints 是列表模式底部常驻的快捷键提示。
//
// 文案从 helpSections 派生，不在这里另写一份 —— 两处各写各的，迟早有
// 一处继续骗人，而且骗的是最该被发现的地方：用户每天看到的那行。
func listHints() string {
	// 跨全部组构建映射：? 在「通用」里，不在会话列表那组。
	desc := map[string]string{}
	for _, s := range helpSections {
		for _, it := range s.items {
			if _, ok := desc[it.label]; !ok {
				desc[it.label] = it.desc
			}
		}
	}

	parts := make([]string, 0, len(hintKeys))
	for _, h := range hintKeys {
		label := h.short
		if label == "" {
			d, ok := desc[h.key]
			if !ok {
				// hintKeys 里写了个 helpSections 中不存在的键。
				// 不静默跳过：那就是「提示里有个不存在的功能」，
				// 比少一个键更坑人。测试会拦住这种情况。
				continue
			}
			// 描述可能很长（帮助页那列有宽度可用，这一行没有）：
			// 在第一个分隔符处取前半句，「接收全部邮件：连历史…」→「接收全部邮件」。
			if i := strings.IndexAny(d, "：:，,（("); i > 0 {
				d = d[:i]
			}
			label = d
		}
		parts = append(parts, h.key+" "+label)
	}
	// 两格空白当分隔符，不是 " · "。
	//
	// " · " 每组要花 3 列，八个键就是 21 列 —— 占掉这一行（最窄双栏下
	// 71 列）的三成，全花在分隔符上。换成一个空格对时，"q 退出" 当场被
	// 挤出屏幕，而那正是用户最需要的那条（怎么退出）。
	//
	// 开头那一格是**左边距**，和状态栏对齐：状态栏走的是 renderStatus
	// 前面那段 pad（见 viewTwoPane / renderInputBlock），少了这一格，
	// 两行就在同一屏里错开一列。
	return " " + strings.Join(parts, "  ")
}

// helpLines 把帮助内容摊平成待渲染的行。
//
// 先截断再上色是这里的硬约束：反过来的话 runewidth 会把 ANSI 转义
// 序列算进显示宽度，行就撑爆了，两栏布局的右边框会跟着错位。
func helpLines(width int) []string {
	keyW := 0
	for _, s := range helpSections {
		for _, it := range s.items {
			if w := lipgloss.Width(it.label); w > keyW {
				keyW = w
			}
		}
	}
	keyW += 2 // 键列与说明之间的间隙

	// 窄终端下留不出「缩进 + 键列 + 至少几个字的说明」，退回纯文本，
	// 总比挤成一团看不清强。
	narrow := width < keyW+10

	var out []string
	for i, s := range helpSections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, styleTitle.Render(truncate(s.title, width)))
		for _, it := range s.items {
			if narrow {
				out = append(out, truncate("  "+it.label+"  "+it.desc, width))
				continue
			}
			desc := truncate(it.desc, width-2-keyW)
			out = append(out, "  "+stylePrompt.Render(padRight(it.label, keyW))+desc)
		}
	}
	return out
}

// helpText 返回帮助页的纯文本形式，供测试断言内容。
func helpText() string {
	var b strings.Builder
	for _, s := range helpSections {
		b.WriteString(s.title + "\n")
		for _, it := range s.items {
			b.WriteString(it.label + " " + it.desc + "\n")
		}
	}
	return b.String()
}
