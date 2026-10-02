package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// zenModel 造一个已经打开会话的模型 —— Zen 只在会话里有意义。
func zenModel(t *testing.T) Model {
	t.Helper()

	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m, _ = update(m, keyMsg("enter")) // 光标默认在最新那条（产品评审）
	return loadBodies(t, m)
}

// ---- 状态建模 ----

func TestZen_F2TogglesLayout(t *testing.T) {
	m := zenModel(t)

	if m.layout != layoutNormal {
		t.Fatalf("初始 layout = %v，want layoutNormal", m.layout)
	}

	m, _ = update(m, keyMsg("f2"))
	if m.layout != layoutZen {
		t.Fatalf("按 F2 之后 layout = %v，want layoutZen", m.layout)
	}
	if !m.zenActive() {
		t.Error("在会话里 + layoutZen 就该是 zenActive")
	}

	m, _ = update(m, keyMsg("f2"))
	if m.layout != layoutNormal {
		t.Fatalf("再按一次 F2 应回 layoutNormal，got %v", m.layout)
	}
}

// Zen 是**布局**维度，不是新的 mode —— 切进去之后 mode 必须还是 modeChat。
// 这条守着「不要把 Zen 做成 modeZen」那个决定。
func TestZen_DoesNotChangeMode(t *testing.T) {
	m := zenModel(t)

	before := m.mode
	m, _ = update(m, keyMsg("f2"))

	if m.mode != before {
		t.Errorf("切 Zen 动了 mode：%v -> %v。layout 和 mode 是两个维度，不该互相污染",
			before, m.mode)
	}
	if m.mode != modeChat {
		t.Errorf("mode = %v，want modeChat", m.mode)
	}
}

// Esc 一次只退一层：Zen → 普通会话，再按一次才回列表。
func TestZen_EscExitsZenFirst(t *testing.T) {
	m := zenModel(t)
	m, _ = update(m, keyMsg("f2"))

	m, _ = update(m, keyMsg("esc"))
	if m.layout != layoutNormal {
		t.Fatalf("Zen 里按 Esc 应退回 layoutNormal，got %v", m.layout)
	}
	if m.mode != modeChat {
		t.Fatalf("第一次 Esc 不该离开会话，mode = %v", m.mode)
	}

	m, _ = update(m, keyMsg("esc"))
	if m.mode != modeList {
		t.Fatalf("第二次 Esc 应回到列表，mode = %v", m.mode)
	}
}

// 列表 / 搜索 / 帮助这些界面没有 Zen 形态。
//
// 注意 Esc 的语义：它在 Zen 里是「退出 Zen」，一次只退一层。所以按一次
// Esc 之后 layout 已经还原了 —— 这条同时守着「Esc 不该一步退到列表」。
func TestZen_EscExitsZenNotSession(t *testing.T) {
	m := zenModel(t)
	m, _ = update(m, keyMsg("f2"))
	if !m.zenActive() {
		t.Fatal("前提不成立：会话里应该是 zenActive")
	}

	m, _ = update(m, keyMsg("esc"))
	if m.zenActive() {
		t.Error("Esc 之后不该还是 zenActive")
	}
	if m.layout != layoutNormal {
		t.Errorf("Esc 应把 layout 还原成 normal，got %v", m.layout)
	}
	if m.mode != modeChat {
		t.Errorf("Esc 一次不该离开会话，mode = %v", m.mode)
	}
}

// layout 和 mode 是两个独立的维度。
//
// Ctrl+U 会离开会话但**不动 layout** —— 正好用来验这一点：layout 还挂在
// Zen 上，但列表界面必须画成 Normal 的样子。
func TestZen_LayoutFlagDoesNotLeakIntoListMode(t *testing.T) {
	m := zenModel(t)
	m, _ = update(m, keyMsg("f2"))

	m, _ = update(m, keyMsg("ctrl+u"))
	if m.mode != modeList {
		t.Fatalf("Ctrl+U 之后应在列表，mode = %v", m.mode)
	}
	if m.layout != layoutZen {
		t.Errorf("Ctrl+U 不该动 layout，got %v", m.layout)
	}
	if m.zenActive() {
		t.Error("列表界面不该是 zenActive —— 这正是把两个维度拆开的意义")
	}

	// 画出来的必须是列表：有侧栏标题，没有输入区那条分隔线。
	view := m.View()
	if !strings.Contains(view, "个未读") {
		t.Error("列表界面没画出来")
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
	m := zenModel(t)

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
	m := zenModel(t)
	m, _ = update(m, keyMsg("f2"))

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
	m := zenModel(t)
	m, _ = update(m, keyMsg("f2"))

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
	m := zenModel(t)
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
	m := zenModel(t)

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
	m := zenModel(t)
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
	m := zenModel(t)
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
	m := zenModel(t)

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
