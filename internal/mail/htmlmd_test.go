package mail

import (
	"strings"
	"testing"
)

func TestHTMLToMarkdown_Links(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"普通链接",
			`<p>见 <a href="https://example.com/docs">文档</a></p>`,
			"见 [文档](https://example.com/docs)",
		},
		{
			// 文字和地址一样时用自动链接更短，也避免 [x](x) 这种重复。
			"文字即地址",
			`<a href="https://example.com/a">https://example.com/a</a>`,
			"<https://example.com/a>",
		},
		{
			// 这是最要命的一种：营销邮件里的「按钮」就是套了样式的 <a>。
			// 正则版会把 URL 整个丢掉，用户在终端里根本没法点。
			"按钮样式的链接",
			`<a href="https://ex.com/confirm" style="display:inline-block;padding:12px 24px;background:#07c;color:#fff;border-radius:4px">确认订阅</a>`,
			"[确认订阅](https://ex.com/confirm)",
		},
		{
			// 图片按钮：终端显示不了图，alt 才是按钮上写的字。
			"图片按钮取 alt",
			`<a href="https://ex.com/order"><img src="btn.png" alt="查看订单" width="200" height="40"></a>`,
			"[查看订单](https://ex.com/order)",
		},
		{
			"图片按钮无 alt",
			`<a href="https://ex.com/x"><img src="btn.png" width="200" height="40"></a>`,
			"[图片](https://ex.com/x)",
		},
		{
			// 空的 "#" 点了没反应，不该做成链接。
			"锚点占位",
			`<a href="#">展开全文</a>`,
			"展开全文",
		},
		{
			"javascript 伪链接",
			`<a href="javascript:void(0)">点我</a>`,
			"点我",
		},
		{
			// 链接里没文字时不能整条丢掉，那等于把地址也丢了。
			"空文字链接",
			`<a href="https://example.com/x"></a>`,
			"[https://example.com/x](https://example.com/x)",
		},
		{
			"无 href",
			`<a name="anchor">锚</a>`,
			"锚",
		},
		{
			// 邮件地址的自动链接要写裸地址。<mailto:hi@example.com>
			// 也是合法的自动链接，但屏幕上会把 "mailto:" 一起显示出来 ——
			// 自动链接的文字就是 URL 本身。
			"邮件地址用裸地址自动链接",
			`<a href="mailto:hi@example.com">hi@example.com</a>`,
			"<hi@example.com>",
		},
		{
			// 文字不是地址本身时不能压成自动链接，否则文字就丢了。
			"邮件地址带文字",
			`<a href="mailto:hi@example.com">写信给我</a>`,
			"[写信给我](mailto:hi@example.com)",
		},
		{
			// href 只是占位，真地址在 data-href 里。跟 <button> 用
			// 同一套兜底：同一个地址写在 button 上认、写在 a 上也得认。
			"a 的 data-href 当链接",
			`<a role="button" data-href="https://ex.com/7">立即购买</a>`,
			"[立即购买](https://ex.com/7)",
		},
		{
			// 邮件服务商写「伪按钮」的经典写法：href 点了没反应，
			// 真跳转在 onclick 里。
			"a 的 javascript: 占位走 onclick",
			`<a href="javascript:void(0)" onclick="window.open('https://ex.com/8')">查看订单</a>`,
			"[查看订单](https://ex.com/8)",
		},
		{
			// 占位 href + 挖不到地址：只能退成纯文本，不能编个链接出来。
			"a 的占位 href 挖不到地址",
			`<a href="javascript:void(0)" onclick="doThing()">取消订阅</a>`,
			"取消订阅",
		},
		{
			// 空格在 </a> 里面也得留住：HTML 的空白折叠是整段文字流
			// 一起算的，`</a> 后面` 那个空格和 `里面 ` 那个空格等价。
			"链接文字带尾随空格",
			`<p><a href="https://ex.com/x">你好 </a>世界</p>`,
			"[你好](https://ex.com/x) 世界",
		},
		{
			// 句子里夹一个 <span> 不能让正文断行。样式标签在真实邮件里
			// 遍地都是，一旦断行就是满屏碎句。
			"span 不断行",
			`<p>甲<span style="color:red">乙</span>丙</p>`,
			"甲乙丙",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tc.in); got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestHTMLToMarkdown_Buttons(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			// <button> 自己没有地址。挖不到地址时加粗 ——
			// 至少让「这里本来是个按钮」在正文里显出来。
			"无地址的按钮加粗",
			`<button>提交</button>`,
			"**提交**",
		},
		{
			"formaction 当链接",
			`<button formaction="https://ex.com/go">下一步</button>`,
			"[下一步](https://ex.com/go)",
		},
		{
			"data-href 当链接",
			`<button data-href="https://ex.com/go">立即购买</button>`,
			"[立即购买](https://ex.com/go)",
		},
		{
			// 真实邮件里大量用 onclick 塞跳转。
			"onclick 里挖地址",
			`<button onclick="location.href='https://ex.com/pay?id=7'">去支付</button>`,
			"[去支付](https://ex.com/pay?id=7)",
		},
		{
			"onclick 用双引号",
			`<button onclick='window.open("https://ex.com/a")'>打开</button>`,
			"[打开](https://ex.com/a)",
		},
		{
			// type=text / checkbox 是表单脚手架，不是正文。
			"非按钮型 input 丢掉",
			`<p>前<input type="text" name="q">后</p>`,
			"前后",
		},
		{
			"submit 的 value 当文字",
			`<input type="submit" value="登录">`,
			"**登录**",
		},
		{
			"submit 带 formaction",
			`<input type="submit" value="登录" formaction="https://ex.com/login">`,
			"[登录](https://ex.com/login)",
		},
		{
			"隐藏型 input 丢掉",
			`<p>甲<input type="hidden" value="csrf123">乙</p>`,
			"甲乙",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tc.in); got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestHTMLToMarkdown_InlineFormatting(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"加粗", `<b>重点</b>`, "**重点**"},
		{"strong", `<strong>重点</strong>`, "**重点**"},
		{"斜体", `<i>书名</i>`, "*书名*"},
		{"em", `<em>强调</em>`, "*强调*"},
		{"删除线", `<del>旧价格</del>`, "~~旧价格~~"},
		{"嵌套", `<b>粗<i>斜</i></b>`, "**粗*斜***"},
		{"行内代码", `<code>go build</code>`, "`go build`"},
		{
			// 内容里有反引号时围栏要加长，否则代码段被当场截断。
			"代码含反引号",
			`<code>a` + "`" + `b</code>`,
			"`` a`b ``",
		},
		{
			// 标记必须包住有内容的字符：收尾的 ** 被空格顶开就失效了。
			"加粗内容带尾随空格",
			`<p><b>你好 </b>世界</p>`,
			"**你好** 世界",
		},
		{
			"加粗内容带前导空格",
			`<p>甲<b> 乙</b></p>`,
			"甲 **乙**",
		},
		{
			// 没有内容可标记时不该输出空的 **。
			"空加粗",
			`<p>甲<b>   </b>乙</p>`,
			"甲 乙",
		},
		{
			"纯空白内容整体折叠",
			`<p>甲   乙	丙</p>`,
			"甲 乙 丙",
		},
		{
			// Markdown 表达不了下划线，但内容不能丢。
			"下划线只留内容",
			`<p><u>注意</u></p>`,
			"注意",
		},
		{
			"半角圆点不被误解",
			`<p>2 * 3 * 4</p>`,
			`2 \* 3 \* 4`,
		},
		{
			"标识符里的下划线不被误解",
			`<p>user_id_name 字段</p>`,
			`user\_id\_name 字段`,
		},
		{
			"方括号不被误解为链接",
			`<p>[草稿] 标题</p>`,
			`\[草稿\] 标题`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tc.in); got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestHTMLToMarkdown_Blocks(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"标题", `<h2>本周进度</h2>`, "## 本周进度"},
		{"一级标题", `<h1>公告</h1>`, "# 公告"},
		{"六级标题", `<h6>附注</h6>`, "###### 附注"},
		{"分隔线", `<p>上</p><hr><p>下</p>`, "上\n\n---\n\n下"},
		{
			// 段落之间空行，这是 Markdown 里最自然的表达。
			"两个段落",
			`<p>第一段</p><p>第二段</p>`,
			"第一段\n\n第二段",
		},
		{
			// <br> 是段内换行，不能被拆成两块。
			"段内换行",
			`<p>第一行<br>第二行</p>`,
			"第一行\n第二行",
		},
		{
			// 文本和 <br> 是兄弟节点，行内缓冲必须把它们攒成一块。
			"div 里的文本与换行",
			`<div>甲<br>乙</div>`,
			"甲\n乙",
		},
		{
			"引用",
			`<blockquote><p>原始内容</p></blockquote>`,
			"> 原始内容",
		},
		{
			"多行引用每行都有前缀",
			`<blockquote><p>第一段</p><p>第二段</p></blockquote>`,
			"> 第一段\n>\n> 第二段",
		},
		{
			"代码块",
			"<pre>func main() {\n\tprintln(1)\n}</pre>",
			"```\nfunc main() {\n\tprintln(1)\n}\n```",
		},
		{
			// 内容里有 ``` 时必须换更长的围栏，否则代码块漏掉。
			"代码块含三反引号",
			"<pre>```\ninner\n```</pre>",
			"````\n```\ninner\n```\n````",
		},
		{
			"代码块保留缩进",
			"<pre>  indented\n\ttabbed</pre>",
			"```\n  indented\n\ttabbed\n```",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tc.in); got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestHTMLToMarkdown_Lists(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"无序列表",
			`<ul><li>苹果</li><li>香蕉</li></ul>`,
			"- 苹果\n- 香蕉",
		},
		{
			"有序列表统一写 1.",
			`<ol><li>一</li><li>二</li><li>三</li></ol>`,
			"1. 一\n1. 二\n1. 三",
		},
		{
			// 嵌套列表必须另起一级，挂在父项后面会被当成父项的文字。
			"嵌套列表",
			`<ul><li>外<ul><li>内一</li><li>内二</li></ul></li><li>下一个</li></ul>`,
			"- 外\n  - 内一\n  - 内二\n- 下一个",
		},
		{
			"列表项里的加粗与链接",
			`<ul><li><b>重要</b>：见 <a href="https://ex.com/a">这里</a></li></ul>`,
			"- **重要**：见 [这里](https://ex.com/a)",
		},
		{
			"空列表项不留孤零零的标记",
			`<ul><li></li><li>有内容</li></ul>`,
			"- 有内容",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tc.in); got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestHTMLToMarkdown_Tables(t *testing.T) {
	t.Run("数据表转 Markdown 表格", func(t *testing.T) {
		in := `<table>
			<tr><th>项目</th><th>金额</th></tr>
			<tr><td>服务器</td><td>120</td></tr>
			<tr><td>域名</td><td>30</td></tr>
		</table>`
		want := "| 项目 | 金额 |\n| --- | --- |\n| 服务器 | 120 |\n| 域名 | 30 |"
		if got := HTMLToMarkdown(in); got != want {
			t.Errorf("\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("单元格里的竖线要转义", func(t *testing.T) {
		in := `<table>
			<tr><th>符号</th><th>含义</th></tr>
			<tr><td>a|b</td><td>或</td></tr>
		</table>`
		got := HTMLToMarkdown(in)
		if !strings.Contains(got, `a\|b`) {
			t.Errorf("竖线没有转义，会把列切错：\n%s", got)
		}
	})

	// 营销邮件几乎全用 <table> 排版。把它当数据表转成 Markdown 表格
	// 会得到一堆 1 列的怪东西，所以要按布局表铺平。
	t.Run("布局表铺平", func(t *testing.T) {
		in := `<table><tr><td>头部</td></tr><tr><td>正文</td></tr><tr><td>页脚</td></tr></table>`
		want := "头部\n\n正文\n\n页脚"
		if got := HTMLToMarkdown(in); got != want {
			t.Errorf("\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("单列表不当数据表", func(t *testing.T) {
		in := `<table><tr><td>甲</td></tr><tr><td>乙</td></tr></table>`
		got := HTMLToMarkdown(in)
		if strings.Contains(got, "|") {
			t.Errorf("单列表不该转成 Markdown 表格：\n%s", got)
		}
	})

	t.Run("嵌套表格不 panic", func(t *testing.T) {
		in := `<table><tr><td><table><tr><td>内层</td></tr></table></td></tr><tr><td>外层</td></tr></table>`
		if got := HTMLToMarkdown(in); !strings.Contains(got, "内层") {
			t.Errorf("嵌套表格内容丢了：\n%q", got)
		}
	})
}

func TestHTMLToMarkdown_DropsNonContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		gone string
	}{
		{"脚本", `<p>甲</p><script>alert('x')</script><p>乙</p>`, "alert"},
		{"样式", `<style>p{color:red}</style><p>甲</p>`, "color"},
		{"标题标签", `<html><head><title>邮件标题</title></head><body><p>正文</p></body></html>`, "邮件标题"},
		{"HTML 注释", `<p>甲<!-- 内部备注 -->乙</p>`, "内部备注"},
		{
			// Outlook 的条件编译注释里裹着一整段给 Word 用的 HTML，
			// 读出来是纯噪音。
			"Outlook 条件注释",
			`<p>甲</p><!--[if mso]><table><tr><td>仅 Word 可见</td></tr></table><![endif]--><p>乙</p>`,
			"仅 Word",
		},
		{
			// preheader：网页里看不见，但直接转出来会顶在正文最前面。
			"display:none 的 preheader",
			`<div style="display:none;max-height:0">这是一封系统通知，请勿回复</div><p>真正的正文</p>`,
			"请勿回复",
		},
		{
			"hidden 属性",
			`<div hidden>隐藏内容</div><p>正文</p>`,
			"隐藏内容",
		},
		{
			"visibility:hidden",
			`<span style="visibility: hidden">藏起来</span><p>正文</p>`,
			"藏起来",
		},
		{"iframe", `<iframe src="https://ads.example.com"></iframe><p>正文</p>`, "ads.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HTMLToMarkdown(tc.in)
			if strings.Contains(got, tc.gone) {
				t.Errorf("应该丢掉的内容还在：%q\n输出：%q", tc.gone, got)
			}
		})
	}
}

func TestHTMLToMarkdown_TrackingPixel(t *testing.T) {
	// 营销邮件末尾那张 1×1 的图，src 是一长串带用户标识的 URL。
	// 铺进聊天流里就是噪音，丢掉。
	for _, in := range []string{
		`<img src="https://track.ex.com/o.gif?id=abc123" width="1" height="1">`,
		`<img src="https://track.ex.com/o.gif?id=abc123" width="1px" height="1px">`,
		`<img src="https://track.ex.com/o.gif?id=abc" width="0" height="0">`,
	} {
		got := HTMLToMarkdown(in)
		if strings.Contains(got, "track.ex.com") {
			t.Errorf("追踪像素没丢掉：%q", got)
		}
	}

	// 正常尺寸的图不能误伤。
	got := HTMLToMarkdown(`<img src="https://cdn.ex.com/logo.png" alt="Logo" width="120" height="40">`)
	if !strings.Contains(got, "logo.png") {
		t.Errorf("正常图片被误删了：%q", got)
	}
}

func TestHTMLToMarkdown_Images(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"带 alt", `<img src="https://ex.com/a.png" alt="折线图">`, "![折线图](https://ex.com/a.png)"},
		{"无 alt", `<img src="https://ex.com/a.png">`, "![](https://ex.com/a.png)"},
		{"回退到 title", `<img src="https://ex.com/a.png" title="说明">`, "![说明](https://ex.com/a.png)"},
		{"无 src 丢掉", `<img alt="空图">`, ""},
		{
			"alt 里的方括号要转义",
			`<img src="https://ex.com/a.png" alt="图[1]">`,
			`![图\[1\]](https://ex.com/a.png)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tc.in); got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestHTMLToMarkdown_Whitespace(t *testing.T) {
	t.Run("nbsp 归一成普通空格", func(t *testing.T) {
		// &nbsp; 解码后是 U+00A0，看起来像空格但正则和 Markdown 都不认。
		got := HTMLToMarkdown("<p>甲&nbsp;乙</p>")
		if strings.ContainsRune(got, '\u00a0') {
			t.Errorf("还有不换行空格：%q", got)
		}
		if got != "甲 乙" {
			t.Errorf("got %q, want %q", got, "甲 乙")
		}
	})

	t.Run("连续空行压成一个", func(t *testing.T) {
		got := HTMLToMarkdown("<p>甲</p><div><div></div></div><p>乙</p>")
		if strings.Contains(got, "\n\n\n") {
			t.Errorf("空行没压干净：%q", got)
		}
	})

	t.Run("首尾空白去掉", func(t *testing.T) {
		got := HTMLToMarkdown("<p>  正文  </p>")
		if got != "正文" {
			t.Errorf("got %q", got)
		}
	})
}

func TestHTMLToMarkdown_Robustness(t *testing.T) {
	// 真实邮件里的 HTML 经常是不合法的（标签没闭合、大小写混乱、
	// 属性没引号）。转换器绝不能在这里 panic —— 一封坏邮件不该
	// 让整个界面崩掉。
	for _, in := range []string{
		"",
		"   ",
		"不是 HTML，就是一段普通文字",
		"<p>没闭合",
		"</p>只有闭合标签</p>",
		"<div><span><b>交叉嵌套</div></span></b>",
		"<P CLASS=foo>属性没引号</P>",
		"<ul><li>列表没闭合",
		"<table><tr><td>表格没闭合",
		"<a href=>空 href</a>",
		"<img>",
		"<button></button>",
		"<pre></pre>",
		"<>",
		"<b><i><u><s><code>深层嵌套</code></s></u></i></b>",
		strings.Repeat("<div>", 200) + "深" + strings.Repeat("</div>", 200),
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("输入 %q 导致 panic: %v", in, r)
				}
			}()
			HTMLToMarkdown(in)
		}()
	}
}

// 纯文字（不带任何标签）必须原样显示，只是把 Markdown 特殊字符转义掉。
func TestHTMLToMarkdown_PlainTextIsEscaped(t *testing.T) {
	got := HTMLToMarkdown("价格是 2*3 元，见 user_id")
	if strings.Contains(got, "*") && !strings.Contains(got, `\*`) {
		t.Errorf("星号没转义：%q", got)
	}
	if strings.Contains(got, "user_id") {
		t.Errorf("下划线没转义：%q", got)
	}
	if !strings.Contains(got, "价格是") {
		t.Errorf("文字丢了：%q", got)
	}
}

func TestEscapeMarkdown(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"普通文字", "普通文字"},
		{"2*3", `2\*3`},
		{"a_b", `a\_b`},
		{"[x]", `\[x\]`},
		{"`code`", "\\`code\\`"},
		{"a\\b", `a\\b`},
		{"# 行首标记", `\# 行首标记`},
		{"- 行首破折号", `\- 行首破折号`},
		{"> 行首引用", `\> 行首引用`},
		// 行首之后的同类字符是普通标点，转义了反而难看。
		{"甲 - 乙", "甲 - 乙"},
		{"甲 # 乙", "甲 # 乙"},
		// 换行之后又回到行首。
		{"甲\n- 乙", "甲\n\\- 乙"},
	} {
		if got := EscapeMarkdown(tc.in); got != tc.want {
			t.Errorf("EscapeMarkdown(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
		}
	}
}

// 一条尽量贴近真实邮件的用例：结构、链接、按钮、列表、表格、隐藏文字
// 全都有。这条不逐字比对，检查的是「该还原的都在、该丢的都没了」。
func TestHTMLToMarkdown_RealisticEmail(t *testing.T) {
	const src = `<html><head><title>订单已发货</title>
<style>body{font-family:sans-serif}</style></head>
<body>
<div style="display:none;max-height:0">您的订单已发货，点击查看物流详情</div>
<table width="600"><tr><td>
  <h2>订单已发货</h2>
  <p>你好，<b>张先生</b>：</p>
  <p>你于 <i>2026-10-01</i> 下单的商品已经发出。</p>
  <p>快递单号：<code>SF1234567890</code><br>预计 10 月 4 日送达。</p>
  <h3>订单明细</h3>
  <table>
    <tr><th>商品</th><th>数量</th><th>金额</th></tr>
    <tr><td>机械键盘</td><td>1</td><td>￥499</td></tr>
    <tr><td>腕托</td><td>2</td><td>￥78</td></tr>
  </table>
  <ul>
    <li>支持 7 天无理由退货</li>
    <li>发票可在 <a href="https://ex.com/invoice">订单中心</a> 下载</li>
  </ul>
  <p><a href="https://ex.com/track?id=SF1234567890" style="background:#07c;padding:12px 32px;color:#fff">查看物流</a></p>
  <p><a href="https://ex.com/unsub">退订</a></p>
  <img src="https://track.ex.com/open.gif?uid=9988" width="1" height="1">
</td></tr></table>
</body></html>`

	got := HTMLToMarkdown(src)

	for _, want := range []string{
		"## 订单已发货",
		"**张先生**",
		"*2026-10-01*",
		"`SF1234567890`",
		"### 订单明细",
		"| 商品 | 数量 | 金额 |",
		"| 机械键盘 | 1 | ￥499 |",
		"- 支持 7 天无理由退货",
		"[订单中心](https://ex.com/invoice)",
		"[查看物流](https://ex.com/track?id=SF1234567890)",
		"[退订](https://ex.com/unsub)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q\n--- 实际输出 ---\n%s", want, got)
		}
	}

	for _, gone := range []string{
		"font-family",           // <style>
		"点击查看物流详情",              // preheader
		"track.ex.com",          // 追踪像素
		"<table", "<div", "<p>", // 残留标签
	} {
		if strings.Contains(got, gone) {
			t.Errorf("不该出现 %q\n--- 实际输出 ---\n%s", gone, got)
		}
	}

	// 段落之间有空行、段内没有 —— 这是可读性的关键。
	if !strings.Contains(got, "你好，**张先生**：") {
		t.Errorf("段落结构不对：\n%s", got)
	}
	if !strings.Contains(got, "快递单号：`SF1234567890`\n预计 10 月 4 日送达。") {
		t.Errorf("<br> 没保留成段内换行：\n%s", got)
	}
}

// 这条替掉了原来那个「幂等性」测试。
//
// 原来它要求 HTMLToMarkdown(HTMLToMarkdown(x)) == HTMLToMarkdown(x)。
// 这条**不是**正确转换器该有的性质：本函数的输入契约是 HTML，把上一次
// 的 Markdown 输出再喂回来，等于在说「这是一段含 [ ] * _ 的字面文本」，
// 那它当然要把这些字符转义掉。不转义才是错的 —— 用户会在正文里看到
// 被吃掉或被当标记解析的原文。见 TestHTMLToMarkdown_PlainTextIsEscaped。
//
// 所以这里改成把「输出的 Markdown 是字面文本」这条边界钉死：以后
// 谁想为了凑出「跑两次一样」而去削弱转义，这条会立刻红，而且注释里
// 就写着为什么不许那么干。
//
// 真正需要防重复转换的地方在调用方：CleanBody 只在取信时对原始 HTML
// 跑一次。
func TestHTMLToMarkdown_MarkdownInputIsLiteralText(t *testing.T) {
	const src = `<p>见 <a href="https://ex.com/a">这里</a>，价格 2*3 元。</p>`
	once := HTMLToMarkdown(src)
	if once != "见 [这里](https://ex.com/a)，价格 2\\*3 元。" {
		t.Fatalf("第一次转换的结果变了：\n%q", once)
	}

	// 第二次的输入已经是 Markdown，方括号、反斜杠、星号都只是字面字符。
	twice := HTMLToMarkdown(once)
	for _, want := range []string{`\[这里\]`, `2\\\*3`} {
		if !strings.Contains(twice, want) {
			t.Errorf("Markdown 字面字符 %q 没被转义：\n%q", want, twice)
		}
	}
	if !strings.Contains(twice, "https://ex.com/a") {
		t.Errorf("地址丢了：\n%q", twice)
	}
	if !strings.Contains(twice, "见") {
		t.Errorf("文字丢了：\n%q", twice)
	}
}

// 营销邮件惯用一个大 <table><tr><td> 把整封信包起来。这种单格布局表
// 必须按块铺开 —— 一压成一行，标题、段落、列表、段内换行会全糊在
// 同一行里。
func TestHTMLToMarkdown_BodyWrappedInLayoutTable(t *testing.T) {
	const src = `<table width="600"><tr><td>
		<h2>标题</h2>
		<p>第一段</p>
		<p>甲<br>乙</p>
		<ul><li>项一</li><li>项二</li></ul>
	</td></tr></table>`

	got := HTMLToMarkdown(src)
	for _, want := range []string{
		"## 标题",
		"第一段",
		"甲\n乙",
		"- 项一",
		"- 项二",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q\n--- 实际输出 ---\n%s", want, got)
		}
	}
	// 段落之间要有空行 —— 整封信被压成一行是这条最容易掉进去的坑。
	if !strings.Contains(got, "## 标题\n\n第一段") {
		t.Errorf("块结构没保住：\n%s", got)
	}
}
