package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/diagnose"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// 自检的退出码。分三档是为了让脚本能区分「连不上」和「根本没跑起来」——
// 前者要去改邮箱设置，后者只要先跑一遍配置向导。
const (
	checkOK      = 0
	checkFailed  = 1
	checkCantRun = 2
)

// runCheck 跑一遍只读连接自检，返回进程退出码。
//
// 它用的是**已经保存下来的**配置和凭据，而不是让用户再填一遍 ——
// 会需要自检的人，恰恰是已经配好但连不上的那批人。这一点也是它和
// tools/conncheck 的分工：conncheck 收环境变量里的凭据，用在
// 「连配置都还没存下来」的更早期阶段。
func runCheck(cfg *config.Config, deep bool) int {
	if !cfg.Configured() {
		fmt.Fprintln(os.Stderr, "clichat: 还没有配置过账号，自检无从跑起。")
		fmt.Fprintln(os.Stderr, "先直接运行 clichat 走一遍配置向导，之后再试 -check。")
		return checkCantRun
	}
	if !config.HasCredentials() {
		fmt.Fprintln(os.Stderr, "clichat: 配置在，但本地凭据文件不见了（credentials.enc）。")
		fmt.Fprintln(os.Stderr, "运行 clichat，在解锁界面按 Ctrl+R 可以重新走一遍配置。")
		return checkCantRun
	}

	creds, err := unlockCredentials()
	if err != nil {
		fmt.Fprintln(os.Stderr, "clichat:", err)
		return checkCantRun
	}

	client, err := mail.NewClient(cfg, creds)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clichat: 创建客户端失败:", err)
		return checkCantRun
	}
	defer client.Close()

	rep := diagnose.Run(client, diagnose.Options{
		Provider:     providerLabel(cfg),
		Email:        cfg.Account.Email,
		IMAPAddr:     fmt.Sprintf("%s (%s)", cfg.IMAP.Addr(), cfg.IMAP.TLS),
		SMTPAddr:     fmt.Sprintf("%s (%s)", cfg.SMTP.Addr(), cfg.SMTP.TLS),
		InitialLimit: cfg.Sync.InitialMaxMessages,
		Deep:         deep,
	})
	rep.Render(os.Stdout)

	if !rep.OK() {
		fmt.Fprintln(os.Stderr, "\n提示：完整日志在", logPathForHint())
		return checkFailed
	}
	return checkOK
}

// unlockCredentials 取主密码，解密本地凭据。
//
// 密码只有两个来源：CLICHAT_MASTER 环境变量，或者终端上无回显地读一行。
//
// **刻意不支持把密码当命令行参数传**：argv 会进 shell 历史，也会出现在
// ps / 任务管理器的进程列表里，同机器的其他用户直接能读到。而 -check 的
// 场景（用户正焦头烂额地排查连不上）尤其容易被人顺手把密码贴到命令行上。
func unlockCredentials() (config.Credentials, error) {
	if master := os.Getenv("CLICHAT_MASTER"); master != "" {
		return config.LoadCredentials(master)
	}

	// 非交互式终端（管道、CI、双击运行）没法提示输入，直接说清楚怎么办，
	// 别让用户对着一个不动的光标猜。
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return config.Credentials{}, errors.New(
			"需要主密码，但当前不是交互式终端。\n" +
				"  在脚本里请设环境变量 CLICHAT_MASTER；在终端里直接运行 clichat -check")
	}

	fmt.Fprint(os.Stderr, "主密码：")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // ReadPassword 不回显换行，自己补一个
	if err != nil {
		return config.Credentials{}, fmt.Errorf("读取主密码失败: %w", err)
	}

	creds, err := config.LoadCredentials(string(raw))
	if errors.Is(err, config.ErrWrongPassword) {
		// 把它翻译成用户能懂的话：底层那句是实现细节，不是给用户看的。
		return creds, errors.New("主密码错误")
	}
	return creds, err
}

// providerLabel 返回服务商的显示名，找不到预设时退回 ID。
func providerLabel(cfg *config.Config) string {
	id := cfg.Account.Provider
	if id == "" {
		return ""
	}
	if p := config.FindProvider(id); p != nil {
		return p.Name
	}
	return id
}

// logPathForHint 只在能拿到路径时给出日志位置，拿不到就不提。
func logPathForHint() string {
	if p, err := config.LogPath(); err == nil {
		return p
	}
	return "(日志路径不可用)"
}
