package tui

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
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
}

// beginSetup 进入配置向导。
func (m *Model) beginSetup() {
	m.mode = modeSetup
	m.setup = setupState{step: stepProvider}
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

// setupFlow 返回当前服务商对应的步骤序列。
//
// 预设的服务器地址已由 ApplyProvider 写好，只问邮箱地址；自定义没人能替它
// 填，所以多三步。这三步排在邮箱地址之后，因为要拿域名做预填（见 guessIMAPHost）。
func (m Model) setupFlow() []setupStep {
	if m.isCustomProvider() {
		return []setupStep{
			stepEmail, stepCustomIMAP, stepCustomSMTP, stepCustomTLS,
			stepPassword, stepMaster,
		}
	}
	return []setupStep{stepEmail, stepPassword, stepMaster}
}

// enterStep 把向导切到某个步骤，并按该步骤的需要配置输入框。
func (m Model) enterStep(step setupStep) (tea.Model, tea.Cmd) {
	m.setup.step = step
	m.setup.err = ""
	m.input.SetValue("")

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

// handleUnlockKey 处理主密码输入。
func (m Model) handleUnlockKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "ctrl+r":
		// 凭据损坏或想换账号时的逃生出口。删掉本地凭据重新走一遍向导。
		_ = config.DeleteCredentials()
		m.cfg = config.Default()
		m.status = ""
		m.beginSetup()
		return m, nil

	case "enter":
		password := m.input.Value()
		if password == "" {
			return m, nil
		}
		creds, err := config.LoadCredentials(password)
		if err != nil {
			if errors.Is(err, config.ErrWrongPassword) {
				m.status = "主密码错误"
			} else {
				m.status = "读取凭据失败：" + shortErr(err)
			}
			m.input.SetValue("")
			return m, nil
		}
		if err := m.connect(creds); err != nil {
			m.status = "连接失败：" + shortErr(err)
			return m, nil
		}
		return m, tea.Batch(m.syncCmd(), m.pollCmd(), textinput.Blink)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleSetupKey 处理配置向导的按键。
func (m Model) handleSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
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

	creds := config.Credentials{Password: m.setup.password}

	if err := m.cfg.Save(); err != nil {
		m.setup.err = "保存配置失败：" + shortErr(err)
		return m, nil
	}
	if err := config.SaveCredentials(master, creds); err != nil {
		m.setup.err = "保存凭据失败：" + shortErr(err)
		return m, nil
	}
	if err := m.connect(creds); err != nil {
		m.setup.err = "连接失败：" + shortErr(err)
		return m, nil
	}

	m.setup.err = ""
	return m, tea.Batch(m.syncCmd(), m.pollCmd(), textinput.Blink)
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
