// Command conncheck 做一次**只读**的连接自检：连 IMAP、登录、选中文件夹、
// 拉一小段邮件头部。
//
// 刻意不做的事：不读任何邮件正文，不打印任何主题或地址 —— 只报「形状」
// （条数、字段完整度）。它的用途是判断「连不上」到底断在哪一步，
// 不需要把用户的邮件内容暴露到终端上。
//
// 凭据从环境变量读，不落盘、不进 shell 历史（调用方负责）。
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
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

	fmt.Println("── 配置 ──────────────────────────")
	fmt.Printf("服务商   %s\n", p.Name)
	fmt.Printf("IMAP     %s:%d (%s)\n", cfg.IMAP.Host, cfg.IMAP.Port, cfg.IMAP.TLS)
	fmt.Printf("SMTP     %s:%d (%s)\n", cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.TLS)
	fmt.Printf("账号     %s\n\n", user)

	client, err := mail.NewClient(cfg, config.Credentials{Password: pass})
	if err != nil {
		fmt.Println("✗ 创建客户端失败:", err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Println("① 连接并登录 IMAP，选中 INBOX")
	f, err := client.Folder("INBOX")
	if err != nil {
		fmt.Println("   ✗", err)
		fmt.Println("\n断在第 ① 步：登录或选文件夹失败。")
		os.Exit(1)
	}
	fmt.Printf("   ✓ INBOX 共 %d 封，UIDNext=%d，UIDValidity=%d\n\n", f.Messages, f.UIDNext, f.UIDValidity)

	fmt.Println("② 拉最近几条邮件头部（只报形状，不报内容）")
	from := uint32(1)
	if f.UIDNext > 6 {
		from = f.UIDNext - 5
	}
	headers, err := client.Headers("INBOX", from, 0)
	if err != nil {
		fmt.Println("   ✗", err)
		fmt.Println("\n断在第 ② 步：登录是成功的，但拉头部失败。")
		os.Exit(1)
	}

	var withDate, withRefs, emptySubject int
	for _, h := range headers {
		if !h.Date.IsZero() {
			withDate++
		}
		if len(h.References) > 0 {
			withRefs++
		}
		if strings.TrimSpace(h.Subject) == "" {
			emptySubject++
		}
	}
	fmt.Printf("   ✓ 拿到 %d 条头部\n", len(headers))
	if len(headers) > 0 {
		fmt.Printf("     有日期 %d/%d · 有 References %d/%d · 主题为空 %d\n",
			withDate, len(headers), withRefs, len(headers), emptySubject)
	}
	fmt.Println()

	fmt.Println("③ 探测「已发送」文件夹")
	sent, err := client.Folder("Sent")
	if err != nil {
		fmt.Printf("   ! %v\n", err)
		fmt.Println("     不影响收信，但你自己发的消息可能看不到（见设计文档风险 #2）")
	} else {
		fmt.Printf("   ✓ Sent 共 %d 封\n", sent.Messages)
	}

	// ④ 只在 CLICHAT_DEEP=1 时跑：实测首拉整批头部要多久。
	// 这是「界面卡在同步中」最常见的解释 —— 拉几百条头部可能要几十秒，
	// 用户会以为程序挂了。
	if os.Getenv("CLICHAT_DEEP") == "1" {
		limit := cfg.Sync.InitialMaxMessages
		start := uint32(1)
		if f.UIDNext > uint32(limit) {
			start = f.UIDNext - uint32(limit)
		}
		fmt.Printf("\n④ 首拉实测：拉 %d 条头部（UID %d 起）\n", limit, start)

		began := time.Now()
		all, err := client.Headers("INBOX", start, 0)
		took := time.Since(began)
		if err != nil {
			fmt.Println("   ✗", err)
			os.Exit(1)
		}
		fmt.Printf("   ✓ %d 条，耗时 %v（约 %v/条）\n",
			len(all), took.Round(time.Millisecond),
			(took / time.Duration(len(all)+1)).Round(time.Millisecond))
	}

	fmt.Println("\n全部通过。")
}
