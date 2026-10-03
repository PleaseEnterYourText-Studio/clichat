package oauth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// PKCE 的 S256 变换必须**逐字节**对得上规范给的向量。
//
// 用 RFC 7636 附录 B 那组已知值，不是自己算一遍再抄进来 —— 后者只能证明
// 实现和它自己一致。这一条错了的表现是：授权页能打开、用户点了同意、
// 然后换 token 时报 invalid_grant，而错误信息里只会说「授权码无效」，
// 没人能想到是 challenge 算错了。
func TestPKCEChallenge_MatchesRFC7636Vector(t *testing.T) {
	// RFC 7636 附录 B。
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	if got := pkceChallenge(verifier); got != want {
		t.Errorf("pkceChallenge = %q，want %q（RFC 7636 附录 B）", got, want)
	}
}

// 授权 URL 要把该带的都带上。
func TestFlow_AuthURLCarriesEverything(t *testing.T) {
	f, err := NewFlow(Google(), "client-123.apps.googleusercontent.com")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	defer f.Close()

	u, err := url.Parse(f.AuthURL())
	if err != nil {
		t.Fatalf("AuthURL 不是合法 URL: %v", err)
	}
	if u.Host != "accounts.google.com" {
		t.Errorf("授权端点的 host = %q", u.Host)
	}
	q := u.Query()
	for _, k := range []string{"client_id", "redirect_uri", "scope", "state",
		"code_challenge", "code_challenge_method"} {
		if q.Get(k) == "" {
			t.Errorf("授权 URL 里缺 %s", k)
		}
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q，want code", got)
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q —— 不能是 plain", got)
	}
	if got := q.Get("redirect_uri"); !strings.HasPrefix(got, "http://localhost:") {
		t.Errorf("redirect_uri = %q，want http://localhost:<port>/callback", got)
	}
	// Google 的两个额外参数：少了 access_type 就拿不到 refresh token。
	if q.Get("access_type") != "offline" {
		t.Errorf("Google 的 access_type = %q，want offline（否则拿不到 refresh token）",
			q.Get("access_type"))
	}
	// scope 里必须含那个唯一能用的 IMAP scope。
	if !strings.Contains(q.Get("scope"), "https://mail.google.com/") {
		t.Errorf("scope = %q，缺 https://mail.google.com/", q.Get("scope"))
	}
	// challenge 必须等于 verifier 的 S256 —— 光有参数不算。
	if got, want := q.Get("code_challenge"), pkceChallenge(f.verifier); got != want {
		t.Errorf("code_challenge = %q，want %q", got, want)
	}
}

// Microsoft 的 tenant 要进端点，scope 里必须含发信那一个。
func TestMicrosoft_EndpointsAndScopes(t *testing.T) {
	p := Microsoft("")
	if !strings.Contains(p.AuthURL, "/common/oauth2/v2.0/authorize") {
		t.Errorf("空 tenant 该落到 common：%q", p.AuthURL)
	}
	p2 := Microsoft("contoso.onmicrosoft.com")
	if !strings.Contains(p2.AuthURL, "/contoso.onmicrosoft.com/") {
		t.Errorf("tenant 没进端点：%q", p2.AuthURL)
	}
	joined := strings.Join(p.Scopes, " ")
	for _, want := range []string{"offline_access", "IMAP.AccessAsUser.All", "SMTP.Send"} {
		if !strings.Contains(joined, want) {
			t.Errorf("scope 里缺 %s（少它就会「能收不能发」）：%v", want, p.Scopes)
		}
	}
}

// 没填 client id 时要在**起监听之前**就报错。
func TestNewFlow_RequiresClientID(t *testing.T) {
	if _, err := NewFlow(Google(), "  "); err == nil {
		t.Error("没有 client id 应该报错 —— OAuth2 服务商要求用你自己注册的应用")
	}
}

// 整条链路：起流 → 浏览器跳回调 → 换到 token。
//
// 用 httptest 假装 token 端点。真网络不可能出现在单测里，而这条链路里
// 唯一需要外部世界的就是那个端点。
func TestFlow_EndToEndExchangesTheCode(t *testing.T) {
	var gotForm url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"AT","refresh_token":"RT","expires_in":3600,"scope":"x"}`)
	}))
	defer tokenSrv.Close()

	p := Google()
	p.TokenURL = tokenSrv.URL

	f, err := NewFlow(p, "cid")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	defer f.Close()

	// 从授权 URL 里取 state —— 判据不直接读 f.state，那样等于绕过"state
	// 真的被带进 URL 了"这件事。
	state := mustQuery(t, f.AuthURL(), "state")

	type result struct {
		tok config.Token
		err error
	}
	done := make(chan result, 1)
	go func() {
		tok, err := f.Wait(context.Background())
		done <- result{tok, err}
	}()

	// 模拟浏览器跳回来。回调是 GET，所以这里必须真的发一次 HTTP 请求，
	// 而不是直接往 codeCh 里塞 —— 后者会把 HTTP 那一层整个跳过。
	resp, err := http.Get(f.RedirectURI() + "?code=THE_CODE&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("请求回调失败: %v", err)
	}
	resp.Body.Close()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Wait: %v", r.err)
		}
		if r.tok.AccessToken != "AT" || r.tok.RefreshToken != "RT" {
			t.Errorf("token = %+v", r.tok)
		}
		if r.tok.Expiry.Before(time.Now().Add(30 * time.Minute)) {
			t.Errorf("expires_in=3600 该算出一小时后的过期时间，实际 %v", r.tok.Expiry)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait 一直没返回")
	}

	// 换 token 的请求体必须带上 PKCE 的 verifier 和原样的 redirect_uri。
	if gotForm.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("code") != "THE_CODE" {
		t.Errorf("code = %q", gotForm.Get("code"))
	}
	if gotForm.Get("code_verifier") == "" {
		t.Error("没带 code_verifier —— 公开客户端就靠它证明身份")
	}
	if gotForm.Get("redirect_uri") != f.RedirectURI() {
		t.Errorf("redirect_uri = %q，want %q", gotForm.Get("redirect_uri"), f.RedirectURI())
	}
	if gotForm.Get("client_id") != "cid" {
		t.Errorf("client_id = %q", gotForm.Get("client_id"))
	}
}

// state 对不上必须拒绝：它是防「别人拿一个别的授权码塞进你的回调」的。
func TestFlow_RejectsMismatchedState(t *testing.T) {
	f, err := NewFlow(Google(), "cid")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	defer f.Close()

	done := make(chan error, 1)
	go func() {
		_, err := f.Wait(context.Background())
		done <- err
	}()

	resp, err := http.Get(f.RedirectURI() + "?code=X&state=NOT_THE_STATE")
	if err != nil {
		t.Fatalf("请求回调失败: %v", err)
	}
	resp.Body.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("state 不一致却通过了")
		}
		if !strings.Contains(err.Error(), "state") {
			t.Errorf("错误信息该提到 state：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait 一直没返回")
	}
}

// 用户在授权页点「拒绝」时，要把服务端给的原因带出来。
func TestFlow_SurfacesDenialReason(t *testing.T) {
	f, err := NewFlow(Google(), "cid")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	defer f.Close()

	done := make(chan error, 1)
	go func() {
		_, err := f.Wait(context.Background())
		done <- err
	}()
	resp, err := http.Get(f.RedirectURI() +
		"?error=access_denied&error_description=" + url.QueryEscape("用户拒绝了"))
	if err != nil {
		t.Fatalf("请求回调失败: %v", err)
	}
	resp.Body.Close()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "用户拒绝了") {
			t.Errorf("该把服务端给的原因带出来，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait 一直没返回")
	}
}

// ctx 取消（用户按 Esc）时 Wait 要立刻返回，不能一直等一个不会来的回调。
func TestFlow_WaitHonoursContext(t *testing.T) {
	f, err := NewFlow(Google(), "cid")
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.Wait(ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("ctx 取消了却没报错")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消之后 Wait 还在等")
	}
}

// 刷新时响应**不带新 refresh token** → 必须保留旧的。
//
// 覆盖成空串的话，下一次刷新就没得用了：用户会在一小时之后突然被要求
// 重新授权，而那时他手上只有一份被自己抹掉的凭据。
func TestRefresh_KeepsTheOldRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q", got)
		}
		// 刻意不回 refresh_token —— 两家在刷新响应里通常都不带。
		fmt.Fprint(w, `{"access_token":"AT2","expires_in":3600}`)
	}))
	defer srv.Close()

	p := Google()
	p.TokenURL = srv.URL

	tok, err := Refresh(context.Background(), p, "cid", "OLD_RT")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "AT2" {
		t.Errorf("access token = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "OLD_RT" {
		t.Errorf("refresh token = %q，want OLD_RT —— 响应里没有就该保留旧的", tok.RefreshToken)
	}
}

// 没有 refresh token 时别去发那个注定失败的请求。
func TestRefresh_WithoutTokenFailsFast(t *testing.T) {
	if _, err := Refresh(context.Background(), Google(), "cid", ""); err == nil {
		t.Error("没有 refresh token 应该直接报错")
	}
}

// token 端点的错误原因必须**挖出来**：只报一个状态码等于什么都没说，
// 而 invalid_grant / invalid_client 这几个字正是唯一能让人修好问题的线索。
func TestPostToken_SurfacesServerReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	}))
	defer srv.Close()

	_, err := postToken(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("400 却成功了")
	}
	for _, want := range []string{"400", "invalid_grant", "revoked"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息里缺 %q：%v", want, err)
		}
	}
}

// expires_in 缺失时按一小时算，**不能**落成零值。
//
// 零值会让 Valid() 永远判失效，于是每次操作都去刷新一次 —— 那比"早一点
// 过期"糟得多。
func TestPostToken_MissingExpiresInFallsBackToOneHour(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"AT"}`)
	}))
	defer srv.Close()

	tok, err := postToken(context.Background(), srv.URL, url.Values{})
	if err != nil {
		t.Fatalf("postToken: %v", err)
	}
	if !tok.Valid() {
		t.Errorf("没有 expires_in 时也该算有效，实际 Expiry=%v", tok.Expiry)
	}
}

// ---- XOAUTH2 ----

// 初始响应必须**逐字节**对：\x01 分隔、结尾两个 \x01。
//
// 多一个少一个字节服务端就认不出来，而报出来的错是「认证失败」——
// 完全指不到这里。
func TestXOAuth2_InitialResponseBytes(t *testing.T) {
	c := XOAuth2("me@example.com", "TOKEN")
	mech, ir, err := c.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mech != "XOAUTH2" {
		t.Errorf("机制名 = %q，want XOAUTH2（不是 OAuthBearer）", mech)
	}
	want := "user=me@example.com\x01auth=Bearer TOKEN\x01\x01"
	if string(ir) != want {
		t.Errorf("初始响应 = %q\n            want %q", string(ir), want)
	}
}

func TestXOAuth2_StartWithoutTokenFails(t *testing.T) {
	if _, _, err := XOAuth2("me@example.com", "").Start(); err == nil {
		t.Error("没有 token 时该直接报错")
	}
}

// 服务端给的那段 base64 JSON 要解成人能读的原因。
//
// 不解的话用户只看到「认证失败」，而真正的原因（scope 不够、应用没被授权）
// 正是唯一能让人把问题修好的信息。
func TestXOAuth2_ChallengeIsDecoded(t *testing.T) {
	payload := `{"status":"401","schemes":"bearer","scope":"https://mail.google.com/"}`
	challenge := []byte(base64.StdEncoding.EncodeToString([]byte(payload)))

	c := XOAuth2("me@example.com", "TOKEN")
	if _, _, err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err := c.Next(challenge)
	if err == nil {
		t.Fatal("收到挑战说明认证已经失败，该报错")
	}
	for _, want := range []string{"401", "mail.google.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息里缺 %q：%v", want, err)
		}
	}
}

// 挑战不是 base64 JSON 时退回原文，别把线索丢掉。
func TestDescribeChallenge_FallsBackToRaw(t *testing.T) {
	if got := describeChallenge([]byte("plain text reason")); !strings.Contains(got, "plain text") {
		t.Errorf("退回原文失败：%q", got)
	}
	if got := describeChallenge(nil); got == "" {
		t.Error("空挑战也该给一句话，不能是空串")
	}
}

// SMTP 上必须拒绝明文连接：XOAUTH2 会把 bearer token 原样放进响应里。
func TestSMTPAuth_RefusesPlaintext(t *testing.T) {
	a := SMTPAuth("me@example.com", "TOKEN")
	if _, _, err := a.Start(&smtp.ServerInfo{TLS: false}); err == nil {
		t.Error("明文连接该被拒绝 —— 否则 token 明文上路")
	}
	mech, ir, err := a.Start(&smtp.ServerInfo{TLS: true})
	if err != nil {
		t.Fatalf("TLS 连接该放行: %v", err)
	}
	if mech != "XOAUTH2" || string(ir) != "user=me@example.com\x01auth=Bearer TOKEN\x01\x01" {
		t.Errorf("机制=%q 响应=%q", mech, string(ir))
	}
}

// SMTP 的失败约定：回一个**空响应**让服务端把最终错误发出来。
func TestSMTPAuth_AnswersChallengeWithEmptyResponse(t *testing.T) {
	a := SMTPAuth("me@example.com", "TOKEN")
	if _, _, err := a.Start(&smtp.ServerInfo{TLS: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	resp, err := a.Next([]byte("whatever"), true)
	if err != nil {
		t.Fatalf("第一次挑战不该报错（要回空响应让服务端把错误发全）: %v", err)
	}
	if resp == nil || len(resp) != 0 {
		t.Errorf("该回一个空响应，实际 %q", resp)
	}
	if _, err := a.Next(nil, true); err == nil {
		t.Error("第二轮挑战说明服务端没按约定来，该报错")
	}
}

// mustQuery 从 URL 里取一个查询参数，取不到直接失败。
func mustQuery(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析 %q: %v", raw, err)
	}
	v := u.Query().Get(key)
	if v == "" {
		t.Fatalf("%q 里没有 %s", raw, key)
	}
	return v
}
