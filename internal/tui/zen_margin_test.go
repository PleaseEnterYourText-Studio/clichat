package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// 正文列必须在**任何终端宽度**下都左右对称，且每行都补齐到终端全宽。
//
// 这组判据抓过一个真 bug，值得写清楚它是怎么漏出去的：
//
// 原先 viewZen 只画到正文列右沿就收手，行尾**没有**补白。后果分两种：
//
//   - 真实终端里，行尾之外由终端背景兜着，肉眼看不出任何问题；
//   - 但任何按「最长行」开窗的渲染器会把窗口开成「左留白 + 正文列」那么宽
//     （截图链路里的 freeze 正是如此），于是正文列贴到窗口右边，居中排版
//     整个作废 —— README 里那几张截图就是这么歪的。
//
// 而当时的单测只断言「分隔线宽度 == zenContentWidth」和「左边距 ==
// zenLeftPad」，两条都只看了**左半边**，全绿。
//
// 教训：验居中要同时量左右两侧，而且右边距必须拿**终端宽度**去减，
// 不能拿那一行自己的宽度去减 —— 后者会把行尾的补白算成「不存在」，
// 右边距永远是 0，看着还挺像「没居中」的。
func TestZen_ContentColumnCentredAtEveryWidth(t *testing.T) {
	for _, w := range []int{40, 50, 56, 58, 60, 64, 70, 72, 80, 90, 100, 120, 140, 160, 200} {
		m := newSampleModel(t)
		m, _ = update(m, tea.WindowSizeMsg{Width: w, Height: 30})
		m, _ = update(m, keyMsg("enter"))
		m = loadBodies(t, m)
		m, _ = update(m, keyMsg("f2"))

		lines := strings.Split(m.View(), "\n")

		// 每行都必须是终端全宽 —— 这是「右边距存在」的前提。
		for i, ln := range lines {
			if got := lipgloss.Width(ln); got != w {
				t.Errorf("宽度 %d：第 %d 行 %d 列，want %d（行尾没补白）",
					w, i, got, w)
			}
		}

		// 用分隔线那行量：它正好铺满正文列。
		var left, ruleW int
		var found bool
		for _, ln := range lines {
			trimmed := strings.TrimSpace(ln)
			if trimmed != "" && strings.Trim(trimmed, "─") == "" {
				ruleW = lipgloss.Width(trimmed)
				left = lipgloss.Width(ln) - lipgloss.Width(strings.TrimLeft(ln, " "))
				found = true
			}
		}
		if !found {
			t.Fatalf("宽度 %d：找不到输入框上方那条分隔线", w)
		}

		right := w - left - ruleW
		if left != right {
			t.Errorf("宽度 %d：正文列左 %d 右 %d —— 没居中", w, left, right)
		}
		if want := zenContentWidth(w); ruleW != want {
			t.Errorf("宽度 %d：分隔线 %d 列，want %d", w, ruleW, want)
		}
	}
}
