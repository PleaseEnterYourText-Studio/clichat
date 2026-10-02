package app

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
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
)

// 真机探针：拿**真实的** index.json 跑一轮同步，报出自愈前后的规模。
// 只在 CLICHAT_LIVE_PW 有值时跑。
func TestLiveHeal(t *testing.T) {
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
	idxPath, err := config.IndexPath()
	if err != nil {
		t.Fatalf("IndexPath: %v", err)
	}
	ix, err := store.Open(idxPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	report := func(tag string) {
		all := ix.All()
		empty, zero, dups := 0, 0, 0
		seen := map[[2]string]int{}
		for _, h := range all {
			if h.MessageID == "" {
				empty++
			}
			if h.UID == 0 {
				zero++
				continue
			}
			seen[[2]string{h.Folder, string(rune(h.UID))}]++
		}
		_ = dups
		t.Logf("%s：索引 %d 条，Message-ID 为空 %d，UID==0 %d", tag, len(all), empty, zero)
	}
	report("同步前")

	c, err := mail.NewClient(cfg, creds)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()
	a := New(cfg, c, ix)
	changed, err := a.Sync()
	if err != nil {
		t.Logf("Sync 报了错（可能有文件夹不存在，不影响自愈）：%v", err)
	}
	t.Logf("changed=%v，会话 %d 个", changed, len(a.Threads()))
	report("同步后")

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	report("落盘后重开")
}
