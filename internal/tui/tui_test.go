package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
)

// update 跑一次 Update 并把结果转回 Model。
func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// keyMsg 把按键名转成 tea.KeyMsg。
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// newMockModel 造一个跑在内存假数据上的模型。
//
// 刻意不走 New() —— 它会去读真实的配置目录，测试不该碰用户的家目录。
func newMockModel(t *testing.T) (Model, *mail.Fake) {
	t.Helper()

	cfg := config.Default()
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0

	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "会议", Date: time.Now().Add(-time.Hour),
	}, "第一句")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<b@x>", References: []string{"<a@x>"},
		From: "me@example.com", To: []string{"alice@example.com"},
		Subject: "Re: 会议", Date: time.Now(),
	}, "第二句")

	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	m := NewWithApp(cfg, app.New(cfg, fake, idx))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, fake
}

// syncOnce 直接调 app.Sync 并把结果喂给模型，绕开 tea.Cmd 的异步性。
// 不这么做的话测试就得等真实定时器。
func syncOnce(t *testing.T, m Model) Model {
	t.Helper()
	changed, err := m.app.Sync()
	m, _ = update(m, syncResultMsg{changed: changed, err: err})
	return m
}

// loadBodies 直接调 app.Bodies 并把结果喂给模型。
func loadBodies(t *testing.T, m Model) Model {
	t.Helper()
	th, ok := m.app.Thread(m.activeRoot)
	if !ok {
		return m
	}
	bodies, err := m.app.Bodies(th)
	m, _ = update(m, bodiesResultMsg{root: th.Root, bodies: bodies, err: err})
	return m
}

func TestModel_ListsThreads(t *testing.T) {
	m, _ := newMockModel(t)
	m = syncOnce(t, m)

	if got := len(m.threads); got != 1 {
		t.Fatalf("want 1 thread, got %d", got)
	}
	view := m.View()
	if !strings.Contains(view, "alice") {
		t.Errorf("会话列表里没看到对方名字:\n%s", view)
	}
	if !strings.Contains(view, "会议") {
		t.Errorf("会话列表里没看到主题:\n%s", view)
	}
}

func TestModel_OpenThreadShowsMessages(t *testing.T) {
	m, _ := newMockModel(t)
	m = syncOnce(t, m)

	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeChat {
		t.Fatalf("mode = %v, want modeChat", m.mode)
	}

	m = loadBodies(t, m)

	view := m.View()
	for _, want := range []string{"第一句", "第二句", "我"} {
		if !strings.Contains(view, want) {
			t.Errorf("聊天流里缺 %q:\n%s", want, view)
		}
	}
}

// 发出去的消息必须立刻可见，不能等下一轮轮询。
func TestModel_SendShowsImmediately(t *testing.T) {
	m, _ := newMockModel(t)
	m = syncOnce(t, m)
	m, _ = update(m, keyMsg("enter"))
	m = loadBodies(t, m)

	m.input.SetValue("我同意")
	m, cmd := update(m, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("回车应该产生发送命令")
	}

	// 同步执行发送命令，把结果喂回去。
	m, _ = update(m, cmd())
	m = loadBodies(t, m)

	if !strings.Contains(m.View(), "我同意") {
		t.Errorf("发出的消息没有立刻出现在聊天流里:\n%s", m.View())
	}
}

// 发送失败必须把原文还给用户，并且明确报错。
func TestModel_SendFailureKeepsText(t *testing.T) {
	m, fake := newMockModel(t)
	fake.SetSendError(errors.New("smtp 拒绝"))
	m = syncOnce(t, m)
	m, _ = update(m, keyMsg("enter"))

	m.input.SetValue("会失败的这条")
	m, cmd := update(m, keyMsg("enter"))
	m, _ = update(m, cmd())

	if got := m.input.Value(); got != "会失败的这条" {
		t.Errorf("发送失败后输入框应保留原文, got %q", got)
	}
	if !strings.Contains(m.status, "发送失败") {
		t.Errorf("状态栏没有报告失败: %q", m.status)
	}
}

func TestModel_TabTogglesReplyAll(t *testing.T) {
	m, _ := newMockModel(t)
	m = syncOnce(t, m)
	m, _ = update(m, keyMsg("enter"))

	if !m.replyAll {
		t.Fatal("默认应该是 Reply-All")
	}
	m, _ = update(m, keyMsg("tab"))
	if m.replyAll {
		t.Error("Tab 之后应切到只回发件人")
	}
	m, _ = update(m, keyMsg("tab"))
	if !m.replyAll {
		t.Error("再按一次应切回 Reply-All")
	}
}

func TestModel_EscReturnsToList(t *testing.T) {
	m, _ := newMockModel(t)
	m = syncOnce(t, m)
	m, _ = update(m, keyMsg("enter"))
	m, _ = update(m, keyMsg("esc"))

	if m.mode != modeList {
		t.Fatalf("Esc 之后 mode = %v, want modeList", m.mode)
	}
	if m.activeRoot != "" {
		t.Error("Esc 之后应清空当前会话")
	}
}

// 新建会话的地址格式不对时要拦下来，而不是发出去。
func TestModel_NewChatRejectsBadAddress(t *testing.T) {
	m, _ := newMockModel(t)
	m = syncOnce(t, m)

	m, _ = update(m, keyMsg("n"))
	if m.mode != modeNewChat {
		t.Fatalf("mode = %v, want modeNewChat", m.mode)
	}

	m.input.SetValue("这不是邮箱")
	m, _ = update(m, keyMsg("enter"))

	if m.mode != modeNewChat {
		t.Error("地址不合法时不该进入聊天界面")
	}
	if !strings.Contains(m.status, "地址格式不对") {
		t.Errorf("没有提示地址错误: %q", m.status)
	}
}

// 任何一行都不能超出终端宽度，否则两栏的右边框会错位。
func TestModel_RenderedLinesFitWidth(t *testing.T) {
	for _, width := range []int{60, 80, 100, 140} {
		m, _ := newMockModel(t)
		m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: 24})
		m = syncOnce(t, m)
		m, _ = update(m, keyMsg("enter"))
		m = loadBodies(t, m)

		for i, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("宽度 %d：第 %d 行实际宽 %d，超出: %q", width, i, w, line)
			}
		}
	}
}

// 窄终端要降级成单栏，不能硬挤两栏。
func TestModel_NarrowTerminalUsesSinglePane(t *testing.T) {
	m, _ := newMockModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 50, Height: 20})
	m = syncOnce(t, m)

	if m.width >= singlePaneWidth {
		t.Fatal("测试前提不成立")
	}
	if strings.Contains(m.View(), "│") {
		t.Error("窄终端下不该出现两栏分隔符")
	}
}

func TestModel_SetupWizardStartsWithProviders(t *testing.T) {
	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]string{},
	}
	m.beginSetup()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	if m.mode != modeSetup || m.setup.step != stepProvider {
		t.Fatalf("新配置应进入服务商选择, mode=%v step=%v", m.mode, m.setup.step)
	}

	view := m.View()
	if !strings.Contains(view, "QQ 邮箱") {
		t.Errorf("服务商列表没渲染出来:\n%s", view)
	}
	// 引导文案是「免配置」承诺的核心，必须出现在界面上。
	if !strings.Contains(view, "授权码") {
		t.Errorf("没有显示授权码引导:\n%s", view)
	}

	// 选中 QQ 之后应进入邮箱地址这一步，并带上正确的服务器地址。
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepEmail {
		t.Fatalf("step = %v, want stepEmail", m.setup.step)
	}
	if m.cfg.IMAP.Host != "imap.qq.com" || m.cfg.SMTP.Host != "smtp.qq.com" {
		t.Errorf("预设没写进配置: IMAP=%q SMTP=%q", m.cfg.IMAP.Host, m.cfg.SMTP.Host)
	}
}

func TestModel_UnlockRejectsShortMasterPassword(t *testing.T) {
	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]string{},
	}
	m.beginSetup()
	m.setup.step = stepMaster
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m.input.SetValue("ab")
	m, _ = update(m, keyMsg("enter"))

	if m.setup.err == "" {
		t.Error("过短的主密码应该被拒绝")
	}
	if m.mode != modeSetup {
		t.Error("被拒绝后应留在向导里")
	}
}

func TestTruncateAndWrapUseDisplayWidth(t *testing.T) {
	// 中文一个字占两列，按 rune 数截断会把行撑宽。
	if got := truncate("中文字符串", 5); lipgloss.Width(got) > 5 {
		t.Errorf("truncate 结果超宽: %q (宽 %d)", got, lipgloss.Width(got))
	}
	if got := truncate("abcdefgh", 5); got != "abcd…" {
		t.Errorf("truncate = %q, want %q", got, "abcd…")
	}

	for _, line := range wrapLines("中文混排 english words 一起折行", 10) {
		if w := lipgloss.Width(line); w > 10 {
			t.Errorf("wrapLines 产出超宽行 %q (宽 %d)", line, w)
		}
	}
}
