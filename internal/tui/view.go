package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// View 渲染整个界面。
func (m Model) View() string {
	if !m.ready {
		return "正在启动 clichat…"
	}

	// Zen 是一套**平级**的排版，不是 Normal 的参数化版本。
	// 判定条件在 zenActive 里：layout 选了 Zen **且**当前在会话里。
	if m.zenActive() {
		return m.viewZen()
	}

	switch m.mode {
	case modeUnlock:
		return m.viewUnlock()
	case modeSetup:
		return m.viewSetup()
	case modeHelp:
		return m.viewHelp()
	case modeFolder:
		return m.viewFolderPicker()
	}

	if m.width < singlePaneWidth {
		return m.viewSinglePane()
	}
	return m.viewTwoPane()
}

// ---- 尺寸 ----

// listWidth 是左侧会话栏的宽度。
func (m Model) listWidth() int {
	w := m.width / 3
	if w < 24 {
		w = 24
	}
	if w > 36 {
		w = 36
	}
	return w
}

// bodyHeight 是除去分隔线、输入行、状态栏之后正文区的高度。
func (m Model) bodyHeight() int {
	h := m.height - 3
	if h < 3 {
		h = 3
	}
	return h
}

// chatPaneWidth 是会话流那一栏的总宽度（含最右那一列滚动条）。
//
// 双栏时它是右栏 —— 左边一栏加中间那根竖线之外的全部；单栏时就是整幅。
// 抽出来是因为「正文折成几行」和「正文画多宽」必须是同一个数，两边各
// 算一遍的话，改了布局里的一处、另一处就开始骗人。
func (m Model) chatPaneWidth() int {
	if m.width < singlePaneWidth {
		return m.width
	}
	return m.width - m.listWidth() - 1
}

// chatBodyWidth 是正文文字可用的宽度 —— 从 chatPaneWidth 里让出最右一列
// 给滚动条。
//
// 让出的一列是**恒定**的，哪怕当前根本不需要滚动条：宽度一变折行位置
// 就跟着变，正文行数随之变，于是「有没有滚动条」可能因为行数变少而翻转，
// 宽度再变回来 —— 一个来回抖动的反馈回路。恒定预留一列没有这个问题。
func (m Model) chatBodyWidth() int {
	if w := m.chatPaneWidth() - 1; w >= 1 {
		return w
	}
	return 1
}

// chatViewHeight 是会话正文区能显示的行数。
//
// 传进来的是 bodyHeight()（标题 + 正文 + 补白那一整块），减 1 是顶部
// 那行会话标题。单栏双栏都一样：renderChat 永远先画一行标题。
func chatViewHeight(bodyH int) int {
	if bodyH < 2 {
		return 1
	}
	return bodyH - 1
}

// ---- 主布局 ----

func (m Model) viewTwoPane() string {
	bodyH := m.bodyHeight()
	listW := m.listWidth()

	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderThreadList(listW, bodyH),
		"│",
		m.renderChat(m.chatPaneWidth(), bodyH),
	)

	return strings.Join([]string{
		panes,
		strings.Repeat("─", m.width),
		m.renderInput(),
		m.renderStatus(),
	}, "\n")
}

func (m Model) viewSinglePane() string {
	bodyH := m.bodyHeight()

	var body string
	if m.mode == modeChat || m.mode == modeNewChat {
		body = m.renderChat(m.width, bodyH)
	} else {
		body = m.renderThreadList(m.width, bodyH)
	}

	return strings.Join([]string{
		body,
		strings.Repeat("─", m.width),
		m.renderInput(),
		m.renderStatus(),
	}, "\n")
}

// ---- 会话列表 ----

// renderThreadList 画左侧的会话列表。
//
// 标题行带上当前文件夹和未读总数 —— 过滤生效时如果不显示，用户会
// 以为邮件丢了。
func (m Model) renderThreadList(width, height int) string {
	lines := make([]string, 0, height)
	lines = append(lines, styleTitle.Render(truncate(m.listHeader(), width-2)))

	if len(m.visible) == 0 {
		lines = append(lines, styleMuted.Render(truncate("  "+m.emptyListHint(), width-2)))
		return fillPane(lines, width, height)
	}

	// 每条会话占两行：标题 + 主题。标题行已经占掉一行，所以可显示条数要减一。
	const perItem = 2
	capacity := (height - 1) / perItem
	if capacity < 1 {
		capacity = 1
	}

	start := 0
	if m.cursor >= capacity {
		start = m.cursor - capacity + 1
	}
	end := start + capacity
	if end > len(m.visible) {
		end = len(m.visible)
	}

	for i := start; i < end; i++ {
		th := m.visible[i]
		marker := "  "
		if th.Unread > 0 {
			marker = "● "
		}

		// 星标紧跟在未读标记后面：两个标记的列宽都是两列，
		// 所以有没有星标都不会让后面的名字错位。
		star := "  "
		if th.IsStarred() {
			star = "★ "
		}

		title := truncate(marker+star+threadTitle(th), width-2)
		// 副行是「现在在聊什么」—— 会话里最新一条的主题。一个会话可以
		// 横跨很多话题，所以它和标题（对方的名字）回答的是两个问题。
		subject := th.Subject
		if subject == "" {
			subject = "(无主题)"
		}
		subject = truncate("    "+subject, width-2)

		if i == m.cursor {
			// 选中行整行反白，用 padRight 补到满宽才有「整行」的效果。
			lines = append(lines,
				styleSelected.Render(padRight(title, width-2)),
				styleSelected.Render(padRight(subject, width-2)),
			)
			continue
		}

		if th.Unread > 0 {
			lines = append(lines, styleUnread.Render(title))
		} else {
			lines = append(lines, styleTitle.Render(title))
		}
		lines = append(lines, styleMuted.Render(subject))
	}

	return fillPane(lines, width, height)
}

// listHeader 是会话列表的标题行。
func (m Model) listHeader() string {
	title := "会话"
	if m.activeFolder != "" {
		title += " · " + m.activeFolder
	}

	unread := 0
	for _, th := range m.visible {
		if th.Unread > 0 {
			unread++
		}
	}
	if unread > 0 {
		title += fmt.Sprintf("（%d 个未读）", unread)
	}
	return title
}

// emptyListHint 说明列表为什么是空的 —— 是真空，还是被过滤掉了。
func (m Model) emptyListHint() string {
	switch {
	case m.query != "":
		return "没有匹配「" + m.query + "」的会话"
	case m.activeFolder != "":
		return m.activeFolder + " 里还没有会话"
	default:
		return "还没有会话，按 n 新建一个"
	}
}

// ---- 聊天流 ----

// renderChat 画会话流那一栏。
//
// width 的契约是「这一栏的总宽度」，也就是 chatPaneWidth()（双栏时右栏、
// 单栏时整幅）；正文文字实际用 width-1 列，最右一列留给滚动条。
// TestChat_BodyWidthMatchesLayout 守着这条契约 —— 算行数的地方
// （Model.chatBodyLines）走的是 chatBodyWidth()，两者差一格就会出现
// 「能滚，但滚到底还差半行」这种对不上的怪现象。
func (m Model) renderChat(width, height int) string {
	contentW := width - 1
	if contentW < 1 {
		contentW = 1
	}

	title := m.chatTitle()
	lines := make([]string, 0, height)
	lines = append(lines, styleTitle.Render(truncate(title, contentW)))

	th, ok := m.activeThread()
	if !ok {
		lines = append(lines, "")
		// 右边这片空白在列表模式下是常态，什么都不说会让人以为界面坏了。
		// 三种情况分开提示，别给一句放之四海而皆准的废话。
		var hint string
		switch {
		case len(m.newChatTo) > 0:
			hint = "  输入内容后回车，就会给 " + strings.Join(m.newChatTo, ", ") + " 发出第一封邮件"
		case len(m.visible) == 0:
			hint = "  左边还没有会话。按 n 新建一个。"
		default:
			hint = "  按回车打开左边选中的会话。"
		}
		lines = append(lines, styleMuted.Render(truncate(hint, contentW)))
		return fillPane(lines, width, height)
	}

	all := m.renderChatBody(th, contentW)
	// 只显示底部放得下的部分；scroll 是从底部往上卷的行数。
	body := tailWindow(all, chatViewHeight(height), m.scroll)
	body = addScrollBar(body, len(all), m.scroll, contentW)

	lines = append(lines, body...)
	return fillPane(lines, width, height)
}

// renderChatBody 把一个会话里的全部消息渲染成行（不裁剪）。
//
// 渲染和裁剪分开是因为「最多能卷多少行」（Model.maxChatScroll）要用到
// 未裁剪的总行数，而裁剪要用到同一个 scroll 口径。两件事共用这一份行。
func (m Model) renderChatBody(th thread.Thread, width int) []string {
	var body []string
	for _, msg := range th.Messages {
		body = append(body, m.renderMessage(msg, width)...)
	}
	return body
}

// addScrollBar 给正文的每一行右侧接上一列滚动条。
//
// width 是正文文字宽度；返回的每行宽 width+1。
//
// 正文一屏放得下（total <= len(lines)）时什么也不画，只接一个空格 ——
// 用不上的滚动条不该在版面边上白竖一根线。反过来说，它的存在本身就是
// 提示：用户按 ↑/↓ 没反应时，先看一眼右边有没有这条线，就知道「是已经
// 到底了」还是「这个界面根本不能滚」。
//
// 位置换算：scroll 是**从底部往上**卷的行数，滚动条要的是**从顶部往下**
// 的偏移，所以 off = total - viewH - scroll。scroll 到顶（= max）时
// off == 0，滑块停在最上面 —— 和「往上卷到尽头就是最早的内容」一致。
func addScrollBar(lines []string, total, scroll, width int) []string {
	viewH := len(lines)
	if viewH == 0 {
		return lines
	}

	var track, thumb string
	thumbTop, thumbH := 0, 0
	if total > viewH {
		track = styleScrollBar.Render("│")
		thumb = styleScrollThumb.Render("┃")

		// 滑块高度按「可见部分占全文的比例」给，至少留一格 —— 否则
		// 长会话里的滑块会被整除抹成 0，滚动条就只剩轨道，等于没画。
		thumbH = viewH * viewH / total
		if thumbH < 1 {
			thumbH = 1
		}
		if thumbH > viewH {
			thumbH = viewH
		}

		off := total - viewH - scroll
		if off < 0 {
			off = 0
		}
		if maxOff := total - viewH; off > maxOff {
			off = maxOff
		}
		thumbTop = off * (viewH - thumbH) / (total - viewH)
	}

	out := make([]string, 0, viewH)
	for i, l := range lines {
		cell := " "
		switch {
		case track == "":
		case i >= thumbTop && i < thumbTop+thumbH:
			cell = thumb
		default:
			cell = track
		}
		out = append(out, padRight(l, width)+cell)
	}
	return out
}

func (m Model) chatTitle() string {
	if m.activeID == "" && len(m.newChatTo) > 0 {
		return "新会话 → " + strings.Join(m.newChatTo, ", ")
	}
	th, ok := m.activeThread()
	if !ok {
		return "会话"
	}
	// 群聊在会话页里把人全列出来：这一行比列表宽得多，而列表里那个
	// 「等 N 人」只是为了省地方。
	if th.IsGroup() {
		return fmt.Sprintf("%s（%d 人）", strings.Join(peerNames(th), ", "), len(th.Peers))
	}
	return threadTitle(th)
}

// renderMessage 把一个消息渲染成若干行。
func (m Model) renderMessage(msg thread.Header, width int) []string {
	mine := msg.From == m.cfg.Self()

	inner := width - 4
	if inner < 10 {
		inner = 10
	}

	body := m.bodies[msg.MessageID]
	switch {
	case body.Text != "":
	case msg.UID == 0:
		body.Text = "（本地待同步）"
	default:
		body.Text = "…"
	}

	// 消息头 = 谁 + 什么时候 [+ HTML 标记]。
	//
	// 那个标记是「为什么这段排版和邮件原文不一样」的答案：HTML 邮件在
	// 入库前被转成了 Markdown（见 mail.HTMLToMarkdown），按钮、表格、
	// 标题都被重排过。没有它的话，用户看到排版差异只会以为是我们
	// 渲染坏了，然后去提一个查不出来源的 bug。
	tag := ""
	if body.HTML {
		tag = "HTML"
	}

	name := displayName(msg, mine) + "  " + msg.Date.Local().Format("15:04")
	// 先按可用宽度截断**再**上色 —— 反过来的话 lipgloss 会把转义序列
	// 算进宽度，右边的边框就歪了（styles.go 里那条硬约束）。显示名是
	// 对方自己写的，长度没有上限，所以这一步不是多余的。
	budget := width - 2
	if tag != "" {
		budget -= textWidth(tag) + 1 // +1 是标记前面那个空格
	}
	name = truncate(name, budget)

	if mine {
		name = styleMine.Render(name)
	} else {
		name = senderStyle(msg.From).Render(name)
	}

	head := name
	if tag != "" {
		head += " " + styleTag.Render(tag)
	}

	out := make([]string, 0, 8)
	// 布局：自己的靠右对齐、无底色；对方的左起一格缩进、铺满一条灰底，
	// 一直铺到版面右沿。两边在余光里就能分开（paintPeerRow 里说明了
	// 为什么不能直接把整行丢给 style.Render）。
	if mine {
		out = append(out, padLeft(head, width-2))
	} else {
		out = append(out, paintPeerRow(padRight(" "+head, width-2)))
	}
	// 正文里存的是 Markdown（见 mail.HTMLToMarkdown），这里渲染成终端样式。
	//
	// 别改成「先上色再折行」—— 折行必须在 renderMarkdown 内部、在**上色之前**
	// 完成：按 rune 算宽度的折行会把转义序列的每个字符当成一列，折点落错位置，
	// 还会把序列拦腰切断。详见 markdown.go 里的说明。
	for _, line := range renderMarkdown(body.Text, inner) {
		if mine {
			out = append(out, padLeft(line, width-2))
		} else {
			out = append(out, paintPeerRow(padRight(" "+line, width-2)))
		}
	}
	return append(out, "")
}

// ---- 输入行与状态栏 ----

func (m Model) renderInput() string {
	switch m.mode {
	case modeConfirm:
		// 确认框占用这一行：不显示输入框，免得用户以为还能打字。
		return styleError.Render(truncate(m.confirmPrompt(), m.width))

	case modeSearch:
		return stylePrompt.Render(m.input.View()) +
			styleMuted.Render("   （回车保留过滤，Esc 清空）")

	case modeForward:
		return stylePrompt.Render(m.input.View()) +
			styleMuted.Render("   （转发最后一条，回车发送，Esc 取消）")

	case modeNewChat:
		return stylePrompt.Render(m.input.View()) +
			styleMuted.Render("   （回车确定，Esc 取消）")

	case modeChat:
		hint := m.chatHint()
		// 先按剩余宽度截断提示**再**上色。输入框里的字数是没上限的
		// （textinput 不会自己折行），用户敲长句时提示会把这一行顶出
		// 屏幕，右边那栏的边框就歪了。反过来先上色再截断会把转义序列
		// 剪断（styles.go 那条硬约束）。
		//
		// 输入框自己的宽度用 lipgloss.Width 量 —— 它带 SGR 和光标，
		// textWidth 那把尺子不认识转义序列。
		if room := m.width - lipgloss.Width(m.input.View()); textWidth(hint) > room {
			hint = truncate(hint, room)
		}
		return stylePrompt.Render(m.input.View()) + styleMuted.Render(hint)
	}

	// 列表模式没有输入框，这一行留给常驻快捷键提示。
	// 界面上的功能之所以长期"不存在"，就是因为它们只活在代码里。
	return styleMuted.Render(truncate(listHints(), m.width))
}

// chatHint 是会话内输入行右侧那串提示。
//
// 它是会话里唯一能直接看见动作键的地方 —— 输入框有焦点，动作键全被
// 逼成了 Ctrl 组合（见 handleChatKey 顶部的说明），而帮助页在会话里
// 得按 F1 才打得开。所以「刷新」这种天天要用的键必须写在这一行上：
// 只写在帮助页里，等于它是一个只有按过 F1 的人才知道的功能。
func (m Model) chatHint() string {
	if m.activeID == "" {
		// 新会话，还没落地 —— Tab 在这里没有意义（没有会话可回）。
		return "   （回车发出第一封 · F1 帮助）"
	}
	scope := "只回发件人"
	if m.replyAll {
		scope = "发给所有人"
	}
	// 这里必须带上 Ctrl+↑/↓：它是「输入框有焦点时唯一能换会话的键」，
	// 而用户抱怨的正是找不到它。会话内的完整清单在帮助页（F1）。
	return "   （Tab：" + scope + " · Ctrl+↑/↓ 切会话 · Ctrl+R 刷新 · F1 帮助）"
}

// confirmPrompt 是确认框上的一句话。
func (m Model) confirmPrompt() string {
	switch m.confirmKind {
	case confirmDelete:
		if th, ok := m.pendingThread(); ok {
			return "删除「" + threadTitle(th) + "」？会移到「已删除」文件夹，可恢复。  y 确认 · n 取消"
		}
		return "删除这个会话？会移到「已删除」文件夹。  y 确认 · n 取消"

	case confirmAllMail:
		// 关掉时说的是实话：这个开关只管「以后还拉不拉历史」。
		// 已经同步下来的邮件不会因为关掉它就消失，那得走别的操作。
		if m.app != nil && m.app.AllMail() {
			return "关闭「接收全部邮件」？已经同步下来的邮件会留着，只是以后不再拉历史。  y 确认 · n 取消"
		}
		return "打开「接收全部邮件」？会把服务器上的历史邮件全部拉下来，可能要几分钟。  y 确认 · n 取消"
	}
	return "确认？  y 确认 · n 取消"
}

// viewHelp 渲染全屏帮助页。
func (m Model) viewHelp() string {
	lines := helpLines(m.width)

	bodyH := m.height - 1
	if bodyH < 1 {
		bodyH = 1
	}

	scroll := m.helpScroll
	max := len(lines) - bodyH
	if max < 0 {
		max = 0
	}
	if scroll > max {
		scroll = max
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + bodyH
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, bodyH+1)
	out = append(out, lines[scroll:end]...)
	for len(out) < bodyH {
		out = append(out, "")
	}
	out = append(out, styleMuted.Render(truncate(
		"↑/↓ 或 PgUp/PgDn 滚动 · ? 或 Esc 关闭", m.width)))
	return strings.Join(out, "\n")
}

// viewFolderPicker 渲染文件夹选择器。
func (m Model) viewFolderPicker() string {
	choices := m.folderChoices()

	bodyH := m.height - 1
	if bodyH < 1 {
		bodyH = 1
	}
	// 首行标题、末行操作提示，中间留给选项。
	avail := bodyH - 3
	if avail < 1 {
		avail = 1
	}

	// 有些服务商的文件夹多到一屏放不下，所以也要开窗口。
	start := 0
	if m.folderCursor >= avail {
		start = m.folderCursor - avail + 1
	}
	end := start + avail
	if end > len(choices) {
		end = len(choices)
	}

	lines := []string{
		styleTitle.Render(truncate("选择文件夹", m.width)),
		"",
	}
	for i := start; i < end; i++ {
		label := choices[i]
		if label == "" {
			label = "（全部文件夹）"
		}
		label = truncate("  "+label, m.width-2)
		if i == m.folderCursor {
			lines = append(lines, styleSelected.Render(padRight(label, m.width-2)))
		} else {
			lines = append(lines, label)
		}
	}

	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	lines = append(lines, styleMuted.Render(truncate(
		"↑/↓ 选择 · 回车确定 · Esc 取消", m.width)))
	return strings.Join(lines, "\n")
}

func (m Model) renderStatus() string {
	var parts []string
	switch {
	case m.busy:
		parts = append(parts, "同步中…")
	case m.app == nil:
		// 还没连上，不显示连接状态。
	case m.connected:
		parts = append(parts, "● 在线")
	default:
		parts = append(parts, "● 离线")
	}

	// 「接收全部邮件」是个会改变同步代价的模式，得一直在状态栏上看得见。
	// 只在确认框里露一次的话，用户按完 y 就再也想不起来自己开过它了。
	if m.app != nil && m.app.AllMail() {
		parts = append(parts, "接收全部邮件")
	}

	if !m.lastSync.IsZero() {
		parts = append(parts, "上次同步 "+m.lastSync.Format("15:04:05"))
	}
	if m.cfg != nil && m.cfg.Account.Email != "" {
		parts = append(parts, m.cfg.Account.Email)
	}

	line := strings.Join(parts, " · ")
	if m.status != "" {
		line += "   " + m.status
	}
	// 先截断再上色：截断会破坏 ANSI 转义序列。
	line = truncate(line, m.width)

	switch {
	case m.status != "":
		return styleError.Render(line)
	case m.app != nil && !m.connected:
		return styleError.Render(line)
	case m.connected:
		return styleOK.Render(line)
	default:
		return styleMuted.Render(line)
	}
}

// ---- 解锁与配置向导 ----

func (m Model) viewUnlock() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("clichat") + "\n\n")
	b.WriteString("账号：" + m.cfg.Account.Email + "\n\n")
	b.WriteString("输入主密码解锁本地凭据：\n\n")
	b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
	b.WriteString(styleMuted.Render("Ctrl+R 重新配置账号 · Ctrl+C 退出"))
	if m.status != "" {
		b.WriteString("\n\n" + styleError.Render(m.status))
	}
	return b.String()
}

func (m Model) viewSetup() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("clichat 首次配置") + "\n\n")

	switch m.setup.step {
	case stepProvider:
		b.WriteString("选择邮箱服务商：\n\n")
		for i, p := range config.Providers {
			if i == m.setup.provider {
				b.WriteString("  " + styleSelected.Render("▸ "+padRight(p.Name, 22)) + "\n")
			} else {
				b.WriteString("    " + padRight(p.Name, 22) + "\n")
			}
		}
		if p := m.currentProvider(); p != nil {
			b.WriteString("\n" + renderCredentialHelp(p))
		}
		b.WriteString("\n" + styleMuted.Render("↑/↓ 选择 · 回车确定 · Ctrl+C 退出"))

	case stepEmail:
		b.WriteString(m.stepHeader("邮箱地址") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n")

	case stepCustomIMAP:
		b.WriteString(m.stepHeader("IMAP 服务器") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		b.WriteString(styleMuted.Render("  主机:端口，例如 imap.example.com:993") + "\n")
		b.WriteString(styleMuted.Render(fmt.Sprintf(
			"  已按你的邮箱域名预填，不对就直接改（省略端口时默认 %d）", config.DefaultIMAPPort)) + "\n")

	case stepCustomSMTP:
		b.WriteString(m.stepHeader("SMTP 服务器") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		b.WriteString(styleMuted.Render("  主机:端口，例如 smtp.example.com:465") + "\n")
		b.WriteString(styleMuted.Render(fmt.Sprintf(
			"  已按你的邮箱域名预填，不对就直接改（省略端口时默认 %d）", config.DefaultSMTPPort)) + "\n")

	case stepCustomTLS:
		b.WriteString(m.stepHeader("加密方式") + "\n\n")
		for i, opt := range tlsOptions {
			if i == m.setup.tls {
				b.WriteString("  " + styleSelected.Render("▸ "+padRight(opt.Label, 24)) + "\n")
			} else {
				b.WriteString("    " + padRight(opt.Label, 24) + "\n")
			}
		}
		b.WriteString("\n" + styleMuted.Render("↑/↓ 选择 · 回车确定"))

	case stepPassword:
		p := m.currentProvider()
		name := "该服务商"
		if p != nil {
			name = p.Name
		}
		b.WriteString(m.stepHeader(name+" 的凭据") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		if p != nil && len(p.Guide) > 0 {
			b.WriteString(renderCredentialHelp(p))
		}

	case stepMaster:
		b.WriteString(m.stepHeader("主密码") + "\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		b.WriteString(styleMuted.Render("  凭据会用这个密码加密后存在本地。") + "\n")
		b.WriteString(styleMuted.Render("  它不会被发送到任何地方，忘了就只能重新配置。") + "\n")
	}

	if m.setup.err != "" {
		b.WriteString("\n" + styleError.Render(m.setup.err) + "\n")
	}
	return b.String()
}

// stepHeader 返回「第 N 步 · 标题」。步数按当前流程现算 —— 自定义比预设多
// 三步，同一个步骤在两种流程里的序号不一样。
func (m Model) stepHeader(title string) string {
	for i, s := range m.setupFlow() {
		if s == m.setup.step {
			return fmt.Sprintf("第 %d 步 · %s", i+1, title)
		}
	}
	return title
}

// ---- 渲染辅助 ----

// renderCredentialHelp 渲染「去哪拿凭据」这一段：直达链接 + 分步说明。
//
// 链接用 OSC 8 包成可点击的，在支持的终端里直接点开就能去拿授权码。
// 约束见 styles.go 里 hyperlink 的注释：样式不能带属性，否则序列会被切碎。
//
// 文字步骤同时保留作为退路：链接会失效（服务商改版），步骤不会。
// 这是配置向导里最容易卡住的一步，把直达链接摆出来能省掉用户自己找入口。
func renderCredentialHelp(p *config.Provider) string {
	if p == nil {
		return ""
	}

	var b strings.Builder

	if p.OpenURL != "" {
		b.WriteString(styleTitle.Render("直接去这里拿") + "\n")
		b.WriteString("  " + styleLink.Render(hyperlink(p.OpenURL, p.OpenURL)) + "\n")
		if p.HelpURL != "" {
			b.WriteString("  " + styleMuted.Render("官方说明：") +
				styleLink.Render(hyperlink(p.HelpURL, p.HelpURL)) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(styleTitle.Render("获取"+p.PasswordLabel) + "\n")
	for i, g := range p.Guide {
		b.WriteString(styleMuted.Render(fmt.Sprintf("  %d. %s", i+1, g)) + "\n")
	}
	return b.String()
}

// fillPane 把行数补齐到 height，并给每行补足宽度，避免拼栏时错位。
func fillPane(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = padRight(l, width)
	}
	return strings.Join(lines, "\n")
}

// tailWindow 取出列表末尾的一段。scroll 是从底部往上卷的行数。
//
// 越界的 scroll 在这里被收进来：负数按 0，超过总行数按「卷到顶」。
// 这是最后一道保险 —— 负责收敛的是 Model.clampChatScroll，但只要有
// 一条路径漏了收敛（比如窗口刚变小、正文刚被替换），这里也能保证画面
// 仍然是**正文**，而不是一片空白。空白会让人以为邮件没了。
//
// 以前这里没有收敛，`end < height` 时的兜底是 `end = height`，等价于
// 「scroll 悄悄降到 len-height」；现在把这件事写明，并且两个方向都收。
func tailWindow(lines []string, height, scroll int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) <= height {
		return lines
	}
	if maxScroll := len(lines) - height; scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	return lines[len(lines)-height-scroll : len(lines)-scroll]
}

// threadTitle 给会话起一个短标题。
//
// 会话的名字是「和谁」，不是「聊什么」—— 这是 IM 的语义，也是把聚合键
// 换成参与人集合之后自然的结果。主题降级成列表里的副行。
//
// 1:1 显示对方的名字（显示名优先，没有就用地址 @ 前那一段）；
// 群聊显示「Alice 等 3 人」—— 没人给群起名字时 IM 就是这么写的。
func threadTitle(th thread.Thread) string {
	if len(th.Peers) == 0 {
		return "(只有你)"
	}
	name := peerLabel(th, th.Peers[0])
	if len(th.Peers) == 1 {
		return name
	}
	return fmt.Sprintf("%s 等 %d 人", name, len(th.Peers))
}

// peerNames 是会话里全部对方的名字，顺序与 Peers 一致（按地址排序）。
func peerNames(th thread.Thread) []string {
	names := make([]string, 0, len(th.Peers))
	for _, p := range th.Peers {
		names = append(names, peerLabel(th, p))
	}
	return names
}

// peerLabel 是单个对方在界面上显示的名字。
//
// 地址本身是兜底：显示名只有在对方作为发件人露过面时才有（Header 里
// 只有 FromName），所以群聊成员里有人只有地址是正常的。
func peerLabel(th thread.Thread, addr string) string {
	if n := th.NameOf(addr); n != "" {
		return n
	}
	return shortAddr(addr)
}

// displayName 决定消息上显示谁的名字。
func displayName(msg thread.Header, mine bool) string {
	if mine {
		return "我"
	}
	if msg.FromName != "" {
		return msg.FromName
	}
	return shortAddr(msg.From)
}

// shortAddr 取地址 @ 前面的部分，省地方。
func shortAddr(addr string) string {
	if i := strings.IndexByte(addr, '@'); i > 0 {
		return addr[:i]
	}
	return addr
}
