package config

// CustomProviderID 是「自定义」预设的 ID。选它表示手填服务器地址。
const CustomProviderID = "custom"

// AuthMethod 说明这个服务商的凭据栏要填什么 —— 也就是「怎么登录」。
//
// 这不是个装饰性的分类，它决定向导能不能往下走：AuthOAuth2 的服务商填
// 什么密码都连不上，所以向导必须在**选服务商那一步**就拦住，而不是让用户
// 填完密码、等到连接失败才告诉他。
type AuthMethod string

const (
	// AuthSecret 是「填一个密钥」：授权码、应用专用密码、客户端授权密码。
	// 名字里不带"password"，是因为这类东西**都不是**账号登录密码，
	// 叫 password 会让人直接把自己平时登录的那个密码填进来。
	AuthSecret AuthMethod = "secret"

	// AuthOAuth2 是「必须走 OAuth2 授权」，没有一个能填进密码栏的等价物。
	AuthOAuth2 AuthMethod = "oauth2"
)

// Provider 是一个内置的邮箱服务商预设。
//
// 预设解决的是「服务器地址/端口/加密方式」这部分配置，让用户不用去查。
// 但它解决不了「密码栏该填什么」—— 绝大多数服务商都不接受登录密码，
// 必须用授权码或应用专用密码。所以 PasswordLabel 和 Guide 是必需的，
// 它们是「免配置」这个承诺能否兑现的关键。
type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	// IMAPTLS / SMTPTLS 是**分开**的，不能共用一个。
	//
	// 曾经共用过（ApplyProvider 把两边都写成隐式 TLS），在 iCloud 上
	// 立刻出事：它收信是 imap.mail.me.com:993（隐式 TLS），发信却是
	// smtp.mail.me.com:**587**（STARTTLS）。共用一个值时，发信那一半
	// 拿 587 去跑隐式 TLS，握不上手。空值按 TLSImplicit 处理。
	IMAPTLS string `json:"imap_tls"`
	SMTPTLS string `json:"smtp_tls"`

	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	// SMTPNote 是一句关于**发信**的提示。
	//
	// 收信那边的问题（去哪开 IMAP、拿哪个码）由 Guide 覆盖；发信这边的
	// 坑不一样 —— 端口/加密方式的组合、发信量上限、用户名要不要带域名 ——
	// 而且它通常在**收信已经通了之后**才暴露（能收不能发）。所以它单独
	// 一行，就摆在 SMTP 那一行的下面。
	SMTPNote string `json:"smtp_note"`

	Auth          AuthMethod `json:"auth"`
	PasswordLabel string     `json:"password_label"`
	// OpenURL 是「去哪拿凭据」的直达页面。界面上会以 OSC 8 超链接的
	// 形式显示，支持的终端里可以直接点开，省掉用户自己找入口。
	OpenURL string `json:"open_url"`
	// HelpURL 是官方图文说明页，供用户对照步骤。可留空。
	HelpURL string `json:"help_url"`
	// OAuthURL 只在 Auth=AuthOAuth2 时有意义：官方那篇「怎么用 OAuth2
	// 连 IMAP/SMTP」的说明。填了这个才叫**引导** —— 光说"不支持"是
	// 把人丢在原地。
	OAuthURL string   `json:"oauth_url"`
	Guide    []string `json:"guide"`
}

// TLSOrDefault 返回预设里某一半要用的加密方式，空值落到隐式 TLS
// （993/465 最常见）。
//
// 导出是因为**界面也要用同一条规则**：配置页要把"将写入的服务器"显示给
// 用户看，而那一栏显示的加密方式必须和 ApplyProvider 真正写进配置的一致 ——
// 两处各判一次「空串算不算隐式」迟早会漂。
func TLSOrDefault(v string) string {
	if v == "" {
		return TLSImplicit
	}
	return v
}

// Providers 是内置的服务商预设。
//
// 地址来源：各服务商官方文档（QQ/163/126 帮助中心、Google 支持页、
// Apple 支持页 102525、Microsoft 支持页）。实现验收时会逐条实测连通性。
//
// ⚠️ 每一条的 IMAP/SMTP 加密方式**都要单独看**，别照着"都是隐式 TLS"抄 ——
// iCloud 和 Outlook 的发信端口都是 587（STARTTLS）。
var Providers = []Provider{
	{
		ID: "qq", Name: "QQ 邮箱",
		IMAPHost: "imap.qq.com", IMAPPort: 993,
		SMTPHost: "smtp.qq.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "授权码",
		SMTPNote:      "发信走同一个授权码，端口 465（隐式 TLS）。免费版有每日发信量上限。",
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
		Auth:          AuthSecret,
		PasswordLabel: "授权码",
		SMTPNote:      "发信走同一个客户端授权密码，端口 465（隐式 TLS）。",
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
		Auth:          AuthSecret,
		PasswordLabel: "授权码",
		SMTPNote:      "发信走同一个客户端授权密码，端口 465（隐式 TLS）。",
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
		Auth:          AuthSecret,
		PasswordLabel: "应用专用密码",
		SMTPNote:      "发信走同一个应用专用密码，端口 465（隐式 TLS）。免费版每天最多向 500 个收件人发信。",
		// 顺序有讲究：没有两步验证就没有应用专用密码，所以 HelpURL 指向
		// 两步验证页（先决条件），OpenURL 指向真正要拿的凭据页。
		OpenURL: "https://myaccount.google.com/apppasswords",
		HelpURL: "https://myaccount.google.com/signinoptions/two-step-verification",
		Guide: []string{
			"先在 Google 账号里开启「两步验证」—— 不开就没有应用专用密码",
			"进入 账号 → 安全性 → 两步验证 → 应用专用密码",
			"生成一个 16 位密码，把它填进来",
		},
	},
	{
		ID: "icloud", Name: "iCloud 邮箱",
		IMAPHost: "imap.mail.me.com", IMAPPort: 993,
		SMTPHost: "smtp.mail.me.com", SMTPPort: 587, SMTPTLS: TLSStartTLS,
		Auth:          AuthSecret,
		PasswordLabel: "App 专用密码",
		// iCloud 的收/发端口不同（993 隐式 / 587 STARTTLS），所以这里
		// SMTPTLS 必须显式写出来 —— 这正是「两边不能共用一个值」的第一个例子。
		SMTPNote: "发信是 587 + STARTTLS（和收信不一样）。用户名填完整邮箱地址，密码用同一个 App 专用密码。",
		// 用户名那件事值得单独说：Apple 文档里 IMAP 的用户名"通常是 @ 前面
		// 那一段"，但 SMTP 明确要求完整地址；而 clichat 收发共用一个账号名。
		// 好在 Apple 对 IMAP 也给了「用完整地址试试」的退路，所以用完整
		// 地址两边都能过 —— 不为此在配置里加第二个用户名字段。
		OpenURL: "https://account.apple.com/account/manage",
		HelpURL: "https://support.apple.com/zh-cn/102654",
		Guide: []string{
			"先在 Apple 账户里开启「双重认证」—— 不开就没有 App 专用密码",
			"进入 Apple 账户 → 登录与安全 → App 专用密码，生成一个",
			"密码栏填这个 App 专用密码 —— 不是你的 Apple 账户密码",
		},
	},
	{
		ID: "outlook", Name: "Outlook / Microsoft 365",
		IMAPHost: "outlook.office365.com", IMAPPort: 993,
		SMTPHost: "smtp-mail.outlook.com", SMTPPort: 587, SMTPTLS: TLSStartTLS,
		// 微软自 2024-09-16 起在全部租户里禁用了 IMAP/POP/SMTP 的基本认证，
		// 现在只能走 OAuth2（官方原话：Outlook.com requires the use of
		// Modern Auth / OAuth2）。所以这一条**不是**可用的预设，是一条引导。
		Auth:          AuthOAuth2,
		PasswordLabel: "OAuth2 授权",
		SMTPNote:      "地址列在这里只为说明现状：基本认证已停用，填任何密码都连不上。",
		OAuthURL:      "https://learn.microsoft.com/zh-cn/exchange/client-developer/legacy-protocols/how-to-authenticate-an-imap-pop-smtp-application-by-using-oauth",
		HelpURL:       "https://support.microsoft.com/zh-cn/office/pop-imap-%E5%92%8C-smtp-%E8%AE%BE%E7%BD%AE-d088b986-291d-42b8-9564-9c414e2aa040",
		Guide: []string{
			"微软已在所有租户里停用 IMAP/SMTP 的基本认证，密码登录这条路没有了",
			"替代办法只有 OAuth2：应用去微软申请、拿授权码、换令牌 —— clichat 本版本还没做",
			"个人 Outlook 账号的「应用密码」也已随基本认证一起失效，不能当退路",
			"现在的可行选择：换一个上面列出的服务商，或者用企业邮箱走「自定义」",
		},
	},
	{
		ID: CustomProviderID, Name: "自定义（手填服务器）",
		Auth:          AuthSecret,
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

	// 两边各自取自己的加密方式 —— 共用一个值会在 iCloud 那种
	// 「收 993 隐式 / 发 587 STARTTLS」的服务商上配出一个连不上的组合。
	cfg.IMAP = Endpoint{Host: p.IMAPHost, Port: p.IMAPPort, TLS: TLSOrDefault(p.IMAPTLS)}
	cfg.SMTP = Endpoint{Host: p.SMTPHost, Port: p.SMTPPort, TLS: TLSOrDefault(p.SMTPTLS)}
}
