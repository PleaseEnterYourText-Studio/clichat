package thread

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const self = "me@example.com"

func at(day, hour int) time.Time {
	return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC)
}

// msg 构造一条最简消息。
func msg(id string, day, hour int, from string, to ...string) Header {
	return Header{
		MessageID: id,
		Date:      at(day, hour),
		From:      from,
		To:        to,
	}
}

// refs 给消息补上引用链。
func refs(m Header, r ...string) Header {
	m.References = r
	return m
}

// reply 给消息补上 In-Reply-To。
func reply(m Header, id string) Header {
	m.InReplyTo = id
	return m
}

func TestAggregate_ReferencesChain(t *testing.T) {
	headers := []Header{
		refs(msg("<b@x>", 2, 10, "bob@x.com", self), "<a@x>"),
		msg("<a@x>", 1, 10, "alice@x.com", self),
		refs(msg("<c@x>", 3, 10, "carol@x.com", self), "<a@x>", "<b@x>"),
	}

	got := Aggregate(headers, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if got[0].Root != "<a@x>" {
		t.Errorf("root: want <a@x>, got %q", got[0].Root)
	}
	want := []string{"<a@x>", "<b@x>", "<c@x>"}
	if ids := got[0].MessageIDs(); !reflect.DeepEqual(ids, want) {
		t.Errorf("order: want %v, got %v", want, ids)
	}
}

func TestAggregate_BrokenReferences(t *testing.T) {
	// ghost 不在输入里 —— 根消息比拉取窗口更早。
	// 两条都引用同一个未拉取的根，必须被合并。
	headers := []Header{
		refs(msg("<b@x>", 2, 10, "bob@x.com", self), "<ghost@x>"),
		refs(msg("<c@x>", 3, 10, "carol@x.com", self), "<ghost@x>"),
	}

	got := Aggregate(headers, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread merged via unseen root, got %d", len(got))
	}
	if got[0].Root != "<ghost@x>" {
		t.Errorf("root: want <ghost@x>, got %q", got[0].Root)
	}
}

func TestAggregate_InReplyToOnly(t *testing.T) {
	headers := []Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
		reply(msg("<b@x>", 2, 10, "bob@x.com", self), "<a@x>"),
	}

	got := Aggregate(headers, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if got[0].Root != "<a@x>" {
		t.Errorf("root: want <a@x>, got %q", got[0].Root)
	}
}

func TestAggregate_NoReferences(t *testing.T) {
	headers := []Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
		msg("<b@x>", 2, 10, "bob@x.com", self),
	}

	got := Aggregate(headers, self)
	if len(got) != 2 {
		t.Fatalf("want 2 independent threads, got %d", len(got))
	}
}

func TestAggregate_CircularReferences(t *testing.T) {
	// 循环引用必须安全：并查集不做遍历，不会死循环。
	headers := []Header{
		refs(msg("<a@x>", 1, 10, "alice@x.com", self), "<b@x>"),
		refs(msg("<b@x>", 2, 10, "bob@x.com", self), "<a@x>"),
	}

	got := Aggregate(headers, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if len(got[0].Messages) != 2 {
		t.Errorf("want 2 messages, got %d", len(got[0].Messages))
	}
}

func TestAggregate_MissingMessageID(t *testing.T) {
	a := msg("", 1, 10, "alice@x.com", self)
	a.Folder, a.UID = "INBOX", 1
	b := msg("", 2, 10, "bob@x.com", self)
	b.Folder, b.UID = "INBOX", 2

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 2 {
		t.Fatalf("want 2 threads, got %d", len(got))
	}
	for _, th := range got {
		if !strings.HasPrefix(th.Root, "synthetic-") {
			t.Errorf("root should be synthetic, got %q", th.Root)
		}
	}

	// 同一封邮件出现两次 —— 合成 ID 相同，必须去重成一条消息，
	// 而不是变成一个含两条相同消息的会话。
	got = Aggregate([]Header{a, a}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if len(got[0].Messages) != 1 {
		t.Errorf("want 1 message after dedupe, got %d", len(got[0].Messages))
	}
}

func TestSyntheticID_Stable(t *testing.T) {
	m := msg("", 1, 10, "alice@x.com", self)
	m.Folder, m.UID, m.Subject = "INBOX", 42, "hi"

	want := syntheticID(m)
	for i := 0; i < 100; i++ {
		if got := syntheticID(m); got != want {
			t.Fatalf("syntheticID 不稳定：%q != %q", got, want)
		}
	}
}

func TestAggregate_OrderIndependent(t *testing.T) {
	base := []Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
		refs(msg("<b@x>", 2, 10, "bob@x.com", self), "<a@x>"),
		refs(msg("<c@x>", 3, 10, "carol@x.com", self), "<a@x>", "<b@x>"),
		refs(msg("<d@x>", 4, 10, "dave@x.com", self), "<ghost@x>"),
		msg("<e@x>", 5, 10, "erin@x.com", self),
	}

	want := fingerprint(Aggregate(base, self))

	perms := [][]int{
		{4, 3, 2, 1, 0},
		{2, 0, 4, 1, 3},
		{1, 4, 3, 0, 2},
		{3, 2, 1, 4, 0},
	}
	for _, p := range perms {
		shuffled := make([]Header, len(base))
		for i, k := range p {
			shuffled[i] = base[k]
		}
		if got := fingerprint(Aggregate(shuffled, self)); got != want {
			t.Errorf("perm %v:\n got %s\nwant %s", p, got, want)
		}
	}
}

// fingerprint 把聚合结果压成一个可比较的字符串。
func fingerprint(ts []Thread) string {
	var b strings.Builder
	for _, th := range ts {
		b.WriteString(th.Root)
		b.WriteString("|")
		b.WriteString(strings.Join(th.MessageIDs(), ","))
		b.WriteString("|")
		b.WriteString(strings.Join(th.Participants, ","))
		b.WriteString("\n")
	}
	return b.String()
}

func TestAggregate_SameSubjectNotMerged(t *testing.T) {
	a := msg("<a@x>", 1, 10, "alice@x.com", self)
	a.Subject = "会议安排"
	b := msg("<b@x>", 2, 10, "bob@x.com", self)
	b.Subject = "Re: 会议安排"

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 2 {
		t.Fatalf("同 Subject 的不同线程被错误合并：want 2, got %d", len(got))
	}
}

func TestThread_IsGroup(t *testing.T) {
	direct := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
	}, self)
	if len(direct) != 1 || direct[0].IsGroup() {
		t.Errorf("1:1 会话不应被判为群聊")
	}

	group := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self, "carol@x.com"),
	}, self)
	if len(group) != 1 || !group[0].IsGroup() {
		t.Fatalf("三人会话应被判为群聊")
	}
	want := []string{"alice@x.com", "carol@x.com"}
	if !reflect.DeepEqual(group[0].Participants, want) {
		t.Errorf("participants: want %v, got %v", want, group[0].Participants)
	}
}

func TestAggregate_DedupeMergesSeen(t *testing.T) {
	inbox := msg("<a@x>", 1, 10, "alice@x.com", self)
	inbox.Folder, inbox.UID, inbox.Seen = "INBOX", 1, false
	sent := inbox
	sent.Folder, sent.UID, sent.Seen = "Sent", 7, true

	got := Aggregate([]Header{inbox, sent}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if len(got[0].Messages) != 1 {
		t.Fatalf("want 1 message after dedupe, got %d", len(got[0].Messages))
	}
	if got[0].Unread != 0 {
		t.Errorf("任一副本已读即应算已读，Unread = %d", got[0].Unread)
	}
}

func TestAggregate_UnreadCount(t *testing.T) {
	a := msg("<a@x>", 1, 10, "alice@x.com", self)
	b := refs(msg("<b@x>", 2, 10, "bob@x.com", self), "<a@x>")
	b.Seen = true

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if got[0].Unread != 1 {
		t.Errorf("want Unread=1, got %d", got[0].Unread)
	}
}

func TestAggregate_ExcludesSelfFromParticipants(t *testing.T) {
	got := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self, self),
	}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if want := []string{"alice@x.com"}; !reflect.DeepEqual(got[0].Participants, want) {
		t.Errorf("participants: want %v, got %v", want, got[0].Participants)
	}
}

func TestStripSubjectPrefix(t *testing.T) {
	cases := map[string]string{
		"Re: hello":     "hello",
		"RE: Re: hello": "hello",
		"回复： 会议":        "会议",
		"回复: 回复： 会议":    "会议",
		"Fwd: Re: 报告":   "报告",
		"转发： 转发: 通知":    "通知",
		"hello":         "hello",
		"":              "",
	}
	for in, want := range cases {
		if got := StripSubjectPrefix(in); got != want {
			t.Errorf("StripSubjectPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
