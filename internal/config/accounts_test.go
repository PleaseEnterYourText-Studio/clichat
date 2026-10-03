package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v1 的配置**原样**长这样（键名是 account / imap / smtp，单账号）。
//
// 用真实形状而不是"差不多"的：迁移判据的价值全在这里 —— 键名错一个字母，
// 用户的账号就会在升级后凭空消失，而那种 bug 在「自己造的夹具」上测不出来。
const v1ConfigJSON = `{
  "version": 1,
  "account": {
    "provider": "126",
    "email": "yzjtiantian@126.com",
    "display_name": "yzjtiantian"
  },
  "imap": { "host": "imap.126.com", "port": 993, "tls": "ssl" },
  "smtp": { "host": "smtp.126.com", "port": 465, "tls": "ssl" },
  "sync": {
    "poll_interval_seconds": 30,
    "initial_days": 90,
    "initial_max_messages": 500,
    "folders": ["INBOX", "Sent"],
    "all_mail": true
  }
}`

// v1 → v2：账号必须**活着**迁过来。
//
// 这条判据守的是一个真犯过的错：v2 里 Account/IMAP/SMTP 的标签是
// `json:"-"`（它们只是活动账号的镜像），于是从 v1 文件里**读不出来**。
// 迁移如果写成「检查 c.Account.Email 非空」，就永远判定「没有账号可迁」，
// 用户升级后直接被弹回配置向导 —— 账号凭空消失。
func TestLoadFrom_MigratesV1SingleAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(v1ConfigJSON), 0o600); err != nil {
		t.Fatalf("写 v1 配置: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	// ① 账号进了列表
	if len(cfg.Accounts) != 1 {
		t.Fatalf("Accounts 有 %d 个，want 1 —— v1 的账号没迁过来", len(cfg.Accounts))
	}
	p := cfg.Accounts[0]
	if p.Account.Email != "yzjtiantian@126.com" {
		t.Errorf("邮箱 = %q", p.Account.Email)
	}
	if p.Account.Provider != "126" || p.Account.DisplayName != "yzjtiantian" {
		t.Errorf("账号的其余字段丢了：%+v", p.Account)
	}
	if p.IMAP.Host != "imap.126.com" || p.IMAP.Port != 993 || p.IMAP.TLS != "ssl" {
		t.Errorf("IMAP 端点丢了：%+v", p.IMAP)
	}
	if p.SMTP.Host != "smtp.126.com" || p.SMTP.Port != 465 {
		t.Errorf("SMTP 端点丢了：%+v", p.SMTP)
	}

	// ② 它同时是活动账号，而且镜像展开了（二十多处调用点读的就是镜像）
	if cfg.Active != "yzjtiantian@126.com" {
		t.Errorf("Active = %q", cfg.Active)
	}
	if cfg.Account.Email != "yzjtiantian@126.com" || cfg.IMAP.Host != "imap.126.com" {
		t.Errorf("镜像没展开：Account=%+v IMAP=%+v", cfg.Account, cfg.IMAP)
	}
	if !cfg.Configured() {
		t.Error("迁移之后 Configured() 该是 true")
	}
	// ③ 其余设置（Sync）原样保留
	if !cfg.Sync.AllMail || cfg.Sync.InitialDays != 90 {
		t.Errorf("Sync 设置丢了：%+v", cfg.Sync)
	}
	// ④ 版本升上来了
	if cfg.Version != CurrentVersion {
		t.Errorf("Version = %d，want %d", cfg.Version, CurrentVersion)
	}
}

// 迁移结果要能落盘，而且**再读一次不会丢**（v1 的键不再写出去）。
func TestSave_AfterMigrationIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(v1ConfigJSON), 0o600); err != nil {
		t.Fatalf("写 v1 配置: %v", err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 落盘的内容里不该再有 v1 的顶层键 —— 留着就是两份真相。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("解析落盘内容: %v", err)
	}
	for _, gone := range []string{"account", "imap", "smtp"} {
		if _, ok := top[gone]; ok {
			t.Errorf("落盘内容里还有 v1 的顶层键 %q —— 迁移没清干净", gone)
		}
	}
	if _, ok := top["accounts"]; !ok {
		t.Error("落盘内容里没有 accounts")
	}

	again, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("二次 LoadFrom: %v", err)
	}
	if len(again.Accounts) != 1 || again.Account.Email != "yzjtiantian@126.com" {
		t.Errorf("二次读回来账号没了：%+v", again.Accounts)
	}
}

// 改**镜像**再 Save，改动要写回列表 —— 这是唯一的回写点。
//
// 配置向导走的就是这条路：往 cfg.Account / cfg.IMAP 上填，填完 Save。
// 少了回写，向导刚配好的账号在保存之后就没了。
func TestSave_CollapsesMirrorIntoAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.SetPath(path)

	cfg.Account.Email = "new@example.com"
	cfg.Account.DisplayName = "新账号"
	cfg.IMAP = Endpoint{Host: "imap.example.com", Port: 993, TLS: TLSImplicit}
	cfg.SMTP = Endpoint{Host: "smtp.example.com", Port: 465, TLS: TLSImplicit}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("Accounts 有 %d 个，want 1", len(got.Accounts))
	}
	if got.Accounts[0].Account.Email != "new@example.com" ||
		got.Accounts[0].IMAP.Host != "imap.example.com" {
		t.Errorf("镜像没写回列表：%+v", got.Accounts[0])
	}
}

// 多账号：加、切、删。
func TestProfiles_AddSwitchRemove(t *testing.T) {
	cfg := Default()
	cfg.SetPath(filepath.Join(t.TempDir(), "config.json"))

	a := Profile{
		Account: Account{Email: "a@example.com", DisplayName: "A"},
		IMAP:    Endpoint{Host: "imap.a.com", Port: 993, TLS: TLSImplicit},
		SMTP:    Endpoint{Host: "smtp.a.com", Port: 465, TLS: TLSImplicit},
	}
	b := Profile{
		Account: Account{Email: "b@example.com", DisplayName: "B"},
		IMAP:    Endpoint{Host: "imap.b.com", Port: 993, TLS: TLSImplicit},
		SMTP:    Endpoint{Host: "smtp.b.com", Port: 465, TLS: TLSImplicit},
	}
	cfg.UpsertProfile(a)
	cfg.UpsertProfile(b)

	if n := len(cfg.Profiles()); n != 2 {
		t.Fatalf("Profiles = %d，want 2", n)
	}
	// 后加的成为活动账号，镜像跟着它
	if cfg.Active != "b@example.com" || cfg.IMAP.Host != "imap.b.com" {
		t.Errorf("Upsert 之后活动账号没跟上：Active=%q IMAP=%+v", cfg.Active, cfg.IMAP)
	}

	// 切回 A
	if !cfg.SetActive("A@Example.com ") { // 大小写与空格都不该影响
		t.Fatal("SetActive 按规范化地址找不到账号")
	}
	if cfg.Account.Email != "a@example.com" || cfg.SMTP.Host != "smtp.a.com" {
		t.Errorf("切换之后镜像没换：%+v", cfg.Account)
	}

	// 同邮箱再 Upsert 是**覆盖**不是追加
	cfg.UpsertProfile(Profile{Account: Account{Email: "a@example.com", DisplayName: "A2"}})
	if n := len(cfg.Profiles()); n != 2 {
		t.Errorf("同邮箱 Upsert 之后变成 %d 个，want 2", n)
	}
	if p := cfg.ProfileOf("a@example.com"); p == nil || p.Account.DisplayName != "A2" {
		t.Errorf("覆盖没生效：%+v", p)
	}

	// 删掉活动账号 → 落到列表里的下一个，而不是留下一个空账号
	if !cfg.RemoveProfile("a@example.com") {
		t.Fatal("RemoveProfile 说没删到")
	}
	if cfg.Account.Email != "b@example.com" || cfg.IMAP.Host != "imap.b.com" {
		t.Errorf("删掉活动账号之后没落到下一个：%+v", cfg.Account)
	}
}

// 邮箱里的危险字符不能进文件名。
func TestAccountFile_SanitizesEmail(t *testing.T) {
	for _, tc := range []struct{ email, want string }{
		{"a@b.com", "a@b.com-index.json"},
		{"A@B.com", "a@b.com-index.json"},                         // 规范化成小写
		{"a/b@c.com", "a_b@c.com-index.json"},                     // `/` 是路径分隔符，必须换掉
		{`a\b@c.com`, "a_b@c.com-index.json"},                     // `\` 同理（Windows）
		{"a b@c.com", "a_b@c.com-index.json"},                     // 空白
		{"", "default-index.json"},                                // 没有邮箱时给个兜底名
		{"../../../etc/passwd", ".._.._.._etc_passwd-index.json"}, // 别想跑出目录
	} {
		if got := accountFile(tc.email, IndexFileName); got != tc.want {
			t.Errorf("accountFile(%q) = %q，want %q", tc.email, got, tc.want)
		}
	}
}

// v1 的单账号文件要认领给账号，而且**只认领一次**。
func TestAdoptLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	// Dir() 走 os.UserConfigDir，在 Windows 上认 APPDATA、Linux 上认
	// XDG_CONFIG_HOME —— 上面三个都设了，三个平台都能落到临时目录。
	cfgDir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	legacy := filepath.Join(cfgDir, IndexFileName)
	if err := os.WriteFile(legacy, []byte(`{"headers":[]}`), 0o600); err != nil {
		t.Fatalf("造 v1 索引: %v", err)
	}

	adopted, err := AdoptLegacyFiles("me@example.com")
	if err != nil {
		t.Fatalf("AdoptLegacyFiles: %v", err)
	}
	if !adopted {
		t.Fatal("有 v1 索引却没认领")
	}
	moved := filepath.Join(cfgDir, accountFile("me@example.com", IndexFileName))
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("认领之后没有目标文件: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("旧文件还在 —— 留着它会让下一个账号又认领一次，两个账号共用一份索引")
	}

	// 第二次是空操作
	again, err := AdoptLegacyFiles("other@example.com")
	if err != nil {
		t.Fatalf("二次 AdoptLegacyFiles: %v", err)
	}
	if again {
		t.Error("旧文件已经搬走了，第二次不该再认领")
	}
}

// 凭据：按账号取密码，以及 v1 单密码的认领。
func TestCredentials_PerAccountAndLegacyAdoption(t *testing.T) {
	creds := Credentials{}
	if _, ok := creds.PasswordFor("a@example.com"); ok {
		t.Error("空凭据里不该有密码")
	}
	creds.SetPassword("a@example.com", "码A")
	if pw, ok := creds.PasswordFor("A@Example.com"); !ok || pw != "码A" {
		t.Errorf("按规范化地址取不到：%q %v", pw, ok)
	}

	// v1：只有一个 Password，认领给唯一的账号
	v1 := Credentials{Password: "老码"}
	if !v1.AdoptLegacyPassword("me@example.com") {
		t.Fatal("v1 的单密码没被认领")
	}
	if pw, _ := v1.PasswordFor("me@example.com"); pw != "老码" {
		t.Errorf("认领之后取到 %q", pw)
	}
	if v1.Password != "" {
		t.Error("认领之后旧字段该清掉，免得下次又认领给别人")
	}

	// 已经有按账号存的密码时，**不再**认领 —— 否则两个账号会共用一个码
	two := Credentials{Password: "老码"}
	two.SetPassword("a@example.com", "码A")
	if two.AdoptLegacyPassword("b@example.com") {
		t.Error("已经存过账号密码了，不该再认领旧字段")
	}

	// 删账号要连 token 一起清
	c3 := Credentials{}
	c3.SetPassword("a@example.com", "码A")
	c3.SetOAuthToken("a@example.com", Token{AccessToken: "t"})
	c3.ForgetAccount("a@example.com")
	if _, ok := c3.PasswordFor("a@example.com"); ok {
		t.Error("ForgetAccount 没清掉密码")
	}
	if _, ok := c3.OAuthToken("a@example.com"); ok {
		t.Error("ForgetAccount 没清掉 token")
	}
}

// Token 的有效性判断要留余量：拿到一个马上过期的 token 再去握手，
// 报出来的错会是「认证失败」，把人引到「密码填错了」的错方向。
func TestToken_ValidKeepsASafetyMargin(t *testing.T) {
	if (Token{AccessToken: "x"}).Valid() {
		t.Error("没有过期时间的 token 不该算有效")
	}
	if (Token{AccessToken: "x", Expiry: timeNowPlus(30)}).Valid() {
		t.Error("30 秒后过期的不该算有效（握手还没完就失效了）")
	}
	if !(Token{AccessToken: "x", Expiry: timeNowPlus(30 * time.Minute)}).Valid() {
		t.Error("半小时后过期的该算有效")
	}
	if (Token{Expiry: timeNowPlus(30 * time.Minute)}).Valid() {
		t.Error("没有 access token 的不该算有效")
	}
}

func timeNowPlus(d time.Duration) time.Time { return time.Now().Add(d) }

// 一条反空壳的自检：上面那批判据用的 v1 夹具必须真的是 v1。
func TestV1FixtureIsActuallyV1(t *testing.T) {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal([]byte(v1ConfigJSON), &probe); err != nil {
		t.Fatalf("夹具不是合法 JSON: %v", err)
	}
	if probe.Version != 1 {
		t.Errorf("夹具的 version = %d，迁移判据要的是 1", probe.Version)
	}
	if !strings.Contains(v1ConfigJSON, `"account"`) {
		t.Error("夹具里没有 v1 的 account 键")
	}
}
