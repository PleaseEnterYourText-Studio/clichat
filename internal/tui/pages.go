// 这个文件装**整页**的两屏：解锁页与配置向导。
//
// 它们和别的界面不同：那两屏是「一屏只问一件事」，所以走的是**居中**
// 那一套 —— 字标 + 内容块整体摆在屏幕中间偏上。别的界面（列表 / 会话 /
// Zen）是信息密度优先，铺满整幅。
//
// # 为什么居中，以及为什么是「偏上」而不是「正中」
//
// 字符网格里没有圆角、没有对齐基线，"这一屏只问一件事"只能靠**空白围住**
// 来表达。至于偏上：人的视线落点在屏幕上方三分之一处，把块放在正中会显得
// 往下坠 —— 底部那截空白看起来像"下面还有东西没显示出来"。
//
// # 两层的宽度
//
// 每个页面都分两层：`pageWidth` 给出**内容列**（终端宽减去两侧留白，
// 上限 78），页面内部的块（字标、字段、列表）都居中到这一列；整块再由
// `centerPage` 居中到**终端**宽度。少了后面那一层，超宽终端上内容会全部
// 贴着左边 —— 前面那层只在窄终端上起作用。
package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

const (
	// pageMaxWidth 是这两屏内容列的最大宽度。
	//
	// 和 Zen 的正文列取同一个量级（78）：一行超过七八十个字符之后，
	// 眼睛从行尾回扫到下一行开头就开始费劲。
	pageMaxWidth = 78
	// pageMinWidth 是窄终端上的下限。再窄就不强行撑了，让它跟着屏幕走。
	pageMinWidth = 30
	// pageTopDivisor 决定块的上边距：可用高度的 1/3。
	//
	// 不是 1/2（正中）—— 见文件头。也不是固定几行：那样在高窗口里
	// 内容会贴着顶。
	pageTopDivisor = 3
	// logoGap 是字标与下面内容之间的空行数。
	logoGap = 2
	// pageFieldMaxWidth 是字段框（下划线的长度）的宽度上限。
	//
	// 78 列的下划线看着不像一个输入框，像一条分隔线。44 是"一个能打字的
	// 地方"的常见宽度，也不需要眼睛横着扫。
	pageFieldMaxWidth = 44
	// maxListRows 是页面上的列表最多列几行（账号、服务商）。
	//
	// 不设上限的话，存了十几个邮箱的配置会把密码栏挤出屏幕 —— 而密码栏
	// 才是那一屏要问的那件事。光标永远留在窗口里（见 windowAround）。
	maxListRows = 6
)

// pageWidth 是这两屏的内容列宽（终端列）。
func pageWidth(screen int) int {
	w := screen - 8
	if w > pageMaxWidth {
		w = pageMaxWidth
	}
	if w < pageMinWidth {
		w = screen
	}
	if w < 1 {
		w = 1
	}
	return w
}

// lineWidth 量「这一行在屏幕上占几列」，**认 ANSI**。
//
// ⚠️ 这两屏的行几乎全是上过色的（styleTitle / styleMuted / stylePrompt），
// 而**不能**拿 textWidth 去量：textWidth 逐 rune 求和，转义序列里的
// `[`、`1`、`m` 都会被当成三个可见字符。实测 styleTitle.Render("clichat")
// 在真终端里是 "\x1b[1mclichat\x1b[0m"，textWidth 给出 13，lipgloss.Width
// 给出 7 —— 差了 6 列，整行往左偏 3 列。
//
// 更麻烦的是它在测试里看不见：**lipgloss 在非 TTY 下根本不输出转义序列**，
// 于是不开色的测试跑出来 textWidth == lipgloss.Width，判据全绿，而用户
// 在自己的终端里看到的是歪的。守着这一点的判据必须自己把配色档位设出来
// （见 pages_test.go 的 styledLine），不能靠跑测试那台机器的环境。
//
// 用 lipgloss.Width 而不是「先 Strip 再用 textWidth」是因为 padRight /
// padLeft —— 本仓库的布局权威 —— 用的就是 lipgloss.Width。两处同一把尺子，
// 居中块和它右边的补白才对得齐。
func lineWidth(s string) int {
	return lipgloss.Width(s)
}

// centerLine 把一行摆到 width 列的中间，并补足到整宽。
//
// 宽度走 lineWidth（认 ANSI），不是 len：中文一个字两列，按字节数居中会把
// 中文行整体偏左；上过色的行更要按可见宽度算，转义序列不算列。
func centerLine(line string, width int) string {
	w := lineWidth(line)
	if w >= width {
		return line
	}
	left := (width - w) / 2
	return strings.Repeat(" ", left) + line + strings.Repeat(" ", width-w-left)
}

// centerBlock 把一个多行块**整体**居中到 width 列。
//
// 按块里最宽那一行算一次补白，所有行共用。不能逐行 centerLine：字标的
// 各行宽度本来就不一样（T 的顶横比竖笔宽），逐行居中会把它拆成参差不齐
// 的几段，整体看起来是歪的。
func centerBlock(lines []string, width int) []string {
	widest := 0
	for _, l := range lines {
		if w := lineWidth(l); w > widest {
			widest = w
		}
	}
	if widest >= width {
		return lines
	}
	pad := strings.Repeat(" ", (width-widest)/2)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = pad + l
	}
	return out
}

// centerPage 把一个块垂直摆到屏幕的偏上位置，并整体居中到整幅。
//
// block 应当**已经**按内容列居中过（见文件头那两层宽度）；这里只做
// 「内容列整体在终端里居中」和「上边距」两件事。
//
// 块比屏幕还高时**从顶部开始**（不裁切）：内容比屏幕长的时候，用户要看的
// 是开头那几行 —— 把它居中会把开头挤出屏幕。
func centerPage(block []string, screenWidth, height int) string {
	if screenWidth <= 0 || height <= 0 {
		return strings.Join(block, "\n")
	}
	centered := centerBlock(block, screenWidth)

	padTop := 0
	if len(centered) < height {
		padTop = (height - len(centered)) / pageTopDivisor
	}
	out := make([]string, 0, len(centered)+padTop)
	for i := 0; i < padTop; i++ {
		out = append(out, "")
	}
	out = append(out, centered...)
	return strings.Join(out, "\n")
}

// pageLogo 返回要画在页首的字标（已经居中到 width）。
//
// 用的是 Zen 首页那一个：figlet 的 ANSI Shadow 框线字。窄到放不下时
// zenLogo 会退成「字母 + 空格」的一行 —— 装饰不该撑破版面。
//
// ⚠️ width 要传**内容列宽**（pageWidth），不是终端宽。传终端宽的话
// zenLogo 会以为放得下、画出 52 列的字标，而内容列只有 40 列。
func pageLogo(width int) []string {
	return centerBlock(zenLogo(width), width)
}

// pageTitle 是页首那行标题，跟着字标一起居中。
func pageTitle(text string, width int) string {
	return centerLine(styleTitle.Render(text), width)
}

// pageHints 把一行提示居中。
//
// 文案从 hintLine 来（见 help.go）—— 这一屏的键住在各自的处理函数里，
// 但**文案只有一份**：本仓库已经为「两处各写一份」栽过（底部提示和帮助页
// 各写一份，`a` 键丢了）。
func pageHints(width int, text string) string {
	if text == "" {
		return ""
	}
	return centerLine(styleMuted.Render(truncate(text, width)), width)
}

// fieldBoxWidth 算字段框（下划线的长度）该多宽。
//
// 上限 44 是「一个能打字的地方」的样子；标签比它还宽时把框撑开 ——
// 下划线短于它上面那行字，看着像排版事故。
func fieldBoxWidth(label, trailing string, width int) int {
	box := width - 2
	if box > pageFieldMaxWidth {
		box = pageFieldMaxWidth
	}
	if need := lineWidth(label) + lineWidth(trailing); need > box {
		box = need
	}
	if box > width {
		box = width
	}
	if box < 1 {
		box = 1
	}
	return box
}

// pageField 画一个字段：标签行（可带右侧提示）/ 输入区 / 下划线，整块居中。
//
// 下划线是这一屏唯一的"可交互"信号：字符网格里没有边框、没有阴影，
// 一条线加上面那行标签就是"这里能打字"。反过来说，**别的文字都不带线** ——
// 线一旦到处都是，它就不再表示"可以输入"。
//
// ⚠️ 它是**方法**而不是自由函数，因为它要顺手把输入框收进框宽（setInputBox）：
// 框宽取决于标签和右侧提示，而只有这一处同时知道这两样。让调用方自己算一遍
// 就等于把「标签换了、框宽忘了跟着换」这条留给了以后。
//
// trailing 是右对齐的提示（如「Ctrl+P 显示」）；框里放不下时退到单独一行，
// 不硬挤。
func (m Model) pageField(label, trailing string, width int) []string {
	lab := styleTitle.Render(label)
	box := fieldBoxWidth(lab, trailing, width)
	m.setInputBox(box)

	var lines []string
	if trailing == "" {
		lines = append(lines, lab)
	} else {
		tip := styleMuted.Render(trailing)
		if gap := box - lineWidth(lab) - lineWidth(tip); gap >= 2 {
			lines = append(lines, lab+strings.Repeat(" ", gap)+tip)
		} else {
			lines = append(lines, lab, tip)
		}
	}

	// 输入区左边留一格：光标贴着框边时看不出底下还有地方可写。
	lines = append(lines, stylePrompt.Render(padRight(" "+m.input.View(), box)))
	lines = append(lines, styleMuted.Render(strings.Repeat("─", box)))
	return centerBlock(lines, width)
}

// pageNote 是字段下面的一行说明，居中。空串返回空行（调用方照样 append，
// 省得每处都判一次）。
func pageNote(text string, width int) string {
	if text == "" {
		return ""
	}
	return centerLine(styleMuted.Render(truncate(text, width)), width)
}

// pageError 是错误行，居中，用错误色。
func pageError(text string, width int) string {
	if text == "" {
		return ""
	}
	return centerLine(styleError.Render(truncate(text, width)), width)
}

// windowAround 返回 [lo, hi) —— 一段长度不超过 max 的下标窗口，且一定
// 包含 idx。
//
// 光标先居中再往两端夹：走到列表头尾时光标不贴边。贴边的话用户看不出
// 上面/下面还有东西 —— 那和「列表就这么长」是同一幅画面。
func windowAround(n, idx, max int) (int, int) {
	if max <= 0 || n <= max {
		return 0, n
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	lo := idx - max/2
	if lo < 0 {
		lo = 0
	}
	if lo+max > n {
		lo = n - max
	}
	return lo, lo + max
}

// joinBlocks 把若干块（每个是 []string）按顺序接起来，块之间插 gap 个空行。
func joinBlocks(gap int, blocks ...[]string) []string {
	var out []string
	for i, b := range blocks {
		if i > 0 && gap > 0 {
			for j := 0; j < gap; j++ {
				out = append(out, "")
			}
		}
		out = append(out, b...)
	}
	return out
}

// blankLines 返回 n 个空行。n <= 0 时返回 nil。
func blankLines(n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, n)
	return out
}

// listBlock 把一个「列表块」（字符串切片）居中，跳过超出窗口的那些。
//
// rows 是**未居中**的行；这里只做居中一件事，窗口由调用方用 windowAround
// 切好 —— 两件事混在一起，改一处就容易忘另一处。
func listBlock(rows []string, width int) []string {
	return centerBlock(rows, width)
}

// elideMiddle 把过长的字符串从**中间**收掉，两端都留着。
//
// 账号列表里放的是邮箱地址，而地址的两端各自都是有信息的那一半：左边是
// 「谁」、右边是「哪家服务商」。直接用 truncate 从末尾砍会把 @域名 整个削掉
// —— 一屏账号全变成 "a.very.long.address@some-very…"，看上去一模一样，
// 而"选哪个账号"恰恰是这一屏唯一要问的事。
//
// 宽度按 textWidth 判、按 cellWidth 切（都是仓库里那把「把 Ambiguous 钉成
// 1 列」的尺子）；输出宽度 ≤ width，即使在拿 lipgloss.Width 量的时候也一样
// —— textWidth ≥ lipgloss.Width，所以宁可早收一格，不会捅穿版面。
func elideMiddle(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if textWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	// 中间那个省略号自己占 1 列，剩下的左右平分。
	left := (width - 1) / 2
	right := width - 1 - left
	return takeCols(s, left) + "…" + takeColsRight(s, right)
}

// takeCols 取字符串**开头**不超过 width 列的那一段。
func takeCols(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		cw := cellWidth(r)
		if w+cw > width {
			break
		}
		b.WriteRune(r)
		w += cw
	}
	return b.String()
}

// takeColsRight 取字符串**末尾**不超过 width 列的那一段。
//
// 逐 rune 从右往左量：只有逐字符的宽度才答得上「从哪个字符开始取」。
func takeColsRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	rs := []rune(s)
	w := 0
	i := len(rs)
	for i > 0 {
		cw := cellWidth(rs[i-1])
		if w+cw > width {
			break
		}
		w += cw
		i--
	}
	return string(rs[i:])
}

// ---- 解锁页 ----

// unlockState 是解锁页的状态。
type unlockState struct {
	// idx 是账号列表里的光标下标（不是邮箱地址）。
	idx int
	// reveal 为真时密码栏显示明文。
	//
	// 授权码和应用专用密码都是几十位的随机串，掩码状态下**抄错一个字符
	// 看不出来**，而报出来的是「认证失败」。给一个显式的切换键，比让用户
	// 怀疑自己是不是多打了一位便宜。
	reveal bool
	// forgetConfirm 为真表示「删掉这个账号」已经按过一次。
	//
	// 删本地凭据是不可逆的（服务器上的邮件一条不动，但那串授权码要重新
	// 去服务商那儿拿一遍），所以做成按两次：第一次只把后果说清楚。
	forgetConfirm bool
}

// unlockHintKeys 是解锁页底部的键与顺序。
//
// **顺序是设计过的**：一行放不下时 truncate 从末尾砍，最该被看到的靠前。
// 「换账号」只在真的存了多个账号时才出现 —— 提示里摆一个按了没反应的功能
// 比不摆更坑人（本仓库的 a 键就是这么丢过一次的）。
//
// short 手写而不是从 helpSections 派生：「回车」在帮助页里的说明是
// 「解锁」，这里正好一致，但同一个键在**配置向导**里是「下一步」——
// 派生只在含义恰好相同时才对。键名仍由 TestPageHints_KeysExistInHelp 钉在
// helpSections 上，杜绝「提示里有个不存在的功能」。
func (m Model) unlockHintKeys() []hintKey {
	keys := []hintKey{{"回车", "解锁"}, {"Ctrl+P", "显示密码"}}
	if len(m.knownProfiles()) > 1 {
		keys = append(keys, hintKey{"↑ / ↓", "换账号"})
	}
	return append(keys,
		hintKey{"Ctrl+N", "添加账号"},
		hintKey{"Ctrl+R", "重配这个账号"},
		hintKey{"Ctrl+X", "删掉账号"},
		hintKey{"Ctrl+C", "退出"},
	)
}

// knownProfiles 返回已保存的账号；cfg 缺失时给空表。
//
// 不是多余的防御：渲染函数会在测试里被单独调用，那里不一定装配了 cfg。
// 让一屏的渲染因为一个 nil 崩掉，换来的是「整页判据没法写」。
func (m Model) knownProfiles() []config.Profile {
	if m.cfg == nil {
		return nil
	}
	return m.cfg.Profiles()
}

// setInputBox 把输入框的显示宽度收到字段框里。
//
// 必须让 textinput 自己知道宽度，不能等渲染时截断：View() 的输出带 ANSI，
// 截断会把它切碎（转义序列截一半，后面的颜色全乱）。textinput 内部按
// 宽度滚动是唯一安全的做法。
//
// ⚠️ 这里是**唯一**兜住"占位文字不撑破字段框"的地方 —— 别再另加一层渲染期
// 裁剪。占位文字本身确实可以比 Width 长（客户端 id 那一步的
// "xxxxxxxx.apps.googleusercontent.com" 实测 35 列），但 textinput v1.0.0 的
// placeholderView 里本来就有一句 `ansi.Truncate(rest, width, "…")`：只要
// Width > 0，View() 返回的就正好是 Width 列（实测 26 / 32 / 42，对应 30 / 44 /
// 200 列的屏）。真正会出事的只有 **Width == 0** 那条分支 —— 那时占位文字可以
// 任意长（实测 35 列原样吐出）。
//
// 反过来说，若在外面再套一层 `truncate(m.input.Placeholder, m.input.Width)`，
// 一旦 Width 是 0 就会把整句提示压成**一个 "…"**（实测宽 1）：爆版没了，但
// "Width 忘了设"这个真错被换成了一个更隐蔽的坏结果，超宽判据反而抓不到它。
func (m *Model) setInputBox(box int) {
	w := box - 2 // 左边留一格给光标，右边留一格别贴着线
	if w < 4 {
		w = 4
	}
	m.input.Width = w
}

// hardWrap 把一串**没有断点**的文本按列硬折行。
//
// 授权地址就是这种文本：两百来个字符一整串，空格一个都没有，词的边界无从
// 谈起 —— 按词折行的那些函数在这儿要么原样返回、要么折在 `&` 上。
//
// width <= 0 时原样返回一行：调用方给的宽度算错了也不该让内容消失。
func hardWrap(s string, width int) []string {
	if width <= 0 || textWidth(s) <= width {
		return []string{s}
	}
	var out []string
	var b strings.Builder
	w := 0
	for _, r := range s {
		cw := cellWidth(r)
		if w+cw > width {
			out = append(out, b.String())
			b.Reset()
			w = 0
		}
		b.WriteRune(r)
		w += cw
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// linkLines 打出**完整**的链接：折行，不截断。
//
// 截断一个地址等于把"拷走它"这条路堵死 —— 而向导里给出地址的唯一用处就是
// 让用户能拿到它（SSH 过去的时候浏览器不在本机，只能靠复制粘贴）。
//
// 每一段都用**完整的**地址做超链接目标（hyperlink 的 text 与 target 是两个
// 参数），所以点哪一段都是打开同一个地方；分成几行只是版面的事。
func linkLines(prefix, url string, width int) []string {
	chunks := hardWrap(url, width-textWidth(prefix))
	out := make([]string, 0, len(chunks))
	for i, c := range chunks {
		lead := strings.Repeat(" ", textWidth(prefix))
		if i == 0 {
			lead = prefix
		}
		out = append(out, lead+styleLink.Render(hyperlink(url, c)))
	}
	return out
}

// unlockBlock 拼出解锁页的内容列块（已居中到 cw）。
func (m Model) unlockBlock(cw int) []string {
	var out []string
	out = append(out, pageLogo(cw)...)
	out = append(out, blankLines(logoGap)...)

	profiles := m.knownProfiles()
	if len(profiles) > 0 {
		out = append(out, centerLine(styleMuted.Render("账号"), cw))
		out = append(out, m.accountRows(profiles, cw)...)
		out = append(out, "")
	}

	out = append(out, m.pageField("主密码", revealHint(m.unlock.reveal), cw)...)

	if m.status != "" {
		out = append(out, "")
		out = append(out, pageError(m.status, cw))
	}
	if m.unlock.forgetConfirm {
		out = append(out, "")
		out = append(out, pageError("再按一次 Ctrl+X：删掉这个账号在本机保存的凭据", cw))
	}

	out = append(out, "")
	out = append(out, pageHints(cw, hintLine(m.unlockHintKeys())))
	return out
}

// accountRows 画账号列表。
//
// 单账号时也画那个 ▸ 标记：它表示「这一次解锁的是这个账号」。两种情况
// 共用一条代码路径，省得只在多账号时才被发现的分支。
func (m Model) accountRows(profiles []config.Profile, cw int) []string {
	lo, hi := windowAround(len(profiles), m.unlock.idx, maxListRows)
	rows := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		// 地址可以长到几十列（"名.姓@很长很长的公司域名.com"），窄屏上会
		// 直接捅穿内容列。留两格给左边那个标记（"▸ " / "  "）。
		email := elideMiddle(profiles[i].Account.Email, cw-2)
		if i == m.unlock.idx {
			rows = append(rows, styleSelected.Render("▸ "+email))
			continue
		}
		rows = append(rows, "  "+email)
	}
	return listBlock(rows, cw)
}

// revealHint 是密码栏右侧那个切换提示。
//
// ⚠️ 改键位时这里和 handleUnlockKey / handleSetupKey 的 switch **同时**改：
// 提示上写着 Ctrl+P、按下去没反应是最难查的一类问题。
func revealHint(revealed bool) string {
	if revealed {
		return "Ctrl+P 隐藏"
	}
	return "Ctrl+P 显示"
}

// viewUnlock 是解锁页。
func (m Model) viewUnlock() string {
	cw := pageWidth(m.width)
	return centerPage(m.unlockBlock(cw), m.width, m.height)
}

// activeIndex 是活动账号在列表里的下标。
//
// 启动时把光标停在**上一次用的**那个账号上：多账号的人绝大多数时候只用
// 其中一个，每次启动都要往下按一格是纯粹的摩擦。找不到（配置被改坏、
// Active 指向一个不存在的账号）就落到第一个。
func activeIndex(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	for i, p := range cfg.Profiles() {
		if p.Account.Email == cfg.Account.Email {
			return i
		}
	}
	return 0
}

// applyPageInputStyle 让输入框回到「整页」的口径。
//
// 这两屏把输入框嵌进自己的字段框里（标签 + 下划线），提示符属于那个框，
// 不属于输入框 —— 留着 "> " 的话，字段里会同时出现两套"这里可以打字"的
// 记号，而且下划线会被顶歪。
//
// ⚠️ 离开这两屏时必须还原（见 attach 里的 applyInputStyle）：不清不还原的话
// 会话输入框前面那个 "> " 就永远消失了。
func (m *Model) applyPageInputStyle() {
	m.input.Prompt = ""
	m.input.PromptStyle = lipgloss.NewStyle()
}

// applyReveal 把密码栏的明文开关落到输入框上。
//
// 两个页面共用（解锁页与向导的密码/主密码步骤）。开关本身各存各的
// （m.unlock.reveal / m.setup.reveal），但改 EchoMode 这一下必须是同一份
// 代码 —— 两处各写一遍，迟早有一处忘了改回去，密码就一直是明文。
func (m *Model) applyReveal(reveal bool) {
	if reveal {
		m.input.EchoMode = textinput.EchoNormal
		return
	}
	m.input.EchoMode = textinput.EchoPassword
}

// moveAccount 在账号列表上挪光标。
//
// ⚠️ 键位只能是方向键和 Tab，**不能用 j/k**：这一屏的输入框里正在敲的是
// 密码，密码里出现 j、k 是家常便饭，把它们当快捷键等于吃掉用户输入的字符。
func (m Model) moveAccount(key string) (tea.Model, tea.Cmd) {
	n := len(m.knownProfiles())
	if n == 0 {
		return m, nil
	}
	switch key {
	case "up", "shift+tab":
		m.unlock.idx--
	case "down", "tab":
		m.unlock.idx++
	}
	// 首尾回环：账号通常只有两三个，让 ↓ 从头绕到尾比按不动更省事。
	m.unlock.idx = ((m.unlock.idx % n) + n) % n
	m.unlock.forgetConfirm = false
	return m, nil
}

// handleUnlockKey 处理解锁页的按键。
func (m Model) handleUnlockKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "enter":
		return m.unlockSubmit()

	case "ctrl+p":
		m.unlock.reveal = !m.unlock.reveal
		m.applyReveal(m.unlock.reveal)
		return m, nil

	case "up", "down", "tab", "shift+tab":
		return m.moveAccount(msg.String())

	case "ctrl+n", "ctrl+r":
		// 添加账号 / 重配当前账号。**不动**已有配置与凭据（见 beginSetup
		// 的注释）；向导走完按邮箱 Upsert，填了同一个邮箱就是重配 ——
		// 所以这两个键本来就是同一个动作，帮助页和这一屏的提示分了两个，
		// 只是为了让用户按自己心里那个词去按。
		//
		// ⚠️ ctrl+r 原先只写在帮助页和这一屏的提示里，handleUnlockKey 里
		// 根本没有这个分支 —— 于是它落进「其余按键进输入框」，按下去
		// 什么都不会发生。提示了一个不存在的功能，比少一个键更坑人：
		// 用户会反复按，然后认为整个程序是坏的。
		m.status = ""
		m.beginSetup()
		return m, nil

	case "ctrl+x":
		return m.forgetCurrentAccount()
	}

	// 其余按键进输入框。
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// 一打字就把上一条提示收掉，以及那次「再按一次」的待确认状态 ——
	// 用户已经在做别的事了，别让两轮之前的警告一直挂着。
	m.status = ""
	m.unlock.forgetConfirm = false
	return m, cmd
}

// unlockSubmit 校验主密码并连接。
func (m Model) unlockSubmit() (tea.Model, tea.Cmd) {
	password := m.input.Value()
	if password == "" {
		return m, nil
	}

	// 光标选中的那个账号就是这一次要解锁的账号。切过去并落盘 ——
	// 「上一次登录的账号」就是这么记住的（下次启动光标还停在这儿）。
	if msg := m.rememberSelectedAccount(); msg != "" {
		m.status = msg
		return m, nil
	}

	// 记下主密码：OAuth2 的 token 刷新之后要重新加密写回
	// credentials.enc，而那份文件就是用它加密的。**只在内存里**。
	m.master = password
	creds, err := config.LoadCredentials(password)
	if err != nil {
		if errors.Is(err, config.ErrWrongPassword) {
			m.status = "主密码错误"
		} else {
			m.status = "读取凭据失败：" + shortErr(err)
		}
		m.input.SetValue("")
		return m, nil
	}
	if err := m.connect(creds); err != nil {
		m.status = "连接失败：" + shortErr(err)
		return m, nil
	}
	m.status = ""
	return m, tea.Batch(m.syncCmd(), m.pollCmd(), textinput.Blink)
}

// rememberSelectedAccount 把光标停着的那个账号设为活动账号并落盘。
//
// 「上一次登录的账号」就是这么记住的：下次启动光标还停在这儿，而绝大多数人
// 绝大多数时候只用其中一个邮箱，每次都要往下按一格是纯粹的摩擦。
//
// 返回空串表示成功，否则是**可以显示给用户看**的一句话。存不下来只是「下次
// 启动光标停在上一个账号」，不影响这次解锁 —— 所以它是一条提示，不是致命
// 错误，调用方不该拿它挡路。
//
// 单独抽出来是为了能测：它的调用点在 unlockSubmit 里，而那条路下一件事
// 就是连服务器。「切了账号有没有记住」这条判据不该被迫去真连一个邮箱。
func (m *Model) rememberSelectedAccount() string {
	profiles := m.knownProfiles()
	if len(profiles) == 0 {
		return ""
	}
	m.unlock.idx = ((m.unlock.idx % len(profiles)) + len(profiles)) % len(profiles)
	email := profiles[m.unlock.idx].Account.Email
	if email == m.cfg.Account.Email {
		return ""
	}
	if !m.cfg.SetActive(email) {
		return "这个账号已经不在配置里了"
	}
	if err := m.cfg.Save(); err != nil {
		return "记住了账号但没写进配置：" + shortErr(err)
	}
	return ""
}

// forgetCurrentAccount 删掉当前账号在本机的东西（两次确认）。
//
// 只删**本机**的配置与凭据：服务器上的邮件一条不动，所以这个操作不会
// 让别人少收到信。邮件索引与正文缓存也留着 —— 重新把账号加回来时它们
// 还能用，而重下一遍 311 封邮件是几分钟的事。
//
// 两次确认的做法：第一次只是把后果说清楚（见 unlockState.forgetConfirm），
// 不像 modeConfirm 那样弹一个框 —— 这一屏没有别的可操作对象，一个全屏
// 确认框比一行红字重得多。
//
// 要主密码：凭据文件是加密的，改它得先解开它。这里**只认输入框里那一份**，
// 不看 m.master —— 解锁页出现时 m.master 一定是空的（还没连上），多一条
// 「本次会话已经解锁过」的分支就是多一条错误处理，而它永远不会被执行。
func (m Model) forgetCurrentAccount() (tea.Model, tea.Cmd) {
	profiles := m.knownProfiles()
	if len(profiles) == 0 {
		return m, nil
	}
	m.unlock.idx = ((m.unlock.idx % len(profiles)) + len(profiles)) % len(profiles)
	if !m.unlock.forgetConfirm {
		m.unlock.forgetConfirm = true
		m.status = ""
		return m, nil
	}
	m.unlock.forgetConfirm = false

	email := profiles[m.unlock.idx].Account.Email

	master := m.input.Value()
	if master == "" {
		m.status = "先在下面输入主密码，再按 Ctrl+X"
		return m, nil
	}
	// 凭据那一份先读出来再改：不读的话就是「从零造一份写回去」，会把
	// 别的账号的授权码一起抹掉。
	creds, err := config.LoadCredentials(master)
	if err != nil {
		if errors.Is(err, config.ErrWrongPassword) {
			m.status = "主密码错误"
		} else {
			m.status = "读取凭据失败：" + shortErr(err)
		}
		return m, nil
	}
	creds.ForgetAccount(email)
	if err := config.SaveCredentials(master, creds); err != nil {
		m.status = "删掉凭据失败：" + shortErr(err)
		return m, nil
	}

	m.cfg.RemoveProfile(email)
	if err := m.cfg.Save(); err != nil {
		m.status = "删掉账号失败：" + shortErr(err)
		return m, nil
	}

	m.input.SetValue("")
	if len(m.cfg.Profiles()) == 0 {
		// 一个账号都不剩了，就是「从没配置过」的状态：凭据文件里也
		// 没有别的东西了，一起清掉，直接进向导。
		_ = config.DeleteCredentials()
		m.cfg = config.Default()
		m.master = ""
		m.creds = config.Credentials{}
		m.status = ""
		m.beginSetup()
		return m, nil
	}

	m.unlock.idx = activeIndex(m.cfg)
	m.status = "已删掉 " + email + " 在本机的配置与凭据"
	return m, nil
}

// ---- 配置向导 ----

// setupHintKeys 是配置向导底部的键与顺序，按当前步骤给。
//
// 键随步骤变化而不是固定一行：选服务商那步 ↑/↓ 有用、填邮箱那步没用；
// 密码那步有 Ctrl+P、别的没有。列出一个当前按了没反应的键，等于让用户
// 去怀疑自己的键盘。
func (m Model) setupHintKeys() []hintKey {
	switch m.setup.step {
	case stepProvider, stepCustomTLS:
		return []hintKey{{"↑ / ↓", "选择"}, {"回车", "确定"}, {"Ctrl+C", "退出"}}
	case stepOAuthWait:
		return []hintKey{{"Esc", "取消授权"}, {"Ctrl+C", "退出"}}
	case stepOAuthClient:
		// 「Esc 改填密码」只在两条路都走得通时存在。给必须走 OAuth2 的
		// 服务商列出这个键，等于骗用户去按一个不会有反应的键。
		if m.oauthOptional() {
			return []hintKey{{"回车", "开始授权"}, {"Esc", "改填密码"}, {"Ctrl+C", "退出"}}
		}
		return []hintKey{{"回车", "开始授权"}, {"Ctrl+C", "退出"}}
	case stepPassword:
		keys := []hintKey{{"回车", "下一步"}, {"Ctrl+P", ""}}
		if m.oauthOptional() {
			keys = append(keys, hintKey{"Ctrl+O", "改用 OAuth2"})
		}
		return append(keys, hintKey{"Ctrl+C", "退出"})
	case stepMaster:
		return []hintKey{{"回车", "下一步"}, {"Ctrl+P", ""}, {"Ctrl+C", "退出"}}
	}
	return []hintKey{{"回车", "下一步"}, {"Ctrl+C", "退出"}}
}

// viewSetup 是配置向导。
func (m Model) viewSetup() string {
	cw := pageWidth(m.width)

	block := m.setupBlock(cw)
	// 装不下就把字标收起来：向导里真正要说的话（服务商说明、凭据步骤）
	// 比装饰重要。留出提示行和错误行的位置再判断。
	if m.height > 0 && len(block)+2 > m.height {
		block = m.setupBlockNoLogo(cw)
	}
	return centerPage(block, m.width, m.height)
}

// setupBlock 拼出向导的内容块（带字标）。
func (m Model) setupBlock(cw int) []string {
	var out []string
	out = append(out, pageLogo(cw)...)
	out = append(out, blankLines(logoGap)...)
	out = append(out, m.setupBody(cw)...)
	return out
}

// setupBlockNoLogo 是不带字标的那一版，窄/矮终端上用。
func (m Model) setupBlockNoLogo(cw int) []string {
	return m.setupBody(cw)
}

// setupBody 是向导每一步的本体（不含字标、不含底部提示）。
func (m Model) setupBody(cw int) []string {
	var out []string

	switch m.setup.step {
	case stepProvider:
		out = append(out, pageTitle(m.stepHeader("选服务商"), cw), "")
		out = append(out, m.providerRows(cw)...)
		if p := m.currentProvider(); p != nil {
			out = append(out, "")
			out = append(out, credentialHelpLines(p, cw)...)
		}

	case stepCustomTLS:
		out = append(out, pageTitle(m.stepHeader("加密方式"), cw), "")
		out = append(out, m.tlsRows(cw)...)

	case stepEmail:
		out = append(out, m.pageField(m.stepHeader("邮箱地址"), "", cw)...)

	case stepCustomIMAP:
		out = append(out, m.pageField(m.stepHeader("IMAP 服务器"), "", cw)...)
		out = append(out, "")
		out = append(out, pageNote("主机:端口，例如 imap.example.com:993", cw))
		out = append(out, pageNote(fmt.Sprintf(
			"已按你的邮箱域名预填；省略端口时默认 %d", config.DefaultIMAPPort), cw))

	case stepCustomSMTP:
		out = append(out, m.pageField(m.stepHeader("SMTP 服务器"), "", cw)...)
		out = append(out, "")
		out = append(out, pageNote("主机:端口，例如 smtp.example.com:465", cw))
		out = append(out, pageNote(fmt.Sprintf(
			"已按你的邮箱域名预填；省略端口时默认 %d", config.DefaultSMTPPort), cw))

	case stepPassword:
		p := m.currentProvider()
		out = append(out, m.pageField(m.stepHeader(m.passwordStepTitle(p)),
			revealHint(m.setup.reveal), cw)...)
		// 两条路都走得通的服务商在**这一屏**就把另一个选项说出来。
		// 只写进底部提示是不够的：底部那行会在窄终端上被截断，而这个键
		// 关系到"用户能不能填一个他根本没拿到的密码"。
		if m.oauthOptional() {
			out = append(out, "")
			out = append(out, pageNote("也可以按 Ctrl+O 改用 OAuth2 授权（不必去生成应用专用密码）", cw))
		}
		if p != nil && len(p.Guide) > 0 {
			out = append(out, "")
			out = append(out, credentialHelpLines(p, cw)...)
		}

	case stepMaster:
		out = append(out, m.pageField(m.stepHeader("主密码"),
			revealHint(m.setup.reveal), cw)...)
		out = append(out, "")
		out = append(out, pageNote("凭据用这个密码加密后存在本地，不会发送到任何地方。", cw))
		out = append(out, pageNote("它只管本机这一个文件；忘了就只能重新配置。", cw))

	case stepOAuthClient:
		out = append(out, m.pageField(m.stepHeader("OAuth 客户端 ID"), "", cw)...)
		out = append(out, "")
		out = append(out, m.oauthClientNotes(cw)...)

	case stepOAuthWait:
		out = append(out, pageTitle(m.stepHeader("浏览器里点同意"), cw), "")
		out = append(out, m.oauthWaitLines(cw)...)
	}

	if m.setup.err != "" {
		out = append(out, "", pageError(m.setup.err, cw))
	}
	out = append(out, "", pageHints(cw, hintLine(m.setupHintKeys())))
	return out
}

// passwordStepTitle 是「要凭据」那一步的标题。
func (m Model) passwordStepTitle(p *config.Provider) string {
	if p == nil {
		return "凭据"
	}
	return p.Name + " 的凭据"
}

// providerRows 画服务商列表（带窗口，光标永远可见）。
func (m Model) providerRows(cw int) []string {
	lo, hi := windowAround(len(config.Providers), m.setup.provider, maxListRows)
	rows := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		rows = append(rows, choiceRow(config.Providers[i].Name, i == m.setup.provider, 0))
	}
	return listBlock(rows, cw)
}

// tlsRows 画加密方式列表。
func (m Model) tlsRows(cw int) []string {
	rows := make([]string, 0, len(tlsOptions))
	for i, opt := range tlsOptions {
		rows = append(rows, choiceRow(opt.Label, i == m.setup.tls, 0))
	}
	return listBlock(rows, cw)
}

// choiceRow 画列表里的一行：选中时反白并带 ▸。
//
// pad 是给名字补到统一宽度用的（0 表示不补）。补与不补要按调用方定：
// 服务商名字长短差很多，不补的话那一列会参差；单个选项的列表（加密方式
// 只有两项）不补更自然。
func choiceRow(name string, selected bool, pad int) string {
	// pad 用**显示宽度**补，不是 %-22s —— 名字里有中文（「QQ 邮箱」），
	// 按字节补会把中文行补短 4 列。
	label := name
	if pad > 0 {
		label = padRight(name, pad)
	}
	if selected {
		return styleSelected.Render("▸ " + label)
	}
	return "  " + label
}

// credentialHelpLines 渲染「去哪拿凭据」这一段：直达链接 + 分步说明。
//
// 链接用 OSC 8 包成可点击的，在支持的终端里直接点开就能去拿授权码。
// 约束见 styles.go 里 hyperlink 的注释：样式不能带属性，否则序列会被切碎。
//
// 文字步骤同时保留作为退路：链接会失效（服务商改版），步骤不会。这是配置
// 向导里最容易卡住的一步，把直达链接摆出来能省掉用户自己找入口。
//
// ⚠️ 地址**折行**而不是截断（linkLines）：这两条链接就是"去这里拿"的全部
// 内容，截掉一半等于没给。文字步骤那种说明性长句截断可以接受（后半句是
// 补充），地址不行。
//
// 每行**先截断再上色**（本仓库的硬约束：截断会破坏 ANSI 序列），最后整块
// 居中 —— 逐行居中会把编号步骤的左侧缩进拆散。
func credentialHelpLines(p *config.Provider, cw int) []string {
	if p == nil {
		return nil
	}

	var rows []string
	if p.OpenURL != "" {
		rows = append(rows, truncate("直接去这里拿", cw))
		rows = append(rows, linkLines("  ", p.OpenURL, cw)...)
		if p.HelpURL != "" {
			rows = append(rows, linkLines("  官方说明：", p.HelpURL, cw)...)
		}
	}

	rows = append(rows, truncate("获取"+p.PasswordLabel, cw))
	for i, g := range p.Guide {
		rows = append(rows, truncate(fmt.Sprintf("  %d. %s", i+1, g), cw))
	}

	// 发信的坑单独一段：端口/加密组合、发信量上限、用户名要不要带域名 ——
	// 它们通常在**收信已经通了之后**才暴露（能收不能发）。
	if p.SMTPNote != "" {
		rows = append(rows, "", truncate("发信注意", cw))
		rows = append(rows, truncate("  "+p.SMTPNote, cw))
	}
	return listBlock(rows, cw)
}

// oauthClientNotes 是「填 client id」那一步的说明。
//
// 这一步存在的理由只有一个：Google 和 Microsoft **不允许**客户端内置
// client secret（内置等于公开，谁都能拿去冒充这个应用），所以用户必须
// 去自己的控制台注册一个应用、把 client id 填进来。不把这件事说透，
// 用户会对着一个连不上的账号反复输密码。
func (m Model) oauthClientNotes(cw int) []string {
	p := m.currentProvider()

	var rows []string
	rows = append(rows, truncate("这一栏填的是你自己注册的应用的 client id。", cw))
	if m.oauthOptional() {
		// 这一家的密码那条路本来是通的 —— 得说清楚"不走这里也行"，
		// 否则用户会以为不注册应用就没法用，白跑一趟控制台。
		rows = append(rows, truncate("这家也支持应用专用密码；不想注册应用就按 Esc 退回去填。", cw))
	} else {
		rows = append(rows, truncate("服务商不接受密码登录，只能走这一步。", cw))
	}
	if p != nil && p.OAuthURL != "" {
		rows = append(rows, "", truncate("怎么注册", cw))
		rows = append(rows, linkLines("  ", p.OAuthURL, cw)...)
	}
	rows = append(rows, "", truncate("回调地址填这个", cw))
	rows = append(rows, truncate("  http://localhost", cw))
	rows = append(rows, pageNote("（端口不用填，服务商会忽略它）", cw))
	return listBlock(rows, cw)
}

// oauthWaitLines 是「等用户在浏览器里点同意」那一步的内容。
//
// 地址必须**完整打出来**，不能只给个"已打开浏览器"：SSH 过去的时候浏览器
// 根本不在本机，用户只能自己把那串地址拷走。可点击链接是附加的方便，
// 不是替代品 —— 所以这里是折行（linkLines）而不是截断，两百来个字符的
// 授权地址在任何终端宽度下都放不进一行。
func (m Model) oauthWaitLines(cw int) []string {
	var rows []string
	rows = append(rows, truncate("已经在浏览器里打开授权页面。", cw))
	rows = append(rows, truncate("如果没打开，手动访问下面这个地址：", cw))
	rows = append(rows, "")

	if m.setup.flow != nil {
		u := m.setup.flow.AuthURL()
		rows = append(rows, linkLines("  ", u, cw)...)
		rows = append(rows, "")
		rows = append(rows, truncate("同意之后会自动回到这里，不用手动复制任何东西。", cw))
		rows = append(rows, truncate("回调地址是 "+m.setup.flow.RedirectURI()+"（本机临时监听）", cw))
	}
	return listBlock(rows, cw)
}
