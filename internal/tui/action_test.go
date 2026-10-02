package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// 这个文件守的是**一条规矩**，不是一组坐标：
//
//	凡是只能靠 Esc 离开的界面，页脚右下角必须长出一格能点的等价按钮，
//	而且点它和按 Esc 必须走同一段代码。
//
// 所以下面这些判据的重点不是「按钮在不在」（那只是必要条件），而是
// 「点它和按 Esc 的结果逐字段一样」。一个「也有按钮、但按钮自己另写一套
// 逻辑」的实现能过前一半，过不了后一半 —— 而那种实现正是要防的：两条路
// 一旦分家，改了键盘那条没人会发现鼠标那条已经不对了。

// clickFooterButton 在**屏幕上真的点一下**页脚上某一格按钮。
//
// 刻意走完整的鼠标通路（Update → handleMouse → actionAt → runAction），
// 而不是直接调 runAction：少了任何一段，「按钮画出来了、鼠标事件没人接」
// 这类错就漏掉了 —— 而它正是最可能发生的一种（动作栏的判定写在
// handleMouse 的最前面，谁把它挪到某个 mode 分支后面，屏幕上什么都不会变）。
//
// 坐标由 footerSlot + actionSpans 算出来，和渲染用的是同两个函数。
func clickFooterButton(t *testing.T, m Model, k actionKind) (Model, bool) {
	t.Helper()

	row, origin, w := m.footerSlot(m.measureLayout())
	if row < 0 {
		return m, false
	}
	for _, a := range m.actionSpans(w) {
		if a.kind != k {
			continue
		}
		x := origin + a.x0 + actionWidth(a)/2
		next, _ := update(m, tea.MouseMsg{
			X: x, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		})
		return next, true
	}
	return m, false
}

// findAction 在页脚上找某一类按钮。
func findAction(m Model, k actionKind) (action, bool) {
	for _, a := range m.footerActions() {
		if a.kind == k {
			return a, true
		}
	}
	return action{}, false
}

// snapshot 把「用户看得出来的一切」压成一行，供「点按钮 vs 按键」的比较用。
//
// 只比 mode 是不够的：一个「也回列表、但顺手把搜索词清了」的实现照样绿，
// 而用户看到的是「点了取消，搜索结果没了」。所以这里逐字段比到输入框里的
// 字为止。
func snapshot(m Model) string {
	return fmt.Sprintf("mode=%v layout=%v screen=%v query=%q folder=%q active=%q input=%q replyAll=%v busy=%v zenHint=%v",
		m.mode, m.layout, m.zenScreen, m.query, m.activeFolder,
		m.activeID, m.input.Value(), m.replyAll, m.busy, m.zenShowHint)
}

// assertDismissMatchesEsc 断言这一屏有「退出去」那一格，且点它 == 按 Esc。
func assertDismissMatchesEsc(t *testing.T, m Model, where string) {
	t.Helper()

	a, ok := findAction(m, actDismiss)
	if !ok {
		t.Fatalf("%s：页脚上没有「退出去」那一格 —— 这一屏只能靠 Esc 离开，"+
			"而用鼠标走到这儿的人没有出路", where)
	}
	if a.label == "" {
		t.Fatalf("%s：那一格按钮没有标签，用户看不见它", where)
	}

	byEsc, _ := update(m, keyMsg("esc"))
	byClick, ok := clickFooterButton(t, m, actDismiss)
	if !ok {
		t.Fatalf("%s：页脚上算不出「%s」那一格的位置", where, a.label)
	}

	if got, want := snapshot(byClick), snapshot(byEsc); got != want {
		t.Errorf("%s：点「%s」和按 Esc 的结果不一样\n  点按钮 → %s\n  按 Esc → %s",
			where, a.label, got, want)
	}
}

// ---- 只能靠 Esc 离开的每一屏 ----

func TestAction_HelpScreenIsClickable(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("?"))
	if m.mode != modeHelp {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	assertDismissMatchesEsc(t, m, "帮助页")
}

func TestAction_SearchIsClickable(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("/"))
	if m.mode != modeSearch {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	assertDismissMatchesEsc(t, m, "搜索")
}

func TestAction_FolderPickerIsClickable(t *testing.T) {
	m, _ := newFeatureModel(t)
	// Tab 只是把「拉文件夹列表」的命令发出去；选择器是在结果回来那一步
	// 才打开的（见 handleListKey 的 tab 分支和 foldersMsg）。所以这一步
	// 要把命令跑完，否则前提就不成立。
	m, cmd := update(m, keyMsg("tab"))
	m = runCmd(t, m, cmd)
	if m.mode != modeFolder {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	assertDismissMatchesEsc(t, m, "文件夹选择器")
}

func TestAction_ForwardIsClickable(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("f"))
	// 输入框里留一点东西再取消，否则「取消时该把地址清掉」这一步不可观测，
	// 漏写它判据也不会红（和下面新建会话那条同一个道理）。
	m, _ = update(m, keyMsg("x"))
	if m.mode != modeForward {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	assertDismissMatchesEsc(t, m, "转发")
}

func TestAction_ConfirmIsClickable(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("d"))
	if m.mode != modeConfirm {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	assertDismissMatchesEsc(t, m, "删除确认")
}

func TestAction_NewChatIsClickable(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("n"))
	m, _ = update(m, keyMsg("x")) // 输入框里留一点东西，取消时该被清掉
	if m.mode != modeNewChat {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	assertDismissMatchesEsc(t, m, "新建会话")
}

func TestAction_ChatScreenIsClickable(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeChat {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	assertDismissMatchesEsc(t, m, "会话")
}

// 「点确认」和按 y 必须一样：删会话是不可逆感受最强的一步（虽然实际是
// 移到「已删除」），两条路走出两个结果最难接受。
func TestAction_ConfirmButtonMatchesY(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("d"))
	if m.mode != modeConfirm {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	if _, ok := findAction(m, actConfirm); !ok {
		t.Fatal("确认框上只有「取消」、没有「确认」—— 鼠标用户没法把这件事办完")
	}

	byKey, _ := update(m, keyMsg("y"))
	byClick, ok := clickFooterButton(t, m, actConfirm)
	if !ok {
		t.Fatal("页脚上算不出「确认」那一格的位置")
	}

	if got, want := snapshot(byClick), snapshot(byKey); got != want {
		t.Errorf("点「确认」和按 y 的结果不一样\n  点按钮 → %s\n  按 y   → %s", got, want)
	}
}

// 搜索的两个出口各对应一个键：回车保留过滤，Esc 清空。
func TestAction_SearchButtonsMatchEnterAndEsc(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("/"))
	m.input.SetValue("ali")
	m.query = "ali"
	m.refreshVisible()
	if len(m.visible) == 0 {
		t.Fatal("前提不成立：搜索词没过滤出任何会话")
	}

	apply, ok := findAction(m, actApply)
	if !ok {
		t.Fatal("搜索里没有「保留过滤」那一格 —— 鼠标用户只能清空了走")
	}
	byEnter, _ := update(m, keyMsg("enter"))
	byClick, ok := clickFooterButton(t, m, actApply)
	if !ok {
		t.Fatalf("页脚上算不出「%s」那一格的位置", apply.label)
	}
	if got, want := snapshot(byClick), snapshot(byEnter); got != want {
		t.Errorf("点「%s」和按回车的结果不一样\n  点按钮 → %s\n  按回车 → %s",
			apply.label, got, want)
	}

	dismiss, _ := findAction(m, actDismiss)
	byEsc, _ := update(m, keyMsg("esc"))
	byClick, ok = clickFooterButton(t, m, actDismiss)
	if !ok {
		t.Fatalf("页脚上算不出「%s」那一格的位置", dismiss.label)
	}
	if got, want := snapshot(byClick), snapshot(byEsc); got != want {
		t.Errorf("点「%s」和按 Esc 的结果不一样\n  点按钮 → %s\n  按 Esc  → %s",
			dismiss.label, got, want)
	}
}

// 列表就是家，底下那一行留给快捷键提示，不该长出按钮。
func TestAction_ListModeHasNoFooterButtons(t *testing.T) {
	m, _ := newFeatureModel(t)
	if m.mode != modeList {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	if as := m.footerActions(); len(as) != 0 {
		t.Errorf("列表模式长出了 %d 格按钮：%+v —— 列表没有「上一层」可退", len(as), as)
	}
	if bar := m.actionSpans(m.measureLayout().paneW); len(bar) != 0 {
		t.Errorf("列表模式的页脚占了 %d 格：%+v", len(bar), bar)
	}
}

// ---- 坐标：点到的 == 看到的 ----

// 按钮画在哪一列，就必须能在那一列点到。两者走的是同一份 actionSpans，
// 但**起点**（页脚内容从第几列开始）是另一回事 —— 两栏模式下它就是那只
// 「列表栏 + 竖线 + 1 格内缩」的偏移，少减或多减一次，症状都是「差一列点
// 不中」，而且只在某一种窗口宽度下出现。
func TestAction_RenderedBarSitsWhereClicksLand(t *testing.T) {
	screens := []struct {
		name string
		open func(t *testing.T) Model
	}{
		{"帮助页（自占一屏）", func(t *testing.T) Model {
			m, _ := newFeatureModel(t)
			m, _ = update(m, keyMsg("?"))
			return m
		}},
		{"确认框（两栏）", func(t *testing.T) Model {
			m, _ := newFeatureModel(t)
			m, _ = update(m, keyMsg("d"))
			return m
		}},
		{"会话（两栏）", func(t *testing.T) Model {
			m, _ := newFeatureModel(t)
			m, _ = update(m, keyMsg("enter"))
			return m
		}},
		{"会话（单栏）", func(t *testing.T) Model {
			m, _ := newFeatureModel(t)
			m, _ = update(m, tea.WindowSizeMsg{Width: 78, Height: 30})
			m, _ = update(m, keyMsg("enter"))
			if m.measureLayout().twoPane {
				t.Fatal("前提不成立：78 列应该是单栏")
			}
			return m
		}},
		{"Zen 首页", func(t *testing.T) Model { return zenHomeModel(t) }},
		{"Zen 会话", func(t *testing.T) Model { return zenChatModel(t) }},
	}

	for _, sc := range screens {
		t.Run(sc.name, func(t *testing.T) {
			forceColor(t)
			m := sc.open(t)

			l := m.measureLayout()
			row, origin, w := m.footerSlot(l)
			if row < 0 {
				t.Fatalf("%s：没有页脚那一行", sc.name)
			}
			rows := strings.Split(m.View(), "\n")
			if row >= len(rows) {
				t.Fatalf("%s：View 只有 %d 行，页脚在第 %d 行 —— 根本没画出来",
					sc.name, len(rows), row)
			}
			line := stripANSI(rows[row])

			spans := m.actionSpans(w)
			if len(spans) == 0 {
				t.Fatalf("%s：页脚上没有动作栏", sc.name)
			}
			for _, a := range spans {
				want := stripANSI(renderActionBar([]action{a}))
				idx := strings.Index(line, want)
				if idx < 0 {
					t.Errorf("%s：页脚那一行里找不到按钮 %q\n  %s", sc.name, want, line)
					continue
				}
				col := textWidth(line[:idx])
				if col != origin+a.x0 {
					t.Errorf("%s：按钮 %q 画在第 %d 列，而命中测试算的是第 %d 列",
						sc.name, a.label, col, origin+a.x0)
				}
				hit, ok := m.actionAt(col+actionWidth(a)/2, row)
				if !ok || hit.kind != a.kind || hit.label != a.label {
					t.Errorf("%s：点按钮 %q 正中那一列，命中的却是 %+v（ok=%v）",
						sc.name, a.label, hit, ok)
				}
			}
		})
	}
}

// 窄到放不下时，先丢左边的，**必须保住最右那一格** —— 它才是「退出去」。
//
// 左对齐的写法正好相反：被挤出屏幕的是出路，而屏幕上不会有任何东西报错。
// 这个仓库已经犯过一次（见 help.go 里 hintKeys 那段：「q 退出」曾经从来
// 没显示出来过）。
func TestAction_NarrowFooterDropsFromTheLeft(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("d"))
	if m.mode != modeConfirm {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}

	// 宽裕时两格都在。
	if got := m.actionSpans(40); len(got) != 2 {
		t.Fatalf("40 列放得下两格，实际 %d 格：%+v", len(got), got)
	}

	// 只放得下最右一格。
	exitW := actionWidth(action{label: "取消"})
	got := m.actionSpans(exitW)
	if len(got) != 1 {
		t.Fatalf("只放得下一格时应该留 1 格，实际 %d 格：%+v", len(got), got)
	}
	if got[0].kind != actDismiss {
		t.Errorf("被保住的是 %q，want「取消」—— 窄终端上不能把出路丢掉", got[0].label)
	}

	// 一格都放不下时不硬塞（硬塞会盖住左边的内容）。
	if got := m.actionSpans(3); len(got) != 0 {
		t.Errorf("3 列放不下任何一格，实际 %+v", got)
	}

	// 任何宽度下渲染出来的东西都不能比自己那份预算宽。
	for _, w := range []int{1, 4, 9, 12, 17, 18, 40, 63} {
		bar := m.actionSpans(w)
		if got := lipgloss.Width(renderActionBar(bar)); got > w {
			t.Errorf("宽度 %d 下动作栏画了 %d 列", w, got)
		}
	}
}

// 状态文字再长也不能把出路挤掉。
//
// 这是 actionRoom 存在的全部理由，也是这类界面最容易悄悄坏掉的地方：
// 屏幕上看不出少了什么，只是有一天用户发现「这儿永远点不动」。
func TestAction_LongStatusNeverPushesTheExitOffScreen(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("d"))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m.status = strings.Repeat("同步失败：连接被重置。", 8)
	m.lastSync = m.lastSync.Add(1)

	l := m.measureLayout()
	row, origin, w := m.footerSlot(l)
	rows := strings.Split(m.View(), "\n")
	if row >= len(rows) {
		t.Fatalf("页脚在第 %d 行，View 只有 %d 行", row, len(rows))
	}

	a, ok := findAction(m, actDismiss)
	if !ok {
		t.Fatal("页脚上没有出路")
	}
	spans := m.actionSpans(w)
	var hit action
	for _, s := range spans {
		if s.kind == actDismiss {
			hit = s
		}
	}
	want := stripANSI(renderActionBar([]action{a}))
	line := stripANSI(rows[row])
	idx := strings.Index(line, want)
	if idx < 0 {
		t.Fatalf("状态文字把「%s」挤出屏幕了：\n%s", want, line)
	}
	if col := textWidth(line[:idx]); col != origin+hit.x0 {
		t.Errorf("按钮画在第 %d 列，命中测试算的是第 %d 列", col, origin+hit.x0)
	}
}

// ---- Zen ----

// Zen 的三屏各有一格出路，而且都在屏幕最后一行。
func TestZenAction_EveryScreenHasAFooterOnTheLastRow(t *testing.T) {
	screens := []struct {
		name  string
		open  func(t *testing.T) Model
		want  string
		leave func(m Model) bool
	}{
		{"首页", zenHomeModel, "退出 Zen", func(m Model) bool { return !m.zenActive() }},
		{"列表", func(t *testing.T) Model {
			m := zenHomeModel(t)
			m, _ = update(m, keyMsg("tab"))
			return m
		}, "回首页", func(m Model) bool { return m.zenScreen == zenHome }},
		{"会话", zenChatModel, "会话列表", func(m Model) bool { return m.zenScreen == zenList }},
	}

	for _, sc := range screens {
		t.Run(sc.name, func(t *testing.T) {
			forceColor(t)
			m := sc.open(t)

			a, ok := findAction(m, actDismiss)
			if !ok {
				t.Fatalf("%s：Zen 页脚上没有出路", sc.name)
			}
			if a.label != sc.want {
				t.Errorf("%s：按钮写的是 %q，want %q", sc.name, a.label, sc.want)
			}

			rows := strings.Split(m.View(), "\n")
			last := stripANSI(rows[len(rows)-1])
			if !strings.Contains(last, stripANSI(renderActionBar([]action{a}))) {
				t.Errorf("%s：最后一行的页脚里没有那一格按钮\n  %q", sc.name, last)
			}

			row, _, _ := m.footerSlot(m.measureLayout())
			if row != len(rows)-1 {
				t.Errorf("%s：动作栏算在第 %d 行，而 View 的最后一行是第 %d 行",
					sc.name, row, len(rows)-1)
			}

			after, ok := clickFooterButton(t, m, actDismiss)
			if !ok {
				t.Fatalf("%s：Zen 页脚上没有「%s」那一格", sc.name, sc.want)
			}
			if !sc.leave(after) {
				t.Errorf("%s：点了「%s」之后没有离开这一屏（screen=%v layout=%v）",
					sc.name, a.label, after.zenScreen, after.layout)
			}
		})
	}
}

// 点列表里的一行就该进去 —— 那一屏的本质就是「挑一个进去读」。
func TestZenAction_ClickListItemOpensThatThread(t *testing.T) {
	forceColor(t)
	m := zenHomeModel(t)
	m, _ = update(m, keyMsg("tab"))
	if m.zenScreen != zenList {
		t.Fatalf("前提不成立：screen=%v", m.zenScreen)
	}
	if len(m.visible) < 2 {
		t.Fatalf("前提不成立：列表里只有 %d 条", len(m.visible))
	}

	// 挑第 2 条：它和光标当前所在的那一条不同，才测得出「点的是被点的那条」。
	m.cursor = 0
	target := 1
	room := m.zenListRoom()
	top := m.zenListTop(room)
	if target < top || target >= top+room {
		t.Fatalf("前提不成立：第 %d 条不在可见窗口 [%d,%d) 里", target, top, top+room)
	}

	row := zenListHeadRows + (target - top)
	next, _ := update(m, tea.MouseMsg{
		X: 2, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if next.zenScreen != zenChat {
		t.Fatalf("点列表第 %d 行（%q）之后没有进去：screen=%v", row,
			threadTitle(m.visible[target]), next.zenScreen)
	}
	if next.activeID != m.visible[target].ID {
		t.Errorf("点的是第 %d 条（%q），打开的却是 %q",
			target, threadTitle(m.visible[target]), next.activeID)
	}
}

// Zen 的滚轮只做屏幕上看得见的事。
//
// 通用那条路拿的是 Normal 的几何（左四分之一归列表栏）—— 在 Zen 里用它，
// 滚轮会去挪一个屏幕上看不见的光标：屏幕纹丝不动，底下却变了。
func TestZenAction_WheelNeverMovesAnInvisibleCursor(t *testing.T) {
	forceColor(t)

	// 首页：滚轮什么也不该做（那一屏只有一个输入框）。
	m := zenHomeModel(t)
	before := snapshot(m)
	after, _ := update(m, tea.MouseMsg{
		X: 2, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
	})
	if got := snapshot(after); got != before {
		t.Errorf("Zen 首页的滚轮动了状态：\n  前 %s\n  后 %s", before, got)
	}

	// 列表：滚轮挪光标（挪的是看得见的那一条）。
	m = zenHomeModel(t)
	m, _ = update(m, keyMsg("tab"))
	m.cursor = 0
	after, _ = update(m, tea.MouseMsg{
		X: 2, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
	})
	if after.cursor != 1 {
		t.Errorf("列表里向下滚一格，光标从 0 走到了 %d，want 1", after.cursor)
	}
}
