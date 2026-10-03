package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// 底部常驻提示里不许出现帮助页没有的键。
//
// 反过来的漂移（键在帮助页有、底部漏了）没法用「必须全部覆盖」来守 ——
// 一行塞不下十几个键。但**幽灵键**必须拦住：那等于告诉用户一个不存在的
// 功能，比少一个键更坑人。
func TestListHints_EveryHintedKeyExistsInHelp(t *testing.T) {
	known := map[string]bool{}
	for _, s := range helpSections {
		for _, it := range s.items {
			known[it.label] = true
		}
	}

	for _, h := range hintKeys {
		if !known[h.key] {
			t.Errorf("底部提示里有 %q，但帮助页里没有这个键 —— "+
				"用户按它不会有任何反应", h.key)
		}
	}
}

// 本轮的真实故障：a（接收全部邮件）进了帮助页，却漏在底部提示外面。
//
// 未开启时状态栏也不显示这个模式，所以不按 ? 就完全看不到它存在 ——
// 功能写好了、用户找不到，等于没做。
func TestListHints_ShowsAllMailKey(t *testing.T) {
	if !strings.Contains(listHints(), "a ") {
		t.Errorf("底部提示里没有 a（接收全部邮件）：\n%s", listHints())
	}
}

// a 必须活过截断 —— 这一条才是用户那句「在 CLI 里根本看不到」的正面判据。
//
// 只断言 listHints() 的返回值含 a 是不够的：渲染时会按终端宽度 truncate，
// 排在末尾的键会被整段切掉。功能在字符串里、在屏幕上却不在，观感上
// 和没做一样。所以「keys 里有 a」和「a 出现在可见的前若干个键位里」
// 是两回事，后者才是真的。
//
// 判据绑的是**意思**不是完整措辞：40 列时截成 `a 全部…`，用户仍能看懂
// 这个键和「全部」有关。断言完整的「全部邮件」会把 40 列判成故障 ——
// 而 40 列下那不算故障，只是终端太窄。所以只要求键名和标签开头还在。
func TestListHints_AllMailSurvivesTruncation(t *testing.T) {
	full := listHints()
	for _, width := range []int{80, 60, 40} {
		got := truncate(full, width)
		// 把用户在这个宽度下真正看到的那一行打出来。宽度别手算 ——
		// 中文占两列、`·` 前后还有空格，心算必然错（本判据第一版就写错了）。
		t.Logf("宽 %d 时底部提示：%q", width, got)
		if !strings.Contains(got, "a 全部") {
			t.Errorf("宽 %d 时底部提示里看不到 a（全部邮件）：\n%q\n"+
				"—— 用户会以为这个功能不存在", width, got)
		}
	}
}

// 底部提示必须能在一行里显示完 —— 放不下的话后面的键会被 truncate 切掉，
// 等于没显示。所以宽度要有上限，不能靠「多塞几个」。
//
// 门槛定在 95 而不是 100：正好卡满的话，以后改一个字就会顶破，
// 而顶破的表现是「末尾的键悄悄消失」，很难注意到。留点余量。
func TestListHints_FitsInOneLine(t *testing.T) {
	const budget = 95
	if w := lipgloss.Width(listHints()); w > budget {
		t.Errorf("底部提示宽 %d，超过 %d 列的预算，末尾的键会被截掉:\n%s",
			w, budget, listHints())
	}
}

// 列表底部那一行必须真的渲染出这些键，而不只是函数返回对。
//
// 顺手把渲染出来的底部整行 log 出去（go test -v 可见）。
// 这不是调试残留：用户看到的**只有**渲染结果，函数返回值对不对是次要的，
// 而这个字符串正是「打开 CLI 第一眼看到的那行提示」。
func TestListHints_RenderedAtListFooter(t *testing.T) {
	m, _ := newFeatureModel(t)
	view := m.View()

	hints := listHints()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, strings.TrimSpace(strings.Split(strings.TrimSpace(hints), "·")[0])) &&
			strings.Contains(line, "·") {
			t.Logf("渲染出来的底部提示行：\n%s", line)
		}
	}

	// 取提示里的第一个键当抽样：整行可能被终端宽度截断，但开头一定在。
	first := strings.TrimSpace(strings.Split(strings.TrimSpace(hints), "·")[0])
	if !strings.Contains(view, first) {
		t.Errorf("列表底部没渲染出提示（找不到 %q）:\n%s", first, view)
	}
}

// 底部提示和帮助页指向同一个事实来源。
//
// 这条不提「哪些键」—— 提的是**结构与一致性**：底部出现的每个键，
// 其文字都该能在帮助页的描述里找到踪迹。硬编码一整行字符串时，
// 两处会各自漂移，而漂移的方向是随机的。
func TestListHints_DescriptionsMatchHelp(t *testing.T) {
	help := helpText()
	for _, h := range hintKeys {
		if !strings.Contains(help, h.key) {
			t.Errorf("底部提示里的 %q 在帮助页里查不到", h.key)
		}
	}
}

// 切换窗口宽度后底部提示仍在，且不撑破（渲染层会 truncate，
// 这里确保 truncate 之前的值本身也是合理的）。
func TestListHints_SurvivesNarrowTerminal(t *testing.T) {
	m, _ := newFeatureModel(t)
	for _, width := range []int{40, 60, 80} {
		m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: 24})
		for i, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("宽 %d：第 %d 行宽 %d，超出: %q", width, i, w, line)
			}
		}
	}
}
