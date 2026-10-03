package config

import (
	"bytes"
	"errors"
	"testing"
)

func TestSealOpenCredentials_RoundTrip(t *testing.T) {
	plain := []byte(`{"password":"授权码-abc123"}`)

	blob, err := SealCredentials("master-pass", plain)
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("密文里竟然包含明文")
	}

	got, err := OpenCredentials("master-pass", blob)
	if err != nil {
		t.Fatalf("OpenCredentials: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("got %q, want %q", got, plain)
	}
}

func TestOpenCredentials_WrongPassword(t *testing.T) {
	blob, err := SealCredentials("right", []byte("secret"))
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	if _, err := OpenCredentials("wrong", blob); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("want ErrWrongPassword, got %v", err)
	}
}

func TestSealCredentials_NonDeterministic(t *testing.T) {
	plain := []byte("same plaintext")
	a, err := SealCredentials("pw", plain)
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	b, err := SealCredentials("pw", plain)
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("两次加密结果相同 —— salt/nonce 可能没随机")
	}
}

func TestOpenCredentials_BadFile(t *testing.T) {
	cases := map[string][]byte{
		"太短":      {1, 2, 3},
		"magic 错": append([]byte("XXXX"), make([]byte, 64)...),
		"版本错":     append([]byte("CLIC\x09"), make([]byte, 64)...),
	}
	for name, data := range cases {
		if _, err := OpenCredentials("pw", data); !errors.Is(err, ErrBadCredsFile) {
			t.Errorf("%s: want ErrBadCredsFile, got %v", name, err)
		}
	}
}

func TestProviders_Complete(t *testing.T) {
	for _, id := range []string{"qq", "163", "126", "gmail"} {
		p := FindProvider(id)
		if p == nil {
			t.Fatalf("预设 %q 不存在", id)
		}
		if p.IMAPHost == "" || p.SMTPHost == "" || p.IMAPPort == 0 || p.SMTPPort == 0 {
			t.Errorf("预设 %q 的服务器地址不完整: %+v", id, p)
		}
		// 密码栏说明和引导文案是「免配置」承诺的核心，缺一不可。
		if p.PasswordLabel == "" {
			t.Errorf("预设 %q 缺少密码栏说明", id)
		}
		if len(p.Guide) == 0 {
			t.Errorf("预设 %q 缺少引导文案", id)
		}
	}
}

// OAuth2 服务商不许假装能连。
//
// 这条判据的前身是 TestProviders_OutlookAbsent，它当时断言「预设里不许
// 有 outlook」。那条规矩的前提变了 —— 用户要的正是**引导**：「列表里找不到
// 我的服务商」和「找到了、并明确告诉我为什么用不了、接下来怎么办」，
// 后者的体验完全不同。所以现在允许这条预设存在。
//
// 但它守的那件事一点没变，而且要求更细：**不许留下"填个密码就能连上"的
// 假象**。两条一起才算数：
//   - Outlook 系必须标 AuthOAuth2 —— 标了向导才拦得住；
//   - 标了 AuthOAuth2 的必须有 OAuthURL 和 Guide —— 否则"拦住"只是把人
//     堵在原地，那还不如让他去试（至少试完知道自己错在哪）。
func TestProviders_OAuthEntriesAreMarkedAndExplained(t *testing.T) {
	seen := 0
	for _, p := range Providers {
		switch p.ID {
		case "outlook", "hotmail", "office365", "live":
			seen++
			if p.Auth != AuthOAuth2 {
				t.Errorf("预设 %q 必须标成 AuthOAuth2，否则向导会放用户去填密码白跑一趟", p.ID)
			}
		}

		if p.Auth != AuthOAuth2 {
			continue
		}
		if p.OAuthURL == "" {
			t.Errorf("预设 %q 标了 OAuth2 却没给 OAuthURL —— 只拦住不解释等于把人丢在原地", p.ID)
		}
		if len(p.Guide) == 0 {
			t.Errorf("预设 %q 标了 OAuth2 却没给 Guide", p.ID)
		}
	}
	if seen == 0 {
		t.Error("预设里没有任何 OAuth2 条目 —— 带着 Outlook 账号来的人会找不到自己那一条")
	}

	// 「两条路都行」的服务商同样要给 OAuthURL。
	//
	// 它和上面那一条的理由不同：不是"拦住之后要解释"，而是**它提供的那个
	// 选项必须能用**。用户在密码那一步按了 Ctrl+O、被问到 client id，这时
	// 没有一个"去哪注册"的链接，他就卡在这儿了 —— 而且很容易以为是自己
	// 填错了，回头去反复重试。
	for _, p := range Providers {
		if p.Auth != AuthEither {
			continue
		}
		if !p.SupportsOAuth2() {
			t.Errorf("预设 %q 是 AuthEither，SupportsOAuth2 却不认它", p.ID)
		}
		if p.OAuthURL == "" {
			t.Errorf("预设 %q 提供了 OAuth2 这条路，却没给 OAuthURL", p.ID)
		}
	}
}

// 预设里的收/发两半要各自对得上**官方文档**。
//
// 表里写死的是文档里的值，不是代码里算出来的值 —— 这一条的分辨力全在这里：
// 直接拿 TLSOrDefault(p.SMTPTLS) 当期望值的话，"有人把 SMTPTLS 照着 IMAPTLS
// 抄了一遍"这种错永远不会红。
//
// iCloud 和 Outlook 是表里仅有的两个**两半不对称**的服务商（收 993 隐式、
// 发 587 STARTTLS）。它们也是当初"两边共用一个隐式 TLS"那个 bug 唯一能
// 暴露出来的地方 —— 所以这两行不许被简化掉。
//
// 表里的值全部来自 2026-10-03 那次核对：每一条都拿 Go 的 crypto/tls 真连过
// （见 internal/mail 的 TestLive_PresetEndpoints），外加官方帮助页对照。
func TestProviders_MatchOfficialSettings(t *testing.T) {
	want := map[string]struct{ imap, smtp Endpoint }{
		"qq":      {Endpoint{"imap.qq.com", 993, TLSImplicit}, Endpoint{"smtp.qq.com", 465, TLSImplicit}},
		"163":     {Endpoint{"imap.163.com", 993, TLSImplicit}, Endpoint{"smtp.163.com", 465, TLSImplicit}},
		"126":     {Endpoint{"imap.126.com", 993, TLSImplicit}, Endpoint{"smtp.126.com", 465, TLSImplicit}},
		"gmail":   {Endpoint{"imap.gmail.com", 993, TLSImplicit}, Endpoint{"smtp.gmail.com", 465, TLSImplicit}},
		"icloud":  {Endpoint{"imap.mail.me.com", 993, TLSImplicit}, Endpoint{"smtp.mail.me.com", 587, TLSStartTLS}},
		"outlook": {Endpoint{"outlook.office365.com", 993, TLSImplicit}, Endpoint{"smtp-mail.outlook.com", 587, TLSStartTLS}},

		"qqexmail": {Endpoint{"imap.exmail.qq.com", 993, TLSImplicit}, Endpoint{"smtp.exmail.qq.com", 465, TLSImplicit}},
		// 官方 Thunderbird 文档写的是 994；实测 994 与 465 都通，取通行的 465。
		"qiye163": {Endpoint{"imap.qiye.163.com", 993, TLSImplicit}, Endpoint{"smtp.qiye.163.com", 465, TLSImplicit}},
		"aliyun":  {Endpoint{"imap.qiye.aliyun.com", 993, TLSImplicit}, Endpoint{"smtp.qiye.aliyun.com", 465, TLSImplicit}},
		// 新浪和 139 的 IMAP 只认 RSA 密钥交换套件 —— 那两条能出现在这张表里，
		// 前提是 internal/mail 的 cipherSuites 补了它们（见那边的注释）。
		// 新浪按后缀分服务器，所以是两条：合成一条的话 @sina.cn 的用户
		// 会拿到一套连不上的地址，界面上还没有任何地方提示他要换域名。
		"sina":    {Endpoint{"imap.sina.com", 993, TLSImplicit}, Endpoint{"smtp.sina.com", 465, TLSImplicit}},
		"sina-cn": {Endpoint{"imap.sina.cn", 993, TLSImplicit}, Endpoint{"smtp.sina.cn", 465, TLSImplicit}},
		"139":     {Endpoint{"imap.139.com", 993, TLSImplicit}, Endpoint{"smtp.139.com", 465, TLSImplicit}},
		// 139 的官方帮助**只列了 143/25 明文端口**，而 143 实测不广告 STARTTLS。
		// 所以这里刻意不是 143 —— 有人"照着官方帮助改回去"时这条会红。

		"yahoo":    {Endpoint{"imap.mail.yahoo.com", 993, TLSImplicit}, Endpoint{"smtp.mail.yahoo.com", 465, TLSImplicit}},
		"zoho":     {Endpoint{"imap.zoho.com", 993, TLSImplicit}, Endpoint{"smtp.zoho.com", 465, TLSImplicit}},
		"fastmail": {Endpoint{"imap.fastmail.com", 993, TLSImplicit}, Endpoint{"smtp.fastmail.com", 465, TLSImplicit}},
	}

	for id, w := range want {
		p := FindProvider(id)
		if p == nil {
			t.Errorf("预设 %q 不见了", id)
			continue
		}
		cfg := Default()
		ApplyProvider(cfg, p)

		if cfg.IMAP != w.imap {
			t.Errorf("%s：收信端点 = %+v，官方文档是 %+v", id, cfg.IMAP, w.imap)
		}
		if cfg.SMTP != w.smtp {
			t.Errorf("%s：发信端点 = %+v，官方文档是 %+v", id, cfg.SMTP, w.smtp)
		}
	}

	// 新加了预设却忘了进上表时，这条会提醒 —— 否则新预设的地址没人守。
	if got, wantN := len(Providers)-1, len(want); got != wantN {
		t.Errorf("预设数量对不上：列表里 %d 个（去掉自定义），上表里 %d 个 —— "+
			"新增的预设要加进 want 表，它才有判据守着", got, wantN)
	}
}

func TestApplyProvider(t *testing.T) {
	cfg := Default()
	ApplyProvider(cfg, FindProvider("qq"))

	if cfg.IMAP.Host != "imap.qq.com" || cfg.IMAP.Port != 993 || cfg.IMAP.TLS != TLSImplicit {
		t.Errorf("IMAP 没写对: %+v", cfg.IMAP)
	}
	if cfg.SMTP.Host != "smtp.qq.com" || cfg.SMTP.Port != 465 || cfg.SMTP.TLS != TLSImplicit {
		t.Errorf("SMTP 没写对: %+v", cfg.SMTP)
	}
	if cfg.Account.Provider != "qq" {
		t.Errorf("Provider 没写对: %q", cfg.Account.Provider)
	}

	before := cfg.IMAP
	ApplyProvider(cfg, FindProvider(CustomProviderID))
	if cfg.IMAP != before {
		t.Error("自定义预设不应改动服务器地址")
	}
}

func TestConfig_ApplyDefaults(t *testing.T) {
	// 一个空配置经过 applyDefaults 后应当可用。
	cfg := &Config{}
	cfg.applyDefaults()

	if cfg.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, CurrentVersion)
	}
	if cfg.Sync.PollIntervalSeconds <= 0 {
		t.Error("轮询间隔没有补默认值")
	}
	if cfg.Sync.InitialMaxMessages <= 0 {
		t.Error("首拉上限没有补默认值")
	}
	if len(cfg.Sync.Folders) == 0 {
		t.Error("文件夹列表没有补默认值")
	}
	if cfg.IMAP.TLS == "" || cfg.SMTP.TLS == "" {
		t.Error("TLS 模式没有补默认值")
	}
}

func TestConfig_Configured(t *testing.T) {
	cfg := Default()
	if cfg.Configured() {
		t.Error("空配置不应算作已配置")
	}
	cfg.Account.Email = "me@qq.com"
	cfg.IMAP.Host = "imap.qq.com"
	cfg.SMTP.Host = "smtp.qq.com"
	if !cfg.Configured() {
		t.Error("填齐后应算作已配置")
	}
}

func TestConfig_SelfNormalized(t *testing.T) {
	cfg := Default()
	cfg.Account.Email = "  Me@QQ.COM "
	if got, want := cfg.Self(), "me@qq.com"; got != want {
		t.Errorf("Self() = %q, want %q", got, want)
	}
}

func TestEndpoint_Addr(t *testing.T) {
	e := Endpoint{Host: "imap.qq.com", Port: 993}
	if got, want := e.Addr(), "imap.qq.com:993"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
}
