package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// testConfig 造一份绑在临时目录里的配置。
//
// 路径必须绑上：配置没有「路径为空就退回默认位置」这种兜底了，而界面上
// 有些操作（比如「接收全部邮件」）会把配置落盘 —— 不绑的话要么保存失败，
// 要么写进用户真实的 config.json。
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	return cfg
}

// newFeatureModel 造一个三个会话的模型：两个在 INBOX（一个未读一个已读），
// 一个只在 Sent。用来观察文件夹过滤、搜索和未读跳转。
func newFeatureModel(t *testing.T) (Model, *mail.Fake) {
	t.Helper()

	cfg := testConfig(t)
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0

	now := time.Now()
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "会议安排",
		Date: now.Add(-2 * time.Hour),
	}, "第一句")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<b@x>", From: "bob@example.com", FromName: "Bob",
		To: []string{"me@example.com"}, Subject: "周末爬山",
		Date: now.Add(-time.Hour), Seen: true,
	}, "第二句")
	fake.AddMessage("Sent", mail.Header{
		MessageID: "<c@x>", From: "me@example.com",
		To: []string{"carol@example.com"}, Subject: "发出去的",
		Date: now, Seen: true,
	}, "第三句")

	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	m := NewWithApp(cfg, app.New(cfg, fake, idx))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = syncOnce(t, m)
	return m, fake
}

// runCmd 执行一个 tea.Cmd 并把结果喂回模型。
//
// 测试里必须手工跑命令：tea.Cmd 在真实运行时由 Bubble Tea 的循环调度，
// 测试里没有那个循环。
func runCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("期望产生一个命令，实际是 nil")
	}
	m, _ = update(m, cmd())
	return m
}

// ---- 帮助页 ----

// 帮助页必须覆盖界面上真正支持的按键。
//
// 这条判据针对的是一个真实发生过的问题：新建会话等一批功能早就实现了，
// 但界面上没有任何提示，用户根本不知道它们存在。加了按键却忘了写进
// 帮助页，等于又把它藏回去了。
//
// 查的是关键词而不是整行文案 —— 绑死措辞的话，改一个字就会变成假红。
func TestHelp_DocumentsCoreActions(t *testing.T) {
	help := strings.ToLower(helpText())

	required := []string{
		"新建会话", "搜索", "切换文件夹", "同步",
		"未读", "星标", "删除", "转发", "复制",
		"回车", "esc",
		"ctrl+y", "ctrl+u", "ctrl+t", "ctrl+d",
		"f1",
		"上一个", "下一个",
	}
	for _, want := range required {
		if !strings.Contains(help, strings.ToLower(want)) {
			t.Errorf("帮助页里没有 %q —— 用户不会知道这个功能存在", want)
		}
	}
}

func TestHelp_OpensAndCloses(t *testing.T) {
	m, _ := newFeatureModel(t)

	m, _ = update(m, keyMsg("?"))
	if m.mode != modeHelp {
		t.Fatalf("? 应该打开帮助, mode=%v", m.mode)
	}

	view := m.View()
	for _, want := range []string{"会话列表", "会话内", "通用"} {
		if !strings.Contains(view, want) {
			t.Errorf("帮助页缺分组 %q:\n%s", want, view)
		}
	}

	m, _ = update(m, keyMsg("?"))
	if m.mode != modeList {
		t.Errorf("再按 ? 应该关掉并回列表, mode=%v", m.mode)
	}
}

// 会话内按 F1 开帮助，关掉之后输入框必须拿回焦点 ——
// 不还回去的话光标不闪、字打不进去。
func TestHelp_FromChatRestoresInputFocus(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeChat {
		t.Fatalf("前提不成立, mode=%v", m.mode)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF1})
	if m.mode != modeHelp {
		t.Fatalf("F1 没打开帮助, mode=%v", m.mode)
	}

	m, _ = update(m, keyMsg("esc"))
	if m.mode != modeChat {
		t.Errorf("应该退回会话, mode=%v", m.mode)
	}
	if !m.input.Focused() {
		t.Error("回到会话后输入框没拿回焦点")
	}
}

func TestHelp_LinesFitWidth(t *testing.T) {
	for _, width := range []int{36, 60, 100, 140} {
		m, _ := newFeatureModel(t)
		m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: 24})
		m, _ = update(m, keyMsg("?"))

		for i, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("宽 %d：帮助页第 %d 行宽 %d，超出: %q", width, i, w, line)
			}
		}
	}
}

// ---- 列表底部的常驻提示 ----

// 列表模式下底部必须常驻显示快捷键提示。
//
// 这就是本次改动的起因：功能早就有了，界面上却一个字都不提，
// 用户只能靠猜。判据落在「有没有提示」上，不绑具体文案。
func TestListMode_ShowsShortcutHints(t *testing.T) {
	m, _ := newFeatureModel(t)
	if m.mode != modeList {
		t.Fatalf("前提不成立, mode=%v", m.mode)
	}

	view := m.View()
	for _, want := range []string{"帮助", "新建", "搜索", "文件夹"} {
		if !strings.Contains(view, want) {
			t.Errorf("列表底部提示里没有 %q:\n%s", want, view)
		}
	}
}

// ---- 搜索 ----

func TestSearch_FiltersLiveAndEscClears(t *testing.T) {
	m, _ := newFeatureModel(t)
	all := len(m.visible)
	if all != 3 {
		t.Fatalf("前提不成立：应该有 3 个会话，实际 %d", all)
	}

	m, _ = update(m, keyMsg("/"))
	if m.mode != modeSearch {
		t.Fatalf("/ 应该进搜索, mode=%v", m.mode)
	}

	// 逐字敲进去，每敲一个字母都应该重算一次过滤。
	for _, ch := range []string{"b", "o", "b"} {
		m, _ = update(m, keyMsg(ch))
	}
	if len(m.visible) != 1 {
		t.Fatalf("搜 bob 之后剩 %d 个, want 1", len(m.visible))
	}
	// 判据绑事实不绑文案：列表里渲染的是地址短名（bob），不是显示名（Bob），
	// 所以断言的是「剩下的是哪个会话」+ 主题渲染出来了。
	if m.visible[0].Root != "<b@x>" {
		t.Errorf("过滤出来的不是 bob 那个会话: %q", m.visible[0].Root)
	}
	if !strings.Contains(m.View(), "周末爬山") {
		t.Errorf("过滤结果没渲染出来:\n%s", m.View())
	}

	// Esc 清空搜索词并恢复全部。
	m, _ = update(m, keyMsg("esc"))
	if m.query != "" {
		t.Errorf("Esc 之后 query 应该清空, got %q", m.query)
	}
	if len(m.visible) != all {
		t.Errorf("Esc 之后应该恢复 %d 个, 实际 %d", all, len(m.visible))
	}
}

// 搜索词要能匹配显示名，不只是主题 —— 这个界面里最常见的诉求是
// 「找那个人」。
func TestSearch_MatchesDisplayNameAndAddress(t *testing.T) {
	m, _ := newFeatureModel(t)

	m, _ = update(m, keyMsg("/"))
	m, _ = update(m, keyMsg("c"))
	m, _ = update(m, keyMsg("a"))
	m, _ = update(m, keyMsg("r"))
	m, _ = update(m, keyMsg("o"))
	m, _ = update(m, keyMsg("l"))

	if len(m.visible) != 1 {
		t.Fatalf("搜 carol（只在收件人里出现）之后剩 %d 个, want 1", len(m.visible))
	}
}

// ---- 文件夹切换 ----

func TestFolderPicker_FiltersList(t *testing.T) {
	m, _ := newFeatureModel(t)
	all := len(m.visible)

	m, cmd := update(m, keyMsg("tab"))
	m = runCmd(t, m, cmd)

	if m.mode != modeFolder {
		t.Fatalf("Tab 应该打开文件夹选择器, mode=%v", m.mode)
	}
	// 选项是 ["", INBOX, Sent, Trash]，光标从「全部」起步。
	if got := m.folderChoices(); len(got) != 4 || got[0] != "" {
		t.Fatalf("选项不对: %v", got)
	}

	m, _ = update(m, keyMsg("down"))
	m, _ = update(m, keyMsg("enter"))

	if m.activeFolder != "INBOX" {
		t.Fatalf("activeFolder = %q, want INBOX", m.activeFolder)
	}
	if m.mode != modeList {
		t.Errorf("选完应该回列表, mode=%v", m.mode)
	}
	if len(m.visible) != 2 {
		t.Errorf("INBOX 里应该有 2 个会话, 实际 %d（总共 %d）", len(m.visible), all)
	}
	// 标题上必须写明当前在哪个文件夹，否则用户会以为邮件丢了。
	if !strings.Contains(m.View(), "INBOX") {
		t.Error("列表标题没显示当前文件夹")
	}
}

func TestFolderPicker_LinesFitWidth(t *testing.T) {
	for _, width := range []int{36, 60, 100} {
		m, _ := newFeatureModel(t)
		m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: 20})
		m, cmd := update(m, keyMsg("tab"))
		m = runCmd(t, m, cmd)

		for i, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("宽 %d：文件夹选择器第 %d 行宽 %d，超出: %q", width, i, w, line)
			}
		}
	}
}

// ---- 删除 ----

func TestDelete_ConfirmThenGone(t *testing.T) {
	m, _ := newFeatureModel(t)
	before := len(m.visible)
	target := m.visible[0].Root

	m, _ = update(m, keyMsg("d"))
	if m.mode != modeConfirm {
		t.Fatalf("d 应该进确认框, mode=%v", m.mode)
	}
	if !strings.Contains(m.View(), "删除") {
		t.Error("确认框里没说清在删什么")
	}

	m, cmd := update(m, keyMsg("y"))
	m = runCmd(t, m, cmd)

	if m.mode != modeList {
		t.Errorf("确认之后应该回列表, mode=%v", m.mode)
	}
	if len(m.visible) != before-1 {
		t.Errorf("删除后可见会话 %d, want %d", len(m.visible), before-1)
	}
	for _, th := range m.visible {
		if th.Root == target {
			t.Error("被删的会话还在列表里")
		}
	}
}

// 取消删除不能有任何副作用 —— 这是防误按的最后一道闸。
func TestDelete_CancelChangesNothing(t *testing.T) {
	m, _ := newFeatureModel(t)
	before := len(m.visible)
	target := m.visible[0].Root

	m, _ = update(m, keyMsg("d"))
	m, _ = update(m, keyMsg("n"))

	if m.mode != modeList {
		t.Errorf("取消后应该回列表, mode=%v", m.mode)
	}
	if len(m.visible) != before {
		t.Errorf("取消后会话数变成 %d, want %d", len(m.visible), before)
	}
	found := false
	for _, th := range m.visible {
		if th.Root == target {
			found = true
		}
	}
	if !found {
		t.Error("取消删除把会话弄丢了")
	}
}

// ---- 星标 / 未读 ----

func TestStar_TogglesAndShowsMarker(t *testing.T) {
	m, _ := newFeatureModel(t)
	target := m.visible[0].Root

	if m.visible[0].IsStarred() {
		t.Fatal("前提不成立：一开始不该有星标")
	}

	m, cmd := update(m, keyMsg("*"))
	m = runCmd(t, m, cmd)

	if !m.visible[0].IsStarred() {
		t.Error("星标没生效")
	}
	// 列表上要看得见，否则用户不知道按了有没有用。
	if !strings.Contains(m.View(), "★") {
		t.Error("列表里没有星标标记")
	}

	m, cmd = update(m, keyMsg("*"))
	m = runCmd(t, m, cmd)

	if m.visible[0].IsStarred() {
		t.Error("再按一次应该取消星标")
	}
	_ = target
}

func TestUnread_MarksFromList(t *testing.T) {
	m, _ := newFeatureModel(t)

	// 光标在第 0 个（Sent 里那条，本来就是已读）。
	if m.visible[0].Unread != 0 {
		t.Fatalf("前提不成立：Unread=%d", m.visible[0].Unread)
	}

	m, cmd := update(m, keyMsg("u"))
	m = runCmd(t, m, cmd)

	if m.visible[0].Unread != 1 {
		t.Errorf("标记未读没生效, Unread=%d", m.visible[0].Unread)
	}
}

func TestNextUnread_JumpsToUnread(t *testing.T) {
	m, _ := newFeatureModel(t)

	if m.visible[m.cursor].Unread != 0 {
		t.Fatal("前提不成立：起点应该是已读的")
	}

	m, _ = update(m, keyMsg("]"))
	if m.visible[m.cursor].Unread == 0 {
		t.Errorf("] 应该跳到未读会话上，实际停在 %q", m.visible[m.cursor].Subject)
	}
}

// ---- 过滤算法本身 ----

func mkThread(root string, folder string, subject string, participants ...string) thread.Thread {
	th := thread.Thread{Root: root, Subject: subject, Participants: participants}
	th.Messages = []thread.Header{{MessageID: root, Folder: folder, Subject: subject}}
	return th
}

func TestFilterThreads(t *testing.T) {
	list := []thread.Thread{
		mkThread("<1>", "INBOX", "会议安排", "alice@example.com"),
		mkThread("<2>", "INBOX", "周末爬山", "bob@example.com"),
		mkThread("<3>", "Sent", "发出去的", "carol@example.com"),
	}

	tests := []struct {
		name   string
		folder string
		query  string
		want   int
	}{
		{"都不过滤", "", "", 3},
		{"只按文件夹", "INBOX", "", 2},
		{"只按搜索词", "", "爬山", 1},
		{"文件夹 + 搜索词", "INBOX", "alice", 1},
		{"文件夹和搜索词互斥", "Sent", "alice", 0},
		{"搜索词大小写不敏感", "", "ALICE", 1},
		{"搜不存在的词", "", "zzz", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(filterThreads(list, tc.folder, tc.query)); got != tc.want {
				t.Errorf("过滤出 %d 个, want %d", got, tc.want)
			}
		})
	}
}

// 不过滤时必须原样返回同一个切片 —— 界面靠这个判断"现在没在过滤"。
func TestFilterThreads_NoFilterReturnsInput(t *testing.T) {
	list := []thread.Thread{mkThread("<1>", "INBOX", "x", "a@b.c")}
	got := filterThreads(list, "", "   ")
	if len(got) != 1 || got[0].Root != "<1>" {
		t.Errorf("不过滤时不该改变内容: %v", got)
	}
}

func TestFirstUnread(t *testing.T) {
	list := []thread.Thread{
		{Root: "<1>"},
		{Root: "<2>", Unread: 1},
		{Root: "<3>"},
		{Root: "<4>", Unread: 2},
	}

	if got := firstUnread(list, 0, 1); got != 1 {
		t.Errorf("从 0 往前找 = %d, want 1", got)
	}
	if got := firstUnread(list, 2, 1); got != 3 {
		t.Errorf("从 2 往前找 = %d, want 3", got)
	}
	// 从 2（已读）往回找，应该落在 1（未读）上。
	// 注意不能从 3 起步 —— 3 自己就是未读，那会返回 3，测不出「往回找」。
	if got := firstUnread(list, 2, -1); got != 1 {
		t.Errorf("从 2 往回找 = %d, want 1", got)
	}
	// 起点自己就是未读时应原地命中。
	if got := firstUnread(list, 3, -1); got != 3 {
		t.Errorf("起点即未读时应返回 3, got %d", got)
	}
	// 全已读时返回 -1，让调用方决定要不要绕回。
	allRead := []thread.Thread{{Root: "<1>"}, {Root: "<2>"}}
	if got := firstUnread(allRead, 0, 1); got != -1 {
		t.Errorf("全已读应该返回 -1, got %d", got)
	}
}
