// Package tui 是 clichat 的终端界面。
//
// 它只负责两件事：把状态画出来，把用户操作转成对 app 层的调用。
// 不直接碰 IMAP / SMTP / 索引 —— 那些都在 app 层。
package tui

import (
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// mode 是界面当前所处的状态。
type mode int

const (
	// modeUnlock 向用户索要主密码。
	modeUnlock mode = iota
	// modeSetup 首次运行的配置向导。
	modeSetup
	// modeList 会话列表。
	modeList
	// modeChat 单个会话的聊天流。
	modeChat
	// modeNewChat 输入新会话的收件人。
	modeNewChat
)

// minPollInterval 是轮询间隔的下限。
//
// 配置里写个 1 秒既没有意义，又容易把服务商惹毛。
const minPollInterval = 10 * time.Second

// Model 是 Bubble Tea 的界面状态。
type Model struct {
	cfg *config.Config
	app *app.App

	width  int
	height int
	ready  bool

	mode mode

	threads []thread.Thread
	cursor  int
	scroll  int

	activeRoot string
	bodies     map[string]string
	replyAll   bool

	// lastSentText 暂存发送失败的那条消息，供重发用。
	lastSentText string
	// newChatTo 是新会话的收件人 —— 在会话真正建立起来之前用它。
	newChatTo []string

	input textinput.Model
	setup setupState

	status    string
	lastSync  time.Time
	busy      bool
	connected bool
}

// ---- Bubble Tea 消息 ----

type syncResultMsg struct {
	changed bool
	err     error
}

type sendResultMsg struct {
	// root 是这条消息所属会话的根 ID。新会话发完之后靠它把视图切过去。
	root string
	err  error
}

type bodiesResultMsg struct {
	root   string
	bodies map[string]string
	err    error
}

// refreshMsg 让界面重新从 app 拉一次会话列表。
type refreshMsg struct{}

// pollTickMsg 是轮询定时器到点的信号。
type pollTickMsg time.Time

// New 构造界面模型。
//
// 配置完整且凭据存在时进入解锁；否则进入配置向导。
func New(cfg *config.Config) Model {
	in := textinput.New()
	in.Prompt = "> "
	in.CharLimit = 0

	m := Model{
		cfg:      cfg,
		input:    in,
		bodies:   map[string]string{},
		replyAll: true,
	}

	if cfg.Configured() && config.HasCredentials() {
		m.mode = modeUnlock
		m.input.Placeholder = "主密码"
		m.input.EchoMode = textinput.EchoPassword
		m.input.Focus()
	} else {
		m.beginSetup()
	}
	return m
}

// NewWithApp 用一个已经装配好的 app 启动界面，跳过解锁与配置向导。
// --mock 模式用它。
func NewWithApp(cfg *config.Config, a *app.App) Model {
	in := textinput.New()
	in.Prompt = "> "
	in.CharLimit = 0

	m := Model{
		cfg:      cfg,
		app:      a,
		input:    in,
		bodies:   map[string]string{},
		replyAll: true,
		mode:     modeList,
		status:   "正在同步…",
		busy:     true,
	}
	m.threads = a.Threads()
	return m
}

// Init 启动时先同步一次，并把轮询定时器挂上。
func (m Model) Init() tea.Cmd {
	if m.mode != modeList && m.mode != modeChat {
		return textinput.Blink
	}
	return tea.Batch(m.syncCmd(), m.pollCmd(), textinput.Blink)
}

// ---- 命令 ----

func (m Model) syncCmd() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		changed, err := a.Sync()
		return syncResultMsg{changed: changed, err: err}
	}
}

func (m Model) pollCmd() tea.Cmd {
	d := m.cfg.PollInterval()
	if d < minPollInterval {
		d = minPollInterval
	}
	return tea.Tick(d, func(t time.Time) tea.Msg { return pollTickMsg(t) })
}

func (m Model) loadBodiesCmd(th thread.Thread) tea.Cmd {
	a := m.app
	root := th.Root
	return func() tea.Msg {
		bodies, err := a.Bodies(th)
		return bodiesResultMsg{root: root, bodies: bodies, err: err}
	}
}

// markReadCmd 在后台把会话标为已读，完成后刷新列表。
func (m Model) markReadCmd(th thread.Thread) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		_ = a.MarkRead(th) // 标记失败不值得打断用户，下次轮询会再试
		return refreshMsg{}
	}
}

func (m Model) replyCmd(text string) tea.Cmd {
	a := m.app
	root := m.activeRoot
	replyAll := m.replyAll
	return func() tea.Msg {
		th, ok := a.Thread(root)
		if !ok {
			return sendResultMsg{err: errors.New("会话已经不存在了")}
		}
		return sendResultMsg{root: root, err: a.Reply(th, text, replyAll)}
	}
}

func (m Model) startCmd(to []string, text string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		root, err := a.Start(to, text)
		return sendResultMsg{root: root, err: err}
	}
}

// ---- Update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		return m, nil

	case pollTickMsg:
		if m.app == nil {
			return m, m.pollCmd()
		}
		return m, tea.Batch(m.syncCmd(), m.pollCmd())

	case syncResultMsg:
		m.busy = false
		m.lastSync = time.Now()
		m.applySyncResult(msg)
		return m, nil

	case sendResultMsg:
		m.busy = false
		if msg.err != nil {
			// 发送失败绝不假装成功：把原文还给用户，让他能重发。
			m.status = "发送失败：" + shortErr(msg.err)
			m.input.SetValue(m.lastSentText)
			return m, nil
		}
		m.status = ""
		m.lastSentText = ""
		m.newChatTo = nil
		if m.app != nil {
			m.threads = m.app.Threads()
		}
		// 新会话在发出去之前不存在于列表里，发完才出现 —— 把视图切过去。
		if msg.root != "" {
			m.activeRoot = msg.root
		}
		return m, tea.Batch(m.syncCmd(), m.loadActiveBodiesCmd())

	case bodiesResultMsg:
		if msg.root != m.activeRoot {
			return m, nil // 用户已经切走了，这份结果作废
		}
		m.bodies = msg.bodies
		if msg.err != nil {
			m.status = "部分正文加载失败：" + shortErr(msg.err)
		}
		return m, nil

	case refreshMsg:
		if m.app != nil {
			m.threads = m.app.Threads()
			m.clampCursor()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// applySyncResult 把一次同步的结果落到界面状态上。
func (m *Model) applySyncResult(msg syncResultMsg) {
	if msg.err != nil {
		m.connected = false
		switch {
		case errors.Is(msg.err, mail.ErrAuth):
			// 认证失败不静默重试 —— 反复用错凭据重连会把账号锁掉。
			m.status = "认证失败，请检查授权码是否还有效"
		case errors.Is(msg.err, mail.ErrNotConfigured):
			m.status = "账号未配置完整"
		default:
			m.status = "离线：" + shortErr(msg.err)
		}
		return
	}

	m.connected = true
	m.status = ""
	if msg.changed {
		m.threads = m.app.Threads()
		m.clampCursor()
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeUnlock:
		return m.handleUnlockKey(msg)
	case modeSetup:
		return m.handleSetupKey(msg)
	case modeList:
		return m.handleListKey(msg)
	case modeChat:
		return m.handleChatKey(msg)
	case modeNewChat:
		return m.handleNewChatKey(msg)
	}
	return m, nil
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.threads)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		if len(m.threads) > 0 {
			m.cursor = len(m.threads) - 1
		}
	case "enter":
		return m.openActive()
	case "n":
		m.mode = modeNewChat
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "收件人邮箱地址"
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "r":
		if m.app != nil {
			m.busy = true
			return m, m.syncCmd()
		}
	}
	return m, nil
}

func (m Model) openActive() (tea.Model, tea.Cmd) {
	if len(m.threads) == 0 {
		return m, nil
	}
	th := m.threads[m.cursor]

	m.activeRoot = th.Root
	m.mode = modeChat
	m.scroll = 0
	m.bodies = map[string]string{}
	m.input.EchoMode = textinput.EchoNormal
	m.input.Placeholder = "输入消息，回车发送"
	m.input.SetValue("")
	m.input.Focus()

	return m, tea.Batch(
		textinput.Blink,
		m.loadBodiesCmd(th),
		m.markReadCmd(th),
	)
}

func (m Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeList
		m.activeRoot = ""
		m.bodies = map[string]string{}
		m.input.Blur()
		return m, nil
	case "tab":
		m.replyAll = !m.replyAll
		return m, nil
	case "pgup":
		m.scroll += 5
	case "pgdown":
		if m.scroll > 0 {
			m.scroll -= 5
		}
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.SetValue("")
		m.lastSentText = text
		m.busy = true
		// activeRoot 为空说明这是刚新建、还没落地的会话。
		if m.activeRoot == "" {
			return m, m.startCmd(m.newChatTo, text)
		}
		return m, m.replyCmd(text)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleNewChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeList
		m.input.SetValue("")
		m.input.Blur()
		return m, nil
	case "enter":
		addr := strings.TrimSpace(m.input.Value())
		if addr == "" {
			return m, nil
		}
		if !mail.LooksLikeAddress(addr) {
			m.status = "地址格式不对：" + addr
			return m, nil
		}
		m.mode = modeChat
		m.activeRoot = "" // 还没建立会话
		m.bodies = map[string]string{}
		m.input.Placeholder = "输入消息，回车发送"
		m.input.SetValue("")
		m.newChatTo = []string{addr}
		m.lastSentText = ""
		return m, textinput.Blink
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) loadActiveBodiesCmd() tea.Cmd {
	if m.app == nil || m.activeRoot == "" {
		return nil
	}
	th, ok := m.app.Thread(m.activeRoot)
	if !ok {
		return nil
	}
	return m.loadBodiesCmd(th)
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.threads) {
		m.cursor = len(m.threads) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// activeThread 返回当前打开的会话。
func (m Model) activeThread() (thread.Thread, bool) {
	if m.app == nil || m.activeRoot == "" {
		return thread.Thread{}, false
	}
	return m.app.Thread(m.activeRoot)
}

// ---- 连接 ----

// connect 用配置和凭据建立客户端，成功后切到会话列表。
func (m *Model) connect(creds config.Credentials) error {
	client, err := mail.NewClient(m.cfg, creds)
	if err != nil {
		return err
	}
	return m.attach(client)
}

// attach 把客户端和索引装配成 app，并切换界面状态。
func (m *Model) attach(client mail.Client) error {
	idxPath, err := config.IndexPath()
	if err != nil {
		return err
	}
	idx, err := store.Open(idxPath)
	if err != nil {
		return err
	}

	m.app = app.New(m.cfg, client, idx)
	m.threads = m.app.Threads()
	m.mode = modeList
	m.status = "正在同步…"
	m.busy = true
	m.connected = false
	m.input.Blur()
	return nil
}

// Close 收尾：断开连接并落盘索引。
func (m Model) Close() error {
	if m.app == nil {
		return nil
	}
	return m.app.Close()
}
