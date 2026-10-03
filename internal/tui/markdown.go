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
//	[文字](链接)  ![替代文字](图片地址)  <自动链接>
//	# 标题（1-6 级）  ---  分隔线
//	> 引用
//	- 列表项 / 1. 列表项（嵌套用两空格缩进）
//	| 表头 | 表头 |
//	| --- | --- |
//	| 单元格 | 单元格 |
//	``` 围栏代码块 ```
//
// 这一点是整套做法的地基：生成器会把正文里出现的 \ ` * _ [ ] < 一律
// 转义成 \<c>（见 escapeMarkdownRune），所以**没转义的 * 一定是我们自己
// 写的标记，转义的才是用户的字面文字**。解析因此可以是确定性的，不需要
// 通用解析器那种「* 后面不能跟空格才算强调」的模糊启发式 —— 那种启发式
// 在中文和邮件排版里经常猜错。
//
// # ⚠️ 这里的语法集合必须和生成端**相等**
//
// 「封闭已知」是个承诺：生成端写得出什么，这里就得认得出什么。承诺一旦
// 打破，表现不是报错，而是**标记原样铺进聊天流** —— 用户看到的是
// `!perational`、`| 接口 | 正常 |` 这种半截源码，比不渲染还难读。
//
// 2026-10-02 就是这么翻的车：生成端写 ![](src) 和表格已经很久了，渲染端
// 两个都不认。所以新增生成端语法时，**同一个改动里**要在这里补上对应分支，
// 两边各加一条判据。守着这条的是 markdown_test.go 里那组「生成端产出 →
// 渲染端不留标记」的用例。
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
	// attrHeading 占最高的两位，存标题的**显眼程度**（见下面的 tier* 常量）。
	// 它是 attrs 里唯一一个「多位存值」的属性 —— 别的属性都是「有/无」，
	// 而标题的层级天然是个值。占两位不用额外字段：uint8 正好还剩两位。
	attrHeading
	_
)

// 标题的显眼程度。**由解析层定，渲染层只负责翻成样式** ——
// 哪些级别算「大标题」是 Markdown 的语义，不是终端的事。
const (
	tierNone   = 0 // 不是标题
	tierStrong = 1 // # / ##
	tierMid    = 2 // ### / ####
	tierPlain  = 3 // ##### / ######：只加粗
)

// headingShift 是标题位数在 attrs 里的起始位置。
const headingShift = 6

// headingTier 取出标题的显眼程度。
func headingTier(a attrs) int { return int(a >> headingShift) }

// withHeadingTier 把显眼程度写进属性位。
func withHeadingTier(a attrs, t int) attrs {
	return a&^(3<<headingShift) | attrs(t)<<headingShift
}

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
	// blockTableHead 是表格的表头行。分隔行（| --- | --- |）不单独成行 ——
	// 它存在的意义只是告诉解析器「上一行是表头」，然后就被丢掉。
	blockTableHead
	// blockTableRow 是表格的数据行。表格**按整块**渲染（见 renderTableBlock），
	// 所以要靠连续的 kind 把它的行圈出来。
	blockTableRow
)

// mdLine 是解析出来的一行（还没折行）。
type mdLine struct {
	kind blockKind

	// prefix 是第一行的纯文本前缀（"• "、"1. "、"> "）。它是**纯文本**，
	// 所以折行时它的宽度会一起算进去，上色不会影响折行位置。
	prefix      string
	prefixAttrs attrs

	spans []span

	// cells 只在表格行上有值，每格是一段行内内容。
	//
	// 单元格里可以有行内标记：生成端走的是 cellText → blockBuilder，
	// 加粗、链接都留得下来（见 htmlmd.go）。所以这里存的是解析好的
	// 片段而不是纯文本，渲染时照样要过 styleSpan。
	cells [][]span
}

// ---- 入口 ----

// imageFallback 是替代文字为空时渲染器自己的兜底。
//
// 生成端已经用 imagePlaceholder 兜过一层（见 htmlmd.go 的 imageLabel），
// 所以这个分支正常走不到。留着是因为渲染器的输入不止「刚生成的正文」——
// 存量邮件里就存着改前生成的 ![](src)。真走到这里还没兜住的话，一整条
// 地址（常带用户标识）会直接铺进聊天流，那比少显示一个词糟得多。
const imageFallback = "图片"

// headingTier 把 Markdown 的标题级别（1-6）折成显眼程度。
//
// 三档而不是六档：终端里没有字号，能用的维度只有「颜色」和「加粗」，
// 六档必然有几档长得一模一样，等于骗人。分档的依据是**结构作用** ——
// # / ## 是「这封信在讲什么」，### / #### 是「这一节在讲什么」，
// 更深的在邮件里基本不出现，只保留加粗。
func headingTierFor(level int) int {
	switch {
	case level <= 2:
		return tierStrong
	case level <= 4:
		return tierMid
	default:
		return tierPlain
	}
}

// renderMarkdown 把邮件正文渲染成终端里的若干行。
//
// width 是可用显示宽度。折行在这里就定下来，之后才上色，所以颜色不会
// 污染宽度计算。
// mdTheme 是 Markdown 渲染用的那组颜色。
//
// 抽出来是因为 Zen 需要一套更克制的配色。Normal 用紫 / 蓝 / 琥珀区分标题
// 层级和代码，那是「信息密度优先」的选择；Zen 的整张脸只有三档灰，正文里
// 突然冒出一个紫色标题，整个界面就散了。
//
// 解析和折行两边完全共用 —— 分家的只有「最后上什么色」这一步。这也是
// 为什么不开一份独立的 Zen renderMarkdown：折行、表格对齐、OSC 8 那几个
// 坑踩一次就够了，踩两遍必然只修好一边。
type mdTheme struct {
	Code       lipgloss.Color
	Quote      lipgloss.Color
	HeadStrong lipgloss.Color
	HeadMid    lipgloss.Color
	Link       lipgloss.Color
	Rule       lipgloss.Color
}

// defaultMDTheme 是 Normal 模式的配色。数值和主题化之前逐字节一致 ——
// 抽主题不该顺手改 Normal 的样子。
var defaultMDTheme = mdTheme{
	Code:       "180",
	Quote:      "245",
	HeadStrong: "141",
	HeadMid:    "110",
	Link:       "45",
	Rule:       "240",
}

// zenMDTheme 是 Zen 的配色：**去掉色相，只留明度**。
//
// 标题不再靠紫 / 蓝区分层级，改用「比正文更亮 + 加粗」—— 层级还在，
// 颜色没了。代码块压暗一档当成「引文」处理，靠上下留白圈出来。
// 只有链接保留一点强调色：它是唯一一个「点得动」的东西，值得破例。
var zenMDTheme = mdTheme{
	// 代码块压暗到 243。246 试过，和正文几乎分不出来 —— 代码块在终端里
	// 没有背景块可用（Zen 不要大面积底色），明度是唯一能用的区分手段。
	Code:       "243",
	Quote:      "242",
	HeadStrong: "255",
	HeadMid:    "252",
	Link:       "109",
	Rule:       "237",
}

// renderMarkdown 是 Normal 模式的入口，签名保持不变。
//
// 不改签名是为了不动那七百多行 markdown 测试 —— 它们验的是解析和折行，
// 和配色无关，不该因为 Zen 要换一套颜色就跟着改一遍。
func renderMarkdown(src string, width int) []string {
	return renderMarkdownThemed(src, width, defaultMDTheme)
}

// renderMarkdownThemed 是带主题的渲染。
func renderMarkdownThemed(src string, width int, th mdTheme) []string {
	if width < 1 {
		width = 1
	}

	lines := parseMarkdown(src)
	var out []string
	for i := 0; i < len(lines); {
		ln := lines[i]

		// 表格要整块拿到手才能对齐：列宽是所有行的最大值。所以先按连续
		// 的 kind 把这一块圈出来，单独走 renderTableBlock。
		if ln.kind == blockTableHead {
			j := i + 1
			for j < len(lines) && lines[j].kind == blockTableRow {
				j++
			}
			out = append(out, renderTableBlock(lines[i:j], width, th)...)
			i = j
			continue
		}

		if ln.kind == blockRule {
			// 分隔线画成一条实心横线，比原样的 "---" 更像分隔线。
			//
			// 铺满整个可用宽度。原先这里硬限 40 列，在宽终端里一条分隔线
			// 只是一小截短线悬在左边，看着像没渲染完 —— 而它本来是对方
			// 在正文里画的一条分隔线，视觉上就该横贯聊天区。
			//
			// width 是调用方给的**可用**宽度（renderMessage 传的是 width-4，
			// 左右各留了缩进），所以铺满它不会顶破版面。
			out = append(out, lipgloss.NewStyle().Foreground(th.Rule).
				Render(strings.Repeat("─", width)))
			i++
			continue
		}
		if ln.kind == blockPara && len(ln.spans) == 0 {
			out = append(out, "")
			i++
			continue
		}
		for _, row := range wrapLine(ln, width) {
			out = append(out, renderRow(row, th))
		}
		i++
	}
	return out
}

// ---- 解析 ----

// parseMarkdown 把正文按行解析成块。
//
// 用下标遍历而不是 range：表格是**多行**结构，识别表头时要往后看一行
// （分隔行），认下来之后还要一次吃掉后续的数据行。
func parseMarkdown(src string) []mdLine {
	var out []mdLine
	lines := strings.Split(src, "\n")

	fence := 0 // 围栏代码块的反引号个数，0 表示不在代码块里
	ol := 0    // 连续有序列表项的计数，遇到别的东西归零

	for i := 0; i < len(lines); i++ {
		raw := lines[i]

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
			// 显眼程度一起记下来（见 tier* 常量）：终端里放不了字号，
			// 颜色是唯一还能表达层级的维度。
			//
			// 记的位置是**样式位**而不是 mdLine 上的一个字段：层级是每一段
			// 文字自身的性质（标题里嵌的普通文字也该跟着变），放进 attrs 才
			// 跟着 span 走。原先 mdLine 上有个 `level int` 只写不读，已删。
			out = append(out, mdLine{
				kind:  blockHeading,
				spans: parseInline(rest, withHeadingTier(attrBold, headingTierFor(level))),
			})
			ol = 0
			continue
		}

		if depth, rest, ok := quote(raw); ok {
			out = append(out, mdLine{
				kind:        blockQuote,
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
				prefix: strings.Repeat("  ", indent) + marker,
				spans:  parseInline(rest, 0),
			})
			continue
		}

		// 表格：本行是 | … |，且**下一行是分隔行**才认。
		//
		// 只靠「以 | 开头」认不行 —— 正文里孤零零一行带竖线的句子就会
		// 被误判成表格。分隔行是生成端一定会写的（见 htmlmd.go 的
		// renderTable），拿它当凭据最稳。分隔行本身没有渲染价值，吃掉。
		if isTableRow(trimmed) && i+1 < len(lines) && isTableSep(lines[i+1]) {
			out = append(out, mdLine{kind: blockTableHead, cells: tableCells(trimmed)})
			j := i + 2
			for ; j < len(lines) && isTableRow(strings.TrimSpace(lines[j])); j++ {
				out = append(out, mdLine{
					kind:  blockTableRow,
					cells: tableCells(strings.TrimSpace(lines[j])),
				})
			}
			i = j - 1
			ol = 0
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

// ---- 表格 ----

// isTableRow 判断一行是不是表格行（以 | 开头、以 | 结尾）。
//
// 两边都要有竖线：生成端写的正是 `| a | b |` 这种带边框的形式（见
// htmlmd.go 的 renderTable），中间那种不带边框的写法它不产出，不认也罢。
func isTableRow(s string) bool {
	return len(s) >= 2 && strings.HasPrefix(s, "|") && strings.HasSuffix(s, "|")
}

// isTableSep 判断一行是不是表头下面的分隔行（| --- | --- |）。
//
// 允许冒号（对齐标记 :---:），虽然生成端只写 "---"。至少要有一个短横，
// 否则 `| | |` 这种空行也会被当成分隔行。
func isTableSep(s string) bool {
	t := strings.TrimSpace(s)
	if !isTableRow(t) {
		return false
	}
	dash := false
	for _, r := range t {
		switch r {
		case '-':
			dash = true
		case '|', ':', ' ', '\t':
		default:
			return false
		}
	}
	return dash
}

// tableCells 把一行表格拆成若干个解析好的单元格。
//
// 拆列要按**未转义**的 | 切：单元格里的 | 被生成端转义成了 \|（见
// escapeMarkdownRune 的 inTable），那是内容，不是列界。
func tableCells(line string) [][]span {
	raw := splitTableRow(line)
	out := make([][]span, 0, len(raw))
	for _, c := range raw {
		out = append(out, parseInline(c, 0))
	}
	return out
}

// splitTableRow 按未转义的 | 拆出一行的各列，去掉首尾边框。
//
// 保留单元格里的 \| 转义不还原 —— 交给 parseInline，它认得 \ 转义。
func splitTableRow(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")

	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			// 反斜杠连同它后面的字符一起留下，别在这一层还原 ——
			// 生成端把 \ 本身也转义成 \\，先还原会把两种情况弄混。
			cur.WriteByte(s[i])
			cur.WriteByte(s[i+1])
			i++
			continue
		}
		if s[i] == '|' {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(s[i])
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
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
				// 链接文字里**还可能有行内标记**：`<a href><b>立即购买</b></a>`
				// 生成出来就是 [**立即购买**](url)，`<a href><code>npm i</code></a>`
				// 则是 [`npm i`](url) —— 都是真实邮件里有的形状。
				//
				// 整段当成一个 span 的话这些标记不会被解析，`**` 和反引号
				// 会原样显示给用户。所以这里要**递归解析**：标记被吃掉、
				// 内容留下。
				//
				// 强调本身显示不出来（styleSpan 只给链接上前景色 ——
				// 加粗会像下划线一样把 OSC 8 切碎，链接就点不动了），
				// 但「吃掉不显示」正是我们要的：链接已经用颜色标出来了。
				for _, sp := range parseInline(text, base|attrLink) {
					emit(sp.text, sp.attrs, url)
				}
				i = next
				continue
			}
			emit("[", base, "")
			i++

		case strings.HasPrefix(s[i:], "!["):
			// 图片。终端里画不出图，替代文字（alt / title）就是这张图
			// **唯一**能留下的信息，所以直接把它当文字显示，不加标记 ——
			// 和 anchor 里图片按钮的做法一致（那里也是只显示 alt，
			// 见 htmlmd.go）。
			//
			// 改之前没有这个分支：![Operational](ok.svg) 会走成
			// 「普通字符 ! + 链接 [Operational](ok.svg)」，显示成
			// !Operational —— 用户看到的半截源码就是这个。
			if label, _, next, ok := imageAt(s, i); ok {
				if label == "" {
					label = imageFallback // 见常量处说明：绝不能把地址漏出去
				}
				emit(label, base, "")
				i = next
				continue
			}
			emit("!", base, "")
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
//
// `!` 得算：图片是 ![。虽然单独一个感叹号不是标记，但把它算进来只是让
// 普通文本多切一段（emit 会把同样式的相邻段并回去），代价远小于漏掉图片。
func isMarkerByte(c byte) bool {
	switch c {
	case '\\', '*', '~', '`', '[', '<', '!':
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

// imageAt 解析 ![替代文字](图片地址)。
//
// 形状和 linkAt 一样，差别只有两个：
//   - 替代文字**允许为空**（![ ](src) 合法，生成端也真的会写），空与否
//     由调用方决定怎么兜底；
//   - 返回的地址用不上，这里只是要正确跳过它 —— 终端画不出图。
//
// 闭合的 ] 取第一个就行：生成端把标签里的 ] 转义成了 \]（EscapeMarkdown），
// 未转义的 ] 一定是结尾。地址里的 ( ) 没被转义，所以要深度配平着找。
func imageAt(s string, i int) (label, url string, next int, ok bool) {
	// s[i:] 以 "![" 开头
	closeIdx := strings.IndexByte(s[i+2:], ']')
	if closeIdx < 0 {
		return "", "", 0, false
	}
	closeIdx += i + 2
	if closeIdx+1 >= len(s) || s[closeIdx+1] != '(' {
		return "", "", 0, false
	}

	depth := 0
	end := -1
	for j := closeIdx + 2; j < len(s); j++ {
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

	url = s[closeIdx+2 : end]
	if url == "" {
		// 没有地址就不是图片语法，交给调用方按普通字符处理。
		return "", "", 0, false
	}
	return s[i+2 : closeIdx], url, end + 1, true
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
func renderRow(spans []span, th mdTheme) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(styleSpan(s, th))
	}
	return b.String()
}

// spansWidth 量一段片段的显示宽度。
//
// 直接对**未上色的文本**求和，不能用 lipgloss.Width(renderRow(...))：
// 后者要认识 SGR 和 OSC 8 才算得对，而 OSC 8（超链接）lipgloss 的宽度
// 算法并不一定认。文本宽度是折行和对齐的**唯一**依据，这里必须是自己
// 说了算的。
func spansWidth(spans []span) int {
	w := 0
	for _, s := range spans {
		w += textWidth(s.text)
	}
	return w
}

// boldSpans 给整段加粗，返回新切片（不改调用方的那份）。
//
// 不在这里特判链接：链接「只上前景色、忽略其它属性」这条规矩**集中在
// styleSpan 里**，那里是唯一的出口。在这儿再挡一次看着更保险，其实
// 是不可观测的死代码（叠上 attrBold 也被 styleSpan 抹掉），而「看着
// 像在守着什么」的死代码比没有更糟。守着这条的是
// TestRenderMarkdown_BoldAroundLinkDoesNotBoldTheLinkText。
func boldSpans(spans []span) []span {
	out := make([]span, len(spans))
	copy(out, spans)
	for i := range out {
		out[i].attrs |= attrBold
	}
	return out
}

// truncateSpans 把一段片段截到 width 列以内，截断处补省略号。
//
// 得自己写：styles.go 里的 truncate 吃的是「纯文本 + 已经上好的色」，
// 而这里手里是片段。先截片段、再上色，顺序不能反 —— 反过来就会把转义
// 序列拦腰剪断。
func truncateSpans(spans []span, width int) []span {
	if spansWidth(spans) <= width {
		return spans
	}
	if width <= 0 {
		return nil
	}

	var out []span
	used := 0
	for _, s := range spans {
		room := width - used
		if room <= 0 {
			break
		}
		if textWidth(s.text) <= room {
			out = append(out, s)
			used += textWidth(s.text)
			continue
		}
		// 这一段放不下，逐字吃到剩最后一列，再补省略号。
		var b strings.Builder
		w := 0
		for _, r := range s.text {
			rw := cellWidth(r)
			if w+rw > room-1 {
				break
			}
			b.WriteRune(r)
			w += rw
		}
		if b.Len() > 0 {
			out = append(out, span{text: b.String(), attrs: s.attrs, url: s.url})
		}
		return append(out, span{text: "…", attrs: s.attrs})
	}
	return out
}

// 表格列之间的空隙宽度。两列："| 名称 | 状态 |" 渲染成 "名称  状态"。
const tableGap = 2

// 列被压到多窄就不再压了。再窄下去每格只剩两三个字，认不出内容。
const tableMinCol = 4

// renderTableBlock 把整张表渲染成对齐的若干行。
//
//   - 表头加粗，下面压一条细横线；
//   - 列宽取该列所有行的最大显示宽度，右补空格对齐；
//   - **不折行，超宽就截断**：表格一旦折行，列就对不上了，那还不如
//     把每一行老老实实截短。窄列（状态、序号）通常短，按渲染宽度把
//     最宽的那列逐步收回，短列不会被无谓地截。
//
// rows[0] 是表头，其余是数据行。
func renderTableBlock(rows []mdLine, width int, th mdTheme) []string {
	if len(rows) == 0 {
		return nil
	}

	cols := 0
	for _, r := range rows {
		if len(r.cells) > cols {
			cols = len(r.cells)
		}
	}
	if cols == 0 {
		return nil
	}

	// 列宽：每列所有单元格的最大宽度。
	w := make([]int, cols)
	for _, r := range rows {
		for j, c := range r.cells {
			if n := spansWidth(c); n > w[j] {
				w[j] = n
			}
		}
	}

	// 超出可用宽度就从最宽的那列开始收，收到最小宽度为止。
	avail := width - tableGap*(cols-1)
	for sumInts(w) > avail {
		k := 0
		for j := range w {
			if w[j] > w[k] {
				k = j
			}
		}
		if w[k] <= tableMinCol {
			break
		}
		w[k]--
	}

	// 连最小列宽都塞不下（终端极窄）时，对齐已经没有意义了 —— 退回
	// 「一格一格铺开」，交给普通折行。表格是为「看对齐」而存在的；
	// 塞不下的时候，对它的要求只剩「一格都别丢」这一条。
	if sumInts(w) > avail {
		return wrapTableAsParagraphs(rows, width, th)
	}

	var out []string
	for i, r := range rows {
		cells := r.cells
		if i == 0 {
			// 表头：加粗，并补一条分隔线。
			cells = make([][]span, cols)
			for j := range cells {
				if j < len(r.cells) {
					cells[j] = boldSpans(r.cells[j])
				}
			}
		}

		var b strings.Builder
		for j := 0; j < cols; j++ {
			if j > 0 {
				b.WriteString(strings.Repeat(" ", tableGap))
			}
			var cell []span
			if j < len(cells) {
				cell = truncateSpans(cells[j], w[j])
			}
			b.WriteString(renderRow(cell, th))
			b.WriteString(strings.Repeat(" ", w[j]-spansWidth(cell)))
		}
		out = append(out, strings.TrimRight(b.String(), " "))

		if i == 0 {
			// 表头下面那条线，用和分隔线同一个灰。
			var rule strings.Builder
			for j := 0; j < cols; j++ {
				if j > 0 {
					rule.WriteString(strings.Repeat(" ", tableGap))
				}
				rule.WriteString(strings.Repeat("─", w[j]))
			}
			out = append(out, styleMDRule.Render(strings.TrimRight(rule.String(), " ")))
		}
	}
	return out
}

// sumInts 求一串整数的和。
func sumInts(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}

// wrapTableAsParagraphs 是极窄终端的降级：把每一行铺成一段，交给普通折行。
//
// 丢了对齐，但一格内容都不丢，而且**必定**能折进给定宽度 —— 表格渲染
// 那条路给不了这个保证（列宽最多收到 tableMinCol，再窄就收不动了）。
func wrapTableAsParagraphs(rows []mdLine, width int, th mdTheme) []string {
	var out []string
	for _, r := range rows {
		var spans []span
		for j, c := range r.cells {
			if j > 0 {
				spans = append(spans, span{text: "  ·  "})
			}
			spans = append(spans, c...)
		}
		for _, row := range wrapLine(mdLine{spans: spans}, width) {
			out = append(out, renderRow(row, th))
		}
	}
	return out
}

// styleSpan 给一段文本上色。
func styleSpan(s span, th mdTheme) string {
	// ⚠️ 链接走单独一条路，**忽略其它所有属性**。
	//
	// 不给它加粗/下划线不是偷懒：styles.go 里 hyperlink 的注释记着那个坑 ——
	// 样式一旦带上下划线，lipgloss 会给每个字符单独套一串 SGR，插进 OSC 8
	// 序列里把它切碎，链接就变成「看着正常但点不动」。规矩取最窄的一条：
	// 链接只上前景色。
	if s.attrs&attrLink != 0 {
		return lipgloss.NewStyle().Foreground(th.Link).Render(hyperlink(s.url, s.text))
	}
	if s.attrs == 0 {
		return s.text
	}
	return markdownStyle(s.attrs, th).Render(s.text)
}

// markdownStyle 把样式位翻成 lipgloss 样式。
func markdownStyle(a attrs, th mdTheme) lipgloss.Style {
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
		st = st.Foreground(th.Code)
	}
	if a&attrQuote != 0 {
		st = st.Foreground(th.Quote)
	}
	// 标题分级：越显眼越靠前，后面覆盖前面的颜色。
	//
	// 只上**前景色**，不碰别的属性 —— 和链接那条规矩同一个理由：
	// 样式一旦带上下划线之类的，lipgloss 会给每个字符单独套一串 SGR。
	// 加粗本来就是每字符一串，标题不裹 OSC 8，所以这里安全；
	// 但也没必要多叠属性，颜色加粗两项足够表达层级。
	//
	// Zen 那套主题里 HeadStrong / HeadMid 都是灰（没有色相），层级只剩
	// 「亮一档 + 加粗」。这是刻意的：Zen 用明度而不是色相表达层级。
	switch headingTier(a) {
	case tierStrong:
		st = st.Bold(true).Foreground(th.HeadStrong)
	case tierMid:
		st = st.Bold(true).Foreground(th.HeadMid)
	case tierPlain:
		st = st.Bold(true)
	}
	return st
}
