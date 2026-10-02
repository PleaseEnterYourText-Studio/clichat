package tui

// 图片、表格，以及「生成端产出 → 渲染端不留标记」那条跨包不变量。
//
// 分成单独一个文件，是因为这一组判据针对的是同一类失误：**生成端写得出、
// 渲染端认不出**。核心的渲染逻辑判据在 markdown_test.go。

import (
	"strings"
	"testing"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/charmbracelet/lipgloss"
)

// ---- 图片 ----

// 用户报的那个现象：DeepSeek 的服务状态邮件里每行都有一个状态图标
// <img alt="Operational" src="…/ok.svg">，CLI 里显示成了 !perational。
//
// 根因是渲染器不认图片语法：![Operational](url) 被拆成「普通字符 ! +
// 链接 [Operational](url)」，于是屏幕上只剩一个感叹号加一个链接文字。
func TestRenderMarkdown_ImageShowsAltNotMarkup(t *testing.T) {
	forceColor(t)

	const url = "https://status.example.com/ok.svg"
	plain := plainText(joined(t, "状态：![Operational]("+url+")", 80))

	if !strings.Contains(plain, "Operational") {
		t.Errorf("图片的替代文字没显示出来：%q", plain)
	}
	if strings.Contains(plain, "![") || strings.Contains(plain, "](") {
		t.Errorf("图片标记原样露出来了：%q", plain)
	}
	if strings.Contains(plain, url) {
		t.Errorf("图片地址漏进了正文：%q", plain)
	}
	// 这条就是「!perational」本身：那个感叹号是 ![] 的残留。
	if strings.Contains(plain, "!") {
		t.Errorf("感叹号漏出来了 —— 正是 ![] 没被认出来的样子：%q", plain)
	}
}

// 替代文字空着的图片：生成端会兜成 ![图片](url)（见 htmlmd.go 的
// imageLabel），但渲染器**自己也得兜一层** —— 存量邮件里存着改前生成的
// ![](url)，那是图没了、地址还在，而且这类地址通常带用户标识。
func TestRenderMarkdown_EmptyImageAltDoesNotLeakURL(t *testing.T) {
	forceColor(t)

	const url = "https://track.example.com/u/9f3a2b1c"
	plain := plainText(joined(t, "看到了 ![]("+url+") 就这些", 80))

	if strings.Contains(plain, url) || strings.Contains(plain, "9f3a2b1c") {
		t.Errorf("地址漏进了正文：%q", plain)
	}
	if strings.Contains(plain, "!") {
		t.Errorf("空的图片标记没被吃掉：%q", plain)
	}
	if strings.Contains(plain, "track.example.com") {
		t.Errorf("地址的一部分漏进了正文：%q", plain)
	}
	// 兜底文字和两侧的文字都要在：别为了不泄漏把整张图连周围一起吞掉，
	// 那样「这里本来有东西」这条信息也没了。
	if !strings.Contains(plain, "图片") {
		t.Errorf("兜底占位文字没显示：%q", plain)
	}
	if !strings.Contains(plain, "看到了") || !strings.Contains(plain, "就这些") {
		t.Errorf("图片两侧的文字被吃掉了：%q", plain)
	}
}

// 地址里的括号要配平着找闭合 —— 生成端不转义 src 里的 ( 和 )。
// 取第一个 ')' 的话，a_(1).png 会在 a_(1 处断掉，剩下的尾巴漏进正文。
func TestRenderMarkdown_ImageURLMayContainParens(t *testing.T) {
	forceColor(t)

	plain := plainText(joined(t, "![折线图](https://ex.com/a_(1).png) 完", 80))

	if !strings.Contains(plain, "折线图") || !strings.Contains(plain, "完") {
		t.Errorf("图片或它后面的文字被弄丢了：%q", plain)
	}
	if strings.Contains(plain, "png") || strings.Contains(plain, "(1)") {
		t.Errorf("地址被截断后漏进了正文：%q", plain)
	}
}

// 感叹号本身是正文时不能受影响。把 ! 算成「可能是标记的开头」只是让普通
// 文本多切一段，不该改变它显示成什么。
func TestRenderMarkdown_BangWithoutImageStaysLiteral(t *testing.T) {
	forceColor(t)

	for _, src := range []string{
		"注意！这是正文",
		"hello! world",
		"![没有地址]所以不是图片",
		"价格 !!! 很高",
	} {
		plain := plainText(joined(t, src, 80))
		// 去掉空白再比：折行会动空格的位置，但字符本身一个都不能变。
		if strings.Join(strings.Fields(plain), "") !=
			strings.Join(strings.Fields(src), "") {
			t.Errorf("输入 %q 渲染成了 %q", src, plain)
		}
	}
}

// ---- 表格 ----

const sampleTable = "| 组件 | 状态 | 备注 |\n" +
	"| --- | --- | --- |\n" +
	"| API 网关 | 正常 | 无 |\n" +
	"| 数据库集群 | 降级 | 已定位 |\n"

// 表格要真的变成表格：竖线和分隔行都不许露出来，文字一个不少。
func TestRenderMarkdown_TableDropsMarkup(t *testing.T) {
	forceColor(t)

	rows := renderMarkdown(sampleTable, 80)
	plain := make([]string, len(rows))
	for i, r := range rows {
		plain[i] = plainText(r)
	}
	got := strings.Join(plain, "\n")

	for _, bad := range []string{"|", "---"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q 原样露出来了，说明表格没被渲染：\n%s", bad, got)
		}
	}
	for _, want := range []string{
		"组件", "状态", "备注", "API 网关", "正常", "无", "数据库集群", "降级", "已定位",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("该显示的文字 %q 不在屏幕上：\n%s", want, got)
		}
	}
}

// 表格的对齐：每一列的**起始列**在各行里必须一致。
//
// 不能断言「所有行等长」—— 行尾空格会被去掉，短行自然更短。要比的是列
// 起点，那才是「对齐」这件事本身。
//
// 起点按**显示列**算（lipgloss.Width），不能按字节下标：一个中文 3 字节
// 2 列，用字节下标比会把明明对齐的行判成没对齐。这正是本仓库那把
// 「两把尺子」的老坑（见 styles.go 里 cellWidth 的注释）。
func TestRenderMarkdown_TableColumnsLineUp(t *testing.T) {
	forceColor(t)

	rows := renderMarkdown(sampleTable, 80)
	if len(rows) != 5 {
		t.Fatalf("想要 5 行（表头 + 分隔线 + 2 行数据），得到 %d 行：%q", len(rows), rows)
	}
	if !strings.Contains(plainText(rows[1]), "─") {
		t.Errorf("第 2 行应该是表头下面那条横线，实际 %q", plainText(rows[1]))
	}
	// 表头要加粗，否则它和第一条数据行长得一样，读不出哪个是表头。
	if !strings.Contains(rows[0], "\x1b[1m") {
		t.Errorf("表头没加粗（没有 SGR 1）：\n%q", rows[0])
	}

	plain := make([]string, len(rows))
	for i, r := range rows {
		plain[i] = plainText(r)
	}

	// 每一列：表头里的词 + 各行里对应的词。定位要用「本行自己的词」，
	// 不能拿表头那两个词去各行里找 —— 数据行里根本没有它们。
	cols := []struct {
		head string
		body []string
	}{
		{"状态", []string{"正常", "降级"}},
		{"备注", []string{"无", "已定位"}},
	}

	for _, c := range cols {
		idx := strings.Index(plain[0], c.head)
		if idx < 0 {
			t.Fatalf("判据自己坏了：表头 %q 里找不到 %q", plain[0], c.head)
		}
		want := lipgloss.Width(plain[0][:idx])

		for i, cell := range c.body {
			line := plain[i+2] // 跳掉表头和分隔线
			j := strings.Index(line, cell)
			if j < 0 {
				t.Fatalf("第 %d 行里找不到 %q：%q", i+2, cell, line)
			}
			if got := lipgloss.Width(line[:j]); got != want {
				t.Errorf("列 %q 没对齐：表头起于第 %d 列，%q 起于第 %d 列\n%s",
					c.head, want, cell, got, strings.Join(plain, "\n"))
			}
		}
	}
}

// 单元格里可以有行内标记（生成端走的是 cellText → 整段渲染，加粗和链接
// 都留得下来）。这些标记照样要被渲染掉，不能因为「在表格里」就原样露出。
func TestRenderMarkdown_TableCellKeepsInlineMarks(t *testing.T) {
	forceColor(t)

	const link = "https://s.ex.com/a"
	src := "| 组件 | 说明 |\n| --- | --- |\n| API | **紧急**，见 [状态页](" + link + ") |"

	got := strings.Join(renderMarkdown(src, 80), "\n")
	plain := plainText(got)

	if strings.Contains(plain, "**") {
		t.Errorf("单元格里的加粗标记露出来了：%q", plain)
	}
	if strings.Contains(plain, "](") || strings.Contains(plain, "s.ex.com") {
		t.Errorf("单元格里的链接没渲染掉：%q", plain)
	}
	if !strings.Contains(plain, "紧急") || !strings.Contains(plain, "状态页") {
		t.Errorf("单元格里的文字丢了：%q", plain)
	}
	if !strings.Contains(got, "\x1b[1m") {
		t.Errorf("单元格里的加粗没真的加粗（没有 SGR 1）：\n%q", got)
	}
	if !strings.Contains(got, "\x1b]8;;"+link+"\x1b\\") {
		t.Errorf("单元格里的链接没变成可点的：\n%q", got)
	}
}

// 生成端把单元格里的 | 转义成 \|（见 escapeMarkdownRune 的 inTable）。
// 那是**内容**，不是列界 —— 拆列时认错的话这一格会被劈成两格，后面的
// 列全部错位。
func TestRenderMarkdown_TableEscapedPipeIsContent(t *testing.T) {
	forceColor(t)

	src := "| 写法 | 说明 |\n| --- | --- |\n| a \\| b | 转义过的竖线 |"
	plain := plainText(joined(t, src, 80))

	if !strings.Contains(plain, "a | b") {
		t.Errorf("单元格里的转义竖线没还原成字面字符：\n%s", plain)
	}
	if strings.Contains(plain, `\|`) {
		t.Errorf("转义用的反斜杠漏出来了：\n%s", plain)
	}
	if !strings.Contains(plain, "转义过的竖线") {
		t.Errorf("第二格的内容跑掉了，说明被切错列：\n%s", plain)
	}
}

// 只有一行带竖线的句子不是表格。凭据是「下一行是分隔行」，不能只看
// 「这行以 | 开头」—— 否则正文里孤零零一行带竖线的文字会被连竖线一起
// 吞掉、再补一条凭空多出来的表头横线。
//
// 所以判据钉的是「它被当成普通文本处理了」：竖线还在（那是正文里的字面
// 字符，不是标记），而且屏幕上没有冒出表格那条表头横线。
func TestRenderMarkdown_LonePipeLineIsNotATable(t *testing.T) {
	forceColor(t)

	src := "| 这只是一个带竖线的句子 |\n\n后面还有正文"
	rows := renderMarkdown(src, 80)
	plain := plainText(strings.Join(rows, "\n"))

	if !strings.Contains(plain, "| 这只是一个带竖线的句子 |") {
		t.Errorf("竖线被吃掉了，说明这一行被误判成了表格：\n%q", plain)
	}
	if !strings.Contains(plain, "后面还有正文") {
		t.Errorf("正文丢了：\n%q", plain)
	}
	if strings.Contains(plain, "─") {
		t.Errorf("凭空多出一条表头横线，说明这一行被误判成了表格：\n%q", plain)
	}
}

// 表格**不折行**：一折列就对不上了。所以宽了就截断 —— 但无论怎么截，
// 每一行都不能超宽，否则会被外层裁掉、右边界参差。
//
// 极窄时（列宽收到下限还塞不下）降级成「一格一格铺开」，所以这条判据
// 对所有宽度都该成立，不需要「宽度太小就跳过」那种台阶。
func TestRenderMarkdown_TableFitsWidth(t *testing.T) {
	forceColor(t)

	for _, width := range []int{8, 12, 20, 30, 40, 60, 80} {
		rows := renderMarkdown(sampleTable, width)
		if len(rows) == 0 {
			t.Fatalf("宽 %d 渲染出 0 行", width)
		}
		for i, row := range rows {
			plain := plainText(row)
			if got := lipgloss.Width(plain); got > width {
				t.Errorf("宽 %d：第 %d 行量出来 %d 列\n%q", width, i, got, plain)
			}
		}
	}
}

// 极窄时降级也要**一格不丢** —— 降级丢的是对齐，不是内容。
func TestRenderMarkdown_NarrowTableFallsBackWithoutLosingCells(t *testing.T) {
	forceColor(t)

	rows := renderMarkdown(sampleTable, 8)
	got := strings.Join(rows, "\n")
	plain := plainText(got)

	if strings.Contains(plain, "│") || strings.Contains(plain, "─") {
		t.Errorf("这么窄还按表格排版，说明降级没生效：\n%q", plain)
	}
	for _, want := range []string{
		"组件", "状态", "备注", "API", "网关", "正常", "无", "数据库", "集群", "降级", "已定位",
	} {
		if !strings.Contains(strings.Join(strings.Fields(plain), ""), want) {
			t.Errorf("降级后丢了内容 %q：\n%q", want, got)
		}
	}
}

// ---- 生成端 → 渲染端：跨包的不变量 ----

// 这条是这一组里最该存在的一条判据：它不测「渲染器认得哪些语法」，而是测
// **生成端写出的东西渲染端全都认得**。
//
// 2026-10-02 那个 bug 之所以能活下来，就是因为两边各自测自己：mail 包测
// 「我产出了 ![alt](src)」，tui 包测「我认得 **粗体**」，两边都绿，而中间
// 那段（![alt](src) 没人认）是空的。所以这里拿真 HTML 走一遍**真实的**
// 生成器，再走一遍**真实的**渲染器。
//
// 两头都要钉：「不留标记」单独用不够 —— 把正文全删了也能满足它。
func TestRenderMarkdown_NoMarkupLeaksFromHTML(t *testing.T) {
	forceColor(t)

	samples := []struct {
		name string
		html string
		want []string // 必须在屏幕上出现的文字
	}{
		{
			"图片的替代文字要显示",
			`<p>状态：<img src="https://status.ex.com/ok.svg" alt="Operational"></p>`,
			[]string{"状态", "Operational"},
		},
		{
			"没有 alt 的图片不能漏地址",
			`<p>见 <img src="https://track.ex.com/u/9f3a2b"></p>`,
			[]string{"见"},
		},
		{
			"图片按钮",
			`<p><a href="https://ex.com/o"><img src="https://ex.com/b.png" alt="查看订单"></a></p>`,
			[]string{"查看订单"},
		},
		{
			"数据表",
			`<table><tr><th>组件</th><th>状态</th></tr>` +
				`<tr><td>API</td><td>正常</td></tr></table>`,
			[]string{"组件", "状态", "API", "正常"},
		},
		{
			"行内样式",
			`<p><b>粗</b> <i>斜</i> <del>删</del> <code>码</code></p>`,
			[]string{"粗", "斜", "删", "码"},
		},
		{
			"块级元素",
			`<h2>小标题</h2><ul><li>甲</li><li>乙</li></ul>` +
				`<blockquote><p>引文</p></blockquote><hr>`,
			[]string{"小标题", "甲", "乙", "引文"},
		},
		{
			"链接与裸地址",
			`<p><a href="https://ex.com/a">点我</a> 或 https://ex.com/b</p>`,
			[]string{"点我", "https://ex.com/b"},
		},
		{
			"围栏代码块",
			`<pre><code>if a[0] == 1 {}</code></pre>`,
			[]string{"if a[0] == 1 {}"},
		},
		{
			"按钮式元素",
			`<p><button data-href="https://ex.com/go">确认订阅</button></p>`,
			[]string{"确认订阅"},
		},
	}

	// 屏幕上一律不该出现的标记。样本里的文字都刻意不含这些字符，
	// 所以不会出现「正文本来就有 **」那种假红。
	//
	// 这条判据**绑的是「标记的形态」而不是「某种语法被支持了」** ——
	// 以后生成端加了新语法，只要忘了在渲染端认，这里就会红。这正是
	// 想要的：红在「加语法」的那一步，而不是等用户截图来报。
	markers := []string{
		"![", "](", "| ---", "**", "~~", "```", "##", "---",
		"<http", "<mailto:", "\\",
	}

	for _, tc := range samples {
		t.Run(tc.name, func(t *testing.T) {
			md := mail.HTMLToMarkdown(tc.html)
			plain := plainText(joined(t, md, 80))

			for _, m := range markers {
				if strings.Contains(plain, m) {
					t.Errorf("生成端写了 %q，渲染端不认，标记原样露在屏幕上\n"+
						"HTML:     %s\nMarkdown: %q\n屏幕:     %q",
						m, tc.html, md, plain)
				}
			}
			for _, w := range tc.want {
				if !strings.Contains(plain, w) {
					t.Errorf("该显示的文字 %q 不在屏幕上\n"+
						"HTML:     %s\nMarkdown: %q\n屏幕:     %q",
						w, tc.html, md, plain)
				}
			}
		})
	}
}
