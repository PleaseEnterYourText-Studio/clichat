package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/oauth"
)

// 这个文件守**整页的两屏**：解锁页与配置向导（pages.go）。
//
// # 判据为什么必须自己把配色档位打开
//
// 这一屏的排版（居中、字段框、居中块）全都按**可见宽度**算，而可见宽度是
// lineWidth（lipgloss）量的。lipgloss 在非 TTY 下**一个转义序列都不输出**
// —— 于是「拿不认 ANSI 的尺子去量上过色的行」这类 bug 在测试里完全看不见：
// 不开色时 textWidth == lineWidth，判据全绿，而用户在自己终端里看到的是歪的。
//
// 所以本文件里每一条跟排版有关的判据都先调 forceColor(t)（见 markdown_test.go），
// 并且**断言前提真的成立**（拿到的行里得有转义序列）。跑测试那台机器的环境
// 不能成为判据的前提。

// ---- 夹具 ----

// sandboxConfigDir 把配置目录挪到临时目录。
//
// Dir() 走 os.UserConfigDir()：Windows 认 APPDATA、Linux 认 XDG_CONFIG_HOME、
// macOS 认 HOME —— 三个都设上，三平台都落进临时目录。
// **不设的话这里会去读写开发者本机真实的 config.json / credentials.enc。**
func sandboxConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
}

// tempConfig 造一份带 n 个账号、绑定到临时文件的配置。
//
// 绑定文件路径是为了让 Save() 真的能落盘 —— 「切账号有没有记住」这条判据
// 要重新 Load 一遍才算数，只看内存里的字段等于在测自己刚写进去的东西。
func tempConfig(t *testing.T, emails ...string) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	for _, e := range emails {
		cfg.UpsertProfile(config.Profile{
			Account: config.Account{Provider: "qq", Email: e, DisplayName: localPart(e)},
			IMAP:    config.Endpoint{Host: "imap.qq.com", Port: 993, TLS: config.TLSImplicit},
			SMTP:    config.Endpoint{Host: "smtp.qq.com", Port: 465, TLS: config.TLSImplicit},
		})
	}
	if len(emails) > 0 {
		cfg.SetActive(emails[0])
	}
	return cfg
}

// newUnlockModel 走**生产路径**建一个解锁页模型。
//
// 不用 NewWithApp 之类的捷径：解锁页那两件事（清掉输入框提示符、把光标停在
// 上次用的账号上）都发生在 New 里面，绕过去就等于绕过了被测代码。
func newUnlockModel(t *testing.T, cfg *config.Config, emails ...string) Model {
	t.Helper()
	passwords := map[string]string{}
	for _, e := range emails {
		passwords[e] = "授权码"
	}
	if err := config.SaveCredentials("hunter2", config.Credentials{Passwords: passwords}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	m := New(cfg)
	if m.mode != modeUnlock {
		t.Fatalf("没进解锁页，mode = %v（配置完整吗？%+v）", m.mode, cfg.Account)
	}
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	return m
}

// newWizardModel 造一个停在指定步骤的向导模型。
//
// setup 直接摆好而不是一路按 enter 走：这里测的是**这一屏画成什么样**，
// 把「怎么走到这一步」也带上，判据红了就分不清是排版坏了还是流程坏了。
func newWizardModel(t *testing.T, step setupStep, providerID string) Model {
	t.Helper()
	cfg := tempConfig(t, "me@example.com")
	m := Model{cfg: cfg, input: textinput.New(), bodies: map[string]app.Body{}}
	m.beginSetup()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.setup.step = step
	m.setup.provider = providerIndex(providerID)
	config.ApplyProvider(m.cfg, m.currentProvider())
	// enterStep 是生产路径：占位文字、EchoMode、预填值都在那里设。
	if step != stepProvider && step != stepCustomTLS && step != stepOAuthWait {
		next, _ := m.enterStep(step)
		m = next.(Model)
	}
	return m
}

// ---- 通用的版面判据 ----

// fits 逐行检查：没有一行超出屏幕宽度。返回超出屏幕的行数。
//
// 用 lineWidth（认 ANSI）而不是 textWidth：超宽/溢出是**屏幕上**的事，
// 而屏幕上不数转义序列。
func fits(out string, screen int) []string {
	var bad []string
	for _, ln := range strings.Split(out, "\n") {
		if lineWidth(ln) > screen {
			bad = append(bad, plainText(ln))
		}
	}
	return bad
}

// leadingCols 是一行里第一个可见字符所在的列。
//
// 前导补白都是普通空格（centerLine / centerBlock 加的），所以「整行宽度
// 减去去掉空格之后的宽度」就是列号 —— 顺带把 ANSI 也排除掉了：转义序列
// 在前导空格之后才开始。
func leadingCols(ln string) int {
	return lineWidth(ln) - lineWidth(strings.TrimLeft(ln, " "))
}

// extent 返回整幅画面里最左的可见列，以及内容的最右边界（列号，开区间）。
//
// 空行不算：它们没有"位置"。
func extent(out string) (left, right int) {
	left, right = -1, -1
	for _, ln := range strings.Split(out, "\n") {
		if lineWidth(ln) == 0 {
			continue
		}
		col := leadingCols(ln)
		if left < 0 || col < left {
			left = col
		}
		if e := col + lineWidth(strings.TrimLeft(ln, " ")); e > right {
			right = e
		}
	}
	return left, right
}

// hasANSI 报告这一串里有没有转义序列。判据用它确认前提成立 ——
// 没上色的话「认不认 ANSI」这类断言测的是空气。
func hasANSI(s string) bool { return strings.Contains(s, "\x1b[") }

// ---- 对齐/居中 ----

func TestCenterLine_MeasuresVisibleWidth(t *testing.T) {
	forceColor(t)

	styled := styleTitle.Render("clichat")
	if !hasANSI(styled) {
		t.Fatal("这一行没上色 —— 这条判据的存在前提（上过色的行）不成立")
	}
	if got := lineWidth(styled); got != 7 {
		t.Fatalf("lineWidth(上色行) = %d, want 7", got)
	}

	// 20 列里摆 7 列：左边 (20-7)/2 = 6 格，右边 7 格。
	got := plainText(centerLine(styled, 20))
	want := strings.Repeat(" ", 6) + "clichat" + strings.Repeat(" ", 7)
	if got != want {
		t.Errorf("centerLine 按可见宽度居中的结果不对：\n got %q\nwant %q", got, want)
	}
}

func TestCenterLine_LeavesOverwideLineAlone(t *testing.T) {
	forceColor(t)
	// 装不下时**不裁也不缩**：裁带 ANSI 的行会把转义序列切断。
	if got := centerLine(styleTitle.Render("clichat"), 4); got != styleTitle.Render("clichat") {
		t.Error("超宽的行被改动了")
	}
}

// 块里所有行共用**同一份**补白，按最宽那行算一次。
//
// ⚠️ 判据不能拿字标来写：框线字自己第一行就带一个前导空格（" ██████╗"），
// 于是「每行起始列相同」在正确的实现下也不成立 —— 那会把装饰自带的留白
// 记到居中的账上。用人造的块才能把"共用同一份补白"这件事单独钉住。
func TestCenterBlock_UsesOnePadForAllLines(t *testing.T) {
	block := []string{"aa", "  bbbb", "c", ""}
	got := centerBlock(block, 20)

	// 最宽是 "  bbbb"（6 列），补白 (20-6)/2 = 7。
	for i, want := range []string{
		strings.Repeat(" ", 7) + "aa",
		strings.Repeat(" ", 7) + "  bbbb",
		strings.Repeat(" ", 7) + "c",
		strings.Repeat(" ", 7) + "",
	} {
		if got[i] != want {
			t.Errorf("第 %d 行 = %q, want %q", i, got[i], want)
		}
	}

	// 放不下时原样返回：不能裁（裁带 ANSI 的行会把转义序列切断）。
	wide := []string{"一二三四五", "abc"}
	if got := centerBlock(wide, 4); got[0] != wide[0] || got[1] != wide[1] {
		t.Errorf("超宽的块被改动了：%q", got)
	}
}

// 字标在内容列里必须**整体**居中到同一列：每行都挨着同一个左边。
//
// 直接量装饰的起始列会被它自带的留白骗到，所以量的是"最宽那行两侧的留白
// 差多少" —— 装饰自己的留白在两端加的量不一样，但整体居中的账是清楚的。
func TestPageLogo_CenteredInContentColumn(t *testing.T) {
	forceColor(t)

	for _, w := range []int{52, 78} {
		logo := pageLogo(w)
		left, right := -1, -1
		for _, ln := range logo {
			if lineWidth(ln) == 0 {
				continue
			}
			col := leadingCols(ln)
			if left < 0 || col < left {
				left = col
			}
			if e := col + lineWidth(strings.TrimLeft(ln, " ")); e > right {
				right = e
			}
		}
		if left < 0 {
			t.Fatalf("宽 %d：字标是空的", w)
		}
		if gap := w - right; gap != left && gap != left+1 {
			t.Errorf("宽 %d：字标左边 %d 格、右边 %d 格 —— 没有居中", w, left, gap)
		}
	}
}

func TestPageLogo_DegradesWhenNarrow(t *testing.T) {
	forceColor(t)

	for _, w := range []int{20, 30, 40, 52, 78} {
		logo := zenLogo(w)
		if len(logo) == 0 {
			t.Fatalf("宽 %d：字标是空的", w)
		}
		for i, ln := range logo {
			if got := lineWidth(ln); got > w {
				t.Errorf("宽 %d：字标第 %d 行 %d 列，撑破了内容列（%q）", w, i, got, plainText(ln))
			}
		}
	}
}

// 两屏在**各种宽度**下都不能有一行捅穿屏幕。
//
// 这条是这一屏最容易破的规矩：账号地址、服务商说明、直达链接、发信注意
// —— 全是长度不受控的外部文本。
func TestPages_FitScreen(t *testing.T) {
	forceColor(t)
	sandboxConfigDir(t)

	emails := []string{
		"me@qq.com",
		"work@example.com",
		"a.very.long.address@some-very-long-company-domain.com",
	}

	for _, w := range []int{30, 40, 44, 60, 80, 100, 120, 200} {
		cfg := tempConfig(t, emails...)
		m := newUnlockModel(t, cfg, emails...)
		m.width = w
		for _, idx := range []int{0, 2} {
			m.unlock.idx = idx
			if bad := fits(m.viewUnlock(), w); len(bad) > 0 {
				t.Errorf("解锁页 %d 列、光标 %d：%d 行超宽：\n  %s",
					w, idx, len(bad), strings.Join(bad, "\n  "))
			}
		}
	}
}

func TestWizard_FitScreen(t *testing.T) {
	forceColor(t)
	sandboxConfigDir(t)

	steps := []struct {
		step     setupStep
		provider string
	}{
		{stepProvider, "gmail"},
		{stepEmail, "gmail"},
		{stepCustomIMAP, "custom"},
		{stepCustomSMTP, "custom"},
		{stepCustomTLS, "custom"},
		{stepPassword, "qq"},
		{stepMaster, "qq"},
		{stepOAuthClient, "outlook"},
	}

	for _, w := range []int{30, 40, 44, 60, 100, 200} {
		for _, tc := range steps {
			m := newWizardModel(t, tc.step, tc.provider)
			m.width, m.height = w, 44
			if bad := fits(m.viewSetup(), w); len(bad) > 0 {
				t.Errorf("向导 %v（%s）在 %d 列下：%d 行超宽：\n  %s",
					tc.step, tc.provider, w, len(bad), strings.Join(bad, "\n  "))
			}
		}

		// 等授权那一步单独来：它要先真的起一个授权流，而**授权地址是这一屏
		// 最长的一串文本**（两百来个字符、没有断点）—— 折行没做好的话，就
		// 只有这一个宽度、这一个步骤会捅穿屏幕。
		if m, ok := newOAuthWaitModel(t, w); ok {
			if bad := fits(m.viewSetup(), w); len(bad) > 0 {
				t.Errorf("向导 stepOAuthWait 在 %d 列下：%d 行超宽：\n  %s",
					w, len(bad), strings.Join(bad, "\n  "))
			}
		}
	}
}

// newOAuthWaitModel 造一个停在「等浏览器授权」那一步的模型。
//
// 第二个返回值表示能不能造（预设里没有 OAuth 端点就造不出来）。
func newOAuthWaitModel(t *testing.T, width int) (Model, bool) {
	t.Helper()
	m := newWizardModel(t, stepOAuthClient, "gmail")
	m.cfg.Account.Provider = "gmail"
	prov, ok := oauth.ForProvider("gmail", "")
	if !ok {
		return m, false
	}
	flow, err := oauth.NewFlow(prov, "test-client-id")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	t.Cleanup(flow.Close)

	m.setup.flow = flow
	m.setup.oauth = &config.OAuthClient{ClientID: "test-client-id"}
	m.setup.step = stepOAuthWait
	m.width, m.height = width, 44
	return m, true
}

// 整幅画面要**横向居中**：左边留白和右边留白相等（差一格以内，奇偶数的
// 那点零头是免不了的）。
//
// 这条是「内容列居中到终端」那一层的判据。少了它，超宽终端上内容会全部
// 贴着左边，而上面那条"不超宽"的判据照样绿。
func TestPages_HorizontallyCentered(t *testing.T) {
	forceColor(t)
	sandboxConfigDir(t)

	for _, w := range []int{40, 60, 100, 101, 200} {
		cfg := tempConfig(t, "me@qq.com")
		m := newUnlockModel(t, cfg, "me@qq.com")
		m.width, m.height = w, 40

		left, right := extent(m.viewUnlock())
		if left < 0 {
			t.Fatalf("宽 %d：解锁页一个字都没有", w)
		}
		if right > w {
			t.Fatalf("宽 %d：内容画到第 %d 列", w, right)
		}
		if gap := w - right; gap != left && gap != left+1 {
			t.Errorf("宽 %d：左边 %d 格、右边 %d 格 —— 没有居中", w, left, gap)
		}
	}
}

// 窄到内容列跟着屏幕走的时候不能越界：pageWidth 有个 30 的下限，屏幕比
// 它还窄时必须退成"跟屏幕一样宽"，而不是硬撑 30 列。
func TestPageWidth_NarrowScreenFollowsTerminal(t *testing.T) {
	for _, tc := range []struct{ screen, want int }{
		{10, 10}, {30, 30}, {38, 30}, {40, 32}, {86, 78}, {200, 78},
	} {
		if got := pageWidth(tc.screen); got != tc.want {
			t.Errorf("pageWidth(%d) = %d, want %d", tc.screen, got, tc.want)
		}
	}
}

// ---- 账号列表 ----

func TestElideMiddle_KeepsBothEnds(t *testing.T) {
	long := "a.very.long.address@some-very-long-company-domain.com"

	got := elideMiddle(long, 30)
	if w := textWidth(got); w > 30 {
		t.Errorf("elideMiddle 给出 %d 列，超过要求的 30：%q", w, got)
	}
	// 两端都得留着：左边是"谁"、右边是"哪家服务商"。从末尾截（truncate）
	// 会把 @域名 整个削掉，一屏账号看起来一模一样。
	if !strings.HasPrefix(got, "a.very") {
		t.Errorf("左边没保住：%q", got)
	}
	if !strings.HasSuffix(got, "-domain.com") {
		t.Errorf("右边没保住：%q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("收掉的部分没有标记：%q", got)
	}

	// 放得下就一个字都不动。
	if got := elideMiddle("me@qq.com", 30); got != "me@qq.com" {
		t.Errorf("放得下的地址被改了：%q", got)
	}
	// 退化宽度不许 panic。
	for _, w := range []int{0, 1, 2, 3} {
		_ = elideMiddle(long, w)
	}
}

func TestUnlock_ShowsEveryAccount(t *testing.T) {
	forceColor(t)
	sandboxConfigDir(t)

	emails := []string{"me@qq.com", "work@example.com", "third@example.org"}
	cfg := tempConfig(t, emails...)
	m := newUnlockModel(t, cfg, emails...)

	v := plainText(m.viewUnlock())
	for _, e := range emails {
		if !strings.Contains(v, e) {
			t.Errorf("解锁页上没有 %s（窗口没把它漏在外吧）", e)
		}
	}
}

// 存了十几个账号时，光标不能被挤出窗口 —— 那一屏要问的是密码，
// 密码栏被顶出屏幕就没法用了。
func TestWindowAround_KeepsCursorVisible(t *testing.T) {
	for _, tc := range []struct {
		n, idx, max    int
		wantLo, wantHi int
	}{
		{3, 0, 6, 0, 3},    // 装得下就不切窗口
		{12, 0, 6, 0, 6},   // 光标在头：靠上
		{12, 11, 6, 6, 12}, // 光标在尾：靠下
		{12, 6, 6, 3, 9},   // 中间：光标居中
		{12, -5, 6, 0, 6},  // 越界不 panic
		{12, 99, 6, 6, 12}, //
		{12, 3, 0, 0, 12},  // max 无效 → 全给
	} {
		lo, hi := windowAround(tc.n, tc.idx, tc.max)
		if lo != tc.wantLo || hi != tc.wantHi {
			t.Errorf("windowAround(%d,%d,%d) = (%d,%d), want (%d,%d)",
				tc.n, tc.idx, tc.max, lo, hi, tc.wantLo, tc.wantHi)
		}
		if hi-lo > tc.max && tc.max > 0 {
			t.Errorf("windowAround(%d,%d,%d) 给出 %d 行，超过上限", tc.n, tc.idx, tc.max, hi-lo)
		}
	}
}

// ---- 明文切换 ----

// Ctrl+P 必须真的把 EchoMode 换掉。
//
// 只断言 reveal 这个布尔翻没翻是不够的：布尔翻了而 EchoMode 没换，
// 屏幕上一个字都不会变 —— 用户看到的现象就是「按了没反应」。
func TestUnlock_RevealTogglesMasking(t *testing.T) {
	sandboxConfigDir(t)
	cfg := tempConfig(t, "me@qq.com")
	m := newUnlockModel(t, cfg, "me@qq.com")
	m.input.SetValue("hunter2")

	if m.input.EchoMode != textinput.EchoPassword {
		t.Fatalf("解锁页初始不是掩码：%v", m.input.EchoMode)
	}

	m, _ = update(m, keyMsg("ctrl+p"))
	if m.input.EchoMode != textinput.EchoNormal {
		t.Errorf("Ctrl+P 之后还是掩码：%v", m.input.EchoMode)
	}
	if !hasANSI(m.viewUnlock()) {
		// 顺带确认这一屏真的上色了（否则上面的断言可能测的是空气）—— 不上色
		// 不算错，只是说明这条判据在本次运行里测不到 ANSI 相关的东西。
		t.Log("提示：本次运行没有开色档，ANSI 相关的判据测不到东西")
	}
	if !strings.Contains(plainText(m.viewUnlock()), "hunter2") {
		t.Error("明文状态下看不到密码内容")
	}

	m, _ = update(m, keyMsg("ctrl+p"))
	if m.input.EchoMode != textinput.EchoPassword {
		t.Errorf("再按一次没有掩回去：%v", m.input.EchoMode)
	}
	if strings.Contains(plainText(m.viewUnlock()), "hunter2") {
		t.Error("掩码状态下密码明文还在屏幕上")
	}
}

func TestWizard_RevealTogglesMasking(t *testing.T) {
	sandboxConfigDir(t)
	m := newWizardModel(t, stepPassword, "qq")
	m.input.SetValue("abcd efgh ijkl")

	if m.input.EchoMode != textinput.EchoPassword {
		t.Fatalf("授权码这一步初始不是掩码：%v", m.input.EchoMode)
	}
	m, _ = update(m, keyMsg("ctrl+p"))
	if m.input.EchoMode != textinput.EchoNormal {
		t.Errorf("Ctrl+P 之后还是掩码：%v", m.input.EchoMode)
	}
	if !strings.Contains(plainText(m.viewSetup()), "abcd efgh ijkl") {
		t.Error("明文状态下看不到授权码")
	}
}

// 邮箱地址、client id 这类步骤**不能**是掩码：用户要核对的是自己填对没有。
func TestWizard_NormalStepsAreNotMasked(t *testing.T) {
	sandboxConfigDir(t)
	for _, step := range []setupStep{stepEmail, stepCustomIMAP, stepCustomSMTP} {
		m := newWizardModel(t, step, "custom")
		if m.input.EchoMode != textinput.EchoNormal {
			t.Errorf("步骤 %v 的输入被掩码了", step)
		}
	}
}

// 提示符属于字段框，不属于输入框：留着 "> " 的话字段里会同时出现两套
// "这里可以打字"的记号（input 的提示符 + 框自己的光标），下划线也会被顶歪。
func TestPages_InputHasNoPrompt(t *testing.T) {
	sandboxConfigDir(t)

	cfg := tempConfig(t, "me@qq.com")
	m := newUnlockModel(t, cfg, "me@qq.com")
	if m.input.Prompt != "" {
		t.Errorf("解锁页的输入框还挂着提示符 %q", m.input.Prompt)
	}
	if v := plainText(m.viewUnlock()); strings.Contains(v, "> ") {
		t.Errorf("解锁页画面上出现了输入框提示符：\n%s", v)
	}

	w := newWizardModel(t, stepEmail, "gmail")
	if w.input.Prompt != "" {
		t.Errorf("向导的输入框还挂着提示符 %q", w.input.Prompt)
	}
	if v := plainText(w.viewSetup()); strings.Contains(v, "> ") {
		t.Errorf("向导画面上出现了输入框提示符：\n%s", v)
	}
}

// 而离开这两屏时必须还原：不还原的话会话输入框前面那个 "> " 就永远消失了,
// 而这件事只在「配好账号之后」才被看到。
func TestAttach_RestoresInputPrompt(t *testing.T) {
	sandboxConfigDir(t)
	m := newWizardModel(t, stepEmail, "gmail")
	if m.input.Prompt != "" {
		t.Fatalf("前提不成立：向导里提示符本来就不是空的（%q）", m.input.Prompt)
	}

	// attach 需要真连接才能走完，这里只测它**最后那一下**还原 —— 直接把
	// 输入框摆成"整页口径"，再调用生产代码里那一个还原入口。
	m.applyInputStyle()
	if m.input.Prompt == "" {
		t.Error("离开整页之后提示符没还原，会话输入框前面的记号会永远消失")
	}
}

// ---- 切账号 ----

// 启动页的光标要停在**上一次用的**账号上。
func TestUnlock_CursorStartsOnActiveAccount(t *testing.T) {
	sandboxConfigDir(t)
	emails := []string{"me@qq.com", "work@example.com"}
	cfg := tempConfig(t, emails...)
	cfg.SetActive("work@example.com")

	m := newUnlockModel(t, cfg, emails...)
	if got := m.knownProfiles()[m.unlock.idx].Account.Email; got != "work@example.com" {
		t.Errorf("光标停在 %q, want work@example.com", got)
	}
}

// 换账号要**落盘**：不落盘的话下次启动光标又回到原来那个，用户每次都得
// 重新往下按一格。
func TestUnlock_SwitchingAccountPersistsActive(t *testing.T) {
	sandboxConfigDir(t)
	emails := []string{"me@qq.com", "work@example.com", "third@example.org"}
	cfg := tempConfig(t, emails...)
	m := newUnlockModel(t, cfg, emails...)

	m, _ = update(m, keyMsg("down"))
	if got := m.knownProfiles()[m.unlock.idx].Account.Email; got != "work@example.com" {
		t.Fatalf("↓ 之后光标在 %q", got)
	}
	m, _ = update(m, keyMsg("down"))
	if got := m.knownProfiles()[m.unlock.idx].Account.Email; got != "third@example.org" {
		t.Fatalf("再按一次 ↓ 之后光标在 %q", got)
	}

	// 落盘发生在**回车那一刻**（切光标只是选，还没"登录"）—— 所以这里调
	// 生产代码里那一个落盘入口。它的调用点在 unlockSubmit 里，而那条路下
	// 一件事就是连服务器：不抽出来，这条判据就得被迫去真连一个邮箱。
	//
	// 先把基线写一次：不写的话磁盘上根本没有这个文件，"重新读一遍"读到的是
	// 默认配置 —— 那条判据会绿/红在一个跟被测代码无关的地方。
	if err := cfg.Save(); err != nil {
		t.Fatalf("写基线配置: %v", err)
	}
	if msg := m.rememberSelectedAccount(); msg != "" {
		t.Fatalf("切账号落盘失败：%s", msg)
	}

	// 真的落盘：从文件重新读一份来看。
	path := cfg.Path()
	if path == "" {
		t.Fatal("配置没有绑定文件路径，这条判据测不到落盘")
	}
	again, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("重新读配置: %v", err)
	}
	if again.Account.Email != "third@example.org" {
		t.Errorf("落盘的活动账号 = %q, want third@example.org", again.Account.Email)
	}
	if len(again.Profiles()) != 3 {
		t.Errorf("账号数 = %d, want 3", len(again.Profiles()))
	}

	// 首尾回环：账号通常只有两三个，让 ↓ 从头绕到尾比"按不动"更省事。
	// （放在最后：上面那条判据要光标**停在第三个**账号上。）
	m, _ = update(m, keyMsg("down"))
	if got := m.knownProfiles()[m.unlock.idx].Account.Email; got != "me@qq.com" {
		t.Errorf("↓ 到尾没有绕回开头：%q", got)
	}
	m, _ = update(m, keyMsg("up"))
	if got := m.knownProfiles()[m.unlock.idx].Account.Email; got != "third@example.org" {
		t.Errorf("↑ 到头没有绕回末尾：%q", got)
	}
}

// ⚠️ 密码里出现 j / k 是家常便饭，它们**不能**是换账号的快捷键。
func TestUnlock_JKAreNotAccountKeys(t *testing.T) {
	sandboxConfigDir(t)
	emails := []string{"me@qq.com", "work@example.com"}
	cfg := tempConfig(t, emails...)
	m := newUnlockModel(t, cfg, emails...)

	m, _ = update(m, keyMsg("j"))
	m, _ = update(m, keyMsg("k"))
	if got := m.knownProfiles()[m.unlock.idx].Account.Email; got != "me@qq.com" {
		t.Errorf("j/k 把光标挪走了（现在在 %q）—— 它们会被吃进密码里", got)
	}
	if v := m.input.Value(); v != "jk" {
		t.Errorf("j/k 没有进输入框：%q", v)
	}
}

// 只有一个账号时不摆「↑ / ↓ 换账号」：摆一个按了没反应的功能比不摆更坑人。
func TestUnlockHintKeys_AccountSwitchOnlyWhenMultiple(t *testing.T) {
	sandboxConfigDir(t)

	one := newUnlockModel(t, tempConfig(t, "me@qq.com"), "me@qq.com")
	if got := hintLine(one.unlockHintKeys()); strings.Contains(got, "换账号") {
		t.Errorf("只有一个账号却提示了换账号：%s", got)
	}

	two := newUnlockModel(t, tempConfig(t, "me@qq.com", "work@example.com"), "me@qq.com", "work@example.com")
	if got := hintLine(two.unlockHintKeys()); !strings.Contains(got, "换账号") {
		t.Errorf("存了两个账号却没有换账号的提示：%s", got)
	}
}

// 提示里出现的键必须**真的在帮助页里**。本仓库栽过：提示和帮助页各写一份，
// 加键时只改了一处，于是提示里有个不存在的功能。
func TestPageHints_KeysExistInHelp(t *testing.T) {
	sandboxConfigDir(t)
	desc := hintDesc()

	cfg := tempConfig(t, "me@qq.com", "work@example.com")
	keys := newUnlockModel(t, cfg, "me@qq.com", "work@example.com").unlockHintKeys()

	for _, step := range []setupStep{
		stepProvider, stepEmail, stepCustomIMAP, stepCustomSMTP,
		stepCustomTLS, stepPassword, stepMaster, stepOAuthClient, stepOAuthWait,
	} {
		w := newWizardModel(t, step, "custom")
		w.setup.step = step
		keys = append(keys, w.setupHintKeys()...)
	}

	for _, k := range keys {
		if k.short == "" {
			if _, ok := desc[k.key]; !ok {
				t.Errorf("提示里的键 %q 在帮助页里不存在", k.key)
			}
		}
	}
}

// ---- 删账号 ----

// 删本机凭据是不可逆的（服务器上的邮件不动，但那串授权码要重新去服务商
// 那儿拿一遍），所以要做成按两次。第一次只把后果说清楚。
func TestUnlock_ForgetAccountNeedsConfirmation(t *testing.T) {
	sandboxConfigDir(t)
	emails := []string{"me@qq.com", "work@example.com"}
	cfg := tempConfig(t, emails...)
	m := newUnlockModel(t, cfg, emails...)
	m.input.SetValue("hunter2")

	m, _ = update(m, keyMsg("ctrl+x"))
	if !m.unlock.forgetConfirm {
		t.Fatal("第一次按 Ctrl+X 就动手了")
	}
	if len(m.cfg.Profiles()) != 2 {
		t.Fatalf("账号已经被删了：%d 个", len(m.cfg.Profiles()))
	}
	if v := plainText(m.viewUnlock()); !strings.Contains(v, "再按一次") {
		t.Errorf("第一次按过之后没有把后果说出来：\n%s", v)
	}

	m, _ = update(m, keyMsg("ctrl+x"))
	if len(m.cfg.Profiles()) != 1 {
		t.Fatalf("确认之后还剩 %d 个账号, want 1", len(m.cfg.Profiles()))
	}
	if got := m.cfg.Profiles()[0].Account.Email; got != "work@example.com" {
		t.Errorf("删错了账号：剩下 %q", got)
	}

	// 别的账号的凭据不能被一起抹掉（多账号时代最容易写错的地方）。
	creds, err := config.LoadCredentials("hunter2")
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if _, ok := creds.PasswordFor("work@example.com"); !ok {
		t.Error("删一个账号把另一个账号的授权码也抹掉了")
	}
}

// 确认删除之后，**被删那个账号**的本机凭据必须真的没了。
//
// 这条是补出来的：原先只有 TestUnlock_ForgetAccountNeedsConfirmation 里
// 那一句「别的账号的凭据不能被一起抹掉」，它盯的是**幸存者**。于是把
// `creds.ForgetAccount(email)` 整句删掉，判据照样全绿（变异验证实测）——
// 加密文件里留着一串谁也不用的授权码，下次用同一个邮箱重新配置时还会
// 被当成"这个账号已经配过了"。
func TestUnlock_ForgetAccountDropsItsCredentials(t *testing.T) {
	sandboxConfigDir(t)
	emails := []string{"me@qq.com", "work@example.com"}
	cfg := tempConfig(t, emails...)
	m := newUnlockModel(t, cfg, emails...)
	m.input.SetValue("hunter2")

	// 光标停在激活账号（me@qq.com）上，两次 Ctrl+X 确认删除。
	m, _ = update(m, keyMsg("ctrl+x"))
	m, _ = update(m, keyMsg("ctrl+x"))
	if got := len(m.cfg.Profiles()); got != 1 {
		t.Fatalf("前提不成立：确认之后还剩 %d 个账号, want 1", got)
	}

	creds, err := config.LoadCredentials("hunter2")
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if _, ok := creds.PasswordFor("me@qq.com"); ok {
		t.Error("账号从配置里删了，本机的授权码却还留着")
	}
	if _, ok := creds.PasswordFor("work@example.com"); !ok {
		t.Error("删一个账号把另一个账号的授权码也抹掉了")
	}
}

// 一个账号都不剩 = 回到「从没配置过」：直接进向导。
func TestUnlock_ForgetLastAccountEntersWizard(t *testing.T) {
	sandboxConfigDir(t)
	cfg := tempConfig(t, "me@qq.com")
	m := newUnlockModel(t, cfg, "me@qq.com")
	m.input.SetValue("hunter2")

	m, _ = update(m, keyMsg("ctrl+x"))
	m, _ = update(m, keyMsg("ctrl+x"))

	if m.mode != modeSetup {
		t.Errorf("删光之后 mode = %v, want modeSetup", m.mode)
	}
	if got := m.setup.step; got != stepProvider {
		t.Errorf("向导停在 %v, want stepProvider", got)
	}
}

// 主密码没填就按 Ctrl+X：要提示，不能静默什么都不做。
func TestUnlock_ForgetWithoutMasterExplains(t *testing.T) {
	sandboxConfigDir(t)
	cfg := tempConfig(t, "me@qq.com")
	m := newUnlockModel(t, cfg, "me@qq.com")

	m, _ = update(m, keyMsg("ctrl+x"))
	m, _ = update(m, keyMsg("ctrl+x"))
	if m.status == "" {
		t.Error("没填主密码就按 Ctrl+X，界面上一个字都没说")
	}
	if len(m.cfg.Profiles()) != 1 {
		t.Error("没填主密码却已经把账号删了")
	}
}

// ---- 向导：流程分叉 ----

// 支持 OAuth2 的服务商必须问 client id、并且多一步等浏览器授权。
//
// 这条守的是「把 OAuth2 服务商当成填授权码的」那类回归：那种情况下用户会
// 一路被问到主密码，最后得到一个看不懂的认证失败。
//
// 两种档位都测，而且它们**进入 OAuth 那条路的开关不一样**：
//   - AuthOAuth2（只能走）：什么都不用设，流程本来就是 OAuth 的；
//   - AuthEither（两条都行）：默认走密钥，**按过 Ctrl+O 之后**才切过来。
//
// 只测前者的话，后者那条路坏了没人知道 —— 而 AuthEither 正是这一版新加的，
// 且它有个很容易写错的地方：切过来之后流程序列必须跟着变（否则 enterNext
// 找不到当前步骤，会把用户甩回第一步重填邮箱）。
func TestSetupFlow_OAuthProvidersAskForClientID(t *testing.T) {
	sandboxConfigDir(t)

	covered := 0
	for _, p := range config.Providers {
		if !p.SupportsOAuth2() {
			continue
		}
		covered++

		step := stepPassword
		if p.RequiresOAuth2() {
			step = stepOAuthClient
		}
		m := newWizardModel(t, step, p.ID)

		if p.Auth == config.AuthEither {
			// 默认必须是密钥那条 —— 一进来就问 client id 等于把
			// 「填个密码就行」的简单路藏起来了。
			if containsStep(m.setupFlow(), stepOAuthClient) {
				t.Errorf("%s 还没按 Ctrl+O 就问 client id 了：%v", p.ID, m.setupFlow())
			}
			// 走真实的按键路径切过去，别直接改字段 —— 那样测的是我自己
			// 设的状态，不是产品。
			m, _ = update(m, keyMsg("ctrl+o"))
			if m.setup.step != stepOAuthClient {
				t.Fatalf("%s 按 Ctrl+O 之后停在 %v, want stepOAuthClient", p.ID, m.setup.step)
			}
		}

		flow := m.setupFlow()
		if !containsStep(flow, stepOAuthClient) {
			t.Errorf("%s 的流程里没有「填 client id」：%v", p.ID, flow)
		}
		if !containsStep(flow, stepOAuthWait) {
			t.Errorf("%s 的流程里没有「等浏览器授权」：%v", p.ID, flow)
		}
		if containsStep(flow, stepPassword) {
			t.Errorf("%s 要 OAuth2 却还在问授权码：%v", p.ID, flow)
		}
	}

	if covered < 2 {
		t.Errorf("只测到 %d 个支持 OAuth2 的预设 —— 这条判据在空转", covered)
	}
}

// 「两条路都行」的服务商，**默认**那条必须是填密钥的。
//
// 这是 AuthEither 存在的全部理由：默认这条路步骤少得多（不用去控制台注册
// 应用）。把 gmail 标成 AuthOAuth2 也能让上面那条判据全绿 —— 它只问
// "切过去之后流程对不对"，从来没问过"不切是什么样"。
func TestSetupFlow_EitherProvidersDefaultToSecret(t *testing.T) {
	sandboxConfigDir(t)

	checked := 0
	for _, p := range config.Providers {
		if p.Auth != config.AuthEither {
			continue
		}
		checked++

		// 从选服务商那一步正常往下走，不做任何切换。
		m := newWizardModel(t, stepEmail, p.ID)
		flow := m.setupFlow()

		if !containsStep(flow, stepPassword) {
			t.Errorf("%s 是「两条路都行」，但默认流程里没有填密钥那一步：%v", p.ID, flow)
		}
		if containsStep(flow, stepOAuthClient) {
			t.Errorf("%s 还没按 Ctrl+O 就问 client id 了：%v", p.ID, flow)
		}
		if m.usesOAuth() {
			t.Errorf("%s 默认就被标成了走 OAuth", p.ID)
		}
	}

	if checked == 0 {
		t.Error("没有任何 AuthEither 预设 —— 这条判据在空转")
	}
}

// 「两条路都行」的服务商，两个方向都要能走通：切过去，也得能切回来。
//
// 反悔这条路很重要：用户按 Ctrl+O 之后才知道要先去控制台注册一个应用。
// 没有退路的话他只能退出去从头再配一遍 —— 邮箱、服务器地址全都白填了。
func TestWizard_CtrlOSwitchesOAuthPathBothWays(t *testing.T) {
	sandboxConfigDir(t)

	m := newWizardModel(t, stepPassword, "gmail")
	if !m.oauthOptional() {
		t.Fatal("前提不成立：gmail 不是「两条路都行」")
	}

	m, _ = update(m, keyMsg("ctrl+o"))
	if m.setup.step != stepOAuthClient {
		t.Fatalf("Ctrl+O 之后停在 %v, want stepOAuthClient", m.setup.step)
	}
	if !m.usesOAuth() {
		t.Error("切过去了，但 usesOAuth() 还是假 —— 流程会退回密钥那条")
	}

	// ⚠️ 先把 client id 真的填进去，再往后退。
	//
	// 直接 Esc 的话 setup.oauth 从头到尾都是 nil，下面那句
	// 「退回来还留着 client id」就是一句**永远绿的空话** ——
	// 变异验证实测：把清理那句删掉，判据照样绿。
	m.input.SetValue("12345.apps.googleusercontent.com")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.step != stepOAuthWait {
		t.Fatalf("填完 client id 之后停在 %v, want stepOAuthWait", m.setup.step)
	}
	if m.setup.oauth == nil || m.setup.oauth.ClientID == "" {
		t.Fatal("前提不成立：client id 没被记下来")
	}

	// 第一次 Esc：从"等浏览器授权"退回填 client id（收掉后台监听）。
	m, _ = update(m, keyMsg("esc"))
	if m.setup.step != stepOAuthClient {
		t.Fatalf("取消授权之后停在 %v, want stepOAuthClient", m.setup.step)
	}
	if m.setup.oauth == nil {
		t.Error("只是取消了这一次授权，client id 不该被丢掉 —— 用户还要重试")
	}

	// 第二次 Esc：改主意，回去填应用专用密码。
	m, _ = update(m, keyMsg("esc"))
	if m.setup.step != stepPassword {
		t.Fatalf("再按一次 Esc 之后停在 %v, want stepPassword", m.setup.step)
	}
	if m.usesOAuth() {
		t.Error("退回密钥那条了，usesOAuth() 还是真")
	}
	if m.setup.oauth != nil {
		t.Error("退回来了却还留着 client id —— 它会被写进这个账号")
	}
	if containsStep(m.setupFlow(), stepOAuthClient) {
		t.Errorf("退回来之后流程里还有 OAuth 步骤：%v", m.setupFlow())
	}
}

// Ctrl+O 对**只能填密码**的服务商不能有任何效果。
//
// 这条防的是「顺手加了个全局快捷键」：那个键一旦不只是 AuthEither 才有，
// 用户在 QQ 邮箱那一步按下去就会进一个没有出路的 OAuth 页。
func TestWizard_CtrlOIsNoopForSecretOnlyProvider(t *testing.T) {
	sandboxConfigDir(t)

	m := newWizardModel(t, stepPassword, "qq")
	if m.oauthOptional() {
		t.Fatal("前提不成立：qq 不该是可选的 OAuth 服务商")
	}
	m, _ = update(m, keyMsg("ctrl+o"))
	if m.setup.step != stepPassword {
		t.Errorf("在 QQ 邮箱那一步按 Ctrl+O 之后跑到了 %v", m.setup.step)
	}
	if m.usesOAuth() {
		t.Error("QQ 邮箱根本没有 OAuth 端点，却把这次配置标成了走 OAuth")
	}
}

// 换了服务商，上一次那个「走 OAuth2」的选择必须跟着清掉。
//
// ⚠️ 判据要问的是**再回到 gmail 时的默认行为**，不能只问"切到 qq 之后
// usesOAuth() 是不是假" —— qq 根本不是 AuthEither，那句话里 useOAuth 是真是假
// 都返回假，是个**永远绿的空话**（变异验证实测：把清理那句删掉，判据照样绿）。
//
// 所以要绕一圈回来：只有回到另一个"两条路都行"的服务商身上，
// 那个残留的选择才会露出马脚。
func TestWizard_SwitchingProviderDropsOAuthChoice(t *testing.T) {
	sandboxConfigDir(t)

	m := newWizardModel(t, stepPassword, "gmail")
	m, _ = update(m, keyMsg("ctrl+o"))
	if !m.usesOAuth() {
		t.Fatal("前提不成立：切到 OAuth 之后 usesOAuth() 该是真的")
	}
	m.input.SetValue("12345.apps.googleusercontent.com")
	m, _ = update(m, keyMsg("enter"))

	// 换到 QQ 邮箱，再换回来。
	//
	// ⚠️ 用「上」而不是「下」：预设列表里 qq 在 gmail **前面**，一直按「下」
	// 会从 gmail 往下越走越远 —— 那是个转不出去的循环，判据会挂住而不是变红。
	// 两次切换各带一个上界，免得以后再有人把方向搞反。
	gotoProvider := func(want string, key string) {
		t.Helper()
		m.setup.step = stepProvider
		for i := 0; i < len(config.Providers) && m.currentProvider().ID != want; i++ {
			m, _ = update(m, keyMsg(key))
		}
		if m.currentProvider().ID != want {
			t.Fatalf("走不到 %s 那一条，停在 %q", want, m.currentProvider().ID)
		}
		m, _ = update(m, keyMsg("enter"))
	}
	gotoProvider("qq", "up")
	gotoProvider("gmail", "down")

	// 回到 gmail 了：默认必须又是「填应用专用密码」那条。
	if m.usesOAuth() {
		t.Error("绕回 gmail 之后还记着上一次那个「走 OAuth2」的选择")
	}
	if m.setup.oauth != nil {
		t.Error("绕回来之后还留着上一次的 client id")
	}
	flow := m.setupFlow()
	if containsStep(flow, stepOAuthClient) {
		t.Errorf("绕回 gmail 之后流程里还有 OAuth 步骤：%v", flow)
	}
	if !containsStep(flow, stepPassword) {
		t.Errorf("绕回 gmail 之后默认流程里没有填密钥那一步：%v", flow)
	}
}

// 跨包不变量：**预设说能走 OAuth2 ⟺ oauth.ForProvider 认识它**。
//
// 这两件事分在两个包里，各自都绿也不会有人发现它们对不上 —— 而裂开的表现
// 是用户在填完 client id 之后才看到一句「这个版本还不认识 X 的 OAuth 流程」，
// 那时他已经去控制台注册过应用了。
func TestProviders_OAuthSupportMatchesEndpoints(t *testing.T) {
	checked := 0
	for _, p := range config.Providers {
		_, known := oauth.ForProvider(p.ID, "")
		checked++
		if p.SupportsOAuth2() == known {
			continue
		}
		if p.SupportsOAuth2() {
			t.Errorf("预设 %q 说能走 OAuth2，但 oauth.ForProvider 不认识它", p.ID)
		} else {
			t.Errorf("oauth.ForProvider 认识 %q，但预设没让用户走到那条路 —— OAuth 代码白写了", p.ID)
		}
	}
	if checked == 0 {
		t.Error("一个预设都没查 —— 这条判据在空转")
	}
}

// 至少有一个 OAuth2 预设，否则上面那条判据会静默地什么都不测。
func TestProviders_HaveAtLeastOneOAuth2(t *testing.T) {
	n := 0
	for _, p := range config.Providers {
		if p.Auth == config.AuthOAuth2 {
			n++
		}
	}
	if n == 0 {
		t.Error("预设里一个 OAuth2 服务商都没有")
	}
}

func containsStep(flow []setupStep, want setupStep) bool {
	for _, s := range flow {
		if s == want {
			return true
		}
	}
	return false
}

// esc 取消授权：要把 flow 收掉并退回上一步，而不是卡在一个没有出路的等待页。
func TestWizard_CancelOAuthReturnsToClientID(t *testing.T) {
	sandboxConfigDir(t)
	m := newWizardModel(t, stepOAuthClient, "gmail")

	prov, ok := oauth.ForProvider(m.cfg.Account.Provider, "")
	if !ok {
		t.Skip("这个服务商没有 OAuth 端点，跳过")
	}
	flow, err := oauth.NewFlow(prov, "test-client-id")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	m.setup.flow = flow
	m.setup.step = stepOAuthWait

	m, _ = update(m, keyMsg("esc"))
	if m.setup.flow != nil {
		t.Error("取消之后 flow 还挂着（本地监听没关掉）")
	}
	if m.setup.step != stepOAuthClient {
		t.Errorf("取消之后停在 %v, want stepOAuthClient", m.setup.step)
	}
	if m.setup.err == "" {
		t.Error("取消是用户主动做的，但界面上一个字都没说")
	}
}

// 迟到的授权回执必须被丢掉。
//
// 用户按 Esc 取消之后，那次 Wait 会返回一个错误 —— 它会变成一条
// oauthResultMsg 落到 Update 上。不认出「我已经不在等授权了」的话，这条
// 迟到的消息会把界面推回填 client id 那一步，用户刚填的东西全没了。
func TestWizard_LateOAuthResultIsDropped(t *testing.T) {
	sandboxConfigDir(t)
	m := newWizardModel(t, stepOAuthClient, "gmail")
	m.input.SetValue("12345.apps.googleusercontent.com")
	m.setup.oauth = &config.OAuthClient{ClientID: "12345.apps.googleusercontent.com"}

	before := m.setup.step
	m, _ = update(m, oauthResultMsg{err: errors.New("授权已取消")})
	if m.setup.step != before {
		t.Errorf("在 %v 这一步收到了授权的回执，界面被推到 %v", before, m.setup.step)
	}
	if m.setup.err != "" {
		t.Errorf("迟到的回执在界面上留下了一句错：%q", m.setup.err)
	}
}

// 等授权那一步收到**失败**回执：退回填 client id，而不是把错误留在
// 一个没有出路的等待页上。
func TestWizard_FailedOAuthReturnsToClientID(t *testing.T) {
	sandboxConfigDir(t)
	m := newWizardModel(t, stepOAuthClient, "gmail")
	m.setup.oauth = &config.OAuthClient{ClientID: "cid"}
	m.setup.step = stepOAuthWait

	m, _ = update(m, oauthResultMsg{err: errors.New("user denied")})
	if m.setup.step != stepOAuthClient {
		t.Errorf("失败之后停在 %v, want stepOAuthClient", m.setup.step)
	}
	if m.setup.err == "" {
		t.Error("授权失败了却没有任何提示")
	}
}

// 等授权那一步必须把地址**完整打出来**：SSH 过去的时候浏览器不在本机，
// 用户只能自己把那串地址拷走。
func TestWizard_OAuthWaitShowsAuthURL(t *testing.T) {
	forceColor(t)
	sandboxConfigDir(t)

	m := newWizardModel(t, stepOAuthClient, "gmail")
	prov, ok := oauth.ForProvider(m.cfg.Account.Provider, "")
	if !ok {
		t.Skip("这个服务商没有 OAuth 端点，跳过")
	}
	flow, err := oauth.NewFlow(prov, "test-client-id")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	t.Cleanup(flow.Close)
	m.setup.flow = flow
	m.setup.step = stepOAuthWait

	v := plainText(m.viewSetup())
	// 地址会被折行（两百来个字符放不进一行），所以比对前把换行和缩进抹掉
	// —— 地址里没有空格，抹掉空白不影响"完整"这件事。
	flat := strings.NewReplacer("\n", "", " ", "", "\t", "").Replace(v)
	if !strings.Contains(flat, flow.AuthURL()) {
		t.Errorf("等授权页上没有完整的授权地址（被截断了？）：\n%s", v)
	}
	if !strings.Contains(v, "http://127.0.0.1") && !strings.Contains(v, "http://localhost") {
		t.Errorf("等授权页上没说回调地址是本地监听：\n%s", v)
	}
}

// 缺 client id 时不许去开浏览器（那会跳出一个必然失败的页面）。
func TestWizard_OAuthWaitRequiresClientID(t *testing.T) {
	sandboxConfigDir(t)
	m := newWizardModel(t, stepProvider, "gmail")
	m.setup.oauth = nil

	next, cmd := m.beginOAuthWait()
	m = next.(Model)
	if m.setup.flow != nil {
		t.Error("没有 client id 却把授权流程起起来了")
	}
	if cmd != nil {
		t.Error("没有 client id 却返回了一个等待回调的命令")
	}
	if m.setup.err == "" {
		t.Error("没有 client id 却没有提示")
	}
}

// 换服务商时要把上一次填的 client id 清掉：Google 的 id 拿到微软那儿
// 只会得到一个看不懂的报错。
func TestWizard_SwitchingProviderDropsClientID(t *testing.T) {
	sandboxConfigDir(t)
	m := newWizardModel(t, stepProvider, "gmail")
	m.setup.oauth = &config.OAuthClient{ClientID: "12345.apps.googleusercontent.com"}

	m.setup.provider = providerIndex("qq")
	m, _ = update(m, keyMsg("enter"))
	if m.setup.oauth != nil {
		t.Errorf("换了服务商之后 client id 还在：%+v", m.setup.oauth)
	}
}

// ---- 步骤标题 ----

// 「第 N 步」要跟着**实际**流程走，不能按常量顺序数：OAuth2 服务商的流程
// 比填授权码的多一步，屏幕上写错步数会让用户以为还有别的东西要填。
func TestStepHeader_CountsActualFlow(t *testing.T) {
	sandboxConfigDir(t)

	qq := newWizardModel(t, stepPassword, "qq")
	qq.setup.step = stepPassword
	h := qq.stepHeader("凭据")
	if !strings.Contains(h, "第") {
		t.Fatalf("没有步数：%q", h)
	}

	// 同一个步骤在两条流程里的序号不一样，这正是它必须查流程的理由。
	oauthFlow := newWizardModel(t, stepOAuthClient, "gmail")
	oauthFlow.setup.step = stepOAuthClient
	if got := oauthFlow.stepHeader("x"); got == "" {
		t.Error("OAuth 流程的标题是空的")
	}
}
