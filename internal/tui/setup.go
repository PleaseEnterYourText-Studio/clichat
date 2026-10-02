package tui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// setupStep 是配置向导的步骤。
type setupStep int

const (
	stepProvider setupStep = iota
	stepEmail
	stepPassword
	stepMaster
)

// setupState 是配置向导的状态。
type setupState struct {
	step     setupStep
	provider int
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

// handleUnlockKey 处理主密码输入。
func (m Model) handleUnlockKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "ctrl+r":
		// 凭据损坏或想换账号时的逃生出口。删掉本地凭据重新走一遍向导。
		_ = config.DeleteCredentials()
		// 重置的是内容，不是落点：路径要留住，向导最后那步 Save 得写回
		// 同一个 config.json。（配置不再有「路径为空就退回默认位置」这
		// 种兜底了，丢掉路径会让保存直接失败。）
		path := m.cfg.Path()
		m.cfg = config.Default()
		m.cfg.SetPath(path)
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

	// 第一步是在列表里选，不走文本输入。
	if m.setup.step == stepProvider {
		return m.handleProviderChoice(msg)
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
		m.setup.err = ""
		return m.advanceToPassword()

	case stepPassword:
		// 授权码里可能有前后空格，这里刻意不 Trim。
		m.setup.password = m.input.Value()
		m.setup.err = ""
		m.setup.step = stepMaster
		m.input.EchoMode = textinput.EchoPassword
		m.input.Placeholder = "设置主密码，用于加密本地凭据"
		m.input.SetValue("")
		return m, textinput.Blink

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
		m.setup.step = stepEmail
		m.setup.err = ""
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "邮箱地址，例如 me@qq.com"
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m Model) advanceToPassword() (tea.Model, tea.Cmd) {
	m.setup.step = stepPassword
	m.input.EchoMode = textinput.EchoPassword
	m.input.Placeholder = "授权码或应用专用密码"
	if p := m.currentProvider(); p != nil && p.PasswordLabel != "" {
		m.input.Placeholder = p.PasswordLabel
	}
	m.input.SetValue("")
	m.input.Focus()
	return m, textinput.Blink
}

// finishSetup 落盘配置与凭据，然后连接。
func (m Model) finishSetup(master string) (tea.Model, tea.Cmd) {
	if len(master) < 4 {
		m.setup.err = "主密码至少 4 位"
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

// localPart 取邮箱 @ 前面的部分，用作默认显示名。
func localPart(addr string) string {
	if i := strings.IndexByte(addr, '@'); i > 0 {
		return addr[:i]
	}
	return addr
}
