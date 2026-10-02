package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
)

const testSelf = "me@example.com"

func newTestApp(t *testing.T, fake *mail.Fake) *App {
	t.Helper()
	return newTestAppWithClient(t, fake)
}

// newTestAppWithClient 用任意 Client 实现装配一个 App。
//
// 存在的理由是注入 Fake 造不出来的行为 —— 比如"服务端上没有这个文件夹"。
func newTestAppWithClient(t *testing.T, client mail.Client) *App {
	t.Helper()

	cfg := config.Default()
	cfg.Account.Email = testSelf
	cfg.Account.DisplayName = "me"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	// 测试里用固定日期，所以关掉时间窗口裁剪，否则会被当成过期邮件丢掉。
	cfg.Sync.InitialDays = 0

	ix, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return New(cfg, client, ix)
}

func TestApp_InitialSyncAggregatesThreads(t *testing.T) {
	fake := mail.NewFake()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@x.com", To: []string{testSelf},
		Subject: "会议", Date: base,
	}, "第一句")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<b@x>", References: []string{"<a@x>"},
		From: testSelf, To: []string{"alice@x.com"},
		Subject: "Re: 会议", Date: base.Add(time.Hour),
	}, "第二句")

	a := newTestApp(t, fake)
	changed, err := a.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !changed {
		t.Error("首次同步应该报告有变化")
	}

	threads := a.Threads()
	if len(threads) != 1 {
		t.Fatalf("want 1 thread, got %d", len(threads))
	}
	if threads[0].Root != "<a@x>" {
		t.Errorf("root = %q, want <a@x>", threads[0].Root)
	}
	if got := len(threads[0].Messages); got != 2 {
		t.Errorf("want 2 messages, got %d", got)
	}
}

func TestApp_IncrementalSync(t *testing.T) {
	fake := mail.NewFake()
	base := time.Now()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "a@x.com", To: []string{testSelf}, Date: base,
	}, "一")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("首次 Sync: %v", err)
	}

	changed, err := a.Sync()
	if err != nil {
		t.Fatalf("二次 Sync: %v", err)
	}
	if changed {
		t.Error("没有新邮件时不该报告变化")
	}

	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<b@x>", From: "b@x.com", To: []string{testSelf}, Date: base.Add(time.Minute),
	}, "二")

	changed, err = a.Sync()
	if err != nil {
		t.Fatalf("三次 Sync: %v", err)
	}
	if !changed {
		t.Error("有新邮件时应该报告变化")
	}
	if got := len(a.Threads()); got != 2 {
		t.Errorf("want 2 threads, got %d", got)
	}
}

// 发出去的消息必须立刻可见，而且不能被 Sent 里的副本弄成两条。
func TestApp_SendAppearsImmediatelyAndDeduplicates(t *testing.T) {
	fake := mail.NewFake()
	a := newTestApp(t, fake)

	if _, err := a.Start([]string{"bob@x.com"}, "你好"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	threads := a.Threads()
	if len(threads) != 1 {
		t.Fatalf("发信后会话应立刻可见, got %d threads", len(threads))
	}
	if got := threads[0].Messages[0].From; got != testSelf {
		t.Errorf("发件人 = %q, want %q", got, testSelf)
	}

	// 再同步一次，Sent 里的真实副本会回来，但 Message-ID 相同。
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	threads = a.Threads()
	if len(threads) != 1 {
		t.Fatalf("同步后会话数变了: %d", len(threads))
	}
	if got := len(threads[0].Messages); got != 1 {
		t.Errorf("Sent 副本造成了重复: %d 条消息", got)
	}
}

func TestApp_ReplyBuildsHeaders(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@x.com", To: []string{testSelf},
		Subject: "会议", Date: time.Now(),
	}, "第一句")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if err := a.Reply(a.Threads()[0], "我同意", true); err != nil {
		t.Fatalf("Reply: %v", err)
	}

	sent := fake.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("want 1 sent, got %d", len(sent))
	}
	if got := sent[0].To; len(got) != 1 || got[0] != "alice@x.com" {
		t.Errorf("To = %v", got)
	}
	if sent[0].InReplyTo != "<a@x>" {
		t.Errorf("InReplyTo = %q", sent[0].InReplyTo)
	}
	if sent[0].Subject != "Re: 会议" {
		t.Errorf("Subject = %q", sent[0].Subject)
	}
}

func TestApp_ReplyAllExcludesSelf(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@x.com",
		To:      []string{testSelf, "carol@x.com"},
		Subject: "讨论", Date: time.Now(),
	}, "大家好")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	th := a.Threads()[0]
	if !th.IsGroup() {
		t.Fatal("三人会话应判为群聊")
	}
	if err := a.Reply(th, "收到", true); err != nil {
		t.Fatalf("Reply: %v", err)
	}

	sent := fake.SentMessages()[0]
	if len(sent.To) != 2 {
		t.Fatalf("Reply-All 应发给 2 个人, got %v", sent.To)
	}
	for _, addr := range sent.To {
		if addr == testSelf {
			t.Errorf("Reply-All 把自己也发进去了: %v", sent.To)
		}
	}
}

// 回复一个「最后一条是我发的」会话时，不能回给自己。
func TestApp_ReplyTargetSkipsSelf(t *testing.T) {
	fake := mail.NewFake()
	base := time.Now()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@x.com", To: []string{testSelf},
		Subject: "会议", Date: base,
	}, "第一句")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<b@x>", References: []string{"<a@x>"},
		From: testSelf, To: []string{"alice@x.com"},
		Subject: "Re: 会议", Date: base.Add(time.Hour),
	}, "第二句")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// replyAll=false：最后一条是我发的，应该回给 alice
	if err := a.Reply(a.Threads()[0], "再补一句", false); err != nil {
		t.Fatalf("Reply: %v", err)
	}

	sent := fake.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("want 1 sent, got %d", len(sent))
	}
	if got := sent[0].To; len(got) != 1 || got[0] != "alice@x.com" {
		t.Errorf("不该回给自己: To = %v", got)
	}
}

func TestApp_UIDValidityChangeResetsIndex(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "a@x.com", To: []string{testSelf}, Date: time.Now(),
	}, "一")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := len(a.Threads()); got != 1 {
		t.Fatalf("want 1 thread, got %d", got)
	}

	// 服务商重置了 UID 空间，本地游标必须整体失效。
	fake.ResetUIDValidity("INBOX")
	if _, err := a.Sync(); err != nil {
		t.Fatalf("重置后 Sync: %v", err)
	}
	if got := len(a.Threads()); got != 0 {
		t.Errorf("UIDVALIDITY 变化后索引应被清空, got %d threads", got)
	}
}

func TestApp_MarkRead(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "a@x.com", To: []string{testSelf}, Date: time.Now(),
	}, "一")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := a.Threads()[0].Unread; got != 1 {
		t.Fatalf("初始未读 = %d, want 1", got)
	}

	if err := a.MarkRead(a.Threads()[0]); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if got := a.Threads()[0].Unread; got != 0 {
		t.Errorf("标记后未读 = %d, want 0", got)
	}
}

// 发送失败必须如实返回，而且不能在本地留下一条「好像发出去了」的消息。
func TestApp_SendErrorSurfaces(t *testing.T) {
	fake := mail.NewFake()
	fake.SetSendError(errors.New("smtp 挂了"))

	a := newTestApp(t, fake)
	if _, err := a.Start([]string{"bob@x.com"}, "你好"); err == nil {
		t.Fatal("发送失败必须返回错误，不能静默吞掉")
	}
	if got := len(a.Threads()); got != 0 {
		t.Errorf("发送失败不该在本地留下消息, got %d threads", got)
	}
}

func TestApp_Bodies(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "a@x.com", To: []string{testSelf}, Date: time.Now(),
	}, "正文内容")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	bodies, err := a.Bodies(a.Threads()[0])
	if err != nil {
		t.Fatalf("Bodies: %v", err)
	}
	if got := bodies["<a@x>"]; got != "正文内容" {
		t.Errorf("正文 = %q", got)
	}
}

// 老邮件要按时间窗口裁掉，但 Date 缺失的要保守保留。
func TestApp_InitialWindowFiltersByAge(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<old@x>", From: "a@x.com", To: []string{testSelf},
		Date: time.Now().AddDate(0, 0, -365),
	}, "一年前")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<new@x>", From: "b@x.com", To: []string{testSelf},
		Date: time.Now().Add(-time.Hour),
	}, "刚刚")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<nodate@x>", From: "c@x.com", To: []string{testSelf},
	}, "没有日期")

	a := newTestApp(t, fake)
	a.cfg.Sync.InitialDays = 90

	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := map[string]bool{}
	for _, th := range a.Threads() {
		got[th.Root] = true
	}
	if got["<old@x>"] {
		t.Error("一年前的邮件应被时间窗口裁掉")
	}
	if !got["<new@x>"] {
		t.Error("刚收到的邮件不该被裁掉")
	}
	if !got["<nodate@x>"] {
		t.Error("Date 缺失的邮件应保守保留，而不是被丢掉")
	}
}

// missingFolderClient 让某个文件夹像真实服务端那样"不存在"。
//
// 真实场景：配置里写的是 "Sent"，但网易系（163 / 126）上这个文件夹叫
// 「已发送」，EXAMINE 会失败。这里只模拟"不存在"这一个行为 ——
// 名字解析本身在 mail 包里有单测，不在这里重复。
type missingFolderClient struct {
	mail.Client
	missing string
}

func (c missingFolderClient) Folder(name string) (mail.Folder, error) {
	if name == c.missing {
		return mail.Folder{}, fmt.Errorf("%w: %q", mail.ErrNoSuchFolder, name)
	}
	return c.Client.Folder(name)
}

// 服务端上少一个文件夹，不该让整个 Sync 报错。
//
// 之前的行为是把它和真正的连接错误一视同仁，结果界面整个标成"离线"，
// 反而盖住了 INBOX 其实同步成功了这件事。
func TestSync_MissingFolderIsNotAnError(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@x.com", To: []string{testSelf},
		Subject: "在的", Date: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
	}, "正文")

	a := newTestAppWithClient(t, missingFolderClient{Client: fake, missing: "Sent"})

	changed, err := a.Sync()
	if err != nil {
		t.Errorf("缺一个文件夹不该报错，实际: %v", err)
	}
	if !changed {
		t.Error("INBOX 里那封新邮件应该让 changed 为 true")
	}

	// 更要紧的是：不能因为 Sent 缺失就把 INBOX 的同步结果丢掉。
	roots := map[string]bool{}
	for _, th := range a.Threads() {
		roots[th.Root] = true
	}
	if !roots["<a@x>"] {
		t.Error("INBOX 里的会话没进索引 —— 缺的文件夹把同步结果带走了")
	}
}

// 反面对照：真正的连接错误必须照报，别被上面那条"跳过"逻辑一起吞掉。
func TestSync_RealErrorStillReported(t *testing.T) {
	a := newTestAppWithClient(t, failingClient{Client: mail.NewFake()})

	if _, err := a.Sync(); err == nil {
		t.Error("连接层真出错时必须报出来，不能被跳过逻辑吞掉")
	}
}

// failingClient 的 Folder 永远返回一个普通错误（非 ErrNoSuchFolder）。
type failingClient struct {
	mail.Client
}

func (failingClient) Folder(string) (mail.Folder, error) {
	return mail.Folder{}, errors.New("连接被拒绝")
}
