// 真机判据：**只在 CLICHAT_LIVE_PW 有值时跑**（CI 里没有这个变量，会 Skip）。
//
//	CLICHAT_LIVE_PW=<主密码> go test ./internal/tui/ -run TestLive -v
//
// 它守的是**后端那条转换链**（mail.CleanBody → HTMLToMarkdown → 渲染），
// 而不是界面本身：把真实邮箱里**每一封**的正文都过一遍，看有没有哪一封会让
// 转换或渲染崩掉。
//
// 为什么非要有这一条：真实 HTML 邮件里有一种东西**只有真邮箱里才有** ——
// `<center>` 之类「生成端和渲染端白名单都不认」的元素。2026-10-03 就是它把
// 渲染器带进互相递归、栈溢出（fatal error，recover 抓不到），用户看到的
// 是「打开这封邮件，程序刷屏退出」。构造出来的用例能覆盖已知的那几种，
// 但**只有真邮箱能证明它真的会发生**。
//
// ⚠️ **只读**：EXAMINE + BODY.PEEK。刻意**不走 enterThread** —— 那条路会调
// app.MarkRead，对真实邮箱是写操作，扫一遍就把人家整个收件箱标成已读了。
// 主密码从环境变量来，不写进任何文件。
package tui

import (
	"os"
	"runtime/debug"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

func TestLive(t *testing.T) {
	pw := os.Getenv("CLICHAT_LIVE_PW")
	if pw == "" {
		t.Skip("没有 CLICHAT_LIVE_PW，跳过真机判据")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	creds, err := config.LoadCredentials(pw)
	if err != nil {
		t.Fatalf("主密码不对？%v", err)
	}

	m := New(cfg)
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if err := m.connect(creds); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = m.app.Close() })

	changed, err := m.app.Sync()
	m, _ = update(m, syncResultMsg{changed: changed, err: err})
	t.Logf("同步完成：changed=%v err=%v，会话 %d 个", changed, err, len(m.visible))

	// 列表本身也要能画（回退之后这一屏是别人的代码，但崩了同样是我们的锅）。
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("列表视图 panic：%v\n%s", r, debug.Stack())
			}
		}()
		_ = m.View()
	}()

	ids := make([]string, 0, len(m.visible))
	for _, th := range m.visible {
		ids = append(ids, th.ID)
	}

	var bodyFail, panics int
	for n, id := range ids {
		th, ok := m.app.Thread(id)
		if !ok {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					t.Errorf("第 %d 个会话（%q，%d 条消息）渲染时 panic：%v\n%s",
						n, th.Subject, len(th.Messages), r, debug.Stack())
				}
			}()

			m.activeID = th.ID
			m.mode = modeChat
			m.scroll = 0

			bodies, berr := m.app.Bodies(th)
			if berr != nil {
				bodyFail++
			}
			m.bodies = bodies
			_ = m.View()
		}()
		if n%40 == 39 {
			t.Logf("已扫 %d/%d 个会话，panic %d，正文失败 %d", n+1, len(ids), panics, bodyFail)
		}
	}
	t.Logf("扫描完毕：%d 个会话，panic %d，正文失败 %d", len(ids), panics, bodyFail)
	if panics > 0 {
		t.Errorf("%d 个会话渲染时 panic", panics)
	}
}
