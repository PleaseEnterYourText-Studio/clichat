package mail

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// 这个文件把 HTML 正文转成 Markdown。
//
// # 为什么不继续用正则
//
// text.go 里的 HTMLToText 是 v1 的捷径：几条正则把标签抹掉。它对
// 「一段一段的纯文字」够用，但一碰上真实邮件就散架 —— 真实邮件里的
// 结构几乎全靠这些撑着：
//
//   - <a style="display:inline-block;padding:12px;background:#07c">
//     确认订阅</a>   ← 「按钮」其实是个套了样式的链接，正则抹完只剩
//     「确认订阅」，URL 直接丢了，用户在终端里根本没法点
//   - <td><img src="btn.png" alt="查看订单"></td>  ← 图片按钮，重点在 alt
//   - <table> 布局（营销邮件几乎全用它排版）＋ 隐藏的 preheader
//   - 嵌套的 <b><i>、<blockquote> 里再套 <ul>
//
// 正则分不清「这个 <a> 是个按钮」和「这个 <a> 是正文里提了一句网址」，
// 也处理不了嵌套。所以这里改成用 x/net/html 建 DOM 再遍历 ——
// 也就是 text.go 那条注释里说的「不属于 v1 范围」的那件事。
//
// # 转换约定
//
//   - 链接与「按钮」统一转成 [文字](链接)。按钮在邮件里通常是两种写法：
//     套了样式的 <a>，或者 <button>/<input type=submit>。前者的地址一般
//     在 href 里，后者整类元素都没有 href，地址往往挂在别处（formaction /
//     data-href / onclick 里的 URL）—— 两边共用同一套兜底去挖，挖到就转
//     链接，挖不到就加粗：没有目标地址的「按钮」本来也没法变成链接，
//     加粗是为了让它别混在正文里看不出来。
//   - 图片按钮（<a> 里只包了张图）取其 alt 当链接文字。终端里显示不了图，
//     而 alt 正是服务商为这种情况准备的替代文字。
//   - 正文里的特殊字符一律转义，避免 Markdown 把用户的原文当标记解析。
//     比如正文里的 2*3 不会被渲染成斜体，user_id_name 也不会。
//
// # 不做的事
//
// 不还原 CSS 排版（终端里没有「左边距 40px」这回事），不下载图片，
// 不做 URL 的安全性判断 —— 那是渲染层的事。
func HTMLToMarkdown(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		// 解析器连容错都救不回来时退回旧的粗降级，至少让用户看到字。
		// 但要把结果转义，否则正文里的 * 会被 Markdown 当标记。
		return EscapeMarkdown(HTMLToText(src))
	}

	root := findBody(doc)
	if root == nil {
		root = doc
	}

	b := &blockBuilder{sep: "\n\n", base: baseFontSize(root)}
	b.blocks(root)
	return tidyMarkdown(b.String())
}

// findBody 找到 <body>。html.Parse 一定会造出 html/head/body 骨架，
// 但从 body 开始渲染能顺手把 <head> 里的 title/style/meta 全挡在外面。
func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findBody(c); got != nil {
			return got
		}
	}
	return nil
}

// ---- 块级渲染 ----

// blockBuilder 把节点流切成「块」。
//
// 分开维护 inline 和 out 两个缓冲是必需的：HTML 里行内内容和块级元素
// 是混着来的 —— `<div>你好<br>再见</div>` 里，文本和 <br> 之间有
// 兄弟节点。如果一看到子节点就当成独立的一块，这段会被拆成两段
// 再空行分开，读起来就散了。所以行内内容先攒着，遇到块级元素才收尾。
type blockBuilder struct {
	// sep 是块与块之间的分隔符。段落之间用空行（\n\n），而 div 这类
	// 通用容器用单换行 —— 邮件里 div 常被拿来当段落使，也常被拿来
	// 做纯排版，两种都按「空行分开」处理会产生大量多余空行。
	sep string

	// base 是这封信的「正文字号」（px）。用它来判断某个块的字号是不是
	// 「标题级」—— 见 headingLevel。0 表示没测出来，此时不做标题推断。
	base float64

	// starDepth 是「当前已经嵌进几层星号标记（* / **）里」。
	//
	// 存在的理由是强调会叠在一起：`<span style="font-weight:bold">甲<b>乙</b></span>`
	// 里外层包一次、<b> 再包一次，写出来是 `**甲**乙****` —— 内层的 ** 会
	// 和外层的配对错位，渲染出来是一堆裸露的星号。所以已经在星号里时，
	// 内层不再加标记。
	//
	// 只数星号，不数 ~~：`~~` 和星号不冲突，`**甲~~旧~~**` 渲染器认得。
	// 而星号嵌套（`<b><i>甲</i></b>` → `***甲***`）渲染器认不得 ——
	// 见 tui.until 里那段「前后不能再有同类字符」，它宁可整段原样显示，
	// 所以生成端不制造那种串。
	starDepth int

	// noMark 表示「这一段不要再包行内标记」。给标题的内容、按钮的标签、
	// 链接文字用 —— 它们的结果要么本来就渲染成粗体（标题），要么外面还
	// 要再包一层 **（无地址的按钮的标签），要么被整段当成一个链接 span
	// （链接文字，见 tui.parseInline）。内层再加标记是冗余，叠起来还会
	// 写出 `****` 或者让标记漏到屏幕上。
	noMark bool

	out    []string
	inline strings.Builder
	// pendSpace 表示「攒了一个空格待写」。HTML 的空白折叠规则：
	// 连续空白算一个，行首行尾的丢掉。
	pendSpace bool
	// inTable 为真时 | 必须转义，否则会把表格的列切错。
	inTable bool
}

// sub 派生一个同上下文的子构造器，只换分隔符。
//
// 要继承的是**语义约束**：
//
//   - base 是整封信的性质（比较的是本合同字号和全文正文基准），不传的话
//     嵌在表格/列表里的标题会静默地不再被识别。
//   - noMark 是「这段文字的上层已经决定了不加标记」（标题的内容、按钮的
//     标签、链接文字），同样要一路传下去，否则深一层又会包出 `**`，
//     叠到外层就成了 `****`，或者漏进链接文字里当字面星号显示。
//
// **不继承 starDepth。** 它记的是「光标附近已经有星号了」，但外层包**块级**
// 子元素时 applyRange 会放弃加标记（标记无处可加），此时内层若也以为
// 「已经在星号里」而闭嘴，整段就一处都不粗 —— 信息白丢。它只该在
// 同一个构造器内部传递。
func (b *blockBuilder) sub(sep string) *blockBuilder {
	return &blockBuilder{sep: sep, base: b.base, inTable: b.inTable, noMark: b.noMark}
}

// text 写入文本，按 HTML 规则折叠空白，并转义 Markdown 特殊字符。
func (b *blockBuilder) text(s string) {
	for _, r := range s {
		if isHTMLSpace(r) {
			// 行首的空格没有意义，丢掉。
			b.pendSpace = b.inline.Len() > 0
			continue
		}
		b.flushSpace()
		b.inline.WriteString(escapeMarkdownRune(r, b.atLineStart(), b.inTable))
	}
}

// raw 写入一段已经成型、不应转义的行内 Markdown（比如我们自己生成的
// ** 或 [文字](链接)）。
func (b *blockBuilder) raw(s string) {
	b.flushSpace()
	b.inline.WriteString(s)
}

// br 是硬换行。Markdown 里段内单换行在部分渲染器里会显示成空格，
// 但这里仍然用单换行：正文的主流消费方是本项目的终端渲染器，
// 而原始 Markdown 里的单换行直接就是换行 —— 两种视图都读得通。
// 换成行尾两个空格这种标准写法，代价是正文里到处是看不见的尾随空格，
// 用户复制时会带走，反而更脏。
func (b *blockBuilder) br() {
	b.pendSpace = false
	b.inline.WriteString("\n")
}

// atLineStart 报告下一个字符是否处在行的开头 —— 行首的 #、>、-、+
// 会被 Markdown 当成块级标记，必须转义。
func (b *blockBuilder) atLineStart() bool {
	s := b.inline.String()
	return s == "" || strings.HasSuffix(s, "\n")
}

func (b *blockBuilder) flushSpace() {
	if b.pendSpace {
		b.inline.WriteString(" ")
		b.pendSpace = false
	}
}

// flush 把攒着的行内内容收尾成一块。
func (b *blockBuilder) flush() {
	b.pendSpace = false
	s := strings.Trim(b.inline.String(), " \t\n")
	b.inline.Reset()
	if s != "" {
		b.out = append(b.out, s)
	}
}

// push 直接推入一整块（段落、列表、代码块这类自己已经成型的）。
func (b *blockBuilder) push(s string) {
	b.flush()
	s = strings.Trim(s, " \t\n")
	if s != "" {
		b.out = append(b.out, s)
	}
}

func (b *blockBuilder) String() string { return strings.Join(b.out, b.sep) }

// blocks 遍历一批节点，把它们按块写进 b。
func (b *blockBuilder) blocks(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.node(c)
	}
	b.flush()
}

func (b *blockBuilder) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.text(n.Data)

	case html.CommentNode, html.DoctypeNode:
		// 注释要丢。Outlook 的条件编译注释（<!--[if mso]>...<![endif]-->）
		// 里裹着一整段给 Word 渲染引擎用的 HTML，读出来是纯噪音。

	case html.ElementNode:
		if skipElement(n) {
			return
		}
		switch n.DataAtom {
		// --- 通用的块级容器 ---
		case atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer,
			atom.Main, atom.Aside, atom.Nav, atom.Figure, atom.Figcaption,
			atom.Form, atom.Fieldset, atom.Address, atom.Details, atom.Summary:
			b.container(n, "\n")

		case atom.P, atom.Caption, atom.Dt, atom.Dd, atom.Li, atom.Td, atom.Th:
			// 这几个自己就是一块，块内用单换行。
			b.container(n, "\n")

		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			b.flush()
			level := int(n.Data[1] - '0')
			inner := renderInline(n)
			if inner != "" {
				b.push(strings.Repeat("#", level) + " " + inner)
			}

		case atom.Hr:
			b.flush()
			b.push("---")

		case atom.Br:
			b.br()

		case atom.Pre:
			b.flush()
			if code := preText(n); strings.TrimSpace(code) != "" {
				b.push(codeFence(code))
			}

		case atom.Blockquote:
			b.flush()
			inner := b.childrenBlocks(n, "\n\n")
			if inner != "" {
				b.push(prefixLines(inner, "> "))
			}

		case atom.Ul:
			b.flush()
			b.push(b.renderList(n, false))
		case atom.Ol:
			b.flush()
			b.push(b.renderList(n, true))

		case atom.Table:
			b.flush()
			b.push(b.renderTable(n))

		// --- 明确要丢掉的东西 ---
		case atom.Style, atom.Script, atom.Title, atom.Meta, atom.Link,
			atom.Base, atom.Noscript, atom.Iframe, atom.Object, atom.Embed,
			atom.Svg, atom.Canvas, atom.Template, atom.Head, atom.Map,
			atom.Audio, atom.Video, atom.Picture, atom.Source, atom.Track:
			// 丢掉 ≠ 丢弃内容：<video> 这类里面通常只有 <source>，
			// 没有可读文字，丢掉是干净的。真要带文字的话外层还有
			// 段落兜着。

		default:
			// 行内元素（a / strong / span / img …）出现在块级位置：
			// 它自己就构成一块，交给行内渲染。
			b.inlineNode(n)
		}

	default:
		// 其它节点类型（ErrorNode 等）忽略。
	}
}

// container 渲染一个「容器型」块：它的子节点各自成块，用 sep 连接。
//
// 标题推断也在这里做，而不是在 node() 的 switch 里 —— 同一个 <td> 会从
// 两条路过来：作为子节点（node → container）或作为表格单元格
// （renderLayoutTable → cellBlocks）。放在 container/cellBlocks 这一层，
// 两条路都覆盖得到。
func (b *blockBuilder) container(n *html.Node, sep string) {
	if lv := b.headingLevel(n); lv > 0 {
		b.flush()
		if inner := renderInline(n); inner != "" {
			b.push(strings.Repeat("#", lv) + " " + inner)
		}
		return
	}
	b.push(b.boldBlock(n, b.childrenBlocks(n, sep)))
}

// boldBlock 给「整块自己声明了加粗」的段落包上 **：
//
//	<td style="font-weight:bold">通过 HTTP 路由快速修改…</td>
//
// 前提是**单行**——标记不跨行（渲染器逐行解析）。多行内容原样返回，
// 宁可少一处加粗，也不往正文里漏标记。
func (b *blockBuilder) boldBlock(n *html.Node, s string) string {
	if !elemBold(n) || b.noMark {
		return s
	}
	if strings.TrimSpace(s) == "" || strings.Contains(s, "\n") {
		return s
	}
	return wrapInline(s, "**")
}

// childrenBlocks 把子节点渲染成块，用 sep 连接。
func (b *blockBuilder) childrenBlocks(n *html.Node, sep string) string {
	sub := b.sub(sep)
	sub.blocks(n)
	return sub.String()
}

// ---- 行内渲染 ----

// inlineNode 把一个节点当行内内容写进 b。
func (b *blockBuilder) inlineNode(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.text(n.Data)

	case html.CommentNode, html.DoctypeNode:

	case html.ElementNode:
		if skipElement(n) {
			return
		}
		// 无语义的行内元素（span/font/small…）只会把子节点原样铺开，
		// 唯一的例外是它们**自己声明了加粗**。
		if plainInline(n.DataAtom) {
			b.plainInlineNode(n)
			return
		}
		switch n.DataAtom {
		case atom.Br:
			b.br()

		case atom.A:
			b.anchor(n)

		case atom.Img:
			if s := renderImage(n); s != "" {
				b.raw(s)
			}

		case atom.Button:
			b.raw(renderButtonish(n))

		case atom.Input:
			b.raw(renderInput(n))

		case atom.B, atom.Strong:
			b.wrapChildren(n, "**", "**")

		case atom.I, atom.Em, atom.Cite, atom.Var:
			b.wrapChildren(n, "*", "*")

		case atom.Del, atom.S, atom.Strike:
			b.wrapChildren(n, "~~", "~~")

		case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
			b.raw(codeSpan(textContent(n)))

		default:
			// 块级元素被塞进了行内上下文（HTML 不规范时很常见），
			// 按块处理，别丢内容。
			b.node(n)
		}
	}
}

// plainInlineNode 渲染一个「无语义的行内元素」。
//
// 默认只是把子节点原样铺开 —— 这里不能改用 b.blocks(n)，它收尾时会把当前
// 这块提前 flush 掉，`<p>甲<span>乙</span>丙</p>` 于是变成「甲乙」和「丙」
// 两块，正文里凭空多一个换行。
//
// 唯一的例外是**自己声明了加粗**的元素：
//
//	<span style="font-weight:bold">注意</span>
//
// 邮件模板里大量这么写，原因和标题一样 —— 客户端会把 <b> 自带的样式
// strip 掉，发件人只能自己把粗体写进 style。不认它，正文里的强调就全平了。
func (b *blockBuilder) plainInlineNode(n *html.Node) {
	if elemBold(n) && !b.noMark {
		b.wrapChildren(n, "**", "**")
		return
	}
	b.inlineChildren(n)
}

// plainInline 是「没有语义、只会把子节点原样铺开」的行内元素。
//
// 样式加粗只在这一类元素上生效：别的元素要么自己就会包标记（<b>、<i>），
// 要么有更具体的处理（<code> 走行内代码、<a> 走链接）。
func plainInline(a atom.Atom) bool {
	switch a {
	case atom.U, atom.Span, atom.Font, atom.Small, atom.Big, atom.Mark,
		atom.Sub, atom.Sup, atom.Time, atom.Abbr, atom.Q, atom.Label,
		atom.Ins, atom.Bdi, atom.Bdo, atom.Ruby, atom.Rt, atom.Rp,
		atom.Wbr, atom.Data, atom.Output, atom.Progress, atom.Meter:
		return true
	}
	return false
}

// elemBold 判断元素是不是**自己声明了**加粗。认两种写法：
//
//   - style="font-weight:bold|bolder|600…900"（现代写法，占绝大多数）
//   - <font weight="bold">（HTML3 的老写法，老模板里还有）
func elemBold(n *html.Node) bool {
	if styleBold(n) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(attr(n, "weight")), "bold")
}

// inlineChildren 把子节点按行内内容写进当前块。
func (b *blockBuilder) inlineChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.inlineNode(c)
	}
}

// wrapChildren 渲染子节点，再用 open/close 把新写进去的那一段包起来。
func (b *blockBuilder) wrapChildren(n *html.Node, open, close string) {
	// 这一段的上层已经决定了不加标记（标题的内容、按钮的标签、链接文字），
	// 一律原样铺开。
	//
	// 必须在这里挡，不能只在 plainInlineNode / boldBlock 里挡：
	// `<button><b>去支付</b></button>` 走的是 <b> 那条路，而按钮外面还要
	// 包一层 **，两处一叠就是 `****去支付****` —— 渲染器认不出四个星号，
	// 原样显示给用户。`<button><i>去支付</i></button>` 同理，出三个星号。
	if b.noMark {
		b.inlineChildren(n)
		return
	}

	// 已经在星号里了，别再包一层。* 和 ** 都算。
	//
	// `<span style="font-weight:bold">甲<b>乙</b></span>`：外层包一次、
	// <b> 再包一次的话写出来是 `**甲**乙****`；`<b><i>甲</i></b>` 则是
	// `***甲***` —— 两种渲染器都认不出，会原样显示星号。内容照样渲染，
	// 只是内层不加标记。
	if open == "**" || open == "*" {
		if b.starDepth > 0 {
			b.inlineChildren(n)
			return
		}
		b.starDepth++
		defer func() { b.starDepth-- }()
	}

	start := b.inline.Len()
	b.inlineChildren(n)
	b.applyRange(start, func(lead, core, trail string) string {
		if core == "" {
			// 没有可标记的内容。硬加标记会写出 `****`，那不但是噪音，
			// 还会被 Markdown 当成一对真的强调标记。
			return lead + trail
		}
		if strings.Contains(core, "\n") {
			// 标记**不跨行**。渲染器是逐行解析的（parseMarkdown 按行切），
			// 跨行的 `**` 两边配不上，会原样显示出来。宁可少一处加粗，
			// 也不要往正文里漏标记。
			return lead + core + trail
		}
		return lead + open + core + close + trail
	})
}

// applyRange 把缓冲区里自 start 起新写进去的那段取出来，用 transform 的
// 返回值替换（首尾空白单独交给 transform，由它决定放标记里面还是外面）。
//
// 为什么要「先写进当前缓冲区、回头再包标记」，而不是给子元素另开一个
// 缓冲区渲染完再拼进来 —— 因为空白折叠的状态是整块共用的。
// `<b>你好 </b>世界` 里那个空格属于整段文字流：另开缓冲区的话，它在
// 收尾时会被当成「元素末尾的空白」丢掉，结果就成了 `**你好**世界`。
//
// 空白又必须挪到标记外面：`**你好 **` 收尾的 ** 被空格顶开，加粗直接失效。
func (b *blockBuilder) applyRange(start int, transform func(lead, core, trail string) string) {
	s := b.inline.String()
	if start > len(s) {
		// 中途有块级子元素把缓冲区 flush 掉了（`<b>` 里塞 `<div>` 这种
		// 不规范写法），标记已经无处可加。认了 —— 内容没丢就够了。
		return
	}
	lead, core, trail := splitSurroundingSpace(s[start:])
	b.inline.Reset()
	b.inline.WriteString(s[:start])
	b.inline.WriteString(transform(lead, core, trail))
}

// renderInline 把一棵子树渲染成一个行内 Markdown 片段。
//
// 它是给「只要一段文字、首尾空白本来就打算不要」的场合用的：标题、
// 按钮和 input 的标签。这些调用方拿到结果都会 TrimSpace，所以另开一个
// 缓冲区不共享空白折叠状态没关系。
//
// 反过来，凡是要把结果包进 ** / []() 里的，都该走 blockBuilder.wrapChildren
// 或 blockBuilder.anchor —— 那两条路共用同一套空白状态，元素末尾的空格
// 才不会在收尾时被当垃圾丢掉。
func renderInline(n *html.Node) string {
	// noMark：这里的结果要么放进标题（本来就渲染成粗体），要么被
	// renderButtonish / renderInput 再包一层 ** —— 内层再加标记是冗余，
	// 叠起来还会写出 ****。
	b := &blockBuilder{sep: "\n", noMark: true}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.inlineNode(c)
	}
	b.flush()
	return strings.Join(b.out, "\n")
}

// wrapInline 用标记包住一段已经成型的行内内容，把内容外侧的空白挪到
// 标记外面。
//
// 这一步不能省：`**你好 **` 收尾的 ** 被空格顶开，加粗当场失效。
//
// 注意它和 blockBuilder.wrapChildren 的分工：包住「元素子树」用
// wrapChildren（能共用空白折叠状态），包住「一段现成的字符串」——
// 比如按钮从 value / 文字里取出来的标签 —— 才用这个。
func wrapInline(s, marker string) string {
	lead, core, trail := splitSurroundingSpace(s)
	if core == "" {
		// 内容全是空白，没有可标记的东西，原样返回。
		return s
	}
	return lead + marker + core + marker + trail
}

// splitSurroundingSpace 把字符串拆成「前导空白 + 内容 + 尾随空白」。
func splitSurroundingSpace(s string) (lead, core, trail string) {
	core = strings.TrimLeft(s, " \t\n")
	lead = s[:len(s)-len(core)]
	trail = core[len(strings.TrimRight(core, " \t\n")):]
	core = strings.TrimRight(core, " \t\n")
	return lead, core, trail
}

// ---- 链接与按钮 ----

// imagePlaceholder 是图片没有 alt 也没有 title 时的占位文字。
//
// 它是**生成端**的兜底，两条图片路径共用：包在链接里的图片按钮（anchor）
// 和裸露的图片（renderImage）。留空的话产出的 Markdown 会变成
// ![图片](https://…) → 改前是 ![](https://…)，标签是空的、地址还在，
// 任何读取这份 Markdown 的地方都会看到一长串带用户标识的 URL。
const imagePlaceholder = "图片"

// anchor 把 <a> 转成 Markdown 链接。
func (b *blockBuilder) anchor(n *html.Node) {
	href := strings.TrimSpace(attr(n, "href"))

	// 空 href、"#"、"javascript:" 点了没反应，做成链接是骗人。
	//
	// 但别急着放弃：按钮式的 <a> 常把真地址塞在 data-href 或者
	// onclick="window.open('…')" 里，href 只是个占位。同一个地址写在
	// <button> 上我们认、写在 <a> 上就不认，没有道理。所以走同一套兜底。
	if href == "" || isJunkHref(href) {
		href = linkURL(n)
	}
	if href == "" {
		b.inlineChildren(n)
		return
	}

	// 图片按钮：<a href="..."><img src="btn.png" alt="查看订单"></a>
	//
	// 终端里显示不了图，而这张图的 alt 正是服务商为「图挂了」准备的
	// 替代文字，也就是按钮上写的字。所以链接文字取 alt ——
	// 写成 [![查看订单](btn.png)](href) 那种嵌套的话，在终端里等于
	// 把链接藏起来了，用户看不到「这里能点」。
	if img := soleImage(n); img != nil {
		// 链接文字取图片的标签。imageLabel 保证不会返回空串 ——
		// 链接文字是用户唯一能看出「这里能点」的线索，宁可写个占位，
		// 也别留空。
		label := imageLabel(img)
		if label == imagePlaceholder {
			// 图自己没标签，看看外面那个链接上有没有。
			// `<a aria-label="查看订单"><img src="btn.png"></a>` 是常见写法：
			// 文字是给读屏软件准备的，正好是我们要的那句话。
			if s := labelAttr(n); s != "" {
				label = s
			}
		}
		b.raw("[" + EscapeMarkdown(label) + "](" + href + ")")
		return
	}

	start := b.inline.Len()
	b.inlineChildren(n)
	// 用未转义的原文来比对「文字是不是就是地址」，转义过的字符串里
	// 下划线和星号都成了 \_ \*，再比就对不上了。
	text := strings.TrimSpace(textContent(n))

	b.applyRange(start, func(lead, core, trail string) string {
		switch {
		case core == "":
			// 链接里没文字（空 <a> 或只有空白）。先看 aria-label / title：
			// 那通常是发件人写给读屏软件的按钮名（「查看订单」），比整条
			// 地址可读得多，而且本地就有。
			if s := labelAttr(n); s != "" {
				return lead + "[" + EscapeMarkdown(s) + "](" + href + ")" + trail
			}
			// 都没有就把地址本身显出来。总比整条链接消失好。
			return lead + "[" + href + "](" + href + ")" + trail

		case text == href && autolinkSafe(href):
			// 文字就是地址本身，用自动链接更短，也避免 [x](x) 这种重复。
			return lead + "<" + href + ">" + trail

		default:
			if addr, ok := mailtoAddr(href, text); ok && autolinkSafe(addr) {
				// 邮件地址的自动链接写裸地址：<mailto:a@b.c> 在屏幕上
				// 会连 "mailto:" 一起显示出来。
				return lead + "<" + addr + ">" + trail
			}
			return lead + "[" + core + "](" + href + ")" + trail
		}
	})
}

// mailtoAddr 在 href 是 mailto: 且正文写的就是那个地址时，返回裸地址。
func mailtoAddr(href, text string) (string, bool) {
	const scheme = "mailto:"
	if len(href) < len(scheme) || !strings.EqualFold(href[:len(scheme)], scheme) {
		return "", false
	}
	addr := href[len(scheme):]
	if addr == "" || strings.TrimSpace(text) != addr {
		return "", false
	}
	return addr, true
}

// autolinkSafe 判断地址能不能安全地放进 < ... > 自动链接里。
//
// 自动链接里再出现 <、> 或空白就会当场截断，链接目标会变成另一个东西。
// 那种情况下宁可退回 [文字](地址) 这种稳妥写法。
func autolinkSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '<' || r == '>' || isHTMLSpace(r) {
			return false
		}
	}
	return true
}

// renderImage 把 <img> 转成 Markdown 图片。
func renderImage(n *html.Node) string {
	if isTrackingPixel(n) {
		// 追踪像素。每一封营销邮件末尾都挂着这么一张 1×1 的图，
		// 它的 src 是一长串带用户标识的 URL —— 铺进聊天流里就是
		// 一坨噪音，而且没有任何信息量。丢掉。
		return ""
	}
	src := strings.TrimSpace(attr(n, "src"))
	if src == "" {
		return ""
	}
	return "![" + EscapeMarkdown(imageLabel(n)) + "](" + src + ")"
}

// renderButtonish 处理 <button>。
//
// <button> 自己没有地址，地址通常在它所在的表单上。先看有没有什么属性
// 能当链接用（formaction / data-href / onclick 里的 URL），有就转成链接，
// 没有就加粗 —— 加粗至少能让它在正文里显出来「这里本来是个按钮」。
func renderButtonish(n *html.Node) string {
	// 标签有两个来源，**转义状态不同**，所以不能算完再统一转义一遍：
	//   - renderInline 的产出**已经**转义过（它走的是 blockBuilder.text）
	//   - value / aria-label / title 是原始属性值，还没转义
	// 原先两种情况都丢给 EscapeMarkdown，于是文字里本来就有 * 的按钮会多出
	// 一层反斜杠：`<button>a*b</button>` 显示成 a\*b 而不是 a*b。
	text := strings.TrimSpace(renderInline(n))
	if text == "" {
		label := strings.TrimSpace(attr(n, "value"))
		if label == "" {
			label = labelAttr(n)
		}
		text = EscapeMarkdown(label)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	if url := linkURL(n); url != "" {
		return "[" + text + "](" + url + ")"
	}
	return wrapInline(text, "**")
}

// renderInput 处理 <input>。
//
// 只处理按钮型的（submit / button / reset / image）——
// text / checkbox / hidden 这些在终端里没有可读的等价物，
// 而且往往是表单脚手架，不是正文。
func renderInput(n *html.Node) string {
	typ := strings.ToLower(strings.TrimSpace(attr(n, "type")))
	switch typ {
	case "submit", "button", "reset", "image":
	default:
		return ""
	}

	text := strings.TrimSpace(attr(n, "value"))
	if text == "" {
		text = strings.TrimSpace(attr(n, "alt"))
	}
	if text == "" {
		text = labelAttr(n)
	}
	if text == "" {
		return ""
	}

	if url := linkURL(n); url != "" {
		return "[" + EscapeMarkdown(text) + "](" + url + ")"
	}
	return wrapInline(EscapeMarkdown(text), "**")
}

// linkURL 从一个元素上挖出能当链接用的地址 —— 给 href 不给力的元素用。
//
// 没有标准的写法：HTML5 有 formaction，各家邮件服务商会用 data-href，
// 也有直接写在 onclick 里的。按可靠性从高到低试。
//
// 叫 linkURL 而不是 buttonURL，是因为 <button> 和「href 是占位符的 <a>」
// 走的是同一条路 —— 名字里带上 button 会让人以为只有按钮能用它。
func linkURL(n *html.Node) string {
	for _, key := range []string{"formaction", "data-href", "data-url", "data-link"} {
		if v := strings.TrimSpace(attr(n, key)); v != "" && !isJunkHref(v) {
			return v
		}
	}
	// onclick="location.href='https://…'" / window.open('https://…')
	if v := extractURL(attr(n, "onclick")); v != "" {
		return v
	}
	return ""
}

// isJunkHref 判断一个 href 是不是「点了没反应」的那种。
func isJunkHref(href string) bool {
	h := strings.ToLower(strings.TrimSpace(href))
	return h == "" || h == "#" || strings.HasPrefix(h, "javascript:")
}

// urlSchemes 是我们在 JS 片段里认的地址前缀。
var urlSchemes = []string{"https://", "http://", "mailto:"}

// extractURL 从一段 JavaScript 片段里挖出地址。
//
// 只做「找前缀再取到分隔符」这一件事，不解析 JS。onclick 里塞地址的
// 写法五花八门（location.href='…'、window.open("…")），但共同点是
// 地址本身是个正常的 URL，所以按前缀找足够准，而且不会因为解析器
// 的 bug 把奇怪的字符串当地址。
func extractURL(s string) string {
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)

	best := -1
	for _, scheme := range urlSchemes {
		if i := strings.Index(lower, scheme); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}

	rest := s[best:]
	// 地址到引号、空白、右括号或分号为止 —— 都是 JS 里常见的收尾字符。
	end := len(rest)
	for i, r := range rest {
		if r == '"' || r == '\'' || r == ')' || r == ';' || r == '\\' || isHTMLSpace(r) || r == '>' {
			end = i
			break
		}
	}
	u := strings.TrimSpace(rest[:end])
	if isJunkHref(u) {
		return ""
	}
	return u
}

// ---- 代码 ----

// preText 取 <pre> 里的原始文字，保留换行和缩进。
func preText(n *html.Node) string {
	s := textContent(n)
	// <pre> 里的首尾换行是排版习惯（`<pre>\ncode\n</pre>`），不是内容。
	return strings.Trim(s, "\n")
}

// codeFence 把代码裹进围栏代码块。
//
// 围栏长度要按内容里最长的一串反引号来定：内容里如果有 ``` ，
// 用三个反引号围栏会被当场截断，代码块就漏了。
func codeFence(code string) string {
	fence := strings.Repeat("`", maxBacktickRun(code)+1)
	if len(fence) < 3 {
		fence = "```"
	}
	if !strings.HasSuffix(code, "\n") {
		code += "\n"
	}
	return fence + "\n" + code + fence
}

// codeSpan 把一段文字裹成行内代码。
func codeSpan(s string) string {
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "`") {
		return "`" + s + "`"
	}
	// 内容里有反引号时，用更长的围栏，并在两侧各留一个空格 ——
	// 这是 Markdown 规范的写法，空格不会被算进代码内容。
	return "`` " + s + " ``"
}

// maxBacktickRun 返回最长的一串连续反引号长度。
func maxBacktickRun(s string) int {
	best, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			if cur > best {
				best = cur
			}
			continue
		}
		cur = 0
	}
	return best
}

// ---- 列表 ----

// renderList 渲染 <ul>/<ol>。
func (b *blockBuilder) renderList(n *html.Node, ordered bool) string {
	return b.renderListAt(n, ordered, 0)
}

// renderListAt 渲染一层列表。depth 只用来缩进嵌套项。
func (b *blockBuilder) renderListAt(n *html.Node, ordered bool, depth int) string {
	indent := strings.Repeat("  ", depth)
	var lines []string
	idx := 0

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Li || skipElement(c) {
			continue
		}
		idx++

		marker := "- "
		if ordered {
			// Markdown 的 ol 会自动编号，所以这里固定写 1. ——
			// 写真实序号反而容易和渲染器的编号冲突（比如列表被
			// 中间一行打断成两个列表时）。
			marker = "1. "
		}

		body, nested := b.listItemParts(c)
		if body == "" && nested == "" {
			continue
		}
		if body == "" {
			// 空的 <li> 只为了装下一层列表，标记照写，保持层级不丢。
			lines = append(lines, indent+strings.TrimRight(marker, " "))
		} else {
			lines = append(lines, indent+marker+indentContinuation(body, indent+marker))
		}
		if nested != "" {
			lines = append(lines, nested)
		}
	}

	return strings.Join(lines, "\n")
}

// listItemParts 把一个 <li> 拆成「自己的内容」和「嵌套的子列表」。
//
// 拆开是必要的：嵌套列表必须顶格另起，不能挂在标记后面，
// 否则 Markdown 会把子列表当成父项文字的一部分。
func (b *blockBuilder) listItemParts(li *html.Node) (body, nested string) {
	sub := b.sub("\n")
	var nestedParts []string

	for c := li.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.DataAtom == atom.Ul || c.DataAtom == atom.Ol) {
			if s := sub.renderListAt(c, c.DataAtom == atom.Ol, 0); s != "" {
				nestedParts = append(nestedParts, s)
			}
			continue
		}
		sub.node(c)
	}
	sub.flush()

	// 子列表按当前层级的缩进量再缩一层。
	if len(nestedParts) > 0 {
		nested = indentLines(strings.Join(nestedParts, "\n"), "  ")
	}
	return strings.Join(sub.out, "\n"), nested
}

// indentContinuation 给多行内容的后续行补上缩进，让它对齐在标记之后。
func indentContinuation(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// indentLines 给每一行加前缀。
func indentLines(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		lines[i] = pad + ln
	}
	return strings.Join(lines, "\n")
}

// prefixLines 给每一行加前缀（引用用）。
func prefixLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			// 空行不加前缀，否则引用块里会拖出 "> " 这种空引用行。
			lines[i] = ">"
			continue
		}
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// ---- 表格 ----

// renderTable 渲染 <table>。
//
// 这里要判断一件事：这张表是「数据表」还是「布局表」。
//
// 营销邮件几乎全用 <table> 排版（对齐、分栏、有些邮件客户端只认它），
// 把它当数据表转成 Markdown 表格会得到一堆 1 列的怪东西。反过来，
// 真正的数据表（有 <th>、行列数整齐）转成 Markdown 表格才好读。
//
// 判据：有 <th> 行，或者各行单元格数一致且 ≥2 列 —— 当成数据表。
func (b *blockBuilder) renderTable(n *html.Node) string {
	rows := collectRows(n)
	if len(rows) == 0 {
		return ""
	}

	// 布局表：不是数据表就按「一行一块」铺开，别再套表格语法。
	if !looksLikeDataTable(rows) {
		return b.renderLayoutTable(rows)
	}

	var lines []string
	cols := 0
	for i, row := range rows {
		cells := make([]string, 0, len(row))
		for _, cell := range row {
			cells = append(cells, b.cellText(cell))
		}
		if len(cells) > cols {
			cols = len(cells)
		}
		if i == 0 {
			// 第一行当表头，顺手把分隔行补上。
			lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
			sep := make([]string, len(cells))
			for j := range sep {
				sep[j] = "---"
			}
			lines = append(lines, "| "+strings.Join(sep, " | ")+" |")
			continue
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

// renderLayoutTable 把布局表铺平：每个非空单元格自己占一块。
//
// 单元格必须按「块」渲染，不能压成一行 —— 营销邮件惯用
//
//	<table><tr><td>整封信</td></tr></table>
//
// 把正文整个包起来。压成一行的话，标题、段落、列表、段内换行会全糊在
// 同一行里，可读性直接归零。压成一行是「数据表单元格」的规矩
// （Markdown 表格的一行就是一个单元格），不是布局表的。
func (b *blockBuilder) renderLayoutTable(rows [][]*html.Node) string {
	var parts []string
	for _, row := range rows {
		var cells []string
		for _, cell := range row {
			if s := b.cellBlocks(cell); strings.TrimSpace(s) != "" {
				cells = append(cells, s)
			}
		}
		if len(cells) > 0 {
			parts = append(parts, strings.Join(cells, "\n\n"))
		}
	}
	return strings.Join(parts, "\n\n")
}

// cellBlocks 按块渲染一个单元格，保留它内部的段落/标题/列表结构。
func (b *blockBuilder) cellBlocks(cell *html.Node) string {
	// 单元格自己可能就是标题（`<td style="font-size:28px">`）——
	// 这正是营销邮件表达标题的标准写法，得在这里再判一次。
	if lv := b.headingLevel(cell); lv > 0 {
		if inner := renderInline(cell); inner != "" {
			return strings.Repeat("#", lv) + " " + inner
		}
	}
	sub := b.sub("\n\n")
	sub.blocks(cell)
	return sub.String()
}

// collectRows 按文档顺序收集所有 <tr>，每个 <tr> 里收集 <td>/<th>。
func collectRows(n *html.Node) [][]*html.Node {
	var rows [][]*html.Node
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && x.DataAtom == atom.Tr {
			var cells []*html.Node
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
					cells = append(cells, c)
				}
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return // 不再往里走，嵌套表格会被内层自己处理
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return rows
}

// looksLikeDataTable 判断这张表该不该转成 Markdown 表格。
func looksLikeDataTable(rows [][]*html.Node) bool {
	if len(rows) < 2 {
		return false
	}
	if hasHeaderCell(rows[0]) {
		return true
	}
	want := len(rows[0])
	if want < 2 {
		return false
	}
	for _, row := range rows {
		if len(row) != want {
			return false
		}
	}
	return true
}

func hasHeaderCell(row []*html.Node) bool {
	for _, c := range row {
		if c.DataAtom == atom.Th {
			return true
		}
	}
	return false
}

// cellText 渲染一个单元格的内容，并把 | 转义掉（否则会切错列）。
//
// 这里**不**做标题推断：数据表的单元格会被压成一行，标题语法
// （"#" 开头）在表格行里不成立，写出来只会是一段带 # 的普通文字。
func (b *blockBuilder) cellText(cell *html.Node) string {
	sub := b.sub("\n")
	sub.inTable = true
	sub.blocks(cell)
	s := sub.String()
	// 单元格里不能有换行，压成一行。
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return b.boldBlock(cell, s)
}

// ---- 标题推断 ----

// headingLevel 从一个元素的**行内样式**推断它在原邮件里的标题级别（1-6），
// 判断不出来返回 0。
//
// # 为什么需要这个
//
// 营销邮件和通知邮件几乎不用 <h1>-<h6>。原因是邮件客户端（Outlook 尤其）
// 会把标题标签自带的样式 strip 掉，发件人为了「所见即所得」，宁可把字号
// 直接写进 <td>/<div>/<p> 的 style：
//
//	<td style="font-family:Arial;font-size:28px;font-weight:bold">
//	  我们的网络正在提升 yzjtian.cn 的速度和安全性
//	</td>
//
// 不认识这一条，整封信的层级就在转换时全塌了 —— 用户看到的是一串同样
// 大小的段落，分不出哪句是小节标题、哪句是正文。这不是「少了个 # 号」，
// 是**读不下去**。
//
// # 判据
//
//  1. 元素得是排版单元（td/div/p/caption/center/dt/dd），且内容是纯行内
//     内容 —— 里面有块级子元素、链接、按钮或图片的，是版式块或按钮，
//     不是标题。按钮尤其容易长得像标题：大字号、加粗、白字，全都符合，
//     只有「里面是个 <a>」这一条能把它挡开。
//  2. 它必须**自己声明** font-size。继承来的不算 —— 标题一定会自己写
//     字号（否则邮件客户端一改默认字号，标题就没了），跟着父级继承的
//     则是正文。
//  3. 字号要明显大于正文基准，按倍数分档。
func (b *blockBuilder) headingLevel(n *html.Node) int {
	switch n.DataAtom {
	case atom.Td, atom.Th, atom.Div, atom.P, atom.Caption, atom.Center,
		atom.Dt, atom.Dd:
	default:
		return 0
	}
	if b.base <= 0 {
		return 0
	}
	size := styleFontSizePx(n)
	if size <= b.base {
		return 0
	}
	if !headingContent(n) {
		return 0
	}

	// 分档用「占正文字号的倍数」而不是绝对 px：正文是 14px 还是 16px，
	// 会整体平移这封信里的所有字号，绝对分档换一封信就全错。
	switch ratio := size / b.base; {
	case ratio >= 1.9:
		return 1
	case ratio >= 1.65:
		return 2
	case ratio >= 1.4:
		return 3
	case ratio >= 1.2:
		// 这一档和正文只差一点，只有加粗才分得出是标题 ——
		// 16px 的正文段落和 16px 的加粗小标题，光看字号分不开。
		if styleBold(n) {
			return 4
		}
	}
	return 0
}

// headingContent 判断一个元素的内容像不像标题：得有文字，且整棵子树里
// 没有块级元素、链接、按钮或图片。
func headingContent(n *html.Node) bool {
	hasText := false

	var walk func(*html.Node) bool
	walk = func(x *html.Node) bool {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				if strings.TrimSpace(c.Data) != "" {
					hasText = true
				}
			case html.ElementNode:
				if skipElement(c) {
					continue
				}
				if isBlockElement(c) {
					return false
				}
				switch c.DataAtom {
				case atom.A, atom.Button, atom.Img, atom.Input:
					return false
				}
				if !walk(c) {
					return false
				}
			}
		}
		return true
	}
	return walk(n) && hasText
}

// isBlockElement 是「会另起一块」的标签。
//
// 只用来判断标题内容是否纯粹，所以列的是 HTML 里真正按块排版的标签，
// 不必和 node() 的分派一一对应。
func isBlockElement(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Div, atom.P, atom.Table, atom.Tr, atom.Td, atom.Th,
		atom.Tbody, atom.Thead, atom.Tfoot, atom.Ul, atom.Ol, atom.Li,
		atom.Section, atom.Article, atom.Header, atom.Footer, atom.Main,
		atom.Aside, atom.Nav, atom.Blockquote, atom.H1, atom.H2, atom.H3,
		atom.H4, atom.H5, atom.H6, atom.Hr, atom.Pre, atom.Form,
		atom.Fieldset, atom.Address, atom.Details, atom.Summary,
		atom.Figure, atom.Figcaption, atom.Center, atom.Dl:
		return true
	}
	return false
}

// styleFontSizePx 取元素行内样式里的 font-size，换算成 px。
//
// 认不出来一律返回 0：不是 px 的（em / % / rem / 关键字）不猜 ——
// 猜错会把正文当标题铺成 # 号，那比漏掉一个标题糟得多。
func styleFontSizePx(n *html.Node) float64 {
	v := strings.ToLower(strings.TrimSpace(styleValue(attr(n, "style"), "font-size")))
	if !strings.HasSuffix(v, "px") {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(v, "px")), 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}

// styleBold 判断元素有没有显式声明加粗。
func styleBold(n *html.Node) bool {
	switch strings.ToLower(strings.TrimSpace(styleValue(attr(n, "style"), "font-weight"))) {
	case "bold", "bolder", "600", "700", "800", "900":
		return true
	}
	return false
}

// styleValue 从 style 属性里取一条声明的值。
func styleValue(style, key string) string {
	for _, decl := range strings.Split(style, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// baseFontSize 估出这封信的「正文字号」。
//
// 按**文字量**加权，而不是按元素个数：正文是长篇，标题是短句，所以
// 「哪种字号承载的字最多，它就是正文字号」。按元素个数会栽在一封短通知上 ——
// 三个小节标题（20px）加一个短正文（14px），个数是 3:1，标题反客为主
// 成了基准，于是正文全部「不达标」、整封信反过来更乱。
//
// 每个元素只按「最内层声明」计入一次：外层 div 和它内层的 p 都写了
// font-size 时，字是内层那个字号渲染的，外层不该重复计入文字量。
//
// 次数并列时取较小的那个，而且这个 tie-break 必须是确定性的：map 的
// 遍历顺序是随机的，写成「先遇到谁算谁」会让同一封信两次转换给出不同
// 结果，判据也就没法写了。
func baseFontSize(root *html.Node) float64 {
	weights := map[float64]int{}
	collectFontWeights(root, weights)

	best, bestWeight := 0.0, 0
	for size, weight := range weights {
		if weight > bestWeight || (weight == bestWeight && size < best) {
			best, bestWeight = size, weight
		}
	}
	return best
}

// collectFontWeights 累积「每种字号承载了多少字」，返回这棵子树里有没有
// 元素自己声明了 font-size。
//
// 后序遍历，一遍算完：只有**最内层**的声明才计入文字量 —— 外层 div 和
// 它内层的 p 都写了 font-size 时，字是内层那个字号渲染的，外层不该重复算。
func collectFontWeights(n *html.Node, weights map[float64]int) bool {
	if n.Type != html.ElementNode || skipElement(n) {
		return false
	}

	inner := false
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if collectFontWeights(c, weights) {
			inner = true
		}
	}
	if inner {
		// 更内层已经有人声明了，文字量记在那些元素上。
		return true
	}
	if size := styleFontSizePx(n); size > 0 {
		weights[size] += ownTextWeight(n)
		return true
	}
	return false
}

// ownTextWeight 数一个元素的子树里有多少字（0 表示这棵子树没有可读文字）。
func ownTextWeight(n *html.Node) int {
	count := 0
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch x.Type {
		case html.TextNode:
			count += len([]rune(strings.TrimSpace(x.Data)))
		case html.ElementNode:
			if skipElement(x) {
				return
			}
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(n)
	return count
}

// ---- 元素筛选 ----

// skipElement 判断这个元素是否整棵子树都该丢掉。
func skipElement(n *html.Node) bool {
	if isHidden(n) {
		return true
	}
	switch n.DataAtom {
	case atom.Style, atom.Script, atom.Title, atom.Meta, atom.Link, atom.Base,
		atom.Noscript, atom.Iframe, atom.Object, atom.Embed, atom.Svg,
		atom.Canvas, atom.Template, atom.Head, atom.Audio, atom.Video,
		atom.Picture, atom.Source, atom.Track, atom.Map, atom.Area,
		atom.Param, atom.Col, atom.Colgroup, atom.Frame, atom.Frameset,
		atom.Applet, atom.Basefont, atom.Keygen:
		return true
	}
	return false
}

// isHidden 判断元素是不是被 CSS 藏起来了。
//
// 主要是为了干掉 preheader —— 营销邮件和通知邮件习惯在正文最前面塞一段
// 「在浏览器中查看 / 这是一封系统通知」的预览文字，用 display:none 藏起来。
// 在网页里看不见，但如果直接转 Markdown，它就成了正文的第一段，
// 每封信都顶着这么一句，非常烦人。
func isHidden(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if _, ok := attrOK(n, "hidden"); ok {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(attr(n, "aria-hidden")), "true") {
		// aria-hidden 只是给读屏软件看的。但邮件里用它藏 preheader
		// 的写法也存在，而且内容同样是「给人看的时候不该看」的。
		return true
	}
	style := strings.ToLower(attr(n, "style"))
	style = strings.ReplaceAll(style, " ", "")
	return strings.Contains(style, "display:none") ||
		strings.Contains(style, "visibility:hidden")
}

// isTrackingPixel 判断这是不是追踪像素。
func isTrackingPixel(n *html.Node) bool {
	for _, k := range []string{"width", "height"} {
		v := strings.TrimSpace(strings.ToLower(attr(n, k)))
		v = strings.TrimSuffix(v, "px")
		if v == "0" || v == "1" {
			return true
		}
	}
	return false
}

// imageLabel 取一张图的文字标签：alt 优先，其次 aria-label / title，
// 都没有就是占位文字。
//
// 终端里显示不了图，这个标签是这张图**唯一**能留下的信息。返回空串的话
// renderImage 会写出 ![](src) —— 图没了，地址却留了下来。
func imageLabel(img *html.Node) string {
	if s := strings.TrimSpace(attr(img, "alt")); s != "" {
		return s
	}
	if s := labelAttr(img); s != "" {
		return s
	}
	return imagePlaceholder
}

// labelAttr 取元素上「给人读的标签」：aria-label 优先，其次 title。
//
// 这一段刻意只用**本地就在手上**的信息。邮件模板为了「图挂了也读得出来、
// 读屏软件也读得出来」，常把按钮文字写在 aria-label 里；有些模板甚至只写
// aria-label，正文一个字都没有。同一件事还有第三种做法 —— 去抓目标网页的
// <title> —— 那要发网络请求、会通知对方「我读了这封信」（正是
// isTrackingPixel 在挡的那种事）、还会被短链重定向绕开。同样的信息
// 本地就有，没有理由上网拿。
func labelAttr(n *html.Node) string {
	if s := strings.TrimSpace(attr(n, "aria-label")); s != "" {
		return s
	}
	return strings.TrimSpace(attr(n, "title"))
}

// soleImage 判断 n 的子树里是否只有一个有意义的节点，且它是 <img>。
//
// 判据刻意收紧：一旦还有别的文字内容，就不能当成图片按钮 ——
// 那种情况下链接文字该用文字，而不是图的 alt。
func soleImage(n *html.Node) *html.Node {
	var img *html.Node
	only := true

	var walk func(*html.Node) bool
	walk = func(x *html.Node) bool {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				if strings.TrimSpace(c.Data) != "" {
					only = false
					return false
				}
			case html.CommentNode:
			case html.ElementNode:
				if skipElement(c) {
					continue
				}
				if c.DataAtom == atom.Img {
					if img != nil {
						only = false
						return false
					}
					img = c
					continue
				}
				if !walk(c) {
					return false
				}
			}
		}
		return true
	}
	walk(n)

	if !only || img == nil {
		return nil
	}
	return img
}

// ---- 文字与转义 ----

// textContent 拼接子树里的所有文字（含换行和缩进，不做空白折叠）。
func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch x.Type {
		case html.TextNode:
			sb.WriteString(x.Data)
		case html.ElementNode:
			if skipElement(x) {
				return
			}
			if x.DataAtom == atom.Br {
				sb.WriteString("\n")
				return
			}
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(n)
	return sb.String()
}

// attr 取属性值，不存在返回空串。
func attr(n *html.Node, key string) string {
	v, _ := attrOK(n, key)
	return v
}

func attrOK(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

// isHTMLSpace 是 HTML 意义上的空白。
//
// 多出来的那两个是真实邮件里常见的坑：&nbsp;（U+00A0）会被 html 包
// 解码成不换行空格，而缩进排版里经常用它。不归一到普通空格的话，
// 转换结果里会夹着一堆看起来像空格、但正则和 Markdown 都不认的字符。
func isHTMLSpace(r rune) bool {
	switch r {
	case '\u00a0', '\u2007', '\u202f', '\u2009', '\u2002', '\u2003', '\u200b':
		return true
	}
	return unicode.IsSpace(r)
}

// EscapeMarkdown 转义一段纯文本，让它在 Markdown 里原样显示。
//
// 给「把已有的纯文本塞进 Markdown」这种场合用。
func EscapeMarkdown(s string) string {
	var sb strings.Builder
	atLineStart := true
	for _, r := range s {
		if r == '\n' {
			sb.WriteByte('\n')
			atLineStart = true
			continue
		}
		sb.WriteString(escapeMarkdownRune(r, atLineStart, false))
		if !isHTMLSpace(r) {
			atLineStart = false
		}
	}
	return sb.String()
}

// escapeMarkdownRune 转义单个字符。
//
// atLineStart 决定要不要转义那几个「只在行首才是标记」的字符 ——
// 正文中间的 - 和 + 是标点，转义了反而难看。
// inTable 为真时 | 必须转义，否则会把表格的列切错。
func escapeMarkdownRune(r rune, atLineStart, inTable bool) string {
	switch r {
	// 这几个在哪儿都是标记，一律转义。
	case '\\', '`', '*', '_', '[', ']', '<':
		return "\\" + string(r)
	case '|':
		if inTable {
			return "\\|"
		}
		return "|"
	}

	if !atLineStart {
		return string(r)
	}

	// 行首才有意义的块级标记。
	switch r {
	case '#', '>', '-', '+', '=', '~':
		return "\\" + string(r)
	}
	return string(r)
}

// tidyMarkdown 收尾：压掉多余空行，去掉首尾空白。
func tidyMarkdown(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			blanks++
			// 最多留一个空行。三个以上连续换行在邮件里几乎一定是
			// 布局残留，铺到聊天流里就是一大片空白。
			if blanks <= 1 && len(out) > 0 {
				out = append(out, "")
			}
			continue
		}
		blanks = 0
		out = append(out, strings.TrimRight(ln, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
