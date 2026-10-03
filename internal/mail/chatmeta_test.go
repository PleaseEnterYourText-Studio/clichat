package mail

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守着 clichat 消息头（X-Clichat-Meta）的线上格式。
//
// chatmeta.go 是发信和收信两侧**唯一**的权威（编、解、净化都在那儿），
// 所以判据也钉在这里。真正容易坏的不是「编码函数能不能编码」，而是
// 两端之间的约定：折了行还认不认、对面没净化我扛不扛得住、版本号空了
// 算不算 clichat 消息。

// fullMeta 是一张「每个字段都有值」的身份卡。
func fullMeta() thread.ClichatMeta {
	return thread.ClichatMeta{
		Version:   "1.4.2",
		Nick:      "衡",
		NameColor: "#ff8800",
		TextColor: "#d0d0d0",
		Device:    "windows/amd64",
	}
}

// b64 手拼一个载荷，绕开 EncodeChatMeta。
//
// 专门给「收信那一侧自己的净化」用：夹具不能借被测对象的另一条路径
// 把前提条件顺手满足掉，否则那条判据测的就不是它声称的东西。
func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// 编出来的东西必须能原样解回来。
func TestChatMeta_RoundTrip(t *testing.T) {
	want := fullMeta()
	got := DecodeChatMeta(EncodeChatMeta(want))
	if got == nil {
		t.Fatal("编出来的值解不回来 —— 发得出去、收不回来")
	}
	if *got != want {
		t.Errorf("往返之后不一样:\n got %+v\nwant %+v", *got, want)
	}
}

// ⚠️ 折行之后仍然要解得开。
//
// 这是整条链路上最容易漏的一环：base64 解码器忽略 \r\n，但**不忽略空格**，
// 而头值折行展开之后那个空格会留下来（RFC 5322 §2.2.3 只去掉 CRLF）。
// 漏了它的后果特别刁钻 —— 只在昵称长到触发折行时才犯，平时完全看不见。
//
// 走的是**真实**那条取值路径：foldHeaderValue 折，headerValue 展开，
// 再交给 DecodeChatMeta。中间任何一环变了，这条都会红。
func TestChatMeta_SurvivesFolding(t *testing.T) {
	m := fullMeta()
	// 昵称填到上限，编码后才够长到折行。
	m.Nick = strings.Repeat("昵", maxChatNick)

	enc := EncodeChatMeta(m)
	if len(enc) <= 76 {
		t.Fatalf("载荷只有 %d 字节、压根没折行 —— 这条判据没走到折行那条路上，"+
			"它绿了也说明不了折行之后还认不认", len(enc))
	}

	folded := foldHeaderValue(ChatMetaHeader, enc)
	if !strings.Contains(folded, "\r\n ") {
		t.Fatalf("foldHeaderValue 没有折行，那它就没起作用:\n%q", folded)
	}

	// headerValue 收的是**整段原始头**，折行的续行靠它展开。
	got := DecodeChatMeta(headerValue(folded, ChatMetaHeader))
	if got == nil {
		t.Fatal("折过行的头解不开 —— 昵称一长就发得出去、收不回来")
	}
	if got.Nick != m.Nick {
		t.Errorf("昵称 = %q，想要 %q", got.Nick, m.Nick)
	}
	if *got != m {
		t.Errorf("折行往返之后不一样:\n got %+v\nwant %+v", *got, m)
	}
}

// 垃圾输入一律当成「不是 clichat 消息」。
//
// 这个头来自别人的机器，所以每一种坏法都得有个明确的结论。返回值只有
// 「是 / 不是」两种，正是因为界面上要做的事也只有两件（标不标那个标记、
// 显不显示身份信息）—— 一个坏掉的头不该让我这边弹出任何东西来。
func TestChatMeta_GarbageIsIgnored(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"空值", ""},
		{"只有空白", "   \r\n\t"},
		{"不是 base64", "!!!not base64@@@"},
		{"base64 但里面不是 JSON", b64("hello")},
		{"空 JSON 对象（没有版本号）", b64(`{}`)},
		{"版本号是空串", b64(`{"v":""}`)},
		{"版本号只有空白", b64(`{"v":"   "}`)},
		{"JSON 是数组", b64(`["v"]`)},
		{"JSON 是字符串", b64(`"v"`)},

		// 挡在 base64 之前：一个几百 KB 的畸形头不该先被解码成一大块
		// 内存，再靠字段上限去截。
		{"超长载荷", strings.Repeat("A", maxChatMetaEncoded+1)},

		// 5 个字符的 base64url 长度不合法（5 % 4 == 1），解出来是错误。
		{"截断的载荷", EncodeChatMeta(fullMeta())[:5]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecodeChatMeta(tc.raw); got != nil {
				t.Errorf("%s 被当成了 clichat 消息: %+v", tc.name, *got)
			}
		})
	}
}

// ⚠️ 净化必须在**收信那一侧**独立成立。
//
// 夹具刻意绕开 EncodeChatMeta 自己拼 base64。走 Encode 的话，是它在
// 「发信前」先把脏字段洗干净了，解码那侧就算一个字段都不判也照样绿 ——
// 而真实的脏数据正是别人（或者别人改过的客户端）直接塞进头里的。
//
// 每个字段都带一个具体的问题，且**逐个断言**：只断言「解出来的结构
// 不为 nil」是空壳判据，它证明不了任何一个字符被滤掉过。
func TestChatMeta_DecodeSanitizesHostileInput(t *testing.T) {
	// 注意这是 JSON 文本，\u001b 由 json.Unmarshal 还原成真正的 ESC。
	raw := `{"v":"1.0\u001b[31m","n":"衡\u202e反","nc":"red","tc":"#12345","d":"` +
		strings.Repeat("d", maxChatDevice+20) + `"}`

	m := DecodeChatMeta(b64(raw))
	if m == nil {
		t.Fatal("版本号还在，整条不该被丢掉 —— 该丢的是脏字符，不是这封信：" +
			"把一封内容干净的邮件整封降级成普通邮件，比留下几个怪字更糟")
	}

	// \x1b 能改写终端状态，是这一类输入里最危险的一个。
	if strings.ContainsAny(m.Version, "\x1b") {
		t.Errorf("版本号里留下了 ESC: %q", m.Version)
	}
	if !strings.Contains(m.Version, "1.0") || !strings.Contains(m.Version, "[31m") {
		t.Errorf("滤 ESC 时把可见字符也吃了: %q", m.Version)
	}

	// U+202E 之类的双向控制符能把整行显示成另一个样子。
	if strings.ContainsRune(m.Nick, '\u202e') {
		t.Errorf("昵称里留下了双向控制符: %q", m.Nick)
	}
	if m.Nick != "衡反" {
		t.Errorf("昵称 = %q，想要 %q", m.Nick, "衡反")
	}

	// 颜色只认十六进制。认不出来的当「对方没给」，退回默认配色 ——
	// 而不是把一串疑似控制序列画到我终端上。
	if m.NameColor != "" {
		t.Errorf("颜色名 %q 被当成了合法颜色，实际 = %q", "red", m.NameColor)
	}
	if m.TextColor != "" {
		t.Errorf("#12345 不是合法长度，却被留下了: %q", m.TextColor)
	}

	if n := utf8.RuneCountInString(m.Device); n != maxChatDevice {
		t.Errorf("设备信息有 %d 个 rune，想要截到 %d", n, maxChatDevice)
	}
}

// 合法的颜色要留下，并且规范化成小写。
//
// 上一条判的是「坏的丢掉」，这一条判的是**没丢过头** —— 只有一边的判据
// 可以被「把 cleanChatColor 直接改成恒返回空串」骗过去。
func TestChatMeta_KeepsValidColors(t *testing.T) {
	tests := []struct{ in, want string }{
		{"#abc", "#abc"},
		{"#ABCDEF", "#abcdef"},
		{"#AABBCCDD", "#aabbccdd"},
		{" #abc ", "#abc"}, // 首尾空白不算内容
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			m := DecodeChatMeta(b64(`{"v":"1.0","nc":"` + tc.in + `"}`))
			if m == nil {
				t.Fatal("解不出来")
			}
			if m.NameColor != tc.want {
				t.Errorf("NameColor = %q，想要 %q", m.NameColor, tc.want)
			}
		})
	}
}

// 超长的字段被截到上限，而不是整条丢掉。
func TestChatMeta_TruncatesLongFields(t *testing.T) {
	long := strings.Repeat("字", maxChatNick+10)
	m := DecodeChatMeta(EncodeChatMeta(thread.ClichatMeta{Version: "1.0", Nick: long}))
	if m == nil {
		t.Fatal("解不出来")
	}
	if n := utf8.RuneCountInString(m.Nick); n != maxChatNick {
		t.Errorf("昵称有 %d 个 rune，想要 %d —— 上限得按 rune 算，按字节算会把字切碎",
			n, maxChatNick)
	}
	if !strings.HasPrefix(long, m.Nick) {
		t.Errorf("截出来的不是原文的前缀: %q", m.Nick)
	}
}

// 版本号为空就整个不写这个头。
//
// 版本号是「这是不是 clichat 消息」的唯一判据（见 DecodeChatMeta），
// 写一个对面不认的头，等于每封邮件白背几百字节。
func TestBuildMessage_NoChatMetaWhenVersionEmpty(t *testing.T) {
	raw, _, err := BuildMessage("衡", "me@example.com", Outgoing{
		To: []string{"you@example.com"}, Subject: "在吗", Body: "喂",
	}, thread.ClichatMeta{Nick: "衡", NameColor: "#ff0000"})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	if strings.Contains(string(raw), ChatMetaHeader) {
		t.Errorf("版本号为空却还是写了 %s 头", ChatMetaHeader)
	}
}

// BuildMessage 写出来的形状，必须能被收信那条路径解回来。
func TestBuildMessage_ChatMetaIsReadable(t *testing.T) {
	meta := fullMeta()
	raw, _, err := BuildMessage("衡", "me@example.com", Outgoing{
		To: []string{"you@example.com"}, Subject: "在吗", Body: "喂",
	}, meta)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	if !strings.Contains(string(raw), ChatMetaHeader+":") {
		t.Fatalf("发出去的信里没有 %s 头:\n%s", ChatMetaHeader, raw)
	}
	got := DecodeChatMeta(headerValue(string(raw), ChatMetaHeader))
	if got == nil {
		t.Fatal("自己写进去的头自己解不出来")
	}
	if *got != meta {
		t.Errorf("发出去和收回来不一样:\n got %+v\nwant %+v", *got, meta)
	}
}

// ⚠️ 这条是用户那句话的判据：「在邮件客户端看着没有任何区别」。
//
// 加了 meta 和没加 meta 的两封信，**正文那一段必须逐字节相同**。
// 只断言「头里有 X-Clichat-Meta」远远不够：那只说明我们写了头，说明不了
// 我们没顺手动正文（比如把 meta 也拼进了正文，或者改了 Content-Type /
// 编码方式 —— 那都会让对方的客户端里显示得和平时不一样）。
//
// 头本身是比不了的：Message-ID 每次都不一样（GenerateMessageID）。所以
// 只比第一个空行之后的正文。
func TestBuildMessage_ChatMetaLeavesBodyUntouched(t *testing.T) {
	out := Outgoing{
		To:      []string{"you@example.com"},
		Subject: "在吗",
		// 故意混进中文、emoji、制表符和看起来像头的行 —— 引号可打印
		// 编码（quoted-printable）要处理它们，而它正是「正文会不会被
		// 改动」最可能出问题的地方。
		Body: "喂 🎉\n\tall:\n\t\tgo build ./...\nX-Not-A-Header: 这行是正文里的\n",
	}

	with, _, err := BuildMessage("衡", "me@example.com", out, fullMeta())
	if err != nil {
		t.Fatalf("BuildMessage(带 meta): %v", err)
	}
	without, _, err := BuildMessage("衡", "me@example.com", out, thread.ClichatMeta{})
	if err != nil {
		t.Fatalf("BuildMessage(不带 meta): %v", err)
	}

	bodyWith := bodyOf(t, with)
	if bodyWith != bodyOf(t, without) {
		t.Errorf("加了 meta 之后正文变了 —— 对方在邮件客户端里会看出差别:\n"+
			"带 meta:\n%s\n不带 meta:\n%s", bodyWith, bodyOf(t, without))
	}
	if !strings.Contains(bodyWith, "go build") {
		t.Errorf("正文没被完整写出来:\n%s", bodyWith)
	}
}

// bodyOf 取出邮件正文（头与正文之间那个空行之后的部分）。
func bodyOf(t *testing.T, raw []byte) string {
	t.Helper()
	i := bytes.Index(raw, []byte("\r\n\r\n"))
	if i < 0 {
		t.Fatalf("这封信里找不到头和正文的分界:\n%s", raw)
	}
	return string(raw[i+4:])
}
