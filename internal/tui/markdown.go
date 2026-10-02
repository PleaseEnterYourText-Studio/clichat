package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// 这个文件把邮件正文里的 Markdown 渲染成终端样式。
//
// # 为什么不是通用 Markdown 渲染器
//
// 正文里的 Markdown 不是用户手写的，是本仓库自己生成的（见
// internal/mail/htmlmd.go）。所以语法集合是**封闭且已知**的：
//
//	**粗体**  *斜体*  ~~删除线~~  `行内代码`
//	[文字](链接)  <自动链接>
//	# 标题（1-6 级）  ---  分隔线
//	> 引用
//	- 列表项 / 1. 列表项（嵌套用两空格缩进）
//	``` 围栏代码块 ```
//
// 这一点是整套做法的地基：生成器会把正文里出现的 \ ` * _ [ ] < 一律
// 转义成 \<c>（见 escapeMarkdownRune），所以**没转义的 * 一定是我们自己
// 写的标记，转义的才是用户的字面文字**。解析因此可以是确定性的，不需要
// 通用解析器那种「* 后面不能跟空格才算强调」的模糊启发式 —— 那种启发式
// 在中文和邮件排版里经常猜错。
//
// # 为什么不用现成的库
//
// glamour 这类库是「一步产出带 ANSI 的字符串」，而本项目的管线要求
// **折行在纯文本阶段完成、上色必须在折行之后**：
//
//   - truncate 按 rune 逐个算宽度，不认识 ANSI 转义序列。喂给它带颜色的
//     字符串，`[38;5;45m` 会被当成各占 1 列的普通字符，折行位置算错，
//     甚至从转义序列中间折断、渲染出乱码。
//   - padLeft / padRight 用的是 lipgloss.Width，反而是认识 ANSI 的。
//
// 所以这里产出的是「文本 + 语义位」的中间表示：先按显示宽度折行，
// 折完再逐段上色。这是 help.go 里那条「先截断再上色」约束的同构版本。
//
// # ⚠️ 链接文字不能带任何属性
//
// styles.go 里 hyperlink 的注释记着那个坑：OSC 8 序列一旦被 SGR 插进去，
// 终端就认不出这是链接了 —— 表现是「看着完全正常，但点不动」。
// 所以链接文字只上 styleLink（纯前景色，无属性），**即使它同时是粗体
// 也不加粗** —— 可点比好看重要。这条由
// TestRenderMarkdown_LinkNeverCarriesAttributes 守着。
//
// （原先这里写「Go 层的单测抓不到它」。2026-10-02 实测纠正了：逐字符的 SGR
// 是 lipgloss 的 Style.Render 干的，纯 Go 层就能看见，判据抓得住。
// 详见 styles.go 里 hyperlink 的注释。）

// ---- 中间表示 ----

// attrs 是行内文本的样式位。用位而不是枚举，是因为它们会叠加：
// ***粗斜体*** 同时是粗体和斜体。
type attrs uint8

const (
	attrBold attrs = 1 << iota
	attrItalic
	attrStrike
	attrCode
	attrLink
	// attrQuote 是引用块里的文字，整体压暗，让它一眼看出是引来的话
	// 而不是对方在说的事。
	attrQuote
)

// span 是一段样式相同的文本。
//
// url 只在 attrLink 置位时有意义 —— 显示文字和目标地址是两回事：
// [查看订单](https://…) 要显示「查看订单」，点开去 https://…。
type span struct {
	text  string
	attrs attrs
	url   string
}

// blockKind 是一行的块级身份。
type blockKind uint8

const (
	blockPara blockKind = iota
	blockHeading
	blockQuote
	blockBullet
	blockRule
)

// mdLine 是解析出来的一行（还没折行）。
type mdLine struct {
	kind  blockKind
	level int // 标题级别（1-6）

	// prefix 是第一行的纯文本前缀（"• "、"1. "、"> "）。它是**纯文本**，
	// 所以折行时它的宽度会一起算进去，上色不会影响折行位置。
	prefix      string
	prefixAttrs attrs

	spans []span
}

// ---- 入口 ----

// renderMarkdown 把邮件正文渲染成终端里的若干行。
//
// width 是可用显示宽度。折行在这里就定下来，之后才上色，所以颜色不会
// 污染宽度计算。
func renderMarkdown(src string, width int) []string {
	if width < 1 {
		width = 1
	}

	var out []string
	for _, ln := range parseMarkdown(src) {
		if ln.kind == blockRule {
			// 分隔线画成一条实心横线，比原样的 "---" 更像分隔线。
			// 限长 40 列：横贯整个屏幕在聊天视图里太吵。
			out = append(out, styleMDRule.Render(strings.Repeat("─", min(width, 40))))
			continue
		}
		if ln.kind == blockPara && len(ln.spans) == 0 {
			out = append(out, "")
			continue
		}
		for _, row := range wrapLine(ln, width) {
			out = append(out, renderRow(row))
		}
	}
	return out
}

// ---- 解析 ----

// parseMarkdown 把正文按行解析成块。
func parseMarkdown(src string) []mdLine {
	var out []mdLine
	lines := strings.Split(src, "\n")

	fence := 0 // 围栏代码块的反引号个数，0 表示不在代码块里
	ol := 0    // 连续有序列表项的计数，遇到别的东西归零

	for _, raw := range lines {
		// 围栏代码块：里面的内容原样保留，不做任何行内解析 ——
		// 代码里的 * 和 [ 是代码，不是标记。
		if n, ok := fenceRun(raw); ok {
			if fence == 0 {
				fence = n
			} else if n >= fence {
				fence = 0
			}
			continue
		}
		if fence > 0 {
			out = append(out, mdLine{
				kind:  blockPara,
				spans: []span{{text: raw, attrs: attrCode}},
			})
			continue
		}

		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			out = append(out, mdLine{kind: blockPara})
			ol = 0
			continue
		}

		if isRule(trimmed) {
			out = append(out, mdLine{kind: blockRule})
			ol = 0
			continue
		}

		if level, rest, ok := heading(raw); ok {
			// 标题不显示 # 号，直接加粗 —— 这才是「渲染」而不是「露出源码」。
			out = append(out, mdLine{
				kind:  blockHeading,
				level: level,
				spans: parseInline(rest, attrBold),
			})
			ol = 0
			continue
		}

		if depth, rest, ok := quote(raw); ok {
			out = append(out, mdLine{
				kind:        blockQuote,
				level:       depth,
				prefix:      strings.Repeat("> ", depth),
				prefixAttrs: attrQuote,
				spans:       parseInline(rest, attrQuote),
			})
			ol = 0
			continue
		}

		if indent, marker, rest, ok := bullet(raw); ok {
			if marker != "• " {
				// 生成器一律把有序项写成 "1. "（见 htmlmd.go 的
				// renderListAt）—— 它不知道渲染器会不会自己编号，写死
				// 序号反而容易冲突。那就由这里补上真实序号。
				ol++
				marker = strconv.Itoa(ol) + ". "
			} else {
				ol = 0
			}
			out = append(out, mdLine{
				kind:   blockBullet,
				level:  indent,
				prefix: strings.Repeat("  ", indent) + marker,
				spans:  parseInline(rest, 0),
			})
			continue
		}

		out = append(out, mdLine{kind: blockPara, spans: parseInline(raw, 0)})
		ol = 0
	}
	return out
}

// fenceRun 判断一行是不是围栏代码块的边界，是的话返回反引号个数。
func fenceRun(s string) (int, bool) {
	t := strings.TrimLeft(s, " ")
	if t == "" || t[0] != '`' {
		return 0, false
	}
	n := 0
	for n < len(t) && t[n] == '`' {
		n++
	}
	if n < 3 {
		return 0, false
	}
	return n, true
}

// isRule 判断一行是不是分隔线。
//
// 生成器只产出 "---"（见 htmlmd.go 的 atom.Hr 分支），多认几种无妨。
// 列表项不会被误判：`- 第一项` 里除了 `-` 还有空格和文字。
func isRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	for _, r := range s {
		if r != '-' && r != '*' && r != '_' {
			return false
		}
	}
	return true
}

// heading 识别 "# 标题"，返回级别和去掉标记后的内容。
func heading(s string) (level int, rest string, ok bool) {
	t := strings.TrimLeft(s, " ")
	if t == "" || t[0] != '#' {
		return 0, "", false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	// 必须是 "# 空" 的形式。`#foo` 不是标题，`####### x` 也不是。
	if n > 6 || n >= len(t) || t[n] != ' ' {
		return 0, "", false
	}
	return n, strings.TrimLeft(t[n:], " "), true
}

// quote 识别引用，返回嵌套层数和去掉 "> " 之后的内容。
func quote(s string) (depth int, rest string, ok bool) {
	t := s
	for {
		u := strings.TrimLeft(t, " ")
		if strings.HasPrefix(u, "> ") {
			depth++
			t = u[2:]
			continue
		}
		if u == ">" {
			depth++
			t = ""
			continue
		}
		break
	}
	if depth == 0 {
		return 0, "", false
	}
	return depth, t, true
}

// bullet 识别列表项，返回嵌套层数、展示用标记和内容。
//
// 生成器用两空格一层缩进（见 htmlmd.go 的 renderListAt），所以这里也
// 按两空格算层。
func bullet(s string) (level int, marker, rest string, ok bool) {
	i := 0
	for i+2 <= len(s) && s[i] == ' ' && s[i+1] == ' ' {
		i += 2
	}
	level = i / 2
	t := s[i:]
	// 无序项渲染成 "• "，有序项交给调用方编号（这里返回占位）。
	if strings.HasPrefix(t, "- ") {
		return level, "• ", t[2:], true
	}
	if strings.HasPrefix(t, "1. ") {
		return level, "1. ", t[3:], true
	}
	return 0, "", "", false
}

// ---- 行内解析 ----

// parseInline 解析一段行内 Markdown。
//
// base 是外层已经攒下的样式位 —— ***粗斜体*** 这种嵌套靠它传下去。
func parseInline(s string, base attrs) []span {
	var out []span

	emit := func(text string, a attrs, url string) {
		if text == "" {
			return
		}
		// 与上一段样式相同就并进去，少切几段、少几串转义序列。
		if n := len(out); n > 0 && out[n-1].attrs == a && out[n-1].url == url {
			out[n-1].text += text
			return
		}
		out = append(out, span{text: text, attrs: a, url: url})
	}

	for i := 0; i < len(s); {
		switch {
		// 转义：下一个字符是用户的字面文字，不是标记。
		case s[i] == '\\' && i+1 < len(s):
			emit(string(s[i+1]), base, "")
			i += 2

		case strings.HasPrefix(s[i:], "**"):
			if inner, next, ok := until(s, i+2, "**"); ok {
				out = append(out, parseInline(inner, base|attrBold)...)
				i = next
				continue
			}
			emit("**", base, "")
			i += 2

		case strings.HasPrefix(s[i:], "~~"):
			if inner, next, ok := until(s, i+2, "~~"); ok {
				out = append(out, parseInline(inner, base|attrStrike)...)
				i = next
				continue
			}
			emit("~~", base, "")
			i += 2

		case s[i] == '*':
			if inner, next, ok := until(s, i+1, "*"); ok {
				out = append(out, parseInline(inner, base|attrItalic)...)
				i = next
				continue
			}
			emit("*", base, "")
			i++

		case s[i] == '`':
			n := backtickRun(s, i)
			if c := findFence(s, i+n, n); c >= 0 {
				emit(trimCodePad(s[i+n:c]), base|attrCode, "")
				i = c + n
				continue
			}
			emit(strings.Repeat("`", n), base, "")
			i += n

		case s[i] == '[':
			if text, url, next, ok := linkAt(s, i); ok {
				emit(text, attrLink, url)
				i = next
				continue
			}
			emit("[", base, "")
			i++

		case s[i] == '<':
			if url, next, ok := autolinkAt(s, i); ok {
				emit(url, attrLink, url)
				i = next
				continue
			}
			emit("<", base, "")
			i++

		default:
			// 攒普通字符，直到下一个可能是标记的位置。
			j := i + 1
			for j < len(s) && !isMarkerByte(s[j]) {
				j++
			}
			emit(s[i:j], base, "")
			i = j
		}
	}
	return out
}

// isMarkerByte 判断一个字节有没有可能是标记的开头。
//
// 注意 `(` 和 `)` 不在内 —— 它们只在 [文字](链接) 的上下文里才有意义，
// 而那个上下文由 `[` 触发。把它们也算成标记开头，只会让正文里普通的
// 括号把字符串切得七零八落。
func isMarkerByte(c byte) bool {
	switch c {
	case '\\', '*', '~', '`', '[', '<':
		return true
	}
	return false
}

// until 找**恰好**是 marker 的闭合标记，返回它前面的内容和之后的位置。
func until(s string, start int, marker string) (inner string, next int, ok bool) {
	idx := strings.Index(s[start:], marker)
	if idx < 0 {
		return "", 0, false
	}
	idx += start
	// 前后不能再有同类字符，否则匹配到的是更长的一串的一部分。
	// 比如找 "**" 时不该匹配到 "***" 的前两个。
	ch := marker[0]
	if ch == '*' || ch == '~' {
		if idx > 0 && s[idx-1] == ch {
			return "", 0, false
		}
		if idx+len(marker) < len(s) && s[idx+len(marker)] == ch {
			return "", 0, false
		}
	}
	return s[start:idx], idx + len(marker), true
}

// backtickRun 数出 s[i:] 开头连续的反引号个数。
func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// findFence 从 start 开始找**恰好 n 个**反引号的闭合围栏，找不到返回 -1。
//
// 不能直接用 strings.Index：围栏是两个反引号、而内容里出现三个时，Index 会
// 匹配到那个更长串的前两个字符，闭合位置就偏了。
//
// 注意这段注释里不写双反引号的字面量：gofmt 会把 Go 注释里的双反引号
// 当成 Markdown 智能引号的源，改写成 “，意思就毁了。
func findFence(s string, start, n int) int {
	if n <= 0 {
		return -1
	}
	for i := start; i+n <= len(s); {
		j := strings.Index(s[i:], strings.Repeat("`", n))
		if j < 0 {
			return -1
		}
		j += i
		before := j == 0 || s[j-1] != '`'
		after := j+n >= len(s) || s[j+n] != '`'
		if before && after {
			return j
		}
		i = j + 1
	}
	return -1
}

// trimCodePad 去掉行内代码两侧为「内容本身含反引号」而留的填充空格。
//
// 内容含反引号时，生成器会把围栏加长一个反引号并在两侧各留一个空格
// （见 htmlmd.go 的 codeSpan）。那两个空格是语法的一部分，不是内容。
func trimCodePad(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, " ") && strings.HasSuffix(s, " ") &&
		strings.TrimSpace(s) != "" {
		return s[1 : len(s)-1]
	}
	return s
}

// linkAt 解析 [文字](链接)。
//
// 地址里可以有括号：生成器不转义 ( 和 )，而真实地址里它们不少见
// （维基百科的条目名就常带括号）。所以闭合括号要按深度配平着找，
// 不能取第一个 ')'。
func linkAt(s string, i int) (text, url string, next int, ok bool) {
	sep := strings.Index(s[i:], "](")
	if sep < 0 {
		return "", "", 0, false
	}
	sep += i

	depth := 0
	end := -1
	for j := sep + 2; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				end = j
			} else {
				depth--
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", "", 0, false
	}

	text = s[i+1 : sep]
	url = s[sep+2 : end]
	if text == "" || url == "" {
		return "", "", 0, false
	}
	return text, url, end + 1, true
}

// autolinkAt 解析 <自动链接>。
func autolinkAt(s string, i int) (url string, next int, ok bool) {
	end := strings.IndexByte(s[i:], '>')
	if end < 0 {
		return "", 0, false
	}
	end += i
	inner := s[i+1 : end]
	// 里面不能有空白 —— 有空白说明这不是个地址，是正文里的尖括号。
	if inner == "" || strings.ContainsAny(inner, " \t") {
		return "", 0, false
	}
	return inner, end + 1, true
}

// ---- 折行与上色 ----

// mdRune 是折行时用的扁平单元：一个显示字符，连同它该上的样式。
//
// 折行本来可以直接在 spans 上做，但「往回找一个能断的位置」要能随机访问
// 每个字符，在 spans 上反而绕。所以先摊平、折完再合并回 spans。
type mdRune struct {
	r     rune
	w     int
	attrs attrs
	url   string
}

// flattenLine 把一行的片段摊平成字符序列。
func flattenLine(ln mdLine) []mdRune {
	var out []mdRune
	for _, sp := range ln.spans {
		for _, r := range sp.text {
			out = append(out, mdRune{r: r, w: cellWidth(r), attrs: sp.attrs, url: sp.url})
		}
	}
	return out
}

// spansFrom 把字符序列装回片段。相邻且样式相同的字符合并成一个片段 ——
// 少产出几段转义序列。
func spansFrom(prefix string, prefixAttrs attrs, rs []mdRune) []span {
	var out []span
	if prefix != "" {
		out = append(out, span{text: prefix, attrs: prefixAttrs})
	}
	for _, m := range rs {
		if n := len(out); n > 0 && out[n-1].attrs == m.attrs && out[n-1].url == m.url {
			out[n-1].text += string(m.r)
			continue
		}
		out = append(out, span{text: string(m.r), attrs: m.attrs, url: m.url})
	}
	return out
}

// skipSpaces 跳过 i 起的连续空格。
func skipSpaces(flat []mdRune, i int) int {
	for i < len(flat) && flat[i].r == ' ' {
		i++
	}
	return i
}

// breakPoint 在「这一行已经装到 end（不含）」的前提下往回找一个合适的折行
// 位置，返回**下一行的起始下标**；找不到就返回 start，调用方按硬断处理。
//
// 为什么要往回找：纯按宽度断会把英文单词劈成两半
// （wonderful → 「hello wo」/「nderful」）。汉字没这个问题，它在哪儿断都
// 读得通 —— 所以下面把「宽字符」也当成可断点，于是纯中文文本的行为和
// 按宽度硬断完全一样（处处可断），只有拉丁字母串才真的需要找空格。
func breakPoint(flat []mdRune, start, end int) int {
	for cut := end; cut > start; cut-- {
		// 断在空格前：那个空格留给下一行，由 skipSpaces 吃掉。
		// 不这么做的话空格会顶在续行开头，悬挂缩进就从 2 格变成 3 格。
		if flat[cut].r == ' ' {
			return cut
		}
		if flat[cut].w == 2 || flat[cut-1].w == 2 {
			return cut
		}
	}
	return start
}

// wrapLine 把一行按显示宽度折开。
//
// 第一行带 prefix（列表的「• 」、引用的「> 」），续行用等宽空格做悬挂缩进，
// 对齐到内容起始处，不然折行后就分不清哪一行属于哪一项。示意：
//
//	首行：• 一个很长的列表项，长到要折行……
//	续行：  续行缩进到内容起始处
//
// 断点是「按词」的，见 breakPoint。
func wrapLine(ln mdLine, width int) [][]span {
	// 缩进宽度用 textWidth（= cellWidth 求和）而不是 runewidth.StringWidth：
	// 列表的「• 」里那个 • 是 East Asian Ambiguous，runewidth 在中文 locale
	// 下算 2 列、lipgloss 算 1 列 —— 用错了续行就比首行内容多缩一格。
	// 详见 styles.go 里 cellWidth 的注释。
	hang := ""
	if ln.prefix != "" {
		hang = strings.Repeat(" ", textWidth(ln.prefix))
	}
	hangW := textWidth(hang)

	flat := flattenLine(ln)

	rowPrefix, rowAttrs := ln.prefix, ln.prefixAttrs
	rowW := textWidth(rowPrefix)

	var rows [][]span
	for start := 0; start < len(flat); {
		// 这一行最多装到哪。
		end, w := start, rowW
		for end < len(flat) && w+flat[end].w <= width {
			w += flat[end].w
			end++
		}
		if end == len(flat) {
			rows = append(rows, spansFrom(rowPrefix, rowAttrs, flat[start:end]))
			break
		}

		if cut := breakPoint(flat, start, end); cut > start {
			rows = append(rows, spansFrom(rowPrefix, rowAttrs, flat[start:cut]))
			start = skipSpaces(flat, cut)
		} else {
			// 这一行里没有可断的位置（一个超长单词，比如长 URL 文字）。
			// 只能硬断 —— 但必须至少吃掉一个字符：前缀本身就超宽时
			// （很深的嵌套列表），少了这一步会在行首反复换行、死循环。
			if end == start {
				end = start + 1
			}
			rows = append(rows, spansFrom(rowPrefix, rowAttrs, flat[start:end]))
			start = end
		}

		rowPrefix, rowAttrs, rowW = hang, ln.prefixAttrs, hangW
	}

	if len(rows) == 0 {
		rows = append(rows, spansFrom(rowPrefix, rowAttrs, nil))
	}
	return rows
}

// renderRow 把一行片段上色。
func renderRow(spans []span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(styleSpan(s))
	}
	return b.String()
}

// styleSpan 给一段文本上色。
func styleSpan(s span) string {
	// ⚠️ 链接走单独一条路，**忽略其它所有属性**。
	//
	// 不给它加粗/下划线不是偷懒：styles.go 里 hyperlink 的注释记着那个坑 ——
	// 样式一旦带上下划线，lipgloss 会给每个字符单独套一串 SGR，插进 OSC 8
	// 序列里把它切碎，链接就变成「看着正常但点不动」。规矩取最窄的一条：
	// 链接只上前景色。
	if s.attrs&attrLink != 0 {
		return styleLink.Render(hyperlink(s.url, s.text))
	}
	if s.attrs == 0 {
		return s.text
	}
	return markdownStyle(s.attrs).Render(s.text)
}

// markdownStyle 把样式位翻成 lipgloss 样式。
func markdownStyle(a attrs) lipgloss.Style {
	st := lipgloss.NewStyle()
	if a&attrBold != 0 {
		st = st.Bold(true)
	}
	if a&attrItalic != 0 {
		st = st.Italic(true)
	}
	if a&attrStrike != 0 {
		st = st.Strikethrough(true)
	}
	if a&attrCode != 0 {
		st = st.Foreground(lipgloss.Color("180"))
	}
	if a&attrQuote != 0 {
		st = st.Foreground(lipgloss.Color("245"))
	}
	return st
}
