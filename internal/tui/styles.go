package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// 终端宽度低于这个值就从两栏降级成单栏。
const singlePaneWidth = 80

var (
	styleTitle    = lipgloss.NewStyle().Bold(true)
	styleMuted    = lipgloss.NewStyle().Faint(true)
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleUnread   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	stylePrompt   = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	styleMine     = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleLink     = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
)

// hyperlink 把文本包成 OSC 8 终端超链接。
//
// 支持的终端（iTerm2 / WezTerm / kitty / Windows Terminal / VS Code 内置
// 终端等）里可以直接点击打开；不支持的终端会忽略这段转义序列，只显示文本
// 本身，所以退化是安全的。
//
// ⚠️ 有一条硬约束：**包裹的样式不能带任何属性，尤其不能带 Underline。**
//
// 这是踩了一整轮才挖出来的。现象是链接「看着完全正常，但点不动」：
//
//   - 给 styleLink 加上 .Underline(true) 之后，bubbletea 会给 URL 的
//     **每一个字符** 单独套一串 SGR。抓原始字节能看到
//     \x1b[4;38;5;45;4mh\x1b[0m\x1b[4;38;5;45;4mt\x1b[0m\x1b[4;... 这样。
//   - 于是 \x1b]8;; 后面紧跟的是 \x1b[4;...，OSC 8 被当场截断，
//     终端根本认不出这是个链接。
//   - 去掉下划线后，URL 变回一整段 \x1b[38;5;45mhttps://...\x1b[0m，
//     OSC 8 完整存活。顺带整个画面的字节数从 2292 降到 1090。
//
// 恶劣之处在于 **Go 层的单测抓不到它** —— View() 返回的字符串永远是完整的，
// 是 bubbletea 的渲染器在后面切碎的。所以验证必须抓原始字节：
// 见 link_test.go 里的 TestStyleLink_MustNotCarryAttributes 和 memory 里
// 记的 /tmp/rawdump.py 方法。
func hyperlink(url, text string) string {
	if url == "" {
		return text
	}
	// 结束序列不能省 —— 省了的话它后面所有文字都会被当成同一个链接。
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// senderPalette 是群聊里区分不同发言人的颜色。
//
// 挑的是在中深色终端背景上都能看清的中间调，避开接近黑和接近白的两端。
var senderPalette = []lipgloss.Color{
	"42", "45", "51", "78", "87", "114", "141", "177", "213", "220",
}

// senderStyle 按地址稳定地挑一个颜色。
//
// 「稳定」是重点：同一个人在这次会话里和下次启动时必须是同一个颜色，
// 否则群聊里一眼认人会失效。
func senderStyle(addr string) lipgloss.Style {
	h := fnv.New32a()
	_, _ = h.Write([]byte(addr))
	return lipgloss.NewStyle().Foreground(senderPalette[int(h.Sum32())%len(senderPalette)])
}

// truncate 按**显示宽度**截断字符串。
//
// 不能用 len() 或 []rune 的长度：中文一个字占两列，按 rune 数截断
// 会让中文行比英文行宽一倍，右边的边框就歪了。
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return runewidth.Truncate(s, width-1, "") + "…"
}

// wrapLines 把一段文本按显示宽度折行。
//
// 同样不能用简单的 len() 切分 —— 中文会算错。
func wrapLines(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}

	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		line := ""
		lineWidth := 0
		for _, r := range paragraph {
			rw := runewidth.RuneWidth(r)
			if lineWidth+rw > width {
				out = append(out, line)
				line = ""
				lineWidth = 0
			}
			line += string(r)
			lineWidth += rw
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// padRight 用空格把字符串补齐到指定显示宽度。
//
// 用 lipgloss.Width 而不是 runewidth.StringWidth —— 后者会把 ANSI 转义
// 序列也算进宽度，带样式的行会被补歪，右边的竖线就跟着歪。
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
