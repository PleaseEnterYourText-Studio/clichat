package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// mail.DisplayWidth 算出来的列数，必须和渲染器真的画出来的列数一样。
//
// # 为什么这条要跨包
//
// html2md 的分栏靠「把一格补到 N 列」实现，而 N 是 mail.DisplayWidth 量
// 出来的；屏幕上那一格到底占几列，则由这个包的渲染器决定。两边一旦分家，
// 补出来的空格就正好错在那些字符上 —— 而且错得很有规律，凡是含链接或
// 加粗的格子都会歪。分栏服务的对象（导航栏、页脚）几乎全是链接。
//
// # 为什么是差分判据
//
// 左边调 mail 的尺子，右边**让渲染器画出来再量**。这是刻意的：如果把
// 标记规则在测试里再抄一遍，实现和判据就会一起错，判据变成空壳。
//
// 它盯的是一类具体的错：mail 那份尺子照 Markdown **规范**去认标记，
// 而渲染器认的是自己那个子集。已知两处规范与子集不一致：
//
//   - 规范认 `_强调_`，渲染器不认（见 parseInline 的 switch 里没有 `_`）；
//   - 含反引号的行内代码，规范要求两侧留一个填充空格、且空格不算内容
//     （见 mail.codeSpanContent），渲染器还要额外要求闭合围栏**恰好**
//     等长（见 findFence）。
//
// 只要哪天有人照着规范去"修正" mail 那份尺子，这里立刻红。
func TestDisplayWidth_MatchesRenderedWidth(t *testing.T) {
	forceColor(t)

	for _, tc := range []struct {
		name string
		md   string
	}{
		{"纯文本", "极客商城"},
		{"加粗", "**张先生**"},
		{"斜体", "*2026-10-01*"},
		{"删除线", "~~旧价格~~"},
		{"行内代码", "`SF1234567890`"},
		{"代码内容含反引号", "`` a`b ``"},
		{"链接", "[新品](https://ex.com/new)"},
		{"链接文字是中文", "[订单中心](https://ex.com/invoice)"},
		{"链接文字里还有标记", "[**立即购买**](https://ex.com/buy)"},
		{"转义星号", `2\*3`},
		{"不成对的星号", "2*3"},
		{"下划线不是标记", "a_b_c"},
		{"下划线加粗也不是标记", "__重点__"},
		{"歧义宽度字符", "•——…①"},
		{"emoji", "🎉"},
		{"空串", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := renderMarkdown(tc.md, 200)
			if len(rows) != 1 {
				t.Fatalf("前提不成立：宽 200 不该折行，却得到 %d 行：%q", len(rows), rows)
			}
			plain := plainText(rows[0])

			// 用 lipgloss.Width 而不是 textWidth：textWidth 和
			// mail.DisplayWidth 共用同一个字符宽度尺子，拿它当裁判等于
			// 让被测的两方互相作证。lipgloss.Width 是布局实际用的那把
			// 尺子（padLeft / padRight 走的就是它），第三方口径。
			got := lipgloss.Width(plain)
			if want := mail.DisplayWidth(tc.md); got != want {
				t.Errorf("渲染出来 %d 列，mail.DisplayWidth 算 %d 列\n"+
					"源文：%q\n渲染：%q", got, want, tc.md, plain)
			}
		})
	}
}
