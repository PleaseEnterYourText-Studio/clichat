package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守着设置页（会话列表按 s 进）。
//
// 这一页最贵的失败不是崩溃，是**假开关**：界面能动、屏幕或行为一动不动。
// 所以每一条判据都尽量钉在「用户看得见的结果」上 —— 落盘的配置值、
// 画出来的那一行、真的换了分组。只钉「字段被赋值了」是不够的，那正是
// 假开关也能满足的东西。

// settingsModel 造一个已经打开设置页的模型，并把配置绑到一个临时路径上。
//
// ⚠️ 绑路径是必须的：设置页每改一项都会真的落盘，没绑路径时 Save 直接
// 报错，于是所有「改完提示了什么」的判据都只能看到那句写盘失败。
func settingsModel(t *testing.T) Model {
	t.Helper()
	return settingsModelWith(t, nil)
}

// settingsModelWith 同上，另允许在**构造之前**改配置。
//
// 加账号这类前提必须在构造前就位：行列表、「编辑哪个账号」的选项数
// 都是构造那一刻从 cfg 算的。
func settingsModelWith(t *testing.T, tweak func(*config.Config)) Model {
	t.Helper()
	m, _ := mockModel(t, func(cfg *config.Config) {
		cfg.SetPath(filepath.Join(t.TempDir(), "config.json"))
		if tweak != nil {
			tweak(cfg)
		}
	})
	m, _ = update(m, keyMsg("s"))
	return m
}

// 从列表按 s 进设置页，Esc 回列表；而且屏幕上真的画出了这一页。
//
// 判据走 m.View() 而不是 m.viewSettings()：光看「mode 变成了 modeSettings」
// 会漏掉**分派忘了接**这种错 —— 那是这一页最经典的假开关（状态对了、
// 屏幕上还是原来那一版）。
func TestSettings_OpenFromListAndClose(t *testing.T) {
	m := settingsModel(t)

	if m.mode != modeSettings {
		t.Fatalf("按 s 之后 mode = %v，want modeSettings", m.mode)
	}

	view := plainText(m.View())
	for _, want := range []string{"设置", "身份", "同步", "外观", "账号"} {
		if !strings.Contains(view, want) {
			t.Errorf("设置页上没看到 %q：\n%s", want, view)
		}
	}

	m, _ = update(m, keyMsg("esc"))
	if m.mode != modeList {
		t.Errorf("Esc 之后 mode = %v，want modeList", m.mode)
	}
}

// 走一串按键，每一步之后光标都必须落在**真的存在**的那一行上。
//
// 光标存的是字段名而不是行下标（行列表会随上下文增减），代价就是它可能
// 悬空：屏幕上一行都没高亮，回车和左右键全都毫无反应，用户看到的就是
// 「设置页坏了」。这条判据同时钉住模型里的字段和屏幕上的那个标记 ——
// 两者只要有一个对不上，用户就会改错配置。
func TestSettings_CursorNeverDangles(t *testing.T) {
	m := settingsModelWith(t, func(cfg *config.Config) {
		cfg.Accounts = []config.Profile{{
			Account: config.Account{Email: "a@example.com"},
			// ⚠️ 账号必须**已经有自己那份**，「恢复为全局默认」才不是一个
			// 空按钮。少了这个前提，按下它只会拿到一句「本来就跟全局走」
			// 然后原样返回 —— 那一行不会消失，判据就走了一条没有风险的
			// 路径。（夹具没满足前提时判据也会绿，绿的理由却不是它主张的。）
			Chat: &config.Chat{Nick: "a 自己的"},
		}}
	})

	check := func(step string) {
		t.Helper()
		if _, ok := m.currentSetRow(settingsRows(m)); !ok {
			t.Fatalf("%s 之后光标悬空：字段 %d 不在行列表里", step, m.setCursor)
		}
		// 选中标记只能有一个：画出来两个，说明有两行都以为自己被选中。
		if n := strings.Count(plainText(m.View()), "▸"); n != 1 {
			t.Fatalf("%s 之后画出了 %d 个选中标记（该恰好 1 个）：\n%s",
				step, n, plainText(m.View()))
		}
	}

	press := func(keys ...string) {
		t.Helper()
		for _, k := range keys {
			m, _ = update(m, keyMsg(k))
			check(k)
		}
	}

	press("right")                                // 目标：全局 → a@example.com
	press("down", "down", "down", "down", "down") // 落到「恢复为全局默认」
	press("left")                                 // 它是个按钮，左右键什么都不该做
	press("enter")                                // 按下它 —— 这一行**自己**会从行列表里消失

	// 前提自检：这一按必须真的把账号那份清掉了。没清掉的话，上面那次
	// check 走的是早退分支（「本来就跟全局走」），判据会假绿。
	if m.cfg.OwnChat("a@example.com") {
		t.Fatal("「恢复为全局默认」没生效，判据走的是空路径 —— 夹具前提不成立")
	}

	press("down", "up", "down", "down", "down", "down", "down", "down", "down", "down")
	press("up", "up", "up", "up")
	press("right", "left")
}

// 「恢复为全局默认」只在**真的有事可做**时出现：目标是某个账号，而且那个
// 账号确实有自己的那份。目标已经是全局那份、或者账号本来就跟着全局走，
// 这个按钮按下去什么都不会发生 —— 一个按不出反应的按钮比没有它更让人困惑。
//
// 而「这个账号现在跟谁走」这条信息改由「编辑哪个账号」那一行的值来回答，
// 不能靠「某一行不在」暗示 —— 所以这条判据两件事一起钉。
func TestSettings_FollowGlobalRowFollowsTarget(t *testing.T) {
	m := settingsModelWith(t, func(cfg *config.Config) {
		cfg.Accounts = []config.Profile{
			{Account: config.Account{Email: "follow@example.com"}},
			{Account: config.Account{Email: "own@example.com"}, Chat: &config.Chat{Nick: "自己的"}},
		}
	})

	// （1）目标是全局那份：没什么可恢复的。
	if strings.Contains(plainText(m.viewSettings()), "恢复为全局默认") {
		t.Error("目标是「（全局默认）」时不该出现「恢复为全局默认」")
	}

	// （2）目标是「没有自己那份」的账号：同样没什么可恢复的。
	m.setCursor = fldAccountPick
	m, _ = update(m, keyMsg("right"))
	if m.setTarget != 1 {
		t.Fatalf("按右键之后 setTarget = %d，want 1", m.setTarget)
	}
	page := plainText(m.viewSettings())
	if strings.Contains(page, "恢复为全局默认") {
		t.Error("账号本来就跟着全局走，不该出现「恢复为全局默认」")
	}
	if !strings.Contains(page, "（跟随全局）") {
		t.Errorf("「编辑哪个账号」那一行该写出这个账号跟着全局走：\n%s", page)
	}

	// （3）目标是「有自己那份」的账号：这时候才真的有事可做。
	m, _ = update(m, keyMsg("right"))
	if m.setTarget != 2 {
		t.Fatalf("再按一次之后 setTarget = %d，want 2", m.setTarget)
	}
	page = plainText(m.viewSettings())
	if !strings.Contains(page, "恢复为全局默认") {
		t.Errorf("账号有自己那份时该出现「恢复为全局默认」：\n%s", page)
	}
	if strings.Contains(page, "（跟随全局）") {
		t.Error("有自己那份的账号不该被标成「（跟随全局）」")
	}
}

// 身份是**每账号一份**的：改账号 a 那份不动全局那份，而且改一个字段时
// **整套**实体化下来（没碰的颜色要从全局继承，不能被清空）。
//
// 「整套」这条最容易做成「只写那一个字段」：那样用户在账号里改了昵称，
// 颜色会凭空变成默认色 —— 因为他从没在这个账号里选过颜色，而「没选过」
// 被当成了「要默认色」。
func TestSettings_IdentityIsPerAccount(t *testing.T) {
	m := settingsModelWith(t, func(cfg *config.Config) {
		cfg.Accounts = []config.Profile{{Account: config.Account{Email: "a@example.com"}}}
		cfg.Chat = config.Chat{Nick: "全局的我", NameColor: "#ff0000"}
	})

	m.setCursor = fldAccountPick
	m, _ = update(m, keyMsg("right")) // 目标切到 a@example.com

	m.setCursor = fldNick
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeSettingsEdit {
		t.Fatalf("在昵称上按回车该进编辑态，mode = %v", m.mode)
	}
	m.input.SetValue("工作上的我")
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeSettings {
		t.Fatalf("回车确认之后该回到设置页，mode = %v", m.mode)
	}

	got := m.cfg.ChatFor("a@example.com")
	if got.Nick != "工作上的我" {
		t.Errorf("账号那份的昵称 = %q，want 工作上的我", got.Nick)
	}
	if got.NameColor != "#ff0000" {
		t.Errorf("实体化时颜色丢了：NameColor = %q，want #ff0000（从全局继承）", got.NameColor)
	}
	if m.cfg.Chat.Nick != "全局的我" {
		t.Errorf("改账号那份把全局那份也改了：%q", m.cfg.Chat.Nick)
	}
	if !m.cfg.OwnChat("a@example.com") {
		t.Error("账号该已经有自己那份了")
	}

	// 恢复为全局默认 → 它自己那份被清掉，取值退回全局。
	m.setCursor = fldFollowGlobal
	m, _ = update(m, keyMsg("enter"))

	if m.cfg.OwnChat("a@example.com") {
		t.Error("「恢复为全局默认」之后账号不该还留着自己那份")
	}
	if got := m.cfg.ChatFor("a@example.com"); got.Nick != "全局的我" {
		t.Errorf("恢复之后昵称 = %q，want 全局的我", got.Nick)
	}
}

// 颜色写错时**留在编辑框里**，配置一个字都不改。
//
// 退回列表等于把用户刚敲的那串扔掉，他还得重新找到那一行、重新敲一遍。
// 合法值则要统一成小写再存 —— 收信侧比颜色是大小写不敏感的，而配置文件
// 是给人看的，「#FF8800」和「#ff8800」混在一起会让人以为它们不是一回事。
func TestSettings_ColorRejectsBadValueAndKeepsEditing(t *testing.T) {
	m := settingsModel(t)
	m.setCursor = fldNameColor

	m, _ = update(m, keyMsg("enter"))
	m.input.SetValue("紫色")
	m, _ = update(m, keyMsg("enter"))

	if m.mode != modeSettingsEdit {
		t.Errorf("非法颜色该留在编辑框里，mode = %v", m.mode)
	}
	if m.status == "" {
		t.Error("非法颜色该有一句提示")
	}
	if got := m.cfg.Chat.NameColor; got != "" {
		t.Errorf("非法颜色不该写进配置，得到 %q", got)
	}
	if !strings.Contains(plainText(m.View()), "紫色") {
		t.Error("用户刚敲的那串被丢掉了（屏幕上找不到它）")
	}

	m.input.SetValue("#FF8800")
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeSettings {
		t.Errorf("合法颜色该确认并退回列表，mode = %v", m.mode)
	}
	if got := m.cfg.Chat.NameColor; got != "#ff8800" {
		t.Errorf("NameColor = %q，want #ff8800（统一小写）", got)
	}
}

// 数字项：区间夹住、手填越界不写、而且「没设过」时按左右键的基准是
// **生效值**不是零值。
//
// 最后那条是这里最容易写错的地方：Appearance.ZenWidth 默认是 0（没设过），
// 拿它当基准的话，按一下左键会从 0 减到下限 48 —— 用户看到的是「按一次
// 左键就跳到 48」，而他从没选过 48。
func TestSettings_NumberClampAndFloor(t *testing.T) {
	m := settingsModel(t)
	m.setCursor = fldZenWidth

	// 已经在上限上，再往右不动。
	//
	// ⚠️ 断言的是 ZenMaxWidth 而不是「没超过 100」：夹取写错成「越界就退回
	// 默认值」的话，100 会被顺手改成 78 —— 用户看到自己的设置自己变了。
	m.cfg.Appearance.ZenWidth = config.ZenMaxWidth
	m, _ = update(m, keyMsg("right"))
	if got := m.cfg.Appearance.ZenWidth; got != config.ZenMaxWidth {
		t.Errorf("在上限上按右键，ZenWidth = %d，want %d", got, config.ZenMaxWidth)
	}

	// 手填越界：不写进去，留在编辑框里。
	m, _ = update(m, keyMsg("enter"))
	m.input.SetValue("999")
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeSettingsEdit {
		t.Errorf("越界的数字该留在编辑框里，mode = %v", m.mode)
	}
	if got := m.cfg.Appearance.ZenWidth; got != config.ZenMaxWidth {
		t.Errorf("越界的数字被写进去了：ZenWidth = %d", got)
	}
	m, _ = update(m, keyMsg("esc"))

	// 没设过（0）时，基准是生效值 78，不是 0。
	m.cfg.Appearance.ZenWidth = 0
	m, _ = update(m, keyMsg("left"))
	if want := config.ZenDefaultWidth - 2; m.cfg.Appearance.ZenWidth != want {
		t.Errorf("ZenWidth = %d，want %d（基准该是生效值 %d，不是 0）",
			m.cfg.Appearance.ZenWidth, want, config.ZenDefaultWidth)
	}
}

// 「同人连发拆开」这个开关要真的改到**分组**上，不只是改一个布尔值。
//
// 分组是渲染层的概念（这一条要不要重新标一次名字和时间），所以判据直接量
// zenPresentation 的结果 —— 配置写了、渲染端没读，正是这一页最常见的
// 假开关形态。
func TestSettings_ZenSplitChangesGrouping(t *testing.T) {
	m := settingsModel(t)

	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	th := thread.Thread{
		ID: "<z1>",
		Messages: []thread.Header{
			{MessageID: "<z1>", Folder: "INBOX", From: "alice@example.com",
				FromName: "Alice", Date: base},
			{MessageID: "<z2>", Folder: "INBOX", From: "alice@example.com",
				FromName: "Alice", Date: base.Add(time.Minute)},
		},
	}

	// 关（默认）：同一个人隔一分钟说的两句并成一组，名字只标一次。
	m.cfg.Appearance.ZenSplit = false
	items := m.zenPresentation(th)
	if len(items) != 2 {
		t.Fatalf("条目数 = %d，want 2", len(items))
	}
	if items[1].GroupStart {
		t.Error("同人连发该并成一组：第二条不该另起一组")
	}

	// 开：每条都单独成组。
	m.cfg.Appearance.ZenSplit = true
	items = m.zenPresentation(th)
	for i, it := range items {
		if !it.GroupStart || !it.GroupEnd {
			t.Errorf("拆开模式下第 %d 条该自成一组（start=%v end=%v）",
				i, it.GroupStart, it.GroupEnd)
		}
	}
}

// 启动布局这个开关要在**attach 那一刻**才生效，不能在构造模型时就生效。
//
// View() 的第一道分支是 zenActive()，排在 mode 判断**前面** —— 构造时就
// 置 layout=Zen 的话，解锁页和配置向导会被禅模式首页整个盖掉，用户连主
// 密码都打不了。这条判据就是那个前提：配置里明明选了禅模式，模型造完
// 之后屏幕上也不该是禅模式首页。
func TestSettings_StartupLayoutAppliesOnlyOnAttach(t *testing.T) {
	m, _ := mockModel(t, func(cfg *config.Config) {
		cfg.Appearance.Layout = config.LayoutZen
	})

	if m.zenActive() {
		t.Fatal("还没 attach 就已经在禅模式了：解锁页 / 配置向导会被盖掉")
	}
	// 禅模式首页上有个大字标（实心块 █）。画的是它，就说明已经盖上来了。
	if strings.Contains(plainText(m.View()), "█") {
		t.Errorf("还没 attach 就画出了禅模式首页：\n%s", plainText(m.View()))
	}

	// attach 那一刻才生效，而且提示先亮着 —— 用户没按过任何键，屏幕上
	// 却是个陌生界面，这时候那行提示最有用。
	m.applyStartupLayout()
	if !m.zenActive() {
		t.Fatal("配置选了禅模式，attach 之后该切过去")
	}
	if m.zenScreen != zenHome {
		t.Errorf("zenScreen = %v，want zenHome", m.zenScreen)
	}
	if !m.zenShowHint {
		t.Error("刚进禅模式该先亮一下怎么用的提示")
	}

	// 配置是两栏时不许动。
	m2, _ := mockModel(t, nil)
	m2.applyStartupLayout()
	if m2.zenActive() {
		t.Error("配置是两栏，attach 之后不该切到禅模式")
	}
}

// 写盘失败要**说出来**，而且刚做的选择不能悄悄回滚。
//
// 回滚会让用户看见自己刚改的值又跳回去，而真正的问题（盘写不进去）
// 一点没解决。所以是「留着 + 明确报错」，让他知道下次启动会变回去。
func TestSettings_SaveFailureKeepsValueAndSaysSo(t *testing.T) {
	m, _ := newMockModel(t) // 刻意不绑路径 → Save 必然失败
	m, _ = update(m, keyMsg("s"))

	m.setCursor = fldZenSplit
	before := m.cfg.Appearance.ZenSplit
	m, _ = update(m, keyMsg("enter"))

	if m.cfg.Appearance.ZenSplit == before {
		t.Fatal("开关没生效")
	}
	if !strings.Contains(m.status, "没能存下来") {
		t.Errorf("写盘失败该有明确提示，status = %q", m.status)
	}
	if !strings.Contains(plainText(m.View()), "没能存下来") {
		t.Errorf("提示没画到屏幕上：\n%s", plainText(m.View()))
	}
}
