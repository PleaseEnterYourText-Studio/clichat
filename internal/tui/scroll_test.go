package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// 这个文件守着「会话视图怎么滚、焦点怎么走」。
//
// 起因是用户的一句「左右两个区域，chatView 完全无法滚动啊，你的聚焦做得
// 不好」。查下来结论分两半：
//
//   - 滚动的代码路径是通的，但它**从来没被测试碰过** —— 见下面
//     TestKeyMsg_ProducesRealKeys 记的那个坑：夹具造出来的 "pgup" 根本不是
//     PgUp 键，于是「测过」是假的。
//   - scroll 从不收敛：按住 PgUp 会无限累加，窗口一变就把正文顶出画面。
//
// 所以判据分三层：夹具自检（按键得是真的）、滚动数值的收敛、以及「两栏
// 各自响应谁」。参照的成熟方案是 Discordo（Go 写的 Discord 客户端）：
// 滚轮和翻页滚消息、组合键切频道、**Esc 从不关闭频道**。

// openSampleChat 造一个「打开着长会话」的模型，后续判据都从这儿起步。
//
// 用 newSampleModel（截图那套夹具）而不是 newMockModel：后者每个会话只有
// 一条消息，一屏根本放不满，tailWindow 会原样返回，什么都测不出来。
//
// 这里会 Fatal 拦住「正文一屏就放得下」的情况，理由和用户那条规矩一致 ——
// 判据不许在「量不了」的时候静悄悄变成空转，那正是假绿的藏身处。
func openSampleChat(t *testing.T) Model {
	t.Helper()

	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = update(m, keyMsg("enter"))
	m = loadBodies(t, m)

	if m.mode != modeChat {
		t.Fatalf("没能进入会话：mode = %v", m.mode)
	}
	if m.maxChatScroll() == 0 {
		t.Fatalf("夹具坏了：正文 %d 行、一屏能放 %d 行，滚无可滚，"+
			"这个文件里的判据全都测不到东西",
			m.chatBodyLines(), chatViewHeight(m.bodyHeight()))
	}
	return m
}

// ---- 夹具自检 ----

// keyMsg 必须造出**真的按键**，而不是碰巧同名的字符串。
//
// 这条是血泪教训。keyMsg("pgup") 以前没有对应的 case，落进了最后的兜底
// 分支，造出来的是 KeyRunes{[]rune("pgup")} —— 它的 String() 恰好也是
// "pgup"，于是一条测「PgUp 能滚动」的判据看着是绿的，可那个按键在真实
// 终端里从来没被按过（真 KeyPgUp 走的是 \x1b[5~）。造不出真按键的夹具
// 比没有测试更危险：它给的是假的安心。
//
// 判断「是不是兜底分支」的尺子不是 String()（那正是骗人的东西），
// 而是 Type。
func TestKeyMsg_ProducesRealKeys(t *testing.T) {
	cases := []struct {
		in  string
		typ tea.KeyType
	}{
		{"enter", tea.KeyEnter},
		{"esc", tea.KeyEsc},
		{"tab", tea.KeyTab},
		{"up", tea.KeyUp},
		{"down", tea.KeyDown},
		{"home", tea.KeyHome},
		{"end", tea.KeyEnd},
		{"pgup", tea.KeyPgUp},
		{"pgdown", tea.KeyPgDown},
		{"ctrl+up", tea.KeyCtrlUp},
		{"ctrl+down", tea.KeyCtrlDown},
		{"ctrl+r", tea.KeyCtrlR},
		{"ctrl+u", tea.KeyCtrlU},
		{"f1", tea.KeyF1},
		{"f2", tea.KeyF2},
	}
	for _, c := range cases {
		got := keyMsg(c.in)
		if got.Type != c.typ {
			t.Errorf("keyMsg(%q).Type = %v，want %v", c.in, got.Type, c.typ)
		}
		if got.Type == tea.KeyRunes {
			t.Errorf("keyMsg(%q) 落进了 KeyRunes 兜底分支 —— 那模拟的是"+
				"「用户打了这几个字」，不是按键", c.in)
		}
	}
}

// ---- 滚动与收敛 ----

// PgUp 往上卷、PgDn 往下卷，两个方向都在边界收住。
func TestChat_PgUpDownScrollsAndClampsAtBounds(t *testing.T) {
	m := openSampleChat(t)

	page := m.chatPageStep()
	maxScroll := m.maxChatScroll()

	if m.scroll != 0 {
		t.Fatalf("刚打开会话时 scroll = %d，want 0（应该停在最新一条上）", m.scroll)
	}

	m, _ = update(m, keyMsg("pgup"))
	if m.scroll != page {
		t.Errorf("按一次 PgUp：scroll = %d，want %d", m.scroll, page)
	}

	// 按住不放：必须停在 maxScroll 上，不能无限累加。
	// 这正是以前那个 bug 的形状 —— 视图早就到顶了，scroll 还在涨，
	// 涨出来的余数会在换窗口/换会话时把正文顶出画面。
	for i := 0; i < 20; i++ {
		m, _ = update(m, keyMsg("pgup"))
	}
	if m.scroll != maxScroll {
		t.Errorf("连按 20 次 PgUp：scroll = %d，want %d（到顶就该收住）",
			m.scroll, maxScroll)
	}

	// 往回滚，下界同样收住。
	for i := 0; i < 20; i++ {
		m, _ = update(m, keyMsg("pgdown"))
	}
	if m.scroll != 0 {
		t.Errorf("连按 20 次 PgDn：scroll = %d，want 0（到底就该收住）", m.scroll)
	}
}

// 卷到顶时，视口最上面那行必须正好是正文的第一行。
//
// 这条抓的是「两把尺子」：算「最多能卷多少」（maxChatScroll）和取窗口
// （tailWindow）各用各的算法，很容易差一行 —— 表现是「滚到底还差半行」
// 或者最早那条永远看不见，两种都很难查。让它们对不上就红。
func TestChat_ScrollToTopShowsFirstLine(t *testing.T) {
	m := openSampleChat(t)

	th, _ := m.activeThread()
	all := m.renderChatBody(th, m.chatBodyWidth())
	viewH := chatViewHeight(m.bodyHeight())

	m.scroll = m.maxChatScroll()
	got := tailWindow(all, viewH, m.scroll)

	if len(got) == 0 {
		t.Fatal("卷到顶竟然是空的")
	}
	if got[0] != all[0] {
		t.Errorf("卷到顶时视口第一行不是正文第一行：\n  got: %q\n want: %q", got[0], all[0])
	}
	if last := got[len(got)-1]; last != all[viewH-1] {
		t.Errorf("卷到顶时视口最后一行不对：\n  got: %q\n want: %q", last, all[viewH-1])
	}
}

// 窗口尺寸一变，滚动位置必须重新落回合法范围。
//
// 这不是理论问题：能卷多少行取决于视口高度，视口一变，原来合法的 scroll
// 就可能越界。越界时 tailWindow 会把画面兜住（不会白屏），但 m.scroll 那个
// 余数会留着 —— 用户接着往下按，前几下全是「没反应」。
//
// 直接构造越界状态，而不是绕弯子用真实 resize 去凑：越界的**机制**和怎么
// 撞上它无关，而构造出来的那个状态是确定的、可复现的。
func TestChat_WindowResizeClampsScroll(t *testing.T) {
	m := openSampleChat(t)

	// 「窗口刚变大」的形状：同样的正文，视口更高 → 能卷的更少。
	m.scroll = m.maxChatScroll() + 500
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	if max := m.maxChatScroll(); m.scroll > max {
		t.Errorf("窗口变大之后 scroll = %d 仍然超过上限 %d", m.scroll, max)
	}

	// 反向：窗口高到一屏放得下，scroll 必须归零。
	// 留着残值的话，它会跟着用户走到下一个会话去。
	m.scroll = 7
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 200})
	if m.scroll != 0 {
		t.Errorf("窗口高到一屏放得下时 scroll = %d，want 0", m.scroll)
	}
}

// 正文重载（加载完、或者同步来了新邮件）之后也要收敛 ——
// 短正文配大 scroll 同样会把画面顶走。
func TestChat_NewBodyClampsScroll(t *testing.T) {
	m := openSampleChat(t)

	// 先造出越界，再走一遍 bodiesResultMsg 那条路。
	m.scroll = m.maxChatScroll() + 500
	m, _ = update(m, bodiesResultMsg{id: m.activeID, bodies: m.bodies})

	if max := m.maxChatScroll(); m.scroll > max {
		t.Errorf("正文重载之后 scroll = %d 超过上限 %d", m.scroll, max)
	}
}

// ---- 切换会话 ----

// Ctrl+↑/↓ 在会话之间跳，并且直接把新会话打开。
func TestChat_CtrlArrowSwitchesThread(t *testing.T) {
	m := openSampleChat(t)

	if len(m.visible) < 2 {
		t.Fatalf("夹具坏了：visible 只有 %d 个会话，没法测切换", len(m.visible))
	}
	first := m.activeID
	if m.cursor != 0 {
		t.Fatalf("夹具的起点变了：cursor = %d，want 0", m.cursor)
	}

	m, _ = update(m, keyMsg("ctrl+down"))
	if m.cursor != 1 {
		t.Errorf("Ctrl+↓ 之后 cursor = %d，want 1", m.cursor)
	}
	if m.activeID != m.visible[1].ID {
		t.Errorf("Ctrl+↓ 之后 activeID = %q，want %q", m.activeID, m.visible[1].ID)
	}
	if m.mode != modeChat {
		t.Errorf("切换之后应该还在会话视图里，mode = %v", m.mode)
	}
	if m.activeID == first {
		t.Error("Ctrl+↓ 没有真的换会话")
	}
	if m.scroll != 0 {
		t.Errorf("换会话应该回到最新一条：scroll = %d，want 0", m.scroll)
	}

	m, _ = update(m, keyMsg("ctrl+up"))
	if m.activeID != first || m.cursor != 0 {
		t.Errorf("Ctrl+↑ 没回到上一个：cursor = %d，activeID = %q", m.cursor, first)
	}

	// 到头停住，不绕回另一头。
	m, _ = update(m, keyMsg("ctrl+up"))
	if m.cursor != 0 || m.activeID != first {
		t.Errorf("在第一个会话上按 Ctrl+↑ 应该停住：cursor = %d，activeID = %q",
			m.cursor, m.activeID)
	}

	// 一路 Ctrl+↓ 走到末尾 —— 走真实路径，不手设 cursor。
	last := len(m.visible) - 1
	for i := 0; i < len(m.visible)+2; i++ {
		m, _ = update(m, keyMsg("ctrl+down"))
	}
	if m.cursor != last {
		t.Errorf("一路 Ctrl+↓ 应该停在最后一个：cursor = %d，want %d", m.cursor, last)
	}
	// 再按也不动，而且不能绕回第一个。
	m, _ = update(m, keyMsg("ctrl+down"))
	if m.cursor != last || m.activeID != m.visible[last].ID {
		t.Errorf("在最后一个会话上按 Ctrl+↓ 应该停住：cursor = %d，activeID = %q",
			m.cursor, m.activeID)
	}
}

// 换会话时草稿要留着；从列表重新打开才清掉。
//
// 两个意图不同：Ctrl+↑/↓ 是「我先去别的会话看一眼」，打了的字还在；
// 从列表回车是「开一个会话」，草稿该清。混成一个的话，前者会把用户打
// 了一半的字悄悄吃掉 —— 那是最招人烦的一类 bug。
func TestChat_SwitchKeepsDraftButReopenClearsIt(t *testing.T) {
	m := openSampleChat(t)

	const draft = "打了一半的字"
	m.input.SetValue(draft)

	m, _ = update(m, keyMsg("ctrl+down"))
	if got := m.input.Value(); got != draft {
		t.Errorf("切会话之后草稿 = %q，want %q", got, draft)
	}

	// 退回列表再回车，那是「重新打开」，草稿清空。
	m, _ = update(m, keyMsg("esc"))
	m, _ = update(m, keyMsg("enter"))
	if got := m.input.Value(); got != "" {
		t.Errorf("从列表重新打开会话时草稿 = %q，want 空", got)
	}
}

// Ctrl+U 才是「离开并丢掉会话」，和 Esc 分开。
//
// 两个键必须给不同的语义：Esc 是「退出去看看」（保留），Ctrl+U 是
// 「把这条放回未读」（必须离开，否则打开着就会立刻被标回已读）。
func TestChat_CtrlULeavesSession(t *testing.T) {
	m := openSampleChat(t)

	m, _ = update(m, keyMsg("ctrl+u"))

	if m.mode != modeList {
		t.Fatalf("Ctrl+U 之后 mode = %v，want modeList", m.mode)
	}
	if m.activeID != "" {
		t.Errorf("Ctrl+U（标记未读）必须离开会话，activeID = %q", m.activeID)
	}
}

// ---- 方向键归谁 ----

// ↑/↓ 只在输入框空着的时候滚正文，非空就还给输入框。
func TestChat_UpDownScrollsOnlyWhenDraftIsEmpty(t *testing.T) {
	m := openSampleChat(t)

	m, _ = update(m, keyMsg("up"))
	if m.scroll != chatLineStep {
		t.Fatalf("输入框空着时按 ↑：scroll = %d，want %d", m.scroll, chatLineStep)
	}

	m.input.SetValue("在打字")
	before := m.scroll
	m, _ = update(m, keyMsg("up"))
	if m.scroll != before {
		t.Errorf("输入框有内容时按 ↑ 不该滚正文：scroll %d → %d", before, m.scroll)
	}

	m.input.SetValue("")
	m, _ = update(m, keyMsg("down"))
	if want := before - chatLineStep; m.scroll != want {
		t.Errorf("输入框又空之后按 ↓：scroll = %d，want %d", m.scroll, want)
	}
}

// ---- 鼠标滚轮 ----

// 滚轮滚的是「指针底下那一栏」：左栏挪光标，右栏滚正文。
func TestChat_MouseWheelScrollsHoveredPane(t *testing.T) {
	m := openSampleChat(t)
	if m.width < singlePaneWidth {
		t.Fatalf("夹具宽度 %d 降级成单栏了，测不了分栏", m.width)
	}

	rightX := m.listWidth() + 5
	leftX := 1

	m, _ = update(m, tea.MouseMsg{
		X: rightX, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp,
	})
	if m.scroll != wheelStep {
		t.Errorf("指针在右栏、滚轮向上：scroll = %d，want %d", m.scroll, wheelStep)
	}
	if m.cursor != 0 {
		t.Errorf("指针在右栏时不该动列表光标：cursor = %d", m.cursor)
	}

	m, _ = update(m, tea.MouseMsg{
		X: leftX, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
	})
	if m.cursor != 1 {
		t.Errorf("指针在左栏、滚轮向下：cursor = %d，want 1", m.cursor)
	}
	if m.scroll != wheelStep {
		t.Errorf("指针在左栏时不该动正文：scroll = %d，want %d", m.scroll, wheelStep)
	}

	// 只认按下：松开的那一下不能也算一次滚动，否则一格滚轮滚两格。
	m, _ = update(m, tea.MouseMsg{
		X: rightX, Action: tea.MouseActionRelease, Button: tea.MouseButtonWheelUp,
	})
	if m.scroll != wheelStep {
		t.Errorf("滚轮的松开事件被当成了滚动：scroll = %d", m.scroll)
	}
}

// ---- 滚动条与宽度 ----

// 滚动条：内容放得下就不画；超出时正好一段滑块，滑块跟着 scroll 走。
func TestAddScrollBar(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("第 %d 行", i)
	}
	const w = 20

	// 一屏放得下：不画滚动条，只接一个空格。
	fits := addScrollBar(lines, len(lines), 0, w)
	if len(fits) != len(lines) {
		t.Fatalf("行数变了：%d → %d", len(lines), len(fits))
	}
	for i, l := range fits {
		if strings.ContainsAny(l, "│┃") {
			t.Errorf("内容放得下时不该有滚动条，第 %d 行：%q", i, l)
		}
		if got := lipgloss.Width(l); got != w+1 {
			t.Errorf("第 %d 行宽 %d 列，want %d", i, got, w+1)
		}
	}

	// 超出：每行都有轨道，滑块高度按可见比例给。
	const total = 40
	out := addScrollBar(lines, total, 0, w)
	thumbs := 0
	for i, l := range out {
		if got := lipgloss.Width(l); got != w+1 {
			t.Errorf("第 %d 行宽 %d 列，want %d", i, got, w+1)
		}
		if !strings.ContainsAny(l, "│┃") {
			t.Errorf("内容超出时第 %d 行应该有滚动条：%q", i, l)
		}
		if strings.Contains(l, "┃") {
			thumbs++
		}
	}
	wantThumb := len(lines) * len(lines) / total
	if wantThumb < 1 {
		wantThumb = 1
	}
	if thumbs != wantThumb {
		t.Errorf("滑块高 %d 行，want %d", thumbs, wantThumb)
	}

	// scroll=0 是「停在最底部」，滑块就该在最下面。
	if !strings.Contains(out[len(out)-1], "┃") {
		t.Errorf("scroll=0 时滑块应该在底部，实际最后一行：%q", out[len(out)-1])
	}

	// 卷到顶：滑块跑到最上面。
	top := addScrollBar(lines, total, total-len(lines), w)
	if !strings.Contains(top[0], "┃") {
		t.Errorf("卷到顶时滑块应该在顶部，实际第一行：%q", top[0])
	}
	if strings.Contains(top[len(top)-1], "┃") {
		t.Errorf("卷到顶时滑块不该还在底部：%q", top[len(top)-1])
	}
}

// renderChat 的宽度契约：传进去的是「这一栏的总宽」，出来的每行也必须
// 正好这么宽。
//
// 多一格会把右边框顶出去、少一格会让两栏之间裂条缝。更要紧的是它守着
// 「算行数用的宽度」和「画的时候用的宽度」是同一个数：chatBodyLines 走
// chatBodyWidth()，renderChat 内部是 width-1，两者对不上就会出现
// 「滚到底还差半行」。
func TestChat_RenderChatHonoursPaneWidth(t *testing.T) {
	m := openSampleChat(t)

	for _, w := range []int{m.chatPaneWidth(), 60, 40} {
		rendered := m.renderChat(w, m.bodyHeight())
		lines := strings.Split(rendered, "\n")

		if len(lines) != m.bodyHeight() {
			t.Errorf("栏宽 %d：渲染出 %d 行，want %d", w, len(lines), m.bodyHeight())
		}
		for i, l := range lines {
			if got := lipgloss.Width(l); got != w {
				t.Errorf("栏宽 %d 的第 %d 行实际 %d 列：%q", w, i, got, l)
			}
		}
	}
}

// ---- 帮助页与其余模式 ----

// 帮助页的滚动位置同样要收敛 —— 和正文那条是同一个 bug 的两个实例。
func TestHelp_ScrollClampsOnResize(t *testing.T) {
	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m, _ = update(m, keyMsg("?"))

	if m.mode != modeHelp {
		t.Fatalf("按 ? 之后 mode = %v，want modeHelp", m.mode)
	}
	if m.maxHelpScroll() <= 0 {
		t.Fatalf("夹具坏了：帮助内容一屏就放得下（maxHelpScroll = %d）", m.maxHelpScroll())
	}

	m, _ = update(m, keyMsg("end"))
	if m.helpScroll != m.maxHelpScroll() {
		t.Errorf("End 之后 helpScroll = %d，want %d", m.helpScroll, m.maxHelpScroll())
	}

	// 造出越界，再把窗口撑到一屏放得下 —— 必须归零，不能留着残值。
	m.helpScroll = m.maxHelpScroll() + 100
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 200})
	if m.helpScroll != 0 {
		t.Errorf("窗口高到帮助一屏放得下时 helpScroll = %d，want 0", m.helpScroll)
	}
}

// 滚轮在帮助页里滚的是帮助，不是底下那个列表。
func TestMouse_WheelInHelpScrollsHelp(t *testing.T) {
	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = update(m, keyMsg("?"))

	cursorBefore := m.cursor
	m, _ = update(m, tea.MouseMsg{
		X: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
	})

	if m.helpScroll != wheelStep {
		t.Errorf("帮助页里向下滚一格：helpScroll = %d，want %d", m.helpScroll, wheelStep)
	}
	if m.cursor != cursorBefore {
		t.Errorf("帮助页里的滚轮不该动列表光标：%d → %d", cursorBefore, m.cursor)
	}
}

// 弹在最上面的那些模式（搜索、确认、文件夹选择）不接管滚轮。
//
// 接管了的话，屏幕上什么都不变、底下的列表光标却动了；等用户关掉这一层，
// 会发现选中项莫名其妙挪了位置。这比「滚轮没反应」更难排查。
func TestMouse_WheelIgnoredInModalModes(t *testing.T) {
	m := newSampleModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// 搜索模式
	m, _ = update(m, keyMsg("/"))
	if m.mode != modeSearch {
		t.Fatalf("mode = %v，want modeSearch", m.mode)
	}
	cursorBefore := m.cursor
	m, _ = update(m, tea.MouseMsg{
		X: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
	})
	if m.cursor != cursorBefore {
		t.Errorf("搜索模式下滚轮动了列表光标：%d → %d", cursorBefore, m.cursor)
	}

	// 确认框（从列表按 d 触发删除确认）
	m, _ = update(m, keyMsg("esc"))
	m, _ = update(m, keyMsg("d"))
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v，want modeConfirm", m.mode)
	}
	cursorBefore = m.cursor
	m, _ = update(m, tea.MouseMsg{
		X: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp,
	})
	if m.cursor != cursorBefore {
		t.Errorf("确认框里滚轮动了列表光标：%d → %d", cursorBefore, m.cursor)
	}
}

// tailWindow 是「scroll 会不会把画面搞空」的最后一道保险：任何取值都必须
// 返回 height 行**正文**，不许 panic、不许越界、不许返回空。
//
// 上面那些判据管的是「谁负责把 scroll 收进合法范围」，这条管的是
// 「万一还是漏了一个越界值，用户看到的最坏情况是什么」。
func TestTailWindow_AlwaysReturnsBody(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("第 %d 行", i)
	}
	const h = 10

	for _, scroll := range []int{-999, -1, 0, 1, 39, 40, 41, 999} {
		got := tailWindow(lines, h, scroll)
		if len(got) != h {
			t.Errorf("scroll = %d：返回 %d 行，want %d（高度必须填满）", scroll, len(got), h)
			continue
		}
		// 越界的 scroll 被收进来之后，视口底沿最多只能顶到正文的最后一行。
		seen := map[string]bool{}
		for _, l := range lines {
			seen[l] = true
		}
		for _, l := range got {
			if !seen[l] {
				t.Errorf("scroll = %d：返回了不属于正文的行 %q", scroll, l)
			}
		}
	}

	// 正文比视口还短：原样返回，不裁剪也不补白。
	short := []string{"只有一行"}
	if got := tailWindow(short, h, 5); len(got) != 1 {
		t.Errorf("正文比视口短时应原样返回，得到 %d 行", len(got))
	}

	// 高度 0 或负数不能 panic。
	for _, hh := range []int{0, -3} {
		if got := tailWindow(lines, hh, 7); len(got) != 0 {
			t.Errorf("高度 %d 时应返回空，得到 %d 行", hh, len(got))
		}
	}
}
