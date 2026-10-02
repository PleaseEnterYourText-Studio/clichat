package mail

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// ---- 量宽度 ----

// DisplayWidth 量的是**用户看到的那一行**，所以 Markdown 标记必须被扣掉。
//
// 这条是分栏能不能对齐的全部前提：分栏靠「把一格补到 N 列」实现，而 N
// 是拿 DisplayWidth 量出来的。量错了，补出来的空格就正好错在那些字符上，
// 而且错得很有规律 —— 凡是含链接或加粗的格子都会歪。分栏服务的对象
// （导航栏、页脚）几乎全是链接，所以这条不是边角，是主干。
func TestDisplayWidth_CountsWhatTheUserSees(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"纯中文", "极客商城", 8},
		{"纯 ASCII", "orders", 6},
		{"加粗标记不算", "**张先生**", 6},
		{"斜体标记不算", "*2026-10-01*", 10},
		{"删除线标记不算", "~~旧价格~~", 6},
		{"行内代码标记不算", "`SF1234567890`", 12},
		{"链接只算文字", "[新品](https://ex.com/new)", 4},
		{"链接文字是中文", "[订单中心](https://ex.com/invoice)", 8},
		{"链接文字里还有标记", "[**立即购买**](https://ex.com/buy)", 8},
		{"代码内容含反引号时两侧填充空格不算", "`` a`b ``", 3},
		{"转义符本身不算", `2\*3`, 3},
		{"转义的下划线不算", `a\_b\_c`, 5},

		// 下面几条是「标记识别不能过头」那一侧：认多了会把用户看得见的
		// 字符当成标记吃掉，格子就少算一列。
		{"不成对的星号是字面字符", "2*3", 3},
		{"下划线不是渲染器认的标记", "a_b_c", 5},
		{"下划线加粗也不认", "__重点__", 8},
		{"三个星号是生成端不会产出的形状，按字面算", "***a***", 7},

		// 宽度尺子本身：歧义宽度算一列（和渲染器一致），中文和 emoji 算两列。
		{"歧义宽度字符算一列", "•——…①", 5},
		{"中文算两列", "中", 2},
		{"emoji 算两列", "🎉", 2},
		{"空串", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DisplayWidth(tc.in); got != tc.want {
				t.Errorf("DisplayWidth(%q) = %d，want %d", tc.in, got, tc.want)
			}
		})
	}
}

// ---- 缩进 ----

// indentColsOf 解析一小段 HTML，返回第一个元素算出来的缩进列数。
func indentColsOf(t *testing.T, style string) int {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(`<div style="` + style + `">甲</div>`))
	if err != nil {
		t.Fatalf("解析 style=%q 失败：%v", style, err)
	}
	body := findBody(doc)
	if body == nil {
		t.Fatalf("style=%q 没解析出 body", style)
	}
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return indentCols(c)
		}
	}
	t.Fatalf("style=%q 没解析出元素", style)
	return 0
}

// 缩进只认**单边**留白。
//
// 这是整个缩进功能的判据所在：左右对称的留白（`padding:20px`、
// `padding:0 20px`）是页面边距，不是层次 —— 认了它，营销邮件里那层
// 「padding:20px」的外壳就会把整封信推右两三格，而它表达的只是
// 「正文别贴着窗口边」，终端窗口自己就是那圈边距。
func TestIndentCols_OnlyOneSidedPaddingCounts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		style string
		want  int
	}{
		{"单边 padding-left", "padding-left:40px", 3},
		{"单边 margin-left", "margin-left:32px", 2},
		{"四值简写里左边那个", "padding:0 0 0 20px", 1},
		{"四值简写左大右小", "padding:0 8px 0 40px", 3},

		// 对称的一律 0。
		{"单值简写是对称的", "padding:20px", 0},
		{"两值简写是对称的", "padding:0 20px", 0},
		{"三值简写是对称的", "padding:8px 20px 8px", 0},
		{"四值简写左右相等", "padding:0 20px 0 20px", 0},
		{"margin 同理", "margin:0 40px", 0},

		// 换算与上限。
		{"em 按一比一", "padding-left:2em", 2},
		{"pt 按 12pt 一列", "padding-left:24pt", 2},
		{"不足一列的非零值也算一列", "padding-left:6px", 1},
		{"百分比不认", "padding-left:20%", 0},
		{"关键字不认", "padding-left:auto", 0},
		{"没有留白", "color:red;font-size:14px", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := indentColsOf(t, tc.style); got != tc.want {
				t.Errorf("style=%q 算出来 %d 列，want %d", tc.style, got, tc.want)
			}
		})
	}
}

// leadingSpaces 返回第一行开头有几个空格。
func leadingSpaces(s string) int {
	line, _, _ := strings.Cut(s, "\n")
	return len(line) - len(strings.TrimLeft(line, " "))
}

// 缩进要真的写进正文里，而且**第一行也不能漏**。
//
// 「第一行漏掉」是这个功能最容易掉进去的坑：块与块之间的拼接、收尾的
// 规整都会顺手 TrimSpace，而缩进恰恰就是前导空格。一整块缩进的内容过了
// 那种规整会变成「首行顶格、后面几行缩进」—— 只错第一行，比不缩进还糟。
func TestHTMLToMarkdown_IndentLandsOnEveryLine(t *testing.T) {
	// 两层嵌套：外层 40px（3 列）+ 内层 20px（1 列）。
	got := HTMLToMarkdown(`<div style="padding-left:40px">
		<p>第一层</p>
		<div style="padding-left:20px"><p>第二层</p></div>
	</div>`)

	if !strings.Contains(got, "   第一层") {
		t.Errorf("第一层没缩进 3 列（首行漏了？）：\n%q", got)
	}
	if !strings.Contains(got, "    第二层") {
		t.Errorf("第二层没缩进 4 列：\n%q", got)
	}
	if leadingSpaces(got) == 0 {
		t.Errorf("首行顶格了 —— 收尾的规整把前导空格吃掉了：\n%q", got)
	}
}

// 封顶是**全局**的：一行里所有祖先加起来不超过 maxIndentCols。
//
// 只看当前元素的话每层都觉得自己没超，三层 40px 就能推出 9 列，营销邮件
// 那种七八层外壳的模板能把正文推到右半屏。
func TestHTMLToMarkdown_IndentIsCappedGlobally(t *testing.T) {
	got := HTMLToMarkdown(`<div style="padding-left:40px">
		<div style="padding-left:40px">
			<div style="padding-left:40px"><p>深</p></div>
		</div>
	</div>`)

	if n := leadingSpaces(got); n != maxIndentCols {
		t.Errorf("缩进 %d 列，want %d（封顶没生效）：\n%q", n, maxIndentCols, got)
	}
}

// 封顶要把**格子外面的**祖先也算进去。
//
// 单元格走的是另一条渲染路径（cellBlocks 派生一个子构造器再铺开），
// 派生时如果不继承当前的缩进，格子里的元素就会以为自己在第 0 列，各自
// 往上涨 —— 外层 4 列 + 里层 4 列 = 8 列，早就超过封顶了，而每一层都
// 觉得自己没超。
func TestHTMLToMarkdown_IndentCapCountsAncestorsOutsideTheCell(t *testing.T) {
	got := HTMLToMarkdown(`<div style="padding-left:64px">
		<table><tr><td>
			<div style="padding-left:64px">甲</div>
		</td></tr></table>
	</div>`)

	if n := leadingSpaces(got); n != maxIndentCols {
		t.Errorf("缩进 %d 列，want %d（格子外的祖先没算进封顶）：\n%q",
			n, maxIndentCols, got)
	}
}

// 对称留白不该把正文推右。
func TestHTMLToMarkdown_SymmetricPaddingDoesNotShiftBody(t *testing.T) {
	for _, style := range []string{"padding:20px", "padding:0 20px", "margin:0 24px"} {
		t.Run(style, func(t *testing.T) {
			if got := HTMLToMarkdown(`<div style="` + style + `"><p>正文</p></div>`); got != "正文" {
				t.Errorf("style=%q 把正文挪动了：%q", style, got)
			}
		})
	}
}

// ---- 分栏 ----

// colOf 返回 needle 在 line 里从第几列开始（0 起算）。
//
// ⚠️ 必须按**列**量前缀，不能按字节：strings.Index 给的是字节位置，
// 「极客商城」是 12 字节 / 8 列，按字节算出来的起点永远是 0 —— 判据
// 会变成一条永绿的空壳。
func colOf(t *testing.T, line, needle string) int {
	t.Helper()
	idx := strings.Index(line, needle)
	if idx < 0 {
		t.Fatalf("在 %q 里找不到 %q", line, needle)
	}
	return DisplayWidth(line[:idx])
}

// 布局表里**一行几栏**的排版要并排还原，而不是上下堆叠。
//
// 上一版把一行的单元格用空行堆起来，屏幕上成了「极客商城」+ 空行 +
// 「新品 订单」，而原邮件的意图是一行两栏。这是 HTML 邮件最典型、
// 也是丢失得最彻底的一样东西。
func TestHTMLToMarkdown_NavBarStaysOnOneLine(t *testing.T) {
	got := HTMLToMarkdown(`<table><tr>
		<td>极客商城</td>
		<td><a href="https://ex.com/new">新品</a> <a href="https://ex.com/order">订单</a></td>
	</tr></table>`)

	if strings.Contains(got, "\n") {
		t.Fatalf("两栏被堆成多行了：\n%q", got)
	}

	// 第二栏的起点必须**正好**在第一栏的自然宽度 + 间隔之后。
	//
	// 这一条盯的是「补空格用的是显示宽度而不是源文长度」：用源文长度补
	// 的话，含链接的那一格会多算十几列，第二栏的起点就偏了。
	want := DisplayWidth("极客商城") + colGap
	if n := colOf(t, got, "[新品]"); n != want {
		t.Errorf("第二栏起点在第 %d 列，want %d（第一栏 %d 列 + 间隔 %d）：\n%q",
			n, want, DisplayWidth("极客商城"), colGap, got)
	}
}

// 补空格必须按**显示宽度**，不能按源文长度。
//
// 这两者在中文和含标记的文本上差得很远：`甲` 是 3 字节 / 2 列，
// `ab` 是 2 字节 / 2 列。屏幕宽度一样，第二栏的起点就必须一样 ——
// 按字节补的话 `甲` 那行会少补一格，两行的第二栏差一列。
//
// 为什么非要专门测这个：第一栏是**中文**时，源文长度比显示宽度**大**，
// 「补到 N 列」的算式退化成「不补」也照样成立 —— 上面那条导航栏判据
// 就是这么被绕过去的（它那一行根本没有格子需要补空格）。所以这里必须
// 用声明宽度把第一栏**撑得比它自己的内容宽**，补空格才会真的发生。
func TestHTMLToMarkdown_ColumnsAlignByDisplayWidthNotSourceLength(t *testing.T) {
	row := func(cell string) string {
		return HTMLToMarkdown(`<table><tr>` +
			`<td width="70%">` + cell + `</td>` +
			`<td width="30%">乙</td></tr></table>`)
	}
	cjk := row("甲")    // 3 字节 / 2 列
	ascii := row("ab") // 2 字节 / 2 列

	cjkCol := colOf(t, cjk, "乙")
	asciiCol := colOf(t, ascii, "乙")
	if cjkCol != asciiCol {
		t.Errorf("第一栏显示宽度相同、字节数不同，第二栏起点却不同：\n"+
			"甲 → 第 %d 列：%q\nab → 第 %d 列：%q", cjkCol, cjk, asciiCol, ascii)
	}
	// 保证上面的相等不是「两边都没补空格」凑出来的。
	if cjkCol <= DisplayWidth("甲") {
		t.Errorf("第一栏没被声明宽度撑宽，这条判据量不到补空格：%q", cjk)
	}
}

// 声明了 width 的格子按声明比例分宽度，最明显的用处是「把右边的东西推到最右」。
//
//	<tr><td width="80%"></td><td width="20%">退订</td></tr>
//
// 空格子按比例分到 80% 的宽度，于是「退订」被推到右边。按内容宽度算的话
// 那个空格子是 0 列，两个格子会挤在左边，版面和原文正好相反。
func TestHTMLToMarkdown_DeclaredWidthPushesRight(t *testing.T) {
	got := HTMLToMarkdown(`<table><tr>
		<td width="80%"></td><td width="20%"><a href="https://ex.com/unsub">退订</a></td>
	</tr></table>`)

	if n := leadingSpaces(got); n == 0 {
		t.Errorf("退订没被推到右边（前导空格被吃掉了？）：%q", got)
	}
	if !strings.Contains(got, "[退订](https://ex.com/unsub)") {
		t.Errorf("退订那格的内容不对：%q", got)
	}
}

// 该堆叠的时候必须堆叠 —— 并排一旦折行，比堆叠更乱。
func TestHTMLToMarkdown_ColumnsFallBackToStacking(t *testing.T) {
	wide := strings.Repeat("字", maxColumnCellCols/2+1) // 两列一个字，必然超单格上限
	for _, tc := range []struct {
		name string
		in   string
	}{
		{
			// 两段话并排，读者不知道该先读哪个。
			"有格子是多行的",
			`<table><tr><td>甲甲甲</td><td>第一段<br>第二段</td></tr></table>`,
		},
		{"格子太长", `<table><tr><td>` + wide + `</td><td>乙</td></tr></table>`},
		{
			"超过四栏",
			`<table><tr><td>甲</td><td>乙</td><td>丙</td><td>丁</td><td>戊</td></tr></table>`,
		},
		{
			// 四格各 10 个中文（20 列）：单格没超，四格加起来超总宽预算。
			"总宽超预算",
			`<table><tr><td>` + strings.Repeat("字", 10) + `</td><td>` + strings.Repeat("字", 10) +
				`</td><td>` + strings.Repeat("字", 10) + `</td><td>` + strings.Repeat("字", 10) +
				`</td></tr></table>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HTMLToMarkdown(tc.in)
			if !strings.Contains(got, "\n\n") {
				t.Errorf("该堆叠却并排了：%q", got)
			}
		})
	}

	// 整行都是空的：什么都不该输出，尤其不能输出一行空格 ——
	// 那一行会变成正文里一条看不见但占位的空行。
	t.Run("整行都是空的", func(t *testing.T) {
		if got := HTMLToMarkdown(`<table><tr><td></td><td>  </td></tr></table>`); got != "" {
			t.Errorf("空行不该产出内容：%q", got)
		}
	})
}

// 含列表的块不许按奇数格缩进。
//
// Markdown 里列表的横向位置是「几个 2 空格」（渲染器按两个空格算一层，
// 见 tui.bullet）。塞奇数个空格，`- 项` 就变成「带前导空格的普通段落」，
// 整段列表塌掉 —— 那不是缩进，是把列表拆了。所以奇数时向下取整。
func TestHTMLToMarkdown_IndentedListKeepsItsMarkers(t *testing.T) {
	for _, style := range []string{
		"padding-left:20px", // 1 列 → 奇数
		"padding-left:48px", // 3 列 → 奇数
		"padding-left:64px", // 4 列 → 偶数
	} {
		t.Run(style, func(t *testing.T) {
			got := HTMLToMarkdown(`<div style="` + style + `"><ul><li>项一</li><li>项二</li></ul></div>`)
			for _, want := range []string{"- 项一", "- 项二"} {
				if !strings.Contains(got, want) {
					t.Errorf("style=%q 之后列表项没了：\n%q", style, got)
				}
			}
			// 缩进必须是偶数（0 也算），否则渲染器认不出标记。
			if n := leadingSpaces(got); n%2 != 0 {
				t.Errorf("style=%q 缩进了 %d 列（奇数）——渲染器会认不出列表：\n%q",
					style, n, got)
			}
		})
	}
}

// ---- 收尾规整 ----

// tidyMarkdown 只能去尾部空白，不能去首行的前导空格。
//
// 这个位置踩过一次：原来结尾是 TrimSpace，它把首行的缩进一起吃掉，
// 于是「整块缩进」的内容会变成「首行顶格、后面几行缩进」。
func TestTidyMarkdown_KeepsFirstLineIndent(t *testing.T) {
	got := tidyMarkdown("   甲\n   乙\n\n\n   丙\n")
	want := "   甲\n   乙\n\n   丙"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}
