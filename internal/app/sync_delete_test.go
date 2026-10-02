package app

import (
	"errors"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
)

// 这个文件钉住「本地索引不再只增不减」。
//
// 背景：增量同步是 from = LastUID+1，只看得见新邮件。服务端删掉一封
// （在手机上删、被服务端规则移走、在别处归档）本地永远不知道，列表上
// 于是留一条打不开的幽灵 —— 打开它只会得到「在 INBOX 里找不到 UID 7」。
//
// 治本是「列出全部 UID 和服务端对账」，但那一次要传回整个文件夹的 UID 表
// （五千封三十来 KB），而同步是分钟级的。所以只在**封数下降**时对账。
// 下面四条判据分别钉住这条链上的四件事：删了要发现、没变别乱发、乐观副本
// 不能误伤、失败了要重试。

// uidsCountingClient 记录 app 层发起了几次「列全部 UID」，并能让它失败。
type uidsCountingClient struct {
	mail.Client

	calls int
	fail  bool
}

func (c *uidsCountingClient) UIDs(folder string) ([]uint32, error) {
	c.calls++
	if c.fail {
		return nil, errors.New("模拟对账失败")
	}
	return c.Client.UIDs(folder)
}

// seedInbox 往 INBOX 塞 n 封不同发件人的邮件（各自成一个会话），
// 返回假客户端和它们的 UID。
func seedInbox(n int) (*mail.Fake, []uint32) {
	fake := mail.NewFake()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	uids := make([]uint32, 0, n)
	for i := 0; i < n; i++ {
		uid := fake.AddMessage("INBOX", mail.Header{
			MessageID: mailID(i),
			From:      "peer" + string(rune('a'+i)) + "@x.com",
			To:        []string{testSelf},
			Subject:   "第" + string(rune('a'+i)) + "封",
			Date:      base.Add(time.Duration(i) * time.Minute),
			Seen:      true,
		}, "正文")
		uids = append(uids, uid)
	}
	return fake, uids
}

func mailID(i int) string {
	return "<seed" + string(rune('a'+i)) + "@x>"
}

// 服务端上删掉的邮件，下一轮同步要从本地索引里消失。
func TestSync_DropsMessagesDeletedOnTheServer(t *testing.T) {
	fake, uids := seedInbox(2)

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("首次 Sync: %v", err)
	}
	if n := len(a.index.All()); n != 2 {
		t.Fatalf("前提不成立：首次同步后索引里 %d 条，想要 2", n)
	}

	// 别人在手机上删了第一封。
	fake.DeleteOnServer("INBOX", uids[0])

	changed, err := a.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !changed {
		t.Error("删了邮件却报「没有变化」—— 界面不会重绘，幽灵会一直留在屏幕上")
	}

	left := a.index.All()
	if len(left) != 1 {
		t.Fatalf("对账后索引里还有 %d 条，想要 1", len(left))
	}
	if left[0].MessageID != mailID(1) {
		t.Errorf("留下的是 %q，想要 %q", left[0].MessageID, mailID(1))
	}
}

// 封数没变就不该对账。
//
// 这条是**代价**判据：对账一次要传回整个文件夹的 UID 表，而同步是分钟级的。
// 每轮都做的话，一个五千封的收件箱就是每分钟三十来 KB —— 而它什么都不会
// 发现（没东西被删）。少了这条判据，把判断删掉照样全绿。
func TestSync_DoesNotReconcileWhenNothingChanged(t *testing.T) {
	fake, _ := seedInbox(3)

	counted := &uidsCountingClient{Client: fake}
	a := newTestAppWithClient(t, counted)

	// 第一轮：还没观察过封数（prevCount == 0），不对账，只把封数记下来。
	if _, err := a.Sync(); err != nil {
		t.Fatalf("首次 Sync: %v", err)
	}
	if counted.calls != 0 {
		t.Errorf("第一次同步就发了 %d 次对账 —— 没有「上次封数」可比，无从判断少了东西",
			counted.calls)
	}

	// 第二轮：封数没变，仍然不该对账。
	if _, err := a.Sync(); err != nil {
		t.Fatalf("第二次 Sync: %v", err)
	}
	if counted.calls != 0 {
		t.Errorf("封数没变却发了 %d 次对账 —— 每轮都在传整个文件夹的 UID 表", counted.calls)
	}
}

// 封数**上升**也不该对账：那是「来了新邮件」，增量拉取本来就管这件事。
//
// 对账只认「下降」一个方向。少了这条，把条件写成 `!=` 也照样全绿 ——
// 而那样一来每来一封新邮件就多传一次整个文件夹的 UID 表，正是要避免的。
func TestSync_NewMailDoesNotTriggerReconcile(t *testing.T) {
	fake, _ := seedInbox(2)

	counted := &uidsCountingClient{Client: fake}
	a := newTestAppWithClient(t, counted)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("首次 Sync: %v", err)
	}
	counted.calls = 0

	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<arrived@x>", From: "new@x.com", To: []string{testSelf},
		Subject: "刚到的", Date: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC), Seen: true,
	}, "正文")

	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if counted.calls != 0 {
		t.Errorf("新邮件到达触发了 %d 次对账 —— 对账只该认封数下降", counted.calls)
	}
}

// 本地刚发出、服务端还没存下的那一封（UID == 0）不能参与对账。
//
// 它在服务端上本来就还没有 UID，按「不在服务端的 UID 表里」删掉，用户刚
// 发出去的那封信会从列表里凭空消失，而且没有任何错误提示。
func TestSync_KeepsOptimisticLocalCopies(t *testing.T) {
	fake := mail.NewFake()
	old := fake.AddMessage("Sent", mail.Header{
		MessageID: "<old-sent@x>", From: testSelf, To: []string{"bob@x.com"},
		Subject: "以前发的", Date: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Seen: true,
	}, "正文")

	a := newTestApp(t, fake)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("首次 Sync: %v", err)
	}

	// 模拟「刚发出、服务端还没存进 Sent」的那一封：UID == 0。
	//
	// 这里直接往索引里插，而不是走 a.Start() —— 走真实那条路的话，紧接着
	// 的那次同步就会把服务端副本的 UID 补上（见 store.Merge），UID 不再是 0，
	// 这条规则也就量不到了。真实世界里它确实会是 0：发送成功到服务端把它
	// 存进 Sent 之间有一段窗口。
	a.index.Merge([]mail.Header{{
		MessageID: "<just-sent@x>", Folder: "Sent", From: testSelf,
		To: []string{"bob@x.com"}, Subject: "刚发的",
		Date: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), Seen: true,
	}})

	// 逼出一次对账：服务端上 Sent 少了东西。
	fake.DeleteOnServer("Sent", old)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var haveJustSent bool
	for _, h := range a.index.All() {
		if h.MessageID == "<just-sent@x>" {
			haveJustSent = true
		}
		if h.MessageID == "<old-sent@x>" {
			t.Error("服务端上已经删掉的那封还在索引里 —— 对账没生效")
		}
	}
	if !haveJustSent {
		t.Error("UID == 0 的乐观副本被对账删掉了 —— 用户刚发出去的信会凭空消失")
	}
}

// 对账失败之后，下一轮必须**重试**，哪怕封数没有再变。
//
// 失败时如果把新的封数记下来，这个信号就被用掉了 —— 本地还留着幽灵，
// 却要等到下一次封数下降才有机会再对账，而那可能很久以后。
func TestSync_RetriesReconcileAfterFailure(t *testing.T) {
	fake, uids := seedInbox(2)

	counted := &uidsCountingClient{Client: fake}
	a := newTestAppWithClient(t, counted)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("首次 Sync: %v", err)
	}

	fake.DeleteOnServer("INBOX", uids[0])

	counted.fail = true
	if _, err := a.Sync(); err != nil {
		t.Fatalf("对账失败不该让整个同步报错: %v", err)
	}
	afterFail := counted.calls
	if afterFail == 0 {
		t.Fatal("前提不成立：封数下降了却没去对账，这条判据量的是失败之后的重试")
	}

	counted.fail = false
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if counted.calls == afterFail {
		t.Error("对账失败之后没有再试 —— 封数没再变，幽灵就一直留着")
	}
	if n := len(a.index.All()); n != 1 {
		t.Errorf("重试之后索引里还有 %d 条，想要 1", n)
	}
}

// ---- store 那一侧的规则 ----

// 对账只在**同一个文件夹内**比，而且跳过 UID == 0。
//
// 两件事都写在这里，是因为它们都是「拿服务端的 UID 表去比」时才成立的
// 前提：UID 只在文件夹内唯一（拿收件箱的表去对已发送的条目会把两个文件夹
// 全删光），而 UID == 0 的条目在服务端上压根没有 UID。
func TestIndex_RemoveMissing_OnlyTouchesThatFolderAndSkipsZeroUIDs(t *testing.T) {
	ix, err := store.Open(t.TempDir() + "/index.json")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ix.Merge([]mail.Header{
		{MessageID: "<a@x>", Folder: "INBOX", UID: 7},
		{MessageID: "<b@x>", Folder: "INBOX", UID: 8},
		{MessageID: "<sent@x>", Folder: "Sent", UID: 7}, // 和收件箱那封同 UID
		{MessageID: "<local@x>", Folder: "INBOX", UID: 0},
	})

	// 服务端说收件箱现在只剩 UID 8。
	removed := ix.RemoveMissing("INBOX", []uint32{8})
	if removed != 1 {
		t.Errorf("删掉 %d 条，想要 1", removed)
	}

	var ids []string
	for _, h := range ix.All() {
		ids = append(ids, h.MessageID)
	}
	want := map[string]bool{"<b@x>": true, "<sent@x>": true, "<local@x>": true}
	if len(ids) != len(want) {
		t.Fatalf("剩下 %v，想要 %v", ids, want)
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("%q 不该被删 —— 要么跨了文件夹，要么是 UID == 0 的乐观副本", id)
		}
	}
}
