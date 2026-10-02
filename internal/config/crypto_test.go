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

func TestProviders_OutlookAbsent(t *testing.T) {
	// Outlook 个人版已停用基本认证，v1 不做 OAuth2，因此它不该出现在预设里。
	for _, p := range Providers {
		if p.ID == "outlook" || p.ID == "hotmail" {
			t.Errorf("Outlook 个人版不支持密码认证，不应有预设: %q", p.ID)
		}
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
