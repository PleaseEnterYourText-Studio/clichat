package tui

import (
	"fmt"
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
	// 开机时那条「拉文件夹列表」的命令也要跑一遍。左侧导航里的「已发送 /
	// 已删除」用的是规范名，得靠这份列表解析成服务端上真实的写法；夹具里
	// 少了这一步，导航就会指着一个不存在的文件夹，点进去永远是空的 ——
	// 而这正是启动时该发生的事（见 Model.Init）。
	m = runCmd(t, m, m.foldersCmd(false))
	return m, fake
}

// manyThreadsModel 造一个有 n 条会话的模型。
//
// 默认夹具只有 3 条，测不出「标签页放不下」那一类情况 —— 那才是标签行
// 里真正容易错的地方（窗口往哪边滚、当前标签会不会被滚出屏幕）。
//
// ⚠️ 对方名字**刻意取得长**（「项目经理 0 号」，13 列）。
//
// 不是凑数：标签格子宽 = 名字宽 + 2，而删掉那条常驻导航列之后，最窄的
// 双栏终端（96 列）也有 69 列的正文栏 —— 名字叫 "Peer0" 的话 8 个标签
// 才 63 列，**放得下**，「放不下时窗口往哪滚」那段代码一行都走不到，
// 判据却一直是绿的（正是 TestTabs_WindowFollowsActiveThread 报过的
// 「前提不成立」）。名字长到接近 tabLabelMax 才是有代表性的情况。
func manyThreadsModel(t *testing.T, n int) Model {
	t.Helper()

	cfg := testConfig(t)
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0

	now := time.Now()
	fake := mail.NewFake()
	for i := 0; i < n; i++ {
		// 全部标成 Seen。开着会话会把那条**标成已读**，而已读的会被分区
		// 到列表后半段 —— 一路 Ctrl+↓ 开下去的话，光标每开一个就被底下的
		// 重排撞一下，走出来的是一条乱序且会重复的路线。预置成已读，
		// 列表从头到尾就是一个稳定的组，标签的打开顺序才可预期。
		fake.AddMessage("INBOX", mail.Header{
			MessageID: fmt.Sprintf("<many%d@x>", i),
			From:      fmt.Sprintf("peer%d@example.com", i),
			FromName:  fmt.Sprintf("项目经理 %d 号", i),
			To:        []string{"me@example.com"},
			Subject:   fmt.Sprintf("会话 %d", i),
			Date:      now.Add(time.Duration(-i) * time.Hour),
			Seen:      true,
		}, fmt.Sprintf("第 %d 句", i))
	}

	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	m := NewWithApp(cfg, app.New(cfg, fake, idx))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = syncOnce(t, m)
	m = runCmd(t, m, m.foldersCmd(false))
	return m
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
		// 空间布局那一轮加的东西：不写进帮助页，用户就不知道它们存在。
		//
		// 这里原来要求的是 "侧栏"，而侧栏已经去掉了（文件夹不占一栏，改成
		// 按需弹出）—— 留着它就成了「帮助页里必须提到一个界面上不存在的
		// 东西」，而且会在改掉那句过时文案时变成假红。
		"ctrl+g", "两栏", "标签页", "滚轮",
	}
	for _, want := range required {
		if !strings.Contains(help, strings.ToLower(want)) {
			t.Errorf("帮助页里没有 %q —— 用户不会知道这个功能存在", want)
		}
	}
}

func TestHelp_OpensAndCloses(t *testing.T) {
	m, _ := newFeatureModel(t)
	// 帮助页比一屏长，是分页滚动的 —— 用默认的 30 行去断言「每组都在
	// 画面上」，其实是在赌内容刚好排得下。这一组判据要查的是「分组存在
	// 且渲染出来了」，那就把视口放大到整页装得下，而不是让内容去迁就
	// 视口高度（加一个快捷键就假红一次）。
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 80})

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
//
// ⚠️ 期望的键里原本有「文件夹」（Tab 那个选择器）。它已经从这一行里删掉了：
// 双栏最窄的终端（96 列）减掉侧栏只剩 82 列，而带 Tab 的版本要 90 列 ——
// 末尾那个「q 退出」**从来没显示出来过**。所以这里换成断言「退出」，
// 它正是被截掉的那一个，留着当哨兵。见 help.go 里 hintKeys 的说明。
func TestListMode_ShowsShortcutHints(t *testing.T) {
	m, _ := newFeatureModel(t)
	if m.mode != modeList {
		t.Fatalf("前提不成立, mode=%v", m.mode)
	}

	view := m.View()
	for _, want := range []string{"帮助", "新建", "搜索", "退出"} {
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
	// 判据绑事实不绑文案：断言的是「剩下的是哪个会话」+ 主题渲染出来了，
	// 不去比对标题那行文字（对方名字怎么显示是另一条判据的事）。
	if m.visible[0].ID != "bob@example.com" {
		t.Errorf("过滤出来的不是 bob 那个会话: %q", m.visible[0].ID)
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
	// 选项就是左栏原来那四个固定入口，光标从「全部」起步。
	items := m.folderPickerItems()
	if len(items) != 4 || items[0].folder != "" || items[0].label != "全部" {
		t.Fatalf("选项不对: %+v", items)
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
	//
	// 绑的是**标签**（「收件箱」）而不是服务端真名 INBOX：文件夹那一列被
	// 删掉之后，这一行是用户唯一能确认「我在哪儿」的地方，而 INBOX 这种
	// 机器名对他没有意义。
	if !strings.Contains(m.View(), "收件箱") {
		t.Error("列表标题没显示当前文件夹")
	}
}

// 选择器里的每一项都要带未读计数。
//
// 这是它接替左栏导航之后唯一会丢的东西（那一列上每项右边都有一个数字）。
// 选文件夹时想知道的就是「那边还有几封没读」，丢了的话选择器就退化成
// 一串光秃秃的名字。
func TestFolderPicker_ShowsUnreadCounts(t *testing.T) {
	m, _ := newFeatureModel(t)

	// 夹具里 INBOX 有一个未读；先确认这一点，否则下面的判据是空转的。
	byFolder := map[string]int{}
	for _, it := range m.folderPickerItems() {
		byFolder[it.folder] = it.unread
	}
	if byFolder["INBOX"] == 0 {
		t.Fatalf("前提不成立：夹具里 INBOX 该有一个未读，实际 %v", byFolder)
	}

	m, cmd := update(m, keyMsg("tab"))
	m = runCmd(t, m, cmd)

	// 量**画出来的那几行**，不是结构体里的字段 —— 数字在字段里而没画出来
	// 正是这条判据要挡的东西。
	view := plainText(m.View())
	for _, it := range m.folderPickerItems() {
		if it.unread == 0 {
			continue
		}
		line := ""
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, it.label) {
				line = l
				break
			}
		}
		if line == "" {
			t.Errorf("选择器里找不到 %q 那一行", it.label)
			continue
		}
		if !strings.Contains(line, fmt.Sprint(it.unread)) {
			t.Errorf("%q 那一行没画未读数 %d：%q", it.label, it.unread, line)
		}
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
	target := m.visible[0].ID

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
		if th.ID == target {
			t.Error("被删的会话还在列表里")
		}
	}
}

// 取消删除不能有任何副作用 —— 这是防误按的最后一道闸。
func TestDelete_CancelChangesNothing(t *testing.T) {
	m, _ := newFeatureModel(t)
	before := len(m.visible)
	target := m.visible[0].ID

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
		if th.ID == target {
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
	target := m.visible[0].ID

	if m.visible[0].IsStarred() {
		t.Fatal("前提不成立：一开始不该有星标")
	}

	m, cmd := update(m, keyMsg("*"))
	m = runCmd(t, m, cmd)

	if !m.visible[0].IsStarred() {
		t.Error("星标没生效")
	}
	// 列表上要看得见，否则用户不知道按了有没有用。
	//
	// 两处刻意的地方：绑 starMark 常量而不是写死字符（上一版写死的是 "★"，
	// 而那个字符在 Windows 上会走 emoji 回退、被换成 "*" —— 于是判据成了
	// 假红，见 styles.go 里那条规矩）；以及在**那一行**上找，不在整个 View
	// 上找 —— 底部提示里也印着 "*"，整个 View 上找的话按不按都绿。
	row := listRowOf(t, m, threadTitle(m.visible[0]))
	if !strings.Contains(row, starMark) {
		t.Errorf("星标没画在这条会话的行上：%q", plainText(row))
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

	// 挑一条**已读**的会话来标记：列表按未读排在前面分组，所以「第 0 个」
	// 一定是未读的那条，不再是以前那个「Sent 里最新的一条」。
	// 位置也不能拿来断言（标记完它会被挪走），只认主题。
	idx := -1
	for i, th := range m.visible {
		if th.Unread == 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("前提不成立：列表里一条已读的都没有")
	}
	m.cursor = idx
	victim := m.visible[idx].Subject

	m, cmd := update(m, keyMsg("u"))
	m = runCmd(t, m, cmd)

	got, ok := threadBySubject(m.visible, victim)
	if !ok {
		t.Fatalf("标记未读之后 %q 不见了", victim)
	}
	if got.Unread != 1 {
		t.Errorf("标记未读没生效, %q 的 Unread=%d", victim, got.Unread)
	}
}

// threadBySubject 按主题在列表里找一条会话。
//
// 按主题而不是按下标：列表会随着未读状态重排（未读浮到前面），
// 任何「第 N 条」的断言在这次重排之后量的都是另一条会话。
func threadBySubject(list []thread.Thread, subject string) (thread.Thread, bool) {
	for _, th := range list {
		if th.Subject == subject {
			return th, true
		}
	}
	return thread.Thread{}, false
}

// listRowOf 从**画出来的列表**里挑出含某段文字的那一行（带样式，原样）。
//
// 找的是列表栏自己的渲染结果，不是整幅 View：主题文字在会话流的标题行上
// 也会出现一次，在整幅里找会挑到错的那一行。
func listRowOf(t *testing.T, m Model, want string) string {
	t.Helper()
	for _, ln := range strings.Split(m.renderThreadList(m.listWidth(), m.bodyHeight()), "\n") {
		if strings.Contains(plainText(ln), want) {
			return ln
		}
	}
	t.Fatalf("列表里没有含 %q 的行", want)
	return ""
}

func TestNextUnread_JumpsToUnread(t *testing.T) {
	m, _ := newFeatureModel(t)

	// 从**末尾**出发。未读的分组排在前面，光标初始位置（第 0 条）本来
	// 就是未读的 —— 那样按 ] 只是原地不动，量不出「它会跳」。
	m, _ = update(m, keyMsg("end"))
	if m.visible[m.cursor].Unread != 0 {
		t.Fatalf("前提不成立：末尾应该是已读的，实际 Unread=%d", m.visible[m.cursor].Unread)
	}

	m, _ = update(m, keyMsg("]"))
	if m.visible[m.cursor].Unread == 0 {
		t.Errorf("] 应该跳到未读会话上，实际停在 %q", m.visible[m.cursor].Subject)
	}
	// 到底之后绕回第一组未读，所以该停在最前面那条上。
	if first := firstUnread(m.visible, 0, 1); m.cursor != first {
		t.Errorf("] 绕回后应该停在第一个未读（下标 %d），实际 %d", first, m.cursor)
	}
}

// ---- 过滤算法本身 ----

// mkThread 手搓一个会话。root 同时当 ID 和唯一那条消息的 Message-ID。
func mkThread(root string, folder string, subject string, peers ...string) thread.Thread {
	th := thread.Thread{ID: root, Subject: subject, Peers: peers}
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
	if len(got) != 1 || got[0].ID != "<1>" {
		t.Errorf("不过滤时不该改变内容: %v", got)
	}
}

func TestFirstUnread(t *testing.T) {
	list := []thread.Thread{
		{ID: "<1>"},
		{ID: "<2>", Unread: 1},
		{ID: "<3>"},
		{ID: "<4>", Unread: 2},
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
	allRead := []thread.Thread{{ID: "<1>"}, {ID: "<2>"}}
	if got := firstUnread(allRead, 0, 1); got != -1 {
		t.Errorf("全已读应该返回 -1, got %d", got)
	}
}
