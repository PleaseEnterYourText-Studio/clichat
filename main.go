// Command clichat 是一个跨平台的终端邮件聊天客户端。
//
// 它用你现有的邮箱账号收发邮件，但把邮件按会话线程呈现成 IM 聊天流。
// 单二进制，Windows / macOS / Linux 通用。
//
// clichat 没有离线演示模式：运行时要么连上你的真实邮箱，要么什么都不做。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/logging"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/tui"
)

// version 由构建时的 -ldflags 注入，源码里保持 dev。
var version = "dev"

func main() {
	// 日志最先起来 —— 后面任何一步出错都要能落到文件里，否则出问题时
	// 用户手上只有一行会被截断的状态栏可看。
	//
	// 打不开日志不该挡住启动：退回 stderr 继续跑。
	if closer, err := logging.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "clichat: 日志不可用，错误只会打到 stderr:", err)
	} else {
		// 注：fatal() 走 os.Exit，会跳过这个 defer。无害 —— log 包直接
		// 写 os.File，没有缓冲区，不会有数据留在内存里丢掉。
		defer closer()
	}

	// 声明终端为深色背景。
	//
	// 实测确认：这一行 **不能** 消除启动时那对终端能力查询
	// （OSC 11 问背景色 + DSR 问光标位置）。那对查询来自 charmbracelet
	// 栈的底层，我没找到关闭它的开关 —— 别指望这行能治那个。
	//
	// 保留它的理由是另一件事：clichat 现在用固定配色，但一旦以后引入
	// lipgloss.AdaptiveColor，有这个声明就不会因为探测不到背景色而退化。
	//
	// 真实终端都会应答那对查询，不影响使用。只有极简终端、或某些
	// tmux / SSH 转发场景可能不应答，那种情况下会卡在启动画面。
	lipgloss.SetHasDarkBackground(true)

	// IMAP 的 ID 命令要向服务端上报版本号，跟 -version 打印的保持一致。
	mail.SetClientIDVersion(version)

	var (
		showVersion = flag.Bool("version", false, "打印版本号后退出")
		showConfDir = flag.Bool("config-dir", false, "打印配置目录后退出")
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

func fatal(err error) {
	if err == nil {
		return
	}
	log.Println("fatal:", err)
	fmt.Fprintln(os.Stderr, "clichat:", err)
	os.Exit(1)
}
