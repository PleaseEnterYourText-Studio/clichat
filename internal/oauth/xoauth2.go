package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/emersion/go-sasl"
)

// XOAUTH2 是 Google 那套 SASL 机制。
//
// ⚠️ 它**不在任何 RFC 里**（RFC 7628 定义的 OAuthBearer 是另一套，两家都不
// 认）。但 Gmail 和 Microsoft 都认 XOAUTH2，而且是唯一两家都认的 —— 所以
// 只能实现它。go-sasl 里没有，这也是本包存在的原因之一。
//
// 初始响应是一个 \x01 分隔的键值串：
//
//	user=<邮箱>\x01auth=Bearer <token>\x01\x01
//
// 认证失败时服务端会回一段 base64 的 JSON（带 scope 和错误原因）。**必须
// 把它解出来**：不解的话用户只看到「认证失败」，而真正的原因（通常是
// 「这个应用没被授权访问 IMAP」或者「scope 不够」）就丢掉了 —— 那正是
// 唯一能让人把问题修好的信息。

// xoauth2 同时满足 go-sasl 的 Client（IMAP 用）和 net/smtp 的 Auth（SMTP 用）。
//
// 两边的接口形状几乎一样（Start 返回机制名与初始响应、Next 处理服务端的
// 挑战），但**方法签名不同**，所以不能直接复用一个实现，只能各写一层薄壳。
type xoauth2 struct {
	user  string
	token string
	// answered 记「挑战已经回过了」。服务端再问一轮说明它没按约定来，
	// 继续陪着聊下去只会让错误更难懂。
	answered bool
}

// XOAuth2 返回一个 go-sasl 的 SASL 客户端，供 IMAP 的 Authenticate 使用。
func XOAuth2(user, accessToken string) sasl.Client {
	return &xoauth2{user: user, token: accessToken}
}

// Start 给出机制名与初始响应。
func (a *xoauth2) Start() (string, []byte, error) {
	if a.token == "" {
		return "", nil, errors.New("XOAUTH2: 没有 access token")
	}
	return "XOAUTH2", initialResponse(a.user, a.token), nil
}

// Next 处理服务端的挑战。
//
// 收到挑战就说明**认证已经失败了**（成功的话服务端直接回 OK，不会问）。
// 所以这里把服务端给的原因解出来当错误返回 —— 比让它去报一个笼统的
// 「认证失败」有用得多。
func (a *xoauth2) Next(challenge []byte) ([]byte, error) {
	if a.answered {
		return nil, errors.New("XOAUTH2: 服务端多问了一轮，认证无法继续")
	}
	a.answered = true
	return nil, fmt.Errorf("XOAUTH2 认证被拒: %s", describeChallenge(challenge))
}

// smtpAuth 是同一个机制在 net/smtp 上的实现。
type smtpAuth struct {
	xoauth2
}

// SMTPAuth 返回一个 net/smtp 的 Auth，供发信使用。
func SMTPAuth(user, accessToken string) smtp.Auth {
	return &smtpAuth{xoauth2{user: user, token: accessToken}}
}

// Start 给出机制名与初始响应。
//
// ⚠️ 明文连接上直接拒绝：XOAUTH2 会把 bearer token **原样**放进响应里，
// 明文连接上发它等于把 token 交给路上的任何人。net/smtp 自己也对 PLAIN
// 做了同样的检查，理由完全一样。
func (a *smtpAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("XOAUTH2 只能走 TLS 连接（否则 token 会明文上路）")
	}
	return a.xoauth2.Start()
}

// Next 处理服务端的挑战。
//
// 这里和 IMAP 那边有个差别：SMTP 的约定是「失败时客户端回一个空响应，
// 服务端才会把最终的错误状态码发出来」。所以这里**回空响应**而不是直接
// 报错 —— 报错的话 net/smtp 会把连接丢在半路，服务端那句真正的原因就
// 读不到了。
func (a *smtpAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if a.answered {
		return nil, fmt.Errorf("XOAUTH2 认证被拒: %s", describeChallenge(fromServer))
	}
	a.answered = true
	return []byte{}, nil
}

// initialResponse 拼 XOAUTH2 的初始响应。
func initialResponse(user, token string) []byte {
	return []byte("user=" + user + "\x01auth=Bearer " + token + "\x01\x01")
}

// describeChallenge 把服务端给的 base64 挑战解成人能读的一句话。
//
// 两家的格式是一样的（Google 定的）：一段 JSON，里面是
// `{"status":"401","schemes":"bearer","scope":"..."}`。解不开就退回原文 ——
// 至少比「认证失败」四个字多给一点线索。
func describeChallenge(challenge []byte) string {
	raw := strings.TrimSpace(string(challenge))
	if raw == "" {
		return "（服务端没给原因）"
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// 有的服务端直接回 JSON，不套 base64。
		decoded = []byte(raw)
	}
	var e struct {
		Status  string `json:"status"`
		Schemes string `json:"schemes"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(decoded, &e); err != nil {
		s := strings.TrimSpace(string(decoded))
		if s == "" {
			return "（服务端没给原因）"
		}
		return s
	}
	var parts []string
	if e.Status != "" {
		parts = append(parts, "status="+e.Status)
	}
	if e.Scope != "" {
		parts = append(parts, "scope="+e.Scope)
	}
	if len(parts) == 0 {
		return strings.TrimSpace(string(decoded))
	}
	return strings.Join(parts, " ")
}
