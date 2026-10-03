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
			{"Tab", "切换文件夹"},
			{"c", "只看 clichat 消息：再按一次看全部"},
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
			{"滚轮", "滚动：指针在左栏就挪光标，在右栏就滚正文"},
			{"Ctrl+↑ / Ctrl+↓", "上一个 / 下一个会话"},
			{"Esc", "返回列表。会话不关 —— 再按回车就回来"},
			{"Ctrl+Y", "复制最后一条消息的正文"},
			{"Ctrl+U", "标记未读并返回列表"},
			{"Ctrl+T", "星标 / 取消星标"},
			{"Ctrl+D", "删除（移到「已删除」）"},
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
		title: "账户与解锁",
		items: []helpItem{
			{"回车", "解锁：用主密码解开本地凭据，然后连接邮箱"},
			{"Ctrl+P", "显示 / 隐藏密码。几十位的授权码在掩码下抄错一位看不出来"},
			{"↑ / ↓", "启动页上换账号（存了多个邮箱时才有用）"},
			{"Ctrl+N", "添加账号：再走一遍配置向导，配好的账号都留着"},
			{"Ctrl+R", "重配当前账号：重新选服务商、重填凭据（OAuth 的重新授权也走这里）"},
			{"Ctrl+X", "启动页上删掉当前账号的本地凭据（服务器上的邮件不动）"},
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
			{"● 3", "会话前有未读标记，数字是未读条数"},
			{"★", "会话里有被星标的消息"},
			{"@", "会话里有 clichat 消息（按 c 只看这些）"},
			{"clichat", "消息头上这个标记表示这条也是 clichat 发的；" +
				"对方的昵称和名字颜色由对方自己设，随信一起带过来"},
			{"删除", "移到服务端的「已删除」文件夹，不是永久删除"},
			{"搜索", "只搜本地已同步的部分，不联网查历史"},
			{"接收全部邮件", "默认只拉最近的；按 a 打开后才会把服务器上的全部历史拉下来"},
			{"正文格式", "HTML 邮件会转成 Markdown（链接、按钮保留成可点的地址），" +
				"这类消息头上带一个 HTML 标记"},
			{"连不上", "退出后运行 clichat -check，它会报出断在哪一步"},
			{"多账号", "每个账号各自记一份索引和正文缓存，各自用一个授权码；" +
				"主密码只有一个，解锁时选哪个账号就进哪个"},
			{"OAuth 登录", "Gmail 和 Microsoft 365 不再接受密码，只能用 OAuth2。" +
				"要先去服务商控制台注册一个应用、把 client id 填进来（向导里会指路）"},
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

// hintDesc 是「键 → 描述」的映射，从 helpSections 派生。
//
// 提取出来给底部提示（listHints）和两个整页（pages.go 的解锁页 /
// 配置向导）共用 —— 三处各自维护一张文案表的代价，本仓库已经付过了。
func hintDesc() map[string]string {
	desc := map[string]string{}
	for _, s := range helpSections {
		for _, it := range s.items {
			if _, ok := desc[it.label]; !ok {
				desc[it.label] = it.desc
			}
		}
	}
	return desc
}

// hintLine 把一组 hintKey 拼成「键 说明 · 键 说明」。不居中、不加缩进 ——
// 那两件事由调用方按自己版面的需要做。
//
// 说明从 helpSections 取（跨全部组，因为 ? 在「通用」那组里）；找不到时
// **不静默跳过**：那就是「提示里有个不存在的功能」，比少一个键更坑人。
func hintLine(keys []hintKey) string {
	desc := hintDesc()
	parts := make([]string, 0, len(keys))
	for _, h := range keys {
		label := h.short
		if label == "" {
			d, ok := desc[h.key]
			if !ok {
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
	return strings.Join(parts, " · ")
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
// 调整时**只改这一个切片**，并留意别顶到宽度上限：
// 100 列是常见终端宽度，留些余量给以后的文案改动。
//
// ⚠️ 这一行是有预算的（TestListHints_FitsInOneLine 卡 95 列），加一个键
// 就意味着**得挤掉一个**。所以加 c（只看 clichat）的时候把 q 换了下去：
// 两者都不是必须在这一行里的，但 q 退出是 TUI 的通用约定、帮助页里也写着，
// 而 clichat 过滤是这个客户端独有的功能 —— 不在这行露面就没人会发现它。
// 判断该挤掉谁的标准是「不写出来用户会不会找不到这个功能」，
// 不是「哪个键更重要」。
var hintKeys = []hintKey{
	{"?", "帮助"},
	{"n", "新建"},
	{"/", ""},
	{"a", "全部邮件"},
	{"c", "clichat"},
	{"Tab", "文件夹"},
	{"回车", "打开"},
	{"d", ""},
	{"u", "未读"},
}

// listHints 是列表模式底部常驻的快捷键提示。
//
// 文案从 helpSections 派生，不在这里另写一份 —— 两处各写各的，迟早有
// 一处继续骗人，而且骗的是最该被发现的地方：用户每天看到的那行。
func listHints() string {
	return "  " + hintLine(hintKeys)
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
