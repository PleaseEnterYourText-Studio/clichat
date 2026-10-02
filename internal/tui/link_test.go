package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// 这一组测试守着配置向导里的可点击链接。
//
// 背景（踩过的坑）：链接用 OSC 8 包成可点击的，但 styleLink 一旦带上
// Underline，URL 的每个字符都会被单独套一串 SGR，把 OSC 8 序列切碎 ——
// 表现是「看着完全正常，但点不动」。
//
// 曾经以为「Go 层的单测抓不到它，是 bubbletea 的渲染器在后面切碎的」。
// 2026-10-02 实测纠正：逐字符输出是 lipgloss 的 Style.Render 干的，纯 Go 层，
// 跟 bubbletea 无关（纯文本 styleLink.Underline(true).Render("点我") 同样逐字）。
// 也就是说**下面是抓得住的**，TestStyleLink_MustNotCarryAttributes 就是那条判据。
// 同时也测出元凶只有 Underline：Bold / Italic / Reverse 都安全。

func TestHyperlink_WidthIgnoresEscapeSequence(t *testing.T) {
	link := hyperlink("https://example.com/some/long/path", "example.com")

	if got, want := lipgloss.Width(link), len("example.com"); got != want {
		t.Errorf("可见宽度 = %d, want %d —— 转义序列被算进宽度了，布局会歪", got, want)
	}
	if !strings.Contains(link, "\x1b]8;;https://example.com/some/long/path\x1b\\") {
		t.Error("超链接的起始序列不对")
	}
	// 结束序列不能省：省了的话它后面所有文字都会被当成同一个链接。
	if !strings.HasSuffix(link, "\x1b]8;;\x1b\\") {
		t.Error("超链接缺少结束序列")
	}
}

func TestHyperlink_EmptyURLFallsBackToPlainText(t *testing.T) {
	if got := hyperlink("", "纯文本"); got != "纯文本" {
		t.Errorf("空 URL 应退回纯文本，got %q", got)
	}
}

// 这条是这组测试里最重要的一条。
//
// lipgloss 没暴露样式属性的读取方法，所以用渲染结果反推：只带前景色时，
// 转义序列恰好是 "\x1b[38;5;45m"。一旦有人给 styleLink 加了属性
// （尤其 Underline），这里就会失败 —— 而失败信息会告诉他为什么。
func TestStyleLink_MustNotCarryAttributes(t *testing.T) {
	// 测试环境不是 TTY，lipgloss 默认不输出颜色，那样这条测试什么都测不到。
	// 强制一个 256 色 profile，让转义序列真的出现。
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	const url = "https://example.com"
	rendered := styleLink.Render(url)

	want := "\x1b[38;5;45m" + url + "\x1b[0m"
	if rendered != want {
		t.Errorf("styleLink 的渲染结果变了。\n got %q\nwant %q\n\n"+
			"如果是因为给它加了属性（比如 .Underline(true)），请撤回：\n"+
			"那会让 bubbletea 逐字符输出 SGR，把 OSC 8 序列切碎，\n"+
			"链接会变成「看着正常但点不动」的静默失效。", rendered, want)
	}
}

// 每个内置服务商都应该有直达链接和官方说明 —— 这是「免配置」承诺的一部分。
//
// ⚠️ OAuth2 服务商走的是另一条路：它**没有**一个"去哪拿凭据"的页面
// （不存在能填进密码栏的东西），要给的是一篇"为什么必须走 OAuth2"的说明。
// 所以判据问的是「有没有把人指到一个真页面去」，而不是「有没有 OpenURL
// 这个字段」—— 后者会把 outlook 那条引导判成红的，而它是刻意没有 OpenURL 的。
func TestProviders_AllHaveLinks(t *testing.T) {
	for _, p := range config.Providers {
		if p.ID == config.CustomProviderID {
			continue // 自定义服务商没有官方页面可指
		}

		var links map[string]string
		if p.Auth == config.AuthOAuth2 {
			links = map[string]string{"OAuthURL": p.OAuthURL, "HelpURL": p.HelpURL}
		} else {
			links = map[string]string{"OpenURL": p.OpenURL, "HelpURL": p.HelpURL}
		}
		if len(p.Guide) == 0 {
			t.Errorf("预设 %q 没有分步说明 —— 链接会失效，步骤不会", p.ID)
		}
		for name, u := range links {
			if u == "" {
				t.Errorf("预设 %q 缺少 %s", p.ID, name)
				continue
			}
			if !strings.HasPrefix(u, "https://") {
				t.Errorf("预设 %q 的 %s 不是 https：%q", p.ID, name, u)
			}
		}
	}
}

// 配置向导必须把链接渲染成 OSC 8 形式。
func TestSetupWizard_ShowsClickableLink(t *testing.T) {
	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]app.Body{},
	}
	m.beginSetup()
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	view := m.View()

	qq := config.FindProvider("qq")
	if qq == nil {
		t.Fatal("找不到 QQ 预设")
	}
	if !strings.Contains(view, qq.OpenURL) {
		t.Errorf("向导里没有显示 QQ 的直达链接 %q:\n%s", qq.OpenURL, view)
	}
	if !strings.Contains(view, "\x1b]8;;"+qq.OpenURL+"\x1b\\") {
		t.Errorf("链接不是可点击的 OSC 8 形式:\n%q", view)
	}
}

// 走到输入授权码那一步，链接仍然要在 —— 那才是用户真正需要它的时候。
func TestSetupWizard_LinkVisibleOnPasswordStep(t *testing.T) {
	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]app.Body{},
	}
	m.beginSetup()
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m, _ = update(m, keyMsg("enter")) // 选 QQ → 邮箱地址
	m.input.SetValue("someone@qq.com")
	m, _ = update(m, keyMsg("enter")) // → 授权码

	if m.setup.step != stepPassword {
		t.Fatalf("step = %v, want stepPassword", m.setup.step)
	}

	got := m.View()
	qq := config.FindProvider("qq")
	if !strings.Contains(got, qq.OpenURL) {
		t.Errorf("授权码这一步没有显示直达链接:\n%s", got)
	}
	if !strings.Contains(got, qq.PasswordLabel) {
		t.Errorf("没有说明密码栏该填什么:\n%s", got)
	}
}
