// 设置页：从会话列表按 s 进入。
//
// # 为什么是这里，而不是启动时的配置向导
//
// 配置向导（setup.go）回答的是「怎么连上邮箱」，那是一次性的、答错了
// 连不上的问题。这里回答的是「连上之后我想怎么用它」—— 昵称、颜色、
// 同步多久一次、界面长什么样。这些以前**只能手改 config.json**：功能
// 早就存在（Chat / Sync / 各种常量），但界面上没有任何入口，于是对
// 用户来说等于不存在。
//
// # 每账号一份 + 全局兜底
//
// 身份那一段的「编辑哪个账号」可以选「（全局默认）」或任意一个账号。
// 选账号时改的是**那个账号自己**那份（`Profile.Chat`），不必先切过去 ——
// 会话中途切账号要断线重连、换索引和正文缓存，那是另一个量级的事，
// 而「给另一个账号改个昵称」完全不该要付那个代价。
//
// 改第一个字段时会把**生效值整套实体化**下来（见 setTargetApply）：覆盖是
// 整套的，不做逐字段合并，理由见 config.Profile.Chat 的注释。
//
// # 一个字段被读了两半会怎样
//
// 这一页写进去的值必须**真的有人读**。外观那三项尤其容易只做一半：
// `zen_width` 在 zen.go 那边读的是 `cfg.ZenWidthLimit()`，不是自己的常量
// （见 zenContentWidth 的注释）；`zen_split` 由 zenPresentation 读；
// `layout` 由 attach → applyStartupLayout 读。少接一处，这一页就变成一个
// 「界面上能动、屏幕上一动不动」的假开关 —— 用户会以为自己被骗了，
// 而他没猜错。
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// setRowKind 决定一行怎么被操作。
type setRowKind uint8

const (
	// setGroup 是分组标题，不可选中。
	setGroup setRowKind = iota
	// setText 回车进编辑，值是任意字符串。
	setText
	// setNumber 回车进编辑，另外 ←/→ 可以直接加减。
	setNumber
	// setToggle 回车或空格翻转。
	setToggle
	// setChoice 回车或 ←/→ 在几个选项之间循环。
	setChoice
	// setAction 回车执行一个动作（不存值）。
	setAction
)

// setField 标识一行读写配置里的哪一项。
//
// 用枚举而不是闭包：Model 在 Update 里是**按值**传的，闭包会捕获那一刻
// 的副本，读出来的是旧值。存字段名、渲染时现从 m.cfg 取，才总是最新的。
type setField int

const (
	fldAccountPick setField = iota
	fldNick
	fldNameColor
	fldTextColor
	fldHideDevice
	fldFollowGlobal
	fldPollSeconds
	fldInitialDays
	fldInitialMax
	fldFolders
	fldLayout
	fldZenWidth
	fldZenSplit
	fldAddAccount
	fldReconfigAccount
)

// setEntry 是行序里的一项：要么是分组标题，要么是一个字段。
type setEntry struct {
	group string
	field setField
}

// setEntries 是设置页的行序。
//
// 顺序按「改完多快能看到效果」排：身份是这一页的重点（也是唯一每账号
// 一份的），同步和外观要等下一轮同步 / 下次启动才显出来，账号那一段
// 基本是入口，放最后。
var setEntries = []setEntry{
	{group: "身份 · clichat 之间互认"},
	{field: fldAccountPick},
	{field: fldNick},
	{field: fldNameColor},
	{field: fldTextColor},
	{field: fldHideDevice},
	{field: fldFollowGlobal},

	{group: "同步 · 全局（所有账号共用）"},
	{field: fldPollSeconds},
	{field: fldInitialDays},
	{field: fldInitialMax},
	{field: fldFolders},

	{group: "外观 · 全局"},
	{field: fldLayout},
	{field: fldZenWidth},
	{field: fldZenSplit},

	{group: "账号"},
	{field: fldAddAccount},
	{field: fldReconfigAccount},
}

// setFieldInfos 是每一行的静态元信息。
//
// hint 会显示在底部说明区，所以写「这一项影响什么、什么时候生效」，
// 而不是复述标签。
var setFieldInfos = map[setField]struct {
	kind  setRowKind
	label string
	hint  string
}{
	fldAccountPick: {setChoice, "编辑哪个账号",
		"身份是**每账号一份**的：选一个账号就改那个账号自己的；选「（全局默认）」改的是没单独设过的账号共用的那份。改哪个账号都不影响当前连着的这个。带「（跟随全局）」的账号就是没有单独设过、用着全局那份。"},
	fldNick: {setText, "昵称",
		"对方在 clichat 里看到的名字。留空则退回这个账号的显示名。"},
	fldNameColor: {setText, "名字颜色",
		"只认 #rgb / #rrggbb / #rrggbbaa 十六进制。留空用默认配色。"},
	fldTextColor: {setText, "文字颜色", "同上，作用于消息正文。"},
	fldHideDevice: {setToggle, "上报设备信息",
		"开：随信带上 系统/架构（例如 windows/amd64），对方看得出你从哪台机器发的。关：不带。"},
	fldFollowGlobal: {setAction, "恢复为全局默认",
		"清掉这个账号自己那份，之后它跟着「（全局默认）」走 —— 那边以后改了，它也跟着改。"},

	fldPollSeconds: {setNumber, "轮询间隔",
		"多少秒去服务器看一眼有没有新邮件。改完从下一轮开始生效。"},
	fldInitialDays: {setNumber, "首次拉取天数",
		"第一次同步往前拉多少天。只影响**还没同步过**的账号，按 a 开「接收全部邮件」时也不生效。"},
	fldInitialMax: {setNumber, "首次拉取封数",
		"第一次同步最多拉多少封。同样只影响还没同步过的账号。"},
	fldFolders: {setText, "同步文件夹",
		"用逗号分隔。收件箱、已发送之类的名字要跟服务端上的一致。改完下一轮同步生效。"},

	fldLayout: {setChoice, "启动布局",
		"启动时用两栏还是禅模式。想现在就看：按 F2。运行中切的那次不写回来。"},
	fldZenWidth: {setNumber, "禅模式宽度",
		"禅模式里正文列的最大宽度。一行太长眼睛回扫会累。太窄的终端上以屏幕为准。"},
	fldZenSplit: {setToggle, "同人连发拆开",
		"关（默认）：同一个人连着说的几句并成一组，名字和时间只标一次。开：每条都单独标。"},

	fldAddAccount: {setAction, "添加账号",
		"走一遍配置向导。填一个新邮箱就是再加一个；填已有邮箱会**重配**那个账号 —— 向导按邮箱认人，加和改是同一个动作。"},
	fldReconfigAccount: {setAction, "重配当前账号",
		"同样走配置向导，会预填这个账号上次填过的东西（比如 OAuth 的 client id）。授权码换了、要重新授权，走这里。"},
}

// settingsRow 是展开后的一行（分组标题 + 元信息）。
type settingsRow struct {
	kind  setRowKind
	label string
	field setField
	hint  string
}

// settingsRows 返回当前该显示的行。
//
// 「恢复为全局默认」只在**真的有事可做**时出现：目标是某个账号，而且那个
// 账号确实有自己的那份。目标已经是全局那份、或者那个账号本来就跟着全局走，
// 这一按什么都不会发生 —— 一个按下去毫无反应、也说不清为什么的按钮，
// 比没有这个按钮更让人困惑。
//
// 「这个账号现在跟谁走」这条信息没有被藏起来：它写在「编辑哪个账号」
// 那一行的值上（见 setTargetLabel）。
func settingsRows(m Model) []settingsRow {
	canReset := false
	if email, ok := m.setTargetEmail(); ok && m.cfg.OwnChat(email) {
		canReset = true
	}

	out := make([]settingsRow, 0, len(setEntries))
	for _, e := range setEntries {
		if e.group != "" {
			out = append(out, settingsRow{kind: setGroup, label: e.group})
			continue
		}
		if e.field == fldFollowGlobal && !canReset {
			continue
		}
		info := setFieldInfos[e.field]
		out = append(out, settingsRow{
			kind:  info.kind,
			label: info.label,
			field: e.field,
			hint:  info.hint,
		})
	}
	return out
}

// ---- 编辑目标（哪个账号） ----

// setTargetCount 是「编辑哪个账号」的选项总数：全局默认 + 每个账号。
func (m Model) setTargetCount() int {
	return 1 + len(m.cfg.Accounts)
}

// setTargetLabel 是选项的文字。
//
// ⚠️ 账号那句要把「它现在跟不跟全局走」写出来。不写的话，用户只能靠
// 「恢复为全局默认」那一行在不在来反推 —— 而按上面那条规矩，那一行只在
// 有事可做时才出现，等于把这条信息藏进了「某一行不存在」里。
func (m Model) setTargetLabel() string {
	if m.setTarget <= 0 || m.setTarget >= m.setTargetCount() {
		return "（全局默认）"
	}
	email := m.cfg.Accounts[m.setTarget-1].Account.Email
	if !m.cfg.OwnChat(email) {
		return email + "（跟随全局）"
	}
	return email
}

// setTargetEmail 返回当前目标的邮箱。全局默认时 ok 为假。
func (m Model) setTargetEmail() (string, bool) {
	if m.setTarget <= 0 || m.setTarget >= m.setTargetCount() {
		return "", false
	}
	return m.cfg.Accounts[m.setTarget-1].Account.Email, true
}

// setTargetChat 返回当前目标**生效**的身份信息。
func (m Model) setTargetChat() config.Chat {
	if email, ok := m.setTargetEmail(); ok {
		return m.cfg.ChatFor(email)
	}
	return m.cfg.Chat
}

// setTargetApply 把一次修改写进当前目标。
//
// ⚠️ 走 SetChatFor 而不是直接改字段，是为了拿到「整套实体化」的语义：
// 账号原先没有自己那份时，先取**生效值**（含全局兜底）改完再整套存下来。
// 少这一步的话，用户在这个账号里改了昵称，颜色却凭空变成空的。
func (m *Model) setTargetApply(fn func(*config.Chat)) {
	if email, ok := m.setTargetEmail(); ok {
		cur := m.cfg.ChatFor(email)
		fn(&cur)
		m.cfg.SetChatFor(email, &cur)
		return
	}
	fn(&m.cfg.Chat)
}

// ---- 取值与显示 ----

// settingsValue 返回一行右侧显示的文字。
func settingsValue(m Model, r settingsRow) string {
	c := m.setTargetChat()
	switch r.field {
	case fldAccountPick:
		return m.setTargetLabel()
	case fldNick:
		if c.Nick == "" {
			return "（用账号显示名）"
		}
		return c.Nick
	case fldNameColor, fldTextColor:
		v := c.NameColor
		if r.field == fldTextColor {
			v = c.TextColor
		}
		if v == "" {
			return "（默认）"
		}
		return v
	case fldHideDevice:
		return onOff(!c.HideDevice)
	case fldFollowGlobal:
		// 这一行只在账号**有自己那份**时才出现（见 settingsRows），
		// 所以这里不必再分情况。
		return "现在：本账号单独设"
	case fldPollSeconds:
		return strconv.Itoa(m.cfg.Sync.PollIntervalSeconds) + " 秒"
	case fldInitialDays:
		return strconv.Itoa(m.cfg.Sync.InitialDays) + " 天"
	case fldInitialMax:
		return strconv.Itoa(m.cfg.Sync.InitialMaxMessages) + " 封"
	case fldFolders:
		return strings.Join(m.cfg.Sync.Folders, ", ")
	case fldLayout:
		if m.cfg.StartupLayout() == config.LayoutZen {
			return "禅模式"
		}
		return "两栏"
	case fldZenWidth:
		return strconv.Itoa(m.cfg.ZenWidthLimit()) + " 列"
	case fldZenSplit:
		return onOff(m.cfg.Appearance.ZenSplit)
	}
	return ""
}

func onOff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

// ---- 光标 ----

// currentSetRow 返回光标所在的行。找不到返回 false。
func (m Model) currentSetRow(rows []settingsRow) (settingsRow, bool) {
	for _, r := range rows {
		if r.kind != setGroup && r.field == m.setCursor {
			return r, true
		}
	}
	return settingsRow{}, false
}

// normalizeSetCursor 保证光标落在**这一版行列表里真的存在**的那一行上。
//
// 行列表会随上下文变（换个编辑目标，「恢复为全局默认」那一行就没了），
// 而光标存的是字段名 —— 目标和字段对不上时它就会「悬空」：屏幕上一行都
// 没高亮，回车、左右键全都毫无反应，看着就是「设置页坏了」。
func (m *Model) normalizeSetCursor(rows []settingsRow) {
	if _, ok := m.currentSetRow(rows); ok {
		return
	}
	for _, r := range rows {
		if r.kind == setGroup {
			continue
		}
		m.setCursor = r.field
		return
	}
}

// moveSetCursor 把光标挪到上/下一个可选行。
//
// 光标存的是**字段**而不是下标：行列表会随上下文变（加账号会多出行），
// 存下标的话加一个账号就能让光标指到别的设置上去 —— 那是最容易误改配置
// 的一种 bug。
func (m *Model) moveSetCursor(rows []settingsRow, delta int) {
	if _, ok := m.currentSetRow(rows); !ok {
		m.normalizeSetCursor(rows)
		return
	}
	i := 0
	for j, r := range rows {
		if r.kind != setGroup && r.field == m.setCursor {
			i = j
			break
		}
	}
	for {
		i += delta
		if i < 0 || i >= len(rows) {
			return // 到头了就停住，不回绕
		}
		if rows[i].kind != setGroup {
			m.setCursor = rows[i].field
			return
		}
	}
}

// ---- 按键：导航 ----

// handleSettingsKey 处理设置页的导航。
func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := settingsRows(m)
	m.normalizeSetCursor(rows)
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.closeSettings()
		return m, nil
	case "up", "k":
		m.moveSetCursor(rows, -1)
	case "down", "j":
		m.moveSetCursor(rows, +1)
	case "left", "h":
		return m.settingsNudge(rows, -1)
	case "right", "l":
		return m.settingsNudge(rows, +1)
	case "enter", " ":
		return m.settingsActivate(rows)
	}
	return m, nil
}

// settingsNudge 处理 ←/→：枚举循环、数字加减，其余不动。
func (m Model) settingsNudge(rows []settingsRow, delta int) (tea.Model, tea.Cmd) {
	r, ok := m.currentSetRow(rows)
	if !ok {
		return m, nil
	}
	switch r.kind {
	case setChoice:
		m.setChoiceNudge(r.field, delta)
	case setNumber:
		m.setNumberNudge(r.field, delta)
	}
	return m, nil
}

// setChoiceNudge 在枚举项上循环。
//
// 刻意**不收** rows 参数：枚举项里只要有一个会改变行列表（fldAccountPick
// 就是），传进来的那份就是过期的。需要行列表就现算，别让调用方有机会
// 递一份旧的进来。
func (m *Model) setChoiceNudge(f setField, delta int) {
	switch f {
	case fldAccountPick:
		n := m.setTargetCount()
		m.setTarget = ((m.setTarget+delta)%n + n) % n
		// 目标一换，行列表就变了（那个账号有没有单独设过，决定「恢复为
		// 全局默认」在不在）。
		//
		// ⚠️ 必须按**换之后**的配置重算行列表。传进来那份 rows 是换之前的：
		// 拿它对号入座等于什么都没做，看着像个归一化、实际是个空操作。
		m.normalizeSetCursor(settingsRows(*m))
	case fldLayout:
		if m.cfg.StartupLayout() == config.LayoutZen {
			m.cfg.Appearance.Layout = config.LayoutNormal
		} else {
			m.cfg.Appearance.Layout = config.LayoutZen
		}
		m.saveSettings("启动布局：" + settingsValue(*m, settingsRow{field: fldLayout}))
	}
}

// setNumberStep 是每种数字用 ←/→ 时一次调多少。
func setNumberStep(f setField) int {
	switch f {
	case fldPollSeconds:
		return 5
	case fldInitialDays:
		return 10
	case fldInitialMax:
		return 100
	case fldZenWidth:
		return 2
	}
	return 1
}

// setNumberRange 是每种数字的合法区间，用来夹住 ←/→ 与手填的结果。
func setNumberRange(f setField) (int, int) {
	switch f {
	case fldPollSeconds:
		return 10, 3600
	case fldInitialDays:
		return 1, 3650
	case fldInitialMax:
		return 1, 100000
	case fldZenWidth:
		return config.ZenMinWidth, config.ZenMaxWidth
	}
	return 0, 0
}

func (m *Model) setNumberNudge(f setField, delta int) {
	step := setNumberStep(f) * delta
	cur, ok := m.readNumber(f)
	if !ok {
		return
	}
	lo, hi := setNumberRange(f)
	next := clampInt(cur+step, lo, hi)
	if next == cur {
		return
	}
	m.applyNumber(f, next)
	m.saveSettings("")
}

// readNumber 取一个数字项当前的值。
//
// ⚠️ 读禅模式宽度走的是 ZenWidthLimit()，不是 Appearance.ZenWidth：后者
// 可能是 0（没设过），拿它做基准的话，按一下 ← 会从 0 减到下限 48 ——
// 用户看到的是「按一次左键就跳到 48」。
func (m Model) readNumber(f setField) (int, bool) {
	switch f {
	case fldPollSeconds:
		return m.cfg.Sync.PollIntervalSeconds, true
	case fldInitialDays:
		return m.cfg.Sync.InitialDays, true
	case fldInitialMax:
		return m.cfg.Sync.InitialMaxMessages, true
	case fldZenWidth:
		return m.cfg.ZenWidthLimit(), true
	}
	return 0, false
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// applyNumber 把某个数字写进配置。
func (m *Model) applyNumber(f setField, v int) {
	switch f {
	case fldPollSeconds:
		m.cfg.Sync.PollIntervalSeconds = v
	case fldInitialDays:
		m.cfg.Sync.InitialDays = v
	case fldInitialMax:
		m.cfg.Sync.InitialMaxMessages = v
	case fldZenWidth:
		m.cfg.Appearance.ZenWidth = v
	}
}

// ---- 按键：回车 ----

// settingsActivate 处理回车/空格。
func (m Model) settingsActivate(rows []settingsRow) (tea.Model, tea.Cmd) {
	r, ok := m.currentSetRow(rows)
	if !ok {
		return m, nil
	}
	switch r.kind {
	case setToggle:
		m.setToggle(r.field)
	case setChoice:
		m.setChoiceNudge(r.field, +1)
	case setText, setNumber:
		return m.beginSettingEdit(r)
	case setAction:
		return m.runSettingAction(r.field)
	}
	return m, nil
}

func (m *Model) setToggle(f setField) {
	switch f {
	case fldHideDevice:
		m.setTargetApply(func(c *config.Chat) { c.HideDevice = !c.HideDevice })
		m.saveSettings("上报设备信息：" + settingsValue(*m, settingsRow{field: fldHideDevice}))
	case fldZenSplit:
		m.cfg.Appearance.ZenSplit = !m.cfg.Appearance.ZenSplit
		m.saveSettings("同人连发拆开：" + onOff(m.cfg.Appearance.ZenSplit))
	}
}

// beginSettingEdit 进编辑模式，把当前值预填进输入框。
func (m Model) beginSettingEdit(r settingsRow) (tea.Model, tea.Cmd) {
	m.editField = r.field
	m.mode = modeSettingsEdit
	m.input.SetValue(m.rawSettingValue(r.field))
	m.input.CursorEnd()
	m.input.Prompt = "> "
	m.input.Placeholder = ""
	// 解锁页会把 EchoMode 设成密码模式，这里必须显式还原 ——
	// 否则改个昵称也会变成一串星号，用户看不见自己打的是什么。
	m.input.EchoMode = textinput.EchoNormal
	// 输入框的宽度要在 View 之前定下来，不然它会按默认值铺开、
	// 顶出设置页那一列。
	m.input.Width = m.editInputWidth(r)
	m.input.Focus()
	return m, textinput.Blink
}

// editInputWidth 是编辑框的可用宽度。
func (m Model) editInputWidth(r settingsRow) int {
	// 「  ▸ 」2 + 标签列 + 两格间隔 + 光标留一点余量。
	w := m.width - textWidth(r.label) - 12
	if w < 10 {
		w = 10
	}
	return w
}

// rawSettingValue 是编辑框的预填值。
//
// 和 settingsValue 的区别：显示时「空」要写成「（默认）」这类提示，
// 而编辑时必须是**真空串**，否则用户一进编辑框就得先删掉那串提示。
func (m Model) rawSettingValue(f setField) string {
	switch f {
	case fldFolders:
		return strings.Join(m.cfg.Sync.Folders, ", ")
	}
	if n, ok := m.readNumber(f); ok {
		return strconv.Itoa(n)
	}
	c := m.setTargetChat()
	switch f {
	case fldNick:
		return c.Nick
	case fldNameColor:
		return c.NameColor
	case fldTextColor:
		return c.TextColor
	}
	return ""
}

// handleSettingsEditKey 处理编辑模式。
func (m Model) handleSettingsEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.cancelSettingEdit()
		return m, nil
	case "enter":
		raw := strings.TrimSpace(m.input.Value())
		note, errMsg := m.commitSetting(m.editField, raw)
		if errMsg != "" {
			// 校验失败就**留在编辑框里**，不退回列表：退回去等于把用户
			// 刚敲的一串东西扔掉，还得重新找到那一行、重新敲一遍。
			m.status = errMsg
			return m, nil
		}
		m.cancelSettingEdit()
		m.saveSettings(note)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// cancelSettingEdit 从编辑态退回设置页。
func (m *Model) cancelSettingEdit() {
	m.mode = modeSettings
	m.input.SetValue("")
	m.input.Blur()
}

// commitSetting 校验并写入。第二个返回值非空表示错误信息（此时不写入）。
func (m *Model) commitSetting(f setField, raw string) (note string, errMsg string) {
	switch f {
	case fldNick:
		if len([]rune(raw)) > maxNickRunes {
			return "", fmt.Sprintf("昵称最多 %d 个字", maxNickRunes)
		}
		m.setTargetApply(func(c *config.Chat) { c.Nick = raw })
		return "昵称：" + settingsValue(*m, settingsRow{field: fldNick}), ""

	case fldNameColor, fldTextColor:
		if raw != "" && !mail.ValidChatColor(raw) {
			return "", "颜色只认 #rgb / #rrggbb / #rrggbbaa（例如 #ff8800）"
		}
		// 统一存小写：收信侧比颜色时是大小写不敏感的，但配置是给人看的，
		// 「#FF8800」和「#ff8800」在同一份文件里混着出现会让人以为它们不同。
		low := strings.ToLower(raw)
		if f == fldNameColor {
			m.setTargetApply(func(c *config.Chat) { c.NameColor = low })
			return "名字颜色：" + settingsValue(*m, settingsRow{field: fldNameColor}), ""
		}
		m.setTargetApply(func(c *config.Chat) { c.TextColor = low })
		return "文字颜色：" + settingsValue(*m, settingsRow{field: fldTextColor}), ""

	case fldFolders:
		parts := splitFolders(raw)
		if len(parts) == 0 {
			return "", "至少要留一个文件夹，否则同步不知道该看哪儿"
		}
		m.cfg.Sync.Folders = parts
		return "同步文件夹：" + strings.Join(parts, ", "), ""
	}

	if _, ok := m.readNumber(f); ok {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return "", "这一项要填数字"
		}
		lo, hi := setNumberRange(f)
		if v < lo || v > hi {
			return "", fmt.Sprintf("这一项要在 %d - %d 之间", lo, hi)
		}
		m.applyNumber(f, v)
		return settingsValue(*m, settingsRow{field: f}), ""
	}
	return "", "这一项不能这样改"
}

// splitFolders 把「INBOX, Sent」拆成切片，去空去空白。
func splitFolders(raw string) []string {
	parts := make([]string, 0, 2)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// maxNickRunes 是昵称的长度上限，和 mail 那边收信侧的截断保持一致。
const maxNickRunes = 32

// ---- 动作 ----

// runSettingAction 执行回车触发的动作。
func (m Model) runSettingAction(f setField) (tea.Model, tea.Cmd) {
	switch f {
	case fldFollowGlobal:
		email, ok := m.setTargetEmail()
		if !ok {
			return m, nil
		}
		if !m.cfg.OwnChat(email) {
			m.status = "这个账号本来就跟全局走"
			return m, nil
		}
		m.cfg.SetChatFor(email, nil)
		// ⚠️ 这一按把**光标自己所在的那一行**弄没了（「恢复为全局默认」只
		// 在目标有自己那份时才出现）。不重算的话 m.setCursor 就悬空：屏幕
		// 上看着没事（viewSettings 每帧会就地归一化），但模型里那个字段
		// 指着一条不存在的行，谁哪天绕开 viewSettings 直接用它就会改错配置。
		m.normalizeSetCursor(settingsRows(m))
		m.saveSettings(email + " 已恢复为跟随全局默认")
		return m, nil

	case fldAddAccount, fldReconfigAccount:
		return m.settingsAccountAction()
	}
	return m, nil
}

// settingsAccountAction 去走一遍配置向导。
//
// 添加与重配是**同一条路**：向导按邮箱 Upsert（见 finishSetup 的注释），
// 填同一个邮箱就是重配。分成两个动作只是为了让用户按自己心里那个词去按。
//
// ⚠️ 进向导之前先把当前这条连接收掉。向导结束时**一定**会重连
// （finishSetup → connect → attach），留着旧的 app 就是每次改账号都多漏
// 一条 IMAP 连接和一份索引文件句柄，一直到程序退出才跟着没。
// 向导没有「取消」这一步（Esc 只在等授权那几步有用），所以不存在
// 「关了老的、用户又退出来、于是手上什么都没有」这种情况。
func (m Model) settingsAccountAction() (tea.Model, tea.Cmd) {
	if m.app != nil {
		_ = m.app.Close()
		m.app = nil
		m.connected = false
		m.setThreads(nil)
	}
	m.status = ""
	m.beginSetup()
	return m, nil
}

// closeSettings 退出设置页回到列表。
func (m *Model) closeSettings() {
	m.mode = modeList
	m.editField = 0
	m.input.SetValue("")
	m.input.Blur()
}

// openSettings 进设置页。
func (m *Model) openSettings() {
	m.mode = modeSettings
	m.status = ""
	m.editField = 0
	m.input.SetValue("")
	m.input.Blur()
	m.normalizeSetCursor(settingsRows(*m))
}

// ---- 落盘 ----

// saveSettings 把配置写回磁盘，并把结果告诉用户。
//
// 写失败**不**回滚内存里那份：用户刚做的选择在界面上是对的，回滚会让
// 他看见自己刚改的值又跳回去，而真正的问题（盘写不进去）一样没解决。
// 所以是「留着 + 明确报错」，让人知道下次启动会变回去。
func (m *Model) saveSettings(note string) {
	if err := m.cfg.Save(); err != nil {
		m.status = "设置没能存下来：" + shortErr(err) + "（本次仍然生效，重启后会变回去）"
		return
	}
	m.status = note
}

// ---- 渲染 ----

// setLine 是渲染出来的一行：文字 + 它属于哪个字段。
//
// 带上字段是为了「让光标那一行留在视口里」：从渲染结果里反查下标，
// 比另写一份「第几行对应第几个字段」的算术稳 —— 组标题占两行这种细节
// 改一次，那份算术就会和渲染悄悄错开，而症状只是「滚动位置偶尔差一行」，
// 没人会去查。
type setLine struct {
	text  string
	field setField
}

// viewSettings 渲染设置页。
func (m Model) viewSettings() string {
	rows := settingsRows(m)
	m.normalizeSetCursor(rows)
	lines := m.settingsLines(rows)

	// 底部三行固定：空行 + 当前项说明 + 按键提示。
	const foot = 3
	bodyH := m.height - 1 - foot
	if bodyH < 1 {
		bodyH = 1
	}

	// 让光标那一行一定在视口里。滚动位置**不落盘**、也不存进 Model：
	// 每帧按光标现算，省掉「改完设置忘了把滚动位置收回来」这类状态。
	scroll := 0
	for i, l := range lines {
		if l.field == m.setCursor {
			if i >= bodyH {
				scroll = i - bodyH + 1
			}
			break
		}
	}
	if max := len(lines) - bodyH; scroll > max && max > 0 {
		scroll = max
	}
	end := scroll + bodyH
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, bodyH+1+foot)
	out = append(out, styleTitle.Render(truncate("  设置", m.width)))
	for _, l := range lines[scroll:end] {
		out = append(out, l.text)
	}
	for len(out) < bodyH+1 {
		out = append(out, "")
	}

	// 说明区：先报错/状态，没有就显示当前项的 hint。
	switch {
	case m.status != "":
		out = append(out, styleError.Render(truncate("  "+m.status, m.width-2)))
	case m.mode == modeSettingsEdit:
		out = append(out, styleMuted.Render(truncate(
			"  回车确认 · Esc 取消", m.width-2)))
	default:
		hint := ""
		if r, ok := m.currentSetRow(rows); ok {
			hint = r.hint
		}
		out = append(out, styleMuted.Render(truncate("  "+hint, m.width-2)))
	}
	out = append(out, styleMuted.Render(truncate(
		"  ↑/↓ 选 · ←/→ 调 · 回车改 · Esc 返回列表 · ? 帮助", m.width)))
	return fillPane(out, m.width, m.height)
}

// settingsLines 把行渲染成字符串。
func (m Model) settingsLines(rows []settingsRow) []setLine {
	// 值右对齐到同一列，扫一眼就能比出来。
	labelW := 0
	for _, r := range rows {
		if r.kind == setGroup {
			continue
		}
		if w := textWidth(r.label); w > labelW {
			labelW = w
		}
	}

	out := make([]setLine, 0, len(rows)+4)
	for _, r := range rows {
		if r.kind == setGroup {
			out = append(out, setLine{})
			out = append(out, setLine{text: styleMuted.Render("  " + r.label)})
			continue
		}

		// 正在编辑的那一行：把值换成输入框。
		if m.mode == modeSettingsEdit && r.field == m.editField {
			out = append(out, setLine{
				text: stylePrompt.Render("  ▸ ") +
					padRight(r.label, labelW) + "  " + m.input.View(),
				field: r.field,
			})
			continue
		}

		val := settingsValue(m, r)
		line := "    " + padRight(r.label, labelW) + "  " + val
		if r.field == m.setCursor {
			// 选中行用「▸ + 亮色」，不用整行反白：这一页的值要能看清
			// 颜色，反白之后十六进制色值本身就难认了。
			line = stylePrompt.Render(truncate(
				"  ▸ "+padRight(r.label, labelW)+"  "+val, m.width-2))
		} else {
			line = truncate(line, m.width-2)
		}
		out = append(out, setLine{text: line, field: r.field})
	}
	return out
}
