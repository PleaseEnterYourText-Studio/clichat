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
	"os"
	"path/filepath"
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

// 配置目录下的文件名。
const (
	ConfigFileName = "config.json"
	CredsFileName  = "credentials.enc"
	IndexFileName  = "index.json"
	LogFileName    = "clichat.log"

	appDirName = "clichat"
)

// CurrentVersion 是配置文件格式版本。
const CurrentVersion = 1

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

// Config 是落在磁盘上的非敏感配置。
type Config struct {
	Version int      `json:"version"`
	Account Account  `json:"account"`
	IMAP    Endpoint `json:"imap"`
	SMTP    Endpoint `json:"smtp"`
	Sync    Sync     `json:"sync"`

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

// IndexPath 返回 header 索引文件的路径。
func IndexPath() (string, error) { return filePath(IndexFileName) }

// LogPath 返回日志文件的路径。
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
type Credentials struct {
	// Password 是授权码或应用专用密码 —— 不是邮箱登录密码。
	Password string `json:"password"`
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
