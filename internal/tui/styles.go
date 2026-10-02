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
)

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
