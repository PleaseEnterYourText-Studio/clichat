package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// 这一版把「怎么离开这一屏」变成一件**看得见、点得到**的事。
//
// 起因是用户的一句话：「不要 ESC 这种退出，全局支持鼠标直接点击」。在那
// 之前，帮助页、确认框、转发、搜索、新建会话这五处都只能靠 Esc 离开 ——
// 一个用鼠标走到那里的人只能再回去摸键盘，而屏幕上没有一个字告诉他这件事。
//
// 参照物是本机装的 OpenCode（sst/opencode，`packages/tui/src/ui/`）：
//
//   - `dialog-confirm.tsx`：标题行**右端那个字面写着「esc」的文本自己就是
//     一个按钮**（`onMouseUp={() => dialog.clear()}`），底部再右对齐两个
//     按钮，激活的那个铺底色（`theme.primary`）。
//   - `dialog.tsx`：`escape` 和 `ctrl+c` 都只做「关掉最上面这一层」；
//     真正退出应用的是 `app_exit = "ctrl+c,ctrl+d,<leader>q"`
//     （见 `config/keybind.ts:48`）。也就是说 Esc 的语义是**退一层**，
//     不是退出 —— 和 clichat 这边 handleChatKey 里那条注释是同一个结论。
//
// 抄过来的只有结构，两处刻意不抄：
//
//  1. **不把句子里的「Esc」三个字符做成热区。** clichat 的页脚本来就是一行
//     连着的提示文字，把它切开做热区就得按文字宽度逐段累加，改一个标点
//     热区就漂 —— 正是「判据绑死措辞」那个病。这里命名的是按钮
//     （`[ 取消 ]`），不是嵌在句子里的键名。
//  2. **不给按钮铺底色、也不设「激活态」。** OpenCode 的激活态背后是一套
//     左右键切焦点的机制，clichat 没有；而底色在这个界面里已经被一条更重要
//     的规矩占着 ——「底色只表达『这里是一块你现在能动的东西』」（见
//     styles.go）。页脚上每一格都能点，给其中一格铺底等于在说「只有它能
//     点」。按钮靠**括号 + 粗体**自证，那是另一根轴，不和底色抢语义。
//
// ⚠️ 有一件事**没有**抄：OpenCode 的元素带 hover 高亮（`onMouseMove`）。
// 终端里要拿到「指针移到哪儿了」得开 bubbletea 的 MouseAllMotion，那会把
// 每一个像素级的移动都变成一条消息。代价和收益不成比例，所以这里不做。
//
// ---- 位置 ----
//
// 动作栏**钉在屏幕最后一行**，一屏只有这一个位置会长出按钮。三种排版各自
// 的最后一行本来就是页脚（两栏/单栏的 statusRow、帮助页与选择器的末行、
// Zen 的页脚），所以「按钮在最后一行」在这三种排版下是同一件事。
//
// 不把它挂在提示文字旁边（比如确认框那句话的右边）是一个刻意的取舍：
// 「提示在哪儿按钮就在哪儿」这条规则用户学不会，因为提示的位置每个界面都
// 不同；「要出去就往屏幕底下看」学一次就够。
//
// ---- 顺序 ----
//
// 右对齐，且**从右边开始丢**：窄到放不下时先少显示左边那一格，保住最右的
// 那一格 —— 它恰好都是「退出去」（关闭 / 取消 / 清空 / 会话列表）。
// 左对齐的话，被挤出去的正是出路，而那个毛病这个仓库已经犯过一次了
// （见 help.go 里 hintKeys 那段：「q 退出」曾经从来没显示出来过）。

// actionKind 是页脚动作栏上一格按钮要做的事。
//
// 用枚举而不是闭包：闭包没法比较、没法在测试里断言，而这里一共就三种。
// 更要紧的是，枚举逼着 runAction 把每一种都显式接上 —— 闭包漏接一个只是
// 那一格点不动，没有任何地方会报错。
type actionKind int

const (
	actNone actionKind = iota
	// actDismiss 是「退出去」：Esc 在鼠标这一侧的等价物。
	//
	// 一个 kind 覆盖了关闭帮助页、取消确认、取消转发、退出搜索、离开会话
	// 这几件事 —— 因为它们在同一种模式下只有一种含义，而具体做什么交给
	// runAction 按 mode 分派。**不要**为「关闭」和「取消」各开一个 kind：
	// 那样每加一个界面就要先想「这次算哪种」，而两者在用户眼里是同一件事。
	actDismiss
	// actConfirm 是「就按这个办」：确认框里的 y。
	actConfirm
	// actApply 是「保留当前结果回去」：搜索里的回车。
	actApply
)

// action 是页脚上一格按钮。
type action struct {
	kind  actionKind
	label string

	// x0 / x1 是这一格在**页脚内容里的相对列** [x0, x1)，由 actionSpans 填。
	//
	// 和 tabChip 一样是相对列：渲染方按它拼，命中测试方减一次页脚起点。
	// 存绝对列的话，帮助页（起点 0）、两栏（起点 paneX）、Zen（起点
	// frame.left）会在同一份数据上给出三种含义，而「少减一次」这种错只在
	// 其中一种排版下露面。
	x0, x1 int
}

// footerActions 是当前界面页脚上该有哪几格。
//
// 只有「这一屏能离开」的地方才返回东西。列表模式返回空：列表就是家，
// 底下那行留给快捷键提示更值。
func (m Model) footerActions() []action {
	if m.zenActive() {
		// Zen 三屏的 Esc 语义本来就是「退一层」，三格各叫什么由屏决定
		// （见 runAction：三个都转发给同一个 esc）—— 名字不一样、动作一样，
		// 因为「退到哪儿」对用户来说本来就是屏的属性。
		switch m.zenScreen {
		case zenHome:
			return []action{{kind: actDismiss, label: "退出 Zen"}}
		case zenList:
			return []action{{kind: actDismiss, label: "回首页"}}
		default:
			return []action{{kind: actDismiss, label: "会话列表"}}
		}
	}

	switch m.mode {
	case modeHelp:
		return []action{{kind: actDismiss, label: "关闭"}}
	case modeFolder:
		return []action{{kind: actDismiss, label: "取消"}}
	case modeConfirm:
		// 顺序和输入行里那句「y 确认 · n 取消」一致。按钮的左右顺序和键的
		// 先后顺序对不上的话，用户会照着提示记，然后按错。
		return []action{
			{kind: actConfirm, label: "确认"},
			{kind: actDismiss, label: "取消"},
		}
	case modeSearch:
		// 同上，对着「回车保留过滤，Esc 清空」排。
		return []action{
			{kind: actApply, label: "保留过滤"},
			{kind: actDismiss, label: "清空"},
		}
	case modeForward, modeNewChat:
		return []action{{kind: actDismiss, label: "取消"}}
	case modeChat:
		// Esc 在会话里是「退出去看看别处」，**不关会话**（这条语义在
		// handleChatKey 里写着）。按钮照抄它，名字也照抄它的意思。
		return []action{{kind: actDismiss, label: "会话列表"}}
	}
	return nil
}

// actionWidth 是一格按钮占的列数。
//
// 渲染成 "[ 取消 ]"：两个括号各 1 列、标签两侧各留 1 格空白。
func actionWidth(a action) int {
	return textWidth(a.label) + 4
}

// actionSpans 给每一格算出它在页脚内容里的列范围（右对齐）。
//
// 放不下的从左边丢，理由见文件头。回来的是**屏幕顺序**（左 → 右）。
func (m Model) actionSpans(w int) []action {
	as := m.footerActions()
	if len(as) == 0 || w <= 0 {
		return nil
	}

	// 一格按钮之间的间隔。1 格就够：括号本身已经把两格分开了。
	const gap = 1

	x := w
	rev := make([]action, 0, len(as))
	for i := len(as) - 1; i >= 0; i-- {
		cw := actionWidth(as[i])
		if x-cw < 0 {
			break
		}
		x -= cw
		a := as[i]
		a.x0, a.x1 = x, x+cw
		rev = append(rev, a)
		x -= gap
	}

	out := make([]action, len(rev))
	for i, a := range rev {
		out[len(rev)-1-i] = a
	}
	return out
}

// renderActionBar 把算好坐标的那几格画成一行。
//
// 括号用灰、标签用粗体：括号是**容器**（它只是说"这一整块是一格"），
// 标签是**内容**（它才是要读的那两个字）。灰的括号配粗的字，一眼就是
// 「可以按的东西」，而不用碰任何语义色。
func renderActionBar(spans []action) string {
	if len(spans) == 0 {
		return ""
	}
	var b strings.Builder
	for i, a := range spans {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(styleMuted.Render("["))
		b.WriteString(styleTitle.Render(" " + a.label + " "))
		b.WriteString(styleMuted.Render("]"))
	}
	return b.String()
}

// actionRoom 是页脚左边那些内容能用多少列 —— 动作栏的位子先留着。
//
// 顺序是硬要求：先给出路留位，再让状态文字按剩下的宽度截断。反过来的话，
// 一条长状态（比如「上次同步 15:04:05 · someone@long-domain.example」）就能
// 把唯一的出路挤出屏幕，而屏幕上看不出少了什么。
func (m Model) actionRoom(width int) int {
	if bar := m.actionSpans(width); len(bar) > 0 {
		return bar[0].x0
	}
	return width
}

// withActions 把已经算好宽度的左边内容，和右对齐的动作栏拼成一行。
func (m Model) withActions(left string, width int) string {
	bar := m.actionSpans(width)
	if len(bar) == 0 {
		return left
	}
	return padRight(left, bar[0].x0) + renderActionBar(bar)
}

// footerLine 是「整屏版」的页脚：左边一句提示，右边一排按钮。
//
// 帮助页和文件夹选择器走它 —— 它们不参与两栏那套 layout，页脚就是屏幕的
// 最后一行，起点是第 0 列。
func (m Model) footerLine(left string, style lipgloss.Style, width int) string {
	room := m.actionRoom(width)
	s := ""
	if left != "" && room > 0 {
		s = style.Render(truncate(left, room))
	}
	return m.withActions(s, width)
}

// footerSlot 返回动作栏所在的「行 / 起始列 / 可用宽度」。
//
// ⚠️ 渲染和命中测试**共用这一份**，理由和 layout / pickerGeom 那两处一样：
// 各算一遍，改了内缩或者页脚高度就会漂，症状是「点到的位置和看到的不在一
// 起」，而且只在某一种排版下出现。
//
// 三种排版各有一行页脚，起点各不相同：
//
//	两栏       statusRow，起点 paneX（列表栏 + 1 格留白 + 1 格内缩之后）
//	单栏       statusRow，起点 0
//	帮助/选择器  屏幕最后一行，起点 0（自己占满一屏，不走 layout）
//	Zen        屏幕最后一行，起点 frame.left（居中那一列的左沿）
func (m Model) footerSlot(l layout) (row, origin, width int) {
	if m.zenActive() {
		f := m.zenFrame()
		return m.height - 1, f.left, f.contentW
	}
	switch m.mode {
	case modeHelp, modeFolder:
		return m.height - 1, 0, m.width
	case modeUnlock, modeSetup:
		// 这两屏自己画一整页（viewUnlock / viewSetup），没有页脚这一行。
		// 返回 -1：命中测试拿它当「哪一行都不是」。
		return -1, 0, 0
	}
	if l.twoPane {
		return l.statusRow, l.paneX, l.paneW
	}
	// 单栏：viewSinglePane 传的是 contentW+1，这里必须和它一致 —— 差一列
	// 就是最后一格点不中。
	return l.statusRow, l.contentX, l.contentW + 1
}

// actionAt 判断一个屏幕坐标有没有落在一格按钮上。
func (m Model) actionAt(x, y int) (action, bool) {
	l := m.measureLayout()
	row, origin, w := m.footerSlot(l)
	if row < 0 || y != row {
		return action{}, false
	}
	for _, a := range m.actionSpans(w) {
		if x >= origin+a.x0 && x < origin+a.x1 {
			return a, true
		}
	}
	return action{}, false
}

// escKey / enterKey 是动作栏要「扮演」的那两个按键。
//
// ⚠️ 刻意用显式的 KeyType，**不要**写成
// `tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("esc")}` —— 后者只是碰巧
// String() 也叫 "esc"，它模拟的是「用户依次打了 e、s、c 三个字符」。同一个
// 坑在 tui_test.go 的 keyMsg 里记着：PgUp 那条判据绿了很久，而真实终端里
// 那个键**从来没人按过**。
func escKey() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }
func enterKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// runAction 执行页脚上某一格按钮。
//
// ⚠️ **每一格都转发给对应 mode 的按键处理函数，不在这里重写一遍逻辑。**
// 这是本文件最重要的一条决定：「点取消」和「按 Esc」必须走同一段代码。
// 各写一遍的话，改了 Esc 的行为而忘了改按钮，两条路就漂了 —— 而按钮那条
// 路只有鼠标用户会走到，测试里往往也没人走，漂了不会有任何东西报错。
//
// 这也是「所有界面都能点」这件事成本这么低的原因：新增一个只能靠 Esc 离开
// 的界面时，只要在 footerActions 里加一格、在下面加一行转发，键盘和鼠标
// 就同时有了出路，不用为一个键写两套。
func (m Model) runAction(k actionKind) (tea.Model, tea.Cmd) {
	if k == actNone {
		return m, nil
	}

	// Zen 三屏的 Esc 本来就是「退一层」，三格按钮（退出 Zen / 回首页 /
	// 会话列表）转发给同一个键，语义由哪一屏决定。
	if m.zenActive() {
		if k == actDismiss {
			return m.handleZenKey(escKey())
		}
		return m, nil
	}

	switch m.mode {
	case modeHelp:
		if k == actDismiss {
			return m.closeHelp()
		}
	case modeFolder:
		if k == actDismiss {
			return m.handleFolderKey(escKey())
		}
	case modeConfirm:
		switch k {
		case actConfirm:
			return m.handleConfirmKey(runeKey("y"))
		case actDismiss:
			return m.handleConfirmKey(runeKey("n"))
		}
	case modeSearch:
		switch k {
		case actApply:
			return m.handleSearchKey(enterKey())
		case actDismiss:
			return m.handleSearchKey(escKey())
		}
	case modeForward:
		if k == actDismiss {
			return m.handleForwardKey(escKey())
		}
	case modeNewChat:
		if k == actDismiss {
			return m.handleNewChatKey(escKey())
		}
	case modeChat:
		if k == actDismiss {
			return m.handleChatKey(escKey())
		}
	}
	return m, nil
}
