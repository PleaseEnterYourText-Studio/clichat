package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"

	"github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
)

var (
	// ErrAuth 表示认证失败。
	//
	// 上层看到它应该弹回配置界面让用户改，而不是默默重试 ——
	// 反复用错误的凭据重连会把账号锁掉。
	ErrAuth = errors.New("认证失败")

	// ErrNotConfigured 表示账号信息不完整。
	ErrNotConfigured = errors.New("账号尚未配置完整")
)

// minTLSVersion 是我们接受的最低 TLS 版本。
//
// 现代邮箱服务商都支持 TLS 1.2+，把底线卡在这里可以挡掉降级攻击。
const minTLSVersion = tls.VersionTLS12

// liveClient 是 Client 的真实实现：IMAP 拉取 + SMTP 发送。
//
// 连接策略：IMAP 长连接复用（断了自动重连），SMTP 每次发送新建连接。
// SMTP 用短连接是有意的 —— 发信是低频操作，为它维护一条常连不值当。
type liveClient struct {
	cfg  *config.Config
	user string
	pass string

	mu   sync.Mutex
	conn *imapclient.Client

	// folderList 是登录后从服务端 LIST 到的文件夹名。
	//
	// 缓存它是因为「已发送」在各家叫法不同（见 folders.go 的别名表），
	// 每次 SELECT 都重新 LIST 一遍纯属浪费 —— 一次连接内不会变。
	// nil 表示还没拉过；重连时清空。
	folderList []string
}

// NewClient 用配置和凭据创建一个真实的邮箱客户端。
func NewClient(cfg *config.Config, creds config.Credentials) (Client, error) {
	if !cfg.Configured() {
		return nil, ErrNotConfigured
	}
	return &liveClient{
		cfg:  cfg,
		user: cfg.Account.Email,
		pass: creds.Password,
	}, nil
}

// tlsConfig 返回适用于某个主机的 TLS 配置。
func tlsConfig(host string) *tls.Config {
	return &tls.Config{
		ServerName: host,
		MinVersion: minTLSVersion,
	}
}

// ensureLocked 保证 IMAP 连接可用。调用方必须已持有 c.mu。
//
// 它会先探活（NOOP）再决定是否重连 —— 邮箱服务商经常在空闲一段时间后
// 单方面掐断连接，本地却还以为连着。
func (c *liveClient) ensureLocked() error {
	if c.conn != nil {
		if err := c.conn.Noop(); err == nil {
			return nil
		}
		_ = c.conn.Logout()
		c.conn = nil
		c.folderList = nil
	}

	ep := c.cfg.IMAP
	var (
		conn *imapclient.Client
		err  error
	)

	if ep.TLS == config.TLSStartTLS {
		conn, err = imapclient.Dial(ep.Addr())
		if err == nil {
			err = conn.StartTLS(tlsConfig(ep.Host))
		}
	} else {
		conn, err = imapclient.DialTLS(ep.Addr(), tlsConfig(ep.Host))
	}
	if err != nil {
		return fmt.Errorf("连接 IMAP 服务器 %s 失败: %w", ep.Addr(), err)
	}

	// 调试转储要赶在 LOGIN 之前挂上，否则会把登录那段对话漏掉。
	if w := imapDebugWriter(); w != nil {
		conn.SetDebug(w)
		log.Printf("⚠️ %s=1：完整 IMAP 会话正在写进日志，其中包含邮件头部与正文", imapDebugEnv)
	}

	if err := conn.Login(c.user, c.pass); err != nil {
		_ = conn.Logout()
		return fmt.Errorf("%w: %v", ErrAuth, err)
	}

	// 登录成功后必须立刻自报家门。网易系（163 / 126）不发这条 ID 的话，
	// 下一条 EXAMINE 就会被拒成 "Unsafe Login"，而 LOGIN 本身是成功的 ——
	// 详见 sendClientID 的注释。
	if err := sendClientID(conn); err != nil {
		_ = conn.Logout()
		return err
	}

	// 拉一次文件夹列表，后面靠它把「已发送」这类叫法不统一的文件夹
	// 解析成服务端上的真实名字（见 folders.go）。
	c.folderList = listFolders(conn)

	c.conn = conn
	return nil
}

// imapDebugEnv 是打开完整 IMAP 会话转储的环境变量名。
const imapDebugEnv = "CLICHAT_IMAP_DEBUG"

// imapDebugWriter 在 CLICHAT_IMAP_DEBUG=1 时返回一个能接住完整 IMAP
// 会话的 writer，否则返回 nil。
//
// ⚠️ 它会记下邮件头部甚至正文，和「日志只记事件」的约束是冲突的，
// 所以必须显式打开、默认关闭。排查连接层问题时，这是唯一能看清协议
// 对话的办法 —— 比如"到底有没有发出去那条 ID 命令"。
func imapDebugWriter() io.Writer {
	if os.Getenv(imapDebugEnv) != "1" {
		return nil
	}
	return log.Writer()
}

// listFolders 列出服务端上所有文件夹。失败返回 nil。
//
// 故意不返回错误：拿不到列表只是让别名解析失效、退化成原样使用配置里的
// 名字，不该因此把整个连接判死 —— 用户配的文件夹名万一本来就对呢。
func listFolders(conn *imapclient.Client) []string {
	ch := make(chan *imap.MailboxInfo, 64)
	done := make(chan error, 1)
	go func() { done <- conn.List("", "*", ch) }()

	var names []string
	for info := range ch {
		names = append(names, info.Name)
	}
	if err := <-done; err != nil {
		return nil
	}
	return names
}

// resolveLocked 把配置里的文件夹名解析成服务端上的真实名字。
// 调用方必须已持有 c.mu。
//
// 错误文案刻意保持短 —— 它会被塞进状态栏，超过 60 列就被截了。
// 完整可用的文件夹列表交给日志，不往界面上堆。
func (c *liveClient) resolveLocked(name string) (string, error) {
	got, ok := resolveFolder(name, c.folderList)
	if !ok {
		log.Printf("文件夹 %q 在服务端上不存在，可用的是：%s",
			name, strings.Join(c.folderList, "、"))
		return "", fmt.Errorf("%w: %q", ErrNoSuchFolder, name)
	}
	return got, nil
}

// Close 断开 IMAP 连接。重复调用是安全的。
func (c *liveClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}
	err := c.conn.Logout()
	c.conn = nil
	c.folderList = nil
	if err != nil {
		return fmt.Errorf("断开 IMAP 连接失败: %w", err)
	}
	return nil
}
