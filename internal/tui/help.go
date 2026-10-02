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
			{"PgUp / PgDn", "上下翻页"},
			{"Esc", "返回列表"},
			{"Ctrl+Y", "复制最后一条消息的正文"},
			{"Ctrl+U", "标记未读并返回列表"},
			{"Ctrl+T", "星标 / 取消星标"},
			{"Ctrl+D", "删除（移到「已删除」）"},
		},
	},
	{
		title: "通用",
		items: []helpItem{
			{"?  或  F1", "打开 / 关闭本页"},
			{"Esc", "取消当前操作"},
			{"Ctrl+C", "随时退出"},
		},
	},
	{
		title: "说明",
		items: []helpItem{
			{"● 3", "会话前有未读标记，数字是未读条数"},
			{"★", "会话里有被星标的消息"},
			{"删除", "移到服务端的「已删除」文件夹，不是永久删除"},
			{"搜索", "只搜本地已同步的部分，不联网查历史"},
			{"接收全部邮件", "默认只拉最近的；按 a 打开后才会把服务器上的全部历史拉下来"},
			{"正文格式", "HTML 邮件会被转成 Markdown（链接、按钮都保留成可点的地址）"},
			{"连不上", "退出后运行 clichat -check，它会报出断在哪一步"},
		},
	},
}

// listHints 是列表模式底部常驻的快捷键提示。
//
// 只放最常用的几个：这一行会被终端宽度截断，塞满反而什么都看不清。
// 完整清单在 help 页（按 ? 打开）。
func listHints() string {
	return "  ? 帮助 · n 新建 · / 搜索 · Tab 文件夹 · 回车 打开 · d 删除 · q 退出"
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
