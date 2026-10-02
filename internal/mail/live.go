package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"

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

	c.conn = conn
	return nil
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
	if err != nil {
		return fmt.Errorf("断开 IMAP 连接失败: %w", err)
	}
	return nil
}
