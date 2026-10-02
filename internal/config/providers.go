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
	ID            string `json:"id"`
	Name          string `json:"name"`
	IMAPHost      string `json:"imap_host"`
	IMAPPort      int    `json:"imap_port"`
	SMTPHost      string `json:"smtp_host"`
	SMTPPort      int    `json:"smtp_port"`
	PasswordLabel string `json:"password_label"`
	// OpenURL 是「去哪拿凭据」的直达页面。界面上会以 OSC 8 超链接的
	// 形式显示，支持的终端里可以直接点开，省掉用户自己找入口。
	OpenURL string `json:"open_url"`
	// HelpURL 是官方图文说明页，供用户对照步骤。可留空。
	HelpURL string   `json:"help_url"`
	Guide   []string `json:"guide"`
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
		// 直达「账号与安全」设置页。
		//
		// 这个路由是实测挖出来的，不是猜的：新版 QQ 邮箱在
		// wx.mail.qq.com（不是 mail.qq.com）；未登录访问 /account/index
		// 返回 302 且 origin_url 里原样保留了路径，而乱试的路径一律 404。
		//
		// 验证边界要说清楚：**路由存在**是验证过的，**页面内容**没有 ——
		// 那需要登录才能看到。所以下面 Guide 里的文字步骤仍然保留，
		// 作为链接万一不对时的退路。
		OpenURL: "https://wx.mail.qq.com/account/index",
		HelpURL: "https://help.mail.qq.com/detail/0/1087",
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
		// 163（和下面的 126）没有可用的设置页深链接，只给首页和帮助中心。
		//
		// 为什么给不了（实测，不是没试）：设置页是 SPA 内部的 hash 路由
		// （mail.163.com/js6/main.jsp#module=settings...）。而 /js6/main.jsp
		// 未登录时 302 到 https://mail.163.com/ —— 重定向把**路径整个丢掉了**，
		// 不像 QQ 会在 origin_url 里原样保留。路径都保不住，hash 更传不过去。
		// 基线法：伪造路径 404，/js6/main.jsp 302。
		OpenURL: "https://mail.163.com/",
		HelpURL: "https://help.mail.163.com/",
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
		// 126 同 163，没有可用的设置页深链接（原因见上面 163 那段）。
		OpenURL: "https://mail.126.com/",
		HelpURL: "https://help.mail.126.com/",
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
		// 顺序有讲究：没有两步验证就没有应用专用密码，所以 HelpURL 指向
		// 两步验证页（先决条件），OpenURL 指向真正要拿的凭据页。
		OpenURL: "https://myaccount.google.com/apppasswords",
		HelpURL: "https://myaccount.google.com/signinoptions/two-step-verification",
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

// ApplyProvider 把预设的服务器地址写进配置。自定义预设没有地址可写，
// 但同样记下用户的选择。
func ApplyProvider(cfg *Config, p *Provider) {
	if p == nil {
		return
	}
	cfg.Account.Provider = p.ID

	// 自定义的地址由向导手填，这里不动端点。
	if p.ID == CustomProviderID {
		return
	}

	cfg.IMAP = Endpoint{Host: p.IMAPHost, Port: p.IMAPPort, TLS: TLSImplicit}
	cfg.SMTP = Endpoint{Host: p.SMTPHost, Port: p.SMTPPort, TLS: TLSImplicit}
}
