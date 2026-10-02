package diagnose

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// stubClient 复用假的客户端，只把需要出错的那几个方法换成可注入的。
//
// 嵌入 *mail.Fake 是为了拿到其余七个方法的实现 —— 手写一个完整的
// mail.Client 桩会有一百多行和被测代码无关的样板。
type stubClient struct {
	*mail.Fake

	// folderErr 让所有 Folder 调用都失败（模拟登录不过）。
	folderErr error
	// headersErr 让 Headers 失败（模拟登录过了但 FETCH 不通）。
	headersErr error
	// sentMissing 让只有 Folder("Sent") 失败，用来验证③不算故障。
	sentMissing bool
}

func (s stubClient) Folder(name string) (mail.Folder, error) {
	if s.folderErr != nil {
		return mail.Folder{}, s.folderErr
	}
	if s.sentMissing && name != "INBOX" {
		return mail.Folder{}, mail.ErrNoSuchFolder
	}
	return s.Fake.Folder(name)
}

func (s stubClient) Headers(folder string, from, to uint32) ([]mail.Header, error) {
	if s.headersErr != nil {
		return nil, s.headersErr
	}
	return s.Fake.Headers(folder, from, to)
}

// seeded 造一个装了 n 封邮件的假客户端，其中带 References 的比例可控。
func seeded(n, withRefs int) *mail.Fake {
	f := mail.NewFake()
	for i := 0; i < n; i++ {
		h := mail.Header{
			MessageID: strings.Repeat("m", 1) + string(rune('a'+i%26)),
			From:      "someone@example.com",
			Subject:   "第 " + string(rune('0'+i%10)) + " 封",
			Date:      time.Now().Add(-time.Duration(i) * time.Hour),
			To:        []string{"me@example.com"},
		}
		if i < withRefs {
			h.References = []string{"<root@example.com>"}
		}
		f.AddMessage("INBOX", h, "正文不该被自检读到")
	}
	return f
}

// 全通的情况：每一步都要留下可断言的事实。
func TestRun_AllGood(t *testing.T) {
	f := seeded(8, 3)
	r := Run(stubClient{Fake: f}, Options{Provider: "测试", Email: "me@example.com"})

	if !r.OK() {
		t.Fatalf("全部正常时不该失败，FailedAt=%d err=%v", r.FailedAt, r.Err)
	}
	if r.Inbox == nil {
		t.Fatal("①应该拿到 INBOX 状态")
	}
	if r.Inbox.Messages != 8 {
		t.Errorf("INBOX 封数 = %d, want 8", r.Inbox.Messages)
	}
	if r.Sent == nil || r.SentErr != nil {
		t.Errorf("③应该探到已发送文件夹，Sent=%v err=%v", r.Sent, r.SentErr)
	}
}

// ②只抽最近 sampleSize 条，不是把整个 INBOX 拉下来。
//
// 这条守的是自检的**代价**：它得能随手跑，不该因为邮箱里有几千封
// 就变成一次全量同步。
func TestRun_HeadersOnlySamplesTail(t *testing.T) {
	f := seeded(400, 0)
	r := Run(stubClient{Fake: f}, Options{})

	if !r.OK() {
		t.Fatalf("不该失败: %v", r.Err)
	}
	if got := len(r.Headers); got != sampleSize {
		t.Errorf("抽到的头部数 = %d, want %d（①的 UIDNext=%d）",
			got, sampleSize, r.Inbox.UIDNext)
	}
}

// 邮箱里没几封信时不该拉出负数区间 —— from 的钳位。
func TestRun_SmallMailboxSamplesEverything(t *testing.T) {
	f := seeded(2, 0)
	r := Run(stubClient{Fake: f}, Options{})

	if !r.OK() {
		t.Fatalf("不该失败: %v", r.Err)
	}
	if got := len(r.Headers); got != 2 {
		t.Errorf("抽到的头部数 = %d, want 2", got)
	}
}

// 登录失败必须停在①，而且不能继续往下跑 —— 后面几步都会连累报错，
// 那些报错只会掩盖真正的原因。
func TestRun_LoginFailureStopsAtStep1(t *testing.T) {
	sentinel := errors.New("服务端拒绝了登录（模拟）")
	r := Run(stubClient{Fake: mail.NewFake(), folderErr: sentinel}, Options{})

	if r.FailedAt != StepLogin {
		t.Errorf("FailedAt = %d, want %d(StepLogin)", r.FailedAt, StepLogin)
	}
	if !errors.Is(r.Err, sentinel) {
		t.Errorf("Err = %v, want 它包着 %v", r.Err, sentinel)
	}
	if r.Inbox != nil {
		t.Error("①没过就不该有 INBOX 状态")
	}
	if len(r.Headers) != 0 {
		t.Error("①没过就不该去拉头部")
	}
}

// 登录过了但拉头部失败：要能区分出这是②，不是①。
// 两者的排查方向完全不同（授权码 vs 限流），混在一起等于没报。
func TestRun_HeadersFailureStopsAtStep2(t *testing.T) {
	sentinel := errors.New("FETCH 被拒（模拟）")
	r := Run(stubClient{Fake: mail.NewFake(), headersErr: sentinel}, Options{})

	if r.FailedAt != StepHeaders {
		t.Errorf("FailedAt = %d, want %d(StepHeaders)", r.FailedAt, StepHeaders)
	}
	if !errors.Is(r.Err, sentinel) {
		t.Errorf("Err = %v, want 它包着 %v", r.Err, sentinel)
	}
	if r.Inbox == nil {
		t.Error("①是过了的，应该留下 INBOX 状态")
	}
}

// 服务端上没有「已发送」文件夹**不算故障**。
//
// 各家对它叫法不同（Sent / 已发送 / Sent Messages…），别名表也可能
// 没覆盖到。收信完全正常，只是看不到自己发出去的信 —— 绝不能因此
// 让整个自检报红。
func TestRun_MissingSentFolderIsNotFailure(t *testing.T) {
	f := seeded(3, 1)
	r := Run(stubClient{Fake: f, sentMissing: true}, Options{})

	if !r.OK() {
		t.Errorf("缺「已发送」不该算失败，FailedAt=%d err=%v", r.FailedAt, r.Err)
	}
	if r.SentErr == nil {
		t.Error("应该把「找不到该文件夹」记下来，而不是吞掉")
	}
	if r.Sent != nil {
		t.Error("没探到时不该给出文件夹状态")
	}
}

// ③不能失败，所以它必须在②之后、且不影响①②的结果。
func TestRun_SentProbeDoesNotMaskInbox(t *testing.T) {
	f := seeded(5, 0)
	r := Run(stubClient{Fake: f, sentMissing: true}, Options{})

	if r.Inbox == nil || r.Inbox.Messages != 5 {
		t.Errorf("①的结果被③影响了: %+v", r.Inbox)
	}
	if len(r.Headers) != 5 {
		t.Errorf("②的结果被③影响了: %d 条", len(r.Headers))
	}
}

// Deep 打开时才跑④，且要真的产出耗时和条数。
func TestRun_DeepMeasures(t *testing.T) {
	f := seeded(30, 0)

	shallow := Run(stubClient{Fake: f}, Options{})
	if shallow.DeepCount != 0 || shallow.DeepTook != 0 {
		t.Errorf("不开 Deep 不该跑④: count=%d took=%v", shallow.DeepCount, shallow.DeepTook)
	}

	deep := Run(stubClient{Fake: f}, Options{Deep: true, InitialLimit: 30})
	if !deep.OK() {
		t.Fatalf("不该失败: %v", deep.Err)
	}
	if deep.DeepCount != 30 {
		t.Errorf("④拉到的条数 = %d, want 30", deep.DeepCount)
	}
	// 刻意**不**断言 DeepTook > 0：假的客户端是内存里的，整批拉完可能
	// 快过系统计时器的精度，耗时真的会是 0。那样这条判据红不红就取决
	// 于机器，而不是被测的代码 —— 一条会间歇变红的判据等于没有信息。
	// 耗时的意义在真账号上（量级是几十毫秒/条），这里只保证它不为负。
	if deep.DeepTook < 0 {
		t.Errorf("耗时不该是负的: %v", deep.DeepTook)
	}
}

// InitialLimit 没给时要有个兜底值，不能把 0 传给服务端。
func TestRun_DeepDefaultsLimit(t *testing.T) {
	f := seeded(4, 0)
	r := Run(stubClient{Fake: f}, Options{Deep: true})

	if !r.OK() {
		t.Fatalf("不该失败: %v", r.Err)
	}
	if r.DeepCount != 4 {
		t.Errorf("④拉到的条数 = %d, want 4（总共只有 4 封）", r.DeepCount)
	}
}

// Stats 的三个数是线程质量诊断的依据，单独测一遍。
func TestReport_Stats(t *testing.T) {
	r := &Report{Headers: []mail.Header{
		{Subject: "有主题", Date: time.Now(), References: []string{"<a>"}},
		{Subject: "  ", Date: time.Now()},             // 空白主题算空
		{Subject: "无日期", References: []string{"<b>"}}, // 零值时间算缺日期
	}}

	s := r.Stats()
	if s.Total != 3 {
		t.Errorf("Total = %d, want 3", s.Total)
	}
	if s.WithDate != 2 {
		t.Errorf("WithDate = %d, want 2", s.WithDate)
	}
	if s.WithRefs != 2 {
		t.Errorf("WithRefs = %d, want 2", s.WithRefs)
	}
	if s.EmptySubject != 1 {
		t.Errorf("EmptySubject = %d, want 1", s.EmptySubject)
	}
}

// ⚠️ 这条守的是自检的**隐私承诺**。
//
// 报告会被用户直接贴进 issue。只要它带上主题或发件人地址，就等于
// 把邮件内容泄出去了。这里塞进不可能自然出现的哨兵字符串，逐个
// 确认它们没出现在任何一个输出路径上。
func TestRender_NeverLeaksMessageContent(t *testing.T) {
	const (
		secretSubject = "哨兵主题-9f3a2b"
		secretFrom    = "sentinel-9f3a2b@example.com"
		secretBody    = "哨兵正文-9f3a2b"
		secretMsgID   = "<sentinel-9f3a2b@example.com>"
	)

	f := mail.NewFake()
	f.AddMessage("INBOX", mail.Header{
		MessageID:  secretMsgID,
		From:       secretFrom,
		FromName:   "哨兵",
		Subject:    secretSubject,
		Date:       time.Now(),
		References: []string{secretMsgID},
	}, secretBody)

	// 三条输出路径都要查：全通、失败、以及 Deep。
	for _, tc := range []struct {
		name string
		r    *Report
	}{
		{"全部通过", Run(stubClient{Fake: f}, Options{Email: "me@example.com"})},
		{"①失败", Run(stubClient{Fake: mail.NewFake(), folderErr: errors.New("boom")}, Options{})},
		{"②失败", Run(stubClient{Fake: f, headersErr: errors.New("boom")}, Options{})},
		{"Deep", Run(stubClient{Fake: f}, Options{Deep: true, InitialLimit: 10})},
	} {
		var buf bytes.Buffer
		tc.r.Render(&buf)
		out := buf.String()

		for label, needle := range map[string]string{
			"主题":         secretSubject,
			"发件人":        secretFrom,
			"正文":         secretBody,
			"Message-ID": secretMsgID,
		} {
			if strings.Contains(out, needle) {
				t.Errorf("%s 的输出里泄露了%s：\n%s", tc.name, label, out)
			}
		}
	}
}

// 失败时报告里要有能拿去搜索的错误原文 —— 否则用户贴出来也没人看得懂。
func TestRender_FailureCarriesUnderlyingError(t *testing.T) {
	sentinel := errors.New("授权码无效（模拟）")
	r := Run(stubClient{Fake: mail.NewFake(), folderErr: sentinel}, Options{})

	var buf bytes.Buffer
	r.Render(&buf)

	if !strings.Contains(buf.String(), sentinel.Error()) {
		t.Errorf("报告里没有底层错误原文：\n%s", buf.String())
	}
}

// 失败的报告不能自称通过。
func TestRender_FailureDoesNotClaimSuccess(t *testing.T) {
	r := Run(stubClient{Fake: mail.NewFake(), folderErr: errors.New("boom")}, Options{})

	var buf bytes.Buffer
	r.Render(&buf)

	if strings.Contains(buf.String(), "全部通过") {
		t.Errorf("失败的报告不该出现「全部通过」：\n%s", buf.String())
	}
	if r.OK() {
		t.Error("OK() 应该是 false")
	}
}

// Render 在任何结果下都不能 panic —— 它要在出错路径上被调用。
func TestRender_NeverPanics(t *testing.T) {
	for _, r := range []*Report{
		{},
		{Options: Options{Deep: true}, Inbox: &mail.Folder{}},
		Run(stubClient{Fake: mail.NewFake()}, Options{Deep: true}),
	} {
		var buf bytes.Buffer
		r.Render(&buf) // 只要求不 panic
	}
}
