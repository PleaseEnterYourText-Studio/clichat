package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守着消息头上那个「HTML」标记。
//
// 背景（用户提的需求）：HTML 邮件在入库前会被转成 Markdown（见
// mail.HTMLToMarkdown），按钮、表格、标题的排版都被重排过，这是有损的。
// 界面上不标出来的话，用户看到排版和邮件原文不一样，只会以为是我们渲染
// 坏了，然后去提一个查不出来源的 bug。

// 造一条消息头，只填判据需要的字段。
func head(id, from, fromName string) thread.Header {
	return thread.Header{
		MessageID: id,
		From:      from,
		FromName:  fromName,
		To:        []string{"me@example.com"},
		Subject:   "会议",
		Date:      time.Date(2026, 10, 2, 14, 32, 0, 0, time.Local),
	}
}

// 从 text/html 转来的消息，头上要有标记；纯文本的没有。
func TestRenderMessage_HTMLGetsTag(t *testing.T) {
	m, _ := newMockModel(t)

	plain := head("<p@x>", "alice@example.com", "Alice")
	html := head("<h@x>", "alice@example.com", "Alice")

	m.bodies = map[string]app.Body{
		"<p@x>": {Text: "纯文本正文"},
		"<h@x>": {Text: "HTML 转来的正文", HTML: true},
	}

	headOf := func(msg thread.Header) string { return m.renderMessage(msg, 80)[0] }

	if got := headOf(html); !strings.Contains(got, "HTML") {
		t.Errorf("HTML 邮件的消息头上没有标记: %q", got)
	}
	if got := headOf(plain); strings.Contains(got, "HTML") {
		t.Errorf("纯文本邮件的消息头被误标成 HTML 了: %q", got)
	}
}

// 显示名很长的时候，标记不能被一起截掉。
//
// 显示名是对方自己写的，长度没有上限。消息头先按可用宽度截断再上色，
// 如果扣宽度时没给标记留出位置，名字一长标记就被挤出画面 —— 而这个标记
// 恰恰在最需要它的时候（长名字的营销邮件/系统通知）才最有用。
func TestRenderMessage_TagSurvivesLongName(t *testing.T) {
	m, _ := newMockModel(t)

	msg := head("<h@x>", "noreply@shop.example.com", strings.Repeat("名", 60))
	m.bodies = map[string]app.Body{"<h@x>": {Text: "正文", HTML: true}}

	got := m.renderMessage(msg, 80)[0]
	if !strings.Contains(got, "HTML") {
		t.Errorf("名字很长时标记被挤掉了: %q", got)
	}
	// 用 lipgloss.Width 而不是 textWidth：这一行已经是上过色的，转义序列
	// 只能交给懂 ANSI 的那把尺子（见 styles.go 里那条硬约束）。
	if w := lipgloss.Width(got); w > 80 {
		t.Errorf("消息头宽 %d，超过了可用宽度 80: %q", w, got)
	}
}
