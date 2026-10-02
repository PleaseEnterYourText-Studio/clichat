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

// ---- 主布局 ----

func (m Model) viewTwoPane() string {
	bodyH := m.bodyHeight()
	listW := m.listWidth()
	chatW := m.width - listW - 1

	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderThreadList(listW, bodyH),
		"│",
		m.renderChat(chatW, bodyH),
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

func (m Model) renderChat(width, height int) string {
	title := m.chatTitle()
	lines := make([]string, 0, height)
	lines = append(lines, styleTitle.Render(truncate(title, width-2)))

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
		lines = append(lines, styleMuted.Render(truncate(hint, width-2)))
		return fillPane(lines, width, height)
	}

	var body []string
	for _, msg := range th.Messages {
		body = append(body, m.renderMessage(msg, width)...)
	}
	// 只显示底部放得下的部分；scroll 是从底部往上卷的行数。
	body = tailWindow(body, height-1, m.scroll)

	lines = append(lines, body...)
	return fillPane(lines, width, height)
}

func (m Model) chatTitle() string {
	if m.activeRoot == "" && len(m.newChatTo) > 0 {
		return "新会话 → " + strings.Join(m.newChatTo, ", ")
	}
	if th, ok := m.activeThread(); ok {
		title := threadTitle(th)
		if th.IsGroup() {
			title += fmt.Sprintf("（%d 人）", len(th.Participants))
		}
		return title
	}
	return "会话"
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
	case body != "":
	case msg.UID == 0:
		body = "（本地待同步）"
	default:
		body = "…"
	}

	who := displayName(msg, mine) + "  " + msg.Date.Local().Format("15:04")
	if mine {
		who = styleMine.Render(who)
	} else {
		who = senderStyle(msg.From).Render(who)
	}

	out := make([]string, 0, 8)
	if mine {
		out = append(out, padLeft(who, width-2))
	} else {
		out = append(out, " "+who)
	}
	for _, line := range wrapLines(body, inner) {
		if mine {
			out = append(out, padLeft(line, width-2))
		} else {
			out = append(out, " "+line)
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
		hint := ""
		if m.activeRoot != "" {
			if m.replyAll {
				hint = "   （Tab：发给所有人）"
			} else {
				hint = "   （Tab：只回发件人）"
			}
		}
		return stylePrompt.Render(m.input.View()) + styleMuted.Render(hint)
	}

	// 列表模式没有输入框，这一行留给常驻快捷键提示。
	// 界面上的功能之所以长期"不存在"，就是因为它们只活在代码里。
	return styleMuted.Render(truncate(listHints(), m.width))
}

// confirmPrompt 是确认框上的一句话。
func (m Model) confirmPrompt() string {
	if m.confirmKind == confirmDelete {
		if th, ok := m.pendingThread(); ok {
			return "删除「" + threadTitle(th) + "」？会移到「已删除」文件夹，可恢复。  y 确认 · n 取消"
		}
		return "删除这个会话？会移到「已删除」文件夹。  y 确认 · n 取消"
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
			b.WriteString("\n" + styleTitle.Render("获取"+p.PasswordLabel+"：") + "\n")
			for i, g := range p.Guide {
				b.WriteString(styleMuted.Render(fmt.Sprintf("  %d. %s", i+1, g)) + "\n")
			}
		}
		b.WriteString("\n" + styleMuted.Render("↑/↓ 选择 · 回车确定 · Ctrl+C 退出"))

	case stepEmail:
		b.WriteString("第 1 步 · 邮箱地址\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n")

	case stepPassword:
		p := m.currentProvider()
		name := "该服务商"
		if p != nil {
			name = p.Name
		}
		b.WriteString("第 2 步 · " + name + " 的凭据\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		if p != nil && len(p.Guide) > 0 {
			b.WriteString(styleTitle.Render("去哪拿：") + "\n")
			for i, g := range p.Guide {
				b.WriteString(styleMuted.Render(fmt.Sprintf("  %d. %s", i+1, g)) + "\n")
			}
		}

	case stepMaster:
		b.WriteString("第 3 步 · 主密码\n\n")
		b.WriteString("  " + stylePrompt.Render(m.input.View()) + "\n\n")
		b.WriteString(styleMuted.Render("  凭据会用这个密码加密后存在本地。") + "\n")
		b.WriteString(styleMuted.Render("  它不会被发送到任何地方，忘了就只能重新配置。") + "\n")
	}

	if m.setup.err != "" {
		b.WriteString("\n" + styleError.Render(m.setup.err) + "\n")
	}
	return b.String()
}

// ---- 渲染辅助 ----

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
func tailWindow(lines []string, height, scroll int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	end := len(lines) - scroll
	if end > len(lines) {
		end = len(lines)
	}
	if end < height {
		end = height
	}
	return lines[end-height : end]
}

// threadTitle 给会话起一个短标题。
func threadTitle(th thread.Thread) string {
	if len(th.Participants) == 0 {
		return "(只有你)"
	}
	if !th.IsGroup() {
		return shortAddr(th.Participants[0])
	}
	names := make([]string, 0, len(th.Participants))
	for _, p := range th.Participants {
		names = append(names, shortAddr(p))
	}
	return strings.Join(names, ", ")
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
