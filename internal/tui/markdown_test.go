package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

// forceColor 让 lipgloss 在非 TTY 的测试环境里也真的输出转义序列。
//
// 不设的话 lipgloss 会把颜色档位降到 Ascii，渲染结果是纯文本，
// 「样式生效了没有」这类断言就全成了空壳。
func forceColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// plainText 去掉 SGR / OSC 序列，只留用户肉眼看到的字符。
func plainText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) {
			if s[i+1] == ']' { // OSC：ESC ] … ESC \
				j := i + 2
				for j < len(s) && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				i = j + 2
				continue
			}
			if s[i+1] == '[' { // CSI：ESC [ … 终止符
				j := i + 2
				for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
					j++
				}
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func joined(t *testing.T, src string, width int) string {
	t.Helper()
	return strings.Join(renderMarkdown(src, width), "\n")
}

// ---- 核心：标记要被渲染掉，而不是原样露出 ----

// 这条是用户报的那个问题的正面判据：CLI 里之前把 Markdown 源码直接显示
// 出来了（`**重要**`、`[文字](url)` 原样可见）。渲染器的存在意义就是
// 让这些标记**不再出现在屏幕上**。
func TestRenderMarkdown_NoRawMarkersRemain(t *testing.T) {
	forceColor(t)

	src := "**重要**：请在 *周五* 前确认 ~~尽快~~。\n\n" +
		"请点 [确认订阅](https://example.com/c) 完成。\n\n" +
		"## 小标题\n\n" +
		"- 第一项\n- 第二项\n\n" +
		"> 引来的一句话\n\n" +
		"行内 `code` 和 <https://example.com/x>\n"

	plain := plainText(joined(t, src, 80))

	// 标记本身不该出现在屏幕上。
	for _, marker := range []string{"**", "~~", "](", "## ", "`"} {
		if strings.Contains(plain, marker) {
			t.Errorf("标记 %q 原样露出来了，说明没被渲染:\n%s", marker, plain)
		}
	}
	// 文字本身要留下 —— 别为了去掉标记把内容也吃了。
	for _, want := range []string{"重要", "周五", "尽快", "确认订阅", "小标题", "第一项", "引来的一句话", "code"} {
		if !strings.Contains(plain, want) {
			t.Errorf("内容 %q 丢了:\n%s", want, plain)
		}
	}
}

// 样式位要真的变成转义序列，而不只是把标记删掉。
//
// 只删标记的话，`**重要**` 会退化成普通的「重要」—— 看起来像渲染了，
// 其实丢掉了「这里是重点」这个信息。
func TestRenderMarkdown_StylesAreEmitted(t *testing.T) {
	forceColor(t)

	got := joined(t, "**粗** *斜* ~~删~~ `码`", 80)

	for _, tc := range []struct {
		sgr, what string
	}{
		{"\x1b[1m", "粗体"},
		{"\x1b[3m", "斜体"},
		{"\x1b[9m", "删除线"},
	} {
		if !strings.Contains(got, tc.sgr) {
			t.Errorf("%s 的转义序列 (%q) 没输出:\n%q", tc.what, tc.sgr, got)
		}
	}
}

// ---- ⚠️ 链接：OSC 8 必须完整，且文字不能带属性 ----

// 这条守着 styles.go 里 hyperlink 记的那个坑。
//
// 现象是链接「看着完全正常，但点不动」：OSC 8 起始序列后面紧跟的必须是
// 显示文字，中间一旦插进任何 SGR（bubbletea 给每个字符单独套的那种），
// 终端就认不出这是链接了。
//
// 而**粗体包着链接**是最容易踩的组合：如果实现图省事把 bold 和 link
// 两层样式叠在一起渲染，就会踩中。
func TestRenderMarkdown_LinkNeverCarriesAttributes(t *testing.T) {
	forceColor(t)

	const url = "https://example.com/confirm?t=abc"
	got := joined(t, "**[确认订阅]("+url+")**", 80)

	// 这条断言把链接的**完整字节形态**钉死：
	//
	//	\x1b[38;5;45m          前缀恰好是纯前景色（styleLink），一个属性都没有
	//	\x1b]8;;<url>\x1b\\    OSC 8 起始序列
	//	确认订阅                 紧接着就是显示文字，中间不许插 SGR
	//	\x1b]8;;\x1b\\          OSC 8 结束序列
	//
	// 钉这么死是有意的。链接有三种退化，只查其中一种会漏掉另两种：
	//
	//  1. 中间插进 SGR —— 起始序列后面紧跟的不是文字，终端认不出链接，
	//     表现是「看着完全正常但点不动」；
	//  2. 前缀多出属性（\x1b[4;38;5;45m 这类）—— lipgloss 会给 URL 的每个
	//     字符单独套 SGR，把 OSC 8 切碎（见 styles.go 里 hyperlink 的注释）；
	//  3. 前缀整个丢掉 —— 比如图省事复用 markdownStyle，而它不认 attrLink，
	//     于是返回一个空样式，链接退化成普通文字、颜色全无。
	//
	// 其中第 3 种最隐蔽：屏幕上的链接**还是能点的**，只是不再有颜色，
	// 所以「点得动吗」这类判据一个都不会红。
	want := "\x1b[38;5;45m" + "\x1b]8;;" + url + "\x1b\\确认订阅\x1b]8;;\x1b\\"
	if !strings.Contains(got, want) {
		t.Errorf("链接的渲染字节变了。\n got  %q\nwant 包含 %q\n\n"+
			"如果只是有意改链接颜色：把 want 开头的 \\x1b[38;5;45m 换成新颜色，\n"+
			"（并且 styleLink 也要跟着改）。\n"+
			"如果是因为给链接加了样式属性，请撤回 —— 先读 styles.go 里\n"+
			"hyperlink 的注释：属性会让 OSC 8 被逐字符的 SGR 切碎，链接点不动。",
			got, want)
	}
}

// 链接文字不该被加粗，即使源里写着 **[文字](url)**。
//
// 这条守的是**设计规矩**（链接只带前景色），而不是某个一踩就炸的 bug：
// 实测 Bold / Italic / Reverse 都不会破坏 OSC 8，只有 Underline 会
// （见 styles.go 里 hyperlink 的注释）。之所以仍然不让它叠，是因为
// 「给链接叠样式」这个动作本身危险 —— 一旦有人顺手把 Underline 也叠上
// （下划线恰好是链接最自然的装饰），链接就当场变成「看着正常但点不动」。
// 规矩留得越窄，越不容易踩。
func TestRenderMarkdown_BoldAroundLinkDoesNotBoldTheLinkText(t *testing.T) {
	forceColor(t)

	got := joined(t, "**[点我](https://example.com/x)**", 80)

	// 这一行里除了链接没有别的东西，所以整行都不该出现任何属性类 SGR。
	//
	// 只查 "\x1b[1m点我" 是不够的：把 markdownStyle 叠到链接上时，SGR 会
	// 出现在 OSC 8 序列**之前**，那种拼法照样漏网。按序列查才守得住。
	for _, tc := range []struct{ seq, what string }{
		{"\x1b[1m", "粗体"},
		{"\x1b[3m", "斜体"},
		{"\x1b[4", "下划线"},
		{"\x1b[9m", "删除线"},
	} {
		if strings.Contains(got, tc.seq) {
			t.Errorf("链接文字被上了%s (%q) —— 这会让 OSC 8 被 SGR 切碎:\n%q",
				tc.what, tc.seq, got)
		}
	}
}

// ---- 折行：ANSI 不能污染宽度计算（本文件最重要的一条）----

// 折行的宽度必须按**显示宽度**算，不能按 rune 数 —— 不认识 ANSI 的话，
// 转义序列里的每个字符都会被当成占 1 列的普通字符，折行位置就算错
// （`[38;5;45m` 这一串会被算成 10 列）。
//
// 所以渲染器的管线顺序是「先折行、后上色」。这条判据直接盯着结果：
// 不管源里有多少样式，每一行的**可见宽度**都不能超过给定的宽度。
//
// 如果哪天有人把顺序反过来（先上色，再交给按 rune 算宽度的折行），
// 这条会变红。
func TestRenderMarkdown_StyledLinesFitWidth(t *testing.T) {
	forceColor(t)

	src := "**粗体** 和 [一个很长的链接](https://example.com/a/very/long/path/that/keeps/going) " +
		"以及中文内容也需要正确地折行，`行内代码` 也要能折。"

	for _, width := range []int{20, 30, 41, 60, 79} {
		for i, line := range renderMarkdown(src, width) {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("宽 %d：第 %d 行可见宽度 %d，超了 %d 列\n%q",
					width, i, got, got-width, plainText(line))
			}
		}
	}
}

// 中文按显示宽度折行 —— 一个汉字占两列，按 rune 数折会撑破一倍。
func TestRenderMarkdown_WrapsByDisplayWidthNotRuneCount(t *testing.T) {
	forceColor(t)

	// 20 个汉字 = 40 列。宽度给 10，应该折成 4 行左右（每行 5 字）。
	got := renderMarkdown("这是一段没有空格的中文内容用来验证折行", 10)

	for i, line := range got {
		if w := lipgloss.Width(line); w > 10 {
			t.Errorf("第 %d 行宽 %d 列，超过 10 列：%q", i, w, plainText(line))
		}
	}
	if len(got) < 3 {
		t.Errorf("折成了 %d 行，看着没按宽度折：%q", len(got), got)
	}
}

// 每一行的转义序列必须完整 —— 折行不能把某个序列从中间折断。
//
// 这条补的是 StyledLinesFitWidth 的漏洞。那条盯的是「行太宽」，而
// 「先上色再折行」的真实症状恰好相反：按 rune 算宽度的折行会把转义序列的
// 字符当成占 1 列的普通字符算进宽度，于是折得**更早**、行更短 ——
// 宽度断言根本不会红。但折点会落在转义序列内部，把它拦腰切断，
// 终端上就是一段乱码。
//
// 所以「行宽没超」和「序列没被切断」是两回事，得分开盯。
func TestRenderMarkdown_NeverSplitsEscapeSequences(t *testing.T) {
	forceColor(t)

	src := "**粗体** 和 [一个很长的链接](https://example.com/a/very/long/path/that/keeps/going) " +
		"还有中文内容一起折行 `code` 结束"

	for _, width := range []int{10, 20, 30, 50} {
		for i, line := range renderMarkdown(src, width) {
			if !escapesBalanced(line) {
				t.Errorf("宽 %d：第 %d 行的转义序列被切断了（折行插进了序列内部）:\n%q",
					width, i, line)
			}
		}
	}
}

// escapesBalanced 检查一行的转义序列都是完整的：CSI 有终止符，OSC 有 ST。
func escapesBalanced(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			continue
		}
		if i+1 >= len(s) {
			return false // ESC 落在行尾，序列被截断
		}
		switch s[i+1] {
		case '[': // CSI：ESC [ 参数… 终止符(@-~)
			j := i + 2
			for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
				j++
			}
			if j >= len(s) {
				return false
			}
			i = j
		case ']': // OSC：ESC ] … ESC \
			j := i + 2
			for j+1 < len(s) && !(s[j] == 0x1b && s[j+1] == '\\') {
				j++
			}
			if j+1 >= len(s) {
				return false
			}
			i = j + 1
		default:
			return false // 认不出来的序列
		}
	}
	return true
}

// ---- 块级结构 ----

func TestRenderMarkdown_ListsGetBulletsAndRealNumbers(t *testing.T) {
	forceColor(t)

	plain := plainText(joined(t, "- 甲\n- 乙\n\n1. 一\n1. 二\n1. 三", 80))

	if !strings.Contains(plain, "• 甲") || !strings.Contains(plain, "• 乙") {
		t.Errorf("无序列表没变成项目符号:\n%s", plain)
	}
	// 生成器一律写 "1. "，渲染层要自己编号 —— 三个全显示「1.」就白渲染了。
	for _, want := range []string{"1. 一", "2. 二", "3. 三"} {
		if !strings.Contains(plain, want) {
			t.Errorf("有序列表编号不对，缺 %q:\n%s", want, plain)
		}
	}
}

// 列表项折行后要悬挂缩进对齐到文字，否则分不清哪行属于哪一项。
func TestRenderMarkdown_ListContinuationIsIndented(t *testing.T) {
	forceColor(t)

	got := renderMarkdown("- 一个很长很长的列表项内容需要折行", 12)
	if len(got) < 2 {
		t.Fatalf("没折行，测不到悬挂缩进：%q", got)
	}
	// "• " 占两列，续行要缩两个空格。
	if !strings.HasPrefix(plainText(got[1]), "  ") {
		t.Errorf("续行没有悬挂缩进，和「顶格」分不开:\n%q", plainText(got[1]))
	}
}

func TestRenderMarkdown_HeadingDropsHashes(t *testing.T) {
	forceColor(t)

	got := renderMarkdown("## 小标题", 80)
	if len(got) != 1 {
		t.Fatalf("想要 1 行，得到 %d 行", len(got))
	}
	if strings.Contains(plainText(got[0]), "#") {
		t.Errorf("# 号没去掉，这不是渲染而是露出源码：%q", plainText(got[0]))
	}
	// 标题本身要加粗 —— 去掉 # 之后总得有个东西表示「这是标题」。
	if !hasSGRParam(got[0], "1") {
		t.Errorf("标题没加粗，看着和正文一样：%q", got[0])
	}
}

// hasSGRParam 报告 s 里的 SGR 序列有没有 code 这个参数。
//
// **为什么不能写 strings.Contains(s, "\x1b[1m")**：lipgloss 会把同一个
// 样式的参数合并成一条序列，加了前景色就成了 "\x1b[1;38;5;141m"。逐字节
// 匹配只认「加粗恰好是唯一参数」这一种形态，别处一上色就假红 ——
// 假红和假绿一样贵。而且参数要**分段精确比**，不能 Contains：256 色的
// 色号 141 里也有个 "1"。
func hasSGRParam(s, code string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] >= '0' && s[j] <= '9' || j < len(s) && (s[j] == ';' || s[j] == ':') {
			j++
		}
		if j >= len(s) || s[j] != 'm' {
			continue // 不是 SGR（可能是别的 CSI），跳过
		}
		for _, p := range strings.FieldsFunc(s[i+2:j], func(r rune) bool {
			return r == ';' || r == ':'
		}) {
			if p == code {
				return true
			}
		}
		i = j
	}
	return false
}

// sgrParams 把一行里所有 SGR 序列的参数收成一个集合（顺序无关）。
// 用它比较「两种标题的样式不一样」，而不是去比具体色号 —— 配色是设计
// 决定，判据要钉的是「分级确实生效」这个事实。
func sgrParams(s string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i+2 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] >= '0' && s[j] <= '9' || j < len(s) && (s[j] == ';' || s[j] == ':') {
			j++
		}
		if j >= len(s) || s[j] != 'm' {
			continue
		}
		for _, p := range strings.FieldsFunc(s[i+2:j], func(r rune) bool {
			return r == ';' || r == ':'
		}) {
			out[p] = true
		}
		i = j
	}
	return out
}

func TestRenderMarkdown_HeadingTiersDiffer(t *testing.T) {
	forceColor(t)

	// 同一封信里三种层级的标题，渲染出来必须**看得出区别**。
	// 否则「智能分析标题等级」解析出来也没用 —— 屏幕上还是分不出。
	lines := renderMarkdown("# 一级\n\n### 三级\n\n##### 五级", 80)
	if len(lines) != 5 {
		t.Fatalf("想要 5 行（三个标题 + 两个空行），得到 %d 行：%q", len(lines), lines)
	}
	strong, mid, plain := lines[0], lines[2], lines[4]

	for _, l := range []string{strong, mid, plain} {
		if !hasSGRParam(l, "1") {
			t.Errorf("标题没加粗：%q", l)
		}
	}
	if sameSGR(strong, mid) {
		t.Errorf("一级和三级标题样式一模一样，分级没生效：\n一级 %q\n三级 %q", strong, mid)
	}
	if sameSGR(mid, plain) {
		t.Errorf("三级和五级标题样式一模一样，分级没生效：\n三级 %q\n五级 %q", mid, plain)
	}
}

func sameSGR(a, b string) bool {
	pa, pb := sgrParams(a), sgrParams(b)
	if len(pa) != len(pb) {
		return false
	}
	for k := range pa {
		if !pb[k] {
			return false
		}
	}
	return true
}

// TestRenderMarkdown_LinkTextMarkersAreEaten 盯的是「链接文字里的行内标记」。
//
// 生成端会产出 [**立即购买**](url) 和 [`npm i`](url) —— 分别是
// `<a href><b>…</b></a>` 和 `<a href><code>…</code></a>` 的真实形状，
// 在营销邮件里到处都是。链接文字如果整段当成一个 span，这些标记不会被
// 解析，`**` 和反引号会原样显示给用户。
//
// 判据直接看屏幕：标记字符一个都不许留下。
func TestRenderMarkdown_LinkTextMarkersAreEaten(t *testing.T) {
	forceColor(t)

	for _, tc := range []struct{ src, want string }{
		{"[**立即购买**](https://ex.com/buy)", "立即购买"},
		{"[`npm i clichat`](https://ex.com/i)", "npm i clichat"},
		{"[~~旧价格~~](https://ex.com/old)", "旧价格"},
		{"[**甲**和`乙`](https://ex.com/mix)", "甲和乙"},
	} {
		lines := renderMarkdown(tc.src, 80)
		if len(lines) == 0 {
			t.Fatalf("%q 渲染出了 0 行", tc.src)
		}
		got := plainText(lines[0])
		if got != tc.want {
			t.Errorf("%q 渲染成 %q，想要 %q（标记没被吃掉）", tc.src, got, tc.want)
		}
	}
}

func TestRenderMarkdown_RuleBecomesLine(t *testing.T) {
	forceColor(t)

	got := plainText(joined(t, "上\n\n---\n\n下", 80))
	if strings.Contains(got, "---") {
		t.Errorf("分隔线还是原样的 --- :\n%s", got)
	}
	if !strings.Contains(got, "─") {
		t.Errorf("分隔线没画成横线:\n%s", got)
	}
}

// 分隔线要铺满聊天区的可用宽度。
//
// 原先这里硬限 40 列（min(width, 40)），在宽终端里一条分隔线只是一小截
// 短线悬在左边，看着像没渲染完 —— 而它本来是对方在正文里画的一条分隔线，
// 视觉上就该横贯聊天区（用户提的第 5 条）。
func TestRenderMarkdown_RuleFillsWidth(t *testing.T) {
	forceColor(t)

	// 40 是原来那个上限，特意跨过去；30 在限内，一起看免得改过头。
	for _, width := range []int{30, 60, 120} {
		plain := plainText(joined(t, "上\n\n---\n\n下", width))

		rule := ""
		for _, ln := range strings.Split(plain, "\n") {
			if strings.Contains(ln, "─") {
				rule = ln
				break
			}
		}
		if rule == "" {
			t.Fatalf("width=%d: 没找到分隔线:\n%s", width, plain)
		}
		if got := textWidth(rule); got != width {
			t.Errorf("width=%d: 分隔线画了 %d 列，应该铺满整个可用宽度", width, got)
		}
	}
}

func TestRenderMarkdown_QuoteKeepsMarker(t *testing.T) {
	forceColor(t)

	plain := plainText(joined(t, "> 引来的话", 80))
	if !strings.Contains(plain, "> 引来的话") {
		t.Errorf("引用的标记丢了，看不出这是引文:\n%s", plain)
	}
}

// ---- 转义：用户的字面字符要还原，不能当标记吃 ----

// 生成器把正文里的 \ * _ [ ] < 都转义了（见 htmlmd.go 的 escapeMarkdownRune），
// 渲染器必须把它们还原成字面文字 —— 不然正文写着 2*3 会变成斜体，
// [限时] 会变成一个不存在的链接。
func TestRenderMarkdown_EscapesBecomeLiteralText(t *testing.T) {
	forceColor(t)

	plain := plainText(joined(t, `正文里的 2\*3 和 \[限时\] 以及 a\_b`, 80))

	for _, want := range []string{"2*3", "[限时]", "a_b"} {
		if !strings.Contains(plain, want) {
			t.Errorf("转义没还原，缺 %q：%s", want, plain)
		}
	}
	if strings.Contains(plain, `\`) {
		t.Errorf("反斜杠没被吃掉：%s", plain)
	}
}

// ---- 链接的解析边界 ----

// 地址里的括号要配平着找闭合 —— 生成器不转义 ( 和 )，而维基百科这类
// 地址里括号很常见。取第一个 ')' 会把地址截断。
func TestRenderMarkdown_LinkURLMayContainParens(t *testing.T) {
	forceColor(t)

	const url = "https://en.wikipedia.org/wiki/Foo_(bar)"
	got := joined(t, "[条目]("+url+")", 80)

	if !strings.Contains(got, "\x1b]8;;"+url+"\x1b\\") {
		t.Errorf("含括号的地址被截断了，OSC 8 里的地址不完整:\n%q", got)
	}
	if strings.Contains(plainText(got), ")") {
		t.Errorf("地址的右括号漏到了正文里：%q", plainText(got))
	}
}

func TestRenderMarkdown_AutolinkIsClickable(t *testing.T) {
	forceColor(t)

	const url = "https://example.com/x"
	got := joined(t, "<"+url+">", 80)

	if !strings.Contains(got, "\x1b]8;;"+url+"\x1b\\") {
		t.Errorf("自动链接没变成可点的:\n%q", got)
	}
	if strings.Contains(plainText(got), "<") || strings.Contains(plainText(got), ">") {
		t.Errorf("尖括号没去掉：%q", plainText(got))
	}
}

// ---- 代码 ----

func TestRenderMarkdown_CodeSpanAndFence(t *testing.T) {
	forceColor(t)

	// 围栏代码块里的内容**原样保留** —— 里面的 * 和 [ 是代码，不是标记。
	src := "行内 `x = *p` 结束\n\n```\nif a[0] == 1 {\n}\n```\n"
	plain := plainText(joined(t, src, 80))

	if !strings.Contains(plain, "x = *p") {
		t.Errorf("行内代码丢了内容：%s", plain)
	}
	if !strings.Contains(plain, "a[0] == 1") {
		t.Errorf("代码块里的内容被当成标记解析了：%s", plain)
	}
	if strings.Contains(plain, "```") {
		t.Errorf("围栏本身不该显示出来：%s", plain)
	}
}

// 生成器在行内代码内容含反引号时，会把围栏加长一个反引号并在两侧各留一个
// 空格（那是语法的一部分，见 htmlmd.go 的 codeSpan）。那两个空格必须去掉，
// 不能当成内容。
func TestRenderMarkdown_CodeSpanWithBackticks(t *testing.T) {
	forceColor(t)

	plain := plainText(joined(t, "`` a`b ``", 80))
	if !strings.Contains(plain, "a`b") {
		t.Errorf("含反引号的行内代码没解析对：%q", plain)
	}
	if strings.Contains(plain, " a`b ") {
		t.Errorf("语法用的填充空格没去掉：%q", plain)
	}
}

// ---- 空行与稳定性 ----

func TestRenderMarkdown_KeepsBlankLines(t *testing.T) {
	forceColor(t)

	got := renderMarkdown("甲\n\n乙", 80)
	if len(got) != 3 {
		t.Fatalf("想要 3 行（含一个空行），得到 %d 行：%q", len(got), got)
	}
	if got[1] != "" {
		t.Errorf("中间那行应该是空的，实际 %q", got[1])
	}
}

// 畸形输入不能让渲染器崩掉或死循环。
func TestRenderMarkdown_SurvivesMalformedInput(t *testing.T) {
	forceColor(t)

	for _, src := range []string{
		"**没有闭合",
		"*没闭合",
		"~~没闭合",
		"`没闭合",
		"[文字](没有右括号",
		"<https://example.com/没有右尖括号",
		"```\n没闭合的代码块",
		"",
		"\n\n\n",
		"#",
		">",
		"- ",
		"***",
		"[][]()",
	} {
		// 不崩、不死循环就算过；内容怎么显示是次要的。
		if got := renderMarkdown(src, 20); len(got) == 0 && src != "" {
			t.Errorf("输入 %q 渲染出了 0 行", src)
		}
	}
}

// 零宽/极窄宽度下不能死循环（wrapLine 里「至少吃掉一个字符」那步守着）。
func TestRenderMarkdown_SurvivesNarrowWidth(t *testing.T) {
	forceColor(t)

	for _, w := range []int{0, 1, 2, 5} {
		if got := renderMarkdown("**粗体** 内容", w); len(got) == 0 {
			t.Errorf("宽度 %d 渲染出 0 行", w)
		}
	}
}

// ---- 折行质量 ----

// 折行不能把英文单词劈成两半（wonderful → 「wo」/「nderful」）。
//
// 判据绑的是**词完整**这件事，不去管具体折在哪一列：每一个由连续字母组成的
// 词都必须完整地落在某一行里。绑列数的话，任何一次「折点微调」都会变成假红。
func TestRenderMarkdown_DoesNotSplitEnglishWords(t *testing.T) {
	forceColor(t)

	src := "英文单词 hello wonderful world 一起折行，宽度给紧一点 extraordinary"
	words := regexp.MustCompile(`[A-Za-z]{2,}`).FindAllString(src, -1)
	if len(words) < 4 {
		t.Fatalf("判据自己坏了：只从源里找出 %d 个词", len(words))
	}

	for _, width := range []int{16, 24, 30, 40} {
		var lines []string
		for _, row := range renderMarkdown(src, width) {
			lines = append(lines, plainText(row))
		}
		for _, w := range words {
			whole := false
			for _, l := range lines {
				if strings.Contains(l, w) {
					whole = true
					break
				}
			}
			if !whole {
				t.Errorf("宽 %d：词 %q 被折行劈开了\n%s", width, w, strings.Join(lines, "\n"))
			}
		}
	}
}

// 一个词比整行还宽时（长 URL 文字），只能硬断 —— 但一个字符都不能丢，
// 也不能因此产出超宽的行。
func TestRenderMarkdown_OverlongWordBreaksWithoutLosingText(t *testing.T) {
	forceColor(t)

	// 没有空格，所以「丢掉行首空格」那个优化不会吃掉任何东西，
	// 拼回去必须与原文逐字节相同。
	const word = "https://example.com/a/very/long/path/that/never/ends/at/all"

	var got strings.Builder
	for _, row := range renderMarkdown(word, 10) {
		plain := plainText(row)
		if w := lipgloss.Width(plain); w > 10 {
			t.Errorf("超宽行 %q (宽 %d > 10)", plain, w)
		}
		got.WriteString(plain)
	}
	if got.String() != word {
		t.Errorf("硬断后内容对不上：\n got %q\nwant %q", got.String(), word)
	}
}

// 续行必须和首行的**内容**左对齐：前缀占几列，续行就缩几格。
//
// 这条盯着两个具体的坑：
//
//   - 前缀宽度用错尺子。列表前缀是「• 」，而 • 是 East Asian Ambiguous ——
//     go-runewidth 在中文 locale 下算 2 列、lipgloss 算 1 列，用错了续行就
//     多缩一格（首行内容在第 3 列，续行却在第 4 列）。
//   - 折行点正好落在空格上时，那个空格被甩到下一行开头、叠在缩进上，
//     于是缩进在「2 格」和「3 格」之间跳，看着参差。
func TestRenderMarkdown_ContinuationAlignsWithContentStart(t *testing.T) {
	forceColor(t)

	const src = "- 一个列表项长到需要折行，英文单词 hello wonderful world 也在里面"

	// 前缀宽度用 lipgloss 量（= 布局用的那把尺子），别抄实现里的公式 ——
	// 抄了的话实现和判据一起错，判据就成了空壳。
	hang := lipgloss.Width("• ")
	if hang != 2 {
		t.Fatalf("前提变了：列表前缀「• 」量出来 %d 列", hang)
	}

	for _, width := range []int{24, 40} {
		rows := renderMarkdown(src, width)
		if len(rows) < 2 {
			t.Fatalf("宽 %d：只折出 %d 行，这条判据量不到续行", width, len(rows))
		}
		for i, row := range rows[1:] {
			plain := plainText(row)
			body, ok := strings.CutPrefix(plain, strings.Repeat(" ", hang))
			if !ok {
				t.Errorf("宽 %d：第 %d 行缩进不足 %d 格：%q", width, i+1, hang, plain)
				continue
			}
			if strings.HasPrefix(body, " ") {
				t.Errorf("宽 %d：第 %d 行多缩了 —— 折行点上的空格被甩到了行首：%q",
					width, i+1, plain)
			}
		}
	}
}

// 折行的结果不能随 locale 变。
//
// • —— … ① 这些是 East Asian Ambiguous 字符。go-runewidth 用一个叫
// EastAsianWidth 的开关决定它们算 1 列还是 2 列，而这个开关默认**取自
// locale** —— 本机中文 locale 下是 true，英文 Linux 上通常是 false。
// 于是用错尺子的话，同一份代码在两台机器上折行位置不同。
//
// 直接测「扳一下那个开关，结果必须一模一样」，比断言某个具体宽度可靠：
// 它不依赖跑测试的机器恰好是什么 locale（而那正是这条 bug 的麻烦之处 ——
// 在本机绿、在 CI 上红，或者反过来）。
//
// 顺带把「行宽不超」也钉在这里：Ambiguous 字符若被算成 0 列，
// 就会折出超过给定宽度的行。
func TestRenderMarkdown_AmbiguousRunesIgnoredByLocale(t *testing.T) {
	forceColor(t)

	const src = "这行有 East Asian Ambiguous 字符：• —— … ① 和中文混在一起，宽度要算对"

	prev := runewidth.DefaultCondition.EastAsianWidth
	defer func() { runewidth.DefaultCondition.EastAsianWidth = prev }()

	for _, width := range []int{17, 24, 33, 40} {
		runewidth.DefaultCondition.EastAsianWidth = false
		off := strings.Join(renderMarkdown(src, width), "\n")
		runewidth.DefaultCondition.EastAsianWidth = true
		on := strings.Join(renderMarkdown(src, width), "\n")

		if off != on {
			t.Errorf("宽 %d：折行结果随 EastAsianWidth 变了 —— 宽度尺子认了 locale\n"+
				"EastAsianWidth=false:\n%s\n\nEastAsianWidth=true:\n%s", width, off, on)
		}

		for i, row := range renderMarkdown(src, width) {
			plain := plainText(row)
			if got := lipgloss.Width(plain); got > width {
				t.Errorf("宽 %d：第 %d 行量出来 %d 列，超了 %d\n%q",
					width, i, got, got-width, plain)
			}
		}
	}
}
