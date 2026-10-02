package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// 列表的顺序**只由时间决定，不由已读状态决定**。
//
// # 为什么单独立一节
//
// 原来列表按未读/已读分成两段（filter.go 的 groupUnreadFirst + view.go 里
// 那两行分组标题），后果是一串连锁的「逻辑不通」：
//
//   - **打开一条会话 = 把它标成已读 = 它立刻跳到「已读」那一段**，于是光标
//     底下换成了另一条。用户按一下回车，选中的东西变了；再按一下，开的是
//     另一条 —— 一路走下来像在原地打转，而且会重复打开同一条。
//   - 列表在用户脚下重排，「按顺序把未读过一遍」这条动线根本走不通。
//   - 光标、鼠标命中、滚动窗口全都建立在 `visible` 的下标上，而它在用户
//     操作的过程中被重排 —— 三者随时可能指向不同的会话。
//
// 现在：**顺序是稳定的时间序**（最新在前），未读靠行内的强调色和计数表示。
// 位置不再承载状态，于是它也不会因为状态变化而移动。

// mixedReadModel 造一个「有未读也有已读」的列表。
//
// ⚠️ 刻意**不**像 manyThreadsModel 那样把所有会话都预置成已读 —— 那是为了
// 绕开重排而做的规避（见那个夹具的注释）。这一节判据量的正是重排本身，
// 绕开它就什么都量不到了。
//
// 第 i 条会话的日期是 now - i 小时，所以**时间序就是 0,1,2,3…**；奇数条
// 未读。于是「时间序」和「未读优先序」是两个不同的排列，一眼能分辨。
func mixedReadModel(t *testing.T, n int) Model {
	t.Helper()

	cfg := config.Default()
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0

	now := time.Now()
	fake := mail.NewFake()
	for i := 0; i < n; i++ {
		fake.AddMessage("INBOX", mail.Header{
			MessageID: fmt.Sprintf("<order%d@x>", i),
			From:      fmt.Sprintf("peer%d@example.com", i),
			FromName:  fmt.Sprintf("同事 %d", i),
			To:        []string{"me@example.com"},
			Subject:   fmt.Sprintf("会话 %d", i),
			Date:      now.Add(time.Duration(-i) * time.Hour),
			Seen:      i%2 == 0, // 偶数已读、奇数未读
		}, fmt.Sprintf("第 %d 句", i))
	}

	idx, err := store.Open(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	m := NewWithApp(cfg, app.New(cfg, fake, idx))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = syncOnce(t, m)
	return m
}

// subjectsOf 把一批会话的主题按顺序取出来，用于比对排列。
func subjectsOf(list []thread.Thread) []string {
	out := make([]string, 0, len(list))
	for _, th := range list {
		out = append(out, th.Subject)
	}
	return out
}

// wantTimeOrder 是 n 条会话按时间（最新在前）排列的主题。
func wantTimeOrder(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("会话 %d", i))
	}
	return out
}

// 列表顺序 = 时间序，未读不许把它顶到前面去。
func TestList_OrderIsTimeOrderNotUnreadFirst(t *testing.T) {
	forceColor(t)
	const n = 6
	m := mixedReadModel(t, n)

	// 前提：夹具里确实两种都有，否则这条判据在两种排法下都绿。
	var unread int
	for _, th := range m.visible {
		if th.Unread > 0 {
			unread++
		}
	}
	if unread == 0 || unread == n {
		t.Fatalf("前提不成立：未读 %d / 共 %d —— 夹具没有混合的已读状态", unread, n)
	}

	got := subjectsOf(m.visible)
	if want := wantTimeOrder(n); !slices.Equal(got, want) {
		t.Errorf("列表顺序 = %v\n              want %v（时间序，最新在前）\n"+
			"未读被顶到前面的话，读一条就重排一次，列表会在用户脚下动", got, want)
	}
}

// 打开一条会话之后，光标必须还指着**同一条**。
//
// 这是「自环」的正身：打开 → 标已读 → 重排 → 光标落到别人身上 → 再按
// 回车开的是另一条。少了这条判据，把重排加回来照样全绿（顺序判据只量
// 静态排列，量不到「操作之后」）。
func TestList_OpeningAThreadKeepsTheCursorOnIt(t *testing.T) {
	forceColor(t)
	m := mixedReadModel(t, 6)

	// 挑一条**未读**的：只有它会因为「被标成已读」而换位置。
	idx := -1
	for i, th := range m.visible {
		if th.Unread > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("前提不成立：列表里没有未读会话")
	}
	m.cursor = idx
	before := m.visible[m.cursor]

	m, cmd := update(m, keyMsg("enter"))
	m = runAll(t, m, cmd)

	after, ok := m.currentThread()
	if !ok {
		t.Fatalf("打开 %q 之后光标落到了列表外", before.Subject)
	}
	if after.ID != before.ID {
		t.Errorf("打开 %q 之后光标落在 %q 上 —— 列表在用户脚下重排了\n"+
			"（顺序现在 = %v）", before.Subject, after.Subject, subjectsOf(m.visible))
	}
}

// 已读状态变了，**顺序不许变**。
//
// 直接量「同一个模型、只改已读」这一件事，比量「打开之后」更窄，也更能
// 定位：它排除了「打开」那条路上的其它副作用。
func TestList_ReadStateChangeDoesNotReorder(t *testing.T) {
	forceColor(t)
	m := mixedReadModel(t, 6)

	before := subjectsOf(m.visible)

	// 走真实那条路标已读（Fake 客户端，不碰网络）。
	target := m.visible[0]
	if err := m.app.MarkRead(target); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	m.setThreads(m.app.Threads())

	after := subjectsOf(m.visible)
	if !slices.Equal(before, after) {
		t.Errorf("标已读之后顺序变了：\n before %v\n after  %v", before, after)
	}
}

// 未读仍然要**看得出来** —— 位置不再承载状态，行内就得承载。
//
// 这条和上面几条是一对：上面要求「位置不随状态变」，它要求「状态仍然可见」。
// 只做上面那半的话，把未读标记整个删掉也全绿。
//
// ⚠️ 断言要**分别**钉住两处表示（标记 + 名字的档位），不能只钉「两行不一样」：
// 只钉「不一样」的话，去掉其中任何一处，另一处还撑着，判据照样绿 ——
// 实测过，删掉标记那条变异在「只比不同」的版本下是绿的。
func TestList_UnreadIsStillVisibleInTheRow(t *testing.T) {
	forceColor(t)
	m := mixedReadModel(t, 6)

	var unreadTh, readTh thread.Thread
	for _, th := range m.visible {
		if th.Unread > 0 && unreadTh.ID == "" {
			unreadTh = th
		}
		if th.Unread == 0 && readTh.ID == "" {
			readTh = th
		}
	}
	if unreadTh.ID == "" || readTh.ID == "" {
		t.Fatal("前提不成立：夹具没有混合的已读状态")
	}

	const w = 40
	unreadRow := m.listRowStyled(unreadTh, true, w)
	readRow := m.listRowStyled(readTh, true, w)

	// ① 标记区：未读有一颗强调色的方块，已读那个位置是空格。
	mark := styleUnread.Render(blockMark)
	if !strings.Contains(unreadRow, mark) {
		t.Errorf("未读那一行没有强调色标记：\n%q", unreadRow)
	}
	if strings.Contains(readRow, mark) {
		t.Errorf("已读那一行也带了未读标记：\n%q", readRow)
	}

	// ② 名字的档位：未读更亮、已读降一档。
	unreadName := styleRowNameUnread.Render(listRowSplit(unreadTh, w).name)
	readName := styleRowNameRead.Render(listRowSplit(readTh, w).name)
	if !strings.Contains(unreadRow, unreadName) {
		t.Errorf("未读那一行的名字没上未读样式：\n got %q\nwant 含 %q", unreadRow, unreadName)
	}
	if !strings.Contains(readRow, readName) {
		t.Errorf("已读那一行的名字没上已读样式：\n got %q\nwant 含 %q", readRow, readName)
	}
}
