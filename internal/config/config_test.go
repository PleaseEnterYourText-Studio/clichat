package config

import (
	"path/filepath"
	"testing"
)

// Save 在没有绑定路径时必须报错，而不是「猜」一个默认位置。
//
// 这不是洁癖：早先的版本会退回默认配置目录，于是任何拿着内存里现造的
// Config（测试、向导中途）一保存，就把用户真实的 config.json 盖掉了。
// 那种 bug 平时看不出来，等用户发现设置被清空时已经晚了。
func TestConfig_SaveWithoutPathIsRefused(t *testing.T) {
	cfg := Default()
	if cfg.Path() != "" {
		t.Fatalf("Default 不该带路径，实际 %q", cfg.Path())
	}
	if err := cfg.Save(); err == nil {
		t.Error("没有绑定路径时 Save 应该报错")
	}
}

// SetPath 之后 Save / LoadFrom 要能原样往返，包括这一版新加的
// 「接收全部邮件」开关。
func TestConfig_SetPathRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Default()
	cfg.SetPath(path)
	cfg.Account.Email = "me@example.com"
	cfg.Sync.AllMail = true

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.Account.Email != "me@example.com" {
		t.Errorf("邮箱 = %q", got.Account.Email)
	}
	if !got.Sync.AllMail {
		t.Error("AllMail 没能往返")
	}
	if got.Path() != path {
		t.Errorf("路径 = %q, want %q", got.Path(), path)
	}
}

// 文件不存在时 LoadFrom 返回一份默认配置，不报错 —— 首次启动就该是
// 这个行为，向导靠它自然起来。
func TestConfig_LoadFromMissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("文件不存在不该报错: %v", err)
	}
	if cfg.Path() != path {
		t.Errorf("路径 = %q, want %q —— 少了这个后面 Save 会失败", cfg.Path(), path)
	}
	if cfg.Sync.AllMail {
		t.Error("默认不该是全量模式：那会让新用户第一次启动就卡在几万封的同步上")
	}
	if cfg.Sync.InitialMaxMessages <= 0 || cfg.Sync.InitialDays <= 0 {
		t.Errorf("默认值不完整: %+v", cfg.Sync)
	}
}
