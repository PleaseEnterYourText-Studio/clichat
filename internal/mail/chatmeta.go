package mail

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// chatIdentityOf 组装出「我」的身份卡，随信发出去。
//
// 这是**唯一**一处把配置和运行环境拼成 ClichatMeta 的地方 —— 发信走它，
// 界面要显示「对方会看到我什么」时也该从这儿取，而不是自己再拼一份。
// 两份拼法迟早有一份忘了跟着配置改（本仓库为这类「两处各写各的」付过
// 好几次账）。
//
// 身份信息按**当前活动账号**取（cfg.ChatFor）：每个账号可以有自己的
// 昵称和配色，没单独配过就落回全局默认。直接读 cfg.Chat 会静默忽略
// 掉账号那份，症状是「我明明在设置里改了，发出去还是老的」。
func chatIdentityOf(cfg *config.Config) thread.ClichatMeta {
	id := cfg.ChatFor(cfg.Account.Email)
	nick := id.Nick
	if nick == "" {
		// 没单独设昵称就退回账号的显示名：绝大多数人不会去改这一项，
		// 而「没有昵称」在对方那边只能显示成邮箱前缀，比显示名难认。
		nick = cfg.Account.DisplayName
	}
	m := thread.ClichatMeta{
		Version:   clientIDVersion,
		Nick:      nick,
		NameColor: id.NameColor,
		TextColor: id.TextColor,
	}
	if !id.HideDevice {
		m.Device = RuntimeDevice()
	}
	return m
}

// ChatMetaHeader 是 clichat 客户端之间互传身份信息的头名。
//
// # 为什么用自定义邮件头，而不是把标识塞进正文
//
// 同步列表只取 HEADER.FIELDS（见 headerSection），正文要**打开会话**
// 才拉得下来。标识放进正文的话，列表想认出「这个会话里有 clichat 消息」
// 就得把每一封的正文都拉一遍 —— 那正是这个仓库一直在省的东西（见
// roundtrip_test.go 里那几条数命令条数的判据）。放头里则是零额外往返：
// 那段头本来就要取，只是多要一个字段。
//
// 代价是每封邮件多带几百字节。它只在**真正拉到那些邮件时**才付：增量
// 同步只拉新邮件，首次同步默认还有 500 封的上限。
//
// # 为什么对方的邮件客户端看不见
//
// 自定义头不是正文，邮件客户端默认一律不显示（网页邮箱要在「显示原文」
// 里才看得到）。所以对方用 Gmail / Outlook 收到的，就是一封和平时一模
// 一样的纯文本邮件 —— 正文一个字节都没动过。
const ChatMetaHeader = "X-Clichat-Meta"

// 各字段的长度上限（rune 数）。
//
// 上限是必须的：这个头会跟着每一封同步下来的邮件走（见上面那段代价
// 说明），它的大小得有界。集中成一组常量是为了让编解码两侧用同一份
// 数字 —— 两边各写一个，改了一边就会悄悄不一致。
const (
	maxChatVersion = 40
	maxChatNick    = 32
	maxChatColor   = 9 // #rrggbbaa 就是最长的合法写法
	maxChatDevice  = 64

	// maxChatMetaEncoded 是**解 base64 之前**的长度上限。
	//
	// 这个头的内容来自别人的机器，所以挡在展开 JSON 之前：一个几百 KB
	// 的畸形头不该先被解码成一大块内存、再靠字段上限去截。
	maxChatMetaEncoded = 4 * 1024
)

// EncodeChatMeta 把身份信息编成 X-Clichat-Meta 头的值。
//
// 返回空串表示「这个头不该写」，调用方按没有 meta 处理 —— 发信不该
// 因为身份信息编不出来而失败。
func EncodeChatMeta(m thread.ClichatMeta) string {
	m = clampChatMeta(m)
	if m.Version == "" {
		// 版本号是「这是不是 clichat 消息」的判据（见 DecodeChatMeta），
		// 没有它写出去的头对面也不认。与其写一个对面不认的头，不如不写。
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		// 结构里全是字符串，走到这里说明前提变了（比如以后加了
		// 一个不能序列化的字段）。留着这个分支是为了让本函数不依赖
		// 「Marshal 一定成功」这个前提。
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeChatMeta 解开 X-Clichat-Meta 头的值。解不出来、或它不是
// clichat 写的，都返回 nil（= 这封邮件不是 clichat 消息）。
//
// 这个值**来自别人的机器**，所以每一层都不假设它合法：
//
//   - 长度先挡一道（在 base64 之前）；
//   - base64 / JSON 任一步失败就当成「没有这个头」；
//   - 解出来的字段还要过一遍 clampChatMeta —— 对面可能用的是比我们
//     新的客户端（多出来的字段忽略即可），也可能根本没做净化。
//
// ⚠️ 值里**允许出现空白**，而且必须允许：头值折行之后展开会留下折行处
// 那个空格（见 foldHeaderValue）。不容忍空白就会「发得出去、收不回来」，
// 而且只在昵称长到触发折行时才犯 —— 那种 bug 平时完全看不见。
func DecodeChatMeta(raw string) *thread.ClichatMeta {
	raw = strings.Map(dropChatSpace, raw)
	if raw == "" || len(raw) > maxChatMetaEncoded {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}
	var m thread.ClichatMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	m = clampChatMeta(m)
	if m.Version == "" {
		return nil
	}
	return &m
}

// dropChatSpace 丢掉 ASCII 空白，供 strings.Map 使用。
//
// 单靠 base64 解码器不行：它忽略 \r 和 \n，但**不忽略空格**，而折行
// 展开留下的恰好是空格。
func dropChatSpace(r rune) rune {
	switch r {
	case ' ', '\t', '\r', '\n':
		return -1
	}
	return r
}

// clampChatMeta 把身份信息限到「能安全使用、且大小有界」的范围内。
//
// 发信前和收信后**共用**这一份规则。两边各写一份的话，会出现「我发得
// 出去、你收不下来」这种只在真人互发时才暴露的问题，而且往往要等到
// 某个人的昵称刚好越界才犯。
func clampChatMeta(m thread.ClichatMeta) thread.ClichatMeta {
	return thread.ClichatMeta{
		Version:   cleanChatText(m.Version, maxChatVersion),
		Nick:      cleanChatText(m.Nick, maxChatNick),
		NameColor: cleanChatColor(m.NameColor),
		TextColor: cleanChatColor(m.TextColor),
		Device:    cleanChatText(m.Device, maxChatDevice),
	}
}

// cleanChatText 滤掉不可打印字符并截到 max 个 rune。
//
// ⚠️ 只留可打印字符，这不是洁癖：这个值是从**网络**收回来、又直接画到
// 终端上的。一个 \x1b（ESC）就能改写终端状态，U+202E 之类的双向控制符
// 能把整行文字显示成另一个样子 —— 这类「对方能左右我屏幕上显示什么」
// 的输入必须在下游之前挡掉。
//
// 代价：ZWJ 这类不可打印的连接符也会被滤掉，某些多码位的 emoji 会散成
// 几个。这是选定的取舍 —— 安全排在美观前面。
func cleanChatText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return truncateRunes(strings.TrimSpace(b.String()), max)
}

// ValidChatColor 报告 s 是不是合法的颜色写法（大小写与首尾空格不计）。
//
// 界面在**提交之前**问它，为的是当场告诉用户「这个值我不会用」；
// 而收信那侧不问、直接丢弃（见 cleanChatColor）—— 那里不能因为一个坏
// 颜色就把整封邮件的身份信息扔掉，宁可退回默认配色。
//
// 两处共用这一份判定，免得界面放行了一个 cleanChatColor 会丢掉的值：
// 那种情况下用户看到「设好了」，对方那边却是默认色，而谁都不会往
// 「两边校验规则不一致」上想。
func ValidChatColor(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 4 && len(s) != 7 && len(s) != 9 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// cleanChatColor 校验一个颜色值，不合法就返回空串。
//
// 只认 #rgb / #rrggbb / #rrggbbaa 三种写法。刻意**不**支持颜色名
// （"red" 之类）：颜色名表得跟着终端的调色板走，而这个值是从网络来的，
// 与其维护一张永远不完整的表，不如只认一种无歧义的写法，认不出来就当
// 对方没给 —— 界面上退回默认颜色，而不是把一串疑似控制序列画上去。
func cleanChatColor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !ValidChatColor(s) {
		return ""
	}
	return s
}

// truncateRunes 把字符串截到 max 个 rune（不是字节）。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

// RuntimeDevice 是默认上报的设备信息。
//
// 刻意只到「操作系统 / 架构」这一层：主机名、用户名、家目录这些能定位
// 到一个具体的人，而它们对这个字段的用途（让对方知道我在什么环境里）
// 毫无帮助 —— 邮件是要给不一定认识的人看的，别把可识别信息顺手送出去。
//
// 用户可以用配置里的 HideDevice 完全关掉它。
func RuntimeDevice() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// foldHeaderValue 按 RFC 5322 把过长的头值折成多行。
//
// RFC 5322 要求单行不超过 998 字符（**建议**不超过 78），而 base64 之后
// 的值轻易上百字符。
//
// ⚠️ 折行插入的是「CRLF + 一个空格」，而展开时那个空格会**留下来**
// （RFC 5322 §2.2.3 只去掉 CRLF）—— 所以取值那侧必须容忍值里出现空白
// （见 DecodeChatMeta）。折了却不容忍空白，就会「发得出去、收不回来」。
//
// 折在固定列上而不去找「好折的地方」：这个值是不透明的 base64，任何
// 位置都等权；而 base64url 的字母表里没有空白，怎么折都不会把一个字节
// 切开。
func foldHeaderValue(name, value string) string {
	const width = 76
	var b strings.Builder
	b.WriteString(name)
	b.WriteString(": ")
	for i := 0; i < len(value); i++ {
		if i > 0 && i%width == 0 {
			b.WriteString("\r\n ")
		}
		b.WriteByte(value[i])
	}
	b.WriteString("\r\n")
	return b.String()
}
