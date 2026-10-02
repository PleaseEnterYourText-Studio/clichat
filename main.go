// Command clichat 是一个跨平台的终端邮件聊天客户端。
//
// 它用你现有的邮箱账号收发邮件，但把邮件按会话线程呈现成 IM 聊天流。
// 单二进制，Windows / macOS / Linux 通用。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/app"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/tui"
)

// version 由构建时的 -ldflags 注入，源码里保持 dev。
var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "打印版本号后退出")
		showConfDir = flag.Bool("config-dir", false, "打印配置目录后退出")
		mock        = flag.Bool("mock", false, "用本地假数据运行界面，不连真实邮箱")
	)
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println("clichat", version)
		return
	case *showConfDir:
		dir, err := config.Dir()
		if err != nil {
			fatal(err)
		}
		fmt.Println(dir)
		return
	case *mock:
		fatal(runMock())
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	fatal(run(tui.New(cfg)))
}

// run 启动界面，并在退出时收尾。
//
// 收尾（断连接、落盘索引）必须在 Bubble Tea 主循环结束后做，
// 放在 defer 里会因为进程还没退出而和界面刷新抢状态。
func run(model tui.Model) error {
	final, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	if m, ok := final.(tui.Model); ok {
		if closeErr := m.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

// runMock 用内存里的假数据跑完整界面。
//
// 它的用处很具体：不碰真实邮箱就能看到界面长什么样、按键对不对，
// 也能在没配账号的机器上演示。
func runMock() error {
	cfg := config.Default()
	cfg.Account.Email = "me@example.com"
	cfg.Account.DisplayName = "我"
	cfg.IMAP.Host = "imap.example.com"
	cfg.SMTP.Host = "smtp.example.com"
	cfg.Sync.InitialDays = 0

	client := mail.NewFake()
	seedMock(client)

	idx, err := store.Open(filepath.Join(os.TempDir(), "clichat-mock-index.json"))
	if err != nil {
		return err
	}
	return run(tui.NewWithApp(cfg, app.New(cfg, client, idx)))
}

// seedMock 造一批覆盖各种情况的假数据：1:1 会话、群聊、独立邮件。
func seedMock(f *mail.Fake) {
	now := time.Now()

	f.AddMessage("INBOX", mail.Header{
		MessageID: "<m1@example.com>", From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "周五的评审", Date: now.Add(-3 * time.Hour),
	}, "周五的评审我改到下午三点了，你那边行吗？")

	f.AddMessage("INBOX", mail.Header{
		MessageID: "<m2@example.com>", References: []string{"<m1@example.com>"},
		From: "me@example.com", To: []string{"alice@example.com"},
		Subject: "Re: 周五的评审", Date: now.Add(-2 * time.Hour),
	}, "可以，我三点到。")

	f.AddMessage("INBOX", mail.Header{
		MessageID: "<m3@example.com>", References: []string{"<m1@example.com>"},
		From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "Re: 周五的评审", Date: now.Add(-90 * time.Minute),
	}, "好，那我把会议链接发出来。")

	f.AddMessage("INBOX", mail.Header{
		MessageID: "<g1@example.com>", From: "bob@example.com", FromName: "Bob",
		To:      []string{"me@example.com", "carol@example.com"},
		Subject: "周末爬山", Date: now.Add(-50 * time.Minute),
	}, "周六天气不错，有人想爬山吗？")

	f.AddMessage("INBOX", mail.Header{
		MessageID: "<g2@example.com>", References: []string{"<g1@example.com>"},
		From: "carol@example.com", FromName: "Carol",
		To:      []string{"me@example.com", "bob@example.com"},
		Subject: "Re: 周末爬山", Date: now.Add(-40 * time.Minute),
	}, "我报名。几点集合？")

	f.AddMessage("INBOX", mail.Header{
		MessageID: "<s1@example.com>", From: "noreply@service.com",
		To: []string{"me@example.com"}, Subject: "你的账单", Date: now.Add(-20 * time.Minute),
	}, "本月账单已生成。")
}

func fatal(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "clichat:", err)
	os.Exit(1)
}
