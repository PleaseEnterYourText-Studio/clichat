package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// 这一组守的是用户对配置页提的两件事：
//
//	「登录页面、创建页面优化样式，TUI 元素居中 + 密码可选展示」
//	「配置账号页提供不同服务商的 SMTP 配置 / OAuth 登录引导」
//
// 三块内容：居中的几何、Ctrl+P 这把开关、以及"服务商信息要摆出来、
// 走不通的那条要当场拦住"。

// newSetupScreen 造一个指定窗口尺寸的向导模型。
func newSetupScreen(t *testing.T, w, h int) Model {
	t.Helper()
	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]app.Body{},
	}
	m.beginSetup()
	m, _ = update(m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// blockPadding 返回内容块第一行非空文字左右两侧的空白列数。
//
// 只算**非空行**：补白的整空行左边距是 0（它们只有宽度，没有内容），
// 混进来会把结果拉偏。
//
// 数的是**空格字符个数**，不是字节长度差 —— 行里有中文时字节数和显示
// 宽度不是一回事，好在前后补的都是 ASCII 空格，直接数字符就对了。
func blockPadding(lines []string) (left, right int, ok bool) {
	for _, l := range lines {
		plain := plainText(l)
		if strings.TrimSpace(plain) == "" {
			continue
		}
		return len(plain) - len(strings.TrimLeft(plain, " ")),
			len(plain) - len(strings.TrimRight(plain, " ")),
			true
	}
	return 0, 0, false
}

// firstContentLine 返回第一行非空内容在屏幕上的行号。
func firstContentLine(lines []string) int {
	for i, l := range lines {
		if strings.TrimSpace(plainText(l)) != "" {
			return i
		}
	}
	return -1
}

// 居中不是"看着差不多"，是一条能算出来的性质：左右留白相等（差一列以内
// —— 奇数差没法平分），而且内容块**不贴着屏幕顶端**。
//
// 为什么顶端那条也要管：这两屏和主界面长得一样（左边一栏、右边一栏），
// 贴着左上角时用户会以为界面渲染完了、只是内容还没出来。
func TestSetupScreen_ContentIsCentered(t *testing.T) {
	forceColor(t)

	for _, w := range []int{80, 120} {
		m := newSetupScreen(t, w, 40)
		lines := viewLines(m)

		left, right, ok := blockPadding(lines)
		if !ok {
			t.Fatalf("宽度 %d：整屏都是空的", w)
		}
		if left == 0 {
			t.Errorf("宽度 %d：内容块贴在左边，没有居中（left=%d）", w, left)
		}
		if d := left - right; d > 1 || d < -1 {
			t.Errorf("宽度 %d：左右留白差 %d 列（左 %d / 右 %d）—— 没居中", w, d, left, right)
		}

		if top := firstContentLine(lines); top <= 0 {
			t.Errorf("宽度 %d：内容块贴顶（第一行非空在第 %d 行）—— "+
				"这样和主界面分不出来", w, top)
		}
	}
}

// 换个宽度，留白要跟着变 —— 否则"居中"其实是个写死的缩进。
func TestSetupScreen_CenteringFollowsTheWidth(t *testing.T) {
	forceColor(t)

	nl, _, ok1 := blockPadding(viewLines(newSetupScreen(t, 80, 40)))
	wl, _, ok2 := blockPadding(viewLines(newSetupScreen(t, 140, 40)))
	if !ok1 || !ok2 {
		t.Fatal("前提不成立：整屏都是空的")
	}
	if wl <= nl {
		t.Errorf("宽度 80 时左边距 %d，宽度 140 时 %d —— 留白没跟着变宽", nl, wl)
	}
}

// 解锁屏是同一套版式，同样要居中（它是最先被人看到的一屏）。
func TestUnlockScreen_ContentIsCentered(t *testing.T) {
	forceColor(t)

	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]app.Body{},
	}
	m.mode = modeUnlock
	m.input.Placeholder = "主密码"
	m.input.EchoMode = textinput.EchoPassword
	m.input.Focus()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	lines := viewLines(m)
	left, right, ok := blockPadding(lines)
	if !ok {
		t.Fatal("解锁屏整屏都是空的")
	}
	if left == 0 {
		t.Error("解锁屏内容贴左，没有居中")
	}
	if d := left - right; d > 1 || d < -1 {
		t.Errorf("解锁屏左右留白差 %d 列（左 %d / 右 %d）", d, left, right)
	}
	if top := firstContentLine(lines); top <= 0 {
		t.Errorf("解锁屏内容贴顶（第一行非空在第 %d 行）", top)
	}
}

// ---- 密码可选展示 ----

// Ctrl+P 要真的把字露出来 —— 判据看**渲染结果**，不看 EchoMode。
//
// 只看 EchoMode 是个空壳：它是个字段，赋值对了不代表输入框真的改了渲染
// （上一版消息头就是这么漏的：shortTime 早就写对，可消息头里还留着一句
// 写死的 Format）。所以在屏幕上找那串字符。
func TestSetupPassword_CtrlPRevealsTheSecret(t *testing.T) {
	forceColor(t)
	m := newSetupScreen(t, 120, 40)

	// 走到「凭据」那一步。
	m.setup.provider = providerIndex("qq")
	m, _ = update(m, keyMsg("enter"))
	m.input.SetValue("me@qq.com")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepPassword {
		t.Fatalf("前提不成立：step = %v", m.setup.step)
	}

	const secret = "abcdefgh12345678"
	m.input.SetValue(secret)

	if got := plainText(m.View()); strings.Contains(got, secret) {
		t.Fatal("前提不成立：还没按 Ctrl+P，授权码就已经明文出现在屏幕上了")
	}

	m, _ = update(m, keyMsg("ctrl+p"))
	if got := plainText(m.View()); !strings.Contains(got, secret) {
		t.Errorf("按了 Ctrl+P 之后屏幕上还是没有明文授权码 —— 这个开关是死的\n%s", got)
	}

	m, _ = update(m, keyMsg("ctrl+p"))
	if got := plainText(m.View()); strings.Contains(got, secret) {
		t.Errorf("再按一次 Ctrl+P 应该重新掩上，明文却还在\n%s", got)
	}
}

// 解锁页的主密码同样能看一眼 —— 那是用户唯一没法从别处粘贴的一串字。
func TestUnlockPassword_CtrlPRevealsTheSecret(t *testing.T) {
	forceColor(t)
	m := Model{
		cfg:    config.Default(),
		input:  textinput.New(),
		bodies: map[string]app.Body{},
	}
	m.mode = modeUnlock
	m.input.EchoMode = textinput.EchoPassword
	m.input.Focus()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	const master = "correct-horse"
	m.input.SetValue(master)

	if got := plainText(m.View()); strings.Contains(got, master) {
		t.Fatal("前提不成立：主密码本来就明文显示着")
	}
	m, _ = update(m, keyMsg("ctrl+p"))
	if got := plainText(m.View()); !strings.Contains(got, master) {
		t.Errorf("解锁页按 Ctrl+P 没反应\n%s", got)
	}
}

// 反面：能见人的字段上，Ctrl+P 不许有任何作用。
//
// 邮箱地址这一步填的本来就不是秘密。允许切换的话，这个键在那些步骤上
// 会变成"按了什么都不发生"，而提示行还会挂着一句「Ctrl+P 显示密码」
// 让它看起来像坏了。
func TestSetupPassword_CtrlPDoesNothingOffPasswordSteps(t *testing.T) {
	forceColor(t)
	m := newSetupScreen(t, 120, 40)

	m.setup.provider = providerIndex("qq")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepEmail {
		t.Fatalf("前提不成立：step = %v", m.setup.step)
	}

	m, _ = update(m, keyMsg("ctrl+p"))

	if m.setup.reveal {
		t.Error("邮箱地址这一步不该进入「明文显示」状态")
	}
	if m.input.EchoMode != textinput.EchoNormal {
		t.Errorf("邮箱地址这一步的 EchoMode 被改成了 %v", m.input.EchoMode)
	}
	if got := plainText(m.View()); strings.Contains(got, "Ctrl+P") {
		t.Errorf("非密码步骤不该出现 Ctrl+P 的提示：\n%s", got)
	}
}

// 上一步按过显示，下一步要重新掩上。
//
// "为了核对自己刚打的那一位"而揭开，是那一步的事；把它带到下一步去，
// 等于让一次临时操作永久降低后面的安全性。默认不可见是安全的那一侧。
func TestSetupPassword_RevealResetsOnStepChange(t *testing.T) {
	forceColor(t)
	m := newSetupScreen(t, 120, 40)

	m.setup.provider = providerIndex("qq")
	m, _ = update(m, keyMsg("enter"))
	m.input.SetValue("me@qq.com")
	m, _ = update(m, keyMsg("enter"))

	m, _ = update(m, keyMsg("ctrl+p"))
	if !m.setup.reveal {
		t.Fatal("前提不成立：Ctrl+P 没生效")
	}

	m.input.SetValue("abcdefgh12345678")
	m, _ = update(m, keyMsg("enter")) // → 主密码
	if m.setup.step != stepMaster {
		t.Fatalf("前提不成立：step = %v", m.setup.step)
	}

	if m.setup.reveal {
		t.Error("换了步骤之后「明文显示」还开着 —— 上一屏的一次临时操作被带过来了")
	}
	if m.input.EchoMode != textinput.EchoPassword {
		t.Errorf("主密码这一步的 EchoMode = %v，它该是掩码的", m.input.EchoMode)
	}
}

// ---- 服务商信息与 OAuth 引导 ----

// 配置页要把**将写入配置的服务器**摊开：收、发各一行，各带自己的加密方式。
//
// 这条的分辨力在 iCloud / Outlook 那两格：它们的收发两半加密方式不同。
// 判据同时找两个主机串和两个加密方式串 —— 只写一个「隐式 TLS」的话，
// STARTTLS 那半边会找不到。
func TestSetupScreen_ShowsBothServersWithTheirOwnTLS(t *testing.T) {
	forceColor(t)

	for _, c := range []struct {
		id   string
		want []string
	}{
		{"qq", []string{"imap.qq.com:993", "smtp.qq.com:465", "隐式 TLS"}},
		{"icloud", []string{"imap.mail.me.com:993", "smtp.mail.me.com:587", "隐式 TLS", "STARTTLS"}},
		{"outlook", []string{"outlook.office365.com:993", "smtp-mail.outlook.com:587", "STARTTLS"}},
	} {
		m := newSetupScreen(t, 140, 50)
		idx := providerIndex(c.id)
		if idx < 0 {
			t.Fatalf("预设 %q 不见了", c.id)
		}
		m.setup.provider = idx

		got := plainText(m.View())
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s：配置页上没有 %q\n%s", c.id, w, got)
			}
		}
	}
}

// 自定义那条不显示服务器 —— 那时候还没有服务器可显示。
func TestSetupScreen_CustomProviderShowsNoServerTable(t *testing.T) {
	forceColor(t)
	m := newSetupScreen(t, 140, 50)
	m.setup.provider = providerIndex(config.CustomProviderID)

	if got := plainText(m.View()); strings.Contains(got, "将写入的服务器") {
		t.Errorf("自定义服务商还没填地址，不该显示「将写入的服务器」：\n%s", got)
	}
}

// OAuth2 服务商：**必须当场拦住**，而且理由要摆在屏幕上。
//
// 这是本轮唯一"功能"性质的新增：以前选 Outlook 会一路走到连接失败，
// 用户手上没有任何新信息 —— 只会以为是自己密码抄错了，然后回去把
// 授权码再生成一遍。
func TestSetupScreen_OAuthProviderIsBlockedWithAReason(t *testing.T) {
	forceColor(t)
	m := newSetupScreen(t, 140, 50)

	idx := providerIndex("outlook")
	if idx < 0 {
		t.Fatal("预设里没有 outlook —— OAuth 引导没有落点")
	}
	m.setup.provider = idx

	// 选之前：列表里就该标出来，而不是等按回车被拒才知道。
	if got := plainText(m.View()); !strings.Contains(got, "OAuth") {
		t.Errorf("服务商列表里没标出「需 OAuth2」：\n%s", got)
	}

	m, _ = update(m, keyMsg("enter"))

	if m.setup.step != stepProvider {
		t.Errorf("选了 OAuth2 服务商却往下走了：step = %v（该留在选择那一步）", m.setup.step)
	}
	if m.setup.err == "" {
		t.Error("被拦下时没有给出理由")
	}
	if m.cfg.Account.Provider == "outlook" {
		t.Error("OAuth2 服务商被写进了配置 —— 之后所有连接都会失败，而用户看不出为什么")
	}
	if m.cfg.IMAP.Host != "" {
		t.Error("OAuth2 服务商的地址被写进了配置 —— 配置看起来完整了，实际连不上")
	}

	got := plainText(m.View())
	for _, w := range []string{"OAuth2", "不支持"} {
		if !strings.Contains(got, w) {
			t.Errorf("屏幕上看不到 %q —— 拦住了却不说清楚，用户只会换个服务商再试\n%s", w, got)
		}
	}

	// 换到别的服务商，那条针对 Outlook 的报错要收起来。
	m, _ = update(m, keyMsg("up"))
	if m.setup.err != "" {
		t.Errorf("移到别的服务商之后还挂着上一条报错：%q", m.setup.err)
	}
}

// 反过来：正常服务商照旧一步不差地走下去（别把拦人的门槛修成了拦所有人）。
func TestSetupScreen_SecretProviderStillAdvances(t *testing.T) {
	m := newSetupScreen(t, 120, 40)

	m.setup.provider = providerIndex("icloud")
	m, _ = update(m, keyMsg("enter"))

	if m.setup.step != stepEmail {
		t.Fatalf("选了 iCloud 却没进邮箱地址那一步：step = %v", m.setup.step)
	}
	if m.cfg.IMAP.Host != "imap.mail.me.com" {
		t.Errorf("iCloud 的收信地址没写进配置：%+v", m.cfg.IMAP)
	}
	// 收发两半的加密方式必须各自写对 —— 共用一个隐式 TLS 是当初的 bug。
	if m.cfg.SMTP.Port != 587 || m.cfg.SMTP.TLS != config.TLSStartTLS {
		t.Errorf("iCloud 的发信端点不对：%+v", m.cfg.SMTP)
	}
}
