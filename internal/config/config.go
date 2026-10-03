// Package config 负责 clichat 的配置读写、服务商预设与凭据加解密。
//
// 分工很明确：
//   - config.json     非敏感配置，明文，权限 0600
//   - credentials.enc 敏感凭据，用主密码加密
//
// 密码绝不写进 config.json。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TLS 模式。
const (
	// TLSImplicit 是直接以 TLS 建立连接，对应 993（IMAP）/ 465（SMTP）。
	TLSImplicit = "ssl"
	// TLSStartTLS 是先明文连接再升级，对应 143（IMAP）/ 587（SMTP）。
	TLSStartTLS = "starttls"
)

// 两个协议的默认端口，供自定义服务商手填服务器时使用。
// 取的是隐式 TLS 那一组；选 STARTTLS 的话端口要改成 143 / 587。
const (
	DefaultIMAPPort = 993
	DefaultSMTPPort = 465
)

// 配置目录下的文件名。
const (
	ConfigFileName = "config.json"
	CredsFileName  = "credentials.enc"
	IndexFileName  = "index.json"
	// BodyCacheFileName 是正文缓存。和 index.json 分开放是有意的：
	// 索引每次同步都要重写（百 KB 量级），正文缓存是 MB 量级 ——
	// 混在一起等于每次同步都重写几 MB。
	BodyCacheFileName = "bodies.json"
	LogFileName       = "clichat.log"

	appDirName = "clichat"
)

// CurrentVersion 是配置文件格式版本。
//
//	v1  单账号：Account / IMAP / SMTP 直接摊在 Config 上
//	v2  多账号：Accounts 列表 + Active（上一次用的那个邮箱）
//
// v1 → v2 的迁移在 migrate() 里，读到旧文件自动升上来，用户无感。
const CurrentVersion = 2

// Endpoint 是一个 IMAP 或 SMTP 服务端点。
type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  string `json:"tls"`
}

// Addr 返回 host:port 形式，便于直接喂给 net 包。
func (e Endpoint) Addr() string {
	return fmt.Sprintf("%s:%d", e.Host, e.Port)
}

// ParseEndpoint 解析手填的「主机[:端口]」，端口省略时用 defaultPort。
// TLS 一律先填隐式 TLS —— 向导里紧接着有一步让用户改。
//
// 带冒号但解析不出端口时直接报错，不当作「含冒号的主机名」：那是端口写错了，
// 报错能当场发现，默默套默认端口则要等到连接失败才暴露。
func ParseEndpoint(raw string, defaultPort int) (Endpoint, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Endpoint{}, errors.New("不能为空")
	}

	host, port := s, defaultPort
	if strings.Contains(s, ":") {
		h, p, err := net.SplitHostPort(s)
		if err != nil {
			return Endpoint{}, errors.New("要写成 主机:端口 的形式（例如 imap.example.com:993）")
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return Endpoint{}, fmt.Errorf("端口 %q 不是数字", p)
		}
		host = strings.Trim(h, "[]")
		port = n
	}

	if host == "" {
		return Endpoint{}, errors.New("主机名不能为空")
	}
	if port < 1 || port > 65535 {
		return Endpoint{}, fmt.Errorf("端口 %d 超出 1-65535", port)
	}
	return Endpoint{Host: host, Port: port, TLS: TLSImplicit}, nil
}

// Account 是账号信息。这里 **不存密码**。
type Account struct {
	Provider    string `json:"provider"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// Sync 控制同步行为。
type Sync struct {
	PollIntervalSeconds int      `json:"poll_interval_seconds"`
	InitialDays         int      `json:"initial_days"`
	InitialMaxMessages  int      `json:"initial_max_messages"`
	Folders             []string `json:"folders"`

	// AllMail 为真时不再对首次同步划窗口：InitialDays 和
	// InitialMaxMessages 都不生效，历史邮件全部拉下来。
	//
	// 单独用一个开关而不是把 InitialDays 设成 0 表示「不限」，
	// 因为 0 是不合法的输入（applyDefaults 会把它当没填而补回默认值），
	// 复用一个「非法值」当第二含义，早晚会在某条分支上理解错。
	//
	// 打开它的代价是一次可能上万封的首次同步，所以界面那边要过一道确认。
	AllMail bool `json:"all_mail"`
}

// Chat 是 clichat 用户之间的身份信息。
//
// 它随每一封发出去的信带给对方，放在 X-Clichat-Meta 头里 —— 对方只有
// 用 clichat 才看得见，普通邮件客户端里连这个头都不显示（见
// internal/mail/chatmeta.go 的 ChatMetaHeader）。
type Chat struct {
	// Nick 是对方看到的昵称。留空时退回当前账号的 DisplayName，
	// 这样刚装好、什么都没配的人也是可读的（见 chatIdentityOf）。
	Nick string `json:"nick"`

	// NameColor / TextColor 是「我这边」用的两个颜色，写法是
	// #rgb / #rrggbb / #rrggbbaa；留空就用默认配色。
	//
	// 只认十六进制：这个值要跨机器传，而颜色名表得跟着每台机器的终端
	// 调色板走，没法保证两边看到的是同一个颜色。非法值会被静默丢弃
	// （见 mail.cleanChatColor），这在收信那一侧尤其重要 —— 对方发来
	// 的是一个会画到我终端上的字符串。
	NameColor string `json:"name_color"`
	TextColor string `json:"text_color"`

	// HideDevice 为真表示**不**上报设备信息。
	//
	// ⚠️ 字段名是反的，这是有意的：默认要「上报」，而这个字段的零值
	// 必须是「上报」。写成 ShareDevice bool 的话，零值 false 就成了
	// 「不上报」，于是「升级上来的老配置（没有 chat 段）」和「主动关掉
	// 的人」会塌成同一种状态 —— 两者本该不同（见 mail.RuntimeDevice）。
	HideDevice bool `json:"hide_device,omitempty"`
}

// Profile 是**一个账号**的完整配置。
//
// v1 里这三样直接摊在 Config 上（那时只有单账号）。收进一个结构是因为
// 「账号 + 它自己的服务器地址」本来就是一件事：不同账号的 IMAP/SMTP
// 一定不同，摊平了就表达不出这个约束。
type Profile struct {
	Account Account  `json:"account"`
	IMAP    Endpoint `json:"imap"`
	SMTP    Endpoint `json:"smtp"`

	// OAuth 只在 Auth=AuthOAuth2 的服务商上有值。
	//
	// ⚠️ **client id 必须由用户自己填**。Google 和 Microsoft 都不允许
	// 客户端内置 client secret（内置等于公开，谁都能拿去冒充这个应用），
	// 所以这里存的是用户在**自己的**服务商控制台注册应用之后拿到的 id。
	// 没有它就走不了 OAuth2 —— 界面上要说清这一步，不能让人对着一个
	// 连不上的账号反复输密码。
	OAuth *OAuthClient `json:"oauth,omitempty"`
}

// OAuthClient 是 OAuth2 授权需要的客户端信息。
type OAuthClient struct {
	// ClientID 是服务商控制台里注册应用后拿到的。
	ClientID string `json:"client_id"`
	// Tenant 只对 Microsoft 有意义：common / organizations / 具体租户 id。
	// 空按 common 处理。
	Tenant string `json:"tenant,omitempty"`
}

// Token 是 OAuth2 拿到的凭据。
//
// 定义在 config 而不是 oauth 包里：**它是落盘的数据**，而 config 是这个
// 仓库里唯一管持久化的叶子包（不依赖任何内部包）。oauth 包 import config，
// 反过来会成环。
type Token struct {
	AccessToken string `json:"access_token"`
	// RefreshToken 用来换新的 access token。它比 access token 长寿得多，
	// 而且**不是每次都会返回** —— 刷新响应里通常不带，所以拿不到新的时候
	// 必须保留旧的，否则下次就没法刷新了。
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry"`
	// Scope 是服务商实际授予的权限，可能比请求的窄。
	Scope string `json:"scope,omitempty"`
}

// Valid 报告 token 现在还能不能用。
//
// 留 2 分钟余量：拿到一个还有 3 秒过期的 token 再去连 IMAP，握手还没完
// 就失效了，报出来的错会是「认证失败」—— 那个错会把人引到「密码填错了」
// 的错方向上去。
func (t Token) Valid() bool {
	return t.AccessToken != "" && time.Now().Add(2*time.Minute).Before(t.Expiry)
}

// Config 是落在磁盘上的非敏感配置。
type Config struct {
	Version int `json:"version"`

	// Accounts 是全部已保存的账号。Active 是**上一次用的**那个（邮箱地址）。
	//
	// 记住「上一次用的」而不是「第一个」：多账号的人大多数时候只用其中一个，
	// 每次启动都要往下选一格是纯粹的摩擦。
	Accounts []Profile `json:"accounts"`
	Active   string    `json:"active"`

	// Account / IMAP / SMTP 是**当前活动账号的展开**，不参与序列化。
	//
	// 这是一个刻意的冗余：仓库里读这三样的地方有二十多处（mail 拨号、
	// 诊断、状态栏、向导……），让它们统统改成「先取活动账号再取字段」
	// 只是把同一个概念抄二十遍。这里保证一条不变量：
	//
	//	Account/IMAP/SMTP 永远等于 Accounts 里 Active 那个账号的对应字段。
	//
	// 维护它只有两个入口：Load 之后 expandActive() 展开一次；
	// Save 之前 collapseActive() 把改动写回列表。**别在别处直接改
	// Accounts[i]**，那样镜像就漂了。
	Account Account  `json:"-"`
	IMAP    Endpoint `json:"-"`
	SMTP    Endpoint `json:"-"`

	Sync Sync `json:"sync"`

	// Chat 描述的是**我这个人**，不是某个邮箱账号，所以它和 Sync 一样
	// 挂在 Config 上而不是 Profile 里 —— 用两个邮箱和同一个人聊天时，
	// 不该出现两个昵称。
	Chat Chat `json:"chat"`

	path string // config.json 的绝对路径，不参与序列化
}

// Default 返回一份带合理默认值的配置。
func Default() *Config {
	return &Config{
		Version: CurrentVersion,
		Sync: Sync{
			PollIntervalSeconds: 30,
			InitialDays:         90,
			InitialMaxMessages:  500,
			Folders:             []string{"INBOX", "Sent"},
		},
	}
}

// Dir 返回 clichat 的配置目录，必要时创建。
//
// 走 os.UserConfigDir()：
//
//	macOS   ~/Library/Application Support/clichat
//	Windows %AppData%\clichat
//	Linux   ~/.config/clichat
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("找不到用户配置目录: %w", err)
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}
	return dir, nil
}

// filePath 返回配置目录下某个文件的绝对路径。
func filePath(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// LogPath 返回日志文件的路径。
//
// 日志**不分账号**：一个进程一次只连一个账号，出问题时想知道的是「这次
// 启动发生了什么」，按账号拆开只会让人在两个文件之间来回找。
func LogPath() (string, error) { return filePath(LogFileName) }

// Load 读取默认位置的配置。文件不存在时返回默认配置且不报错，
// 这样首次启动可以自然进入配置向导。
func Load() (*Config, error) {
	path, err := filePath(ConfigFileName)
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom 读取指定路径的配置，语义与 Load 相同。
//
// 存在的理由有两个：测试需要一个不和用户真实配置打架的落点；
// 以及「配置从哪来」这件事本身只该有一个实现。
func LoadFrom(path string) (*Config, error) {
	cfg := Default()
	cfg.path = path

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	cfg.path = path
	// 顺序有讲究：先迁移（把 v1 的账号收进列表），再展开活动账号，最后补
	// 默认值 —— 补默认值补的是**镜像**，而镜像在 Save 时会被写回列表。
	// 反过来（先补默认值再展开）会让展开把刚补上的 TLS 默认值又用列表里
	// 的空值盖掉。
	if err := cfg.migrate(data); err != nil {
		return nil, err
	}
	cfg.expandActive()
	cfg.applyDefaults()
	return cfg, nil
}

// Save 把配置写回磁盘（权限 0600）。
//
// path 为空时直接报错，不去猜默认位置。早先的版本会退回默认路径，
// 那不叫方便，那是一个会咬人的默认值：任何拿着内存里现造的 Config
// （测试、向导中途）一保存，就会把用户真实的 config.json 盖掉。
// 这种错误越早暴露越好，所以宁可让它写不出去。
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("这份配置没有绑定文件路径，拒绝保存")
	}
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	// 镜像 → 列表。这是**唯一**的回写点：所有改账号的地方（向导往字段上
	// 填、SetActive 切账号、UpsertProfile 存账号）最后都经过这里落盘。
	c.collapseActive()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := os.WriteFile(c.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return nil
}

// Path 返回 config.json 的绝对路径。
func (c *Config) Path() string { return c.path }

// SetPath 把配置绑到一个文件路径上。
//
// 给两种场合用：测试要一个不和用户真实配置打架的落点；界面上
// 「重置配置向导」时得保留原来的路径 —— 内容可以重来，落点的
// 位置不该跟着漂走。
func (c *Config) SetPath(p string) { c.path = p }

// Exists 报告配置文件是否已存在。
func (c *Config) Exists() bool {
	if c.path == "" {
		return false
	}
	_, err := os.Stat(c.path)
	return err == nil
}

// PollInterval 返回轮询间隔。
func (c *Config) PollInterval() time.Duration {
	return time.Duration(c.Sync.PollIntervalSeconds) * time.Second
}

// ---- 多账号 ----

// ActiveProfile 返回当前活动账号；没有账号时返回 nil。
//
// 返回的是**列表里的那一个**（指针），所以改它就是改列表 —— 但注意
// 不要长期持有它：Accounts 一旦 append 就可能重新分配底层数组。
func (c *Config) ActiveProfile() *Profile {
	return c.ProfileOf(c.Active)
}

// ProfileOf 按邮箱找一个账号（大小写与空格不敏感）。找不到返回 nil。
func (c *Config) ProfileOf(email string) *Profile {
	want := normalizeAddress(email)
	if want == "" {
		return nil
	}
	for i := range c.Accounts {
		if normalizeAddress(c.Accounts[i].Account.Email) == want {
			return &c.Accounts[i]
		}
	}
	return nil
}

// Profiles 返回全部账号（副本），按保存顺序。
func (c *Config) Profiles() []Profile {
	out := make([]Profile, len(c.Accounts))
	copy(out, c.Accounts)
	return out
}

// SetActive 把活动账号切到 email 并展开镜像。找不到这个账号返回 false。
func (c *Config) SetActive(email string) bool {
	p := c.ProfileOf(email)
	if p == nil {
		return false
	}
	c.Active = p.Account.Email
	c.Account = p.Account
	c.IMAP = p.IMAP
	c.SMTP = p.SMTP
	return true
}

// UpsertProfile 保存一个账号（按邮箱覆盖或追加），并把它设为活动账号。
func (c *Config) UpsertProfile(p Profile) {
	if old := c.ProfileOf(p.Account.Email); old != nil {
		*old = p
		c.SetActive(p.Account.Email)
		return
	}
	c.Accounts = append(c.Accounts, p)
	c.SetActive(p.Account.Email)
}

// RemoveProfile 删掉一个账号。它是活动账号时，活动账号落到列表里的下一个
// （没有就落到第一个）。返回是否删掉了东西。
func (c *Config) RemoveProfile(email string) bool {
	want := normalizeAddress(email)
	for i := range c.Accounts {
		if normalizeAddress(c.Accounts[i].Account.Email) != want {
			continue
		}
		c.Accounts = append(c.Accounts[:i], c.Accounts[i+1:]...)
		if normalizeAddress(c.Active) == want {
			c.Active = ""
			c.Account, c.IMAP, c.SMTP = Account{}, Endpoint{}, Endpoint{}
			if len(c.Accounts) > 0 {
				c.SetActive(c.Accounts[0].Account.Email)
			}
		}
		return true
	}
	return false
}

// expandActive 把活动账号的字段展开到镜像上。Load 之后调一次。
//
// Active 指向一个不存在的账号时（手改坏了配置、或者账号被删了）退到
// **第一个**账号：有账号却一个都不活动，界面会显示成一个空账号，而用户
// 明明配过 —— 那比"选错了账号"更难理解。
func (c *Config) expandActive() {
	if len(c.Accounts) == 0 {
		return
	}
	if !c.SetActive(c.Active) {
		c.SetActive(c.Accounts[0].Account.Email)
	}
}

// collapseActive 把镜像写回账号列表。Save 之前调。
//
// 镜像里有邮箱、列表里却没有时**追加**一条：配置向导走的是「往活动账号
// 的字段上填，填完保存」，那条路不会显式调 UpsertProfile；少了这一支，
// 向导刚配好的账号在保存之后就没了。
func (c *Config) collapseActive() {
	email := strings.TrimSpace(c.Account.Email)
	if email == "" {
		return
	}
	p := c.ProfileOf(email)
	if p == nil {
		c.Accounts = append(c.Accounts, Profile{})
		p = &c.Accounts[len(c.Accounts)-1]
	}
	p.Account = c.Account
	p.IMAP = c.IMAP
	p.SMTP = c.SMTP
	c.Active = email
}

// migrate 把老格式的配置升到当前版本。
//
// v1 → v2：单账号的 account / imap / smtp 收进 Accounts[0]，Active 指向它。
//
// ⚠️ 它**直接解析原始字节**，不是看已经反序列化好的 Config —— v2 里那三个
// 字段的标签是 `json:"-"`（它们只是活动账号的镜像），从 v1 文件里**读不出来**。
// 曾经写成「检查 c.Account.Email 是否非空」，结果是：v1 文件读进来之后
// Account 永远是空的 → 迁移判定「没有账号可迁」→ **用户的账号凭空消失**，
// 界面上直接跳回配置向导。这一类必须由「拿真实 v1 文件喂进去」的判据守着。
func (c *Config) migrate(data []byte) error {
	if c.Version >= CurrentVersion {
		return nil
	}

	var v1 struct {
		Account Account  `json:"account"`
		IMAP    Endpoint `json:"imap"`
		SMTP    Endpoint `json:"smtp"`
	}
	if err := json.Unmarshal(data, &v1); err != nil {
		return fmt.Errorf("解析 v1 配置失败: %w", err)
	}
	if v1.Account.Email != "" && len(c.Accounts) == 0 {
		c.Accounts = []Profile{{
			Account: v1.Account,
			IMAP:    v1.IMAP,
			SMTP:    v1.SMTP,
		}}
		c.Active = v1.Account.Email
	}
	c.Version = CurrentVersion
	return nil
}

// 一个账号一份的本地文件。索引和正文缓存都必须按账号分开：
// 它们装的是**那个邮箱的**邮件与同步游标，混在一起就是把两个邮箱的信
// 搅进同一张列表里。

// IndexPath 返回某个账号的 header 索引文件路径。
func IndexPath(email string) (string, error) {
	return filePath(accountFile(email, IndexFileName))
}

// BodyCachePath 返回某个账号的正文缓存文件路径。
func BodyCachePath(email string) (string, error) {
	return filePath(accountFile(email, BodyCacheFileName))
}

// accountFile 给配置目录下的文件名加上账号前缀。
//
// 邮箱里唯一真会破坏路径的字符是 `/` 和 `\`，但顺手把别的符号也换掉 ——
// 文件名里出现它们会让某些工具犯迷糊，而这里没有任何理由冒这个险。
// `@` 保留：三个平台都合法，而且它是邮箱里最认得出身份的那个字符。
func accountFile(email, base string) string {
	var b strings.Builder
	for _, r := range normalizeAddress(email) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '@', r == '.', r == '-', r == '_', r == '+':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "default-" + base
	}
	return b.String() + "-" + base
}

// AdoptLegacyFiles 把 v1 时代的单账号文件认领给某个账号。
//
// v1 只有单账号，所以配置目录里的 index.json / bodies.json 本来就是
// **那个账号的**。多账号之后它们要改名成带账号前缀的，否则新账号会从
// 一份别人的索引开始。
//
// 只在目标文件还不存在时改名（改名而不是拷贝：留着旧的会让下一个账号
// 又认领一次，两个账号共用同一份索引）。返回是否有文件被认领。
func AdoptLegacyFiles(email string) (bool, error) {
	adopted := false
	for _, base := range []string{IndexFileName, BodyCacheFileName} {
		oldPath, err := filePath(base)
		if err != nil {
			return adopted, err
		}
		newPath, err := filePath(accountFile(email, base))
		if err != nil {
			return adopted, err
		}
		if _, err := os.Stat(oldPath); err != nil {
			continue // 没有旧文件，没什么可认领的
		}
		if _, err := os.Stat(newPath); err == nil {
			continue // 已经认领过了
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return adopted, fmt.Errorf("认领 %s 失败: %w", base, err)
		}
		adopted = true
	}
	return adopted, nil
}

// Configured 报告账号是否已配置完整。
func (c *Config) Configured() bool {
	return c.Account.Email != "" && c.IMAP.Host != "" && c.SMTP.Host != ""
}

// Self 返回当前账号地址（小写去空格），用于在会话里区分「我」和「别人」。
func (c *Config) Self() string {
	return normalizeAddress(c.Account.Email)
}

// normalizeAddress 把地址统一成小写去空格的形式，便于比较。
//
// 这里刻意不引用 thread.NormalizeAddress —— config 不应该依赖 thread。
// 这个函数只有一个赋值那么长，重复的代价低于引入一条依赖边。
func normalizeAddress(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}

// applyDefaults 给缺失或非法的字段补上默认值。
func (c *Config) applyDefaults() {
	d := Default()
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Sync.PollIntervalSeconds <= 0 {
		c.Sync.PollIntervalSeconds = d.Sync.PollIntervalSeconds
	}
	if c.Sync.InitialDays <= 0 {
		c.Sync.InitialDays = d.Sync.InitialDays
	}
	if c.Sync.InitialMaxMessages <= 0 {
		c.Sync.InitialMaxMessages = d.Sync.InitialMaxMessages
	}
	if len(c.Sync.Folders) == 0 {
		c.Sync.Folders = d.Sync.Folders
	}
	if c.IMAP.TLS == "" {
		c.IMAP.TLS = TLSImplicit
	}
	if c.SMTP.TLS == "" {
		c.SMTP.TLS = TLSImplicit
	}
}

// Credentials 是加密存储的敏感信息。
//
// 多账号之后它是**按邮箱索引**的：一个账号一份授权码（或应用专用密码），
// 外加 OAuth2 的 token。全部用主密码加密之后才落盘。
type Credentials struct {
	// Passwords 是账号 → 授权码。键是**规范化之后**的邮箱。
	Passwords map[string]string `json:"passwords,omitempty"`
	// OAuth 是账号 → OAuth2 token。
	OAuth map[string]Token `json:"oauth,omitempty"`

	// Password 是 v1 的单账号字段。只在读旧凭据时用（见 AdoptLegacyPassword），
	// 新写入不再产生它。
	Password string `json:"password,omitempty"`
}

// PasswordFor 取某个账号的授权码。
func (c Credentials) PasswordFor(email string) (string, bool) {
	pw, ok := c.Passwords[normalizeAddress(email)]
	return pw, ok && pw != ""
}

// SetPassword 记下某个账号的授权码。
func (c *Credentials) SetPassword(email, password string) {
	if c.Passwords == nil {
		c.Passwords = map[string]string{}
	}
	c.Passwords[normalizeAddress(email)] = password
}

// AdoptLegacyPassword 把 v1 的单账号密码认领给某个账号。
//
// 老用户的 credentials.enc 里只有一个 Password，而它属于**当时唯一的
// 那个账号**。不认领的话，升级之后每个人都要重新填一遍授权码 —— 而他们
// 只会看到「认证失败」，根本猜不到是存储格式变了。
//
// 只在「还没有任何按账号存的密码」时认领：一旦用户存过第二个账号，那个
// 旧字段就不该再被当成谁的密码了（否则两个账号会共用一个码）。
func (c *Credentials) AdoptLegacyPassword(email string) bool {
	if c.Password == "" || len(c.Passwords) > 0 {
		return false
	}
	c.SetPassword(email, c.Password)
	c.Password = ""
	return true
}

// ForgetAccount 删掉某个账号的授权码与 token。
func (c *Credentials) ForgetAccount(email string) {
	delete(c.Passwords, normalizeAddress(email))
	delete(c.OAuth, normalizeAddress(email))
}

// ForgetOAuthToken 只删 token，留着授权码。
//
// 一个账号可能从 OAuth2 改成用授权码（或者反过来）。留着另一条路的凭据，
// 最轻的后果是文件里多一段谁也不看的垃圾；重一点的后果是「明明改成授权码
// 了却还在走 OAuth」，而那条路的报错是「认证失败」，指不到真正的原因。
func (c *Credentials) ForgetOAuthToken(email string) {
	delete(c.OAuth, normalizeAddress(email))
}

// OAuthToken 取某个账号的 OAuth2 token。
func (c Credentials) OAuthToken(email string) (Token, bool) {
	t, ok := c.OAuth[normalizeAddress(email)]
	return t, ok
}

// SetOAuthToken 记下某个账号的 OAuth2 token。
func (c *Credentials) SetOAuthToken(email string, t Token) {
	if c.OAuth == nil {
		c.OAuth = map[string]Token{}
	}
	c.OAuth[normalizeAddress(email)] = t
}

// HasCredentials 报告凭据文件是否存在。
func HasCredentials() bool {
	p, err := filePath(CredsFileName)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// SaveCredentials 用主密码加密并写入凭据。
func SaveCredentials(masterPassword string, creds Credentials) error {
	plain, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("序列化凭据失败: %w", err)
	}
	blob, err := SealCredentials(masterPassword, plain)
	if err != nil {
		return err
	}
	p, err := filePath(CredsFileName)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, blob, 0o600); err != nil {
		return fmt.Errorf("写入凭据失败: %w", err)
	}
	return nil
}

// LoadCredentials 用主密码解密凭据。
// 主密码错误时返回 ErrWrongPassword。
func LoadCredentials(masterPassword string) (Credentials, error) {
	var creds Credentials
	p, err := filePath(CredsFileName)
	if err != nil {
		return creds, err
	}
	blob, err := os.ReadFile(p)
	if err != nil {
		return creds, fmt.Errorf("读取凭据失败: %w", err)
	}
	plain, err := OpenCredentials(masterPassword, blob)
	if err != nil {
		return creds, err
	}
	if err := json.Unmarshal(plain, &creds); err != nil {
		return creds, fmt.Errorf("解析凭据失败: %w", err)
	}
	return creds, nil
}

// DeleteCredentials 删除凭据文件。文件不存在不算错误。
func DeleteCredentials() error {
	p, err := filePath(CredsFileName)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
