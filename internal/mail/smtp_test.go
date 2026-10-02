package mail

import (
	"io"
	"mime/quotedprintable"
	"strings"
	"testing"
)

func TestBuildMessage_Structure(t *testing.T) {
	out := Outgoing{
		To:         []string{"bob@x.com"},
		Subject:    "会议安排",
		Body:       "第一行\n第二行",
		InReplyTo:  "<a@x>",
		References: []string{"<a@x>", "<b@x>"},
	}

	raw, msgID, err := BuildMessage("衡", "me@qq.com", out)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	if !strings.HasPrefix(msgID, "<") || !strings.HasSuffix(msgID, "@qq.com>") {
		t.Errorf("Message-ID 形状不对: %q", msgID)
	}

	head, body, ok := strings.Cut(string(raw), "\r\n\r\n")
	if !ok {
		t.Fatal("找不到头 / 正文分隔")
	}

	// 中文 Subject 必须走 RFC 2047 编码，否则中间环节很可能把它搞坏。
	if strings.Contains(head, "会议安排") {
		t.Error("中文 Subject 没有编码")
	}
	if !strings.Contains(head, "=?utf-8?") {
		t.Error("Subject 没有 RFC 2047 编码")
	}

	// 会话链要原样带上，否则对方客户端串不起线程。
	if !strings.Contains(head, "In-Reply-To: <a@x>") {
		t.Errorf("In-Reply-To 丢了:\n%s", head)
	}
	if !strings.Contains(head, "References: <a@x> <b@x>") {
		t.Errorf("References 丢了:\n%s", head)
	}

	// 正文要能原样解回来。
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("正文解码失败: %v", err)
	}
	if got, want := normalizeNewlines(string(decoded)), "第一行\n第二行"; got != want {
		t.Errorf("正文往返不一致: got %q, want %q", got, want)
	}
}

func TestBuildMessage_BlocksHeaderInjection(t *testing.T) {
	out := Outgoing{
		To:      []string{"bob@x.com"},
		Subject: "hello\r\nBcc: evil@attacker.com",
		Body:    "x",
	}

	raw, _, err := BuildMessage("", "me@qq.com", out)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	head, _, ok := strings.Cut(string(raw), "\r\n\r\n")
	if !ok {
		t.Fatal("找不到头 / 正文分隔")
	}
	// 注意：不能只判断 head 里有没有 "bcc:" 字样 —— Subject 的值本身
	// 就可能包含这段文本。要判断的是它有没有变成一个**独立的头行**。
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("Subject 里的换行成功注入了 Bcc 头:\n%s", head)
		}
	}
}

func TestBuildMessage_EmptyOptionalHeaders(t *testing.T) {
	// 没有 Cc / In-Reply-To / References 时，这几个头不应该出现，
	// 而不是出现一个空值。
	raw, _, err := BuildMessage("", "me@qq.com", Outgoing{
		To:      []string{"bob@x.com"},
		Subject: "hi",
		Body:    "x",
	})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, unwanted := range []string{"Cc:", "In-Reply-To:", "References:"} {
		if strings.Contains(head, unwanted) {
			t.Errorf("不该出现的空头: %s\n%s", unwanted, head)
		}
	}
}

func TestFormatAddress(t *testing.T) {
	cases := []struct {
		name, addr, want string
	}{
		{"", "me@qq.com", "me@qq.com"},
		{"Alice", "me@qq.com", `"Alice" <me@qq.com>`},
		{"衡", "me@qq.com", "=?utf-8?q?=E8=A1=A1?= <me@qq.com>"},
	}
	for _, c := range cases {
		if got := formatAddress(c.name, c.addr); got != c.want {
			t.Errorf("formatAddress(%q, %q) = %q, want %q", c.name, c.addr, got, c.want)
		}
	}
}

func TestEncodeHeaderWord(t *testing.T) {
	if got := encodeHeaderWord("hello world"); got != "hello world" {
		t.Errorf("纯 ASCII 不该被编码: %q", got)
	}
	if got := encodeHeaderWord("会议"); !strings.HasPrefix(got, "=?utf-8?") {
		t.Errorf("中文应该被编码: %q", got)
	}
	if got := encodeHeaderWord("a\r\nb"); strings.ContainsAny(got, "\r\n") {
		t.Errorf("换行没被挡住: %q", got)
	}
}

func TestLooksLikeAddress(t *testing.T) {
	valid := []string{"a@b.com", "first.last@sub.example.org", "x+tag@qq.com"}
	for _, s := range valid {
		if !LooksLikeAddress(s) {
			t.Errorf("应判为合法: %q", s)
		}
	}
	invalid := []string{"", "no-at-sign", "@b.com", "a@b", "a@b.com\r\nBcc: x@y.com", "a b@c.com"}
	for _, s := range invalid {
		if LooksLikeAddress(s) {
			t.Errorf("应判为非法: %q", s)
		}
	}
}

func TestGenerateMessageID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := GenerateMessageID("me@qq.com")
		if seen[id] {
			t.Fatalf("Message-ID 重复: %q", id)
		}
		seen[id] = true
		if !strings.HasSuffix(id, "@qq.com>") {
			t.Fatalf("域名部分不对: %q", id)
		}
	}

	// 没有域名可用时要能兜底，而不是拼出 "<...@>"
	if id := GenerateMessageID("no-at-sign"); !strings.HasSuffix(id, "@clichat.local>") {
		t.Errorf("兜底域名不对: %q", id)
	}
}
