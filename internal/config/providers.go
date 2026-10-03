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

	// AuthEither 是「两条路都走得通」：默认填密钥（步骤少、不用注册应用），
	// 用户想走 OAuth2 可以从那一步切过去。
	//
	// 为什么需要这一档：Gmail 既接受应用专用密码、也接受 XOAUTH2。把它写死成
	// AuthSecret 的话，Google Workspace 里被管理员禁掉应用专用密码的用户就
	// 完全没路可走 —— 而 OAuth2 那条路的代码本来是好的，只是没人能走到它。
	AuthEither AuthMethod = "either"
)

// SupportsOAuth2 报告这个预设能不能走 OAuth2。
//
// ⚠️ 它必须和 oauth.ForProvider 的覆盖范围**一致**：说能走而那边不认识，
// 用户会在填完 client id 之后撞上一句「这个版本还不认识 X 的 OAuth 流程」。
// 有一条判据盯着这个等式（tui 包里的 TestProviders_OAuthSupportMatchesEndpoints）。
func (p Provider) SupportsOAuth2() bool {
	return p.Auth == AuthOAuth2 || p.Auth == AuthEither
}

// RequiresOAuth2 报告这个预设是不是**只能**走 OAuth2（没有密钥那条路）。
func (p Provider) RequiresOAuth2() bool { return p.Auth == AuthOAuth2 }

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
// # 这些地址是怎么核对的
//
// **不是照抄文档，也不是照抄博客** —— 每一条的 host/port 都拿 Go 自己的
// crypto/tls 真连过一次（TCP + TLS 握手 + 读 banner，不登录）：
//
//	CLICHAT_LIVE_TLS=1 go test ./internal/mail/ -run TestLive_PresetEndpoints -v
//
// 那条命令同时就是这份表的回归判据：谁换了端口、撤了加密方式、域名下线，
// 跑一遍就红。为什么要这样：二手博客互相抄，官方帮助有时只列明文端口
// （139 就是一例），而**只有连一次能定论**。核对日期 2026-10-03。
//
// # 读这张表要记住的两件事
//
// ⚠️ 每一条的 IMAP/SMTP 加密方式**都要单独看**，别照着"都是隐式 TLS"抄 ——
// iCloud 和 Outlook 的发信端口都是 587（STARTTLS）。
//
// ⚠️ PasswordLabel 是「密码栏填什么」，它**几乎从来不是**账号登录密码。
// 这一栏写错，用户会遇到最难自查的一类故障：配置看起来全对，就是认证失败。
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
		ID: "qqexmail", Name: "腾讯企业邮 / 企业微信邮箱",
		IMAPHost: "imap.exmail.qq.com", IMAPPort: 993,
		SMTPHost: "smtp.exmail.qq.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "客户端专用密码",
		// 这一条的两个坑都和别家不同，写清楚是因为用户在两处都会误判：
		//   1. 发信不是"发不出去"，而是**发出去了但网页版看不到** ——
		//      用户会以为发信失败、反复重发；
		//   2. 「只同步到最近 30 天」正好压在本程序「首次同步从什么时候
		//      开始拉」那条设置上，两边叠起来会少收一大截。
		SMTPNote: "发信端口 465（隐式 TLS）。客户端发出的信默认不进网页版「已发送」，要去 收发信设置 勾上「保存已发送至服务器」。",
		// 拿密码的那一页在登录态里面（设置 → 邮箱绑定），给不了深链接 ——
		// 同 163 的理由：未登录访问会被重定向掉，路径保不住。
		OpenURL: "https://exmail.qq.com/",
		HelpURL: "https://open.work.weixin.qq.com/help2/pc/19886",
		// Guide 比别家长，因为企业邮多一道**管理员**的闸：成员自己在网页版
		// 点了「开启服务」也可能什么都开不了 —— 管理端没给这个成员开客户端
		// 访问权限。不说这一步，用户会在网页版和客户端之间来回撞，
		// 而两边都不告诉他去看管理端。
		Guide: []string{
			"先让管理员开通客户端权限，没开的话成员这边怎么设置都没用",
			"管理端路径：企业微信 → 协作 → 邮件 → 安全管理 → 客户端访问权限",
			"网页版登录企业邮箱：设置 → 收发信设置，开启 IMAP/SMTP 服务",
			"开了「安全登录」的邮箱，密码栏填 设置 → 邮箱绑定 里的客户端专用密码",
			"默认只同步最近 30 天邮件，收全部要在 收发信设置 里改收取范围",
			"信创版主机名是 xcimap / xcsmtp.exmail.qq.com",
		},
	},
	{
		ID: "qiye163", Name: "网易企业邮",
		IMAPHost: "imap.qiye.163.com", IMAPPort: 993,
		// 994 那段不是废话：官方帮助确实写 994，实测两个都开着。写清楚是为了
		// 让下一个看见 994 的文档、想"顺手改成官方说的那个"的人知道这里查过了。
		SMTPHost: "smtp.qiye.163.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "客户端授权码",
		SMTPNote:      "发信端口 465（隐式 TLS）。官方帮助写的是 994，实测 465 和 994 都开着 —— 465 是通行标准，选它。",
		OpenURL:       "https://qiye.163.com/",
		HelpURL:       "https://office.163.com/helpCenter/mail/d/1967865131602903042.html",
		Guide: []string{
			"网页版登录企业邮箱，在设置里开启 IMAP/SMTP 服务",
			"到 设置 → 客户端授权码 生成一个，填进来 —— 不是网页登录密码",
			"管理员可以关掉成员的自助开启权限，开不了就问管理员",
		},
	},
	{
		ID: "aliyun", Name: "阿里邮箱（企业邮）",
		IMAPHost: "imap.qiye.aliyun.com", IMAPPort: 993,
		SMTPHost: "smtp.qiye.aliyun.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "三方客户端安全密码",
		// 80/587 明确未开通，所以这一句是"别自作聪明换端口"的护栏。
		SMTPNote: "发信端口 465（隐式 TLS）。官方明确说明 80 和 587 端口未开通，别去试 587。" +
			"中国香港地区的主机名是 imaphk / smtphk.qiye.aliyun.com，旧版是 imap / smtp.mxhichina.com。",
		OpenURL: "https://qiye.aliyun.com/",
		HelpURL: "https://help.aliyun.com/zh/document_detail/36576.html",
		// 第一条是最要紧的：阿里邮箱**默认禁止第三方客户端**。用户拿到的
		// 报错是「用户名或密码错误」，而密码是对的 —— 那道闸在管理员那边。
		Guide: []string{
			"⚠️ 阿里邮箱默认禁止第三方客户端：先确认管理员没在后台限制、且已开通 POP3/IMAP 权限",
			"建议先在 设置 → 安全设置 里生成「三方客户端安全密码」再用来登录",
			"组织强制开启安全密码时，填邮箱登录密码会被直接拒绝",
			"老域名的地址 imap.mxhichina.com / smtp.mxhichina.com 仍然有效",
		},
	},
	{
		ID: "sina", Name: "新浪邮箱（@sina.com）",
		IMAPHost: "imap.sina.com", IMAPPort: 993,
		SMTPHost: "smtp.sina.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "授权码（16 位）",
		SMTPNote:      "发信端口 465（隐式 TLS）。官方说 465 不通时可以换 587，但有些地区只开其中一个。",
		OpenURL:       "https://mail.sina.com.cn/",
		HelpURL:       "https://help.sina.com.cn/comquestiondetail/view/160/",
		Guide: []string{
			"网页版登录新浪邮箱，进入 设置区 → 客户端POP/IMAP/SMTP",
			"开启 IMAP4/SMTP 服务",
			"在同一页获取 16 位「授权码」，填进来 —— 不是登录密码",
		},
	},
	{
		// 与上一条**只差地址**，但必须独立成条：新浪按邮箱后缀分服务器，
		// 而预设写进配置后用户在向导里改不了（只有「自定义」才让手填）。
		// 合成一条的话，@sina.cn 的用户会拿到一套连不上的地址，而且
		// 界面上没有任何地方提示他"该换域名"。
		ID: "sina-cn", Name: "新浪邮箱（@sina.cn）",
		IMAPHost: "imap.sina.cn", IMAPPort: 993,
		SMTPHost: "smtp.sina.cn", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "授权码（16 位）",
		SMTPNote:      "发信端口 465（隐式 TLS）。官方说 465 不通时可以换 587，但有些地区只开其中一个。",
		OpenURL:       "https://mail.sina.com.cn/",
		HelpURL:       "https://help.sina.com.cn/comquestiondetail/view/160/",
		Guide: []string{
			"网页版登录新浪邮箱，进入 设置区 → 客户端POP/IMAP/SMTP",
			"开启 IMAP4/SMTP 服务",
			"在同一页获取 16 位「授权码」，填进来 —— 不是登录密码",
			"VIP 邮箱的主机名是 vip.sina.cn —— 用「自定义」手填",
		},
	},
	{
		ID: "139", Name: "139 邮箱（中国移动）",
		IMAPHost: "imap.139.com", IMAPPort: 993,
		SMTPHost: "smtp.139.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "授权码",
		// ⚠️ 这一家里有个别家都没有的坑，记在这儿免得以后有人把它当成笔误：
		// imap.139.com 的 993 **只提供 RSA 密钥交换的套件**（一个 ECDHE 都不认），
		// 所以 Go 默认套件表下它握手就失败，报 `tls: handshake failure`。
		// 用户端的表现是「配置明明和官方帮助一样，就是连不上」。
		// 兜住它的是 internal/mail 的 cipherSuites —— 改那边之前先看这段。
		//
		// 官方帮助只列了 143/25 两个明文端口，而且 143 实测**不广告 STARTTLS**：
		// 明文是这条路唯一的官方说法。所以别把预设改成 143。
		SMTPNote: "发信端口 465（隐式 TLS）。139 的官方帮助只列了明文端口（143/110/25），加密端口是实测确认可用的。",
		OpenURL:  "https://mail.10086.cn/",
		HelpURL:  "https://help.mail.10086.cn/statichtml/1/Content/3620.html",
		Guide: []string{
			"登录 139 邮箱（手机号@139.com），开启 IMAP/SMTP 服务 —— 系统默认是关闭的",
			"设置路径：设置 → 常规设置 → 账户与安全 → 邮箱协议设置",
			"系统默认关闭 POP3/IMAP，不开通的话怎么连都只会报认证失败",
			"在同一页获取「邮箱授权码」，填进来 —— 不是邮箱登录密码",
		},
	},
	{
		ID: "gmail", Name: "Gmail",
		IMAPHost: "imap.gmail.com", IMAPPort: 993,
		SMTPHost: "smtp.gmail.com", SMTPPort: 465,
		// 两条路都行，所以是 AuthEither 而不是 AuthSecret。
		//
		// 默认那条（应用专用密码）步骤少得多：不用去 Cloud Console 建应用、
		// 不用填 client id。但 Google Workspace 的管理员可以把应用专用密码
		// 整个关掉，那时只剩 OAuth2 —— 这两种情况都真实存在，所以两条都留着，
		// 让用户在密码那一步自己选。
		Auth:          AuthEither,
		PasswordLabel: "应用专用密码",
		SMTPNote: "发信走同一个应用专用密码，端口 465（隐式 TLS）。免费版每天最多向 500 个收件人发信。" +
			"若管理员禁用了应用专用密码，就在密码那一步改用 OAuth2 授权。",
		// 顺序有讲究：没有两步验证就没有应用专用密码，所以 HelpURL 指向
		// 两步验证页（先决条件），OpenURL 指向真正要拿的凭据页。
		OpenURL: "https://myaccount.google.com/apppasswords",
		HelpURL: "https://myaccount.google.com/signinoptions/two-step-verification",
		// 走 OAuth2 时要在自己的 Cloud Console 里建一个 OAuth 客户端。
		// 类型选「桌面应用」—— 那份凭据才允许 http://localhost 回调。
		OAuthURL: "https://console.cloud.google.com/apis/credentials",
		Guide: []string{
			"先在 Google 账号里开启「两步验证」—— 不开就没有应用专用密码",
			"进入 账号 → 安全性 → 两步验证 → 应用专用密码",
			"生成一个 16 位密码，把它填进来",
			"（企业/学校账号常被管理员禁用应用专用密码，那就下一步选 OAuth2）",
		},
	},
	{
		ID: "outlook", Name: "Outlook / Microsoft 365",
		IMAPHost: "outlook.office365.com", IMAPPort: 993,
		// 个人 outlook.com 账号的官方值是 smtp-mail.outlook.com；工作/学校
		// （Microsoft 365）官方值换成 smtp.office365.com。两个都实测过，都通。
		// 这里取前者 —— 它对个人账号是官方值，而个人账号是本程序的主要用户。
		SMTPHost: "smtp-mail.outlook.com", SMTPPort: 587, SMTPTLS: TLSStartTLS,
		// 微软自 2024-09-16 起在全部租户里禁用了 IMAP/POP/SMTP 的基本认证，
		// 现在只能走 OAuth2（官方原话：Outlook.com requires the use of
		// Modern Auth / OAuth2）。所以这一条是 AuthOAuth2，不是 AuthEither：
		// 密码栏填什么都是白填。
		Auth:          AuthOAuth2,
		PasswordLabel: "OAuth2 授权",
		SMTPNote: "发信是 587 + STARTTLS（和收信不一样）。工作/学校账号把服务器换成 smtp.office365.com，端口不变。" +
			"基本认证已停用，所以发信也要用同一个 OAuth2 授权。",
		OAuthURL: "https://learn.microsoft.com/zh-cn/exchange/client-developer/legacy-protocols/how-to-authenticate-an-imap-pop-smtp-application-by-using-oauth",
		HelpURL:  "https://support.microsoft.com/zh-cn/office/pop-imap-%E5%92%8C-smtp-%E8%AE%BE%E7%BD%AE-d088b986-291d-42b8-9564-9c414e2aa040",
		Guide: []string{
			"微软已在所有租户里停用 IMAP/SMTP 的基本认证，密码登录这条路没有了",
			"个人账号的「应用密码」也已随基本认证一起失效，不能当退路",
			"下一步要填的是你自己注册的应用的 client id（下一步有说明）",
			"注册时应用类型选「移动和桌面应用程序」，回调地址填 http://localhost",
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
		ID: "yahoo", Name: "Yahoo 邮箱",
		IMAPHost: "imap.mail.yahoo.com", IMAPPort: 993,
		SMTPHost: "smtp.mail.yahoo.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "第三方应用密码",
		// Yahoo 的 IMAP/SMTP **确实**广告 AUTH=XOAUTH2（实测），但它的 OAuth
		// 对邮件协议只开放给商业合作方，个人开发者拿不到 —— 所以这里只能是
		// AuthSecret，不能写成能走 OAuth2。
		SMTPNote: "发信 465（隐式 TLS）。中国大陆访问 Yahoo 的服务会直接跳转到停止服务页，这个账号在国内基本用不了。",
		OpenURL:  "https://login.yahoo.com/account/security",
		HelpURL:  "https://my.help.yahoo.com/kb/mail/generate-third-party-passwords-sln15241.html",
		Guide: []string{
			"打开 Yahoo 账户安全页，找到「生成第三方应用密码」",
			"生成一个 16 位应用密码并复制",
			"密码栏填它 —— 不是你的 Yahoo 账号密码",
		},
	},
	{
		ID: "zoho", Name: "Zoho Mail",
		IMAPHost: "imap.zoho.com", IMAPPort: 993,
		SMTPHost: "smtp.zoho.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "应用专用密码",
		// Zoho 的 IMAP **广告 XOAUTH2**（实测），官方也有一篇 SASL XOAuth2
		// 说明 —— 但它换 token 时**要求 client_secret**，而本程序的设计是
		// 「不携带任何凭据、靠公开客户端 + PKCE」（见 internal/oauth 的包注释）。
		// 塞一个 secret 进客户端等于公开它。所以这里只能是 AuthSecret。
		//
		// 区域说明：上面两个主机名是 .com 数据中心的。注册在 zoho.eu / .in /
		// .jp / .com.au 的账号主机名后缀不同，得用「自定义」手填。
		SMTPNote: "发信 465（隐式 TLS）。主机名按数据中心分：这里填的是 .com 那一套，" +
			"账号注册在 zoho.eu / zoho.in / zoho.jp / zoho.com.au 的话要用对应的主机名。",
		OpenURL: "https://accounts.zoho.com/home#security/app_password",
		HelpURL: "https://www.zoho.com/mail/help/imap-access.html",
		Guide: []string{
			"登录 Zoho 账户，进入 安全 → App Password（应用专用密码）",
			"生成一个应用专用密码并复制",
			"密码栏填它 —— 账号密码在 IMAP 上是填不通的",
		},
	},
	{
		ID: "fastmail", Name: "Fastmail",
		IMAPHost: "imap.fastmail.com", IMAPPort: 993,
		SMTPHost: "smtp.fastmail.com", SMTPPort: 465,
		Auth:          AuthSecret,
		PasswordLabel: "App 专用密码",
		// Fastmail 的 IMAP 广告 XOAUTH2（实测），但那是给自家 API/JMAP 的：
		// 官方帮助里对第三方邮件客户端只说 App Password。所以是 AuthSecret。
		//
		// 还有一条套餐限制值得直说：Basic 套餐根本不含 IMAP/SMTP。
		SMTPNote: "发信 465（隐式 TLS）。Basic 套餐不含 IMAP/SMTP 权限，用这个套餐的话第三方客户端连不上。",
		OpenURL:  "https://app.fastmail.com/settings/security",
		HelpURL:  "https://www.fastmail.help/hc/en-us/articles/1500000278342-Server-names-and-ports",
		Guide: []string{
			"登录 Fastmail 网页版，进入 Settings → Privacy & Security",
			"在「Connected apps & API tokens」里新建一个 App password",
			"权限选「Mail, Contacts & Calendars」，复制生成的 16 位密码填进来",
		},
	},
	{
		ID: CustomProviderID, Name: "自定义（手填服务器）",
		Auth:          AuthSecret,
		PasswordLabel: "密码或授权码",
		Guide: []string{
			"手填 IMAP / SMTP 服务器地址与端口",
			"大多数服务商不接受登录密码，需要用授权码或应用专用密码",
			"加密方式：993 / 465 选隐式 TLS，143 / 587 选 STARTTLS",
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
