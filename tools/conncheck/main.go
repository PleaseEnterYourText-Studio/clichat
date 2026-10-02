// Command conncheck 做一次**只读**的连接自检：连 IMAP、登录、选中文件夹、
// 拉一小段邮件头部。
//
// 它和 `clichat -check` 的分工在于**凭据从哪来**：
//
//   - clichat -check 用已经保存下来的配置和凭据，适合「配好了但连不上」。
//   - conncheck 从环境变量读，适合「连配置都还没成功存下来」的更早期阶段，
//     或者想拿另一套账号配置试一下的时候。
//
// 两者的检查逻辑是同一份代码（internal/diagnose），输出的也是同一种报告 ——
// 分成两份实现迟早会漂移，而漂移的诊断工具比没有更糟。
//
// 凭据从环境变量读，不落盘、不进 shell 历史（调用方负责）。
package main

import (
	"fmt"
	"os"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/diagnose"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

func main() {
	user := os.Getenv("CLICHAT_USER")
	pass := os.Getenv("CLICHAT_PASS")
	providerID := os.Getenv("CLICHAT_PROVIDER")
	if providerID == "" {
		providerID = "qq"
	}
	if user == "" || pass == "" {
		fmt.Fprintln(os.Stderr, "需要 CLICHAT_USER 和 CLICHAT_PASS 环境变量")
		os.Exit(2)
	}

	p := config.FindProvider(providerID)
	if p == nil {
		fmt.Fprintf(os.Stderr, "没有 %q 这个预设\n", providerID)
		os.Exit(2)
	}

	cfg := config.Default()
	config.ApplyProvider(cfg, p)
	cfg.Account.Email = user

	client, err := mail.NewClient(cfg, config.Credentials{Password: pass})
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建客户端失败:", err)
		os.Exit(2)
	}
	defer client.Close()

	rep := diagnose.Run(client, diagnose.Options{
		Provider:     p.Name,
		Email:        user,
		IMAPAddr:     fmt.Sprintf("%s (%s)", cfg.IMAP.Addr(), cfg.IMAP.TLS),
		SMTPAddr:     fmt.Sprintf("%s (%s)", cfg.SMTP.Addr(), cfg.SMTP.TLS),
		InitialLimit: cfg.Sync.InitialMaxMessages,
		Deep:         os.Getenv("CLICHAT_DEEP") == "1",
	})
	rep.Render(os.Stdout)

	if !rep.OK() {
		os.Exit(1)
	}
}
