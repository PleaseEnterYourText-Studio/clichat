// Package diagnose 对邮箱连接做一次**只读**自检，回答一个具体问题：
// 「连不上，到底断在哪一步」。
//
// 它刻意不做的事：
//
//   - 不读任何邮件正文
//   - 不打印发件人、收件人、主题
//   - 不添加也不删除任何标志（拉头部走的是 BODY.PEEK，不会把邮件标成已读）
//
// 报告出来的只有「形状」：条数、字段完整度。所以它的输出可以直接贴进
// issue，不必担心带出邮件内容。
//
// 为什么要有它：首次配置走完之后如果同步没跑起来，界面上只剩一行会被
// 截断的状态栏，用户拿不到任何可诊断的信息。日志回答的是「程序内部走到
// 哪一步了」，自检回答的是「网络和服务端那一侧到底通不通」——两件事，
// 缺一不可。
//
// 这一层的逻辑之所以从 tools/conncheck 挪进来，是为了能被单测覆盖：
// mail.Client 是接口，测试用 mail.NewFake() 注入即可，一个真实连接都不碰。
package diagnose

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// sampleSize 是②里抽查的头条数。取 5 是够看出「字段是不是普遍缺失」，
// 又不至于让自检自己变慢。
const sampleSize = 5

// 自检的步骤号。FailedAt 用它表示「断在第几步」。
const (
	// StepNone 表示全部通过。
	StepNone = 0
	// StepLogin 是①连接并登录、选中 INBOX。
	StepLogin = 1
	// StepHeaders 是②拉一段邮件头部。
	StepHeaders = 2
	// StepSent 是③探测「已发送」文件夹。这一关不会让它失败。
	StepSent = 3
	// StepDeep 是④首拉耗时实测（只在 Deep 为真时才跑）。
	StepDeep = 4
)

// Options 是自检的输入。
//
// 这里收的是拼好的字符串而不是 *config.Config：diagnose 只关心
// 「往哪连」，不该反过来依赖配置格式，否则以后改配置结构要动两处。
type Options struct {
	Provider string
	Email    string
	IMAPAddr string
	SMTPAddr string

	// InitialLimit 是④里实测首拉多少条，通常取配置里的
	// Sync.InitialMaxMessages。只在 Deep 为真时用。
	InitialLimit int

	// Deep 为真时额外跑④，实测首拉整批头部要多久。
	//
	// 单列一个开关是因为它**明显更慢**（真实账号实测约 39ms/条，
	// 拉 500 条要 20 秒），日常自检不该默认付这个代价。
	Deep bool
}

// HeaderStats 是一批头部在「形状」上的统计。
//
// 这三个数能区分出几类问题：有 References 的比例低说明线程聚合会退化
// 成一条一条的散会话；主题为空多半是服务商没给 Subject 头。
type HeaderStats struct {
	Total        int
	WithDate     int
	WithRefs     int
	EmptySubject int
}

// Report 是自检的结果。
//
// 它是纯数据，判断（该不该报错、退出码多少）交给调用方。
// 这样测试断言的是事实字段，不用去匹配输出文案的措辞。
type Report struct {
	Options Options

	// Inbox 是①的结果。为 nil 说明连登录都没过，后面的步骤没跑。
	Inbox *mail.Folder
	// Headers 是②抽到的那几条头部。
	Headers []mail.Header
	// Sent 是③的结果。SentErr 非 nil 表示服务端上没有这个文件夹 ——
	// 这不是故障（各家对「已发送」的叫法本来就不同），不影响收信。
	Sent    *mail.Folder
	SentErr error

	// DeepCount / DeepTook 是④的实测结果。Deep 为假时是零值。
	DeepCount int
	DeepTook  time.Duration

	// FailedAt 是断掉的那一步（StepLogin / StepHeaders / StepDeep），
	// StepNone 表示全部通过。中间的失败会让后面的步骤不再执行 ——
	// 「断在第几步」本身就是最有用的信息。
	FailedAt int
	// Err 是让自检停下来的那个错误。
	Err error
}

// OK 报告自检是否全部通过。
//
// 注意③不算失败：服务端上没有「已发送」文件夹，收信完全正常，
// 只是用户自己发出去的邮件在界面上看不到。
func (r *Report) OK() bool { return r.FailedAt == StepNone }

// Stats 统计②抽到的头部的形状。
func (r *Report) Stats() HeaderStats {
	var s HeaderStats
	s.Total = len(r.Headers)
	for _, h := range r.Headers {
		if !h.Date.IsZero() {
			s.WithDate++
		}
		if len(h.References) > 0 {
			s.WithRefs++
		}
		if strings.TrimSpace(h.Subject) == "" {
			s.EmptySubject++
		}
	}
	return s
}

// Run 跑一遍自检。
//
// 只读，且不会因为失败而 panic：所有结果都落在 Report 里。
func Run(client mail.Client, opts Options) *Report {
	r := &Report{Options: opts}

	// ① 连接、登录、选中 INBOX。
	//
	// 这三件事在协议上不是一个动作，但**对用户来说是同一个症状**
	// （「连不上」），分开报只会让人困惑该怪谁。真正的区别在
	// 服务端返回的错误文本里，而那是客户端拼不出来的。
	inbox, err := client.Folder("INBOX")
	if err != nil {
		r.FailedAt, r.Err = StepLogin, err
		return r
	}
	r.Inbox = &inbox

	// ② 拉一小段头部。
	//
	// 这一关存在的意义和①不同：①过了只说明「能登录」，而拉头部要
	// 走 FETCH，是另一条代码路径（也是线程聚合的数据来源）。
	from := uint32(1)
	if inbox.UIDNext > sampleSize+1 {
		from = inbox.UIDNext - sampleSize
	}
	headers, err := client.Headers("INBOX", from, 0)
	if err != nil {
		r.FailedAt, r.Err = StepHeaders, err
		return r
	}
	r.Headers = headers

	// ③ 探测「已发送」。失败不算故障，记下来就够。
	sent, err := client.Folder("Sent")
	if err != nil {
		r.SentErr = err
	} else {
		r.Sent = &sent
	}

	// ④ 可选：实测首拉耗时。
	if opts.Deep {
		limit := opts.InitialLimit
		if limit <= 0 {
			limit = 500
		}
		start := uint32(1)
		if inbox.UIDNext > uint32(limit) {
			start = inbox.UIDNext - uint32(limit)
		}

		began := time.Now()
		all, err := client.Headers("INBOX", start, 0)
		took := time.Since(began)
		if err != nil {
			r.FailedAt, r.Err = StepDeep, err
			return r
		}
		r.DeepCount, r.DeepTook = len(all), took
	}

	return r
}

// Render 把结果写成给人看的一段报告。
//
// 渲染和判断分开：Run 只产出事实，措辞改了不会影响任何调用方的逻辑。
func (r *Report) Render(w io.Writer) {
	o := r.Options

	fmt.Fprintln(w, "── 配置 ──────────────────────────")
	if o.Provider != "" {
		fmt.Fprintf(w, "服务商   %s\n", o.Provider)
	}
	if o.IMAPAddr != "" {
		fmt.Fprintf(w, "IMAP     %s\n", o.IMAPAddr)
	}
	if o.SMTPAddr != "" {
		fmt.Fprintf(w, "SMTP     %s\n", o.SMTPAddr)
	}
	if o.Email != "" {
		fmt.Fprintf(w, "账号     %s\n", o.Email)
	}
	fmt.Fprintln(w)

	// ①
	fmt.Fprintln(w, "① 连接并登录 IMAP，选中 INBOX")
	if r.Inbox == nil {
		fmt.Fprintf(w, "   ✗ %v\n", r.Err)
		fmt.Fprintln(w, "\n断在第 ① 步：连接、登录或选中 INBOX 没成功。")
		fmt.Fprintln(w, "常见原因：授权码填错、服务商没开 IMAP、网络到不了该端口。")
		return
	}
	fmt.Fprintf(w, "   ✓ INBOX 共 %d 封，UIDNext=%d，UIDValidity=%d\n",
		r.Inbox.Messages, r.Inbox.UIDNext, r.Inbox.UIDValidity)
	if r.Inbox.UIDNext == 0 {
		// 这不是错误，但值得说一句：客户端拿不到 UIDNEXT 时要走兜底，
		// 否则会被误判成「文件夹是空的」。网易 126 就是这样。
		fmt.Fprintln(w, "     · 服务端没返回 UIDNEXT（照常同步，客户端会自己兜底）")
	}
	fmt.Fprintln(w)

	// ②
	fmt.Fprintf(w, "② 拉最近 %d 条头部（只报形状，不报内容）\n", sampleSize)
	if r.FailedAt == StepHeaders {
		fmt.Fprintf(w, "   ✗ %v\n", r.Err)
		fmt.Fprintln(w, "\n断在第 ② 步：登录是成功的，但拉头部失败。")
		fmt.Fprintln(w, "常见原因：服务商限流，或该账号的 IMAP 权限被单独限制。")
		return
	}
	s := r.Stats()
	fmt.Fprintf(w, "   ✓ 拿到 %d 条\n", s.Total)
	if s.Total > 0 {
		fmt.Fprintf(w, "     有日期 %d/%d · 有 References %d/%d · 主题为空 %d\n",
			s.WithDate, s.Total, s.WithRefs, s.Total, s.EmptySubject)
	}
	// 服务端说有信、却一条都拿不回来 —— 这是「界面空着但看不出原因」
	// 那类故障的信号。不点出来的话，用户只会看到「拿到 0 条」，
	// 然后去怀疑自己授权码填错了。
	if s.Total == 0 && r.Inbox.Messages > 0 {
		fmt.Fprintf(w, "   ! INBOX 里有 %d 封，却一条都没抽到。可能的原因：\n", r.Inbox.Messages)
		fmt.Fprintln(w, "     · 邮件都早于首次同步的时间窗口（默认 90 天）——界面里按 a 打开")
		fmt.Fprintln(w, "       「接收全部邮件」即可连历史一起拉；")
		fmt.Fprintln(w, "     · 或服务商对这个账号的 FETCH 有限制。")
	}
	fmt.Fprintln(w)

	// ③
	fmt.Fprintln(w, "③ 探测「已发送」文件夹")
	// 用 switch 而不是 if/else：Sent 和 SentErr 是互斥的，但「两个都是零值」
	// 也是合法状态（①或②就断了的话就会走到这里）。先判 Sent 再判 SentErr，
	// 零值也不至于解引用到 nil —— 诊断工具自己崩掉是最糟的结果。
	switch {
	case r.Sent != nil:
		fmt.Fprintf(w, "   ✓ %s 共 %d 封\n", r.Sent.Name, r.Sent.Messages)
	case r.SentErr != nil:
		fmt.Fprintf(w, "   ! %v\n", r.SentErr)
		fmt.Fprintln(w, "     不影响收信；但你发出去的邮件在界面上可能看不到。")
	default:
		fmt.Fprintln(w, "   · 没跑到这一步")
	}

	// ④
	if r.Options.Deep {
		fmt.Fprintln(w)
		if r.FailedAt == StepDeep {
			fmt.Fprintf(w, "④ 首拉实测失败：%v\n", r.Err)
			fmt.Fprintln(w, "\n断在第 ④ 步：单次拉和整批拉走的是同一段代码，")
			fmt.Fprintln(w, "差别只在量。多半是服务端限流或连接被中途掐断。")
			return
		}
		per := r.DeepTook / time.Duration(r.DeepCount+1)
		fmt.Fprintf(w, "④ 首拉实测：%d 条，耗时 %v（约 %v/条）\n",
			r.DeepCount, r.DeepTook.Round(time.Millisecond), per.Round(time.Millisecond))
		fmt.Fprintln(w, "   界面上这段时间只显示「同步中…」，不是卡死。")
	}

	if r.OK() {
		fmt.Fprintln(w, "\n全部通过。")
	}
}
