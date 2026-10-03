package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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
		m, _ = update(m, keyMsg("f2"))    // 进 Zen（落在首页）
		m, _ = update(m, keyMsg("tab"))   // → 列表
		m, _ = update(m, keyMsg("enter")) // → 会话屏，那条分隔线在这里

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
		if want := m.zenContentWidth(); ruleW != want {
			t.Errorf("宽度 %d：分隔线 %d 列，want %d", w, ruleW, want)
		}
	}
}

// 列表标题必须在正文列里居中。
//
// 这条抓的是「先上色、再量宽度」：viewZenList 原先写
// `f.center(zenFaint.Render("会话"))`，而 center 内部用 textWidth 算补白，
// textWidth 逐 rune 量、**不认 ANSI** —— 转义序列里的 `[`、`3`、`8`、`m`
// 都是可打印字符，于是「会话」4 列被算成 17 列（256 色下
// `\x1b[38;5;240m` + `\x1b[0m` 一共 13 个可打印字符），补白少 7 列。
//
// 症状是「看着好像有点歪」，量出来才知道偏了 7 列 —— 而且**偏多少取决于
// 色深**（16 色下序列更短，偏得少），所以这里必须先把色深钉成 256 色再断言，
// 否则这条判据在不同环境下给出不同结论。
func TestZenList_TitleCentredInContentColumn(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	ansiSeq := regexp.MustCompile("\x1b\\[[0-9;]*m")

	for _, w := range []int{60, 72, 80, 100, 120, 160, 200, 210} {
		m := newSampleModel(t)
		m, _ = update(m, tea.WindowSizeMsg{Width: w, Height: 30})
		m, _ = update(m, keyMsg("f2"))  // 进 Zen（落在首页）
		m, _ = update(m, keyMsg("tab")) // → 列表

		var title string
		for _, ln := range strings.Split(m.View(), "\n") {
			if strings.TrimSpace(ansiSeq.ReplaceAllString(ln, "")) == "会话" {
				title = ln
				break
			}
		}
		if title == "" {
			t.Fatalf("宽度 %d：列表里找不到标题行", w)
		}

		contentW := m.zenContentWidth()
		got := lipgloss.Width(title) - lipgloss.Width(strings.TrimLeft(title, " "))
		want := zenLeftPad(w, contentW) + (contentW-textWidth("会话"))/2
		if got != want {
			t.Errorf("宽度 %d：标题落在第 %d 列，want %d —— 居中的补白把转义序列算宽了",
				w, got, want)
		}
	}
}
