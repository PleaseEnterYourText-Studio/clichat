package mail

import (
	"strings"
	"testing"
)

// styleTitleMail 是一封「用行内样式表达标题」的通知邮件，按真实营销邮件的
// 结构手写：嵌套布局表、标题用 font-size 而不是 <h1>-<h6>、按钮是套了样式
// 的 <a>、正文带链接。
//
// 这类邮件是标题推断存在的理由 —— 拿掉 headingLevel，整封信的层级会全塌成
// 同样大小的段落。
const styleTitleMail = `<html><body>
<div style="display:none;font-size:1px">在浏览器中查看</div>
<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">
<table width="600" cellpadding="0" cellspacing="0">
  <tr><td align="center"><img src="https://cf.ex.com/logo.png" alt="CLOUDFLARE" width="160" height="30"></td></tr>
  <tr><td align="center" style="font-family:Arial;font-size:28px;font-weight:bold;color:#222">
    我们的网络正在提升 yzjtian.cn 的速度和安全性
  </td></tr>
  <tr><td align="center"><a href="https://dash.cloudflare.com/?to=/:account/yzjtian.cn" style="font-size:18px;background:#f6821f;color:#fff;padding:12px 24px">查看域名分析</a></td></tr>

  <tr><td>
    <table width="100%" cellpadding="0" cellspacing="0">
      <tr><td style="font-size:20px;font-weight:bold">使用 Cloudflare Workers 更快地</td></tr>
      <tr><td style="font-size:14px">通过 HTTP 路由快速修改 yzjtian.cn 上后应用数据等。</td></tr>
      <tr><td><a href="https://workers.cloudflare.com/">探索 Workers</a></td></tr>
    </table>
  </td></tr>

  <tr><td>
    <table width="100%" cellpadding="0" cellspacing="0">
      <tr><td style="font-size:20px;font-weight:bold">进一步提升性能</td></tr>
      <tr><td style="font-size:14px">了解 Cloudflare 如何通过图片、内容和性能优化方案，提升您的网站性能。</td></tr>
      <tr><td><a href="https://dash.cloudflare.com/speed">查看速度建议</a></td></tr>
    </table>
  </td></tr>

  <tr><td>
    <table width="100%" cellpadding="0" cellspacing="0">
      <tr><td style="font-size:20px;font-weight:bold">将安全性提升到新高度</td></tr>
      <tr><td style="font-size:14px">探索 Cloudflare 提升 yzjtian.cn 安全性</td></tr>
      <tr><td><a href="https://dash.cloudflare.com/security">查看安全建议</a></td></tr>
    </table>
  </td></tr>

  <tr><td style="font-size:20px;font-weight:bold">名称服务器详细信息（供参考）</td></tr>
  <tr><td style="font-size:14px">yzjtian.cn 当前的 Cloudflare 名称服务器:</td></tr>
  <tr><td><ul><li>ace.ns.cloudflare.com</li><li>naomi.ns.cloudflare.com</li></ul></td></tr>
  <tr><td style="font-size:14px">以前的名称服务器:</td></tr>
  <tr><td><ul><li>dns9.hichina.com</li><li>dns10.hichina.com</li></ul></td></tr>

  <tr><td><hr></td></tr>
  <tr><td style="font-size:18px;font-weight:bold">资源</td></tr>
  <tr><td><ul>
    <li><a href="https://cf.ex.com/1">Cloudflare 基础知识</a></li>
    <li><a href="https://cf.ex.com/2">文章</a></li>
    <li><a href="https://cf.ex.com/3">如何最大程度减少停机时间</a></li>
    <li><a href="https://cf.ex.com/4">您的计划中包含哪些内容</a></li>
  </ul></td></tr>
  <tr><td style="font-size:18px;font-weight:bold">为什么我会收到这封邮件?</td></tr>
  <tr><td style="font-size:14px">您收到此邮件，是因为 yzjtian.cn 完成了验证并在 Cloudflare 上激活。</td></tr>
  <tr><td style="font-size:12px;color:#999">Copyright © 2026 Cloudflare, Inc.<br>101 Townsend Street, San Francisco, CA 94107<br>
    <a href="https://www.cloudflare.com">www.cloudflare.com</a> | <a href="https://cf.ex.com/c">社区</a> | <a href="https://cf.ex.com/p">隐私</a></td></tr>
</table>
</td></tr></table>
</body></html>`

// headingOf 找出包含 kw 的那一行，返回它的标题级别（0 表示这行不是标题）。
//
// 判据刻意**不绑级别数字**只绑相对关系：级别取决于「正文字号基准」，
// 而基准是这封信自己算出来的 —— 换一封正文是 16px 的信，同样的 20px
// 小节标题会落到不同档。绑死数字会在换夹具时变成假红，而假红和假绿一样贵。
func headingOf(md, kw string) int {
	for _, ln := range strings.Split(md, "\n") {
		if !strings.Contains(ln, kw) {
			continue
		}
		n := 0
		for n < len(ln) && ln[n] == '#' {
			n++
		}
		if n > 0 && n < len(ln) && ln[n] == ' ' {
			return n
		}
	}
	return 0
}

func TestHTMLToMarkdown_StyleTitlesKeepHierarchy(t *testing.T) {
	md := HTMLToMarkdown(styleTitleMail)

	big := headingOf(md, "速度和安全")
	if big == 0 {
		t.Fatalf("整封信的大标题没被认出来 —— 标题推断没生效\n%s", md)
	}

	// 小节标题都得是标题，而且级别要**低于**大标题。只断言「是标题」不够：
	// 全判成同一级同样是把层级抹平了。
	for _, kw := range []string{
		"Workers 更快地",
		"进一步提升性能",
		"将安全性提升到新高度",
		"名称服务器详细信息",
		"资源",
		"为什么我会收到",
	} {
		lv := headingOf(md, kw)
		if lv == 0 {
			t.Errorf("小节标题 %q 没被认成标题\n%s", kw, md)
			continue
		}
		if lv <= big {
			t.Errorf("小节标题 %q 的级别 %d 不深于大标题的 %d —— 层级没保住", kw, lv, big)
		}
	}

	// 正文不能被铺成标题。
	for _, kw := range []string{
		"通过 HTTP 路由",
		"了解 Cloudflare 如何通过图片",
		"您收到此邮件",
		"Copyright",
	} {
		if lv := headingOf(md, kw); lv != 0 {
			t.Errorf("正文 %q 被当成标题了（级别 %d）", kw, lv)
		}
	}

	// 按钮：大字号 + 加粗，长得最像标题，只有「里面是个 <a>」能挡开它。
	if lv := headingOf(md, "查看域名分析"); lv != 0 {
		t.Errorf("按钮被当成标题了（级别 %d）", lv)
	}
	// 页脚链接字号 12px，低于正文，更不该是标题。
	if lv := headingOf(md, "www.cloudflare.com"); lv != 0 {
		t.Errorf("页脚链接被当成标题了（级别 %d）", lv)
	}
}

func TestHTMLToMarkdown_HeadingNeedsOwnFontSize(t *testing.T) {
	// 基准钉在 14px 的正文段落。**它必须比被测的那一段更长** ——
	// 否则基准会被被测元素抢走（谁字多谁是基准），判据就在测基准估算、
	// 而不是在测它自己声称的那条规矩了。这里每一格的 `in` 都是
	// longBody + 被测片段，`want` 也由同一段正文拼出来。
	const longBody = `<p style="font-size:14px">这一段正文足够长，用来把正文字号牢牢钉在基准上，不让后面被测的那一段反过来影响基准估算。</p>`
	const bodyText = "这一段正文足够长，用来把正文字号牢牢钉在基准上，不让后面被测的那一段反过来影响基准估算。"

	for _, tc := range []struct {
		name    string
		sub     string // 被测片段（接在 longBody 之后）
		raw     string // 非空时整封自带，不用 longBody 拼
		wantRaw string // raw 非空时的期望产出
		tail    string // 期望的产出（正文之后的部分）
	}{
		{
			// 标题一定会自己声明字号 —— 不声明就跟着客户端默认走，
			// 那种排版「标题」在别的客户端里本来也跟正文一样大。
			name: "只有 b 没有字号不算标题",
			sub:  `<p><b>加粗但没字号</b></p>`,
			tail: "**加粗但没字号**",
		},
		{
			// 加粗**不是**判据的一部分：字号没超出正文就只是个加粗的段落。
			// 这里刻意用 font-weight 样式而不是 <b> 标签 —— 真实邮件就是
			// 这么写的，而它必须同时满足两件事：转成 **（样式即语义），
			// 但不升级成标题（字号不够）。
			name: "字号和正文一样不算标题",
			sub:  `<p style="font-size:14px;font-weight:bold">也是正文</p>`,
			tail: "**也是正文**",
		},
		{
			// 不带 px 单位的字号不猜：猜错会把正文铺成 # 号，
			// 那比漏掉一个标题糟得多。
			name: "非 px 字号不算标题",
			sub:  `<p style="font-size:2em">放大的一段</p>`,
			tail: "放大的一段",
		},
		{
			name: "标题里有链接就不算标题",
			sub:  `<p style="font-size:24px">看 <a href="https://ex.com">这里</a></p>`,
			tail: "看 [这里](https://ex.com)",
		},
		{
			name: "标题里有图片就不算标题",
			sub:  `<p style="font-size:24px"><img src="a.png" alt="示意图"></p>`,
			tail: "![示意图](a.png)",
		},
		{
			name: "标题里有块级子元素就不算标题",
			sub:  `<div style="font-size:24px"><p>里面还有一段</p></div>`,
			tail: "里面还有一段",
		},
		{
			// 版式邮件用大字号做竖直留白（`<td style="font-size:24px">&nbsp;</td>`）。
			// 里面一个字都没有，绝不能被铺成一个孤零零的 "#"。
			// 判据是「产出里没有 #」——只看「产出为空」不够，那样即便它变成标题，
			// 只要标题内容渲染成空也还是会绿。
			name: "只有空白的放大块不算标题",
			sub:  `<table><tr><td style="font-size:24px">&nbsp;</td></tr></table>`,
			tail: "",
		},
		{
			// 外层声明 14px、内层正文 16px：字是内层渲染的，基准该是 16。
			// 判据用「18px + 加粗」——18/16 = 1.13，落在「只有加粗才成立」
			// 的那一档（1.2~1.4）；所以正确基准下它不是标题。只有基准被
			// 外层的 14px 带偏（18/14 = 1.29），它才会跨过 1.2 那一档。
			// 这一格盯的就是「外层声明不能盖过内层」。
			//
			// 内层刻意写样式 font-weight:bold 而不是 <b> 标签：生成端不把
			// 样式变成 **，所以期望里没有粗体标记 —— 而 styleBold 认的正是
			// 这个样式。
			name: "外层字号不盖过内层",
			raw: `<body><div style="font-size:14px">` +
				`<p style="font-size:16px">内层的正文段落，字数要足够多，多到只要它被正确计入就一定是基准。</p>` +
				`<p style="font-size:18px;font-weight:bold">略大的一点</p>` +
				`</div></body>`,
			wantRaw: "内层的正文段落，字数要足够多，多到只要它被正确计入就一定是基准。\n**略大的一点**",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, want := tc.raw, tc.wantRaw
			if in == "" {
				in = `<body>` + longBody + tc.sub + `</body>`
				want = bodyText
				if tc.tail != "" {
					want += "\n\n" + tc.tail
				}
			}

			got := HTMLToMarkdown(in)
			if got != want {
				t.Errorf("HTMLToMarkdown()\n得到 %q\n想要 %q", got, want)
			}
			if strings.Contains(got, "#") {
				t.Errorf("产出里出现了标题标记 —— 这一格不该有标题：\n%s", got)
			}
		})
	}
}

func TestHTMLToMarkdown_DivAndPHeadings(t *testing.T) {
	// 标题写法不止一种。td / div / p 都得认。
	for _, tc := range []struct {
		name string
		in   string
		kw   string
	}{
		{"div 上的标题", `<body><p style="font-size:14px">正文。</p><div style="font-size:26px;font-weight:bold">小节甲</div></body>`, "小节甲"},
		{"p 上的标题", `<body><p style="font-size:14px">正文。</p><p style="font-size:26px;font-weight:bold">小节乙</p></body>`, "小节乙"},
		{"td 上的标题", `<body><table><tr><td style="font-size:14px">正文。</td></tr><tr><td style="font-size:26px;font-weight:bold">小节丙</td></tr></table></body>`, "小节丙"},
		{"样式写在包裹的 span 上也能认", `<body><table><tr><td style="font-size:14px">正文。</td></tr><tr><td style="font-size:26px"><b>小节丁</b></td></tr></table></body>`, "小节丁"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := HTMLToMarkdown(tc.in)
			if headingOf(md, tc.kw) == 0 {
				t.Errorf("%q 没被认成标题\n%s", tc.kw, md)
			}
		})
	}
}

// TestHTMLToMarkdown_BaseFontSizeWeighsTextNotElements 盯着基准估算的脆弱点：
// 短通知邮件里标题比正文段落**多**（三个小节标题 + 一句正文），按元素个数
// 数的话标题会反客为主变成基准，于是正文全部「不达标」、整封信反过来更乱。
// 所以要按**文字量**加权。
func TestHTMLToMarkdown_BaseFontSizeWeighsTextNotElements(t *testing.T) {
	const in = `<body>
<p style="font-size:14px">这是一段足够长的正文，用来把正文字号的分量压过去。</p>
<p style="font-size:20px;font-weight:bold">小节甲</p>
<p style="font-size:20px;font-weight:bold">小节乙</p>
<p style="font-size:20px;font-weight:bold">小节丙</p>
</body>`

	md := HTMLToMarkdown(in)

	// 正文字号是 14px（字最多），所以 20px 的三个小节兄弟都该是标题。
	for _, kw := range []string{"小节甲", "小节乙", "小节丙"} {
		if headingOf(md, kw) == 0 {
			t.Errorf("%q 没被认成标题 —— 基准被标题带跑了\n%s", kw, md)
		}
	}
	// 反过来，正文不能被当成标题。
	if lv := headingOf(md, "这是一段足够长的正文"); lv != 0 {
		t.Errorf("正文被当成标题了（级别 %d）\n%s", lv, md)
	}
}

// TestHTMLToMarkdown_BaseFontSizeCountsInnermostDeclaration 盯着「外层和内层
// 都声明字号时的重复计入」：字是内层那个字号渲染的，外层不该再算一份。
func TestHTMLToMarkdown_BaseFontSizeCountsInnermostDeclaration(t *testing.T) {
	// 外层 div 写着 14px 但自己一个字都没有正文意义，真正的正文在
	// 内层 16px 的 <p> 里 —— 内层的字多，基准该是 16 而不是 14。
	const in = `<body>
<div style="font-size:14px">
  <p style="font-size:16px">内层这一段是正文，字数明显比别的都多，基准该跟着它走。</p>
  <p style="font-size:16px">再来一段正文，把内层字号的分量压稳。</p>
  <p style="font-size:26px;font-weight:bold">小节</p>
</div>
</body>`

	md := HTMLToMarkdown(in)
	if headingOf(md, "小节") == 0 {
		t.Errorf("16px 正文基准下，26px 的小节没被认成标题 —— 基准被外层 14px 带偏了\n%s", md)
	}
	if lv := headingOf(md, "内层这一段是正文"); lv != 0 {
		t.Errorf("正文被当成标题了（级别 %d）\n%s", lv, md)
	}
}

func TestHTMLToMarkdown_BaseFontSizeIsDeterministic(t *testing.T) {
	// 两个字号承载的文字量**并列**（各一个字），基准取较小的那个
	// ——「更小」才是正文。这个 tie-break 必须与 map 遍历顺序无关。
	const in = `<body>
<p style="font-size:14px">甲</p>
<p style="font-size:20px">乙</p>
</body>`

	first := HTMLToMarkdown(in)
	for i := 0; i < 20; i++ {
		if got := HTMLToMarkdown(in); got != first {
			t.Fatalf("同一封邮件两次转换结果不同（第 %d 次）—— 基准选取不稳：\n%q\nvs\n%q", i, first, got)
		}
	}
	// base 应当取到 14（小者），于是 20px 的「乙」仍是标题。
	if lv := headingOf(first, "乙"); lv == 0 {
		t.Errorf("并列时没取到较小的基准：20px 的「乙」应当仍是标题\n%s", first)
	}
}

// TestHTMLToMarkdown_BlockInInlineContextRenderedOnce 盯着一个曾经真实存在的
// bug：inlineNode 的 default 分支把 b.node(n) 写了两遍，于是「行内上下文里
// 冒出来的块级元素」会被渲染两次，内容在原处重复一遍。
func TestHTMLToMarkdown_BlockInInlineContextRenderedOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		kw   string
	}{
		{"span 里包 div", `<body><p>前</p><span><div>甲</div></span></body>`, "甲"},
		{"span 里包表格", `<body><p>前</p><span><table><tr><td>乙</td></tr></table></span></body>`, "乙"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HTMLToMarkdown(tc.in)
			if n := strings.Count(got, tc.kw); n != 1 {
				t.Errorf("%q 在结果里出现了 %d 次，应该是 1 次\n%s", tc.kw, n, got)
			}
		})
	}
}
