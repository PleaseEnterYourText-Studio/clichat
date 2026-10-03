package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/oauth"

	"github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
)

// livenessWindow 是「多久没碰过这条连接，才值得先探一次活」。
//
// NOOP 探活存在的唯一理由是服务商会在**空闲一段时间**后单方面掐断连接
// （见 ensureLocked）。那么刚用过的连接就不需要探 —— 上一条命令的应答
// 已经把「它还活着吗」回答了。而一次「打开会话」会连着走十几条命令，
// 每条前面插一个 NOOP 就是十几条白跑的往返：实测 15 条命令里有 6 条是它。
//
// 5 秒这个值很保守：真正的空闲断连是**分钟**量级（RFC 3501 建议服务端
// 至少 30 分钟），而一次打开会话的全部操作在几十毫秒内跑完。
const livenessWindow = 5 * time.Second

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
	cfg *config.Config
	// auth 是这次会话的认证方式（密码或 OAuth2）。
	//
	// 它必须在**每次建立连接**时重新取 token —— 见 mail.Auth.Token 的注释：
	// 长连接被掐断之后重连，拿一个过期的 token 去认证只会得到「认证失败」。
	auth Auth

	mu   sync.Mutex
	conn *imapclient.Client

	// folderList 是登录后从服务端 LIST 到的文件夹名。
	//
	// 缓存它是因为「已发送」在各家叫法不同（见 folders.go 的别名表），
	// 每次 SELECT 都重新 LIST 一遍纯属浪费 —— 一次连接内不会变。
	// nil 表示还没拉过；重连时清空。
	folderList []string

	// selName / selRO / selBox 记住**这条连接上当前选中的文件夹**。
	//
	// 为什么需要它：一次「打开会话」会连着走同步、正文、标已读三条腿，
	// 每条腿都各自 SELECT 一遍同一个文件夹。实测（见 roundtrip_test.go
	// 的 TestOpenThread_...）：15 条命令里只有 3 条在干活 —— 2 条 FETCH
	// 加 1 条 STORE，另外 12 条全是重复的 NOOP + EXAMINE。
	//
	// selName 存的是**服务端上的真实名字**（解析别名之后的），空串表示
	// 当前没有选中任何文件夹。换连接时三样一起清掉：选中状态是**连接级**
	// 的，新连接上什么也没选中。
	selName string
	selRO   bool
	selBox  *imap.MailboxStatus

	// verifiedAt 是上一次**确认**这条连接还活着的时刻（探活通过、或者
	// 一条命令成功跑完都算）。零值表示「还没确认过」—— 那时一律先探活。
	//
	// 它只用来省掉重复探活，不改变任何语义：连接死了照样能被发现，
	// 只是发现它的那条命令会失败一次（调用方 markSuspectLocked 之后，
	// 下一次就会走重连）。见 livenessWindow。
	verifiedAt time.Time
}

// markAliveLocked 记下「刚刚确认过这条连接还活着」。
// 调用方必须已持有 c.mu。
func (c *liveClient) markAliveLocked() { c.verifiedAt = time.Now() }

// markSuspectLocked 把连接标成可疑：下一次 ensureLocked 会重新探活，
// 探不通就重连。**任何一条命令失败都要调它。**
//
// 不调的话后果是实实在在的：连接已经死了，但 verifiedAt 还在窗口内，
// 于是接下来几秒里的每条命令都会跳过探活、各自失败一遍，而原先只要
// 一条就能发现并重连。省探活不能省掉「发现连接死了」这个能力。
func (c *liveClient) markSuspectLocked() { c.verifiedAt = time.Time{} }

// NewClient 用配置与认证方式创建一个真实的邮箱客户端。
func NewClient(cfg *config.Config, auth Auth) (Client, error) {
	if !cfg.Configured() {
		return nil, ErrNotConfigured
	}
	if auth.User == "" {
		auth.User = cfg.Account.Email
	}
	return &liveClient{cfg: cfg, auth: auth}, nil
}

// cipherSuites 是客户端愿意谈的 TLS 1.0–1.2 套件。
//
// ⚠️ 这里**必须**显式列出，而且必须补上 RSA 密钥交换的那两个 ——
// 不补的话 139 邮箱和新浪邮箱的 IMAP 一台都连不上，而且报的是一句
// 完全指不到原因的 `tls: handshake failure`。
//
// 实测（2026-10-03，逐套件单发）：

//	imap.139.com:993 / imap.sina.com:993 / imap.sina.cn:993
//	  RSA-GCM-128        ✓        RSA-CBC-128        ✓
//	  RSA-GCM-256        ✓        RSA-CBC-256        ✓
//	  ECDHE-RSA-GCM-128  ×        ECDHE-RSA-CBC-128  ×
//
// 也就是说这两家**只肯谈 RSA 密钥交换**，一个 ECDHE 套件都不认；而 Go 从
// 1.22 起不再默认提供 RSA 密钥交换的套件（它们全在 tls.InsecureCipherSuites()
// 里）。两边一夹，默认配置下就是握手失败。
//
// 补哪几个是有取舍的：
//   - 只补 GCM 的两个，不补 CBC 的两个。RSA 密钥交换本身没有前向保密，这是
//     不得不接受的代价（对面只给这个），但 CBC 还额外带填充预言问题，没必要
//     一起接过来。
//   - 顺序上把 tls.CipherSuites() 放前面、RSA 那两个放最后：只有对面**不支持**
//     任何 ECDHE 套件时才会用到它们。其余服务商走的还是原来那套。
//   - 另一个选项是干脆不支持这两家 —— 那更糟：它们在国内的用户量摆在那儿。
var cipherSuites = func() []uint16 {
	var ids []uint16
	// 安全的那批在前：它们是绝大多数服务商实际会用的。
	for _, s := range tls.CipherSuites() {
		ids = append(ids, s.ID)
	}
	// RSA 密钥交换的两个放最后：只有对面不支持任何 ECDHE 套件时才会轮到它们。
	return append(ids,
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	)
}()

// tlsConfig 返回适用于某个主机的 TLS 配置。
//
// CipherSuites 只作用于 TLS 1.2 及以下（TLS 1.3 的套件不可配置），所以
// 上面那件事不影响任何正常支持 TLS 1.3 的服务商。
func tlsConfig(host string) *tls.Config {
	return &tls.Config{
		ServerName:   host,
		MinVersion:   minTLSVersion,
		CipherSuites: cipherSuites,
	}
}

// ensureLocked 保证 IMAP 连接可用。调用方必须已持有 c.mu。
//
// 它会先探活（NOOP）再决定是否重连 —— 邮箱服务商经常在空闲一段时间后
// 单方面掐断连接，本地却还以为连着。
func (c *liveClient) ensureLocked() error {
	if c.conn != nil {
		// 刚用过的连接不必探活 —— 上一条命令的应答已经回答了这个问题。
		if !c.verifiedAt.IsZero() && time.Since(c.verifiedAt) < livenessWindow {
			return nil
		}
		if err := c.conn.Noop(); err == nil {
			c.markAliveLocked()
			return nil
		}
		_ = c.conn.Logout()
		c.conn = nil
		c.folderList = nil
		c.clearSelectLocked()
		c.markSuspectLocked()
	}

	conn, err := dialEndpoint(c.cfg.IMAP)
	if err != nil {
		return fmt.Errorf("连接 IMAP 服务器 %s 失败: %w", c.cfg.IMAP.Addr(), err)
	}

	// 调试转储要赶在 LOGIN 之前挂上，否则会把登录那段对话漏掉。
	if w := imapDebugWriter(); w != nil {
		conn.SetDebug(w)
		log.Printf("⚠️ %s=1：完整 IMAP 会话正在写进日志，其中包含邮件头部与正文", imapDebugEnv)
	}

	// 两种认证方式二选一。OAuth2 服务商（Gmail / Outlook）**不接受密码**，
	// 密码服务商也不认 XOAUTH2 —— 走错一条报出来都是「认证失败」，
	// 所以这里必须按账号配的方式走，不能"两个都试一遍"。
	if c.auth.UsesOAuth() {
		tok, err := c.auth.accessToken(context.Background())
		if err != nil {
			_ = conn.Logout()
			return fmt.Errorf("%w: %v", ErrAuth, err)
		}
		if err := conn.Authenticate(oauth.XOAuth2(c.auth.User, tok)); err != nil {
			_ = conn.Logout()
			return fmt.Errorf("%w: %v", ErrAuth, err)
		}
	} else if err := conn.Login(c.auth.User, c.auth.Password); err != nil {
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
	// 新连接上什么文件夹都没选中 —— 不清的话会把上一条连接的选中状态
	// 当成这条连接的，然后对着一个没选中的邮箱发 FETCH。
	c.clearSelectLocked()
	// 刚登录成功，连接是活的。
	c.markAliveLocked()
	return nil
}

// clearSelectLocked 清掉「当前选中的文件夹」这块记忆。
// 调用方必须已持有 c.mu。
func (c *liveClient) clearSelectLocked() {
	c.selName = ""
	c.selRO = false
	c.selBox = nil
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

// dialEndpoint 建立一条到 ep 的 IMAP 连接（按配置升级 TLS），**但不登录**。
//
// 抽成变量是为了让协议层能在**进程内**的假服务端上被测到：假服务端跑明文，
// 走不了生产这条 TLS 路径（见 imap_roundtrip_test.go）。生产路径上它恒等于
// dialEndpointTLS —— 没有任何配置项能把它指向别处。
var dialEndpoint = dialEndpointTLS

// dialEndpointTLS 是 dialEndpoint 的生产实现：隐式 TLS 或 STARTTLS。
func dialEndpointTLS(ep config.Endpoint) (*imapclient.Client, error) {
	if ep.TLS != config.TLSStartTLS {
		return imapclient.DialTLS(ep.Addr(), tlsConfig(ep.Host))
	}

	conn, err := imapclient.Dial(ep.Addr())
	if err != nil {
		return nil, err
	}
	if err := conn.StartTLS(tlsConfig(ep.Host)); err != nil {
		// 升级失败要主动断开：留着这条明文连接，后面每条命令都会以
		// 「未加密」的身份发出去，而调用方只看到一句「连接失败」。
		_ = conn.Logout()
		return nil, err
	}
	return conn, nil
}

// selectLocked 解析文件夹名并选中它，返回服务端上的真实名字和 SELECT 状态。
// 调用方必须已持有 c.mu。
//
// Folder / Headers / Bodies / 标志操作 / 移动 五个入口要做的是同一件事：
// ensureLocked → resolveLocked → Select。抽出来是因为漏掉其中任何一步
// 都会表现成「某个功能莫名其妙连不上」，而且很难从现象反推是哪一步。
//
// fresh 为真表示**一定要重新 SELECT**，不吃「已经选中过」的记忆。什么时候
// 必须给真：
//
//   - Folder：调用方要的就是权威状态（UIDVALIDITY / UIDNEXT / 总封数），
//     拿一份旧的回去等于把「有没有新邮件」算错；
//   - 任何刚改过这个文件夹内容的写操作之后（见 Move 末尾的 clear）。
//
// 其余情况给假 —— 复用记忆能把一次「打开会话」里的重复 SELECT 从 6 次
// 压到 3 次。这是测量出来的，不是猜的：见 roundtrip_test.go 的
// TestOpenThread_CostDoesNotGrowWithMessageCount（改前 15 条命令，
// 其中 12 条是重复的 NOOP + EXAMINE）。
func (c *liveClient) selectLocked(folder string, readOnly, fresh bool) (string, *imap.MailboxStatus, error) {
	if err := c.ensureLocked(); err != nil {
		return "", nil, err
	}
	real, err := c.resolveLocked(folder)
	if err != nil {
		return "", nil, err
	}

	// 复用规则不是「名字相同就行」，**读写模式也得兼容**：
	//
	//   - 当初可写选中的（SELECT），后来的只读请求能复用 —— 可写包含只读；
	//   - 当初只读选中的（EXAMINE），后来的可写请求**不能**复用 ——
	//     在 EXAMINE 过的邮箱上 STORE，服务端会直接拒掉。
	//
	// 后者正是 MarkSeen 的情形：正文是 EXAMINE 拉的，标已读需要 SELECT，
	// 所以那一次注定要重选一条。这不是没优化到，是协议本身要求的。
	if !fresh && c.selName == real && (c.selRO == readOnly || !c.selRO) {
		return real, c.selBox, nil
	}

	mbox, err := c.conn.Select(real, readOnly)
	if err != nil {
		// 选失败说明这条连接上的选中状态已经不可信 —— 清掉记忆，
		// 别让下一次操作继续复用一个说不清是什么的状态；同时把连接
		// 标成可疑，让下一次重新探活（探不通就重连）。
		c.clearSelectLocked()
		c.markSuspectLocked()
		return "", nil, fmt.Errorf("选中文件夹 %s 失败: %w", real, err)
	}
	c.selName, c.selRO, c.selBox = real, readOnly, mbox
	// 一条命令成功跑完 = 这条连接刚被证实是活的。
	c.markAliveLocked()
	return real, mbox, nil
}

// seqSetOf 把 UID 切片转成 IMAP 的序列集合。
func seqSetOf(uids []uint32) *imap.SeqSet {
	s := new(imap.SeqSet)
	for _, u := range uids {
		s.AddNum(u)
	}
	return s
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
	got, ok := ResolveFolder(name, c.folderList)
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
