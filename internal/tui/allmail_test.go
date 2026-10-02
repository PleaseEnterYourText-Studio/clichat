package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
)

// ---- 「接收全部邮件」 ----

// newClampedModel 造一个被「首次同步封数上限」截断过的模型。
//
// 上限压到 2、服务器上放 6 封，于是「打开全量模式」这件事有了可观测的
// 后果：列表里真的会多出 4 行。没有这个落差的话，这个模式是不是真的
// 生效就只能靠读配置项猜。
//
// ⚠️ 六封信刻意来自**六个不同的发件人**。会话是按参与人集合聚合的，
// 同一个人的六封信始终只是一个会话 —— 那样列表行数在开关前后都是 1，
// 这条判据就什么都测不出来了。
func newClampedModel(t *testing.T, total int) (Model, *mail.Fake) {
	t.Helper()

	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0
	cfg.Sync.InitialMaxMessages = 2

	fake := mail.NewFake()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		fake.AddMessage("INBOX", mail.Header{
			MessageID: fmt.Sprintf("<m%d@x>", i),
			From:      fmt.Sprintf("user%d@example.com", i),
			FromName:  fmt.Sprintf("User %d", i),
			To:        []string{"me@example.com"},
			Subject:   fmt.Sprintf("第 %d 封", i),
			Date:      base.Add(time.Duration(i) * time.Minute),
		}, "正文")
	}

	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	m := NewWithApp(cfg, app.New(cfg, fake, idx))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = syncOnce(t, m)
	return m, fake
}

// a 键必须先问一句。打开之后首次同步可能要把服务器上几万封邮件的头部
// 拉一遍，那不是按错键该承担的代价。
func TestAllMail_KeyAsksBeforeTurningOn(t *testing.T) {
	m, _ := newClampedModel(t, 6)

	if m.app.AllMail() {
		t.Fatal("默认不该是全量模式")
	}

	m, cmd := update(m, keyMsg("a"))
	if cmd != nil {
		t.Error("按下 a 只该弹出确认框，不该立刻动手")
	}
	if m.mode != modeConfirm {
		t.Fatalf("a 应该进入确认态, mode=%v", m.mode)
	}
	if m.confirmKind != confirmAllMail {
		t.Fatalf("确认的类型不对: %v", m.confirmKind)
	}

	// 不复用 view 里的原文，查关键词就够 —— 绑死措辞会在改文案那轮变成假红。
	view := m.View()
	for _, want := range []string{"接收全部邮件", "历史"} {
		if !strings.Contains(view, want) {
			t.Errorf("确认框里没提 %q，用户不知道自己在同意什么:\n%s", want, view)
		}
	}
}

// 取消之后必须一动不动：没开关、没拉邮件。
func TestAllMail_CancelKeepsItOff(t *testing.T) {
	m, _ := newClampedModel(t, 6)
	before := len(m.visible)

	m, _ = update(m, keyMsg("a"))
	m, cmd := update(m, keyMsg("n"))

	if cmd != nil {
		t.Error("取消不该产生任何命令")
	}
	if m.mode != modeList {
		t.Errorf("取消后应该回到列表, mode=%v", m.mode)
	}
	if m.app.AllMail() {
		t.Error("取消了却把开关打开了")
	}
	if got := len(m.visible); got != before {
		t.Errorf("取消了却动了数据：%d → %d", before, got)
	}
}

// 确认之后要真的把历史拉下来，而且不用用户再按一次 r。
//
// 这条判据盯的是列表里的行数，不是 AllMail() 的返回值 —— 这个开关
// 最容易做成一个假功能：配置改了、状态栏也显示了，邮件一封没多。
func TestAllMail_ConfirmTurnsOnAndPullsHistory(t *testing.T) {
	m, _ := newClampedModel(t, 6)

	if got := len(m.visible); got != 2 {
		t.Fatalf("前提不成立：默认模式应该只看到 2 封，实际 %d", got)
	}

	m, _ = update(m, keyMsg("a"))
	m, cmd := update(m, keyMsg("y"))
	if cmd == nil {
		t.Fatal("按 y 确认后应该产生一个命令")
	}

	next, follow := update(m, cmd())
	m = next
	if !m.app.AllMail() {
		t.Fatal("确认了却没打开开关")
	}
	if follow == nil {
		t.Error("打开之后应该自动接一轮同步 —— 让用户自己再按 r 会让人怀疑开关没生效")
	}

	// 把自动接的那轮同步跑完。
	m = runCmd(t, m, follow)

	if got := len(m.visible); got != 6 {
		t.Errorf("打开全量模式后列表里应该是 6 封，实际 %d —— 历史没拉上来", got)
	}
	if m.busy {
		t.Error("同步结束了还在转圈")
	}
}

// 再按一次 a 是关掉，而且确认框说的是实话：已经拉下来的邮件留着。
func TestAllMail_SecondPressTurnsOff(t *testing.T) {
	m, _ := newClampedModel(t, 6)

	m, _ = update(m, keyMsg("a"))
	m, cmd := update(m, keyMsg("y"))
	m, follow := update(m, cmd())
	m = runCmd(t, m, follow)
	after := len(m.visible)

	m, _ = update(m, keyMsg("a"))
	if m.mode != modeConfirm {
		t.Fatalf("再按 a 应该又是确认态, mode=%v", m.mode)
	}
	if view := m.View(); !strings.Contains(view, "关闭") {
		t.Errorf("已经是打开状态，确认框却在问要不要打开:\n%s", view)
	}
	if view := m.View(); !strings.Contains(view, "留着") {
		t.Errorf("关掉的确认框该说明已同步的邮件留着:\n%s", view)
	}

	m, cmd = update(m, keyMsg("y"))
	m = runCmd(t, m, cmd)

	if m.app.AllMail() {
		t.Error("第二次确认没把开关关掉")
	}
	if got := len(m.visible); got != after {
		t.Errorf("关掉之后邮件数从 %d 变成了 %d —— 关开关不该删数据", after, got)
	}
}

// statusBarOnly 抹掉 m.status 之后再渲染。
//
// 「接收全部邮件」这几个字有两个来源：状态栏上的模式标记，以及 m.status
// 里的提示（刚打开时的「已打开…」、失败时的「切换…失败」）。不抹掉 status
// 的话，判据分不清自己看到的是哪个 —— 标记去掉之后照样绿，标记留下之后
// 照样红，两种错都测不出来。
func statusBarOnly(m Model) string {
	m.status = ""
	return m.View()
}

// 这个模式会让同步的代价变得很大，所以它必须一直挂在状态栏上。
// 只在确认框里露一次的话，用户按完 y 就再也想不起来自己开过它。
func TestAllMail_ModeIsVisibleInStatusBar(t *testing.T) {
	m, _ := newClampedModel(t, 6)

	if got := statusBarOnly(m); strings.Contains(got, "接收全部邮件") {
		t.Errorf("还没开就已经显示模式标记了:\n%s", got)
	}

	m, _ = update(m, keyMsg("a"))
	m, cmd := update(m, keyMsg("y"))
	m, follow := update(m, cmd())
	m = runCmd(t, m, follow)

	if got := statusBarOnly(m); !strings.Contains(got, "接收全部邮件") {
		t.Errorf("开着全量模式，状态栏却不显示 —— 用户看不到同步为什么变慢:\n%s", got)
	}
}

// 保存失败必须如实报错，而且不能留下一个「开着」的假标记。
func TestAllMail_SaveFailureIsReported(t *testing.T) {
	m, _ := newClampedModel(t, 6)

	// 把配置指到一个「存在但是个目录」的路径：os.WriteFile 必然失败。
	dir := filepath.Join(t.TempDir(), "not-a-file")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	m.cfg.SetPath(dir)

	m, _ = update(m, keyMsg("a"))
	m, cmd := update(m, keyMsg("y"))
	m = runCmd(t, m, cmd)

	if m.app.AllMail() {
		t.Error("保存失败了，开关却停在打开状态")
	}
	if m.status == "" {
		t.Error("保存失败了却什么都没说")
	}
	if m.busy {
		t.Error("失败了还在转圈")
	}
	if got := statusBarOnly(m); strings.Contains(got, "接收全部邮件") {
		t.Errorf("保存没成功，状态栏却挂上了模式标记:\n%s", got)
	}
}

// 帮助页里必须有它，否则用户永远不会知道 a 键存在。
func TestHelp_DocumentsAllMail(t *testing.T) {
	help := helpText()
	if !strings.Contains(help, "接收全部邮件") {
		t.Error("帮助页里没有「接收全部邮件」—— 用户不会知道这个功能存在")
	}
	// 正文会被转成 Markdown 也不是显而易见的，得写出来。
	if !strings.Contains(help, "Markdown") {
		t.Error("帮助页没提正文会被转成 Markdown")
	}
}
