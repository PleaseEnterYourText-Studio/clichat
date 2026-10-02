package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 这一组守的是用户的一句原话：
//
//	「消息列表展示最新一条消息」
//
// 副行以前放的是**主题**（这段对话叫什么），现在是**最新那条的正文摘要**
// （现在讲到哪儿了）。两者在群里聊到第三十轮的时候差别最大：主题还停在
// 第一轮的名字上，而列表真正该告诉你的是刚来的那句。

// newPreviewModel 造一个「主题和最新那条正文明显不同」的三会话模型。
//
// 副行的两个候选值必须**互不包含**（主题「会议安排」/ 正文「周三下午三点」），
// 而且**都不出现在别处** —— 否则「副行显示的是哪一个」就分不出来了，
// 判据会在换了实现之后照样绿。夹具里原先主题和正文挨得很近
// （Subject「会议安排」+ body「第一句」），够用但不该照着抄。
func newPreviewModel(t *testing.T) (Model, *mail.Fake) {
	t.Helper()
	return newPreviewModelWith(t, nil)
}

// newPreviewModelWith 同上，但允许换掉底层客户端（传 nil 用普通假客户端）。
func newPreviewModelWith(t *testing.T, wrap func(*mail.Fake) mail.Client) (Model, *mail.Fake) {
	t.Helper()

	cfg := testConfig(t)
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0

	now := time.Now()
	fake := mail.NewFake()
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p1@x>", From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "会议安排",
		Date: now.Add(-2 * time.Hour),
	}, "周三下午三点在 3 楼会议室")
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p2@x>", From: "bob@example.com", FromName: "Bob",
		To: []string{"me@example.com"}, Subject: "周末爬山",
		Date: now.Add(-time.Hour), Seen: true,
	}, "装备清单已经发你邮箱了")
	fake.AddMessage("Sent", mail.Header{
		MessageID: "<p3@x>", From: "me@example.com",
		To: []string{"carol@example.com"}, Subject: "报价单",
		Date: now, Seen: true,
	}, "附件里是最终价格")

	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	var client mail.Client = fake
	if wrap != nil {
		client = wrap(fake)
	}

	m := NewWithApp(cfg, app.New(cfg, client, idx))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = syncOnce(t, m)
	m = runCmd(t, m, m.foldersCmd(false))
	return m, fake
}

// bodyFailClient 让所有正文拉取都失败。
//
// 用来测「拉不到摘要」那条路：它必须既让列表退回显示主题，又**不能**让
// 拉取链无限重试 —— 后者是真正的坑，一个失败的消息会被每轮重新请求一次，
// 而每次都要等一次真实的连接超时。
type bodyFailClient struct {
	*mail.Fake
}

func (c bodyFailClient) Body(string, uint32) (mail.Message, error) {
	return mail.Message{}, errors.New("连接断了")
}

// loadPreviews 反复跑「拉列表摘要」那条链，直到没有新的要拉。
//
// 上界不是为了防慢，是为了**防死循环**：拉过的每一条都必须落表（哪怕是
// 空串），缺了这个记号就会永远有「还没拉的」，而界面上看起来一切正常
// （副行显示主题）—— 只有把命令真的跑起来才看得见。
func loadPreviews(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 50; i++ {
		cmd := m.previewsCmd()
		if cmd == nil {
			return m
		}
		m = runCmd(t, m, cmd)
	}
	t.Fatal("预览的链没停下来 —— 拉过的没有落表，会无限重试")
	return m
}

// listSubRow 返回列表里某条会话那一对行里的**副行**（去掉样式和缩进）。
//
// 从渲染结果里取，不是直接调 listSubLine：直接调只能证明那个函数算得对，
// 证明不了列表真的用了它 —— 而「算了但没接上」正是这类改动最常见的错。
func listSubRow(t *testing.T, m Model, th thread.Thread) string {
	t.Helper()
	l := m.measureLayout()
	rows := m.listRows()
	start, end := m.listWindow(rows, l.bodyH)

	for i := start; i < end; i++ {
		// 副行紧跟在它那一对的标题行后面。
		if !rows[i].first || rows[i].idx < 0 || m.visible[rows[i].idx].ID != th.ID {
			continue
		}
		pane := strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n")
		// pane 的第 0 行是列表标题，之后才是 rows[start:end]。
		k := 1 + (i - start) + 1
		if k >= len(pane) {
			t.Fatalf("列表栏只有 %d 行，取不到第 %d 行", len(pane), k)
		}
		return strings.TrimSpace(plainText(pane[k]))
	}
	t.Fatalf("会话 %q 不在可见窗口里", th.Subject)
	return ""
}

func visibleNamed(t *testing.T, m Model, subject string) thread.Thread {
	t.Helper()
	for _, th := range m.visible {
		if th.Subject == subject {
			return th
		}
	}
	t.Fatalf("前提不成立：列表里没有主题为 %q 的会话", subject)
	return thread.Thread{}
}

// 副行显示的是**最新那条的正文**，不是主题。
func TestListSubLine_ShowsTheLatestMessageNotTheSubject(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	m = loadPreviews(t, m)

	th := visibleNamed(t, m, "会议安排")
	if th.Subject == "" {
		t.Fatal("前提不成立")
	}

	got := listSubRow(t, m, th)
	if !strings.Contains(got, "周三下午三点") {
		t.Errorf("副行是 %q，没看到最新那条的正文", got)
	}
	if strings.Contains(got, "会议安排") {
		t.Errorf("副行还是主题（%q）—— 用户要的是「最新一条消息」", got)
	}
}

// 正文还没到（或者拉不到）时，副行退回显示主题。
//
// 这条和上一条是一对：只有两条都在，才能证明改的是「有了正文就显示正文」，
// 而不是「把主题换成了另一句话」。
//
// ⚠️ 它守的是一条**开机就必须成立**的性质：摘要是网络往返，而列表在它
// 回来之前就画出来了。没有兜底的话，开机头几百毫秒所有副行都是空的 ——
// 那正是用户对新版本的第一印象。
func TestListSubLine_FallsBackToSubjectBeforeTheBodyArrives(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t) // 刻意**不**跑预览命令

	th := visibleNamed(t, m, "会议安排")
	got := listSubRow(t, m, th)
	if !strings.Contains(got, "会议安排") {
		t.Errorf("摘要还没到时副行是 %q，该退回显示主题", got)
	}
}

// 自己发出去的那条要标出来。
//
// 名字那一行写的是**对方**（会话是「和谁」）。摘要却是原样的一句正文，
// 不标的话「附件里是最终价格」看着就像 Carol 说的。
func TestListSubLine_MarksYourOwnMessage(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	m = loadPreviews(t, m)

	// 夹具里第三条会话只在 Sent，最后一条是自己发的。
	th := visibleNamed(t, m, "报价单")
	got := listSubRow(t, m, th)
	if !strings.Contains(got, "我: ") {
		t.Errorf("自己发的那条没标出来：%q", got)
	}
	// 正文只在**没被栏宽截断的那个答案**上找：渲染出来的那一行会被 "…" 收尾
	// （列表栏就这么宽），拿它去 Contains 完整正文是尺子的错。
	if sub := m.listSubLine(th); !strings.Contains(sub, "附件里是最终价格") {
		t.Errorf("副行是 %q，没看到正文", sub)
	}
}

// 只拉**当前窗口里**那几条的摘要。
//
// 列表可能有几百条，开机时一次全拉完意味着几百个 FETCH 换一行小字。
// 窗口是跟着光标走的，翻下去的时候由键盘的尾巴再发一轮。
func TestPreviews_FetchOnlyWhatTheWindowShows(t *testing.T) {
	forceColor(t)
	m := manyThreadsModel(t, 40)

	from, to := m.listVisibleRange()
	if to-from >= len(m.visible) {
		t.Fatalf("前提不成立：窗口 %d 行盖住了全部 %d 条会话", to-from, len(m.visible))
	}

	want := m.wantedPreviews()
	if len(want) == 0 {
		t.Fatal("窗口里一条都没有要拉的 —— 摘要永远不会出现")
	}
	if len(want) > previewBatch {
		t.Errorf("一轮发了 %d 条，超过 previewBatch=%d", len(want), previewBatch)
	}

	// 每一条要拉的都得落在窗口里。
	inWindow := map[string]bool{}
	for i := from; i < to; i++ {
		inWindow[listPreviewID(m.visible[i])] = true
	}
	for _, h := range want {
		if !inWindow[h.MessageID] {
			t.Errorf("要拉 %q，但它不在窗口（%d,%d）里 —— 花了没人看的流量",
				h.MessageID, from, to)
		}
	}
}

// 光标往下翻，窗口之外的那几条会被接着拉。
func TestPreviews_FollowTheCursorDownTheList(t *testing.T) {
	forceColor(t)
	m := manyThreadsModel(t, 40)
	m = loadPreviews(t, m)

	top := m.visible[0]
	if p := m.previews[listPreviewID(top)]; p == "" {
		t.Fatalf("列表最上面那条没有摘要：%q", p)
	}

	// 挪到列表末尾 —— 走真实的按键，不是直接改光标。
	for i := 0; i < len(m.visible); i++ {
		m, _ = update(m, keyMsg("down"))
	}
	m = loadPreviews(t, m)

	last := m.visible[len(m.visible)-1]
	if p := m.previews[listPreviewID(last)]; p == "" {
		t.Fatalf("一路翻到底之后，最下面那条还是没有摘要 —— 窗口挪了却没有再拉")
	}
}

// 把窗口里的摘要拉完之后，链要停下来。
//
// 「停下来」在这里是一句有内容的话：它同时证明了**拉不到的也落了表**。
// 少了那个记号，链会永远有活干，而界面上看起来完全正常。
func TestPreviews_ChainStopsWhenEverythingIsLoaded(t *testing.T) {
	forceColor(t)
	m := manyThreadsModel(t, 12)
	m = loadPreviews(t, m)

	if cmd := m.previewsCmd(); cmd != nil {
		t.Error("都拉完了还在发命令 —— 拉过的没落表，会无限重试")
	}

	from, to := m.listVisibleRange()
	for i := from; i < to; i++ {
		id := listPreviewID(m.visible[i])
		if _, ok := m.previews[id]; !ok {
			t.Errorf("窗口里第 %d 条（%q）没有落表", i, m.visible[i].Subject)
		}
	}
}

// 正文拉不到时：列表退回显示主题，而且**不再重试**。
func TestPreviews_FailedFetchKeepsTheSubjectAndStopsRetrying(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModelWith(t, func(f *mail.Fake) mail.Client {
		return bodyFailClient{Fake: f}
	})

	// loadPreviews 自己会 Fatal（链停不下来），所以它能返回就说明停住了。
	m = loadPreviews(t, m)

	th := visibleNamed(t, m, "会议安排")
	if got := listSubRow(t, m, th); !strings.Contains(got, "会议安排") {
		t.Errorf("正文拉不到时副行是 %q，该退回显示主题", got)
	}
	// 拉失败也要落表 —— 落的是空串。
	if _, ok := m.previews[listPreviewID(th)]; !ok {
		t.Error("拉失败的那条没有落表，下一轮会再去要一遍（每次都要等一次连接超时）")
	}
}

// 对方又来了一封，摘要要跟着换。
//
// 这一条是「摘要表用 Message-ID 而不是会话 ID 当键」的全部理由：新消息
// 的 Message-ID 不在表里，于是它被重新拉一次；用会话 ID 当键的话，旧的
// 摘要会一直留在那儿，而它说的是上一封的事。
func TestPreviews_NewMessageReplacesTheSummary(t *testing.T) {
	forceColor(t)
	m, fake := newPreviewModel(t)
	m = loadPreviews(t, m)

	th := visibleNamed(t, m, "会议安排")
	if got := listSubRow(t, m, th); !strings.Contains(got, "周三下午三点") {
		t.Fatalf("前提不成立：副行是 %q", got)
	}

	// Alice 又发了一封，成为这条会话里最新的一条。
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<p1b@x>", References: []string{"<p1@x>"},
		From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "Re: 会议安排",
		Date: time.Now(),
	}, "改到周四上午十点")

	m = syncOnce(t, m)
	m = loadPreviews(t, m)

	got := listSubRow(t, m, th)
	if !strings.Contains(got, "改到周四上午十点") {
		t.Errorf("新邮件来了，副行还是 %q —— 摘要停在上一封上", got)
	}
	if strings.Contains(got, "周三下午三点") {
		t.Errorf("副行还是上一封的内容：%q", got)
	}
}

// 滚出列表的摘要要被丢掉。
//
// 不清的话，一个开一整天的窗口会把每一封收到过的邮件正文都留一份在内存里，
// 而其中绝大多数早就滚出列表了。
func TestPreviews_DroppedWhenTheThreadLeavesTheList(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	m = loadPreviews(t, m)

	// 「全部」里三条都有摘要。
	if len(m.previews) < 2 {
		t.Fatalf("前提不成立：只拉到 %d 条摘要", len(m.previews))
	}

	// 切到「已发送」—— 列表里就只剩那一条了。走真实的路径。
	m.setFolder(m.folderName("SENT"))
	if len(m.visible) != 1 {
		t.Fatalf("前提不成立：已发送里有 %d 条", len(m.visible))
	}
	if len(m.previews) != 1 {
		t.Errorf("切走之后还留着 %d 条摘要，只该留列表上那一条", len(m.previews))
	}
}

// 两套版式的列表在「写什么」上必须是同一个答案。
//
// Zen 的列表是一行一条，Normal 是两行一对 —— 但两边的后半句都该是
// 「最新一条说了什么」。各写一套的话，同一个会话按 F2 切一下就会显示
// 两句不同的话，而用户没有任何依据判断哪边是对的。
func TestListSubLine_AgreesAcrossNormalAndZen(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	m = loadPreviews(t, m)

	th := visibleNamed(t, m, "会议安排")
	sub := m.listSubLine(th)
	if !strings.Contains(sub, "周三下午三点") {
		t.Fatalf("前提不成立：副行文字是 %q", sub)
	}

	zen := plainText(m.viewZenList())
	if !strings.Contains(zen, sub) {
		t.Errorf("Zen 的列表里没有 %q —— 两套版式对同一个会话给出了不同的说法\n%s",
			sub, zen)
	}
}

// ---- 把正文压成一行 ----

// markdownPreview 的契约：只取第一个有内容的块，空白压成单空格。
//
// 走的是和正文同一个解析器，所以这里顺手钉住「标记不会漏进列表」——
// 列表上出现 `## ` 或 `**` 比不显示摘要还难看。
func TestMarkdownPreview_FlattensToASingleLine(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want string
	}{
		{"普通段落", "第一行\n第二行", "第一行 第二行"},
		{"跳过开头空行", "\n\n  才是我  ", "才是我"},
		{"标题不留 #", "## 会议纪要\n正文", "会议纪要 正文"},
		{"粗体不留星号", "**重要**：改时间了", "重要：改时间了"},
		{"引用不留尖括号", "> 引来的话", "引来的话"},
		{"列表项留项目符号", "- 第一项\n- 第二项", "• 第一项 • 第二项"},
		{"行内代码不留反引号", "跑 `make test` 就行", "跑 make test 就行"},
		{"链接只留文字", "见 [文档](https://example.com/x)", "见 文档"},
		{"跳过分隔线", "---\n真正的正文", "真正的正文"},
		{"表格取第一行", "| 接口 | 状态 |\n| --- | --- |\n| /a | 好 |", "接口 状态"},
		{"空正文", "", ""},
		{"只有分隔线", "---", ""},
		{"第一段就够，空行之后不取", "第一段\n\n第二段", "第一段"},
		{"连续空白压成一个", "a\t\tb    c", "a b c"},
	} {
		if got := markdownPreview(c.src); got != c.want {
			t.Errorf("%s：markdownPreview(%q) = %q，want %q", c.name, c.src, got, c.want)
		}
	}
}

// 超长正文被截断，但截断是按**列**算的（中文一个字两列）。
func TestListSubLine_TruncatedToThePaneWidth(t *testing.T) {
	forceColor(t)
	_, fake := newPreviewModel(t)
	long := strings.Repeat("这是一句很长的正文。", 20)
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<long@x>", From: "dave@example.com", FromName: "Dave",
		To: []string{"me@example.com"}, Subject: "长信", Date: time.Now(),
	}, long)

	cfg := testConfig(t)
	cfg.Account.Email = "me@example.com"
	cfg.Sync.InitialDays = 0
	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	m := NewWithApp(cfg, app.New(cfg, fake, idx))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = syncOnce(t, m)
	m = loadPreviews(t, m)

	l := m.measureLayout()
	for _, ln := range strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n") {
		// ⚠️ 要先剥掉样式再量。textWidth 数的是**每一个 rune**（含 ANSI 转义
		// 序列里那些字符），直接量彩色行得到的是「字节数」而不是「列数」，
		// 每一条都会红 —— 而真凶在尺子这一侧。
		if w := textWidth(plainText(ln)); w > l.listW {
			t.Errorf("列表某一行的显示宽是 %d，超过栏宽 %d：%q", w, l.listW, plainText(ln))
		}
	}
}

// 副行在窗口里的每一行都要有东西，不能出现空白行。
//
// 分组的标题行是唯一允许出现的非会话行。有没有内容能一眼看出来：一条
// 会话的副行如果空了，用户会以为「这条坏了」。
func TestListSubLine_NeverEmpty(t *testing.T) {
	forceColor(t)
	m := manyThreadsModel(t, 8)
	m = loadPreviews(t, m)

	l := m.measureLayout()
	pane := strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n")
	rows := m.listRows()
	start, end := m.listWindow(rows, l.bodyH)

	for i := start; i < end; i++ {
		k := 1 + (i - start)
		if k >= len(pane) {
			t.Fatalf("列表栏只有 %d 行，取不到第 %d 行", len(pane), k)
		}
		if text := strings.TrimSpace(plainText(pane[k])); text == "" {
			t.Errorf("rows[%d]（idx=%d first=%v）那一行是空的",
				i, rows[i].idx, rows[i].first)
		}
	}
}

// 摘要必须在**会话列表**上，而不是只在某个内部字段里。
//
// 一条把 listSubLine 实现对了、却没接到列表上的改动能过长上面几乎所有
// 判据（它们最终都读 m.previews 或直接调 listSubLine）—— 除了这一条。
func TestPreviews_ReachTheRenderedList(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	before := plainText(m.renderThreadList(m.listWidth(), m.bodyHeight()))

	m = loadPreviews(t, m)
	after := plainText(m.renderThreadList(m.listWidth(), m.bodyHeight()))

	if before == after {
		t.Fatalf("拉完摘要之后列表一个字符都没变 —— 摘要没接到列表上\n%s", after)
	}
	if !strings.Contains(after, "周三下午三点") {
		t.Errorf("列表里找不到最新那条的正文\n%s", after)
	}
}

// 摘要和主题都要能认出「这是哪条会话」，判据的尺子不能只认主题。
//
// threadNamed 是鼠标那一组判据的尺子（TestMouse_EveryListRowOpensItsOwnThread
// 用它认行）。它只认主题的话，摘要装好之后所有会话行都会被认成空白行，
// 于是那些行被**跳过不点** —— 一条不再检查任何东西、却依然绿着的判据。
func TestThreadNamed_RecognisesThePreviewRow(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	m = loadPreviews(t, m)

	l := m.measureLayout()
	pane := strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n")
	rows := m.listRows()
	start, _ := m.listWindow(rows, l.bodyH)

	seen := map[string]bool{}
	for i := start; i < len(rows) && 1+(i-start) < len(pane); i++ {
		text := plainText(pane[1+(i-start)])
		th, ok := threadNamed(m, text)
		if rows[i].idx < 0 {
			if ok {
				t.Errorf("分组标题行 %q 被认成了会话 %q", strings.TrimSpace(text), th.Subject)
			}
			continue
		}
		if !ok {
			t.Errorf("第 %d 行（%q）没被认出来 —— 尺子认不出摘要行", i, strings.TrimSpace(text))
			continue
		}
		if want := m.visible[rows[i].idx].ID; th.ID != want {
			t.Errorf("第 %d 行（%q）认成了 %q，want %q", i, strings.TrimSpace(text), th.ID, want)
		}
		seen[th.ID] = true
	}
	if len(seen) < 2 {
		t.Errorf("只认出了 %d 条会话，尺子基本没在工作", len(seen))
	}
}

// 摘要只在**列表**上有；会话里那条正文的行不应该被它影响。
//
// 这条挡的是「顺手把摘要也塞进正文流」这类改法 —— 那会让同一句话在
// 屏幕上出现两遍。
func TestPreviews_DoNotLeakIntoTheChatStream(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	m.cursor = 0
	m, _ = update(m, keyMsg("enter"))
	m = loadBodies(t, m)
	m = loadPreviews(t, m)

	// 会话流里那条正文只该出现一次。
	//
	// 尺子只量**会话栏**：列表栏那条摘要按设计也会出现同一句话，
	// 量整屏必然数到 2 次 —— 那是判据的错，不是产品的错。
	l := m.measureLayout()
	chat := plainText(m.renderChat(l.chatW, l.bodyH))
	if n := strings.Count(chat, "周三下午三点"); n != 1 {
		t.Errorf("「周三下午三点」在会话栏里出现了 %d 次，want 1\n%s", n, chat)
	}
}

// 分组标题（"未读 N" / "已读 N"）不能被当成会话。
//
// 顺带守着 listRows 的契约：src 里的分组行 idx 必须是 -1，摘要那一套
// 才会跳过它（跳过靠的就是这个 -1）。
func TestPreviews_SkipGroupHeaders(t *testing.T) {
	forceColor(t)
	m, _ := newPreviewModel(t)
	m = loadPreviews(t, m)

	var headers []int
	for i, r := range m.listRows() {
		if r.idx < 0 {
			headers = append(headers, i)
			if r.group == "" {
				t.Errorf("第 %d 行 idx=-1 却没有组名", i)
			}
		}
	}
	if len(headers) == 0 {
		// 夹具里 Alice 未读、Bob 已读，两组都有 —— 前提该成立。
		t.Fatal("前提不成立：列表里没有分组标题行")
	}

	// 窗口里的摘要条数不能把分组标题算进去。
	if got, want := len(m.wantedPreviews()), len(m.visible); got > want {
		t.Errorf("要拉 %d 条，会话只有 %d 条", got, want)
	}
}
