package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
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
	return newTestAppAt(t, client, t.TempDir())
}

// newTestAppAt 和 newTestAppWithClient 一样，但状态（配置 + 索引）落在
// 指定的目录里。
//
// 「重启之后还看不看得见」这类判据需要一个**能再打开一次**的目录：
// 用两次 t.TempDir() 造出来的两个 App 各写各的索引，那种测试量的其实是
// 另起一炉，跟真实重启（同一份索引读回来）不是一回事。
func newTestAppAt(t *testing.T, client mail.Client, dir string) *App {
	t.Helper()

	// 绑到临时目录：App 上有会把配置落盘的操作（比如「接收全部邮件」），
	// 不绑路径 Save 会直接报错，绑默认路径会写进用户真实的 config.json。
	cfg, err := config.LoadFrom(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	cfg.Account.Email = testSelf
	cfg.Account.DisplayName = "me"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	// 测试里用固定日期，所以关掉时间窗口裁剪，否则会被当成过期邮件丢掉。
	cfg.Sync.InitialDays = 0

	ix, err := store.Open(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return New(cfg, client, ix)
}

// newTestAppWithCacheAt 和 newTestAppAt 一样，但挂上正文的磁盘缓存。
//
// 缓存单独走一个 helper：默认那条路（newTestAppAt）**不碰磁盘**，
// 而落盘只有「重启之后还免不免费」这类判据才需要 —— 让所有判据都去写
// 一个 bodies.json，既慢又会把「缓存坏了」这类问题混进无关的红条里。
func newTestAppWithCacheAt(t *testing.T, client mail.Client, dir string) *App {
	t.Helper()
	a := newTestAppAt(t, client, dir)
	a.cache = store.OpenBodyCache(filepath.Join(dir, "bodies.json"), store.DefaultBodyCacheBytes)
	return a
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
	// 会话 ID 按参与人集合算：这一来一往只有 alice 一个对方。
	if threads[0].ID != "alice@x.com" {
		t.Errorf("ID = %q, want %q", threads[0].ID, "alice@x.com")
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

// 自己发出去的信，在服务端那份同步回来之后必须**离开「待同步」状态**，
// 而且这个状态要能挺过一次重启。
//
// 这是用户报的那个问题：「每次重开 CLI 都会出现（本地待同步）」。
// 界面按 UID == 0 判这个标记（见 tui.renderMessage），而本地乐观插入的
// 那条记录正是 UID == 0 —— 服务端 Sent 里的真副本回来时，Merge 早先只合并
// 已读标志、不补 UID，于是那个 0 永远留在索引里，还被写进磁盘。
//
// 判据量的是**用户看得见的两件事**：
//   - 重启之后那条消息的 UID 不是 0（标记不会出现）；
//   - Bodies 拉得到正文（而不是退化成一句「（本地待同步）」）。
func TestApp_SentMessageLeavesPendingStateAndStaysGone(t *testing.T) {
	fake := mail.NewFake()
	dir := t.TempDir()
	a := newTestAppAt(t, fake, dir)

	if _, err := a.Start([]string{"bob@x.com"}, "已经发出去的话"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if uid := onlyMessageUID(t, a); uid != 0 {
		t.Fatalf("前提不成立：刚发出、服务端副本还没回来的消息本该 UID=0，得到 %d", uid)
	}

	// 服务端 Sent 里那份回来了。
	changed, err := a.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !changed {
		t.Error("Sent 副本带回了 UID，索引内容变了，Sync 该报 changed —— " +
			"否则界面会一直拿着那份 UID=0 的旧快照")
	}
	if uid := onlyMessageUID(t, a); uid == 0 {
		t.Fatal("同步之后 UID 还是 0 —— 界面会一直标「本地待同步」")
	}

	// 重启：存下索引、再从那**同一份文件**打开一个新的 App。
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	restarted := newTestAppAt(t, fake, dir)
	if uid := onlyMessageUID(t, restarted); uid == 0 {
		t.Error("重启之后 UID 又变回 0 了 —— 那个标记每次开 CLI 都会回来")
	}
	// 正文也得拉得到：UID 为 0 时 app.Bodies 会直接跳过它，界面上那一封
	// 就永远只剩一句「（本地待同步）」，而不是用户自己写的话。
	th := restarted.Threads()[0]
	bodies, err := restarted.Bodies(th)
	if err != nil {
		t.Fatalf("Bodies: %v", err)
	}
	if got := bodies[th.Messages[0].MessageID].Text; !strings.Contains(got, "已经发出去的话") {
		t.Errorf("重启后正文拉不到（拿到 %q）—— 用户会在自己发的那条上看到「本地待同步」", got)
	}
}

// onlyMessageUID 取出「唯一的那个会话里唯一那条消息」的 UID。
func onlyMessageUID(t *testing.T, a *App) uint32 {
	t.Helper()
	threads := a.Threads()
	if len(threads) != 1 {
		t.Fatalf("会话数 = %d, want 1", len(threads))
	}
	if len(threads[0].Messages) != 1 {
		t.Fatalf("消息数 = %d, want 1", len(threads[0].Messages))
	}
	return threads[0].Messages[0].UID
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
	// HTML 邮件要能一路把「这是我转来的」这个事实带到界面上，否则消息头
	// 上那个标记就没有依据。同一个发件人 —— 会话按参与人集合分，换个人
	// 就落到另一个会话里去了。
	fake.AddHTMLMessage("INBOX", mail.Header{
		MessageID: "<h@x>", From: "a@x.com", To: []string{testSelf}, Date: time.Now(),
	}, "<p>HTML 正文</p>")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	bodies, err := a.Bodies(a.Threads()[0])
	if err != nil {
		t.Fatalf("Bodies: %v", err)
	}
	if got := bodies["<a@x>"]; got.Text != "正文内容" {
		t.Errorf("正文 = %q", got.Text)
	}
	if got := bodies["<a@x>"]; got.HTML {
		t.Error("纯文本正文被标成了 HTML")
	}
	if got := bodies["<h@x>"]; !got.HTML {
		t.Error("来自 text/html 的正文没被标成 HTML")
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
		got[th.ID] = true
	}
	if got["a@x.com"] {
		t.Error("一年前的邮件应被时间窗口裁掉")
	}
	if !got["b@x.com"] {
		t.Error("刚收到的邮件不该被裁掉")
	}
	if !got["c@x.com"] {
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
	ids := map[string]bool{}
	for _, th := range a.Threads() {
		ids[th.ID] = true
	}
	if !ids["alice@x.com"] {
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

// ---- 标记 / 删除 / 转发 ----

// 新建一个只有一个会话的 app，供下面几条测试复用。
func newAppWithOneThread(t *testing.T) (*App, *mail.Fake, thread.Header) {
	t.Helper()

	h := mail.Header{
		MessageID: "<a@x>", From: "alice@x.com", FromName: "Alice",
		To: []string{testSelf}, Subject: "在的",
		Date: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
		Seen: true,
	}

	fake := mail.NewFake()
	fake.AddMessage("INBOX", h, "正文内容")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return a, fake, h
}

// 标记未读必须同时改服务端和本地索引 —— 只改一边的话，要么刷新就
// 弹回未读，要么界面一直显示已读。
func TestApp_MarkUnreadHitsServerAndIndex(t *testing.T) {
	a, fake, _ := newAppWithOneThread(t)

	th := a.Threads()[0]
	if th.Unread != 0 {
		t.Fatalf("前提不成立：这条应该是已读的（Unread=%d）", th.Unread)
	}

	if err := a.MarkUnread(th); err != nil {
		t.Fatalf("MarkUnread: %v", err)
	}

	if got := a.Threads()[0].Unread; got != 1 {
		t.Errorf("本地未读数 = %d, want 1", got)
	}

	hdrs, err := fake.Headers("INBOX", 1, 0)
	if err != nil {
		t.Fatalf("读服务端状态: %v", err)
	}
	if len(hdrs) != 1 || hdrs[0].Seen {
		t.Error(`服务端的 \Seen 没被去掉`)
	}
}

func TestApp_SetStarredBothWays(t *testing.T) {
	a, fake, _ := newAppWithOneThread(t)

	th := a.Threads()[0]
	if th.IsStarred() {
		t.Fatal("前提不成立：这条不该是星标")
	}

	if err := a.SetStarred(th, true); err != nil {
		t.Fatalf("SetStarred(true): %v", err)
	}
	if !a.Threads()[0].IsStarred() {
		t.Error("本地没记上星标")
	}
	hdrs, _ := fake.Headers("INBOX", 1, 0)
	if len(hdrs) != 1 || !hdrs[0].Flagged {
		t.Error(`服务端没打上 \Flagged`)
	}

	if err := a.SetStarred(a.Threads()[0], false); err != nil {
		t.Fatalf("SetStarred(false): %v", err)
	}
	if a.Threads()[0].IsStarred() {
		t.Error("取消星标没生效")
	}
	hdrs, _ = fake.Headers("INBOX", 1, 0)
	if len(hdrs) != 1 || hdrs[0].Flagged {
		t.Error(`服务端的 \Flagged 没被去掉`)
	}
}

// 删除是「移到垃圾箱」，而且本地索引必须跟着更新。
//
// 这条判据的关键在于 Fake.Move 会给邮件分配 **新的 UID**（照真实服务端
// 的行为）。所以如果 app.Delete 忘了把本地索引里那条老记录删掉，
// 它就会带着已经失效的 UID 继续出现在列表里 —— 测试立刻红。
func TestApp_DeleteMovesToTrashAndClearsIndex(t *testing.T) {
	a, fake, h := newAppWithOneThread(t)

	th := a.Threads()[0]
	if err := a.Delete(th); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := len(a.Threads()); n != 0 {
		t.Errorf("删除后列表里还剩 %d 个会话", n)
	}

	// 服务端：INBOX 里没了，Trash 里有一份
	inbox, _ := fake.Headers("INBOX", 1, 0)
	if len(inbox) != 0 {
		t.Errorf("INBOX 里还剩 %d 封", len(inbox))
	}
	trash, _ := fake.Headers("Trash", 1, 0)
	if len(trash) != 1 {
		t.Fatalf("Trash 里有 %d 封, want 1", len(trash))
	}
	if trash[0].MessageID != h.MessageID {
		t.Errorf("Trash 里的不是同一封: %q", trash[0].MessageID)
	}
}

// 转发出去的是新会话，不能挂 References —— 挂了就会并进原来的讨论，
// 那就不叫转发了。
func TestApp_ForwardStartsNewConversation(t *testing.T) {
	a, fake, _ := newAppWithOneThread(t)

	th := a.Threads()[0]
	if _, err := a.Forward(th, []string{"bob@x.com"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	sent := fake.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("发出去了 %d 封, want 1", len(sent))
	}
	if len(sent[0].To) != 1 || sent[0].To[0] != "bob@x.com" {
		t.Errorf("收件人不对: %v", sent[0].To)
	}
	if len(sent[0].References) != 0 || sent[0].InReplyTo != "" {
		t.Errorf("转发不该挂引用链: refs=%v inReplyTo=%q",
			sent[0].References, sent[0].InReplyTo)
	}
	if !strings.HasPrefix(sent[0].Subject, "Fwd: ") {
		t.Errorf("主题没有 Fwd 前缀: %q", sent[0].Subject)
	}
	if !strings.Contains(sent[0].Body, "正文内容") {
		t.Errorf("转发正文里没有原文:\n%s", sent[0].Body)
	}
}

// 转发要拉得到正文 —— 列表模式下用户没打开过会话，缓存里是空的。
func TestApp_LastBodyTextLoadsOnDemand(t *testing.T) {
	a, _, _ := newAppWithOneThread(t)

	text, err := a.LastBodyText(a.Threads()[0])
	if err != nil {
		t.Fatalf("LastBodyText: %v", err)
	}
	if text != "正文内容" {
		t.Errorf("正文 = %q, want %q", text, "正文内容")
	}
}

// ---- 接收全部邮件 ----

// 这个开关最容易做成一个假功能：配置改了、状态栏也显示了，但历史邮件
// 一封都没多 —— 因为本地游标还停在半路，下一轮同步照旧从 LastUID+1 走。
// 所以这条判据盯的不是「配置项变成 true」，而是**邮件真的多出来了**。
//
// 数的是**消息条数**不是会话数：这六封都来自 alice，按参与人聚合是同
// 一个会话（会话数一直是 1，拿它当判据什么都测不出来）。
func TestApp_AllMailRewindsCursorAndSkipsClamp(t *testing.T) {
	fake := mail.NewFake()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		fake.AddMessage("INBOX", mail.Header{
			MessageID: fmt.Sprintf("<m%d@x>", i),
			From:      "alice@x.com", To: []string{testSelf},
			Subject: fmt.Sprintf("第 %d 封", i),
			Date:    base.Add(time.Duration(i) * time.Minute),
		}, "正文")
	}

	a := newTestApp(t, fake)
	// 把首次同步的封数上限压到 2，好让「划窗口」这件事可观测。
	a.Config().Sync.InitialMaxMessages = 2

	if a.AllMail() {
		t.Fatal("默认不该是全量模式")
	}

	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := countMessages(a.Threads()); got != 2 {
		t.Fatalf("默认模式下首次同步只该拉最近 2 封，实际 %d 封", got)
	}

	if err := a.SetAllMail(true); err != nil {
		t.Fatalf("SetAllMail(true): %v", err)
	}
	if !a.AllMail() {
		t.Error("开关没打开")
	}

	if _, err := a.Sync(); err != nil {
		t.Fatalf("第二轮 Sync: %v", err)
	}
	if got := countMessages(a.Threads()); got != 6 {
		t.Errorf("打开全量模式后应该看到 6 封，实际 %d 封 —— 游标没退回去", got)
	}
}

// countMessages 数出所有会话里一共有多少条消息。
func countMessages(threads []thread.Thread) int {
	n := 0
	for _, th := range threads {
		n += len(th.Messages)
	}
	return n
}

// 关掉全量模式不该反过来删东西：已经同步下来的邮件得留着，游标也不该动。
func TestApp_AllMailOffKeepsEverything(t *testing.T) {
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<a@x>", From: "alice@x.com", To: []string{testSelf},
		Subject: "一封", Date: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
	}, "正文")

	a := newTestApp(t, fake)
	if err := a.SetAllMail(true); err != nil {
		t.Fatalf("SetAllMail(true): %v", err)
	}
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	before := len(a.Threads())

	if err := a.SetAllMail(false); err != nil {
		t.Fatalf("SetAllMail(false): %v", err)
	}
	if a.AllMail() {
		t.Error("开关没关掉")
	}
	if got := len(a.Threads()); got != before {
		t.Errorf("关掉之后会话从 %d 变成了 %d", before, got)
	}
}

// 开关必须落到磁盘上。只在内存里改的话，重启一次用户会发现自己
// 「打开的选项自己关回去了」，然后不知道该信哪个。
func TestApp_SetAllMailPersists(t *testing.T) {
	fake := mail.NewFake()
	a := newTestApp(t, fake)
	path := a.Config().Path()

	if err := a.SetAllMail(true); err != nil {
		t.Fatalf("SetAllMail(true): %v", err)
	}

	// 重新从同一个文件读一份，模拟重启。
	reloaded, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	if !reloaded.Sync.AllMail {
		t.Error("重启之后「接收全部邮件」丢了")
	}
}

// 落盘失败时开关不能留在打开状态。
//
// 界面上那个「接收全部邮件」标记读的就是 AllMail()。如果保存失败还让它
// 留在 true，用户会看到一个说自己开着、实际既没落盘也没退游标的模式 ——
// 而这一整个功能的意义恰恰是「真的多拉邮件」，挂个假标记是最坏的结果。
func TestApp_SetAllMailSaveFailureRevertsFlag(t *testing.T) {
	fake := mail.NewFake()
	a := newTestApp(t, fake)

	// 把配置指到一个「路径存在但是个目录」的位置：os.WriteFile 必然失败，
	// 又不会牵扯到权限问题（CI 里也能稳定复现）。
	dir := filepath.Join(t.TempDir(), "not-a-file")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	a.Config().SetPath(dir)

	if err := a.SetAllMail(true); err == nil {
		t.Fatal("路径写不进去，SetAllMail 应该报错")
	}
	if a.AllMail() {
		t.Error("保存失败了，开关却停在打开状态 —— 状态栏会显示一个假的模式标记")
	}
}
