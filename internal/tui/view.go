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

func (m Model) renderThreadList(width, height int) string {
	lines := make([]string, 0, height)
	lines = append(lines, styleTitle.Render(truncate("会话", width-2)))

	if len(m.threads) == 0 {
		lines = append(lines, styleMuted.Render("（还没有会话）"))
		lines = append(lines, styleMuted.Render("按 n 新建一个"))
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
	if end > len(m.threads) {
		end = len(m.threads)
	}

	for i := start; i < end; i++ {
		th := m.threads[i]
		marker := "  "
		if th.Unread > 0 {
			marker = "● "
		}

		title := truncate(marker+threadTitle(th), width-2)
		subject := th.Subject
		if subject == "" {
			subject = "(无主题)"
		}
		subject = truncate("  "+subject, width-2)

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

// ---- 聊天流 ----

func (m Model) renderChat(width, height int) string {
	title := m.chatTitle()
	lines := make([]string, 0, height)
	lines = append(lines, styleTitle.Render(truncate(title, width-2)))

	th, ok := m.activeThread()
	if !ok {
		lines = append(lines, "")
		if len(m.newChatTo) > 0 {
			lines = append(lines, styleMuted.Render("  输入内容后回车，就会给 "+strings.Join(m.newChatTo, ", ")+" 发出第一封邮件"))
		}
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
	hint := ""
	switch {
	case m.mode == modeNewChat:
		hint = "   （回车确定，Esc 取消）"
	case m.mode == modeChat && m.activeRoot != "" && m.replyAll:
		hint = "   （Tab：发给所有人）"
	case m.mode == modeChat && m.activeRoot != "":
		hint = "   （Tab：只回发件人）"
	}
	return stylePrompt.Render(m.input.View()) + styleMuted.Render(hint)
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
