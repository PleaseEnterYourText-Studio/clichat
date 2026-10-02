// Package tui 是 clichat 的终端界面。
//
// 它只负责两件事：把状态画出来，把用户操作转成对 app 层的调用。
// 不直接碰 IMAP / SMTP / 索引 —— 那些都在 app 层。
package tui

import (
	"errors"
	"strings"
	"time"

	"github.com/atotto/clipboard"
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
	// modeHelp 是全屏帮助页。
	modeHelp
	// modeSearch 是在列表上做实时过滤，输入框装着搜索词。
	modeSearch
	// modeFolder 是文件夹选择器。
	modeFolder
	// modeForward 输入转发的收件人。
	modeForward
	// modeConfirm 是删除这类操作前的确认。
	modeConfirm
)

// confirmKind 是待确认的操作类型。
//
// 用枚举而不是存一个闭包：闭包没法比较、没法在测试里断言，
// 而这里要确认的动作一共就两个，枚举更清楚。
type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmDelete
	// confirmAllMail 是「接收全部邮件」开关。它也要确认，因为打开之后
	// 首次同步可能要把服务器上几万封邮件的头部拉一遍。
	confirmAllMail
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

	// threads 是 app 给的全部会话；visible 是当前实际显示的
	// （按 activeFolder 和 query 过滤之后）。光标索引的是 visible。
	//
	// 两个都留着而不是只存过滤结果：过滤条件一变就得重算，
	// 而重算需要原始列表。
	threads []thread.Thread
	visible []thread.Thread
	cursor  int
	scroll  int

	// query 是列表的搜索词，空串表示不搜索。
	query string

	// folders 是服务端上真实存在的文件夹名，供切换器用。nil 表示还没拉过。
	folders      []string
	folderCursor int
	// activeFolder 为空串表示不按文件夹过滤。
	activeFolder string

	// helpScroll 是帮助页的滚动位置（从顶部算起的行数）。
	helpScroll int
	// helpReturn 记录帮助页是从哪个界面打开的，关掉时退回去。
	helpReturn mode

	// confirmKind 描述待确认的操作；pendingRoot 是它作用的会话根 ID。
	// 转发也复用它来记住目标会话。
	confirmKind confirmKind
	pendingRoot string

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

// actionResultMsg 是标记 / 删除 / 转发 / 复制这类操作的统一回执。
//
// 合成一个消息类型而不是每样一个：这些操作的后续处理完全一样
// （出错就报错、成功就刷新列表），拆成四种只是四份复制粘贴。
type actionResultMsg struct {
	// what 是动作名，用来拼错误文案，例如「删除失败：…」。
	what string
	err  error
	// done 是成功时要显示的一句话；空串表示不提示。
	done string
}

// foldersResultMsg 是文件夹列表的拉取结果。
type foldersResultMsg struct {
	folders []string
	err     error
}

// allMailResultMsg 是「接收全部邮件」开关切换完成的回执。
//
// 它没有塞进 actionResultMsg：这个开关成功之后还要接着触发一轮同步，
// 而 actionResultMsg 的语义是「到此为止」。
type allMailResultMsg struct {
	on  bool
	err error
}

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
//
// 只给测试用 —— 让界面层的测试能直接跑在内存假数据上，不碰真实邮箱。
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
	m.setThreads(a.Threads())
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

// ---- 标记 / 删除 / 转发 / 复制 ----

func (m Model) unreadCmd(th thread.Thread) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		return actionResultMsg{what: "标记未读", err: a.MarkUnread(th)}
	}
}

func (m Model) starCmd(th thread.Thread, on bool) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		return actionResultMsg{what: "星标", err: a.SetStarred(th, on)}
	}
}

func (m Model) deleteCmd(th thread.Thread) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		return actionResultMsg{what: "删除", err: a.Delete(th)}
	}
}

func (m Model) forwardCmd(th thread.Thread, to []string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		_, err := a.Forward(th, to)
		done := ""
		if err == nil {
			done = "已转发给 " + strings.Join(to, ", ")
		}
		return actionResultMsg{what: "转发", err: err, done: done}
	}
}

// copyLastBodyCmd 拉取会话最后一条的正文并写进系统剪贴板。
//
// 放到 tea.Cmd 里而不是直接在按键处理里做：列表模式下正文大概率还没
// 加载过，这一步要走网络，不能让界面卡住。
func (m Model) copyLastBodyCmd(th thread.Thread) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		text, err := a.LastBodyText(th)
		if err != nil {
			return actionResultMsg{what: "复制", err: err}
		}
		if strings.TrimSpace(text) == "" {
			return actionResultMsg{what: "复制", err: errors.New("这个会话没有可复制的正文")}
		}
		if err := clipboard.WriteAll(text); err != nil {
			return actionResultMsg{what: "复制", err: err}
		}
		return actionResultMsg{what: "复制", done: "已复制最后一条正文"}
	}
}

func (m Model) foldersCmd() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		f, err := a.Folders()
		return foldersResultMsg{folders: f, err: err}
	}
}

// allMailCmd 切换「接收全部邮件」模式（同时落盘）。
func (m Model) allMailCmd(on bool) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		return allMailResultMsg{on: on, err: a.SetAllMail(on)}
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
			m.setThreads(m.app.Threads())
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
			m.setThreads(m.app.Threads())
		}
		return m, nil

	case actionResultMsg:
		m.busy = false
		if msg.err != nil {
			m.status = msg.what + "失败：" + shortErr(msg.err)
		} else {
			m.status = msg.done
		}
		if m.app != nil {
			m.setThreads(m.app.Threads())
		}
		// 会话被删掉之后要退回列表，否则会停在一个已经不存在的会话上，
		// 标题空着、输入框还能打字，但发不出去。
		if m.activeRoot != "" {
			if _, ok := m.app.Thread(m.activeRoot); !ok {
				m.mode = modeList
				m.activeRoot = ""
				m.bodies = map[string]string{}
				m.input.Blur()
			}
		}
		return m, nil

	case foldersResultMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "取文件夹列表失败：" + shortErr(msg.err)
			return m, nil
		}
		m.folders = msg.folders
		// 打开选择器时把光标停在当前选中的那一项上，而不是从头开始。
		m.folderCursor = 0
		for i, name := range m.folderChoices() {
			if name == m.activeFolder {
				m.folderCursor = i
				break
			}
		}
		m.mode = modeFolder
		return m, nil

	case allMailResultMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "切换「接收全部邮件」失败：" + shortErr(msg.err)
			return m, nil
		}
		if !msg.on {
			m.status = "已关闭「接收全部邮件」"
			return m, nil
		}
		// 打开之后立刻同步一轮。这个模式的全部意义就是把历史拉下来，
		// 让用户自己再按一次 r 只会让人怀疑开关是不是没生效。
		m.status = "已打开「接收全部邮件」，正在拉取历史邮件…"
		m.busy = true
		return m, m.syncCmd()

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
		m.setThreads(m.app.Threads())
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// F1 在哪儿都能开帮助。会话内输入框有焦点，? 会被当成正文打进去，
	// 那一边只能靠 F1。
	if msg.String() == "f1" {
		return m.openHelp()
	}

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
	case modeHelp:
		return m.handleHelpKey(msg)
	case modeSearch:
		return m.handleSearchKey(msg)
	case modeFolder:
		return m.handleFolderKey(msg)
	case modeForward:
		return m.handleForwardKey(msg)
	case modeConfirm:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

// openHelp 打开帮助页，并记住是从哪儿来的。
func (m Model) openHelp() (tea.Model, tea.Cmd) {
	m.helpReturn = m.mode
	m.mode = modeHelp
	m.helpScroll = 0
	m.input.Blur()
	return m, nil
}

// closeHelp 关掉帮助页，退回打开它的那个界面。
func (m Model) closeHelp() (tea.Model, tea.Cmd) {
	m.mode = m.helpReturn
	m.helpScroll = 0
	// 退回到有输入框的界面时要把焦点还回去，否则光标不闪、打不了字。
	switch m.mode {
	case modeChat, modeNewChat, modeSearch, modeForward:
		m.input.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

// maxHelpScroll 返回帮助页能滚到的最大行偏移。
func (m Model) maxHelpScroll() int {
	bodyH := m.height - 1
	if bodyH < 1 {
		bodyH = 1
	}
	max := len(helpLines(m.width)) - bodyH
	if max < 0 {
		return 0
	}
	return max
}

func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "?", "esc", "q":
		return m.closeHelp()
	case "up", "k":
		m.helpScroll--
	case "down", "j":
		m.helpScroll++
	case "pgup":
		m.helpScroll -= 10
	case "pgdown":
		m.helpScroll += 10
	case "home", "g":
		m.helpScroll = 0
	case "end", "G":
		m.helpScroll = m.maxHelpScroll()
	}

	if m.helpScroll > m.maxHelpScroll() {
		m.helpScroll = m.maxHelpScroll()
	}
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}
	return m, nil
}

// handleSearchKey 处理搜索输入。过滤是实时的 —— 每敲一个字就重算。
func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		// Esc 清空搜索词回列表，和大多数客户端的习惯一致。
		m.query = ""
		m.refreshVisible()
		m.cursor = 0
		m.mode = modeList
		m.input.SetValue("")
		m.input.Blur()
		return m, nil
	case "enter":
		// 回车保留过滤结果，回到列表继续用方向键挑。
		m.mode = modeList
		m.input.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.query = m.input.Value()
	m.refreshVisible()
	m.clampCursor()
	return m, cmd
}

// handleFolderKey 处理文件夹选择器。方向键选，回车确定。
func (m Model) handleFolderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := m.folderChoices()

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "tab":
		m.mode = modeList
		return m, nil
	case "up", "k":
		if m.folderCursor > 0 {
			m.folderCursor--
		}
	case "down", "j":
		if m.folderCursor < len(choices)-1 {
			m.folderCursor++
		}
	case "home", "g":
		m.folderCursor = 0
	case "end", "G":
		m.folderCursor = len(choices) - 1
	case "enter":
		if m.folderCursor >= 0 && m.folderCursor < len(choices) {
			m.activeFolder = choices[m.folderCursor]
		}
		m.refreshVisible()
		m.cursor = 0
		m.mode = modeList
		return m, nil
	}
	return m, nil
}

// handleForwardKey 处理转发收件人的输入。
func (m Model) handleForwardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		th, ok := m.pendingThread()
		if !ok {
			m.status = "要转发的会话已经不在了"
			m.mode = modeList
			return m, nil
		}
		m.mode = modeList
		m.input.SetValue("")
		m.input.Blur()
		m.busy = true
		return m, m.forwardCmd(th, []string{addr})
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleConfirmKey 处理确认框。只有明确按 y / 回车才执行。
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "enter":
		kind := m.confirmKind

		// 「接收全部邮件」不作用在某个会话上，先分出去 —— 下面那段要拿
		// pendingThread，对它不适用（硬套的话会报「会话已经不在」）。
		if kind == confirmAllMail {
			m.confirmKind = confirmNone
			m.mode = modeList
			m.input.Blur()
			if m.app == nil {
				return m, nil
			}
			m.busy = true
			return m, m.allMailCmd(!m.app.AllMail())
		}

		// 顺序要紧：pendingThread 读的是 pendingRoot，必须趁清空之前问。
		th, ok := m.pendingThread()
		m.confirmKind = confirmNone
		m.pendingRoot = ""
		m.mode = modeList
		m.input.Blur()
		if !ok {
			m.status = "这个会话已经不在了"
			return m, nil
		}
		if kind == confirmDelete {
			m.busy = true
			return m, m.deleteCmd(th)
		}
		return m, nil
	case "n", "esc", "q":
		m.confirmKind = confirmNone
		m.pendingRoot = ""
		m.mode = modeList
		return m, nil
	}
	return m, nil
}

// pendingThread 返回模态操作（删除确认 / 转发）作用在哪个会话上。
func (m Model) pendingThread() (thread.Thread, bool) {
	if m.app == nil || m.pendingRoot == "" {
		return thread.Thread{}, false
	}
	return m.app.Thread(m.pendingRoot)
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
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		if len(m.visible) > 0 {
			m.cursor = len(m.visible) - 1
		}

	// 未读跳转。从光标旁边一格起步（当前这条已经看过了），走到头就
	// 绕回另一端 —— 否则在末尾按 ] 会「没反应」，用户会以为键坏了。
	case "]", "ctrl+n":
		if i := firstUnread(m.visible, m.cursor+1, 1); i >= 0 {
			m.cursor = i
		} else if i := firstUnread(m.visible, 0, 1); i >= 0 {
			m.cursor = i
		}
	case "[", "ctrl+p":
		if i := firstUnread(m.visible, m.cursor-1, -1); i >= 0 {
			m.cursor = i
		} else if i := firstUnread(m.visible, len(m.visible)-1, -1); i >= 0 {
			m.cursor = i
		}

	case "enter":
		return m.openActive(false)
	case "R":
		return m.openActive(true)

	case "n":
		m.mode = modeNewChat
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "收件人邮箱地址"
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink

	case "/":
		m.mode = modeSearch
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "搜索：参与者 / 主题 / 显示名"
		m.input.SetValue(m.query)
		m.input.CursorEnd()
		m.input.Focus()
		return m, textinput.Blink

	case "tab":
		if m.app == nil {
			return m, nil
		}
		// 文件夹列表要问服务端，所以先转个圈再开选择器。
		m.busy = true
		return m, m.foldersCmd()

	case "r":
		if m.app != nil {
			m.busy = true
			return m, m.syncCmd()
		}

	case "?":
		return m.openHelp()

	case "a":
		// 「接收全部邮件」开关。要确认 —— 打开之后首次同步可能要把
		// 服务器上几万封邮件的头部拉一遍，那不是按错键该承担的代价。
		if m.app != nil {
			m.confirmKind = confirmAllMail
			m.mode = modeConfirm
			return m, nil
		}

	case "u":
		if th, ok := m.currentThread(); ok {
			m.busy = true
			return m, m.unreadCmd(th)
		}

	case "*":
		if th, ok := m.currentThread(); ok {
			m.busy = true
			return m, m.starCmd(th, !th.IsStarred())
		}

	case "y":
		if th, ok := m.currentThread(); ok {
			m.busy = true
			return m, m.copyLastBodyCmd(th)
		}

	case "d":
		if th, ok := m.currentThread(); ok {
			m.pendingRoot = th.Root
			m.confirmKind = confirmDelete
			m.mode = modeConfirm
			return m, nil
		}

	case "f":
		if th, ok := m.currentThread(); ok {
			m.pendingRoot = th.Root
			m.mode = modeForward
			m.input.EchoMode = textinput.EchoNormal
			m.input.Placeholder = "转发给（收件人邮箱地址）"
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		}
	}
	return m, nil
}

// openActive 打开光标所在的会话。
//
// solo 为 true 时把回复对象锁定成「只回发件人」—— 从列表按 R 进来的
// 快捷路径，省掉进去之后再按一次 Tab。默认一律重置成 Reply-All，
// 免得上一轮切过的状态悄悄带到下一个会话里。
func (m Model) openActive(solo bool) (tea.Model, tea.Cmd) {
	th, ok := m.currentThread()
	if !ok {
		return m, nil
	}

	m.activeRoot = th.Root
	m.mode = modeChat
	m.scroll = 0
	m.bodies = map[string]string{}
	m.replyAll = !solo
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

	// 下面这几个都用 Ctrl 组合键：会话里输入框有焦点，单个字母会被
	// 当成正文打进消息里。这是"聊天界面"和"列表界面"按键设计上最
	// 本质的差别。
	case "ctrl+y":
		if th, ok := m.activeThread(); ok {
			m.busy = true
			return m, m.copyLastBodyCmd(th)
		}
	case "ctrl+t":
		if th, ok := m.activeThread(); ok {
			m.busy = true
			return m, m.starCmd(th, !th.IsStarred())
		}
	case "ctrl+u":
		// 标记未读之后必须离开这个会话 —— 留在里面的话它马上又会被
		// 标回已读（打开会话时会 MarkRead），用户会以为按键没生效。
		if th, ok := m.activeThread(); ok {
			m.busy = true
			m.mode = modeList
			m.activeRoot = ""
			m.bodies = map[string]string{}
			m.input.Blur()
			return m, m.unreadCmd(th)
		}
	case "ctrl+d":
		if th, ok := m.activeThread(); ok {
			m.pendingRoot = th.Root
			m.confirmKind = confirmDelete
			m.mode = modeConfirm
			m.input.Blur()
			return m, nil
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

// setThreads 换掉会话列表，并重算过滤结果与光标位置。
//
// 列表的每一处赋值都必须走它 —— 直接写 m.threads 会让 visible 变成
// 陈旧数据，界面显示的是上一轮的会话，点进去却对不上号。
func (m *Model) setThreads(list []thread.Thread) {
	m.threads = list
	m.refreshVisible()
	m.clampCursor()
}

// refreshVisible 按当前文件夹与搜索词重算要显示的会话。
func (m *Model) refreshVisible() {
	m.visible = filterThreads(m.threads, m.activeFolder, m.query)
}

// currentThread 返回光标所在的会话。
func (m Model) currentThread() (thread.Thread, bool) {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return thread.Thread{}, false
	}
	return m.visible[m.cursor], true
}

// folderChoices 返回文件夹选择器的选项，第一项固定是「全部」。
//
// 用空串表示「全部」，界面上渲染成「（全部文件夹）」。这样选项值可以
// 直接赋给 activeFolder，不需要额外的哨兵常量，也不会和真实文件夹名撞车
// —— 真实文件夹名不会是空串。
func (m Model) folderChoices() []string {
	out := make([]string, 0, len(m.folders)+1)
	out = append(out, "")
	return append(out, m.folders...)
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
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
	m.setThreads(m.app.Threads())
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
