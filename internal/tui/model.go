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

	// layout 是**视觉**维度，和 mode（交互状态）正交。
	//
	// 两者拆开而不是合成一个枚举的理由见 zen.go 顶部。要点：状态是
	// (mode, layout) 这样的二元组，每条转移规则只动一个轴 ——
	// Esc 动 mode，F2 动 layout。
	layout layoutMode

	// zenShowHint 控制 Zen 下那行「怎么用」的一次性提示。
	// 进 Zen 时置起，用户按第一个键就收掉（见 handleZenKey）。
	zenShowHint bool

	// zenScreen 是 Zen 空间里的哪一屏（首页 / 列表 / 会话）。
	// 只在 layout == layoutZen 时有意义。
	zenScreen zenScreen

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

	// recent 是最近打开过的会话 ID，最近的在最前。
	//
	// 顶部那几个标签页从这里来。用「打开过的」而不是「列表里最上面的」：
	// 标签页回答的是「我刚才在跟谁说话」，那是操作历史；按时间排是左边
	// 列表已经在做的事，在顶上再抄一遍等于没有信息量。
	recent []string

	// helpScroll 是帮助页的滚动位置（从顶部算起的行数）。
	helpScroll int
	// helpReturn 记录帮助页是从哪个界面打开的，关掉时退回去。
	helpReturn mode

	// confirmKind 描述待确认的操作；pendingID 是它作用的会话 ID。
	// 转发也复用它来记住目标会话。
	confirmKind confirmKind
	pendingID   string

	activeID string
	bodies   map[string]app.Body
	replyAll bool

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

	// manual 表示这次同步是用户按了刷新（Ctrl+R），不是定时轮询。
	//
	// 手动刷新时即使没有新数据也要重读一次正文：用户按了键、界面却纹丝
	// 不动，看着就是「刷新坏了」；何况上一轮的正文可能加载失败过，这里
	// 是重试的机会。轮询不能这么干 —— 每 30 秒一次全量正文往返。
	manual bool
}

type sendResultMsg struct {
	// id 是这条消息所属会话的 ID。新会话发完之后靠它把视图切过去。
	id  string
	err error
}

type bodiesResultMsg struct {
	id     string
	bodies map[string]app.Body
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
	// open 表示这次拉取是为了打开 Tab 选择器（见 foldersCmd）。
	open bool
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

// newTextInput 造一个配好样式的输入框。
//
// 存在的理由很实际：`textinput.New()` 的零值里 PlaceholderStyle 是一个
// 写死的 hex，降级到 256 色正好是 240 —— 和输入卡底色同色，那句提示会
// 整个消失。而**散在两处** new 出来的话，迟早有一处漏配（这次就是），
// 症状还极难查：代码全对，只有截图上看不见几个字。
// 收成一个构造函数，样式就只有一处可漏了。
func newTextInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "> "
	in.CharLimit = 0
	in.PlaceholderStyle = stylePlaceholder
	return in
}

// New 构造界面模型。
//
// 配置完整且凭据存在时进入解锁；否则进入配置向导。
func New(cfg *config.Config) Model {
	in := newTextInput()

	m := Model{
		cfg:      cfg,
		input:    in,
		bodies:   map[string]app.Body{},
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
	in := newTextInput()

	m := Model{
		cfg:      cfg,
		app:      a,
		input:    in,
		bodies:   map[string]app.Body{},
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
	// 文件夹列表要开机就拉：左侧导航里的「已发送 / 已删除」是**规范名**
	// （SENT / TRASH），各家服务端上真正的写法可能是 Sent、已发送邮件……
	// 列表没到手之前，那两项会被原样当成 "SENT" 去过滤，点进去永远是空的。
	// 以前只有 Tab 选择器用得到它，按一下 Tab 就有了；现在导航一开机就
	// 把那两项摆在那儿，所以得在开机时把它取回来。
	return tea.Batch(m.syncCmd(), m.foldersCmd(false), m.pollCmd(), textinput.Blink)
}

// ---- 命令 ----

func (m Model) syncCmd() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		changed, err := a.Sync()
		return syncResultMsg{changed: changed, err: err}
	}
}

// refreshCmd 是用户主动触发的同步（会话里按 Ctrl+R）。
//
// 和 syncCmd 只差一个 manual 标记 —— 有了它，即使这一轮没有新邮件，
// 当前会话的正文也会重读一遍（见 syncResultMsg.manual）。
func (m Model) refreshCmd() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		changed, err := a.Sync()
		return syncResultMsg{changed: changed, err: err, manual: true}
	}
}

func (m Model) pollCmd() tea.Cmd {
	d := m.cfg.PollInterval()
	if d < minPollInterval {
		d = minPollInterval
	}
	return tea.Tick(d, func(t time.Time) tea.Msg { return pollTickMsg(t) })
}

// loadBodiesByIDCmd 生成一个「读某个会话的正文」的命令。
//
// 会话只在**执行的那一刻**按 ID 去取，不在生成命令时取好塞进闭包 ——
// 命令是异步跑的，从生成到执行之间，会话可能已经因为一次同步而变了
// （多了新邮件）。拿着按下按键那一刻的旧快照去读，读出来的是旧正文，
// 落地后反而把刚刷新的内容覆盖掉；而且看运气：并发的「同步」和「读正文」
// 谁后完成谁赢。
func (m Model) loadBodiesByIDCmd(id string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		th, ok := a.Thread(id)
		if !ok {
			return bodiesResultMsg{id: id}
		}
		bodies, err := a.Bodies(th)
		return bodiesResultMsg{id: id, bodies: bodies, err: err}
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
	id := m.activeID
	replyAll := m.replyAll
	return func() tea.Msg {
		th, ok := a.Thread(id)
		if !ok {
			return sendResultMsg{err: errors.New("会话已经不存在了")}
		}
		return sendResultMsg{id: id, err: a.Reply(th, text, replyAll)}
	}
}

func (m Model) startCmd(to []string, text string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		id, err := a.Start(to, text)
		return sendResultMsg{id: id, err: err}
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

// foldersCmd 生成一个「问服务端要文件夹列表」的命令。
//
// open 表示这次拉取的目的是**打开选择器**（Tab）：只有这条路才需要转圈、
// 才该把失败报给用户。开机那一次是后台补齐，安静地做就行 —— 服务端不支
// 持 LIST 的时候，不值得为一件用户没要求的事弹一条错。
func (m Model) foldersCmd(open bool) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		f, err := a.Folders()
		return foldersResultMsg{folders: f, err: err, open: open}
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
		// 窗口一变，正文区的高度和宽度都变了 —— 能卷的行数跟着变。
		// 不在这里收敛的话，上一次在大窗口里卷下去的 scroll 会原样留着，
		// 缩小窗口就卷过了头，右栏直接空掉。这是「自适应窗口大小」那条
		// 需求里最容易漏的一步：光把宽高存下来不算自适应。
		//
		// 帮助页同理（帮助内容的行数只取决于宽度），两处一起收。
		m.clampChatScroll()
		m.clampHelpScroll()
		// 输入框自己也得有个宽度上限：它的 Width 为 0 时完全不裁剪。
		//
		// 两条路在这里收口，顺序不能换：
		//
		//  1. Zen 的正文列比终端窄，宽度得跟着 zenContentWidth 走（见
		//     syncInputWidth），否则从大窗口缩到小窗口之后，敲到一半的字
		//     会顶出正文列。非 Zen 时它把 Width 归零 —— 表示"不限制"。
		//  2. 归零之后再补上整页视图（解锁、配置向导）那个粗上限。这两个
		//     页面里输入框独占一整行，给个略小于终端宽度的值就够了。
		//
		// 会话里那个浮起输入框**不走这里**：它的宽度要精确到列，由
		// renderInputLine 按那一行实际剩多少现算（它才知道提示要占多宽），
		// 走的是 sizedInput 里的临时副本，不碰 m.input.Width。
		m.syncInputWidth()
		if m.input.Width == 0 {
			m.input.Width = m.pageInputWidth()
		}
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
		// 同步之后，当前会话的正文也要跟着重载（什么时候该重载见
		// reloadBodiesIfChanged 的说明）。
		//
		// 少了这一步，用户在会话里干等的时候，对方新来的那一封只会出现在
		// 左边的列表里（未读标记、时间、主题都会变），右边那一屏却停在
		// 上一轮的内容上 —— 看上去就是「刷新没用」。轮询路径原先只更新了
		// 列表，这就是本轮修的那个 bug。
		return m, m.reloadBodiesIfChanged(msg)

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
		// 自己刚发出去的那条一定是最新的，滚到底让用户看见它已经被送出去。
		// 留着上一条的 scroll 会让新消息落在视口外面，看起来像「没发出去」。
		m.scroll = 0
		if m.app != nil {
			m.setThreads(m.app.Threads())
		}
		// 新会话在发出去之前不存在于列表里，发完才出现 —— 把视图切过去。
		if msg.id != "" {
			m.activeID = msg.id
			m.noteRecent(msg.id)
		}
		return m, tea.Batch(m.syncCmd(), m.loadActiveBodiesCmd())

	case bodiesResultMsg:
		if msg.id != m.activeID {
			return m, nil // 用户已经切走了，这份结果作废
		}
		m.bodies = msg.bodies
		if msg.err != nil {
			m.status = "部分正文加载失败：" + shortErr(msg.err)
		}
		// 正文换了，能卷的行数也跟着变。上一轮如果卷得比较深、新正文又短
		// 一些，scroll 就留在了界外 —— 收敛一次，免得用户从头按到底都
		// 「没反应」（画面靠 tailWindow 兜住了，但这个余数很难察觉）。
		m.clampChatScroll()
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
		if m.activeID != "" {
			if _, ok := m.app.Thread(m.activeID); !ok {
				m.mode = modeList
				m.activeID = ""
				m.bodies = map[string]app.Body{}
				m.input.Blur()
			}
		}
		return m, nil

	case foldersResultMsg:
		if msg.err != nil {
			// 开机那次拉不到就安静地算了（导航会退回按规范名过滤，
			// 和以前一样）；按 Tab 才需要告诉用户为什么选择器没开。
			if msg.open {
				m.busy = false
				m.status = "取文件夹列表失败：" + shortErr(msg.err)
			}
			return m, nil
		}
		m.folders = msg.folders
		if !msg.open {
			return m, nil
		}
		m.busy = false
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

	case tea.MouseMsg:
		return m.handleMouse(msg)
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

	// Zen 是另一个世界：它有自己的一套屏幕和导航（首页 → 列表 → 会话），
	// 所以在这里整块分派出去，不让 mode 参与 —— mode 记的是 Normal 世界
	// 停在哪，出 Zen 时原样回去。
	if m.zenActive() {
		return m.handleZenKey(msg)
	}

	// F2 进 Zen。
	//
	// 放在这里而不是各 mode 的分支里：**Zen 的入口不该挑界面** —— 在列表上
	// 按 F2 和在会话里按 F2，用户想的都是「去那个安静的地方」。原先只在
	// handleChatKey 里接了，结果列表上按 F2 毫无反应（截图脚本一头撞上，
	// 拍出来的「首页」其实是普通两栏）。
	//
	// 搜索 / 确认框 / 文件夹选择器 / 配置向导这些**中间态**不接：它们都是
	// 「正在做一件事」，半路切走会把那件事的上下文弄丢。
	switch msg.String() {
	case "f2":
		switch m.mode {
		case modeList, modeChat, modeNewChat:
			m.toggleZen()
			return m, nil
		}
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

	m.clampHelpScroll()
	return m, nil
}

// clampHelpScroll 把帮助页的滚动位置收进 [0, maxHelpScroll]。
//
// 和会话正文的 clampChatScroll 是同一件事的两个实例：帮助内容的行数只
// 取决于宽度，所以窗口一变，「能滚多少」也跟着变，旧位置就可能越界。
// 收敛逻辑写成方法而不是散在两处，是因为它要被三个地方调用 ——
// 按键、滚轮、窗口尺寸变化。
func (m *Model) clampHelpScroll() {
	if max := m.maxHelpScroll(); m.helpScroll > max {
		m.helpScroll = max
	}
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}
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

		// 顺序要紧：pendingThread 读的是 pendingID，必须趁清空之前问。
		th, ok := m.pendingThread()
		m.confirmKind = confirmNone
		m.pendingID = ""
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
		m.pendingID = ""
		m.mode = modeList
		return m, nil
	}
	return m, nil
}

// pendingThread 返回模态操作（删除确认 / 转发）作用在哪个会话上。
func (m Model) pendingThread() (thread.Thread, bool) {
	if m.app == nil || m.pendingID == "" {
		return thread.Thread{}, false
	}
	return m.app.Thread(m.pendingID)
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k", "ctrl+up":
		m.moveCursor(-1)
	case "down", "j", "ctrl+down":
		m.moveCursor(1)
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

	// Ctrl+G 循环切换左侧导航（文件夹）。用「循环」而不是给每一项配键：
	// 见 cycleNav 的说明。
	case "ctrl+g":
		m.cycleNav()

	// Esc 一次收起一层筛选：先退掉文件夹，再退掉搜索词。两层都干净时
	// 什么也不做 —— 列表模式下没有「上一层」可退，凭空把光标挪回顶部
	// 只会让人以为按错了。
	//
	// 这两层筛选**都必须有键盘退路**：导航是可以点出来的（鼠标），
	// 搜索词是回车保留的（键盘），只进不出就成了死胡同。
	case "esc":
		switch {
		case m.activeFolder != "":
			m.setFolder("")
		case m.query != "":
			m.query = ""
			m.refreshVisible()
			m.cursor = 0
			m.clampCursor()
		}

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
		return m, m.foldersCmd(true)

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
			m.pendingID = th.ID
			m.confirmKind = confirmDelete
			m.mode = modeConfirm
			return m, nil
		}

	case "f":
		if th, ok := m.currentThread(); ok {
			m.pendingID = th.ID
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
// 快捷路径，省掉进去之后再按一次 Tab。
func (m Model) openActive(solo bool) (tea.Model, tea.Cmd) {
	th, ok := m.currentThread()
	if !ok {
		return m, nil
	}
	return m.enterThread(th, solo, false)
}

// switchThread 在会话列表里往上（dir<0）或往下（dir>0）跳一个会话，并
// 直接打开它。
//
// 光标跟着一起走，所以 Ctrl+↑/↓ 和「在列表里挪一下再回车」落到的是同一个
// 状态，不会出现「光标停在 A、打开的却是 B」这种对不上号的画面。
func (m Model) switchThread(dir int) (tea.Model, tea.Cmd) {
	if len(m.visible) == 0 {
		return m, nil
	}
	next := m.cursor + dir
	if next < 0 || next >= len(m.visible) {
		// 到头就停住，不绕回另一头。绕回会让「按住 Ctrl+↓」变成一台
		// 不会累的永动机，停住只是「没反应」—— 后者不会误伤。
		return m, nil
	}
	m.cursor = next
	return m.enterThread(m.visible[next], false, true)
}

// enterThread 把界面切到 th 这个会话：重置滚动、丢掉上一个会话的正文、
// 重新拉正文并标已读。
//
// solo 决定回复对象（见 openActive）；keepDraft 决定要不要保留输入框里
// 正在打的字。
//
// keepDraft 是给 Ctrl+↑/↓ 用的：用户打了一半才发现发错人（或者只是想去
// 别的会话看一眼），换会话不该把草稿吃掉。而从列表里回车进来是「开一个
// 新会话」的意思，草稿一律清掉 —— 两个不同的意图，所以是两个参数。
func (m Model) enterThread(th thread.Thread, solo, keepDraft bool) (tea.Model, tea.Cmd) {
	m.activeID = th.ID
	m.mode = modeChat
	// 换会话一定从底部看起。scroll 是「从这个会话底部往上卷多少行」，
	// 留着上一个会话的值，新会话一进来就是一片空画面。
	m.scroll = 0
	m.bodies = map[string]app.Body{}
	m.replyAll = !solo
	// newChatTo 是「还没落地的新会话」的收件人。切到已有会话必须清掉，
	// 否则回车会把消息发到那个还没建立起来的新会话去。
	m.newChatTo = nil
	// lastSentText 是发送失败后留给用户重发的副本。换了会话，上一轮失败
	// 的那个对象已经不是当前会话了，留着只会把消息重发到错的地方。
	m.lastSentText = ""
	m.input.EchoMode = textinput.EchoNormal
	m.input.Placeholder = chatInputPlaceholder
	if !keepDraft {
		m.input.SetValue("")
	}
	m.input.Focus()
	// 记进「最近打开」，顶部标签页按这个顺序排。
	m.noteRecent(th.ID)

	return m, tea.Batch(
		textinput.Blink,
		m.loadBodiesByIDCmd(th.ID),
		m.markReadCmd(th),
	)
}

func (m Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	// Esc 只退出「会话视图」，**不丢弃**当前会话。
	//
	// 这里原先顺手把 activeID 清空、bodies 也清掉，等于「按 Esc = 关掉
	// 这个会话」；用户的原话是「怎么 esc 直接关闭当前会话了？只有在切换
	// 会话时才更换」。Esc 的语义该是「退一层」，不是「扔掉东西」——
	// 成熟客户端也都是这么定的：Discordo 的 composer 里 esc 只是取消
	// 当前输入，Discord 里 esc 标记已读，**没有一个是「关闭频道」**。
	//
	// 保留 activeID 还有个直接好处：退回列表之后右栏仍然显示这个会话的
	// 内容（renderChat 看的是 activeThread()，与 mode 无关），用户扫一眼
	// 列表再按回车就回来了，不用重新找。
	case "esc":
		// Normal 下 Esc 只退出「会话视图」，**不丢弃**当前会话。
		//
		// 这里原先顺手把 activeID 清空、bodies 也清掉，等于「按 Esc = 关掉
		// 这个会话」；用户的原话是「怎么 esc 直接关闭当前会话了？只有在切换
		// 会话时才更换」。Esc 的语义该是「退一层」，不是「扔掉东西」——
		// 成熟客户端也都是这么定的：Discordo 的 composer 里 esc 只是取消
		// 当前输入，Discord 里 esc 标记已读，**没有一个是「关闭频道」**。
		//
		// 保留 activeID 还有个直接好处：退回列表之后右栏仍然显示这个会话的
		// 内容（renderChat 看的是 activeThread()，与 mode 无关），用户扫一眼
		// 列表再按回车就回来了，不用重新找。
		//
		// Zen 里的 Esc 不走到这里 —— 那条路由 handleZenKey 分派掉了，
		// 语义是「会话 → 列表」，同样不丢会话。
		m.mode = modeList
		m.input.Blur()
		return m, nil

	case "tab":
		m.replyAll = !m.replyAll
		return m, nil

	// 上下滚正文。
	//
	// PgUp/PgDn 一次一屏减一行：留一行重叠是标准做法，两屏完全错开的话
	// 读者得自己找「刚才读到哪了」。↑/↓ 一次三行 —— 一屏二十来行，
	// 三行一步既有明确反馈，又不至于像整屏那样容易翻过头。
	//
	// 这三个键都在 switch 里被拦下，不会落到下面的 input.Update：
	// textinput 本来就不认 PgUp/PgDn，但它认 ↑/↓（要在打的字里挪光标），
	// 所以下面那两只要先问一句「输入框是不是空的」。
	case "pgup":
		m.scrollChat(m.chatPageStep())
		return m, nil
	case "pgdown":
		m.scrollChat(-m.chatPageStep())
		return m, nil

	// 在会话之间直接跳，不用先退回列表。
	//
	// 这是「输入框有焦点时换不了会话」的答案：裸 ↑/↓ 归输入框（要在打的
	// 字里挪光标），所以换会话必须走组合键。Discordo 用的是 alt+↑/↓，
	// 同一个道理；这里取 ctrl+↑/↓，因为 Windows 终端下 ESC 与 Alt 的
	// 歧义让 alt+方向键不可靠。
	case "ctrl+up":
		return m.switchThread(-1)
	case "ctrl+down":
		return m.switchThread(1)

	// ↑/↓ 在输入框是空的时候拿来滚正文，非空就还给输入框。
	//
	// 不加这一条的话，会话视图里能滚的就只剩 PgUp/PgDn 和滚轮，而用户的
	// 第一直觉是方向键 ——「chatView 完全无法滚动」有一半是这么来的。
	// 输入框非空时不抢：那时候用户是在改字，↑/↓ 该归光标。
	case "up":
		if m.inputDraftEmpty() {
			m.scrollChat(chatLineStep)
			return m, nil
		}
	case "down":
		if m.inputDraftEmpty() {
			m.scrollChat(-chatLineStep)
			return m, nil
		}

	// 会话里的刷新。
	//
	// 必须带 Ctrl：这里输入框有焦点，裸 r 会被当成正文的字符打进去
	// （下面那段注释说的就是这个坑）。列表上的 r 还在，那是另一个界面。
	//
	// 只发一条命令（同步），正文的重载由 syncResultMsg 那条路自己接上。
	// **别写成 tea.Batch(syncCmd, loadActiveBodiesCmd)**：两条命令并发跑，
	// 读正文那条若在同步落地之前开始，读到的就是旧会话，完成得又晚的话
	// 还会把新正文覆盖回去 —— 表现为「按了刷新，内容随机地不更新」。
	case "ctrl+r":
		if m.app == nil {
			return m, nil
		}
		m.busy = true
		return m, m.refreshCmd()

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
		//
		// 这里是**真的**要离开，所以连 activeID 一起清掉，别和上面 Esc
		// 那条混了：Esc 是「退出去看看别处」，Ctrl+U 是「把这个会话
		// 放回未读」，后者留着 activeID 反而会让下次进列表时右栏还挂着
		// 一个已经标成未读的会话。
		if th, ok := m.activeThread(); ok {
			m.busy = true
			m.mode = modeList
			m.activeID = ""
			m.bodies = map[string]app.Body{}
			m.input.Blur()
			return m, m.unreadCmd(th)
		}
	case "ctrl+d":
		if th, ok := m.activeThread(); ok {
			m.pendingID = th.ID
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
		// activeID 为空说明这是刚新建、还没落地的会话。
		if m.activeID == "" {
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

	// 新建会话也画在会话那一栏里，所以同样支持 F2。
	// 「只有 Chat mode 支持 Zen」约束的是**界面形态**（列表 / 搜索没有
	// Zen 版），不是 mode 枚举值 —— 新建会话这一屏用的就是会话的排版。
	case "f2":
		m.toggleZen()
		return m, nil
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
		m.activeID = "" // 还没建立会话
		m.bodies = map[string]app.Body{}
		m.input.Placeholder = chatInputPlaceholder
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
	if m.app == nil || m.activeID == "" {
		return nil
	}
	if _, ok := m.app.Thread(m.activeID); !ok {
		return nil
	}
	return m.loadBodiesByIDCmd(m.activeID)
}

// reloadBodiesIfChanged 决定「这一轮同步之后，要不要重读当前会话的正文」。
//
// 四种情况分开看：
//   - 同步出错 → 不读。索引可能只同步了一半，读出来的是一份残缺的正文。
//   - 用户按了 Ctrl+R（manual）→ 读。按了键就得有动静，这也是重试正文
//     加载失败的机会。
//   - 轮询且确有新数据（changed）→ 读。这正是要修的那个 bug：不读的话，
//     对方新来的那一封只出现在左边列表里，右边停在上一轮的内容上。
//   - 轮询且没有新数据 → 不读。定时器每 30 秒响一次，每次都把整个会话的
//     正文重拉一遍，等于把空转的轮询变成稳定的网络流量。
//
// 不在会话里时 loadActiveBodiesCmd 自己返回 nil，所以列表模式下这里是无操作。
func (m Model) reloadBodiesIfChanged(msg syncResultMsg) tea.Cmd {
	if msg.err != nil {
		return nil
	}
	if !msg.changed && !msg.manual {
		return nil
	}
	return m.loadActiveBodiesCmd()
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
//
// 顺序也在这一步定下来（未读排前面），因为列表的分组要靠它 ——
// 光标索引的就是这个切片，重排和不重排必须发生在同一个地方，否则
// 「按一下 ↓ 跳到哪儿」和「屏幕上第几行亮」会对不上。
func (m *Model) refreshVisible() {
	m.visible = groupUnreadFirst(filterThreads(m.threads, m.activeFolder, m.query))
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

// ---- 滚动 ----

const (
	// chatLineStep 是 ↑/↓ 一次滚多少行。
	//
	// 三行是个折中：一屏二十来行，滚一行几乎看不出动，滚一屏又太粗 ——
	// 三行既有明确反馈，也还停得住。
	chatLineStep = 3

	// wheelStep 是鼠标滚轮一格滚多少行。
	//
	// 和 chatLineStep 取同一个值，让「按方向键」和「滚滚轮」手感一致 ——
	// 差着数的话，用户在两种输入之间切换会觉得有一边是坏的。
	wheelStep = chatLineStep
)

// chatPageStep 是 PgUp/PgDn 一次滚多少行：一屏减一行。
//
// 留一行重叠是标准做法 —— 两屏完全错开的话，读者要自己找「刚才读到
// 哪了」。
//
// 一屏的高度走 chatViewport()，因为 Zen 和 Normal 的可见行数不同。
func (m Model) chatPageStep() int {
	if n := m.chatViewport() - 1; n > 1 {
		return n
	}
	return 1
}

// scrollChat 把正文往上卷（delta>0）或往下卷（delta<0）delta 行，并收敛。
func (m *Model) scrollChat(delta int) {
	m.scroll += delta
	m.clampChatScroll()
}

// clampChatScroll 把 scroll 收进 [0, maxChatScroll]。
//
// scroll 是「从底部往上卷的行数」，所以两个方向都会越界：往下卷过头是
// 负数（收成 0），往上卷过头会超过正文总行数（收成 maxChatScroll）。
//
// 这件事以前完全没人做，于是按住 PgUp 会让 scroll 无限累加 —— 视图早就
// 到顶了还在加；窗口一变或者换个会话，残留的值就把正文顶出画面，表现
// 为「右边一片空白」。这也正是「自适应窗口大小」那条需求里最容易漏的
// 一步：光把宽高存下来不算自适应，得让滚动位置跟着新的尺寸重新落到
// 合法的范围里。
func (m *Model) clampChatScroll() {
	if max := m.maxChatScroll(); m.scroll > max {
		m.scroll = max
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// maxChatScroll 是正文往上最多能卷多少行（0 表示一屏就放得下）。
func (m Model) maxChatScroll() int {
	n := m.chatBodyLines()
	if v := m.chatViewport(); n > v {
		return n - v
	}
	return 0
}

// chatViewport 是当前布局下正文区能显示的行数。
//
// 抽出来是因为滚动相关的三个地方（maxChatScroll / chatPageStep / clamp）
// 必须用**同一个**可见行数。两种布局的可见行数不同 —— Normal 是
// bodyHeight 减掉标题那一行，Zen 要扣掉自己的六行固定装饰 —— 各算一遍
// 迟早会对不上，表现是「滚到底还差半行」，很难查（chatBodyLines 的
// 注释里记着同一个坑）。
func (m Model) chatViewport() int {
	if m.zenInChat() {
		statusH := 0
		if m.renderZenStatus(zenContentWidth(m.width)) != "" {
			statusH = 1
		}
		if h := m.height - zenChromeHeight - statusH; h > 1 {
			return h
		}
		return 1
	}
	return chatViewHeight(m.bodyHeight())
}

// chatBodyLines 是当前会话的正文渲染出来一共有多少行。
//
// 它和 renderChat 里那份是同一份计算，共用 renderChatBody —— 抽出来的
// 理由是「最多能卷多少」和「画出来几行」必须用同一个宽度、同一把尺子，
// 各自算一遍迟早会对不上；对不上的表现是「滚到底还差半行」，很难查。
//
// 代价是 O(消息数)：每按一次滚动键要多渲染一遍正文。可以接受 ——
// renderChat 本身就是每帧 O(消息数)，这里只是把同一份活多干一次，
// 而人工按键的频率远低于帧率。
func (m Model) chatBodyLines() int {
	th, ok := m.activeThread()
	if !ok {
		return 0
	}
	// Zen 的正文是**分组之后**的行数，和 Normal 那份不一样，不能混用。
	if m.zenInChat() {
		return len(m.renderZenBody(th))
	}
	return len(m.renderChatBody(th, m.chatBodyWidth()))
}

// inputDraftEmpty 说输入框里是不是没有内容。
//
// 用 TrimSpace 而不是 == ""：敲了一串空格在会话视图里等同于没打字，
// 这时候方向键该归滚动，不该在一个全是空格的框里空转。
func (m Model) inputDraftEmpty() bool {
	return strings.TrimSpace(m.input.Value()) == ""
}

// moveCursor 把列表光标上下挪一格，到边界就停住。
func (m *Model) moveCursor(dir int) {
	m.cursor += dir
	m.clampCursor()
}

// ---- 鼠标 ----

// hitRegion 是屏幕上一块可交互的区域。
type hitRegion int

// 注：这里原本还有个 hitDivider —— 列表栏和会话流之间那根竖线所在的列。
// 竖线去掉之后那一列归了会话流（就是它的第 0 列），这个区域没有存在的
// 理由了。留着它的话，那一列会变成"点了没反应"的死区 —— 而它明明在
// 会话流里面。
const (
	hitNone hitRegion = iota
	hitNav
	hitTabs
	hitList
	hitChat
	hitInput
	hitStatus
)

// hitTest 判断一个坐标落在哪一块。
//
// 判断全部走 measureLayout()，和画界面用的是同一份几何。这是本轮「点得到」
// 能算数（而不是「差不多能点」）的唯一原因：只要画的时候用的是这份
// 几何，点的时候也用这份，两者就不可能漂移。
func (m Model) hitTest(x, y int) (hitRegion, layout) {
	l := m.measureLayout()
	if x < 0 || y < 0 || x >= m.width || y >= m.height {
		return hitNone, l
	}

	switch {
	case l.tabsRow >= 0 && y == l.tabsRow:
		// 标签页占的是内容区那一行；左边那一截还是导航。
		// 内容区从侧栏右沿开始（l.contentX == navWidth），所以这里只
		// 需要一条判断 —— 上一版要两条，是因为两个数之间还夹着一格空隙。
		if x < l.contentX {
			return hitNav, l
		}
		return hitTabs, l

	case y >= l.inputTop && y < l.inputTop+l.inputRows:
		return hitInput, l

	case y == l.statusRow:
		return hitStatus, l

	case y >= l.bodyTop && y < l.bodyTop+l.bodyH:
		switch {
		case x < l.navX+l.navW:
			return hitNav, l
		case x < l.chatX:
			return hitList, l
		default:
			return hitChat, l
		}
	}
	return hitNone, l
}

// handleMouse 处理鼠标事件：滚轮滚动，左键点击。
//
// 滚轮按**指针落在哪一栏**分流：在导航或列表上挪光标，在会话流上滚正文。
// 这就是「聚焦」在鼠标这一侧的正解 —— 指针在哪，滚轮就管哪，不用先按
// 一个键把焦点挪过去。
//
// 左边那一栏也归滚轮管（而不是「只要进了会话，滚轮就只滚正文」）：
// 用户看着某一条会话想往下挪，手自然会放到那一条上面。
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// 只认「按下」这一下。滚轮的松开事件也带着同一个 Button，不挡掉的话
	// 一格滚轮会滚两格。
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}

	// 帮助页自己占满一屏、有自己的滚动位置，单独处理。
	// 少了这一条的话，在帮助页上滚滚轮会去动底下那个列表的光标 ——
	// 屏幕上看不见任何变化，用户只会觉得轮子坏了。
	if m.mode == modeHelp {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.helpScroll -= wheelStep
			m.clampHelpScroll()
		case tea.MouseButtonWheelDown:
			m.helpScroll += wheelStep
			m.clampHelpScroll()
		}
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.handleWheel(1, msg.X, msg.Y)
	case tea.MouseButtonWheelDown:
		return m.handleWheel(-1, msg.X, msg.Y)
	case tea.MouseButtonLeft:
		return m.handleClick(msg.X, msg.Y)
	}
	return m, nil
}

// handleWheel 处理滚轮。up>0 表示向上滚。
func (m Model) handleWheel(up, x, y int) (tea.Model, tea.Cmd) {
	region, _ := m.hitTest(x, y)

	switch m.mode {
	case modeChat, modeNewChat:
		if region == hitNav || region == hitList {
			m.moveCursor(-up)
		} else {
			m.scrollChat(up * wheelStep)
		}
	case modeList:
		m.moveCursor(-up)
	}
	// 其余模式（搜索、文件夹选择、确认框、配置向导、解锁）不接管滚轮：
	// 它们要么是弹在最上面的一小条，要么在等一个明确的答复，
	// 让滚轮去改背后那些状态只会造出「屏幕上没变、底下却变了」的怪事。
	return m, nil
}

// handleClick 处理左键点击。
//
// 会话内的点击一律用 keepDraft=true 进新会话：点击比回车「手滑」得多，
// 打了一半的字不该因为点到别处就没了。草稿跨会话保留本来就是这个界面
// 已有的行为（Ctrl+↑/↓ 走的就是它）。
func (m Model) handleClick(x, y int) (tea.Model, tea.Cmd) {
	region, l := m.hitTest(x, y)

	switch region {
	case hitTabs:
		if c, ok := m.tabChipAt(l, x); ok && c.id != m.activeID {
			th, ok := m.threadByID(c.id)
			if !ok {
				return m, nil
			}
			return m.enterThread(th, false, true)
		}

	case hitNav:
		// 导航只认「主体区里的那几行」；上下留白和贯通到底的那一条
		// 都不该有点击效果。
		if y < l.bodyTop || y >= l.bodyTop+l.bodyH {
			return m, nil
		}
		if it, ok := m.navRowIndexAt(y - l.bodyTop); ok {
			m.setFolder(it.folder)
		}

	case hitList:
		if m.mode != modeList {
			return m, nil
		}
		idx, ok := m.listRowAt(l.listW, l.bodyH, y-l.bodyTop)
		if !ok {
			return m, nil
		}
		m.cursor = idx
		return m.enterThread(m.visible[idx], false, true)

	case hitChat:
		if m.mode != modeChat && m.mode != modeNewChat {
			return m, nil
		}
		// 点在正文最右那一列（滚动条那一列）上就跳过去。
		if x == l.chatX+l.chatW-1 {
			m.scrollToBar(y - l.bodyTop - 1)
		}
	}
	return m, nil
}

// scrollToBar 把正文滚到滚动条上第 row 行对应的位置。
//
// 换算里带那个 -1 的 offset，是因为正文第一行是会话标题，滚动条从
// 标题下面才开始（renderChat 里画的就是这样）。少了它，点最上面
// 一格会差一行 —— 看着「点到底了还差一点」。
func (m *Model) scrollToBar(row int) {
	total := m.chatBodyLines()
	viewH := chatViewHeight(m.bodyHeight())
	max := total - viewH
	if max <= 0 {
		return
	}
	if row < 0 {
		row = 0
	}
	if row >= viewH {
		row = viewH - 1
	}
	// 滑块顶端在滚动条上的位置：off = row * (max) / (viewH - 1)。
	// viewH 为 1 时没有可插值的地方，直接滚到底。
	off := 0
	if viewH > 1 {
		off = row * max / (viewH - 1)
	}
	m.scroll = max - off
	m.clampChatScroll()
}

// pageInputWidth 是整页视图里那个独占一行的输入框的上限。
//
// textinput 的 Width 为 0 时**不裁剪**：用户敲一长句就会把这一行顶出屏幕。
// 解锁页和配置向导的输入框自己占一整行，给它一个略小于终端宽度的上限
// 就够了 —— 不需要精确，因为那一行没有别的东西要跟它分地方。
//
// 会话里那个浮起输入框走的是另一条路（renderInputLine 现算宽度），
// 因为它要和右侧的提示文字分同一行，宽度必须精确到列。
func (m Model) pageInputWidth() int {
	w := m.width - 6
	if w < 10 {
		w = 10
	}
	return w
}

// activeThread 返回当前打开的会话。
func (m Model) activeThread() (thread.Thread, bool) {
	if m.app == nil || m.activeID == "" {
		return thread.Thread{}, false
	}
	return m.app.Thread(m.activeID)
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
