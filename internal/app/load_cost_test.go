package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这个文件钉住的是「取正文的代价不随邮件条数增长」。
//
// 它和 mail 包里那把数**协议命令**的尺子分工不同，两把都不能少：
//
//   - mail/roundtrip_test.go 钉「一次批量 = 一条 FETCH」；
//   - 这里钉「app 不会把一封邮件算成一次批量」。
//
// 少了这一把，把 app.Bodies 改回逐封循环，mail 那边照样全绿 —— 因为它
// 量的是每个文件夹各发了多少条命令，而逐封循环每封都会重新选中同一个
// 文件夹，那边只会看到「命令变多了」，看不出是 app 层的错。

// countingClient 记录 app 层发起了几次批量取正文、每次带了多少 UID。
//
// 内嵌 mail.Client 拿到其余方法的转发，只有 Bodies 被拦下来计数。
type countingClient struct {
	mail.Client

	batches []string // 每次批量请求的文件夹名，按发生顺序
	uids    int      // 请求过的 UID 总数（去重前的原始个数）
}

func (c *countingClient) Bodies(folder string, uids []uint32) (map[uint32]mail.Message, error) {
	c.batches = append(c.batches, folder)
	c.uids += len(uids)
	return c.Client.Bodies(folder, uids)
}

// reset 把计数清零，好让一段被测行为独占一张计数表。
func (c *countingClient) reset() {
	c.batches = nil
	c.uids = 0
}

// seedThread 往 INBOX 塞 n 封同一个会话的邮件，返回假客户端。
//
// 同一个发件人 + 同一个收件人 = 同一组参与人，聚合时落进同一个会话
// （会话是按参与人集合分的，见 thread.Aggregate）。
func seedThread(n int) *mail.Fake {
	fake := mail.NewFake()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		fake.AddMessage("INBOX", mail.Header{
			MessageID: fmt.Sprintf("<m%d@x>", i),
			From:      "alice@x.com",
			To:        []string{testSelf},
			Subject:   fmt.Sprintf("第%d封", i),
			Date:      base.Add(time.Duration(i) * time.Minute),
		}, fmt.Sprintf("第%d封的正文", i))
	}
	return fake
}

// openSoleThread 同步一次并返回唯一的那个会话。
func openSoleThread(t *testing.T, a *App) thread.Thread {
	t.Helper()

	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	ths := a.Threads()
	if len(ths) != 1 {
		t.Fatalf("会话数 = %d，想要 1", len(ths))
	}
	return ths[0]
}

// TestBodies_OneBatchForTheWholeThread：30 封同文件夹的邮件只需**一次**
// 批量请求。改之前是 30 次（每次还得付 NOOP + SELECT 的代价）。
func TestBodies_OneBatchForTheWholeThread(t *testing.T) {
	const n = 30

	counter := &countingClient{Client: seedThread(n)}
	a := newTestAppWithClient(t, counter)
	th := openSoleThread(t, a)

	if len(th.Messages) != n {
		t.Fatalf("会话里有 %d 封，想要 %d", len(th.Messages), n)
	}

	counter.reset()
	bodies, err := a.Bodies(th)
	if err != nil {
		t.Fatalf("Bodies: %v", err)
	}

	if len(bodies) != n {
		t.Errorf("拿到 %d 封正文，想要 %d", len(bodies), n)
	}
	if len(counter.batches) != 1 {
		t.Errorf("%d 封邮件发了 %d 次批量请求（%v），要 1 次 —— 又退回逐封了",
			n, len(counter.batches), counter.batches)
	}
	if counter.uids != n {
		t.Errorf("批量里带了 %d 个 UID，想要 %d", counter.uids, n)
	}
}

// TestBodies_OneBatchPerFolder：会话跨越两个文件夹（收件箱 + 已发送）时，
// 请求次数按**文件夹**算，不按邮件数算。
//
// 分组不是可选的优化：IMAP 的 FETCH 只作用于当前选中的那一个文件夹，
// 所以「一次批量」最多只能覆盖一个文件夹。
func TestBodies_OneBatchPerFolder(t *testing.T) {
	fake := mail.NewFake()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

	// 3 封对方发来的（收件箱）+ 2 封自己发的（已发送）。
	// 参与人集合都是 {alice, me}，所以聚合成同一个会话。
	for i := 0; i < 3; i++ {
		fake.AddMessage("INBOX", mail.Header{
			MessageID: fmt.Sprintf("<in%d@x>", i),
			From:      "alice@x.com",
			To:        []string{testSelf},
			Subject:   fmt.Sprintf("对方第%d封", i),
			Date:      base.Add(time.Duration(i) * time.Minute),
		}, fmt.Sprintf("对方第%d封的正文", i))
	}
	for i := 0; i < 2; i++ {
		fake.AddMessage("Sent", mail.Header{
			MessageID: fmt.Sprintf("<out%d@x>", i),
			From:      testSelf,
			To:        []string{"alice@x.com"},
			Subject:   fmt.Sprintf("我第%d封", i),
			Date:      base.Add(time.Duration(10+i) * time.Minute),
		}, fmt.Sprintf("我第%d封的正文", i))
	}

	counter := &countingClient{Client: fake}
	a := newTestAppWithClient(t, counter)
	th := openSoleThread(t, a)

	if len(th.Messages) != 5 {
		t.Fatalf("会话里有 %d 封，想要 5", len(th.Messages))
	}

	counter.reset()
	bodies, err := a.Bodies(th)
	if err != nil {
		t.Fatalf("Bodies: %v", err)
	}
	if len(bodies) != 5 {
		t.Errorf("拿到 %d 封正文，想要 5", len(bodies))
	}
	if len(counter.batches) != 2 {
		t.Fatalf("批量请求 %d 次（%v），想要 2 次（每个文件夹一次）",
			len(counter.batches), counter.batches)
	}
	if got := fmt.Sprint(counter.batches); got != "[INBOX Sent]" && got != "[Sent INBOX]" {
		t.Errorf("批量请求的文件夹是 %v，想要 INBOX 和 Sent 各一次", counter.batches)
	}
}

// TestBodies_SecondCallHitsTheCache：第二次打开同一个会话不该再发请求。
//
// ⚠️ 这条判据守的是**缓存**这一半：批量让第一次便宜了，缓存让第二、
// 第三次免费。少了它，可以把批量做对、却把缓存做丢 —— 那种情况下
// 「上下翻会话」每次都要重新下载全部正文。
func TestBodies_SecondCallHitsTheCache(t *testing.T) {
	const n = 8

	counter := &countingClient{Client: seedThread(n)}
	a := newTestAppWithClient(t, counter)
	th := openSoleThread(t, a)

	if _, err := a.Bodies(th); err != nil {
		t.Fatalf("首次 Bodies: %v", err)
	}

	counter.reset()
	bodies, err := a.Bodies(th)
	if err != nil {
		t.Fatalf("二次 Bodies: %v", err)
	}
	if len(bodies) != n {
		t.Errorf("二次读到 %d 封正文，想要 %d —— 缓存没命中", len(bodies), n)
	}
	if len(counter.batches) != 0 {
		t.Errorf("二次打开又发了 %d 次批量请求（%v），要 0 次", len(counter.batches), counter.batches)
	}
}

// TestLastBodyTexts_BatchesAcrossThreads：列表副行要一批不同会话的摘要，
// 也必须**合并成一次**请求 —— 它们都在收件箱，只是不属于同一个会话。
//
// 这条路是列表滚动时走的那条（见 tui.wantedPreviews），逐封取的话
// 滚一屏就是几十次往返，换来的只是右边一行小字。
func TestLastBodyTexts_BatchesAcrossThreads(t *testing.T) {
	fake := mail.NewFake()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

	// 8 个**不同**的会话（每个换一个发件人），全部落在收件箱。
	const n = 8
	for i := 0; i < n; i++ {
		fake.AddMessage("INBOX", mail.Header{
			MessageID: fmt.Sprintf("<t%d@x>", i),
			From:      fmt.Sprintf("sender%d@x.com", i),
			To:        []string{testSelf},
			Subject:   fmt.Sprintf("话题%d", i),
			Date:      base.Add(time.Duration(i) * time.Minute),
		}, fmt.Sprintf("话题%d的最新一句", i))
	}

	counter := &countingClient{Client: fake}
	a := newTestAppWithClient(t, counter)
	if _, err := a.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	ths := a.Threads()
	if len(ths) != n {
		t.Fatalf("会话数 = %d，想要 %d", len(ths), n)
	}

	// 取每个会话的最后一条消息 —— 和 tui 那边要摘要时给的是同一批东西。
	lasts := make([]thread.Header, 0, n)
	for _, th := range ths {
		lasts = append(lasts, th.Messages[len(th.Messages)-1])
	}

	counter.reset()
	texts := a.LastBodyTexts(lasts)

	if len(texts) != n {
		t.Errorf("拿到 %d 条摘要，想要 %d", len(texts), n)
	}
	if len(counter.batches) != 1 {
		t.Errorf("%d 个会话的摘要发了 %d 次批量请求（%v），要 1 次 —— 又退回逐封了",
			n, len(counter.batches), counter.batches)
	}
	if counter.uids != n {
		t.Errorf("批量里带了 %d 个 UID，想要 %d", counter.uids, n)
	}
}
