package tui

import "github.com/charmbracelet/lipgloss"

// Zen 的配色原则是**降低颜色数量**，而不是换一套更好看的颜色。
//
// Normal 模式有十来种颜色（发件人各有各的色、未读橙、在线绿、错误红……），
// 那是给「一眼扫过去找东西」用的 —— 列表界面要的是快速定位。Zen 是读和写，
// 颜色一多，注意力就从文字跑到界面上了。
//
// 所以这里只有三档灰、一个降饱和的错误色，外加一个几乎看不出的强调色。
// 全部走 AdaptiveColor：写死一个灰，在另一种背景的终端上不是糊掉就是刺眼
// （peerRowBg 那边踩过同一个坑，注释里记着）。
var (
	// zenText 是正文。刻意**不设任何前景色** —— 让终端自己的前景色透上来。
	// 这才是 terminal-native：用户把终端配成什么样，正文就是什么样，
	// 而不是我们强行糊一层网页式的黑字白底。
	zenText = lipgloss.NewStyle()

	// zenMeta 是「谁 · 什么时候」这类次级信息。
	zenMeta = lipgloss.NewStyle().Foreground(zenMetaFg)

	// zenFaint 是头部、状态提示这类最弱的信息。
	zenFaint = lipgloss.NewStyle().Foreground(zenFaintFg)

	// zenRule 是输入区上方那条分隔线。
	zenRule = lipgloss.NewStyle().Foreground(zenRuleFg)

	// zenError 是错误提示。降过饱和 —— 纯红在一个安静的界面里像警报灯。
	zenError = lipgloss.NewStyle().Foreground(zenErrorFg)

	// zenAccent 是唯一的强调色，只用在输入提示符那个 › 上。
	//
	// 一处。就一处。强调色的说服力来自稀缺性，用第二处就废了。
	zenAccent = lipgloss.NewStyle().Foreground(zenAccentFg)
)

// Zen 的三档灰。
//
// 浅色终端要更深、深色终端要更浅 —— 但两边都比正文弱一档，这是「层级」
// 而不是「颜色」。数字贴着各自的背景走，保证「看得见但不抢」。
var (
	zenMetaFg   = lipgloss.AdaptiveColor{Light: "242", Dark: "245"}
	zenFaintFg  = lipgloss.AdaptiveColor{Light: "248", Dark: "240"}
	zenRuleFg   = lipgloss.AdaptiveColor{Light: "252", Dark: "237"}
	zenErrorFg  = lipgloss.AdaptiveColor{Light: "131", Dark: "174"}
	zenAccentFg = lipgloss.AdaptiveColor{Light: "60", Dark: "109"}
)
