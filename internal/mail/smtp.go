package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/smtp"
	"regexp"
	"strings"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/oauth"

	"github.com/emersion/go-message/charset"
)

// wordDecoder 负责解码 RFC 2047 编码的头部（形如 =?utf-8?B?...?=）。
//
// 挂上 go-message 的字符集表之后，GBK / GB2312 这些老客户端发出来的
// 中文主题也能正确解出来，而不是显示成一串乱码。
var wordDecoder = &mime.WordDecoder{CharsetReader: charset.Reader}

// decodeMIMEHeader 解码 RFC 2047 头部。解不开就原样返回 —— 宁可显示
// 原始字符串，也不要因为解码失败把内容丢掉。
func decodeMIMEHeader(s string) string {
	if s == "" {
		return ""
	}
	if decoded, err := wordDecoder.DecodeHeader(s); err == nil {
		return decoded
	}
	return s
}

// addrRe 是收件人地址的最低限度校验。
// 不追求符合 RFC 5322，只要挡住明显的手滑和头部注入。
var addrRe = regexp.MustCompile(`^[^@\s,;<>]+@[^@\s,;<>]+\.[^@\s,;<>]+$`)

// LooksLikeAddress 判断字符串看起来是不是一个邮箱地址。
//
// 导出是为了让界面层用同一份判断，而不是各写一个正则 ——
// 两处校验规则不一致迟早会漏掉一个。
func LooksLikeAddress(s string) bool {
	return addrRe.MatchString(strings.TrimSpace(s))
}

// Send 发送一封邮件，返回它的 Message-ID。
//
// SMTP 每次发送新建一条连接。发信是低频操作，为它维护常连不值当，
// 而且短连接天然避开了「连接被服务商悄悄掐断」这类问题。
func (c *liveClient) Send(msg Outgoing) (string, error) {
	recipients := make([]string, 0, len(msg.To)+len(msg.Cc))
	recipients = append(recipients, msg.To...)
	recipients = append(recipients, msg.Cc...)

	if len(recipients) == 0 {
		return "", errors.New("收件人为空")
	}
	for _, addr := range recipients {
		if !LooksLikeAddress(addr) {
			return "", fmt.Errorf("收件人地址不合法: %q", addr)
		}
	}

	raw, msgID, err := BuildMessage(c.cfg.Account.DisplayName, c.auth.User, msg)
	if err != nil {
		return "", err
	}

	ep := c.cfg.SMTP
	conn, err := dialSMTP(ep)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// 和收信那边同一套判断：OAuth2 服务商不接受密码。
	if c.auth.UsesOAuth() {
		tok, err := c.auth.accessToken(context.Background())
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrAuth, err)
		}
		if err := conn.Auth(oauth.SMTPAuth(c.auth.User, tok)); err != nil {
			return "", fmt.Errorf("%w: %v", ErrAuth, err)
		}
	} else if err := conn.Auth(smtp.PlainAuth("", c.auth.User, c.auth.Password, ep.Host)); err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuth, err)
	}
	if err := conn.Mail(c.auth.User); err != nil {
		return "", fmt.Errorf("SMTP MAIL FROM 被拒: %w", err)
	}
	for _, rcpt := range recipients {
		if err := conn.Rcpt(rcpt); err != nil {
			return "", fmt.Errorf("SMTP RCPT TO %s 被拒: %w", rcpt, err)
		}
	}

	w, err := conn.Data()
	if err != nil {
		return "", fmt.Errorf("SMTP DATA 被拒: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return "", fmt.Errorf("写入邮件内容失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("提交邮件内容失败: %w", err)
	}
	if err := conn.Quit(); err != nil {
		return "", fmt.Errorf("SMTP QUIT 失败: %w", err)
	}
	return msgID, nil
}

// dialSMTP 按端点配置建立 SMTP 连接，支持隐式 TLS 与 STARTTLS 两种模式。
func dialSMTP(ep config.Endpoint) (*smtp.Client, error) {
	if ep.TLS == config.TLSStartTLS {
		conn, err := smtp.Dial(ep.Addr())
		if err != nil {
			return nil, fmt.Errorf("连接 SMTP 服务器 %s 失败: %w", ep.Addr(), err)
		}
		if err := conn.StartTLS(tlsConfig(ep.Host)); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("STARTTLS 失败: %w", err)
		}
		return conn, nil
	}

	tlsConn, err := tls.Dial("tcp", ep.Addr(), tlsConfig(ep.Host))
	if err != nil {
		return nil, fmt.Errorf("连接 SMTP 服务器 %s 失败: %w", ep.Addr(), err)
	}
	conn, err := smtp.NewClient(tlsConn, ep.Host)
	if err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("初始化 SMTP 会话失败: %w", err)
	}
	return conn, nil
}

// BuildMessage 把一封待发邮件序列化成 RFC 5322 字节流。
//
// 第二个返回值是生成的 Message-ID。调用方需要它做两件事：
// 和 Sent 文件夹里的副本对上（去重），以及作为后续回复的 In-Reply-To。
func BuildMessage(displayName, from string, msg Outgoing) ([]byte, string, error) {
	msgID := GenerateMessageID(from)

	var buf bytes.Buffer
	write := func(name, value string) {
		value = SanitizeHeaderValue(value)
		if value == "" {
			return
		}
		buf.WriteString(name)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}

	write("From", formatAddress(displayName, from))
	write("To", strings.Join(msg.To, ", "))
	write("Cc", strings.Join(msg.Cc, ", "))
	write("Subject", encodeHeaderWord(msg.Subject))
	write("Date", time.Now().Format(time.RFC1123Z))
	write("Message-ID", msgID)
	write("In-Reply-To", msg.InReplyTo)
	if len(msg.References) > 0 {
		write("References", strings.Join(msg.References, " "))
	}
	write("MIME-Version", "1.0")
	write("Content-Type", `text/plain; charset="utf-8"`)
	write("Content-Transfer-Encoding", "quoted-printable")
	write("X-Mailer", "clichat")
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(normalizeNewlines(msg.Body))); err != nil {
		return nil, "", fmt.Errorf("编码正文失败: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, "", fmt.Errorf("编码正文失败: %w", err)
	}

	return buf.Bytes(), msgID, nil
}

// GenerateMessageID 生成一个全局唯一的 Message-ID。
//
// 域名部分借用发件人自己的域名 —— 这样它看起来像一封正常的邮件，
// 而不是一眼可疑的机器生成物。
func GenerateMessageID(from string) string {
	domain := "clichat.local"
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		domain = from[i+1:]
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), hex.EncodeToString(b[:]), domain)
}

// normalizeNewlines 把各种换行统一成 LF，交给 quoted-printable 编码器
// 去决定最终的换行形式。
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// encodeHeaderWord 按需把头部值编码成 RFC 2047 形式。
//
// 纯 ASCII 且不含特殊字符时原样返回 —— 能读得懂的就不编码。
func encodeHeaderWord(s string) string {
	s = SanitizeHeaderValue(s)
	if s == "" || isHeaderSafe(s) {
		return s
	}
	return mime.QEncoding.Encode("utf-8", s)
}

// isHeaderSafe 判断字符串能不能不经编码直接放进头部。
func isHeaderSafe(s string) bool {
	const safePunct = " ._-()[]'!?,:;/+@#&*"
	for _, r := range s {
		if r > 127 {
			return false
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if !strings.ContainsRune(safePunct, r) {
			return false
		}
	}
	return true
}

// isASCII 判断字符串是否全为 ASCII。
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// formatAddress 拼出 "显示名 <地址>" 形式的 From。
//
// 非 ASCII 显示名走 RFC 2047 编码；ASCII 显示名加引号并去掉内部引号，
// 避免拼出一个语法错误的头部。
func formatAddress(displayName, addr string) string {
	name := SanitizeHeaderValue(displayName)
	if name == "" {
		return addr
	}
	if !isASCII(name) {
		return mime.QEncoding.Encode("utf-8", name) + " <" + addr + ">"
	}
	return `"` + strings.ReplaceAll(name, `"`, "") + `" <` + addr + `>`
}
