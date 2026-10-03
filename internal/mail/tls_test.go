package mail

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// 这条判据守的是一件**看不见**的事：客户端的套件表里有没有留 RSA 密钥交换。
//
// 留了，139 和新浪的 IMAP 能连；没留，它们握手就失败，而且报出来的是
// `tls: handshake failure` —— 一句完全指不到原因的话。所以不能靠"跑一遍
// 试试看"来发现它没了，得有判据在这儿盯着。
//
// 实测依据（2026-10-03）：imap.139.com:993 / imap.sina.com:993 /
// imap.sina.cn:993 只认 RSA 密钥交换，ECDHE 一律拒绝。
func TestTLSConfig_KeepsRSASuites(t *testing.T) {
	cfg := tlsConfig("imap.139.com")

	if cfg.ServerName != "imap.139.com" {
		t.Errorf("ServerName = %q, want imap.139.com", cfg.ServerName)
	}
	if cfg.MinVersion != minTLSVersion {
		t.Errorf("MinVersion = %#04x, want %#04x", cfg.MinVersion, minTLSVersion)
	}
	if cfg.CipherSuites == nil {
		t.Fatal("CipherSuites 是 nil（= 用 Go 默认表）—— 139 和新浪的 IMAP 会连不上")
	}

	// 必须有的：RSA 密钥交换的那两个。
	for _, want := range []uint16{
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	} {
		if !slicesContains(cfg.CipherSuites, want) {
			t.Errorf("套件表里没有 %#04x —— 139 / 新浪的 IMAP 会握手失败", want)
		}
	}

	// 不许有的：CBC 版本的 RSA 套件。RSA 密钥交换没有前向保密是不得不接受的
	// （对面只给这个），但 CBC 还额外带填充预言问题，没有理由一起接过来。
	for _, bad := range []uint16{
		tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	} {
		if slicesContains(cfg.CipherSuites, bad) {
			t.Errorf("套件表里混进了 CBC 的 RSA 套件 %#04x —— 没必要承担填充预言的风险", bad)
		}
	}

	// 不许挤掉安全的那批：补 RSA 不能以砍掉 ECDHE 为代价。
	for _, s := range tls.CipherSuites() {
		if !slicesContains(cfg.CipherSuites, s.ID) {
			t.Errorf("安全套件 %#04x (%s) 被挤掉了", s.ID, s.Name)
		}
	}

	// 顺序即偏好：RSA 那两个必须在最后。放前面的话，一个只支持 ECDHE 的服务商
	// 也会看到我们把没有前向保密的套件排在首选 —— 顺序写错了不会报错，只会
	// 静悄悄地降低所有连接的安全性。
	first, last := cfg.CipherSuites[0], cfg.CipherSuites[len(cfg.CipherSuites)-1]
	if isRSASuite(first) {
		t.Errorf("RSA 密钥交换的套件排在了第一位（%#04x）—— 它只能当兜底", first)
	}
	if !isRSASuite(last) {
		t.Errorf("最后一位是 %#04x，want 一个 RSA 兜底套件", last)
	}
}

func isRSASuite(id uint16) bool {
	return id == tls.TLS_RSA_WITH_AES_128_GCM_SHA256 || id == tls.TLS_RSA_WITH_AES_256_GCM_SHA384
}

func slicesContains(hay []uint16, needle uint16) bool {
	for _, v := range hay {
		if v == needle {
			return true
		}
	}
	return false
}

// tlsConfig 是 IMAP 与 SMTP 共用的，套件表不该随协议变。
//
// 这条防的是"只给一侧打了补丁"：真到了只改一处的那天，两边的表现会不一样
// （收信能通、发信握手失败），而报错同样指不到原因。
func TestTLSConfig_SameForIMAPAndSMTP(t *testing.T) {
	a, b := tlsConfig("imap.example.com"), tlsConfig("smtp.example.com")
	if a.CipherSuites == nil || len(a.CipherSuites) == 0 {
		t.Fatal("套件表是空的")
	}
	if len(a.CipherSuites) != len(b.CipherSuites) {
		t.Fatal("套件表随主机名变了 —— 那说明它不该是以主机为变量的")
	}
	for i := range a.CipherSuites {
		if a.CipherSuites[i] != b.CipherSuites[i] {
			t.Fatalf("第 %d 个套件不同：%#04x vs %#04x", i, a.CipherSuites[i], b.CipherSuites[i])
		}
	}
}

// TestLive_PresetEndpoints 拿**预设里的每一条地址**去真连一次：只做 TCP +
// TLS 握手、读一眼 banner，**不登录**（所以不需要任何账号）。
//
//	CLICHAT_LIVE_TLS=1 go test ./internal/mail/ -run TestLive_PresetEndpoints -v
//
// 它把「服务商预设核对到最新」这件事从"照着文档抄一遍"变成一条能执行的判据：
// 服务商换了端口、撤了加密方式、域名下线，下一次跑就会红 —— 而用户看到的
// 否则只是「连不上服务器」。
//
// 默认 Skip：门禁不该依赖外网。
//
// 有些端点在某些网络里是**本来就不可达**的（比如 gmail 在中国大陆），那种
// 失败会把真回归盖住。所以给一个显式的跳过名单：
//
//	CLICHAT_LIVE_TLS_SKIP=gmail go test ...
func TestLive_PresetEndpoints(t *testing.T) {
	if os.Getenv("CLICHAT_LIVE_TLS") == "" {
		t.Skip("没有 CLICHAT_LIVE_TLS（这条判据要真连外网）")
	}
	skip := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("CLICHAT_LIVE_TLS_SKIP"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			skip[id] = true
		}
	}

	for _, p := range config.Providers {
		if p.ID == config.CustomProviderID {
			continue // 没有地址可连
		}
		if skip[p.ID] {
			t.Logf("[跳过] %-22s 在本次网络里本来就不可达", p.Name)
			continue
		}
		t.Run(p.ID, func(t *testing.T) {
			for _, ep := range []struct {
				kind   string
				host   string
				port   int
				useTLS string
			}{
				{"IMAP", p.IMAPHost, p.IMAPPort, config.TLSOrDefault(p.IMAPTLS)},
				{"SMTP", p.SMTPHost, p.SMTPPort, config.TLSOrDefault(p.SMTPTLS)},
			} {
				if ep.host == "" || ep.port == 0 {
					t.Errorf("%s 这一半预设里没有地址 —— 向导会把一条连不上的配置写进磁盘", ep.kind)
					continue
				}
				got, err := probeEndpoint(ep.host, ep.port, ep.useTLS)
				if err != nil {
					t.Errorf("%s %s:%d（%s）连不上：%v", ep.kind, ep.host, ep.port, ep.useTLS, err)
					continue
				}
				t.Logf("%s %s:%d（%s）→ %s",
					ep.kind, ep.host, ep.port, ep.useTLS, got)
			}
		})
	}
}

// probeEndpoint 按设定的加密方式连一次，返回协商到的版本与对端 banner。
func probeEndpoint(host string, port int, useTLS string) (string, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := &net.Dialer{Timeout: 15 * time.Second}

	raw, err := d.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(20 * time.Second))

	if useTLS != config.TLSStartTLS {
		// 走的是生产代码里那同一个 tlsConfig —— 判据要验的正是它。
		conn := tls.Client(raw, tlsConfig(host))
		if err := conn.Handshake(); err != nil {
			return "", err
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		return tlsVersionName(conn) + " " + trimLine(line), nil
	}

	// STARTTLS：先明文读 banner，再问一句有没有 STARTTLS，最后才升级。
	rd := bufio.NewReader(raw)
	greet, err := rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	// IMAP 的 banner 里就带 CAPABILITY；SMTP 要主动问。
	advertised := strings.Contains(strings.ToUpper(greet), "STARTTLS")
	if !advertised {
		if _, err := raw.Write([]byte("EHLO clichat.local\r\n")); err != nil {
			return "", err
		}
		for i := 0; i < 20; i++ {
			l, err := rd.ReadString('\n')
			if err != nil {
				break
			}
			advertised = advertised || strings.Contains(strings.ToUpper(l), "STARTTLS")
			if len(l) >= 4 && l[3] == ' ' {
				break
			}
		}
	}
	if !advertised {
		return "", errNoStartTLS
	}
	if _, err := raw.Write([]byte("STARTTLS\r\n")); err != nil {
		return "", err
	}
	if _, err := rd.ReadString('\n'); err != nil {
		return "", err
	}
	conn := tls.Client(raw, tlsConfig(host))
	if err := conn.Handshake(); err != nil {
		return "", err
	}
	defer conn.Close()
	return tlsVersionName(conn) + "（STARTTLS 升级）" + trimLine(greet), nil
}

var errNoStartTLS = errors.New("对端没有广告 STARTTLS —— 按 STARTTLS 配会卡在这儿")

func tlsVersionName(c *tls.Conn) string {
	switch c.ConnectionState().Version {
	case tls.VersionTLS13:
		return "TLS1.3"
	case tls.VersionTLS12:
		return "TLS1.2"
	default:
		return "TLS?"
	}
}

func trimLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) > 70 {
		s = s[:70] + "…"
	}
	return s
}
