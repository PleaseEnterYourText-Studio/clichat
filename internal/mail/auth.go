package mail

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/oauth"
)

// TokenSource 在需要时给一个可用的 access token。
//
// 它内部该刷新就刷新 —— 调用方只管要一个「现在能用」的。
type TokenSource func(ctx context.Context) (string, error)

// Auth 是一次会话的认证方式。
//
// 两种方式互斥：填了 Token 就走 XOAUTH2，否则用 Password 走 LOGIN / PLAIN。
// **不要两个都填**：OAuth2 服务商不接受密码，而密码服务商不认 XOAUTH2，
// 猜错了报出来的都是「认证失败」，完全指不到真正的原因。
type Auth struct {
	// User 是邮箱地址。两种方式都要。
	User string

	// Password 是授权码或应用专用密码 —— **不是**邮箱登录密码。
	// 绝大多数服务商都不接受登录密码，见 config.Provider.PasswordLabel。
	Password string

	// Token 非 nil 表示这个账号走 OAuth2。
	//
	// ⚠️ 做成**回调**而不是一个字符串：token 会过期，而连接是**按需**建的
	// （空闲被掐断后重连、每次发信各建一条）。拿一次性字符串的话，一条活了
	// 很久的连接断掉之后，重连会拿一个已经过期的 token 去认证，报出来是
	// 「认证失败」—— 用户完全没法从这句话里想到是 token 过期。
	Token TokenSource
}

// UsesOAuth 报告这次认证走不走 XOAUTH2。
func (a Auth) UsesOAuth() bool { return a.Token != nil }

// accessToken 取一个当前可用的 token。没配 Token 时返回错误。
func (a Auth) accessToken(ctx context.Context) (string, error) {
	tok, err := a.Token(ctx)
	if err != nil {
		return "", err
	}
	return tok, nil
}

// AuthFor 按账号配置与凭据挑出这次要用的认证方式。
//
// # 为什么收在这里而不是各界面各写一份
//
// 它是**两个入口共用**的一条判断：交互界面（tui）和只读自检（-check）。
// 两处各写一份的话，OAuth 那条路迟早在其中一处漂掉 —— 而漂掉的表现是
// 「界面能收信、自检说连不上」这类互相矛盾的结论。
//
// # save
//
// token 刷新之后要重新加密写回 credentials.enc，而加密要主密码 —— 那是
// 调用方才知道的东西。所以这里只回调，不自己落盘。传 nil 表示不落盘
// （自检工具、测试）。
//
// creds 传指针：刷新出来的 token 要能被调用方看见。传值的话，
// OAuth map 是 nil 时 SetOAuthToken 会新建一张表挂在副本上，
// 调用方那份仍然是 nil —— 表现为「每次要 token 都重新授权一遍」。
func AuthFor(cfg *config.Config, creds *config.Credentials, save func() error) (Auth, error) {
	email := cfg.Account.Email
	p := cfg.ActiveProfile()

	if p != nil && p.OAuth != nil && p.OAuth.ClientID != "" {
		prov, ok := oauth.ForProvider(p.Account.Provider, p.OAuth.Tenant)
		if !ok {
			return Auth{}, fmt.Errorf("这个版本还不认识 %q 的 OAuth 流程", p.Account.Provider)
		}
		return Auth{User: email, Token: tokenSource(creds, email, prov, p.OAuth.ClientID, save)}, nil
	}

	pw, ok := creds.PasswordFor(email)
	if !ok {
		// v1 升级上来的第一次：老凭据里只有一个 Password，而它属于当时
		// 唯一的那个账号。认领它并落盘，之后就不必再认领。
		if creds.AdoptLegacyPassword(email) {
			if save != nil {
				_ = save()
			}
			pw, ok = creds.PasswordFor(email)
		}
	}
	if !ok {
		return Auth{}, fmt.Errorf("本地没有 %s 的凭据，请重新配置这个账号", email)
	}
	return Auth{User: email, Password: pw}, nil
}

// tokenSource 返回「要一个可用 token」的回调。
//
// 给回调而不是给 token 本身，是因为**连接是按需建的**（空闲被掐断后重连、
// 每次发信各一条），而 token 一小时就过期。给一次性字符串的话，一条活了
// 很久的连接断掉之后重连会拿过期的 token 去认证，报出来是「认证失败」——
// 用户没法从这句话里想到是 token 过期。
func tokenSource(creds *config.Credentials, email string, prov oauth.Provider,
	clientID string, save func() error) TokenSource {
	return func(ctx context.Context) (string, error) {
		tok, _ := creds.OAuthToken(email)
		if tok.Valid() {
			return tok.AccessToken, nil
		}
		if tok.RefreshToken == "" {
			return "", errors.New("OAuth 授权已失效，需要重新授权这个账号")
		}

		fresh, err := oauth.Refresh(ctx, prov, clientID, tok.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("刷新 OAuth 授权失败（可能要重新授权）: %w", err)
		}
		creds.SetOAuthToken(email, fresh)

		// 落盘失败**不**让这次操作失败：token 已经在内存里了，这一次连接
		// 能成。落盘失败只意味着下次启动要重新授权一次。
		if save != nil {
			if err := save(); err != nil {
				log.Printf("OAuth token 刷新成功但落盘失败（下次启动可能要重新授权）: %v", err)
			}
		}
		return fresh.AccessToken, nil
	}
}
