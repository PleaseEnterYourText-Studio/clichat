package mail

import (
	"html"
	"regexp"
	"strings"
)

var (
	msgIDRe = regexp.MustCompile(`<[^<>]+>`)
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	// RE2 不支持反向引用（\1），所以 script 和 style 只能分开写两条。
	scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	styleRe  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	breakRe  = regexp.MustCompile(`(?i)<br\s*/?>|</p\s*>|</div\s*>|</tr\s*>|</h[1-6]\s*>`)
	spacesRe = regexp.MustCompile(`[ \t]{2,}`)
	blankRe  = regexp.MustCompile(`\n{3,}`)
)

// unfoldHeader 展开 RFC 5322 的折行（CRLF + 后续空白）。
func unfoldHeader(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// ParseReferences 从 References 头部值里抽出所有 Message-ID。
//
// 标准格式是空格分隔的 <id> 列表，但现实里会碰到折行、
// 多余的逗号、甚至裸地址，所以这里直接抓 <...> 形状的片段。
func ParseReferences(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return msgIDRe.FindAllString(unfoldHeader(raw), -1)
}

// SanitizeHeaderValue 去掉头值里的 CR / LF，防止头部注入。
//
// 这不是洁癖。Subject 和地址都可能来自用户输入或远端邮件，
// 一个换行就能凭空塞进一个 Bcc 或改写 Content-Type。
func SanitizeHeaderValue(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// HTMLToText 把 HTML 粗略降级成纯文本。
//
// 它已经不是正文的主路径了 —— 主路径是 htmlmd.go 里的 HTMLToMarkdown
// （走 DOM 解析）。这个函数现在只留一个用途：HTMLToMarkdown 连解析都
// 失败时的兜底，至少把字交出来，而不是把一封邮件显示成空白。
func HTMLToText(s string) string {
	s = scriptRe.ReplaceAllString(s, "")
	s = styleRe.ReplaceAllString(s, "")
	s = breakRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = spacesRe.ReplaceAllString(s, " ")
	s = blankRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// quoteBoundary 是「从这里开始都是引用」的判定模式。
var quoteBoundary = []*regexp.Regexp{
	regexp.MustCompile(`^\s*>`), // 经典引用
	regexp.MustCompile(`(?i)^\s*on .+wrote:?\s*$`),
	regexp.MustCompile(`^\s*.{1,80}写道[:：]\s*$`),
	regexp.MustCompile(`(?i)^\s*-{2,}\s*(original message|forwarded message)`),
	regexp.MustCompile(`^\s*-{2,}\s*(原始邮件|转发邮件|原邮件)`),
	regexp.MustCompile(`^\s*_{5,}\s*$`), // Outlook 风格分隔线
	regexp.MustCompile(`^-- $`),         // 签名分隔符
}

// TrimQuoted 砍掉正文里的引用历史。
//
// 聊天视图里引用是冗余的 —— 上一条消息就在上面。不砍的话每条消息
// 都会拖着整段历史，读起来像在翻邮件而不是聊天。
//
// 已知代价：正文中间以 ">" 开头的行（比如引用一段代码）会被误判。
// 这是刻意的取舍：宁可偶尔砍多，也不要每条都拖着长尾巴。
func TrimQuoted(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		for _, re := range quoteBoundary {
			if re.MatchString(line) {
				return strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\n")
			}
		}
	}
	return strings.TrimRight(normalized, " \t\n")
}

// FirstLine 取正文的第一行并截断，用于给新会话起 Subject。
// max 按 rune 计，<= 0 表示不截断。
func FirstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// CleanBody 是正文进入本地库前的统一处理：
// 统一换行、砍掉引用、压掉多余空行。
//
// HTML 正文先转成 Markdown（见 htmlmd.go）。存 Markdown 而不是纯文本，
// 是因为纯文本把邮件里唯一有用的那几个东西全丢了：链接、按钮、表格、
// 标题。一封「点击这里确认订阅」的邮件降级成纯文本之后，用户连点哪儿
// 都不知道。
//
// 顺序也很讲究：TrimQuoted 跑在转换之后。HTML 里的 <blockquote> 会被
// 转成 Markdown 的 "> "，正好落进 TrimQuoted 的经典引用判据里，引用
// 历史照砍不误；而正文里本来就有的 ">"（HTML 实体 &gt;）会被转义成
// "\>"，不会被误当成引用起点。
func CleanBody(raw string, isHTML bool) string {
	if isHTML {
		raw = HTMLToMarkdown(raw)
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = TrimQuoted(raw)
	raw = blankRe.ReplaceAllString(raw, "\n\n")
	return strings.TrimSpace(raw)
}
