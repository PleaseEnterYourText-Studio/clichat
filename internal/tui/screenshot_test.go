package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
)

// 这个文件负责生成 README 里用的终端截图素材。
//
// 默认跳过 —— 它只在需要更新 docs/images/ 时才跑，而且必须带
// CLICOLOR_FORCE=1：lipgloss 在非 TTY 下会把颜色档位降到 Ascii，
// 渲染出来全是黑白，截出来不像终端。
//
// 重新生成的完整流程：
//
//	cd /d D:\develop\clichat
//	set CLICHAT_SCREENSHOTS=1
//	set CLICOLOR_FORCE=1
//	go test ./internal/tui/ -run TestGenerateScreenshots -v
//	python tools/render-screenshots.py
//
// 第一步产出 docs/images/raw/*.ansi，第二步把它们渲染成 docs/images/*.png
// （用 charmbracelet/freeze 出 SVG，再用 Edge 无头模式栅格化 —— 为什么
// 要绕这一圈、以及色深那个坑，见 tools/render-screenshots.py 顶部的说明）。
//
// **每次重出，01-chat / 02-list / 03-search 这三张会变，即使画面内容没动。**
// 状态栏那一行的「上次同步 HH:MM:SS」来自 model.go 里的 time.Now()，它走的是
// 生产代码路径，而这里驱动的就是生产代码 —— 于是时间戳跟着当下走，那三张
// 都带着它。（消息日期是固定的，见 newSampleModel 里的 base；只有这个同步
// 时间没固定。）要真正固定得给 Model 注入一个时钟，还没做。这三张本来就是
// 会变的，**不要拿它们的字节去判断「有没有意外改动」** —— 要看图。
//
// 04-help 上没有任何时钟（帮助页没有状态栏），所以它的 ANSI 是逐字节稳定的：
// 内容没动，它就不该变；它变了就一定是帮助页本身被改了。这是四张里唯一一张
// 可以当「指纹」用的，剩下三张不行。
//
// （渲染本身是可复现的：ANSI 不变，PNG 就不变。）
func TestGenerateScreenshots(t *testing.T) {
	if os.Getenv("CLICHAT_SCREENSHOTS") != "1" {
		t.Skip("需要 CLICHAT_SCREENSHOTS=1 才跑（见本文件顶部注释）")
	}
	if os.Getenv("CLICOLOR_FORCE") == "" {
		t.Fatal("必须同时设 CLICOLOR_FORCE=1，否则 lipgloss 不输出颜色，截出来是黑白的")
	}

	// 挡位必须自己钉成 256 色，不能跟着环境判。
	//
	// CLICOLOR_FORCE=1 只保证「有颜色」，色深还是 termenv 按环境自动判的；
	// 这里没有 TTY，它判成 16 色。16 色会把「对方消息的灰底」（自适应色
	// 236）压成 \x1b[40m —— 而 **freeze 恰好不画这一个序列**，于是截图里
	// 那层灰底根本不存在，README 反倒成了唯一看不见这个功能的地方。
	//
	// 这条不是猜的，用探针实测过：48;5;236 会被还原成背景矩形，48;2;r;g;b
	// 也会，唯独 40 什么都不画。所以问题出在色深，不在 paintPeerRow。
	//
	// 顺带一提，256 色也更接近现代终端的真实观感（Windows Terminal /
	// iTerm2 / VS Code 默认都是真彩），16 色才是 headless 环境的副产品。
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })

	// 输出到仓库根的 docs/images/raw/。测试的工作目录是包目录，
	// 所以往上退两级。
	out := filepath.Join("..", "..", "docs", "images", "raw")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}

	shots := []struct {
		name  string
		width int
		// height 是终端行数。
		height int
		// drive 在模型准备好之后操作它，把界面切到要拍的样子。
		drive func(t *testing.T, m Model) Model
	}{
		{
			name:   "01-chat",
			width:  104,
			height: 32,
			drive: func(t *testing.T, m Model) Model {
				// 光标默认在第 0 个会话（最新那条）上，回车进会话。
				m, _ = update(m, keyMsg("enter"))
				return loadBodies(t, m)
			},
		},
		{
			name:   "02-list",
			width:  104,
			height: 28,
			drive: func(t *testing.T, m Model) Model {
				// 往下挪一格，让「选中行反白」落在一条未读会话上。
				m, _ = update(m, keyMsg("down"))
				return m
			},
		},
		{
			name: "03-search",
			// 刻意用窄终端：宽度低于 80 会降级成单栏，正好一并展示
			// 响应式布局 —— 而且单栏下没有那个空着的右半边。
			width:  74,
			height: 20,
			drive: func(t *testing.T, m Model) Model {
				// 搜「周」会命中四条（周五的产品评审 / 周末团建 / 第 40 周
				// 团队周报 / 周四下午），既看得出在过滤，列表又不至于空荡荡。
				m, _ = update(m, keyMsg("/"))
				m, _ = update(m, keyMsg("周"))
				return m
			},
		},
		{
			name:   "04-help",
			width:  84,
			height: 36,
			drive: func(t *testing.T, m Model) Model {
				m, _ = update(m, keyMsg("?"))
				return m
			},
		},
	}

	for _, s := range shots {
		t.Run(s.name, func(t *testing.T) {
			m := newSampleModel(t)
			m, _ = update(m, tea.WindowSizeMsg{Width: s.width, Height: s.height})
			m = s.drive(t, m)

			view := m.View()
			path := filepath.Join(out, s.name+".ansi")
			if err := os.WriteFile(path, []byte(view), 0o644); err != nil {
				t.Fatalf("写 %s 失败: %v", path, err)
			}

			lines := strings.Split(view, "\n")
			t.Logf("%s: %dx%d, %d 行, 首行 %q", s.name, s.width, s.height, len(lines), lines[0])
		})
	}
}

// newSampleModel 造一份像真实使用场景的样例数据。
//
// 刻意铺满各种形态：群聊、1:1、未读、星标、自己的消息 —— 截图要一眼
// 看出这是个聊天客户端，而不是一个空壳。
func newSampleModel(t *testing.T) Model {
	t.Helper()

	cfg := config.Default()
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0

	// 用固定时间：截图里的时间戳必须是稳定的，否则每次生成都不一样。
	base := time.Date(2026, 10, 2, 14, 0, 0, 0, time.Local)

	fake := mail.NewFake()

	// —— 群聊：产品评审（截图里的主角）——
	//
	// 刻意铺得比一屏能放下的多：真实聊天窗口本来就是「滚到底、上面的
	// 已经滚出去了」的样子，而且这能顺带展示 tailWindow 的裁剪行为。
	group := []string{"me@example.com", "alice@example.com", "bob@example.com"}
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p1@x>", From: "alice@example.com", FromName: "Alice",
		To: group, Subject: "周五的产品评审", Date: base.Add(4 * time.Minute),
	}, "周五的评审我改到下午三点了，你那边行吗？")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p2@x>", References: []string{"<p1@x>"},
		From: "me@example.com", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(9 * time.Minute), Seen: true,
	}, "可以，我三点到。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p3@x>", References: []string{"<p1@x>"},
		From: "bob@example.com", FromName: "Bob", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(16 * time.Minute),
	}, "我也没问题。要不要顺手把新版设计稿一起过一遍？")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p4@x>", References: []string{"<p1@x>"},
		From: "alice@example.com", FromName: "Alice", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(21 * time.Minute),
	}, "好，那我把会议链接发出来。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p5@x>", References: []string{"<p1@x>"},
		From: "me@example.com", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(27 * time.Minute), Seen: true,
	}, "收到，评审前我把上季度的数据整理好。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p6@x>", References: []string{"<p1@x>"},
		From: "bob@example.com", FromName: "Bob", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(34 * time.Minute),
	}, "另外接口那块文档里还标着 TBD，这次能定下来吗？")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p7@x>", References: []string{"<p1@x>"},
		From: "me@example.com", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(41 * time.Minute), Seen: true,
	}, "接口我这两天补上，评审前发给你。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p8@x>", References: []string{"<p1@x>"},
		From: "alice@example.com", FromName: "Alice", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(48 * time.Minute),
	}, "好，那就这么定。会议链接我发到群里了。")
	// 这条故意写长，会折成两行 —— 截图里要看得见「正文会折行」这件事。
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p9@x>", References: []string{"<p1@x>"},
		From: "alice@example.com", FromName: "Alice", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(55 * time.Minute),
	}, "对了，数据那块如果来不及，可以先只出华东区的。别为了凑全量把整个评审拖到下周，那样反而更被动。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p10@x>", References: []string{"<p1@x>"},
		From: "me@example.com", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(62 * time.Minute), Seen: true,
	}, "华东区的我今天就能出，全量的周五前应该也赶得上。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p11@x>", References: []string{"<p1@x>"},
		From: "bob@example.com", FromName: "Bob", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(68 * time.Minute),
	}, "那就先按华东区准备，全量的当加分项。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p12@x>", References: []string{"<p1@x>"},
		From: "alice@example.com", FromName: "Alice", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(74 * time.Minute),
	}, "行。那我把议程按这个顺序排一下，明天发出来。")
	// 这条是一条**真 HTML 邮件** —— 走 AddHTMLMessage，正文由真实的生成器
	// 在测试时转成 Markdown，不是手抄的 Markdown 字符串。
	//
	// 放它是为了让 01-chat 那张截图能**看出渲染**（这是聊天视图的主角，
	// 也是最后一条、一定在可视区内）：标题的井号没了、列表补上了真实序号
	// （生成器一律写「1.」，序号是渲染层补的）、链接只留文字、**强调**变成
	// 粗体，而且消息头上带一个 HTML 标记。若哪天渲染器坏了，这张图会直接
	// 变成一堆 Markdown 源码。
	//
	// 必须走 AddHTMLMessage 而不是 HTMLToMarkdown + AddMessage：后者在假
	// 客户端里就把「这段正文来自 HTML」这个事实丢了，消息头上那个标记
	// 永远拍不出来。
	fake.AddHTMLMessage("INBOX", mail.Header{
		MessageID: "<p13@x>", References: []string{"<p1@x>"},
		From: "alice@example.com", FromName: "Alice", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(80 * time.Minute),
	},
		`<h3>评审议程（周五 15:00）</h3>`+
			`<ol><li>上季度数据回顾</li><li>接口文档里剩下的 TBD</li><li>新版设计稿</li></ol>`+
			`<p>会议链接：<a href="https://meeting.example.com/abc-def-ghi">meeting.example.com/abc-def-ghi</a></p>`+
			`<p><strong>注意</strong>：资料在共享盘，看<strong>新版</strong>那份，旧版有几处数字是错的。</p>`)

	// 这条的正文**在测试时由真实的生成器算出来**（AddHTMLMessage 对一段
	// 真 HTML 的产出），不是手抄的字符串。
	//
	// 刻意的形状：一个用 font-size 表达的标题 + 一个数据表 + 单元格里带
	// alt 的状态图标 —— 就是用户截图里那封服务状态邮件的样子。
	//
	// 标题那条尤其要紧：真实邮件几乎不用 <h1>-<h6>（邮件客户端会把标题
	// 标签的样式 strip 掉），而是把字号写进 style。生成器得从字号反推
	// 层级，否则整封信的所有标题都会塌成同样大小的普通段落 —— 这正是
	// 用户报的「上下文严重丢失」。同样，改前数据表和图片渲染器都不认，
	// 屏幕上会出现 `!perational` 和 `| 组件 | 状态 |` 这种半截源码。
	//
	// 让它全部跟着真生成器走，截图就不可能和实际管线脱节。
	fake.AddHTMLMessage("INBOX", mail.Header{
		MessageID: "<p14@x>", References: []string{"<p1@x>"},
		From: "bob@example.com", FromName: "Bob", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(86 * time.Minute),
	},
		`<p style="font-size:22px;font-weight:bold">故障复盘</p>`+
			`<p style="font-size:14px">涉及的三个组件，当前状态如下：</p>`+
			`<table>`+
			`<tr><th>组件</th><th>状态</th><th>影响</th></tr>`+
			`<tr><td>API 接口</td>`+
			`<td><img src="https://status.example.com/ok.svg" alt="Operational"></td>`+
			`<td>无</td></tr>`+
			`<tr><td>消息推送</td>`+
			`<td><img src="https://status.example.com/ok.svg" alt="Operational"></td>`+
			`<td>延缓 12 分钟</td></tr>`+
			`<tr><td>数据同步</td>`+
			`<td><img src="https://status.example.com/warn.svg" alt="Degraded"></td>`+
			`<td>延缓 40 分钟</td></tr>`+
			`</table>`+
			`<p>现在都恢复了，细节在共享盘。</p>`)

	// 会话的最后一条由**自己**发出。
	//
	// 这一条是给截图凑构图的，两个作用：
	//
	//   - IM 的聊天窗本来就该两头都有。全是对方的话，看着像一封长邮件
	//     被排版成了聊天，而不是聊天。
	//   - 自己没有灰底、靠右对齐，正好和上面铺满灰底的对方消息形成对照。
	//     「谁在说」一眼就能分开，靠的正是这个 —— 没有对照，那层灰底在
	//     图里就只是一块不明所以的底色。
	//
	// 位置放在最后是必须的：正文区只显示末尾一屏（tailWindow），要让它在
	// 截图里看得见，它就得在末尾。
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p15@x>", References: []string{"<p1@x>"},
		From: "me@example.com", To: group,
		Subject: "Re: 周五的产品评审", Date: base.Add(92 * time.Minute), Seen: true,
	}, "收到，我这边也正常。评审的材料我周五上午发出来。")

	// —— 1:1，两条未读 ——
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<c1@x>", From: "carol@example.com", FromName: "Carol",
		To: []string{"me@example.com"}, Subject: "季度预算表", Date: base.Add(-40 * time.Minute),
	}, "预算表我更新了一版，你有空看一下。")

	// —— 星标会话 ——
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<d1@x>", From: "dave@example.com", FromName: "Dave",
		To: []string{"me@example.com"}, Subject: "设计稿反馈", Date: base.Add(-2 * time.Hour),
		Flagged: true, Seen: true,
	}, "第二版设计稿放在共享盘了，主要改了导航层级。")

	// —— 已读的独立邮件 ——
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<e1@x>", From: "billing@service.com", To: []string{"me@example.com"},
		Subject: "10 月账单已生成", Date: base.Add(-6 * time.Hour), Seen: true,
	}, "本月账单已生成，详情见附件。")

	// —— 再铺几条，让列表有「用了一段时间」的样子 ——
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<f1@x>", From: "weekly@example.com", FromName: "团队周报",
		To: []string{"me@example.com"}, Subject: "第 40 周团队周报", Date: base.Add(-8 * time.Hour),
		Seen: true,
	}, "本周进展和下周计划见正文。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<g1@x>", From: "noreply@github.com", To: []string{"me@example.com"},
		Subject: "[clichat] 有一条新的安全提醒", Date: base.Add(-11 * time.Hour),
	}, "依赖里有可升级的版本。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<h1@x>", From: "hr@example.com", FromName: "HR",
		To: []string{"me@example.com"}, Subject: "面试安排：周四下午", Date: base.Add(-26 * time.Hour),
		Seen: true,
	}, "周四下午两点，会议室在三楼。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<i1@x>", From: "ops@example.com", FromName: "运维告警",
		To: []string{"me@example.com"}, Subject: "磁盘使用率超过 85%", Date: base.Add(-30 * time.Hour),
		Seen: true,
	}, "生产环境磁盘使用率已超过阈值，请关注。")

	// —— 第二个群聊：多几个人，让列表里有「群」和「1:1」的对比 ——
	trip := []string{"me@example.com", "lisa@example.com", "tom@example.com", "erin@example.com"}
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<j1@x>", From: "lisa@example.com", FromName: "Lisa",
		To: trip, Subject: "周末团建", Date: base.Add(-3 * time.Hour),
	}, "周末团建定在周六还是周日？我先统计一下人数。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<j2@x>", References: []string{"<j1@x>"},
		From: "tom@example.com", FromName: "Tom", To: trip,
		Subject: "Re: 周末团建", Date: base.Add(-2 * time.Hour),
	}, "周六吧，周日想在家躺着。")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<j3@x>", References: []string{"<j1@x>"},
		From: "me@example.com", To: trip,
		Subject: "Re: 周末团建", Date: base.Add(-110 * time.Minute), Seen: true,
	}, "周六可以，算我一个。")

	// —— 1:1，未读 ——
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<k1@x>", From: "lisa@example.com", FromName: "Lisa",
		To: []string{"me@example.com"}, Subject: "合同盖章的事", Date: base.Add(-14 * time.Hour),
	}, "合同我已经寄出去了，大概周三到。")

	// —— 已发送文件夹里也放一条，好让「切换文件夹」有东西可切 ——
	fake.AddMessage("Sent", mail.Header{
		MessageID: "<s1@x>", From: "me@example.com", To: []string{"erin@example.com"},
		Subject: "资料已经发过去了", Date: base.Add(-90 * time.Minute), Seen: true,
	}, "资料已经发过去了，有问题随时说。")

	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	m := NewWithApp(cfg, app.New(cfg, fake, idx))
	return syncOnce(t, m)
}
