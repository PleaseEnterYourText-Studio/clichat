package config

// CustomProviderID 是「自定义」预设的 ID。选它表示手填服务器地址。
const CustomProviderID = "custom"

// Provider 是一个内置的邮箱服务商预设。
//
// 预设解决的是「服务器地址/端口/加密方式」这部分配置，让用户不用去查。
// 但它解决不了「密码栏该填什么」—— 绝大多数服务商都不接受登录密码，
// 必须用授权码或应用专用密码。所以 PasswordLabel 和 Guide 是必需的，
// 它们是「免配置」这个承诺能否兑现的关键。
type Provider struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	IMAPHost      string   `json:"imap_host"`
	IMAPPort      int      `json:"imap_port"`
	SMTPHost      string   `json:"smtp_host"`
	SMTPPort      int      `json:"smtp_port"`
	PasswordLabel string   `json:"password_label"`
	Guide         []string `json:"guide"`
}

// Providers 是内置的服务商预设。
//
// 地址来源：常见邮箱服务器配置汇总（更新于 2025-10-22）。
// 实现验收时会逐条实测连通性。
//
// 两个刻意的缺席：
//
//   - Outlook / Outlook.com 个人版 —— 微软自 2024-09-16 起停用了它的
//     IMAP/SMTP 基本认证，填密码连不上，必须走 OAuth2。v1 不做 OAuth2，
//     所以它不在列表里，而不是我们忘了。
//   - iCloud —— 服务器地址尚未核实，宁可不给预设，也不给一个可能错的。
var Providers = []Provider{
	{
		ID: "qq", Name: "QQ 邮箱",
		IMAPHost: "imap.qq.com", IMAPPort: 993,
		SMTPHost: "smtp.qq.com", SMTPPort: 465,
		PasswordLabel: "授权码",
		Guide: []string{
			"登录 QQ 邮箱网页版，点顶部「设置」，选「账号与安全」",
			"找到「POP3/IMAP/SMTP/Exchange/CardDAV服务」区域，点旁边的「开启」",
			"按提示用绑定的手机号发送指定内容到指定号码，发完点「我已发送」",
			"页面会弹出 16 位授权码，复制它填进来 —— 不是你的 QQ 密码",
		},
	},
	{
		ID: "163", Name: "163 邮箱",
		IMAPHost: "imap.163.com", IMAPPort: 993,
		SMTPHost: "smtp.163.com", SMTPPort: 465,
		PasswordLabel: "授权码",
		Guide: []string{
			"登录 163 邮箱网页版，进入 设置 → POP3/SMTP/IMAP",
			"开启 IMAP/SMTP 服务，按提示扫码或发短信验证",
			"设置客户端授权密码，把它填进来 —— 不是登录密码",
		},
	},
	{
		ID: "126", Name: "126 邮箱",
		IMAPHost: "imap.126.com", IMAPPort: 993,
		SMTPHost: "smtp.126.com", SMTPPort: 465,
		PasswordLabel: "授权码",
		Guide: []string{
			"登录 126 邮箱网页版，进入 设置 → POP3/SMTP/IMAP",
			"开启 IMAP/SMTP 服务，按提示扫码或发短信验证",
			"设置客户端授权密码，把它填进来 —— 不是登录密码",
		},
	},
	{
		ID: "gmail", Name: "Gmail",
		IMAPHost: "imap.gmail.com", IMAPPort: 993,
		SMTPHost: "smtp.gmail.com", SMTPPort: 465,
		PasswordLabel: "应用专用密码",
		Guide: []string{
			"先在 Google 账号里开启「两步验证」—— 不开就没有应用专用密码",
			"进入 账号 → 安全性 → 两步验证 → 应用专用密码",
			"生成一个 16 位密码，把它填进来",
			"注意：Gmail 免费版每天最多向 500 个收件人发信",
		},
	},
	{
		ID: CustomProviderID, Name: "自定义（手填服务器）",
		PasswordLabel: "密码或授权码",
		Guide: []string{
			"手填 IMAP / SMTP 服务器地址与端口",
			"大多数服务商不接受登录密码，需要用授权码或应用专用密码",
		},
	},
}

// FindProvider 按 ID 查预设。找不到返回 nil。
func FindProvider(id string) *Provider {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// ApplyProvider 把预设的服务器地址写进配置。
// 自定义预设不做任何改动。
func ApplyProvider(cfg *Config, p *Provider) {
	if p == nil || p.ID == CustomProviderID {
		return
	}
	cfg.Account.Provider = p.ID
	cfg.IMAP = Endpoint{Host: p.IMAPHost, Port: p.IMAPPort, TLS: TLSImplicit}
	cfg.SMTP = Endpoint{Host: p.SMTPHost, Port: p.SMTPPort, TLS: TLSImplicit}
}
