package mail

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseReferences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"空", "", nil},
		{"只有空白", "   ", nil},
		{"单个", "<a@x>", []string{"<a@x>"}},
		{"两个", "<a@x> <b@x>", []string{"<a@x>", "<b@x>"}},
		{"折行", "<a@x>\r\n <b@x>", []string{"<a@x>", "<b@x>"}},
		{"多余逗号", "<a@x>,<b@x>", []string{"<a@x>", "<b@x>"}},
	}
	for _, c := range cases {
		got := ParseReferences(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseReferences(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestSanitizeHeaderValue_BlocksInjection(t *testing.T) {
	in := "hello\r\nBcc: evil@attacker.com"
	got := SanitizeHeaderValue(in)

	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("换行没有被去掉，可以注入头部: %q", got)
	}
	if want := "hello  Bcc: evil@attacker.com"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHTMLToText(t *testing.T) {
	in := `<html><head><style>p{color:red}</style></head>` +
		`<body><p>第一段</p><p>第二段 &amp; 更多</p>` +
		`<script>alert(1)</script></body></html>`

	got := HTMLToText(in)

	if strings.Contains(got, "color:red") {
		t.Error("style 内容没被剥掉")
	}
	if strings.Contains(got, "alert(1)") {
		t.Error("script 内容没被剥掉")
	}
	if !strings.Contains(got, "第一段") {
		t.Errorf("丢了第一段: %q", got)
	}
	if !strings.Contains(got, "第二段 & 更多") {
		t.Errorf("实体没解码或丢了第二段: %q", got)
	}
}

func TestTrimQuoted(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"经典引用",
			"我同意\n> 上一句\n> 再上一句",
			"我同意",
		},
		{
			"On ... wrote",
			"收到\n\nOn Mon, Oct 1, 2026 at 2:00 PM Alice <a@x.com> wrote:\n> 历史",
			"收到",
		},
		{
			"中文写道",
			"好的\n\n在 2026年10月1日，Alice 写道：\n> 历史",
			"好的",
		},
		{
			"签名分隔符",
			"你好\n-- \n签名行",
			"你好",
		},
		{
			"无引用",
			"纯正文，没有引用",
			"纯正文，没有引用",
		},
	}
	for _, c := range cases {
		if got := TrimQuoted(c.in); got != c.want {
			t.Errorf("%s: TrimQuoted = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"第一行\n第二行", 0, "第一行"},
		{"一二三四五", 3, "一二三…"},
		{"  短  ", 10, "短"},
		{"", 5, ""},
		{"没有换行", 10, "没有换行"},
	}
	for _, c := range cases {
		if got := FirstLine(c.in, c.max); got != c.want {
			t.Errorf("FirstLine(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestCleanBody(t *testing.T) {
	// HTML 正文转成 Markdown：段落之间是一个空行。这是 Markdown 里
	// 「这里是两段」的唯一表达方式 —— 单个换行会被渲染进同一段。
	if got, want := CleanBody("<p>第一段</p><p>第二段</p>", true), "第一段\n\n第二段"; got != want {
		t.Errorf("HTML 正文: got %q, want %q", got, want)
	}
	if got, want := CleanBody("正文\n\n\n\n> 引用", false), "正文"; got != want {
		t.Errorf("纯文本正文: got %q, want %q", got, want)
	}

	// HTML 的 <blockquote> 转出来就是 "> "，正好落进 TrimQuoted 的
	// 经典引用判据里 —— 引用历史照砍，不需要为 HTML 单开一条路径。
	if got, want := CleanBody("<p>收到</p><blockquote><p>上一句</p></blockquote>", true), "收到"; got != want {
		t.Errorf("HTML 里的引用: got %q, want %q", got, want)
	}

	// 反过来，正文里本来就有的 ">"（HTML 实体）会被转义成 "\>"，
	// 不会被误当成引用起点把后半封砍掉。
	if got := CleanBody("<p>甲</p><p>&gt; 这是正文里的箭头</p>", true); !strings.Contains(got, "这是正文里的箭头") {
		t.Errorf("正文里的 > 被误判成引用了: %q", got)
	}
}
