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
