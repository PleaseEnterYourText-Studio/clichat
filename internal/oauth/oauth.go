// Package oauth 实现 OAuth2 的**授权码 + PKCE** 流程，以及把它接到
// IMAP / SMTP 上要用的 XOAUTH2 SASL 机制。
//
// # 为什么不带 client secret
//
// Google 和 Microsoft 都不允许「装在所有用户机器上的客户端」内置 client
// secret —— 内置等于公开，谁都能拿去冒充这个应用。所以本包**不携带任何
// 凭据**：client id 由用户在自己的服务商控制台注册应用之后填进来。
//
// 好在两家都支持「公开客户端 + PKCE」：授权码换 token 时用 code_verifier
// 证明「换 token 的人就是发起授权的人」，不需要 secret。
//
// # 回调为什么是 localhost
//
// 两家都支持回环地址回调，而且**忽略端口**（Google 对桌面应用、Microsoft
// 对 public client 都是这个规则）。所以这里固定监听 127.0.0.1 的随机端口，
// 用户在控制台里注册的回调地址只需要填 `http://localhost`。
//
// 好处是这整条链路**不需要公网可达的回调服务** —— 一个本地终端程序没法
// 对外提供 HTTPS 回调，而 device code 流程又要求用户去另一个设备上输码，
// 在同一个终端里体验更差。
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// Provider 是一家的 OAuth2 端点与权限。
type Provider struct {
	Name     string
	AuthURL  string
	TokenURL string
	// Scopes 是请求的权限。申请得越宽，用户看到的授权页越吓人 ——
	// 只申请收信和发信真正需要的。
	Scopes []string
	// ExtraAuth 是授权请求里额外要带的参数。
	//
	// Google 靠 `access_type=offline` 才会**给** refresh token，而且
	// 只在用户「没授过权」或显式 `prompt=consent` 时才给 —— 少了它，
	// 一小时之后就得重新授权一次，用户会以为是这个客户端坏了。
	ExtraAuth map[string]string
}

// Google 返回 Gmail 的 OAuth2 端点。
//
// Scope 用 `https://mail.google.com/` 而不是更细的那几个：Google 明确
// 说明 IMAP/SMTP 要用这一个（细分的 gmail.readonly 之类**不给** IMAP
// 用）。它在授权页上会显示成「读取、撰写、发送和永久删除您的所有电子邮件」
// —— 那句话不好看，但它是唯一能用的那个。
func Google() Provider {
	return Provider{
		Name:     "Google / Gmail",
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
		Scopes:   []string{"https://mail.google.com/"},
		ExtraAuth: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
	}
}

// Microsoft 返回 Outlook / Microsoft 365 的 OAuth2 端点。
//
// tenant 传空按 common 处理（个人账号与工作账号都能登）。要限定成只允许
// 某个组织时传 organizations 或具体租户 id。
func Microsoft(tenant string) Provider {
	if tenant == "" {
		tenant = "common"
	}
	base := "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0"
	return Provider{
		Name:     "Microsoft / Outlook",
		AuthURL:  base + "/authorize",
		TokenURL: base + "/token",
		// offline_access 是拿 refresh token 用的（微软把它也当成一个 scope）。
		// 两个资源前缀是微软指定的：IMAP.AccessAsUser.All 管收信，
		// SMTP.Send 管发信。少一个就会「能收不能发」。
		Scopes: []string{
			"offline_access",
			"https://outlook.office.com/IMAP.AccessAsUser.All",
			"https://outlook.office.com/SMTP.Send",
		},
	}
}

// Flow 是一次进行中的授权。
//
// 拆成「建流 → 拿 URL 给用户 → 等回调」三步而不是一个阻塞的 Authorize：
// 界面要在等之前把 URL **显示出来**（终端里浏览器不一定打得开，尤其是
// SSH 过去的时候），还要能中途取消。
type Flow struct {
	provider  Provider
	clientID  string
	verifier  string
	state     string
	redirect  string
	listener  net.Listener
	server    *http.Server
	codeCh    chan string
	errCh     chan error
	closeOnce bool
}

// NewFlow 起一个本地回调监听并准备好授权 URL。
func NewFlow(p Provider, clientID string) (*Flow, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("缺少 client id：OAuth2 服务商要求用你自己注册的应用")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("起本地回调监听失败: %w", err)
	}
	verifier, err := randomURLSafe(32)
	if err != nil {
		ln.Close()
		return nil, err
	}
	state, err := randomURLSafe(16)
	if err != nil {
		ln.Close()
		return nil, err
	}

	f := &Flow{
		provider: p,
		clientID: clientID,
		verifier: verifier,
		state:    state,
		// 回调地址里用 localhost 而不是 127.0.0.1：两家的控制台里
		// 让用户填的都是 `http://localhost`，而它们都会忽略端口。
		redirect: fmt.Sprintf("http://localhost:%d/callback", ln.Addr().(*net.TCPAddr).Port),
		listener: ln,
		codeCh:   make(chan string, 1),
		errCh:    make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", f.handleCallback)
	f.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = f.server.Serve(ln) }()

	return f, nil
}

// RedirectURI 是这次授权用的回调地址（要让用户填进服务商控制台）。
func (f *Flow) RedirectURI() string { return f.redirect }

// AuthURL 是要用户在浏览器里打开的那个地址。
func (f *Flow) AuthURL() string {
	q := url.Values{}
	q.Set("client_id", f.clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", f.redirect)
	q.Set("scope", strings.Join(f.provider.Scopes, " "))
	q.Set("state", f.state)
	q.Set("code_challenge", pkceChallenge(f.verifier))
	q.Set("code_challenge_method", "S256")
	for k, v := range f.provider.ExtraAuth {
		q.Set(k, v)
	}
	return f.provider.AuthURL + "?" + q.Encode()
}

// handleCallback 接浏览器那一下跳转。
//
// 它只把 code 交给 Wait，**不在这里换 token**：换 token 是一次网络请求，
// 放在 HTTP 处理函数里会让浏览器一直转圈；而且失败了也没法把错误说清楚。
func (f *Flow) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = e
		}
		f.fail(fmt.Errorf("授权被拒绝: %s", desc))
		http.Error(w, "授权失败："+desc+"\n可以关掉这个页面了。", http.StatusBadRequest)
		return
	}
	// state 必须对得上：它是防「别人拿一个别的授权码塞进你的回调」的。
	if q.Get("state") != f.state {
		f.fail(errors.New("回调里的 state 与发起时不一致，这次授权作废"))
		http.Error(w, "state 不一致，已作废。", http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" {
		f.fail(errors.New("回调里没有 code"))
		http.Error(w, "回调里没有 code。", http.StatusBadRequest)
		return
	}
	select {
	case f.codeCh <- code:
	default:
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "授权完成，可以关掉这个页面回到 clichat 了。")
}

func (f *Flow) fail(err error) {
	select {
	case f.errCh <- err:
	default:
	}
}

// Close 收掉监听。可重复调用。
func (f *Flow) Close() {
	if f.closeOnce {
		return
	}
	f.closeOnce = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = f.server.Shutdown(ctx)
}

// Wait 等浏览器回调，然后用 code 换 token。
//
// ctx 取消（用户按了 Esc）时它会立刻返回，并把监听收掉 —— 不取消的话
// 那个端口会一直开着等一个永远不会来的回调。
func (f *Flow) Wait(ctx context.Context) (config.Token, error) {
	defer f.Close()

	var code string
	select {
	case code = <-f.codeCh:
	case err := <-f.errCh:
		return config.Token{}, err
	case <-ctx.Done():
		return config.Token{}, ctx.Err()
	}

	return f.exchange(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {f.redirect},
		"client_id":     {f.clientID},
		"code_verifier": {f.verifier},
	})
}

// exchange 是换 token 的唯一出口：首次授权和刷新走的是同一个端点，
// 只有 grant_type 和几个参数不同。
func (f *Flow) exchange(ctx context.Context, form url.Values) (config.Token, error) {
	return postToken(ctx, f.provider.TokenURL, form)
}

// Refresh 用 refresh token 换一个新的 access token。
//
// ⚠️ 响应里**通常不带新的 refresh token**，这时必须保留旧的那个 ——
// 覆盖成空串的话，下一次刷新就没得用了，用户会在一小时之后突然被要求
// 重新授权，而那时他手上只有一份被自己抹掉的凭据。
func Refresh(ctx context.Context, p Provider, clientID, refreshToken string) (config.Token, error) {
	if refreshToken == "" {
		return config.Token{}, errors.New("没有 refresh token，需要重新授权")
	}
	tok, err := postToken(ctx, p.TokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	})
	if err != nil {
		return config.Token{}, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// postToken 向 token 端点发一次请求并解析响应。
func postToken(ctx context.Context, tokenURL string, form url.Values) (config.Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return config.Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// 明确要 JSON：微软的 v2 端点默认回表单编码，解析不了。
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return config.Token{}, fmt.Errorf("请求 token 端点失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return config.Token{}, fmt.Errorf("读取 token 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// OAuth 的错误响应体里才有真正的原因（invalid_grant、
		// invalid_client……），只报状态码等于什么都没说。
		return config.Token{}, fmt.Errorf("token 端点返回 %d: %s",
			resp.StatusCode, describeTokenError(body))
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		Desc         string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return config.Token{}, fmt.Errorf("解析 token 响应失败: %w", err)
	}
	if raw.Error != "" {
		return config.Token{}, fmt.Errorf("%s: %s", raw.Error, raw.Desc)
	}
	if raw.AccessToken == "" {
		return config.Token{}, errors.New("token 响应里没有 access_token")
	}

	tok := config.Token{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		Scope:        raw.Scope,
	}
	// expires_in 缺失时保守一点：按一小时算。设成零值的话 Valid() 会
	// 一直判失效，于是**每次操作都去刷新一次** —— 那比"早一点过期"糟得多。
	if raw.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)
	} else {
		tok.Expiry = time.Now().Add(time.Hour)
	}
	return tok, nil
}

// describeTokenError 从错误响应体里挖出人能读的那一句。
func describeTokenError(body []byte) string {
	var e struct {
		Error    string `json:"error"`
		Desc     string `json:"error_description"`
		ErrorURI string `json:"error_uri"`
	}
	if err := json.Unmarshal(body, &e); err == nil {
		switch {
		case e.Error != "" && e.Desc != "":
			return e.Error + ": " + e.Desc
		case e.Error != "":
			return e.Error
		case e.Desc != "":
			return e.Desc
		}
	}
	// 不是 JSON（网关返回的 HTML 之类）时截一段原文，总比只有一个数字强。
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "（响应体是空的）"
	}
	return s
}

// randomURLSafe 生成 n 字节的随机串，按 base64url 无填充编码。
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机串失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge 是 PKCE 的 S256 变换：base64url(sha256(verifier))。
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ForProvider 按服务商 id 返回 OAuth 端点。
//
// 按 id 而不是按邮箱域名：端点是**服务商**的性质，而同一个服务商下可能有
// 企业自建域名（Microsoft 365 的租户域名可以是任意域名）。
//
// 不认识的 id 返回 ok=false。调用方**必须**把它当成错误报出来 —— 配置里
// 写了要 OAuth 而代码不认识这个服务商时，默默用一个空的端点会让人看到
// 一个毫无线索的失败。
func ForProvider(id, tenant string) (Provider, bool) {
	switch id {
	case "gmail":
		return Google(), true
	case "outlook":
		return Microsoft(tenant), true
	}
	return Provider{}, false
}
