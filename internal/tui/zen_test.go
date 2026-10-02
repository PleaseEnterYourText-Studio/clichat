package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// zenReadyModel 造一个打开着会话、但**还没进 Zen** 的模型。
func zenReadyModel(t *testing.T) Model {
	t.Helper()

	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m, _ = update(m, keyMsg("enter"))
	return loadBodies(t, m)
}

// zenHomeModel 造一个已经进入 Zen 首页的模型。
//
// 先在 Normal 里打开一个会话：F2 是从会话里按的，而且首页的输入框要有个
// 默认发送对象才测得下去。
func zenHomeModel(t *testing.T) Model {
	t.Helper()

	m := zenReadyModel(t)
	m, _ = update(m, keyMsg("f2"))
	return m
}

// zenChatModel 进 Zen 并切到会话屏（首页 → Tab 列表 → 回车会话）。
func zenChatModel(t *testing.T) Model {
	t.Helper()

	m := zenHomeModel(t)
	m, _ = update(m, keyMsg("tab"))
	m, _ = update(m, keyMsg("enter"))
	return m
}

// ---- 三屏导航 ----

// F2 从 Normal 会话进 Zen，落在首页；在首页再按 F2 才离开。
func TestZen_F2EntersAtHomeAndLeavesFromHome(t *testing.T) {
	m := zenReadyModel(t)

	if m.layout != layoutNormal {
		t.Fatalf("初始 layout = %v，want layoutNormal", m.layout)
	}

	m, _ = update(m, keyMsg("f2"))
	if m.layout != layoutZen {
		t.Fatalf("按 F2 之后 layout = %v，want layoutZen", m.layout)
	}
	if m.zenScreen != zenHome {
		t.Fatalf("进 Zen 应落在首页，got screen=%v", m.zenScreen)
	}

	m, _ = update(m, keyMsg("f2"))
	if m.layout != layoutNormal {
		t.Fatalf("首页按 F2 应离开 Zen，got %v", m.layout)
	}
	// 出 Zen 要回到进之前那个界面，不能掉到列表。
	if m.mode != modeChat {
		t.Errorf("出 Zen 后应回到会话，mode = %v", m.mode)
	}
}

// 会话里按 F2 是「回首页」，**不是**「关掉 Zen」。
//
// 这条守着一次实测出来的预期：F2 是从会话里按进来的，进去之后再按同一个键，
// 用户想的是「回到刚才那个起点」，不是「把整个 Zen 关掉重来」。
func TestZen_F2FromChatGoesHome(t *testing.T) {
	m := zenChatModel(t)
	id := m.activeID

	m, _ = update(m, keyMsg("f2"))
	if !m.zenActive() {
		t.Fatal("会话里按 F2 不该退出 Zen")
	}
	if m.zenScreen != zenHome {
		t.Fatalf("会话里按 F2 应回首页，got %v", m.zenScreen)
	}
	if m.activeID != id {
		t.Errorf("回首页不该丢会话：%q -> %q", id, m.activeID)
	}
}

// 首页 → Tab → 列表 → 回车 → 会话；再一路 Esc 退回来。
func TestZen_NavigationRoundTrip(t *testing.T) {
	m := zenHomeModel(t)

	m, _ = update(m, keyMsg("tab"))
	if m.zenScreen != zenList {
		t.Fatalf("Tab 应到列表，got %v", m.zenScreen)
	}

	m, _ = update(m, keyMsg("enter"))
	if m.zenScreen != zenChat {
		t.Fatalf("回车应开会话，got %v", m.zenScreen)
	}
	if m.activeID == "" {
		t.Error("开了会话却没有 activeID")
	}

	// Esc 一次退一层，全程不离开 Zen。
	m, _ = update(m, keyMsg("esc"))
	if m.zenScreen != zenList {
		t.Fatalf("会话里 Esc 应回列表，got %v", m.zenScreen)
	}
	m, _ = update(m, keyMsg("esc"))
	if m.zenScreen != zenHome {
		t.Fatalf("列表里 Esc 应回首页，got %v", m.zenScreen)
	}
	if !m.zenActive() {
		t.Error("一路 Esc 不该把 Zen 关掉 —— 离开要用 F2 或首页上的 Esc")
	}
}

// Esc 只退一层，**不关会话**。和 Normal 那边同一条原则。
func TestZen_EscDoesNotCloseThread(t *testing.T) {
	m := zenChatModel(t)
	id := m.activeID

	m, _ = update(m, keyMsg("esc"))
	if m.activeID != id {
		t.Errorf("Esc 把会话丢了：%q -> %q", id, m.activeID)
	}
}

// 列表里上下选，回车打开的是选中的那条。
func TestZen_ListSelectsAndOpens(t *testing.T) {
	m := zenHomeModel(t)
	m, _ = update(m, keyMsg("tab"))

	if len(m.visible) < 2 {
		t.Fatal("样例数据里会话太少，测不了选择")
	}

	start := m.cursor
	m, _ = update(m, keyMsg("down"))
	if m.cursor != start+1 {
		t.Fatalf("↓ 之后 cursor = %d，want %d", m.cursor, start+1)
	}
	m, _ = update(m, keyMsg("up"))
	if m.cursor != start {
		t.Fatalf("↑ 之后 cursor = %d，want %d", m.cursor, start)
	}

	want := m.visible[m.cursor].ID
	m, _ = update(m, keyMsg("enter"))
	if m.activeID != want {
		t.Errorf("打开的会话不对：got %q want %q", m.activeID, want)
	}
	if m.zenScreen != zenChat {
		t.Errorf("回车后应在会话屏，got %v", m.zenScreen)
	}
}

// Zen 是**布局**维度，不是新的 mode —— 切进去之后 mode 必须原样保留。
//
// 保留它是有用的：出 Zen 时要靠它回到原来那个界面。
func TestZen_DoesNotChangeMode(t *testing.T) {
	m := zenReadyModel(t)

	before := m.mode
	m, _ = update(m, keyMsg("f2"))

	if m.mode != before {
		t.Errorf("进 Zen 动了 mode：%v -> %v。mode 记的是 Normal 世界停在哪，"+
			"出 Zen 时要用它回去", before, m.mode)
	}
}

// 首页那个输入框：输入后回车，消息发给当前会话。
func TestZen_HomeInputSendsToCurrentThread(t *testing.T) {
	m := zenHomeModel(t)
	want := m.activeID
	if want == "" {
		t.Fatal("前提不成立：应该有一个打开的会话")
	}

	m.input.SetValue("从首页发的一句")
	m, cmd := update(m, keyMsg("enter"))

	if cmd == nil {
		t.Fatal("回车应该产生一条发送命令")
	}
	if m.input.Value() != "" {
		t.Errorf("发出后输入框该清空，got %q", m.input.Value())
	}
	if m.activeID != want {
		t.Errorf("发送对象变了：%q -> %q", want, m.activeID)
	}
}

// 首页和会话屏的输入框必须是聚焦的，列表上必须失焦。
//
// 这条抓过一个真 bug（用户原话「f2 后 tab 有 bug」）：输入框的焦点原先一直
// **继承**着进 Zen 之前的状态 —— 从 Normal 会话进 Zen 恰好是聚焦的（那边本来
// 就在打字），所以一路没暴露；从 Normal **列表**进 Zen 就是失焦的，首页上敲
// 什么都没反应。而且失焦的输入框**看不出来**：提示符和占位文字照画不误，
// 只能靠「按了没反应」发现。
//
// 下面这条特意从 **Normal 列表**出发 —— 之前所有测试都先开一个会话，
// 正好绕开了出问题的那条路。
func TestZen_InputFocusFollowsScreen(t *testing.T) {
	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})

	if m.input.Focused() {
		t.Fatal("前提不成立：Normal 列表上输入框本该是失焦的")
	}

	m, _ = update(m, keyMsg("f2"))
	if !m.input.Focused() {
		t.Error("Zen 首页的输入框必须聚焦 —— 否则首页上打不了字")
	}

	m, _ = update(m, keyMsg("tab"))
	if m.input.Focused() {
		t.Error("Zen 列表上没有输入，输入框该失焦")
	}

	// 回首页要重新聚焦。这就是用户报的那条路：F2 → Tab → Esc。
	m, _ = update(m, keyMsg("esc"))
	if !m.input.Focused() {
		t.Error("从列表回首页后输入框必须重新聚焦")
	}

	// 出 Zen 回到 Normal 列表，输入框也该是失焦的。
	m, _ = update(m, keyMsg("f2"))
	if m.input.Focused() {
		t.Error("退回 Normal 列表后输入框不该还聚焦着")
	}
}

// 首页能真的把字打进去 —— 上面那条只验焦点，这条验端到端。
func TestZen_HomeAcceptsTyping(t *testing.T) {
	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = update(m, keyMsg("f2"))

	// 逐字敲进去，走真实的 textinput 更新路径。
	for _, r := range "你好" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.input.Value(); got != "你好" {
		t.Errorf("首页输入框收到的是 %q，want %q —— 焦点没设对", got, "你好")
	}
}

// 字标各行的宽度必须和 lipgloss 那把尺子一致。
//
// 字标里全是 █ ╗ ╔ ╚ ═ ║ 这类 East Asian Ambiguous 字符。本仓库的
// textWidth 把它们钉成 1 列（cellWidth 的注释解释了为什么），而居中用的
// padRight 走 lipgloss.Width。两把尺子只要有一把把它们算成 2 列，
// 补白就会多算，整个字标会歪向一边 —— 而且歪得很微妙，容易被当成「设计」。
func TestZenLogo_WidthMatchesLipgloss(t *testing.T) {
	for i, l := range zenLogoArt {
		if got, want := textWidth(l), lipgloss.Width(l); got != want {
			t.Errorf("字标第 %d 行：textWidth = %d，lipgloss.Width = %d",
				i, got, want)
		}
	}
	if w := zenLogoWidth(); w <= 0 {
		t.Errorf("字标整块宽度算出来是 %d", w)
	}
}

// 字标第 0 行和第 5 行开头的空格不能丢。
//
// ANSI Shadow 的阴影相对字身右下偏移一格，所以 'C' 的顶横 `██████╗` 和底横
// `╚═════╝` 都比它的竖笔 `██║` 右移一列 —— figlet 官方输出里这两行就以一个
// 空格开头，其余四行从第 0 列起。丢掉这个空格，'C' 的上下两横会比竖笔左移
// 一格，整个字像被斜切了一刀。
//
// 这条抓的是**复制粘贴吃字符**：从终端里拷字标时行首那个空格很容易被顺手
// trim 掉，而丢掉之后画面只是「有点歪」，不像坏了 —— 肉眼扫过去会当成设计。
// 下面两组数来自 figlet 的 ansi_shadow 字体生成 "CLICHAT" 的官方输出
// （去掉行尾空格）。
func TestZenLogo_LeadingSpacesMatchFiglet(t *testing.T) {
	wantIndent := []int{1, 0, 0, 0, 0, 1}
	wantWidth := []int{52, 52, 49, 49, 49, 49}

	if len(zenLogoArt) != len(wantIndent) {
		t.Fatalf("字标 %d 行，want %d 行", len(zenLogoArt), len(wantIndent))
	}
	for i, l := range zenLogoArt {
		if got := len(l) - len(strings.TrimLeft(l, " ")); got != wantIndent[i] {
			t.Errorf("第 %d 行前导空格 %d 个，want %d —— 行首那个空格是字的一部分，不是缩进",
				i, got, wantIndent[i])
		}
		if got := textWidth(strings.TrimRight(l, " ")); got != wantWidth[i] {
			t.Errorf("第 %d 行去掉行尾空格后 %d 列，want %d —— 字形和 figlet 官方输出对不上",
				i, got, wantWidth[i])
		}
	}
}

// 首页那行提示说的是首页能做的事，不是会话屏的。
func TestZen_HintIsScreenSpecific(t *testing.T) {
	home := zenHintFor(zenHome)
	list := zenHintFor(zenList)
	chat := zenHintFor(zenChat)

	if home == list || list == chat || home == chat {
		t.Error("三屏的提示不该是同一句 —— 每屏能做的事不一样")
	}
	if !strings.Contains(home, "Tab") {
		t.Errorf("首页的提示该提到 Tab（去列表），got %q", home)
	}
}

// ---- 分组规则（纯函数） ----

func TestZenStartsGroup(t *testing.T) {
	base := time.Date(2026, 10, 2, 15, 0, 0, 0, time.Local)

	cases := []struct {
		name string
		prev zenItem
		cur  zenItem
		want bool
	}{
		{
			"同一人、一分钟内：并成一组",
			zenItem{Sender: "Alice", Time: base},
			zenItem{Sender: "Alice", Time: base.Add(time.Minute)},
			false,
		},
		{
			"换了人：分组",
			zenItem{Sender: "Alice", Time: base},
			zenItem{Sender: "Bob", Time: base.Add(time.Minute)},
			true,
		},
		{
			"同一个人但隔了 20 分钟：分组（时间戳不能撒谎）",
			zenItem{Sender: "Alice", Time: base},
			zenItem{Sender: "Alice", Time: base.Add(20 * time.Minute)},
			true,
		},
		{
			"刚好卡在窗口上：不分组",
			zenItem{Sender: "Alice", Time: base},
			zenItem{Sender: "Alice", Time: base.Add(zenGroupWindow)},
			false,
		},
		{
			"方向变了（对方 → 我）：分组",
			zenItem{Sender: "Alice", Time: base},
			zenItem{Sender: "我", Mine: true, Time: base.Add(time.Minute)},
			true,
		},
		{
			"时间倒流（乱序到达）：不分组，且不能算成隔了很久",
			zenItem{Sender: "Alice", Time: base},
			zenItem{Sender: "Alice", Time: base.Add(-time.Minute)},
			false,
		},
	}

	for _, c := range cases {
		if got := zenStartsGroup(c.prev, c.cur); got != c.want {
			t.Errorf("%s: zenStartsGroup = %v, want %v", c.name, got, c.want)
		}
	}
}

// ---- 尺寸 ----

func TestZenContentWidth(t *testing.T) {
	cases := []struct {
		width int
		want  int
		why   string
	}{
		{160, 78, "超宽终端封顶在 78，正文绝不拉满"},
		{120, 78, "同上"},
		{100, 78, "100-16=84，仍然封顶"},
		{94, 78, "94-16=78，正好到顶"},
		{80, 64, "中间档：两边各留 8 列"},
		{72, 56, "72-16=56，正好到下限"},
		{70, 56, "低于下限时抬到 56"},
		{60, 56, "同上"},
		{58, 56, "同上，且不超屏"},
		{40, 38, "窄终端兜底：不能为了凑 56 反而溢出"},
	}

	for _, c := range cases {
		got := zenContentWidth(c.width)
		if got != c.want {
			t.Errorf("zenContentWidth(%d) = %d, want %d（%s）", c.width, got, c.want, c.why)
		}
		if got > c.width {
			t.Errorf("zenContentWidth(%d) = %d —— 正文列比终端还宽，会横向溢出",
				c.width, got)
		}
	}
}

// 窄终端上自己的消息取消右对齐 —— 正文列本来就窄，再砍一半就没法读了。
func TestZen_RightAlignFallsBackOnNarrow(t *testing.T) {
	m := zenChatModel(t)

	if !m.zenRightAlign() {
		t.Error("120 列的终端上自己的消息该靠右")
	}

	m, _ = update(m, tea.WindowSizeMsg{Width: 72, Height: 26})
	if m.zenRightAlign() {
		t.Error("72 列的终端上该退回左对齐（单列）")
	}
}

// ---- 视觉约束 ----

// Zen 的核心承诺：去掉侧栏和状态栏。
func TestZen_HasNoSidebarAndNoStatusBar(t *testing.T) {
	m := zenChatModel(t)

	view := m.View()

	for _, banned := range []struct{ s, why string }{
		{"个未读", "会话列表的标题还在 —— 侧栏没删掉"},
		{"上次同步", "状态栏还在 —— Zen 不该常驻这些"},
		{"Ctrl+R", "常驻快捷键提示还在"},
		{"（Tab：", "输入框旁的快捷键提示还在"},
	} {
		if strings.Contains(view, banned.s) {
			t.Errorf("Zen 视图里出现了 %q（%s）", banned.s, banned.why)
		}
	}

	// 分隔线只有一条（输入框上方那条），不该有侧栏那根竖线。
	if n := strings.Count(view, "│"); n > 0 {
		t.Errorf("Zen 视图里有 %d 个竖线分隔符，侧栏没删干净", n)
	}
}

// 正文列居中，且分隔线铺满正文列宽。
func TestZen_ContentColumnIsCentred(t *testing.T) {
	m := zenChatModel(t)

	contentW := zenContentWidth(m.width)
	wantLeft := zenLeftPad(m.width, contentW)

	var ruleLine string
	for _, ln := range strings.Split(m.View(), "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed != "" && strings.Trim(trimmed, "─") == "" {
			ruleLine = ln
		}
	}
	if ruleLine == "" {
		t.Fatal("找不到输入框上方那条分隔线")
	}

	// 分隔线应当正好是 contentW 列，且左边留白 wantLeft 格。
	if got := textWidth(strings.TrimSpace(ruleLine)); got != contentW {
		t.Errorf("分隔线宽 %d，want %d", got, contentW)
	}
	left := textWidth(ruleLine) - textWidth(strings.TrimLeft(ruleLine, " "))
	if left != wantLeft {
		t.Errorf("正文列左留白 %d，want %d（宽 %d，正文列 %d）",
			left, wantLeft, m.width, contentW)
	}
}

// 任何一行都不能超过终端宽度 —— 超了就会横向溢出、把画面撑坏。
func TestZen_LinesNeverExceedTerminalWidth(t *testing.T) {
	for _, size := range []struct{ w, h int }{
		{160, 45}, {120, 35}, {100, 30}, {80, 24}, {72, 24}, {60, 20}, {40, 16},
	} {
		m := newSampleModel(t)
		m, _ = update(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m, _ = update(m, keyMsg("enter"))
		m = loadBodies(t, m)
		m, _ = update(m, keyMsg("f2"))

		lines := strings.Split(m.View(), "\n")
		if len(lines) != size.h {
			t.Errorf("%dx%d: 渲染出 %d 行，want %d", size.w, size.h, len(lines), size.h)
		}
		for i, ln := range lines {
			if got := lipgloss.Width(ln); got > size.w {
				t.Errorf("%dx%d: 第 %d 行宽 %d，超出终端", size.w, size.h, i, got)
			}
		}
	}
}

// ---- 分组在渲染结果里的样子 ----

func TestZen_GroupHeaderAppearsOncePerBurst(t *testing.T) {
	m := zenChatModel(t)
	m, _ = update(m, keyMsg("f2"))

	// 样例数据里 Alice 连发两条（p12 与 p13，间隔 6 分钟，同方向），
	// 应当只出现一个组头。
	body := strings.Join(m.renderZenBody(m.mustThread(t)), "\n")
	lines := strings.Split(body, "\n")

	var aliceHeads int
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(stripANSI(ln)), "Alice · ") {
			aliceHeads++
		}
	}
	if aliceHeads == 0 {
		t.Fatal("正文里一个 Alice 组头都没有 —— 分组或渲染坏了")
	}

	// 组头的数量必须**少于** Alice 发的消息数，否则等于没分组。
	var aliceMsgs int
	for _, msg := range m.mustThread(t).Messages {
		if msg.FromName == "Alice" {
			aliceMsgs++
		}
	}
	if aliceHeads >= aliceMsgs {
		t.Errorf("Alice 发了 %d 条，却出现了 %d 个组头 —— 没分组成",
			aliceMsgs, aliceHeads)
	}
}

// ---- 滚动 ----

// 两种布局的可见行数不同，滚动位置必须按当前布局收敛。
// 不收敛的话，从 Normal 切到 Zen 会看到一片空白。
func TestZen_ToggleClampsScroll(t *testing.T) {
	m := zenChatModel(t)

	// 先在 Normal 里卷到最上面。
	for i := 0; i < 50; i++ {
		m.scrollChat(chatLineStep)
	}
	if m.scroll == 0 {
		t.Fatal("前提不成立：Normal 下应该能卷起来")
	}

	m, _ = update(m, keyMsg("f2"))
	if m.scroll > m.maxChatScroll() {
		t.Errorf("切到 Zen 后 scroll = %d 超过上限 %d —— 会看到空白",
			m.scroll, m.maxChatScroll())
	}
}

func TestZen_ScrollClampsAtBothEnds(t *testing.T) {
	m := zenChatModel(t)
	m, _ = update(m, keyMsg("f2"))

	for i := 0; i < 200; i++ {
		m.scrollChat(chatLineStep)
	}
	if got, max := m.scroll, m.maxChatScroll(); got != max {
		t.Errorf("往上卷到头：scroll = %d，want %d", got, max)
	}

	for i := 0; i < 400; i++ {
		m.scrollChat(-chatLineStep)
	}
	if m.scroll != 0 {
		t.Errorf("往下卷到底：scroll = %d，want 0", m.scroll)
	}
}

// ---- Normal 不能有回归 ----

// 进出一次 Zen 之后，Normal 的渲染必须逐字节回到原样。
func TestZen_NormalUnchangedAfterToggle(t *testing.T) {
	m := zenReadyModel(t)
	before := m.View()

	m, _ = update(m, keyMsg("f2"))
	m, _ = update(m, keyMsg("f2"))
	after := m.View()

	if before != after {
		t.Errorf("进出 Zen 之后 Normal 视图变了。\n--- 之前 ---\n%s\n--- 之后 ---\n%s",
			before, after)
	}
}

// Zen 下输入框的提示符和占位文字都换掉，切回来要还原。
//
// 提示符是给「这是 Zen」的视觉信号；占位文字短一截是因为 Zen 的输入区
// 是写作的地方，不是一张告诉你怎么用的表单。
func TestZen_InputPromptAndPlaceholderSwapAndRestore(t *testing.T) {
	m := zenReadyModel(t)

	normalPrompt, normalPlaceholder := m.input.Prompt, m.input.Placeholder

	m, _ = update(m, keyMsg("f2"))
	if m.input.Prompt == normalPrompt {
		t.Errorf("进 Zen 之后提示符没换：%q", m.input.Prompt)
	}
	if m.input.Prompt != zenInputPrompt {
		t.Errorf("Zen 提示符 = %q，want %q", m.input.Prompt, zenInputPrompt)
	}
	if m.input.Placeholder != zenInputPlaceholder {
		t.Errorf("Zen 占位文字 = %q，want %q", m.input.Placeholder, zenInputPlaceholder)
	}
	if textWidth(m.input.Placeholder) >= textWidth(normalPlaceholder) {
		t.Errorf("Zen 的占位文字不该比 Normal 长：%q vs %q",
			m.input.Placeholder, normalPlaceholder)
	}

	m, _ = update(m, keyMsg("f2"))
	if m.input.Prompt != normalPrompt {
		t.Errorf("切回 Normal 后提示符没还原：%q，want %q", m.input.Prompt, normalPrompt)
	}
	if m.input.Placeholder != normalPlaceholder {
		t.Errorf("切回 Normal 后占位文字没还原：%q，want %q",
			m.input.Placeholder, normalPlaceholder)
	}
}

// 帮助页必须列出 F2 —— 否则这个功能只活在代码里。
func TestZen_HelpMentionsF2(t *testing.T) {
	var found bool
	for _, sec := range helpSections {
		for _, it := range sec.items {
			if it.label == "F2" {
				found = true
			}
		}
	}
	if !found {
		t.Error("帮助页里没有 F2 —— 用户不会知道有 Zen")
	}
}

// 刚进 Zen 时那一行「怎么换会话」的提示：进的时候有，按第一个键就没了。
//
// 这条是补一个**实测出来的可发现性问题**：Zen 为了安静把常驻提示全删了，
// 结果连「怎么换会话」都没人知道 —— NoWint 直接来问了一遍。所以留一行，
// 但只留到第一次按键为止，不该变成又一个常驻 footer。
func TestZen_HintShowsOnEntryThenDisappears(t *testing.T) {
	m := zenHomeModel(t)

	if !m.zenShowHint {
		t.Fatal("刚进 Zen 应该亮一下提示")
	}
	if !strings.Contains(m.View(), "会话列表") {
		t.Errorf("首页的提示里没有「会话列表」:\n%s", m.View())
	}

	// 按任意一个别的键，提示就该收掉。
	m, _ = update(m, keyMsg("pgup"))
	if m.zenShowHint {
		t.Error("按过键之后提示该收掉")
	}
	if strings.Contains(m.View(), "会话列表") {
		t.Errorf("提示没从视图里消失:\n%s", m.View())
	}

	// 出去再进来，应该重新提示一次。
	m, _ = update(m, keyMsg("f2")) // 首页按 F2 = 离开 Zen
	m, _ = update(m, keyMsg("f2")) // 再进
	if !m.zenShowHint {
		t.Error("重新进 Zen 应该再提示一次")
	}
}

// 真实状态优先于提示：一进 Zen 就掉线时，看到的应该是 offline。
//
// 反过来的话（提示压过 offline），用户在最需要知道「网络断了」的时候
// 看到的却是一句快捷键说明。
func TestZen_StatusBeatsHint(t *testing.T) {
	m := zenChatModel(t)
	m, _ = update(m, keyMsg("f2"))
	m.connected = false

	got := m.renderZenStatus(zenContentWidth(m.width))
	if !strings.Contains(got, "offline") {
		t.Errorf("掉线时该显示 offline，got %q", got)
	}
	if strings.Contains(got, "切会话") {
		t.Errorf("提示不该压过真实状态，got %q", got)
	}
}

// ---- 测试辅助 ----

func (m Model) mustThread(t *testing.T) thread.Thread {
	t.Helper()
	th, ok := m.activeThread()
	if !ok {
		t.Fatal("没有打开的会话")
	}
	return th
}

// stripANSI 去掉 SGR 序列，方便按纯文本断言。
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && !((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
