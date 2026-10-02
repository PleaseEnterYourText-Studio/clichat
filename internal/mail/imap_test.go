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

	got, isHTML, err := readPlainText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("readPlainText 出错: %v", err)
	}

	// 走的是 HTML 那一支 —— 界面靠这个标记在消息头上写「HTML」。
	if !isHTML {
		t.Error("正文来自 text/html，却没被标成 HTML")
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

	got, isHTML, err := readPlainText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("readPlainText 出错: %v", err)
	}
	// 正文是纯文本，不该带上「HTML」标记 —— 消息头上会多一个骗人的标记。
	if isHTML {
		t.Error("正文是纯文本，却被标成了 HTML")
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

	got, isHTML, err := readPlainText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("readPlainText 出错: %v", err)
	}
	if !isHTML {
		t.Error("只有 HTML 部分的邮件没被标成 HTML")
	}
	for _, want := range []string{"## 本周进度", "- 甲", "- 乙"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q:\n%s", want, got)
		}
	}
}

// ---- 取数区间的边界 ----

// 上界未知时不能当成「没有邮件」。
//
// 这是一个真实故障的回归测试：网易 126 的 EXAMINE 响应里没有 UIDNEXT，
// 而旧代码把 uidNext==0 直接当成「文件夹是空的」返回空切片。结果是
// 收件箱永远同步不到一封邮件 —— 用户配好了 SMTP/IMAP、能发信、能登录，
// 就是看不到收信。日志里连一条同步记录都没有，因为「空文件夹」这条
// 分支根本不记日志。
//
// 判据盯的是 ok 必须为 true：只要不把「未知」当成「不存在」，
// 后面自会渲染成 from:* 交给服务端定上界。
func TestFetchRange_UnknownUpperBoundIsNotAnEmptyFolder(t *testing.T) {
	lo, hi, ok := fetchRange(1, 0, 0)
	if !ok {
		t.Fatal("UIDNEXT 未知时被判成了「没有邮件」—— 这会让整个文件夹同步不出来")
	}
	if hi != 0 {
		t.Errorf("上界未知时 hi 该保持 0（由上层渲染成 `*`），实际 %d", hi)
	}
	if lo != 1 {
		t.Errorf("下界应该是 1，实际 %d", lo)
	}
}

// 拿得到 UIDNEXT 时用它算准上界 —— 这是「只拉最近 N 封」能算对的前提。
func TestFetchRange_UsesUIDNextWhenKnown(t *testing.T) {
	lo, hi, ok := fetchRange(1, 0, 101)
	if !ok {
		t.Fatal("UIDNEXT=101 时区间不该是空的")
	}
	if lo != 1 || hi != 100 {
		t.Errorf("区间 = [%d, %d]，want [1, 100]", lo, hi)
	}
}

// 调用方给了明确上界就照用，不去看 UIDNEXT。
func TestFetchRange_ExplicitUpperBoundWins(t *testing.T) {
	lo, hi, ok := fetchRange(10, 20, 999)
	if !ok || lo != 10 || hi != 20 {
		t.Errorf("区间 = [%d, %d] ok=%v，want [10, 20] true", lo, hi, ok)
	}
}

// 游标已经追上最新时，区间是空的 —— 这是正常情况，不该报错。
func TestFetchRange_NothingNewIsEmpty(t *testing.T) {
	// UIDNEXT=7 表示最大 UID 是 6，而我们已经有到 6 了。
	if _, _, ok := fetchRange(7, 0, 7); ok {
		t.Error("没有新邮件时该返回 ok=false")
	}
}

// from=0 按 1 处理，别把整个文件夹漏掉。
func TestFetchRange_ZeroFromBecomesOne(t *testing.T) {
	lo, _, ok := fetchRange(0, 0, 5)
	if !ok || lo != 1 {
		t.Errorf("from=0 应该当成 1，实际 lo=%d ok=%v", lo, ok)
	}
}

// 端到端复现 126 那个故障：服务端不给 UIDNEXT 时，收件箱也必须能同步出来。
//
// 上面那条 fetchRange 的测试盯的是边界函数本身；这条盯的是「整条路走通」——
// 因为漏掉的可能是接线（Headers 忘了把 UIDNext 传进 fetchRange）。
// 两条都留：函数级定位问题快，端到端证明用户看到的现象真的变了。
func TestHeaders_WorksWhenServerOmitsUIDNext(t *testing.T) {
	f := NewFake()
	for i := 0; i < 3; i++ {
		f.AddMessage("INBOX", Header{
			MessageID: "<m" + string(rune('a'+i)) + "@x>",
			From:      "alice@x.com", Subject: "信",
		}, "正文")
	}
	f.SimulateNoUIDNext()

	// 前提：确实模拟出了「UIDNEXT 未知」。
	folder, err := f.Folder("INBOX")
	if err != nil {
		t.Fatalf("Folder: %v", err)
	}
	if folder.UIDNext != 0 {
		t.Fatalf("前提不成立：应该报 UIDNext=0，实际 %d", folder.UIDNext)
	}

	headers, err := f.Headers("INBOX", 1, 0)
	if err != nil {
		t.Fatalf("Headers: %v", err)
	}
	if len(headers) != 3 {
		t.Errorf("UIDNEXT 未知时应该仍然拉到 3 封，实际 %d 封 —— "+
			"这正是 126 收件箱空着的病根", len(headers))
	}
}
