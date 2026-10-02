package tui

// 真机判据：**只在 CLICHAT_LIVE_PW 有值时跑**（CI 里没有这个变量，会 Skip）。
//
//	CLICHAT_LIVE_PW=<主密码> go test ./internal/<pkg>/ -run TestLive -v
//
// 为什么需要它们：有一类 bug **只在真实服务端上出现** —— 进程内那个假服务端
// （go-imap 的 memory 后端）造不出真实服务商的取舍。实测过的三个：
//
//   - 网易 126 的 ENVELOPE 不给 Message-ID（196 封里有 13 封），而原始头里有
//     （见 mail.applyRawHeaders）；
//   - 126 登录后必须先发 ID 命令，否则 EXAMINE 回「Unsafe Login」；
//   - 真实 HTML 邮件里 `<center>` 之类两边白名单都不认的元素会把渲染器
//     带进互相递归（见 htmlmd.go 的 node/inlineNode）。
//
// 这三类在端到端测试里永远不会出现，所以只能对真邮箱跑一遍。
//
// ⚠️ **只读**：EXAMINE + BODY.PEEK。不要在这里调 MarkRead / Delete / Send ——
// 那是对用户真实邮箱的写操作。主密码从环境变量来，不写进任何文件。

import (
	"fmt"
	"os"
	"runtime/debug"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// 真机探针：只在 CLICHAT_LIVE_PW 有值时跑（CI 里没有这个变量，会跳过）。
//
// ⚠️ **刻意不走 enterThread。** 那条路会调 app.MarkRead —— 对真实邮箱
// 是**写操作**，扫一遍 277 个会话就把人家整个收件箱标成已读了。
// 这里只驱动渲染路径：activeID + bodies + View()，全是只读的。
func TestLive(t *testing.T) {
	pw := os.Getenv("CLICHAT_LIVE_PW")
	if pw == "" {
		t.Skip("没有 CLICHAT_LIVE_PW，跳过真机探针")
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

	// ---- 1. 同步 ----
	changed, err := m.app.Sync()
	m, _ = update(m, syncResultMsg{changed: changed, err: err})
	t.Logf("同步完成：changed=%v err=%v，会话 %d 个", changed, err, len(m.visible))

	// ---- 2. 列表渲染 + 摘要链（自环就在这里）----
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("列表视图 panic：%v\n%s", r, debug.Stack())
			}
		}()
		_ = m.View()
	}()

	for round := 0; ; round++ {
		if round > 60 {
			t.Errorf("摘要链跑了 60 轮还没停 —— 自环（每轮都在重复要同一批）")
			break
		}
		cmd := m.previewsCmd()
		if cmd == nil {
			t.Logf("摘要链 %d 轮后停下", round)
			break
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("摘要第 %d 轮 panic：%v\n%s", round, r, debug.Stack())
				}
			}()
			m = runCmd(t, m, cmd)
		}()
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("拉完摘要后列表视图 panic：%v\n%s", r, debug.Stack())
			}
		}()
		_ = m.View()
	}()

	// ---- 3. 逐个会话打开渲染（刷屏退出的元凶在这里）----
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

			m.mode = modeList
			for i, v := range m.visible {
				if v.ID == id {
					m.cursor = i
					break
				}
			}
			m.activeID = th.ID
			m.mode = modeChat
			m.scroll = 0

			bodies, berr := m.app.Bodies(th)
			if berr != nil {
				bodyFail++
			}
			m.bodies = bodies
			_ = m.View()
			// 会话内滚动（只用不会触发网络写操作的键）
			for _, k := range []string{"pgup", "pgdown", "up", "down"} {
				m, _ = update(m, keyMsg(k))
				_ = m.View()
			}
		}()
		if n%40 == 39 {
			t.Logf("已扫 %d/%d 个会话，panic %d，正文失败 %d", n+1, len(ids), panics, bodyFail)
		}
	}
	t.Logf("扫描完毕：%d 个会话，panic %d，正文失败 %d", len(ids), panics, bodyFail)
	if panics > 0 {
		t.Errorf("%d 个会话渲染时 panic", panics)
	}
	_ = fmt.Sprint()
}
