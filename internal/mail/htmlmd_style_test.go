package mail

import (
	"strings"
	"testing"
)

// 样式加粗（`style="font-weight:bold"`）和标题推断是同一类问题：
// 邮件客户端会把 `<b>` 自带的样式 strip 掉，所以模板把粗体直接写进 style。
// 只认标签的话，正文里的强调在终端里全平了 —— 和标题塌成一段是同一个病。
func TestHTMLToMarkdown_StyleBold(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"span 上的 font-weight",
			`<body><p>请看<span style="font-weight:bold">注意</span>这一句。</p></body>`,
			"请看**注意**这一句。",
		},
		{
			"font-weight bolder 和数字都认",
			`<body><p><span style="font-weight:700">甲</span> <span style="font-weight:bolder">乙</span></p></body>`,
			"**甲** **乙**",
		},
		{
			// HTML3 的老写法，老邮件模板里还有。
			"font 标签的 weight 属性",
			`<body><p>请看<font weight="bold">注意</font>这一句。</p></body>`,
			"请看**注意**这一句。",
		},
		{
			"normal 和 400 不是加粗",
			`<body><p><span style="font-weight:normal">甲</span><span style="font-weight:400">乙</span></p></body>`,
			"甲乙",
		},
		{
			// 整块自己声明加粗：粗体段落。
			"块级元素上的样式加粗",
			`<body><p style="font-weight:bold">整段都加粗</p></body>`,
			"**整段都加粗**",
		},
		{
			// <b> 标签本来就会包 **，外层再包一次会写出 ****。
			"b 标签和样式叠在一起只包一层",
			`<body><p><b style="font-weight:bold">甲</b></p></body>`,
			"**甲**",
		},
		{
			// 外层样式 + 内层标签：内层不能再包一次。
			// 双层写出来是 `**甲**乙****`，两对标记配错位，屏幕上全是星号。
			"样式里套 b 标签不嵌套",
			`<body><p><span style="font-weight:bold">甲<b>乙</b></span></p></body>`,
			"**甲乙**",
		},
		{
			"样式里套多个 b 标签不嵌套",
			`<body><p><span style="font-weight:bold">甲<b>乙</b>丙<b>丁</b></span></p></body>`,
			"**甲乙丙丁**",
		},
		{
			// 标记不跨行。渲染器按行解析，跨行的 ** 两边配不上、会原样显示。
			"加粗里有换行就不加标记",
			`<body><p><b>甲<br>乙</b></p></body>`,
			"甲\n乙",
		},
		{
			"样式加粗里有换行也不加标记",
			`<body><span style="font-weight:bold">甲<br>乙</span></body>`,
			"甲\n乙",
		},
		{
			// 标题本来就是粗体，再包 ** 是冗余，而且会被
			// renderButtonish 那一类再包一层 → ****。
			"标题里不放加粗标记",
			`<body><p style="font-size:14px">正文。</p><h2>标题<span style="font-weight:bold">重点</span></h2></body>`,
			"正文。\n\n## 标题重点",
		},
		{
			// 无地址的按钮标签外面还要包一层 **，内层不能再包。
			"按钮标签里不重复加粗",
			`<body><p style="font-size:14px">正文。</p><button style="font-weight:bold">去支付</button></body>`,
			"正文。\n\n**去支付**",
		},
		{
			"加粗的单元格",
			`<body><table><tr><th>甲</th><th>乙</th></tr><tr><td style="font-weight:bold">丙</td><td>丁</td></tr></table></body>`,
			"| 甲 | 乙 |\n| --- | --- |\n| **丙** | 丁 |",
		},
		{
			// 块级子元素落在「不要加标记」的上下文里（按钮/标题的标签）时，
			// 那一整棵子树都得闭嘴。子构造器忘了继承这个约束的话，
			// 按钮会写出 ****去支付**** —— 屏幕上四个星号。
			"按钮里带块级加粗子元素不双重包装",
			`<body><button><div style="font-weight:bold">去支付</div></button></body>`,
			"**去支付**",
		},
		{
			// 再嵌一层：约束要传到**孙**节点（走的是 sub()）。
			"子构造器也继承不加标记的约束",
			`<body><button><div><div style="font-weight:bold">去支付</div></div></button></body>`,
			"**去支付**",
		},
		{
			"子构造器也挡得住标签加粗",
			`<body><button><div><span style="font-weight:bold">去支付</span></div></button></body>`,
			"**去支付**",
		},
		{
			// <b> 走的是 wrapChildren，不是 plainInlineNode —— 挡的地方
			// 少一处就会输出去重。

			// 按钮外面还要包一层 **，里面再来一个 <b> 就是 ****去支付****，
			// 而四个星号渲染端认不出来，会原样显示给用户。
			"按钮标签里的 b 标签不再加粗",
			`<body><button><b>去支付</b></button></body>`,
			"**去支付**",
		},
		{
			"按钮标签里的斜体也不加标记",
			`<body><button><i>去支付</i></button></body>`,
			"**去支付**",
		},
		{
			"按钮标签里嵌套的 b 标签也不再加粗",
			`<body><button><div><b>去支付</b></div></button></body>`,
			"**去支付**",
		},
		{
			// 标签形式的标题同理：标题本来就渲染成粗体，标签里的 <b>
			// 再包一次就是 `## 标题**重点**`，多余（这条是防回归）。

			"标签标题里的 b 标签不再加粗",
			`<body><p style="font-size:14px">正文。</p><h2>标题<b>重点</b></h2></body>`,
			"正文。\n\n## 标题重点",
		},
		{
			// 嵌套的星号标记压成一层（`***粗斜***` 渲染端认不出），
			// 但 ~~ 和星号不冲突，该留着。
			"加粗里的删除线不被压平",
			`<body><p><b>甲<del>旧</del></b></p></body>`,
			"**甲~~旧~~**",
		},
		{
			"标题里带块级加粗子元素不长出标记",
			`<body><p style="font-size:14px">正文写得足够长，长到成为正文字号的基准。</p><h2><div style="font-weight:bold">甲</div></h2></body>`,
			"正文写得足够长，长到成为正文字号的基准。\n\n## 甲",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tc.in); got != tc.want {
				t.Errorf("HTMLToMarkdown()\n得到 %q\n想要 %q", got, tc.want)
			}
		})
	}
}

// TestHTMLToMarkdown_NoUnbalancedBoldMarkers 是一条不变量：产出里的 `**`
// 一定成对。它对**所有**输入都该成立 —— 上面那些具体用例只能钉住想得到的
// 形状，而「配对」这件事是可以穷举检查的。
//
// SQL 注入式地想想：`**` 的数量是奇数就说明有一处标记没配上，屏幕上会露出
// 裸露的星号。这比「少加粗了一处」糟得多。
func TestHTMLToMarkdown_NoUnbalancedBoldMarkers(t *testing.T) {
	inputs := []string{
		`<body><p><b>甲</b></p></body>`,
		`<body><p><b>甲<br>乙</b></p></body>`,
		`<body><p><span style="font-weight:bold">甲<b>乙</b><i>丙</i></span></p></body>`,
		`<body><p><b></b></p></body>`,
		`<body><p><b>   </b></p></body>`,
		`<body><p><b>甲<i>乙<b>丙</b></i></span></p></body>`,
		`<body><span style="font-weight:bold"><div>甲</div></span></body>`,
		`<body><table><tr><td style="font-weight:bold">甲<br>乙</td><td>丙</td></tr></table></body>`,
		styleTitleMail,
	}
	for i, in := range inputs {
		got := HTMLToMarkdown(in)
		// 数 ** 的个数：转义后的 \* 不算（那是正文里的星号）。
		n := 0
		for j := 0; j+1 < len(got); j++ {
			if got[j] == '\\' {
				j++ // 跳过被转义的字符
				continue
			}
			if got[j] == '*' && got[j+1] == '*' {
				n++
				j++
			}
		}
		if n%2 != 0 {
			t.Errorf("第 %d 个输入产出了奇数个 ** （%d 个），一定有标记没配上：\n%s", i, n, got)
		}
	}
}

// TestHTMLToMarkdown_StyleBoldNeedsInnermostMark 盯着「外层样式不能盖过内层」：
// 外层 <b> 里塞了块级子元素时，内层的文字不该被外层顺手包进去。
func TestHTMLToMarkdown_StyleBoldInBlockContextIsSafe(t *testing.T) {
	// <b> 在行内位置包住一个块级元素（不规范 HTML）。内容不许丢、也不许
	// 漏出裸标记。
	got := HTMLToMarkdown(`<body><span style="font-weight:bold"><div>甲</div></span></body>`)
	if got != "甲" {
		t.Errorf("得到 %q，想要 %q", got, "甲")
	}
	if strings.Contains(got, "*") {
		t.Errorf("产出了星号 —— 块级子元素让标记无处可加，就不该硬加：%q", got)
	}
}
