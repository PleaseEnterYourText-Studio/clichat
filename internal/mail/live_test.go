package mail

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
	"os"
	"testing"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// 真机判据：只在 CLICHAT_LIVE_PW 有值时跑。只读（EXAMINE + BODY.PEEK）。
func TestLiveHeaders(t *testing.T) {
	pw := os.Getenv("CLICHAT_LIVE_PW")
	if pw == "" {
		t.Skip("没有 CLICHAT_LIVE_PW")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	creds, err := config.LoadCredentials(pw)
	if err != nil {
		t.Fatalf("主密码：%v", err)
	}
	c, err := NewClient(cfg, creds)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	for _, folder := range []string{"INBOX", "Sent"} {
		hs, err := c.Headers(folder, 1, 0)
		if err != nil {
			t.Fatalf("Headers(%s): %v", folder, err)
		}
		emptyID, noRefs, dupUID := 0, 0, 0
		seen := map[uint32]int{}
		for _, h := range hs {
			if h.MessageID == "" {
				emptyID++
			}
			if len(h.References) == 0 && h.InReplyTo == "" {
				noRefs++
			}
			if h.UID != 0 {
				seen[h.UID]++
			}
		}
		for _, n := range seen {
			if n > 1 {
				dupUID++
			}
		}
		t.Logf("%-6s 共 %d 条：Message-ID 为空 %d，无 References/InReplyTo %d，UID 重复 %d 组",
			folder, len(hs), emptyID, noRefs, dupUID)
	}
}
