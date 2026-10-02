package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守的是用户的一句原话：
//
//	「如果消息时间不为当天，则显示日期」
//
// 它看着像件小事，实际是一条**不许说谎**的规矩。上一版消息头写死
// `Format("15:04")`，于是三天前那封信的头上写着 `02:25` —— 读的人会以为
// 它是今天凌晨发的。列表右边那一列看不出问题（位置本来就窄，用户默认
// 那是「最近」）；消息头独立成行，没有任何上下文能纠正它，这个谎就露出来了。
//
// 所以判据不绑具体文案，绑的是**意思**：
//   - 当天的消息头里必须有时分（这是唯一需要时分的场合）；
//   - 非当天的消息头里**不许出现时分**，而且必须能读出「哪一天」。

// wantDateOf 算出「某个时间点在该显示成什么日子」——
// 判据自己造这个值，不写死字符串，跨月/跨年/闰日才不会变成假红。
func wantDateOf(t time.Time, now time.Time) string {
	l := t.Local()
	switch {
	case sameDay(l, now):
		return l.Format("15:04")
	case sameDay(l, now.AddDate(0, 0, -1)):
		return "昨天"
	case l.Year() == now.Year():
		return l.Format("01-02")
	default:
		return l.Format("06-01")
	}
}

// 梯子本身：由近及远降精度，每一档都得落在该落的地方。
//
// ⚠️ 每个用例的时间点都从「今天的某个时刻」推出来（当天中午、昨天中午……），
// 不是从 time.Now() 直接减 —— 直接减的话，凌晨跑测试会跨过午夜落到前一天，
// 判据就变成间歇红的了（间歇红 = 没有信息的判据）。
func TestShortTime_DateLadder(t *testing.T) {
	now := time.Now()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 30, 0, 0, now.Location())

	for _, c := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"当天中午", noon, "12:30"},
		{"当天凌晨", noon.Add(-11*time.Hour - 30*time.Minute), "01:00"},
		{"昨天中午", noon.AddDate(0, 0, -1), "昨天"},
		{"前天中午", noon.AddDate(0, 0, -2), noon.AddDate(0, 0, -2).Format("01-02")},
		{"去年", noon.AddDate(-1, 0, 0), noon.AddDate(-1, 0, 0).Format("06-01")},
		{"零值", time.Time{}, ""},
	} {
		if got := shortTime(c.at); got != c.want {
			t.Errorf("%s：shortTime = %q，want %q", c.name, got, c.want)
		}
	}
}

// 判据的正身：非当天的消息，头上得说清是哪一天。
//
// 直接量**渲染出来的那一行**，而不是读 shortTime 的返回值 —— 后者只能
// 证明那把尺子对，证明不了消息头真的用了它。上一版就是这么漏的：shortTime
// 早就写好了，可 Normal 的消息头里还留着一句写死的 `Format("15:04")`。
func TestMessageHeader_ShowsTheDateWhenNotToday(t *testing.T) {
	forceColor(t)
	now := time.Now()

	for _, c := range []struct {
		name    string
		at      time.Time
		wantNot string // 不该出现的时分
	}{
		{"三天前", now.AddDate(0, 0, -3), now.AddDate(0, 0, -3).Format("15:04")},
		{"上周", now.AddDate(0, 0, -7), now.AddDate(0, 0, -7).Format("15:04")},
		{"去年", now.AddDate(-1, 0, 0), now.AddDate(-1, 0, 0).Format("15:04")},
	} {
		// 挑一个和被测时间不同的小时，否则「出现了时分」和「出现了别的
		// 数字」会撞在一起，判据可能在瞎红。
		if c.at.Format("15:04") == "12:00" {
			t.Fatalf("%s：夹具时间撞上了哨兵值，前提不成立", c.name)
		}

		m, fake := newFeatureModel(t)
		fake.AddMessage("INBOX", mail.Header{
			MessageID: "<old@x>", From: "alice@example.com", FromName: "Alice",
			To: []string{"me@example.com"}, Subject: "很久以前的一封", Date: c.at,
		}, "一句旧话")
		m = syncOnce(t, m)

		msg := thread.Header{
			From: "alice@example.com", FromName: "Alice",
			To: []string{"me@example.com"}, Subject: "很久以前的一封", Date: c.at,
		}
		head := strings.Join(m.renderMessage(msg, 60), "\n")
		plain := plainText(head)

		if strings.Contains(plain, c.wantNot) {
			t.Errorf("%s：消息头里出现了时分 %q —— 读的人会以为它是今天发的\n%s",
				c.name, c.wantNot, plain)
		}

		// 还得**说出日子**：光把时分删掉不算完成任务，用户要知道是哪天。
		want := wantDateOf(c.at, now)
		if want == "12:00" {
			t.Fatalf("%s：期望值撞上了哨兵值", c.name)
		}
		if !strings.Contains(plain, want) {
			t.Errorf("%s：消息头里读不出日子（找 %q）\n%s", c.name, want, plain)
		}
	}
}

// 当天的消息反而**要**时分 —— 显示成「今天」是退步，用户本来就在等它。
//
// 这条和上一条是一对：只有两条都在，才能证明改的是「按远近降精度」，
// 不是「一律不许出现时分」。
func TestMessageHeader_KeepsTheClockForToday(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)

	// ⚠️ 这里原来写的是 `time.Now().Add(-2 * time.Hour)`，于是这条判据
	// **看跑测试的时刻**给答案：00:00–02:00 之间跑，两小时前落进**昨天**，
	// 消息头合法地显示「昨天」，而判据还在找 "22:00" —— 红。间歇红的判据
	// = 没有信息的判据（正是本文件顶部那条规矩，这里曾经自己犯了一次）。
	//
	// 「今天 00:00」整天都落在今天 —— shortTime 比的是**日历日**（见 sameDay），
	// 所以不论几点跑，期望值都钉得住：00:00。
	now := time.Now()
	at := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	msg := thread.Header{
		From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "刚来的", Date: at,
	}
	plain := plainText(strings.Join(m.renderMessage(msg, 60), "\n"))

	if !strings.Contains(plain, "00:00") {
		t.Errorf("当天的消息头应该写时分（找 %q）\n%s", "00:00", plain)
	}
	// 反面更关键：这一条真正要挡的是**反过来的那个回归** —— 「一律显示
	// 日期」。所以「今天」这一档必须在，而且不许退成日子 / 「昨天」。
	//
	// （正面那条断言本身没有分辨力：今天的消息，shortTime 和写死的
	// `Format("15:04")` 给出的是同一个答案。分辨力来自本文件里那两条
	// 非当天的判据 —— 这一条补的是它们的反面。）
	for _, bad := range []string{"昨天", at.Format("01-02")} {
		if strings.Contains(plain, bad) {
			t.Errorf("当天的消息头不该出现 %q —— 远精度那一档漏到近处来了\n%s", bad, plain)
		}
	}
}

// Zen 那边是同一个病，同一个药。
//
// ⚠️ 这条必须单独写：两套头部是两段代码（renderZenItem 自己拼 head），
// 上一版正是只修了其中一处。判据只对着 Normal 写的话，Zen 那边删掉
// shortTime 换回 `Format("15:04")` 也不会有任何一条红。
func TestZenMessageHeader_ShowsTheDateWhenNotToday(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)

	at := time.Now().AddDate(0, 0, -3)
	stampAt := at.Format("15:04")

	got := plainText(strings.Join(m.renderZenItem(zenItem{
		Sender: "Alice", Time: at, Body: "一句旧话", GroupStart: true,
	}, []string{"一句旧话"}, 60), "\n"))

	if strings.Contains(got, stampAt) {
		t.Errorf("Zen 消息头里出现了时分 %q —— 和 Normal 那边一样会读成今天发的\n%s",
			stampAt, got)
	}
	// 期望值走 wantDateOf 而不是写死 `at.Format("01-02")`：跨年那几天
	// （1 月 1–3 日）「三天前」落在去年，shortTime 给的是 "06-01" 那一档，
	// 写死的格式在一年里会假红一次。
	if want := wantDateOf(at, time.Now()); !strings.Contains(got, want) {
		t.Errorf("Zen 消息头里读不出日子（找 %q）\n%s", want, got)
	}
}

// 列表右边那一列和消息头必须是**同一把尺子**。
//
// 分家的症状是「列表说昨天、消息头说 09-28」—— 同一条消息，隔着一列
// 竖线给出两个答案。这条判据把两个渲染结果摆在一起比：同一个时间点、
// 同一条会话，两处都得吐出 shortTime 的那一个答案。
func TestListAndHeader_AgreeOnWhen(t *testing.T) {
	forceColor(t)
	m, fake := newFeatureModel(t)

	at := time.Now().AddDate(0, 0, -5)
	// ⚠️ 用一个**新**对方，否则这封会被并进已有的那条会话里 ——
	// 而会话的 LastDate 是它最新那一条，加一封更旧的进去不会改变任何事，
	// 判据就变成「量了一条日期根本没变过的会话」。
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<agree@x>", From: "dave@example.com", FromName: "Dave",
		To: []string{"me@example.com"}, Subject: "对表用", Date: at,
	}, "正文")
	m = syncOnce(t, m)

	// 列表里那条会话的 LastDate 就是刚加的这一封。
	found := false
	for _, th := range m.visible {
		if th.Subject == "对表用" {
			found = true
		}
	}
	if !found {
		t.Fatalf("前提不成立：列表里没有那条会话（visible=%d 条）", len(m.visible))
	}

	l := m.measureLayout()
	list := plainText(m.renderThreadList(l.listW, l.bodyH))

	msg := thread.Header{
		From: "dave@example.com", FromName: "Dave",
		To: []string{"me@example.com"}, Subject: "对表用", Date: at,
	}
	head := plainText(strings.Join(m.renderMessage(msg, 60), "\n"))

	want := shortTime(at)
	if want == "" {
		t.Fatal("前提不成立：算出来的短标签是空的")
	}
	if !strings.Contains(list, want) {
		t.Errorf("列表右侧没写 %q\n%s", want, list)
	}
	if !strings.Contains(head, want) {
		t.Errorf("消息头没写 %q（两处该用同一把尺子）\n%s", want, head)
	}
}

// ---- 会话里也能用鼠标换会话 ----

// listRowScreenY 给出第 idx 条会话**标题行**在屏幕上的 y 坐标。
//
// 走的是渲染用的那两个函数（listRows + listWindow），和 renderThreadList
// 数行数的方式一致。自己另算一遍 start/capacity 的话，判据就会和渲染
// 分家 —— 而分家的那一刻恰恰是它要抓的那类错。
func listRowScreenY(t *testing.T, m Model, idx int) int {
	t.Helper()
	l := m.measureLayout()
	rows := m.listRows()
	start, end := m.listWindow(rows, l.bodyH)
	for i := start; i < end; i++ {
		if rows[i].first && rows[i].idx == idx {
			// +1 是列表标题那一行 —— 它不属于 rows。
			return l.bodyTop + (i - start) + 1
		}
	}
	t.Fatalf("第 %d 条会话不在可见窗口里（start=%d end=%d）", idx, start, end)
	return 0
}

// clickListRow 在列表上真的点一下第 idx 条会话。
func clickListRow(t *testing.T, m Model, idx int) Model {
	t.Helper()
	l := m.measureLayout()
	y := listRowScreenY(t, m, idx)
	next, _ := update(m, tea.MouseMsg{
		X: l.listX + 2, Y: y,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	return next
}

// 输入框拿着焦点**不等于**鼠标要失效。
//
// 用户的原话：「要求输入框为焦点时，无需 ESC 也可以用鼠标切换会话」。
//
// 改动前 hitList 分支上挂着 `if m.mode != modeList { return m, nil }`，
// 于是会话里点左侧列表被**整个吞掉**。用户看不出是被挡了 —— 只会以为
// 鼠标坏了，于是按 Esc 退出来再点。一个打字的人不该为了换个会话先退出。
//
// ⚠️ 这条判据走完整的鼠标通路（Update → handleMouse → handleClick），
// 不直接调 listIsCurrent：那样测不出「handleClick 忘了调它」——
// 而漏调正是这类守卫最容易出的错（新函数写好了，调用点漏了一处，
// 判据全绿而功能没生效）。
func TestMouse_ClickListSwitchesThreadWhileTyping(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	if len(m.visible) < 2 {
		t.Fatalf("前提不成立：列表里只有 %d 条会话", len(m.visible))
	}

	// 先走真实路径开一条会话，进 modeChat。
	m.cursor = 0
	m, _ = update(m, keyMsg("enter"))
	m = loadBodies(t, m)
	if m.mode != modeChat {
		t.Fatalf("前提不成立：mode=%v，want modeChat", m.mode)
	}
	opened := m.activeID

	// 在输入框里打一半的字 —— 这就是「输入框为焦点」那个状态。
	m.input.SetValue("打了一半的草稿")

	// 点列表里**另一条**会话。
	target := 1
	if m.visible[target].ID == opened {
		target = 0
	}
	if m.visible[target].ID == opened {
		t.Fatal("前提不成立：找不到第二条会话")
	}
	wantID := m.visible[target].ID

	got := clickListRow(t, m, target)

	if got.activeID != wantID {
		t.Errorf("在会话里点列表第 %d 条之后 activeID = %q，want %q —— "+
			"鼠标事件被输入框的焦点吃掉了", target, got.activeID, wantID)
	}
	if got.mode != modeChat {
		t.Errorf("点完之后 mode = %v，want modeChat（还在会话里，只是换了一条）", got.mode)
	}
	// 草稿要留着：点一下比回车「手滑」得多，打了一半的字不该因为点到
	// 别处就没了（见 handleClick 的说明）。
	if v := got.input.Value(); v != "打了一半的草稿" {
		t.Errorf("换会话把草稿吃掉/改掉了：%q", v)
	}
}

// 新建会话那一屏也一样 —— 它和会话屏是同一套版式，输入框同样拿着焦点。
func TestMouse_ClickListSwitchesThreadFromNewChat(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	if len(m.visible) == 0 {
		t.Fatal("前提不成立：列表是空的")
	}

	m, _ = update(m, keyMsg("n"))
	if m.mode != modeNewChat {
		t.Fatalf("前提不成立：mode=%v，want modeNewChat", m.mode)
	}
	m.input.SetValue("someone@example.com")

	got := clickListRow(t, m, 0)

	if got.activeID != m.visible[0].ID {
		t.Errorf("新建会话那一屏点列表没反应：activeID = %q，want %q",
			got.activeID, m.visible[0].ID)
	}
	if got.mode != modeChat {
		t.Errorf("mode = %v，want modeChat", got.mode)
	}
	// newChatTo 是「还没落地的新会话」的收件人。切到已有会话必须清掉，
	// 否则回车会把消息发到那个还没建立起来的新会话去。
	if got.newChatTo != nil {
		t.Errorf("切到已有会话之后 newChatTo 还剩着 %v", got.newChatTo)
	}
}

// 反过来的那一半：模态层**不许**穿透。
//
// 搜索框、确认框、选择器都在最上面一层等一个明确的答复。点穿到背后去
// 切会话，会把那个待答复的问题连人一起扔掉 —— 用户在搜索框里打了字，
// 手滑点到列表，回来发现打的字没了。
func TestMouse_ModalLayersDontLetClicksThrough(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	if len(m.visible) < 2 {
		t.Fatal("前提不成立")
	}

	for _, c := range []struct {
		name string
		open func(Model) Model
	}{
		{"搜索", func(m Model) Model {
			next, _ := update(m, keyMsg("/"))
			return next
		}},
		{"确认框", func(m Model) Model {
			next, _ := update(m, keyMsg("d"))
			return next
		}},
	} {
		base := c.open(m)
		if base.mode == modeList || base.mode == modeChat {
			t.Fatalf("%s：前提不成立，mode=%v", c.name, base.mode)
		}
		before := base.activeID

		got := clickListRow(t, base, 1)

		if got.mode != base.mode {
			t.Errorf("%s：点列表把模态层关掉了（mode %v → %v）", c.name, base.mode, got.mode)
		}
		if got.activeID != before {
			t.Errorf("%s：点列表穿透到了背后，切走了会话（%q → %q）",
				c.name, before, got.activeID)
		}
	}
}

// 从会话里点开文件夹选择器，取消之后要**退回会话**，不是被踢回列表。
//
// 用户的原话是「无需 ESC 也可以用鼠标切换会话」—— 上面那条已经保证了
// 会话里点列表能换会话。但列表的**标题行**同时还是「换文件夹」那个入口，
// 而从会话里点它、按 Esc 取消之后被扔到列表上，等于用一次取消换走了
// 当前会话的视图 —— 用户只是想换个文件夹，没想放弃这个会话。
//
// 这条顺带守着 folderReturn 的取值时机：命令是异步的，赋值必须放在
// **结果到达**的那一步，而且要避开「退回选择器自己」（否则 Esc 看起来
// 完全没反应）。
func TestMouse_FolderPickerReturnsToWhereItOpened(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)

	// 开一条会话，进 modeChat。
	m.cursor = 0
	m, _ = update(m, keyMsg("enter"))
	m = loadBodies(t, m)
	opened := m.activeID

	// 在会话里点列表标题行。
	l := m.measureLayout()
	m, cmd := update(m, tea.MouseMsg{
		X: 0, Y: l.bodyTop, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if cmd == nil {
		t.Fatal("会话里点列表标题行没有去要文件夹列表 —— 选择器打不开")
	}
	m = runCmd(t, m, cmd)
	if m.mode != modeFolder {
		t.Fatalf("前提不成立：mode=%v，want modeFolder", m.mode)
	}

	// Esc 取消。
	m, _ = update(m, keyMsg("esc"))
	if m.mode != modeChat {
		t.Errorf("从会话里打开选择器、按 Esc 之后 mode = %v，want modeChat —— "+
			"用户只是想换文件夹，却被踢出了这个会话", m.mode)
	}
	if m.activeID != opened {
		t.Errorf("取消选择器之后会话变了：%q → %q", opened, m.activeID)
	}

	// 从列表里打开时，Esc 照旧回列表（别把默认那条路改坏了）。
	m, _ = update(m, keyMsg("esc")) // 先退出会话，回到列表
	if m.mode != modeList {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	m, cmd = update(m, tea.MouseMsg{
		X: 0, Y: l.bodyTop, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	m = runCmd(t, m, cmd)
	if m.mode != modeFolder {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	m, _ = update(m, keyMsg("esc"))
	if m.mode != modeList {
		t.Errorf("从列表里打开选择器、按 Esc 之后 mode = %v，want modeList", m.mode)
	}
}

// 命令还没回来的时候用户跑去了别处：取消要退回他**此刻**在的地方。
//
// 「拉文件夹列表」是异步的。点标题行发出了命令、结果还没到，这中间用户
// 完全可以点去别的会话。如果 folderReturn 记的是**发命令那一刻**的 mode，
// 取消就会把他扔回列表 —— 而他明明已经在一个会话里了。
//
// ⚠️ 反过来也守着一件事：赋值不能放在「已经开着选择器」的时候。
// 覆盖成 modeFolder 之后，取消会「退回选择器自己」，屏幕上看起来就是
// Esc 没反应。
func TestMouse_FolderPickerReturnsToWhereTheUserIsNow(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	if len(m.visible) < 2 {
		t.Fatal("前提不成立")
	}

	// 第 1 步：从**列表**上点标题行，发出「拉文件夹列表」的命令。
	l := m.measureLayout()
	m, cmd := update(m, tea.MouseMsg{
		X: 0, Y: l.bodyTop, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if cmd == nil {
		t.Fatal("点标题行没去要文件夹列表")
	}

	// 第 2 步：命令还在路上，用户点去了第 2 条会话。
	m.cursor = 0
	m, _ = update(m, keyMsg("enter")) // 先进 modeChat，才有「跑去别处」可言
	m = clickListRow(t, m, 1)
	if m.mode != modeChat {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	nowOpen := m.activeID

	// 第 3 步：命令的结果到了（foldersResultMsg 会把选择器打开）。
	m = runCmd(t, m, cmd)
	if m.mode != modeFolder {
		t.Fatalf("结果到了之后 mode=%v，want modeFolder", m.mode)
	}

	// 第 4 步：Esc。用户此刻在会话里，就该退回会话。
	m, _ = update(m, keyMsg("esc"))
	if m.mode != modeChat {
		t.Errorf("mode = %v，want modeChat —— 退回的是发命令那一刻的位置，"+
			"不是用户此刻在的地方", m.mode)
	}
	if m.activeID != nowOpen {
		t.Errorf("activeID = %q，want %q", m.activeID, nowOpen)
	}
}

// 列表标题行在会话里也得点得动 —— 顺手补上的那一半。
//
// hitListHeader 这个分支以前**根本没有守卫**，而 hitList 有；一旦有人
// 把守卫加回去（或者加错地方），两者就会不一致。这条判据把「两种模式
// 下点标题行都能开选择器」钉在一起。
func TestMouse_ListHeaderWorksInBothModes(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()

	clickHeader := func(m Model) (Model, tea.Cmd) {
		return update(m, tea.MouseMsg{
			X: 0, Y: l.bodyTop, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		})
	}

	if _, cmd := clickHeader(m); cmd == nil {
		t.Error("在列表上点标题行没反应")
	}

	m.cursor = 0
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeChat {
		t.Fatal("前提不成立")
	}
	if _, cmd := clickHeader(m); cmd == nil {
		t.Error("在会话里点列表标题行没反应 —— 鼠标用户没有了换文件夹的入口")
	}
}
