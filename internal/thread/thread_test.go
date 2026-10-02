package thread

import (
	"reflect"
	"strconv"
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

// withRefs 给消息补上引用链。
//
// 聚合已经不看引用链了，这些帮助函数存在的意义正相反：用来**证明**
// 引用链怎么变都不影响分组结果。
func withRefs(m Header, r ...string) Header {
	m.References = r
	return m
}

// withReplyTo 给消息补上 In-Reply-To。
func withReplyTo(m Header, id string) Header {
	m.InReplyTo = id
	return m
}

// ---- 参与人集合就是会话 ----

// 同样是 alice，她发给我的和我发给她的算一个会话 —— 这是「双向」。
func TestAggregate_BidirectionalSamePeer(t *testing.T) {
	got := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
		msg("<b@x>", 2, 10, self, "alice@x.com"),
	}, self)

	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if want := []string{"alice@x.com"}; !reflect.DeepEqual(got[0].Peers, want) {
		t.Errorf("peers: want %v, got %v", want, got[0].Peers)
	}
	if want := []string{"<a@x>", "<b@x>"}; !reflect.DeepEqual(got[0].MessageIDs(), want) {
		t.Errorf("order: want %v, got %v", want, got[0].MessageIDs())
	}
}

// 参与人集合就是会话键，所以键要能一眼看出来是什么。
func TestAggregate_IDIsPeerSet(t *testing.T) {
	got := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
	}, self)

	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if got[0].ID != "alice@x.com" {
		t.Errorf("ID = %q, want %q", got[0].ID, "alice@x.com")
	}
}

func TestAggregate_DifferentPeersAreSeparate(t *testing.T) {
	got := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
		msg("<b@x>", 2, 10, "bob@x.com", self),
	}, self)

	if len(got) != 2 {
		t.Fatalf("want 2 threads, got %d", len(got))
	}
}

// 群发 [Alice, Bob] 是独立的一个会话，不能并进「我和 Alice」的私聊。
func TestAggregate_GroupSeparateFromDirect(t *testing.T) {
	got := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
		msg("<b@x>", 2, 10, "alice@x.com", self, "bob@x.com"),
	}, self)

	if len(got) != 2 {
		t.Fatalf("want 2 threads（私聊 + 群聊）, got %d", len(got))
	}
	for _, th := range got {
		switch len(th.Peers) {
		case 1:
			if th.Peers[0] != "alice@x.com" {
				t.Errorf("私聊的参与人不对: %v", th.Peers)
			}
		case 2:
			if want := []string{"alice@x.com", "bob@x.com"}; !reflect.DeepEqual(th.Peers, want) {
				t.Errorf("群聊的参与人不对: %v", th.Peers)
			}
		default:
			t.Errorf("参与人数不对: %v", th.Peers)
		}
	}
}

// 参与人集合是不分先后、不去重的**集合**：To / Cc 里的顺序、重复、
// 大小写都不该影响分组。
func TestAggregate_PeerSetIsOrderAndCaseInsensitive(t *testing.T) {
	got := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self, "bob@x.com"),
		msg("<b@x>", 2, 10, "bob@x.com", self, "ALICE@x.com "),
		// Cc 里再抄一遍同一批人，不该变成第三个键。
		func() Header {
			m := msg("<c@x>", 3, 10, "carol@x.com", self)
			m.Cc = []string{"alice@x.com", "bob@x.com", "alice@x.com"}
			return m
		}(),
	}, self)

	if len(got) != 2 {
		t.Fatalf("want 2 threads（{alice,bob} 和 {carol,alice,bob}）, got %d", len(got))
	}
	// 排序后的键：alice 在前。
	for _, th := range got {
		if len(th.Peers) == 2 && (th.Peers[0] != "alice@x.com" || th.Peers[1] != "bob@x.com") {
			t.Errorf("参与人集合没排序去重: %v", th.Peers)
		}
	}
}

// ---- 这一轮改动的核心：同一批人的不同话题会并到一起 ----

// 同一个人的账单、通知、讨论现在是一个会话。同名主题、引用链完全不同
// 也一样 —— 引用链不参与聚合。
func TestAggregate_SamePeerDifferentTopicsMerge(t *testing.T) {
	a := msg("<a@x>", 1, 10, "alice@x.com", self)
	a.Subject = "月度账单"
	b := msg("<b@x>", 2, 10, "alice@x.com", self)
	b.Subject = "会议安排"
	c := withRefs(msg("<c@x>", 3, 10, "alice@x.com", self), "<ghost@x>", "<ghost2@x>")
	c.Subject = "Re: 会议安排"

	got := Aggregate([]Header{a, b, c}, self)
	if len(got) != 1 {
		t.Fatalf("同一个人的三封邮件应该并成一个会话, got %d", len(got))
	}
	if n := len(got[0].Messages); n != 3 {
		t.Errorf("合并后应该有 3 条消息, got %d", n)
	}
}

// 引用链现在只是消息的元信息，断链/异链都不影响分组 —— 这条守着
// 「并查集删掉之后没人再把 References 捡回来当分组依据」。
func TestAggregate_ReferencesDoNotAffectGrouping(t *testing.T) {
	got := Aggregate([]Header{
		withRefs(msg("<a@x>", 1, 10, "alice@x.com", self), "<one@x>"),
		withRefs(msg("<b@x>", 2, 10, "alice@x.com", self), "<two@x>"),
		withRefs(msg("<c@x>", 3, 10, "alice@x.com", self), "<three@x>", "<four@x>"),
		withReplyTo(msg("<d@x>", 4, 10, "alice@x.com", self), "<five@x>"),
	}, self)

	if len(got) != 1 {
		t.Fatalf("引用链不同的同人邮件应该并成一个会话, got %d", len(got))
	}
	if n := len(got[0].Messages); n != 4 {
		t.Errorf("应该有 4 条消息, got %d", n)
	}
}

// 不同的人即使主题一模一样（甚至互为回复）也不能并 —— 主题不是键。
func TestAggregate_SameSubjectDifferentPeersNotMerged(t *testing.T) {
	a := msg("<a@x>", 1, 10, "alice@x.com", self)
	a.Subject = "会议安排"
	b := withReplyTo(msg("<b@x>", 2, 10, "bob@x.com", self), "<a@x>")
	b.Subject = "Re: 会议安排"

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 2 {
		t.Fatalf("不同发件人同主题被错误合并：want 2, got %d", len(got))
	}
}

// ---- Subject 取最新的一条 ----

func TestAggregate_SubjectIsFromLatestMessage(t *testing.T) {
	a := msg("<a@x>", 1, 10, "alice@x.com", self)
	a.Subject = "月度账单"
	b := msg("<b@x>", 2, 10, "alice@x.com", self)
	b.Subject = "Re: 会议安排"

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	// 最新那条的主题才是「现在在聊什么」，而且要剥掉前缀。
	if got[0].Subject != "会议安排" {
		t.Errorf("Subject = %q, want %q", got[0].Subject, "会议安排")
	}
}

// 最新那条偶尔没有主题（有的客户端会这样），退回上一条，别让整个
// 会话失去主题。
func TestAggregate_SubjectSkipsEmptyLatest(t *testing.T) {
	a := msg("<a@x>", 1, 10, "alice@x.com", self)
	a.Subject = "会议安排"
	b := msg("<b@x>", 2, 10, "alice@x.com", self) // 无主题

	got := Aggregate([]Header{a, b}, self)
	if got[0].Subject != "会议安排" {
		t.Errorf("Subject = %q, want %q（应退回到上一条有主题的）", got[0].Subject, "会议安排")
	}
}

func TestAggregate_SubjectEmptyWhenNoMessageHasOne(t *testing.T) {
	a := msg("<a@x>", 1, 10, "alice@x.com", self)
	b := msg("<b@x>", 2, 10, "alice@x.com", self)

	got := Aggregate([]Header{a, b}, self)
	if got[0].Subject != "" {
		t.Errorf("Subject = %q, want 空", got[0].Subject)
	}
}

// ---- 边界 ----

// 没有别的参与人（自己发给自己、或者地址全空）时不能全都塞进同一个
// 会话里 —— 那会把不相干的邮件藏到一起。
func TestAggregate_NoPeersFallsBackToSingleton(t *testing.T) {
	a := msg("<a@x>", 1, 10, self, self)
	b := msg("<b@x>", 2, 10, self, self)

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 2 {
		t.Fatalf("没有参与人的消息应各成一个会话, got %d", len(got))
	}
	for _, th := range got {
		if len(th.Peers) != 0 {
			t.Errorf("Peers 应为空, got %v", th.Peers)
		}
		if len(th.Messages) != 1 {
			t.Errorf("每个会话应该只有一条消息, got %d", len(th.Messages))
		}
	}
}

func TestAggregate_MissingMessageID(t *testing.T) {
	a := msg("", 1, 10, "alice@x.com", self)
	a.Folder, a.UID = "INBOX", 1
	b := msg("", 1, 10, "bob@x.com", self)
	b.Folder, b.UID = "INBOX", 2

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 2 {
		t.Fatalf("want 2 threads, got %d", len(got))
	}
	for _, th := range got {
		if !strings.HasPrefix(th.LastMessageID(), "synthetic-") {
			t.Errorf("没有 Message-ID 的消息应该合成一个, got %q", th.LastMessageID())
		}
	}

	// 同一封邮件出现两次 —— 合成 ID 相同，必须去重成一条消息。
	got = Aggregate([]Header{a, a}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if len(got[0].Messages) != 1 {
		t.Errorf("want 1 message after dedupe, got %d", len(got[0].Messages))
	}
}

// 没有 Message-ID、又没有参与人时，会话键退化用合成 ID：两条不同的
// 消息仍然各自成会话（键不能撞）。
func TestAggregate_NoPeersAndNoMessageID(t *testing.T) {
	a := msg("", 1, 10, self, self)
	a.Folder, a.UID = "INBOX", 1
	b := msg("", 2, 10, self, self)
	b.Folder, b.UID = "INBOX", 2

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 2 {
		t.Fatalf("want 2 threads, got %d", len(got))
	}
	if got[0].ID == got[1].ID {
		t.Errorf("两个会话的 ID 撞了: %q", got[0].ID)
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
		msg("<b@x>", 2, 10, "alice@x.com", self, "bob@x.com"),
		msg("<c@x>", 3, 10, "carol@x.com", self, "alice@x.com"),
		msg("<d@x>", 4, 10, self, "dave@x.com"),
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
		b.WriteString(th.ID)
		b.WriteString("|")
		b.WriteString(strings.Join(th.MessageIDs(), ","))
		b.WriteString("|")
		b.WriteString(strings.Join(th.Peers, ","))
		b.WriteString("|")
		b.WriteString(th.Subject)
		b.WriteString("\n")
	}
	return b.String()
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
	if !reflect.DeepEqual(group[0].Peers, want) {
		t.Errorf("peers: want %v, got %v", want, group[0].Peers)
	}
}

// 一个会话里就算全是同一批人，看起来也还是 1:1 —— 群聊的判据是
// 参与人个数，不是消息条数。
func TestThread_GroupIsAboutPeersNotMessages(t *testing.T) {
	many := []Header{
		msg("<a@x>", 1, 10, "alice@x.com", self),
		msg("<b@x>", 2, 10, self, "alice@x.com"),
		msg("<c@x>", 3, 10, "alice@x.com", self),
	}
	got := Aggregate(many, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if got[0].IsGroup() {
		t.Error("只有 alice 一个人，不该判成群聊")
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
	b := msg("<b@x>", 2, 10, "alice@x.com", self)
	b.Seen = true

	got := Aggregate([]Header{a, b}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if got[0].Unread != 1 {
		t.Errorf("want Unread=1, got %d", got[0].Unread)
	}
}

func TestAggregate_ExcludesSelfFromPeers(t *testing.T) {
	got := Aggregate([]Header{
		msg("<a@x>", 1, 10, "alice@x.com", self, "ME@Example.com"),
	}, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	if want := []string{"alice@x.com"}; !reflect.DeepEqual(got[0].Peers, want) {
		t.Errorf("peers: want %v, got %v", want, got[0].Peers)
	}
}

// ---- NameOf ----

func TestThread_NameOf(t *testing.T) {
	alice := msg("<a@x>", 1, 10, "alice@x.com", self)
	alice.FromName = "Alice"
	// 后一条改成新名字 —— 界面上应该显示最新的那个。
	alice2 := msg("<b@x>", 2, 10, "alice@x.com", self)
	alice2.FromName = "Alice Chen"
	// bob 只出现在 Cc 里，从来没当过发件人，所以取不到名字。
	// （Header 只有 FromName，收件人那边不带显示名。）
	cc := msg("<c@x>", 3, 10, self, self)
	cc.Cc = []string{"bob@x.com"}

	got := Aggregate([]Header{alice, alice2, cc}, self)
	byID := map[string]Thread{}
	for _, th := range got {
		byID[th.ID] = th
	}

	if n := byID["alice@x.com"].NameOf("alice@x.com"); n != "Alice Chen" {
		t.Errorf("NameOf = %q, want %q（取了旧名字说明是从头往前找的）", n, "Alice Chen")
	}
	if n := byID["alice@x.com"].NameOf("ALICE@X.COM"); n != "Alice Chen" {
		t.Errorf("NameOf 应该忽略大小写, got %q", n)
	}
	if n := byID["alice@x.com"].NameOf("nobody@x.com"); n != "" {
		t.Errorf("会话里没有的人应该返回空串, got %q", n)
	}
	if n := byID["bob@x.com"].NameOf("bob@x.com"); n != "" {
		t.Errorf("只出现在收件人里的地址没有显示名可用, got %q", n)
	}
}

// ---- ReplyRefs ----

// 回复的引用链只能描述最后一条消息的祖先，不能把整个会话全挂上去。
//
// 这条判据守着一个会真出事的地方：会话是按参与人聚合的，和同一个人
// 来往几年的邮件可以上千封，全塞进 References 会写出几十 KB 的头部，
// 很多服务端直接拒收。
func TestThread_ReplyRefs(t *testing.T) {
	var headers []Header
	// 先堆 500 封「和 alice 的往来」。
	for i := 0; i < 500; i++ {
		headers = append(headers, msg("<fill"+strconv.Itoa(i)+"@x>", 1, i%24, "alice@x.com", self))
	}
	// 最后一条带着自己的祖先链。
	last := withRefs(msg("<last@x>", 2, 10, "alice@x.com", self), "<p1@x>", "<p2@x>")
	headers = append(headers, last)

	got := Aggregate(headers, self)
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d", len(got))
	}
	refs := got[0].ReplyRefs()

	// 只该有 last 自己的两个祖先 + 它自己。
	want := []string{"<p1@x>", "<p2@x>", "<last@x>"}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("ReplyRefs = %v, want %v", refs, want)
	}
	if len(refs) >= len(got[0].Messages) {
		t.Errorf("引用链把整个会话（%d 条）都挂上了", len(got[0].Messages))
	}
	if got[0].LastMessageID() != "<last@x>" {
		t.Errorf("LastMessageID = %q", got[0].LastMessageID())
	}
}

// 祖先链里已经含着自己时不能重复挂。
func TestThread_ReplyRefsNoDuplicate(t *testing.T) {
	last := withRefs(msg("<last@x>", 1, 10, "alice@x.com", self), "<p1@x>", "<last@x>")
	// 顺手去重祖先里的重复项。
	last.References = []string{"<p1@x>", "<p1@x>", "<last@x>"}

	got := Aggregate([]Header{last}, self)
	want := []string{"<p1@x>", "<last@x>"}
	if refs := got[0].ReplyRefs(); !reflect.DeepEqual(refs, want) {
		t.Errorf("ReplyRefs = %v, want %v", refs, want)
	}
}

func TestThread_ReplyRefsEmptyThread(t *testing.T) {
	var th Thread
	if refs := th.ReplyRefs(); refs != nil {
		t.Errorf("空会话的 ReplyRefs 应为 nil, got %v", refs)
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
