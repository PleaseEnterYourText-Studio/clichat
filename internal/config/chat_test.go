package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 身份信息是「每账号 + 全局兜底」：账号自己那份优先，没有就用全局。
//
// 这三条一起守着 ChatFor 的两条分支 —— 只测「有覆盖」会漏掉兜底，
// 只测「没覆盖」会漏掉覆盖本身根本没生效（而后者症状更隐蔽：
// 用户设了却不生效）。
func TestChatFor_AccountOverrideWins(t *testing.T) {
	cfg := Default()
	cfg.Chat = Chat{Nick: "全局昵称", NameColor: "#111111"}
	own := Chat{Nick: "账号昵称", TextColor: "#222222"}
	cfg.Accounts = []Profile{
		{Account: Account{Email: "work@example.com"}, Chat: &own},
		{Account: Account{Email: "me@example.com"}},
	}

	if got := cfg.ChatFor("work@example.com"); got.Nick != "账号昵称" {
		t.Errorf("有自己那份时该用它，昵称得到 %q，想要 账号昵称", got.Nick)
	}
	// ⚠️ 覆盖是整套的：账号那份没写 NameColor，就该是空，而不是
	// 悄悄从全局继承过来。逐字段合并做不到（见 Profile.Chat 的注释），
	// 所以这里必须把「不合并」钉住，免得以后有人「顺手优化」成合并。
	if got := cfg.ChatFor("work@example.com"); got.NameColor != "" {
		t.Errorf("覆盖是整套的，不该继承全局的 NameColor，得到 %q", got.NameColor)
	}
}

func TestChatFor_FallsBackToGlobal(t *testing.T) {
	cfg := Default()
	cfg.Chat = Chat{Nick: "全局昵称", NameColor: "#111111"}
	cfg.Accounts = []Profile{{Account: Account{Email: "me@example.com"}}}

	got := cfg.ChatFor("me@example.com")
	if got.Nick != "全局昵称" || got.NameColor != "#111111" {
		t.Errorf("账号没单独设时该整套落回全局，得到 %+v", got)
	}
}

// 找不到这个账号时也退回全局 —— 不能返回零值。
//
// 返回零值意味着「昵称空、颜色空、不隐藏设备」，发出去的是一张
// 空白身份卡：对方那边只能显示邮箱前缀。而这条路径在真实使用里
// 是会走到的（配置刚被手改坏、账号刚被删）。
func TestChatFor_UnknownAccountFallsBackToGlobal(t *testing.T) {
	cfg := Default()
	cfg.Chat = Chat{Nick: "全局昵称"}

	if got := cfg.ChatFor("nobody@example.com"); got.Nick != "全局昵称" {
		t.Errorf("账号不存在时该退回全局，得到 %+v", got)
	}
}

func TestSetChatFor_NilRestoresGlobal(t *testing.T) {
	cfg := Default()
	cfg.Chat = Chat{Nick: "全局昵称"}
	own := Chat{Nick: "账号昵称"}
	cfg.Accounts = []Profile{{Account: Account{Email: "me@example.com"}, Chat: &own}}

	if !cfg.OwnChat("me@example.com") {
		t.Fatal("刚设过自己的那份，OwnChat 该为真")
	}

	if !cfg.SetChatFor("me@example.com", nil) {
		t.Fatal("SetChatFor(nil) 该成功")
	}
	if cfg.OwnChat("me@example.com") {
		t.Error("清掉之后 OwnChat 该为假")
	}
	if got := cfg.ChatFor("me@example.com"); got.Nick != "全局昵称" {
		t.Errorf("恢复默认后该落回全局，得到 %q", got.Nick)
	}
}

// SetChatFor 存的必须是**副本**。
//
// 调用方（设置页）手里那个结构是会继续被改的草稿，存指针会让
// 「界面上刚改、还没点保存」直接写进配置 —— 取消都取消不掉。
func TestSetChatFor_StoresACopy(t *testing.T) {
	cfg := Default()
	cfg.Accounts = []Profile{{Account: Account{Email: "me@example.com"}}}

	draft := Chat{Nick: "改之前"}
	cfg.SetChatFor("me@example.com", &draft)
	draft.Nick = "改之后"

	if got := cfg.ChatFor("me@example.com"); got.Nick != "改之前" {
		t.Errorf("配置跟着调用方的草稿一起变了，得到 %q，想要 改之前", got.Nick)
	}
}

func TestSetChatFor_UnknownAccountReturnsFalse(t *testing.T) {
	cfg := Default()
	if cfg.SetChatFor("nobody@example.com", &Chat{Nick: "x"}) {
		t.Error("账号不存在时该返回 false")
	}
}

// 外观：零值要落到「两栏 + 默认宽度」，并且越界值被夹回区间。
//
// 手改配置把宽度写成 500 或 -3 是很容易的事，夹一下比渲染时再兜底
// 更早、也更好解释。
func TestAppearance_DefaultsAndClamping(t *testing.T) {
	cfg := Default()
	if cfg.StartupLayout() != LayoutNormal {
		t.Errorf("默认布局该是两栏，得到 %q", cfg.StartupLayout())
	}
	if cfg.ZenWidthLimit() != ZenDefaultWidth {
		t.Errorf("默认禅宽度该是 %d，得到 %d", ZenDefaultWidth, cfg.ZenWidthLimit())
	}

	cfg.Appearance.Layout = "随便写的"
	if cfg.StartupLayout() != LayoutNormal {
		t.Error("非法布局名该退回两栏，而不是原样返回")
	}
	cfg.Appearance.Layout = LayoutZen
	if cfg.StartupLayout() != LayoutZen {
		t.Error("设成 zen 之后该返回 zen")
	}

	// ⚠️ 越界与「没设」都退回**默认宽度**（ZenDefaultWidth），不是退回上限。
	// 退回上限会让「把配置写坏的人」拿到一个他从没选过的最大宽度。
	for _, tc := range []struct{ in, want int }{
		{0, ZenDefaultWidth},
		{-3, ZenDefaultWidth},
		{500, ZenDefaultWidth},
		{ZenMinWidth, ZenMinWidth},
		{ZenMaxWidth, ZenMaxWidth},
		{72, 72},
	} {
		cfg.Appearance.ZenWidth = tc.in
		if got := cfg.ZenWidthLimit(); got != tc.want {
			t.Errorf("ZenWidth=%d 时该得到 %d，实际 %d", tc.in, tc.want, got)
		}
	}
}

// 外观和身份一样要能原样往返落盘。
func TestAppearance_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.SetPath(path)
	cfg.Account.Email = "me@example.com"
	cfg.Appearance = Appearance{Layout: LayoutZen, ZenWidth: 64, ZenSplit: true}
	cfg.Chat = Chat{Nick: "全局昵称"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.Appearance != cfg.Appearance {
		t.Errorf("外观没原样往返：%+v，想要 %+v", got.Appearance, cfg.Appearance)
	}
	if got.StartupLayout() != LayoutZen {
		t.Error("布局没落盘")
	}
	if !got.Appearance.ZenSplit {
		t.Error("ZenSplit 没落盘")
	}
}

// 每账号的身份要能原样往返，且**不该**污染全局那份。
func TestProfileChat_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.SetPath(path)
	cfg.Chat = Chat{Nick: "全局昵称"}
	cfg.Accounts = []Profile{
		{Account: Account{Email: "work@example.com"}},
		{Account: Account{Email: "me@example.com"}},
	}
	cfg.SetActive("work@example.com")
	cfg.SetChatFor("work@example.com", &Chat{Nick: "张工", NameColor: "#ff8800"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !got.OwnChat("work@example.com") {
		t.Fatal("work 账号自己那份没落盘")
	}
	if n := got.ChatFor("work@example.com").Nick; n != "张工" {
		t.Errorf("work 昵称得到 %q，想要 张工", n)
	}
	if n := got.ChatFor("me@example.com").Nick; n != "全局昵称" {
		t.Errorf("另一个账号该还是全局昵称，得到 %q", n)
	}
	if got.Chat.Nick != "全局昵称" {
		t.Errorf("全局那份被账号覆盖污染了：%q", got.Chat.Nick)
	}
}

// ⚠️ 回归哨兵：拿**真实的 v1 文件**喂进去，账号不能凭空消失。
//
// 这条判据在仓库里已经栽过一次：migrate 当时写成「检查 c.Account.Email
// 是否非空」，而 v1 文件里那三个字段的标签是 json:"-"，读进来永远是空的
// → 判定「没有账号可迁」→ 用户账号消失、界面直接跳回配置向导。
//
// 加了 v3 之后风险更大：migrate 现在按版本分段，写错分段就会把 v2
// 的文件送进 v1 的解析路径。所以这里连 v2 一起钉。
func TestMigrate_RealOldFilesKeepAccounts(t *testing.T) {
	t.Run("v1 单账号", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		v1 := `{
  "version": 1,
  "account": {"provider": "qq", "email": "old@example.com", "display_name": "老账号"},
  "imap": {"host": "imap.qq.com", "port": 993, "tls": "ssl"},
  "smtp": {"host": "smtp.qq.com", "port": 465, "tls": "ssl"},
  "chat": {"nick": "旧的全局昵称", "name_color": "#abcdef"}
}`
		if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFrom(path)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if len(cfg.Accounts) != 1 {
			t.Fatalf("v1 账号该被收进列表，得到 %d 个", len(cfg.Accounts))
		}
		if cfg.Account.Email != "old@example.com" {
			t.Errorf("活动账号该是 old@example.com，得到 %q", cfg.Account.Email)
		}
		if cfg.Version != CurrentVersion {
			t.Errorf("版本该升到 %d，得到 %d", CurrentVersion, cfg.Version)
		}
		// v1 里那个顶层 chat 段在 v3 里语义不变：它是**全局默认**。
		if cfg.Chat.Nick != "旧的全局昵称" {
			t.Errorf("v1 的 chat 段该原样成为全局默认，得到 %q", cfg.Chat.Nick)
		}
		// 而且账号自己那份是空的 —— 升级不该顺手给每个账号抄一份，
		// 否则以后改全局默认就再也影响不到它们了。
		if cfg.OwnChat("old@example.com") {
			t.Error("升级不该给账号凭空造一份自己的身份")
		}
		if got := cfg.ChatFor("old@example.com"); got.Nick != "旧的全局昵称" {
			t.Errorf("生效昵称该来自全局，得到 %q", got.Nick)
		}
	})

	t.Run("v2 多账号", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		v2 := `{
  "version": 2,
  "accounts": [
    {"account": {"provider": "qq", "email": "a@example.com", "display_name": "A"}},
    {"account": {"provider": "163", "email": "b@example.com", "display_name": "B"}}
  ],
  "active": "b@example.com",
  "chat": {"nick": "v2 的全局昵称"}
}`
		if err := os.WriteFile(path, []byte(v2), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFrom(path)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if len(cfg.Accounts) != 2 {
			t.Fatalf("v2 的两个账号都该在，得到 %d 个", len(cfg.Accounts))
		}
		if cfg.Active != "b@example.com" {
			t.Errorf("活动账号该保持 b@example.com，得到 %q", cfg.Active)
		}
		if cfg.Version != CurrentVersion {
			t.Errorf("版本该升到 %d，得到 %d", CurrentVersion, cfg.Version)
		}
		if cfg.Chat.Nick != "v2 的全局昵称" {
			t.Errorf("v2 的 chat 段该原样保留，得到 %q", cfg.Chat.Nick)
		}
		// v2 文件里没有 appearance 段 → 该落到默认外观，而不是零值。
		if cfg.StartupLayout() != LayoutNormal {
			t.Error("没有 appearance 段时该是两栏")
		}
		if cfg.ZenWidthLimit() != ZenDefaultWidth {
			t.Error("没有 appearance 段时禅宽度该是默认值")
		}
	})
}
