package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// 这一组守着会话里的「刷新」——IM 里这一条特别要紧。
//
// 背景（用户报的问题）：轮询到新邮件时，只有左边的列表会更新（未读标记、
// 时间、主题都变了），右边这一屏停在上一轮的内容上。用户坐在会话里干等，
// 看着列表说「有新信」，正文却纹丝不动 —— 结论就是「刷新没用」。
//
// 两条路都要通：
//   - 自动跟进：轮询同步到新数据时，当前会话的正文跟着重载；
//   - 手动刷新：Ctrl+R 立刻同步一次并重载正文。
//
// 会话内的刷新必须是 Ctrl+R 而不是裸 r —— 这里输入框有焦点，裸 r 会被
// 当成正文字符打进去。这一点由 TestChat_PlainRGoesIntoInput 守着。

// runAll 执行一个 tea.Cmd；如果是 tea.Batch 出来的批量命令，就逐个展开。
//
// 测试里没有 Bubble Tea 的事件循环，批量命令必须手工铺开 —— 只跑外层的话，
// tea.Batch 返回的 BatchMsg 里那几条命令会被当成「一个消息」喂回去，
// 断言就变成了看一个空转的结果。
func runAll(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = runAll(t, m, c)
		}
		return m
	}
	next, c := update(m, msg)
	return runAll(t, next, c)
}

// 打开一个会话，并把正文加载进来 —— 后面几条判据的公共起点。
func openChat(t *testing.T) (Model, *mail.Fake) {
	t.Helper()
	m, fake := newMockModel(t)
	m = syncOnce(t, m)
	m, _ = update(m, keyMsg("enter"))
	if m.mode != modeChat {
		t.Fatalf("回车后没进会话, mode=%v", m.mode)
	}
	m = loadBodies(t, m)
	return m, fake
}

// 轮询到新邮件时，当前会话的正文必须跟着重载。
//
// 这是用户报的那个问题的正面判据 —— 去掉 syncResultMsg 分支里的
// reloadBodiesIfChanged，这一条就会红。
func TestChat_PollPullsNewMessageIntoView(t *testing.T) {
	m, fake := openChat(t)

	// 对方又来了一封。会话键是「除我之外的参与人集合」，所以它落进同一个
	// 会话，而不是另起一个。
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<c@x>", From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "Re: 会议",
		Date: time.Now().Add(time.Minute),
	}, "第三句")

	// 轮询到点：同步 → 拿到新数据 → 正文也得跟着重载。
	changed, err := m.app.Sync()
	m, cmd := update(m, syncResultMsg{changed: changed, err: err})
	if cmd == nil {
		t.Fatal("轮询到新邮件后没有重载正文 —— 右边会一直停在上一轮的内容上")
	}
	m = runAll(t, m, cmd)

	if !strings.Contains(m.View(), "第三句") {
		t.Errorf("轮询到新邮件，这一屏的正文却没跟着更新:\n%s", m.View())
	}
}

// 空转的轮询不能顺手把整个会话的正文重拉一遍。
//
// 轮询每 30 秒来一次，而绝大多数时候是「没有新邮件」。不加这道闸的话，
// 每 30 秒就是一次「整个会话正文」的网络往返 —— 把空转的定时器变成
// 稳定的流量。
func TestChat_PollWithoutNewsDoesNotRefetch(t *testing.T) {
	m, _ := openChat(t)

	// 没有新数据。
	m, cmd := update(m, syncResultMsg{changed: false})
	if cmd != nil {
		t.Error("没有新数据却重载了正文 —— 每 30 秒一次的全量往返")
	}

	// 同步失败同理：索引可能只同步了一半，这时候重载正文只是白跑一轮。
	m, cmd = update(m, syncResultMsg{changed: true, err: errors.New("同步失败")})
	if cmd != nil {
		t.Error("同步出错却重载了正文")
	}
}

// Ctrl+R 要立刻同步一次，并把当前会话的正文重载出来。
func TestChat_CtrlRRefreshesBody(t *testing.T) {
	m, fake := openChat(t)

	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<d@x>", From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "Re: 会议",
		Date: time.Now().Add(2 * time.Minute),
	}, "手动刷新拉到的")

	m, cmd := update(m, keyMsg("ctrl+r"))
	if cmd == nil {
		t.Fatal("Ctrl+R 什么也没做")
	}
	if !m.busy {
		t.Error("刷新期间没有进入忙状态 —— 用户看不到任何反馈")
	}
	m = runAll(t, m, cmd)

	if m.busy {
		t.Error("刷新结束后还停在忙状态")
	}
	if !strings.Contains(m.View(), "手动刷新拉到的") {
		t.Errorf("Ctrl+R 之后新邮件没出现:\n%s", m.View())
	}
}

// 手动刷新即使这一轮没有新邮件，也要把正文重读一遍。
//
// 用户按了刷新却什么都没动，看着就是「刷新坏了」；而且上一轮的正文可能
// 加载失败过（服务端超时之类），这里是他唯一的重试入口。
func TestChat_CtrlRReloadsEvenWithoutNews(t *testing.T) {
	m, _ := openChat(t)

	// 抹掉已加载的正文 —— 模拟上一轮没读到。
	m.bodies = map[string]app.Body{}

	m, cmd := update(m, keyMsg("ctrl+r"))
	m = runAll(t, m, cmd)

	if !strings.Contains(m.View(), "第一句") {
		t.Errorf("没有新邮件时 Ctrl+R 没有重读正文:\n%s", m.View())
	}
}

// 会话内裸 r 必须落进输入框，不能当作刷新。
//
// 这条判据挡的是一个很容易犯的错：把列表上的 r（立即同步）直接搬到会话里。
// 这里输入框有焦点，裸 r 是用户要打的字。
func TestChat_PlainRGoesIntoInput(t *testing.T) {
	m, _ := openChat(t)

	m, _ = update(m, keyMsg("r"))

	if got := m.input.Value(); !strings.Contains(got, "r") {
		t.Errorf("裸 r 没进输入框，反倒被当成快捷键了: input=%q", got)
	}
}
