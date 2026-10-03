package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/oauth"
)

// setupStep 是配置向导的步骤。常量顺序不等于实际顺序 —— 走哪几步由
// setupFlow 决定。
type setupStep int

const (
	stepProvider setupStep = iota
	stepEmail
	stepCustomIMAP
	stepCustomSMTP
	stepCustomTLS
	// stepOAuthClient 收 OAuth2 的 client id。
	//
	// 它只在 Auth=AuthOAuth2 的服务商上出现，位置和 stepPassword 一样 ——
	// 两者是同一个问题的两种答案：这个账号怎么证明身份。
	stepOAuthClient
	// stepOAuthWait 等用户在浏览器里点完同意。
	stepOAuthWait
	stepPassword
	stepMaster
)

// tlsOption 是自定义服务商可选的加密方式。
type tlsOption struct {
	Label string
	Value string
}

// tlsOptions 是加密方式选项，顺序即界面顺序，默认第一个（993/465 最常见）。
var tlsOptions = []tlsOption{
	{Label: "隐式 TLS（993 / 465）", Value: config.TLSImplicit},
	{Label: "STARTTLS（143 / 587）", Value: config.TLSStartTLS},
}

// setupState 是配置向导的状态。
type setupState struct {
	step     setupStep
	provider int
	tls      int
	password string
	err      string

	// oauth 是 OAuth2 服务商要写进账号的客户端信息。
	//
	// ⚠️ client id **由用户自己填**：Google 和 Microsoft 都不允许客户端内置
	// client secret，所以只能让用户去自己的控制台注册一个应用。向导里
	// 必须把这一步说清楚，否则用户会对着一个连不上的账号反复输密码。
	oauth *config.OAuthClient
	// flow 是正在进行的授权。非 nil 表示停在 stepOAuthWait。
	flow *oauth.Flow
	// token 是走完授权拿到的 token。
	token *config.Token
	// useOAuth 记录「这一次走 OAuth2」这个选择。
	//
	// 只有两条路都走得通的服务商（AuthEither）会用到它：默认 false（走密钥），
	// 用户在密钥那一步按 Ctrl+O 之后才置真。对"必须走 OAuth2"的服务商它
	// 一直是 false 而流程照样是 OAuth —— 那种情况下该看的是预设，
	// 所以判断一律走 Model.usesOAuth()，别在这儿直接读。
	useOAuth bool
	// reveal 为真时密码栏显示明文。
	//
	// 授权码和应用专用密码都是几十位的随机串，掩码状态下**抄错一个字符
	// 看不出来**，而报出来的是「认证失败」。给一个显式的切换键，比让用户
	// 怀疑自己是不是多打了一位便宜。
	reveal bool

	// forceOverwrite 为真表示「新的主密码解不开已有凭据，用户已经确认
	// 要用它覆盖」。见 finishSetup。
	forceOverwrite bool
}

// beginSetup 进入配置向导。
//
// ⚠️ 它**不删任何东西**。老版本这里是「删掉 credentials.enc 再重来」，
// 单账号时代没问题；多账号之后那是把**所有**账号的授权码一起清掉 ——
// 用户只是想再加一个邮箱。旧凭据该不该丢、丢哪一份，由 finishSetup 按
// 它在磁盘上真正读到的东西决定，不在这里预先销毁证据。
func (m *Model) beginSetup() {
	m.mode = modeSetup
	m.setup = setupState{step: stepProvider}
	m.applyPageInputStyle()
	m.input.EchoMode = textinput.EchoNormal
	m.input.Placeholder = ""
	m.input.SetValue("")
	m.input.Blur()
}

// currentProvider 返回向导里当前选中的服务商。
func (m Model) currentProvider() *config.Provider {
	if m.setup.provider < 0 || m.setup.provider >= len(config.Providers) {
		return nil
	}
	return &config.Providers[m.setup.provider]
}

// isCustomProvider 报告当前选中的是不是「自定义」预设。
func (m Model) isCustomProvider() bool {
	p := m.currentProvider()
	return p != nil && p.ID == config.CustomProviderID
}

// supportsOAuth 报告当前服务商**能不能**走 OAuth2。
func (m Model) supportsOAuth() bool {
	p := m.currentProvider()
	return p != nil && p.SupportsOAuth2()
}

// oauthOptional 报告当前服务商是不是「两条路都行」。
//
// 单独拆出来是因为它对应一个**具体动作**：在密钥那一步给一个切换键、
// 在 OAuth 那一步允许反悔退回来。只有"必须走 OAuth2"的服务商没有这一步
// 可说 —— 对它来说，"退回去填密码"等于退回到一条不存在的路。
func (m Model) oauthOptional() bool {
	p := m.currentProvider()
	return p != nil && p.Auth == config.AuthEither
}

// usesOAuth 报告这一次配置**实际**走不走 OAuth2。
//
// ⚠️ 不能只看预设。「两条路都行」的服务商默认走密钥那条（步骤少、不用注册
// 应用），用户按 Ctrl+O 才切过来 —— 而流程序列必须跟着**这一次的选择**变，
// 否则切过去之后 setupFlow 里没有 stepOAuthClient，enterNext 找不到当前步骤，
// 会把用户直接甩回第一步重填邮箱。
func (m Model) usesOAuth() bool {
	p := m.currentProvider()
	if p == nil {
		return false
	}
	if p.RequiresOAuth2() {
		return true
	}
	return p.Auth == config.AuthEither && m.setup.useOAuth
}

// setupFlow 返回当前服务商对应的步骤序列。
//
// 预设的服务器地址已由 ApplyProvider 写好，只问邮箱地址；自定义没人能替它
// 填，所以多三步。这三步排在邮箱地址之后，因为要拿域名做预填（见 guessIMAPHost）。
//
// 「怎么证明身份」那一步按服务商分叉：填授权码的只有一步，走 OAuth2 的要
// 两步（填 client id → 浏览器里授权）。序列里**必须**有分叉 —— 把一个
// 连不上的账号一路问到主密码再报「认证失败」，是最容易让人放弃的那条路。
func (m Model) setupFlow() []setupStep {
	if m.isCustomProvider() {
		return []setupStep{
			stepEmail, stepCustomIMAP, stepCustomSMTP, stepCustomTLS,
			stepPassword, stepMaster,
		}
	}
	if m.usesOAuth() {
		return []setupStep{stepEmail, stepOAuthClient, stepOAuthWait, stepMaster}
	}
	return []setupStep{stepEmail, stepPassword, stepMaster}
}

// enterStep 把向导切到某个步骤，并按该步骤的需要配置输入框。
func (m Model) enterStep(step setupStep) (tea.Model, tea.Cmd) {
	m.setup.step = step
	m.setup.err = ""
	m.input.SetValue("")
	m.setup.reveal = false

	switch step {
	case stepEmail:
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "邮箱地址，例如 me@qq.com"

	case stepCustomIMAP:
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "imap.example.com:993"
		// 按域名预填一个猜测值，用户可以改。
		m.input.SetValue(guessIMAPHost(m.cfg.Account.Email))

	case stepCustomSMTP:
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "smtp.example.com:465"
		m.input.SetValue(guessSMTPHost(m.cfg.Account.Email))

	case stepCustomTLS:
		// 列表选择步骤，和选服务商一样不走文本输入。
		m.setup.tls = 0
		m.input.Blur()
		return m, nil

	case stepOAuthClient:
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "xxxxxxxx.apps.googleusercontent.com"
		// 预填这个账号上次填过的：重配时不必再去控制台把 id 抄一遍。
		if p := m.cfg.ProfileOf(m.cfg.Account.Email); p != nil && p.OAuth != nil {
			m.input.SetValue(p.OAuth.ClientID)
		}

	case stepOAuthWait:
		// 这一步不收输入，只等浏览器那边回调。
		m.input.Blur()
		return m.beginOAuthWait()

	case stepPassword:
		m.input.EchoMode = textinput.EchoPassword
		m.input.Placeholder = "授权码或应用专用密码"
		if p := m.currentProvider(); p != nil && p.PasswordLabel != "" {
			m.input.Placeholder = p.PasswordLabel
		}

	case stepMaster:
		m.input.EchoMode = textinput.EchoPassword
		m.input.Placeholder = "设置主密码，用于加密本地凭据"
	}

	m.input.Focus()
	return m, textinput.Blink
}

// enterNext 进入 cur 在当前流程里的下一步。cur 不在流程里（如 stepProvider）
// 就落到流程第一步；已是最后一步则不动，收尾交给调用方。
func (m Model) enterNext(cur setupStep) (tea.Model, tea.Cmd) {
	flow := m.setupFlow()
	for i, s := range flow {
		if s != cur {
			continue
		}
		if i+1 < len(flow) {
			return m.enterStep(flow[i+1])
		}
		return m, nil
	}
	if len(flow) > 0 {
		return m.enterStep(flow[0])
	}
	return m, nil
}

// handleSetupKey 处理配置向导的按键。
func (m Model) handleSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "esc":
		// Esc 只在等授权那一步有意义：那时有一个后台监听和一次浏览器
		// 会话挂着，得把它们收掉。别的步骤上按 Esc 不该丢掉已经填的东西。
		if m.setup.step == stepOAuthWait {
			return m.cancelOAuthWait()
		}
		// 两条路都走得通的服务商，在「填 client id」那一步按 Esc 就是
		// 「算了，还是填授权码吧」—— 没有这条退路，用户一旦点进来就只能
		// 去注册一个应用，或者退出去重来。
		if m.setup.step == stepOAuthClient && m.oauthOptional() {
			m.setup.useOAuth = false
			m.setup.oauth = nil
			return m.enterStepWith(stepPassword, "已改回填应用专用密码")
		}
		return m, nil

	case "ctrl+p":
		switch m.setup.step {
		case stepPassword, stepMaster:
			m.setup.reveal = !m.setup.reveal
			m.applyReveal(m.setup.reveal)
		}
		return m, nil

	case "ctrl+o":
		// 只有「两条路都行」的服务商才有这一步。放在输入框更新之前处理，
		// 否则这个键会被 textinput 吃掉（它认得 Ctrl+O 吗不重要 ——
		// 重要的是我们不想把决定权交给它）。
		if m.setup.step == stepPassword && m.oauthOptional() {
			m.setup.useOAuth = true
			return m.enterStepWith(stepOAuthClient,
				"改用 OAuth2 授权：这一栏填你自己注册的应用的 client id")
		}
		return m, nil
	}

	// 列表选择类的步骤不走文本输入。
	switch m.setup.step {
	case stepProvider:
		return m.handleProviderChoice(msg)
	case stepCustomTLS:
		return m.handleTLSChoice(msg)
	}

	if msg.String() != "enter" {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	// 已经在等授权了：这一步没有输入框，回车当「再等一等」处理。
	if m.setup.step == stepOAuthWait {
		return m, nil
	}

	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		return m, nil
	}

	switch m.setup.step {
	case stepEmail:
		if !mail.LooksLikeAddress(value) {
			m.setup.err = "地址格式不对"
			return m, nil
		}
		m.cfg.Account.Email = value
		m.cfg.Account.DisplayName = localPart(value)
		return m.enterNext(stepEmail)

	case stepCustomIMAP:
		ep, err := config.ParseEndpoint(value, config.DefaultIMAPPort)
		if err != nil {
			m.setup.err = "IMAP 服务器填错了：" + err.Error()
			return m, nil
		}
		m.cfg.IMAP = ep
		return m.enterNext(stepCustomIMAP)

	case stepCustomSMTP:
		ep, err := config.ParseEndpoint(value, config.DefaultSMTPPort)
		if err != nil {
			m.setup.err = "SMTP 服务器填错了：" + err.Error()
			return m, nil
		}
		m.cfg.SMTP = ep
		return m.enterNext(stepCustomSMTP)

	case stepOAuthClient:
		m.setup.oauth = &config.OAuthClient{ClientID: value}
		return m.enterNext(stepOAuthClient)

	case stepPassword:
		// 授权码里可能有前后空格，这里刻意不 Trim。
		m.setup.password = m.input.Value()
		return m.enterNext(stepPassword)

	case stepMaster:
		return m.finishSetup(m.input.Value())
	}
	return m, nil
}

// handleProviderChoice 处理服务商选择。
func (m Model) handleProviderChoice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.setup.provider > 0 {
			m.setup.provider--
		}
	case "down", "j":
		if m.setup.provider < len(config.Providers)-1 {
			m.setup.provider++
		}
	case "enter":
		if p := m.currentProvider(); p != nil {
			config.ApplyProvider(m.cfg, p)
		}
		// 换了服务商，上一次填的 client id 就不属于任何东西了（Google 的
		// id 拿到微软那儿只会得到一个看不懂的报错）。顺手清掉，而不是
		// 让它跟着走到下一步 —— 那等于把上一个服务商的凭据写进这个账号。
		m.setup.oauth = nil
		// 同理，「这一次走 OAuth2」这个选择也是上一个服务商的。不清的话，
		// 从 Gmail（可选）按过 Ctrl+O 再切到 QQ 邮箱，setupFlow 会拿着一个
		// 谁也不认识的 OAuth 流程去问 client id。
		m.setup.useOAuth = false
		return m.enterNext(stepProvider)
	}
	return m, nil
}

// handleTLSChoice 处理自定义服务商的加密方式选择。
func (m Model) handleTLSChoice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.setup.tls > 0 {
			m.setup.tls--
		}
	case "down", "j":
		if m.setup.tls < len(tlsOptions)-1 {
			m.setup.tls++
		}
	case "enter":
		// IMAP 和 SMTP 共用一个选择。
		tls := tlsOptions[m.setup.tls].Value
		m.cfg.IMAP.TLS = tls
		m.cfg.SMTP.TLS = tls
		return m.enterNext(stepCustomTLS)
	}
	return m, nil
}

// oauthResultMsg 是授权流程的回执。
type oauthResultMsg struct {
	token config.Token
	err   error
}

// beginOAuthWait 起一次授权：建本地回调监听、把授权地址在浏览器里打开、
// 返回一个等回调的命令。
//
// 「建流 → 拿 URL 给用户 → 等回调」三步而不是一个阻塞的 Authorize 是
// oauth.Flow 刻意拆开的（见那边的注释）：界面要把地址**显示出来**，
// 还要能中途取消。
func (m Model) beginOAuthWait() (tea.Model, tea.Cmd) {
	p := m.currentProvider()
	if p == nil {
		m.setup.err = "没有选中服务商"
		return m, nil
	}
	if m.setup.oauth == nil || strings.TrimSpace(m.setup.oauth.ClientID) == "" {
		m.setup.err = "缺少 client id"
		return m, nil
	}
	if m.setup.flow != nil {
		m.setup.flow.Close()
		m.setup.flow = nil
	}

	// 按 **provider id** 而不是邮箱域名找端点：Microsoft 365 的租户域名
	// 是各公司自己的，从地址里推不出该用哪套端点。
	prov, ok := oauth.ForProvider(m.cfg.Account.Provider, m.setup.oauth.Tenant)
	if !ok {
		m.setup.err = fmt.Sprintf("这个版本还不认识 %q 的 OAuth 流程", m.cfg.Account.Provider)
		return m, nil
	}

	flow, err := oauth.NewFlow(prov, m.setup.oauth.ClientID)
	if err != nil {
		m.setup.err = shortErr(err)
		return m, nil
	}
	m.setup.flow = flow

	// 尽力打开浏览器，但**不依赖**它成功：SSH 过去的时候浏览器不在本机，
	// 用户只能自己把那串地址拷走（地址在页面上完整打出来了）。
	openBrowser(flow.AuthURL())

	return m, m.oauthWaitCmd()
}

// oauthWaitCmd 等浏览器那边回调。
//
// 刻意用 context.Background 而不是带超时的 ctx：用户可能要在控制台里
// 注册应用、翻好几页文档，一个"三分钟后放弃"的倒计时只会让他白填一遍。
// 想放弃就按 Esc（那会 Close 掉 flow，Wait 随即返回）。
func (m Model) oauthWaitCmd() tea.Cmd {
	flow := m.setup.flow
	if flow == nil {
		return nil
	}
	return func() tea.Msg {
		tok, err := flow.Wait(context.Background())
		return oauthResultMsg{token: tok, err: err}
	}
}

// cancelOAuthWait 取消正在进行的授权，退回上一步。
func (m Model) cancelOAuthWait() (tea.Model, tea.Cmd) {
	if m.setup.flow != nil {
		m.setup.flow.Close()
		m.setup.flow = nil
	}
	return m.enterStepWith(stepOAuthClient, "授权已取消，可以改完再试一次")
}

// applyOAuthResult 处理授权回调的结果。
func (m Model) applyOAuthResult(msg oauthResultMsg) (tea.Model, tea.Cmd) {
	m.setup.flow = nil
	if msg.err != nil {
		// 退回填 client id 那一步，而不是卡在一个没有出路的等待页上。
		return m.enterStepWith(stepOAuthClient, "授权失败："+shortErr(msg.err))
	}
	tok := msg.token
	m.setup.token = &tok
	// 授权拿到的 token 还没有落盘，但它已经能用来证明身份了 —— 直接
	// 往下走设置主密码，最后由 finishSetup 一并写进 credentials.enc。
	return m.enterNext(stepOAuthWait)
}

// enterStepWith 切到某个步骤，并在切完之后留下一句提示。
//
// ⚠️ 顺序不能反：enterStep 会把上一步的错误清掉（那是它该做的事 —— 换了
// 一步就不该顶着上一步的报错），所以"这一句"必须写在它**后面**。反过来的
// 话提示会被自己清掉，用户按了 Esc 取消授权却一个字都看不到，只能对着一屏
// 没变化的画面猜自己按到没有。
func (m Model) enterStepWith(step setupStep, note string) (tea.Model, tea.Cmd) {
	next, cmd := m.enterStep(step)
	mm := next.(Model)
	mm.setup.err = note
	return mm, cmd
}

// openBrowser 尽力在系统默认浏览器里打开一个地址。
//
// 失败**静默忽略**：地址已经在界面上完整显示了，打开浏览器只是省一次复制。
// 为此弹一个错误框，反而是在一个已经能继续的流程里制造焦虑。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// cmd /c start 的第一个参数会被当成窗口标题，空串占位。
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// finishSetup 落盘配置与凭据，然后连接。
func (m Model) finishSetup(master string) (tea.Model, tea.Cmd) {
	if len(master) < 4 {
		m.setup.err = "主密码至少 4 位"
		return m, nil
	}

	// 别把连不上的配置写进磁盘：一旦落盘，下次启动 Configured() 仍是 false，
	// 用户会被反复丢回向导却看不出缺什么。
	if !m.cfg.Configured() {
		m.setup.step = stepProvider
		m.setup.provider = 0
		m.input.Blur()
		m.setup.err = "服务器地址没填全，请重新选择服务商"
		return m, nil
	}

	// 凭据要**并进已有那一份**，不能从零开始造一个。
	//
	// 多账号之后这条是硬要求：从零造的话，保存会把别的账号的授权码一起
	// 抹掉 —— 而用户只是想在向导里再加一个邮箱。第一次配置时 m.creds 是
	// 零值，并进去也还是只有这一个账号。
	creds, ok := m.baseCredentials(master)
	if !ok {
		return m, nil
	}

	creds.SetPassword(m.cfg.Account.Email, m.setup.password)
	// OAuth2 账号没有授权码，token 是走完浏览器授权之后才有的（见
	// stepOAuthWait）。这里把已经拿到的 token 一并并进去。
	if m.setup.token != nil {
		creds.SetOAuthToken(m.cfg.Account.Email, *m.setup.token)
	}
	// 这个账号改成用授权码了，就把它的旧 token 清掉：留着的话，AuthFor
	// 会按 profile 里的 OAuth 决定走哪条路 —— 而 profile 里的 OAuth 已经
	// 被下面的 UpsertProfile 写成 nil，token 就成了谁也用不到的垃圾。
	if m.setup.oauth == nil {
		creds.ForgetOAuthToken(m.cfg.Account.Email)
	}

	// 账号列表按**邮箱** Upsert：同一个邮箱是「重配」，新邮箱是「再加一个」。
	// 所以这里不需要区分「添加」和「重配」两条路 —— 用户在邮箱那一步
	// 填了什么，结果就是什么。
	m.cfg.UpsertProfile(config.Profile{
		Account: m.cfg.Account,
		IMAP:    m.cfg.IMAP,
		SMTP:    m.cfg.SMTP,
		OAuth:   m.setup.oauth,
	})

	if err := m.cfg.Save(); err != nil {
		m.setup.err = "保存配置失败：" + shortErr(err)
		return m, nil
	}
	if err := config.SaveCredentials(master, creds); err != nil {
		m.setup.err = "保存凭据失败：" + shortErr(err)
		return m, nil
	}
	m.master = master
	if err := m.connect(creds); err != nil {
		m.setup.err = "连接失败：" + shortErr(err)
		return m, nil
	}

	m.setup.err = ""
	return m, tea.Batch(m.syncCmd(), m.pollCmd(), textinput.Blink)
}

// baseCredentials 返回「新凭据要并进哪一份」。
//
// 主密码是**一把**，它加密整个 credentials.enc；所以换主密码必然解不开
// 里面的旧条目。这不是能修的东西，而是这个设计的一部分 —— 能做的是：
// 尽量把旧的那份读出来并进去（用户改了某个账号的授权码时，别的账号不能
// 跟着遭殃），读不出来时**明确问一次**再覆盖。
//
// 返回 ok=false 表示「先别保存，界面上已经说清楚了」。
func (m *Model) baseCredentials(master string) (config.Credentials, bool) {
	if len(m.creds.Passwords) > 0 || len(m.creds.OAuth) > 0 {
		return m.creds, true // 本次会话已经有一份了
	}
	if !config.HasCredentials() {
		return m.creds, true // 第一次配置
	}
	prev, err := config.LoadCredentials(master)
	if err == nil {
		return prev, true
	}
	if m.setup.forceOverwrite {
		return m.creds, true
	}

	// 解不开：主密码和现有凭据对不上（或者文件坏了）。旧条目用的还是旧
	// 密码，谁也读不出来了 —— 但用户至少要知道它们会消失。
	m.setup.forceOverwrite = true
	if errors.Is(err, config.ErrWrongPassword) {
		m.setup.err = fmt.Sprintf(
			"这个主密码和已保存的凭据不同，再按一次回车会覆盖它（其他 %d 个账号已存的授权码会一起清掉）",
			len(m.cfg.Profiles()))
	} else {
		m.setup.err = "读不出已保存的凭据：" + shortErr(err) + "。再按一次回车会覆盖它"
	}
	return m.creds, false
}

// stepHeader 返回「第 N 步 · 标题」。步数按当前流程现算 —— 自定义比预设多
// 三步、OAuth2 比授权码多一步，同一个步骤在几种流程里的序号都不一样。
func (m Model) stepHeader(title string) string {
	for i, s := range m.setupFlow() {
		if s == m.setup.step {
			return fmt.Sprintf("第 %d 步 · %s", i+1, title)
		}
	}
	return title
}

// guessIMAPHost / guessSMTPHost 按域名惯例猜一个地址，只用于预填。
//
// 别改成查 DNS：MX 记录是「收信」服务器（gmail.com → gmail-smtp-in.l.google.com），
// 不接受客户端认证投递；null MX 的域名还会推出空主机名。而且 net.Lookup* 没有
// 超时，会在 Update 里同步阻塞界面。
func guessIMAPHost(addr string) string {
	if d := domainOf(addr); d != "" {
		return "imap." + d + ":" + strconv.Itoa(config.DefaultIMAPPort)
	}
	return ""
}

func guessSMTPHost(addr string) string {
	if d := domainOf(addr); d != "" {
		return "smtp." + d + ":" + strconv.Itoa(config.DefaultSMTPPort)
	}
	return ""
}

// domainOf 取邮箱地址 @ 后面的部分。取不到返回空串。
func domainOf(addr string) string {
	_, domain, ok := strings.Cut(addr, "@")
	if !ok {
		return ""
	}
	return strings.TrimSpace(domain)
}

// localPart 取邮箱 @ 前面的部分，用作默认显示名。
func localPart(addr string) string {
	if i := strings.IndexByte(addr, '@'); i > 0 {
		return addr[:i]
	}
	return addr
}
