package mail

import (
	"strings"
	"testing"
)

// 这个测试守着两个容易写错、写错代价又很大的点：
// 忘了 PEEK 会把整个收件箱标成已读；忘了限定字段会让每轮轮询
// 都把每封邮件的完整头部拉下来。
func TestHeaderSection_PeeksAndRestrictsFields(t *testing.T) {
	// BodySectionName 只有未导出的 string()，能拿到渲染结果的是 FetchItem()。
	got := string(headerSection().FetchItem())
	t.Logf("section = %q", got)

	if !strings.HasPrefix(got, "BODY.PEEK[") {
		t.Errorf("没有用 BODY.PEEK，轮询会把邮件误标为已读: %q", got)
	}
	if !strings.Contains(got, "HEADER.FIELDS") {
		t.Errorf("没有限定为 HEADER.FIELDS，会把整个头部拉下来: %q", got)
	}
	if !strings.Contains(got, "References") {
		t.Errorf("字段白名单里没有 References: %q", got)
	}
}

func TestHeaderValue(t *testing.T) {
	raw := "References: <a@x> <b@x>\r\n"

	if got, want := headerValue(raw, "References"), "<a@x> <b@x>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// 头名大小写不敏感
	if got, want := headerValue(raw, "references"), "<a@x> <b@x>"; got != want {
		t.Errorf("大小写不敏感失败: got %q, want %q", got, want)
	}
	// 找不到就是空串，不该 panic
	if got := headerValue(raw, "Message-ID"); got != "" {
		t.Errorf("不存在的头应返回空串, got %q", got)
	}
}

func TestHeaderValue_UnfoldsContinuationLines(t *testing.T) {
	// RFC 5322 允许把头折成多行，续行以空白开头。
	raw := "References: <a@x>\r\n <b@x>\r\n\t<c@x>\r\nSubject: hi\r\n"

	got := headerValue(raw, "References")
	want := "<a@x> <b@x> <c@x>"
	if got != want {
		t.Errorf("折行没展开: got %q, want %q", got, want)
	}

	// 解析出来的 References 应该正好是三个 ID
	if refs := ParseReferences(got); len(refs) != 3 {
		t.Errorf("ParseReferences 得到 %d 个, want 3: %v", len(refs), refs)
	}
}

// multipart/alternative 里两个部分都有的情况下选错，用户看到的要么是
// 链接全丢的退化文本，要么是没转过格式的 HTML 源码 —— 两种都很难看。
func TestReadPlainText_PrefersHTML(t *testing.T) {
	const raw = "From: a@x.com\r\n" +
		"Subject: 订单\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=BOUND\r\n" +
		"\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"你好，点击 https://ex.com/confirm 确认订阅\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<p>你好</p>\r\n" +
		"<p><a href=\"https://ex.com/confirm\" style=\"background:#07c\">确认订阅</a></p>\r\n" +
		"--BOUND--\r\n"

	got, err := readPlainText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("readPlainText 出错: %v", err)
	}

	// HTML 那一支胜出，按钮被还原成了 Markdown 链接。
	if !strings.Contains(got, "[确认订阅](https://ex.com/confirm)") {
		t.Errorf("没有优先用 HTML 转 Markdown:\n%s", got)
	}
	if strings.Contains(got, "<p>") || strings.Contains(got, "<a href") {
		t.Errorf("原始标签漏进正文了:\n%s", got)
	}
}

// 没有 HTML 部分时退回 text/plain，而且原样保留 —— 纯文本正文里的
// * 和 _ 是作者自己敲的，不是 Markdown 标记，不该替他转义。
func TestReadPlainText_PlainOnlyIsKeptAsIs(t *testing.T) {
	const raw = "From: a@x.com\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"价格 2*3 元，字段 user_id\r\n"

	got, err := readPlainText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("readPlainText 出错: %v", err)
	}
	if want := "价格 2*3 元，字段 user_id"; got != want {
		t.Errorf("纯文本正文被改动了:\n got: %q\nwant: %q", got, want)
	}
}

// 只有 HTML 部分的邮件（营销邮件基本都是）也要转成 Markdown。
func TestReadPlainText_HTMLOnly(t *testing.T) {
	const raw = "From: a@x.com\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<h2>本周进度</h2><ul><li>甲</li><li>乙</li></ul>\r\n"

	got, err := readPlainText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("readPlainText 出错: %v", err)
	}
	for _, want := range []string{"## 本周进度", "- 甲", "- 乙"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q:\n%s", want, got)
		}
	}
}
