package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// providerIndex 按 ID 找预设下标。写死下标会在有人调整 Providers 顺序时失效。
func providerIndex(id string) int {
	for i, p := range config.Providers {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// newSetupModel 造一个停在服务商选择这一步的向导模型。
func newSetupModel(t *testing.T) Model {
	t.Helper()
	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]app.Body{},
	}
	m.beginSetup()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	return m
}

// 选自定义后必须能填到服务器地址。回归的是：向导里没有这几步时，选自定义
// 会一路走到连接失败 —— 地址没人问，Configured() 永远 false，配置却已落盘。
func TestSetupWizard_CustomProviderCollectsServers(t *testing.T) {
	m := newSetupModel(t)

	idx := providerIndex(config.CustomProviderID)
	if idx < 0 {
		t.Fatal("预设列表里没有自定义服务商")
	}
	m.setup.provider = idx
	m, _ = update(m, keyMsg("enter"))

	if m.setup.step != stepEmail {
		t.Fatalf("选完自定义应先问邮箱地址, step = %v", m.setup.step)
	}

	m.input.SetValue("me@example.com")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepCustomIMAP {
		t.Fatalf("填完邮箱应问 IMAP 服务器, step = %v", m.setup.step)
	}
	if v := m.View(); !strings.Contains(v, "IMAP") {
		t.Errorf("IMAP 步骤没渲染出来:\n%s", v)
	}

	// IMAP：清掉预填值，手填一个带端口的。
	m.input.SetValue("imap.example.com:993")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepCustomSMTP {
		t.Fatalf("step = %v, want stepCustomSMTP", m.setup.step)
	}

	// SMTP：这次省略端口，走协议默认值。
	m.input.SetValue("smtp.example.com")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepCustomTLS {
		t.Fatalf("step = %v, want stepCustomTLS", m.setup.step)
	}

	if m.cfg.IMAP.Host != "imap.example.com" || m.cfg.IMAP.Port != 993 {
		t.Errorf("IMAP 端点没写对: %+v", m.cfg.IMAP)
	}
	if m.cfg.SMTP.Host != "smtp.example.com" || m.cfg.SMTP.Port != config.DefaultSMTPPort {
		t.Errorf("SMTP 端点没写对: %+v", m.cfg.SMTP)
	}

	// 加密方式：移到第二项 STARTTLS 再确定。
	m, _ = update(m, keyMsg("down"))
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepPassword {
		t.Fatalf("step = %v, want stepPassword", m.setup.step)
	}
	if m.cfg.IMAP.TLS != config.TLSStartTLS || m.cfg.SMTP.TLS != config.TLSStartTLS {
		t.Errorf("加密方式没写对: IMAP=%q SMTP=%q", m.cfg.IMAP.TLS, m.cfg.SMTP.TLS)
	}

	// 配置该完整了 —— Configured() 是连接的前置条件，也是当初卡住的地方。
	if !m.cfg.Configured() {
		t.Errorf("走完服务器步骤后配置仍不完整: IMAP=%+v SMTP=%+v", m.cfg.IMAP, m.cfg.SMTP)
	}
}

// 手填那两步要按域名预填，值可见可改 —— 不是静默推断。
func TestSetupWizard_CustomServersPrefilledFromDomain(t *testing.T) {
	m := newSetupModel(t)
	m.setup.provider = providerIndex(config.CustomProviderID)
	m, _ = update(m, keyMsg("enter"))

	m.input.SetValue("me@example.com")
	m, _ = update(m, keyMsg("enter"))

	if got := m.input.Value(); got != "imap.example.com:993" {
		t.Errorf("IMAP 预填值 = %q, want %q", got, "imap.example.com:993")
	}

	m, _ = update(m, keyMsg("enter")) // 接受预填
	if got := m.input.Value(); got != "smtp.example.com:465" {
		t.Errorf("SMTP 预填值 = %q, want %q", got, "smtp.example.com:465")
	}

	// 直接回车接受预填，配置也要是对的。
	m, _ = update(m, keyMsg("enter"))
	if m.cfg.IMAP.Host != "imap.example.com" || m.cfg.SMTP.Host != "smtp.example.com" {
		t.Errorf("接受预填后端点不对: IMAP=%+v SMTP=%+v", m.cfg.IMAP, m.cfg.SMTP)
	}
}

// 预设服务商不该被多出来的步骤打扰：选完直接问邮箱地址。
func TestSetupWizard_PresetSkipsServerSteps(t *testing.T) {
	m := newSetupModel(t)

	m.setup.provider = providerIndex("qq")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepEmail {
		t.Fatalf("预设服务商应直接进入邮箱地址, step = %v", m.setup.step)
	}

	m.input.SetValue("me@qq.com")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepPassword {
		t.Fatalf("预设服务商填完邮箱应直接问凭据, step = %v", m.setup.step)
	}
}

// 预设的地址不能被「按域名推断」覆盖。曾有一版实现拿域名查 DNS 反推，会把
// imap.qq.com:993 覆盖成 MX 主机 mx3.qq.com —— 那是收信服务器。
func TestSetupWizard_PresetServersSurviveEmailStep(t *testing.T) {
	m := newSetupModel(t)

	m.setup.provider = providerIndex("qq")
	m, _ = update(m, keyMsg("enter"))

	m.input.SetValue("me@qq.com")
	m, _ = update(m, keyMsg("enter"))

	if m.cfg.IMAP.Host != "imap.qq.com" || m.cfg.IMAP.Port != 993 {
		t.Errorf("IMAP 被改动了: %+v", m.cfg.IMAP)
	}
	if m.cfg.SMTP.Host != "smtp.qq.com" || m.cfg.SMTP.Port != 465 {
		t.Errorf("SMTP 被改动了: %+v", m.cfg.SMTP)
	}
}

// 端口写错要当场报错，而不是默默套默认端口一路连不上。
func TestSetupWizard_RejectsBadServerAddress(t *testing.T) {
	m := newSetupModel(t)

	m.setup.provider = providerIndex(config.CustomProviderID)
	m, _ = update(m, keyMsg("enter"))
	m.input.SetValue("me@example.com")
	m, _ = update(m, keyMsg("enter"))

	m.input.SetValue("imap.example.com:not-a-port")
	m, _ = update(m, keyMsg("enter"))

	if m.setup.step != stepCustomIMAP {
		t.Fatalf("地址非法时应留在原步骤, step = %v", m.setup.step)
	}
	if m.setup.err == "" {
		t.Error("地址非法时应该给出错误提示")
	}
	if m.cfg.IMAP.Host != "" {
		t.Errorf("地址非法时不该写进配置: %+v", m.cfg.IMAP)
	}
}

// 步数要按流程长度现算，写死会让主密码那一步显示成「第 3 步」而实际是第 6 步。
func TestSetupWizard_CustomStepNumbering(t *testing.T) {
	m := newSetupModel(t)
	m.setup.provider = providerIndex(config.CustomProviderID)

	m, _ = update(m, keyMsg("enter")) // → 邮箱地址
	if got := m.View(); !strings.Contains(got, "第 1 步") {
		t.Errorf("邮箱地址应是第 1 步:\n%s", got)
	}

	m.input.SetValue("me@example.com")
	m, _ = update(m, keyMsg("enter")) // → IMAP
	if got := m.View(); !strings.Contains(got, "第 2 步") {
		t.Errorf("IMAP 应是第 2 步:\n%s", got)
	}

	m.input.SetValue("imap.example.com:993")
	m, _ = update(m, keyMsg("enter")) // → SMTP
	if got := m.View(); !strings.Contains(got, "第 3 步") {
		t.Errorf("SMTP 应是第 3 步:\n%s", got)
	}

	m.input.SetValue("smtp.example.com:465")
	m, _ = update(m, keyMsg("enter")) // → 加密方式
	if got := m.View(); !strings.Contains(got, "第 4 步") {
		t.Errorf("加密方式应是第 4 步:\n%s", got)
	}

	m, _ = update(m, keyMsg("enter")) // → 凭据
	if got := m.View(); !strings.Contains(got, "第 5 步") {
		t.Errorf("凭据应是第 5 步:\n%s", got)
	}

	m.input.SetValue("authcode")
	m, _ = update(m, keyMsg("enter")) // → 主密码
	if got := m.View(); !strings.Contains(got, "第 6 步") {
		t.Errorf("主密码应是第 6 步:\n%s", got)
	}
}
