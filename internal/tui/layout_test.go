package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// 这个文件守着「空间布局」那一轮的改动：去硬线、输入框浮起、顶部会话
// 标签页、列表分组、两级侧栏、鼠标点击。
//
// 判据几乎全部走**真实路径**（按键、鼠标事件、View() 渲染出来的字符串），
// 而不是直接断言内部字段 —— 布局这种事，字段对了画面不一定对，而画面
// 才是用户看到的东西。

// openTwoPane 造一个双栏、已经开着会话、并且开过两个以上会话的模型。
//
// 顶部标签页只在「打开过两个会话」之后才出现，所以要真的走一遍切换，
// 不能直接改 openTabs —— 那样测的是夹具，不是功能。
func openTwoPane(t *testing.T) Model {
	t.Helper()
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("enter"))     // 打开第一条
	m, _ = update(m, keyMsg("ctrl+down")) // 换到第二条（这一步才产生第二个「最近打开」）
	m = loadBodies(t, m)

	l := m.measureLayout()
	if !l.twoPane {
		t.Fatalf("前提不成立：宽度 %d 降级成了单栏", m.width)
	}
	if l.tabsRow < 0 {
		t.Fatalf("前提不成立：标签页没出现（openTabs=%d 条）", len(m.openTabs))
	}
	return m
}

// viewLines 是画面的每一行（去过样式）。
func viewLines(m Model) []string {
	return strings.Split(m.View(), "\n")
}

// ---- 几何 ----

// 界面的四段（标签页 / 主体 / 输入区 / 状态栏）必须**正好**铺满屏幕：
// 不重叠、不留缝，加起来就是终端的高度。
//
// 这是「自适应窗口大小」的正面。以前那版的问题不是画得不好看，而是各段
// 的高度各算各的 —— 终端一矮，输入区和状态栏就叠在一起，或者画面比终端
// 多出一行把顶上的标题挤走。这类错误只在某些高度下出现。
func TestLayout_RegionsTileTheScreen(t *testing.T) {
	for _, w := range []int{96, 100, 140} {
		for _, h := range []int{12, 20, 30, 60} {
			m, _ := newFeatureModel(t)
			m, _ = update(m, tea.WindowSizeMsg{Width: w, Height: h})
			l := m.measureLayout()

			rows := l.bodyH + l.inputRows + 1 // 主体 + 输入区 + 状态栏
			if l.tabsRow >= 0 {
				rows++
			}
			if rows != h {
				t.Errorf("%dx%d：各段高度加起来 %d 行，屏幕 %d 行", w, h, rows, h)
			}
			if l.inputTop != l.bodyTop+l.bodyH {
				t.Errorf("%dx%d：主体底 %d 和输入区顶 %d 之间有空隙",
					w, h, l.bodyTop+l.bodyH, l.inputTop)
			}
			if l.statusRow != l.inputTop+l.inputRows {
				t.Errorf("%dx%d：状态栏在第 %d 行，输入区底在 %d 行",
					w, h, l.statusRow, l.inputTop+l.inputRows)
			}
			if l.bodyH < 1 || l.inputRows < 1 {
				t.Errorf("%dx%d：主体 %d 行、输入区 %d 行，都不该是空",
					w, h, l.bodyH, l.inputRows)
			}

			// 横向同理：列表 + 分隔竖线 + 正文 = 整幅宽，**中间不留缝**。
			if l.twoPane {
				if l.listX != 0 {
					t.Errorf("%dx%d：列表栏没从第 0 列起步（listX=%d）", w, h, l.listX)
				}
				// 那条竖线是**实打实的一列**，夹在两栏之间。
				if l.ruleX != l.listX+l.listW {
					t.Errorf("%dx%d：竖线在第 %d 列，该紧跟列表右沿 %d",
						w, h, l.ruleX, l.listX+l.listW)
				}
				if l.chatX != l.ruleX+1 {
					t.Errorf("%dx%d：正文栏没紧接竖线（chatX=%d ruleX=%d）", w, h, l.chatX, l.ruleX)
				}
				if l.chatX+l.chatW != w {
					t.Errorf("%dx%d：会话流右边缘在 %d 列，屏幕 %d 列", w, h, l.chatX+l.chatW, w)
				}
			} else if l.ruleX != -1 || l.listX != 0 {
				t.Errorf("%dx%d：单栏模式下不该有分隔竖线（ruleX=%d listX=%d）", w, h, l.ruleX, l.listX)
			}

			// 画出来的行数必须等于屏幕高度 —— 上面那些都是算出来的，
			// 这一条量的是**实际渲染**。
			if got := len(viewLines(m)); got != h {
				t.Errorf("%dx%d：画面渲染出 %d 行", w, h, got)
			}
		}
	}
}

// 画面里不该有横贯一整行的硬分割线。
//
// ⚠️ 阈值取「会话流那一栏的宽度」而不是 1，是因为 markdown 的 `---`
// 本来就渲染成一串 ─（styleMDRule）。那种线最长也只有会话流内部的宽度，
// 真正要去掉的是以前那种 `strings.Repeat("─", m.width)` —— 它横贯整幅，
// 长度必然超过会话流那一栏。
//
// 想收紧这条判据之前先看一眼 markdown.go：把阈值改小会立刻变成假红。
func TestView_NoFullWidthHorizontalRules(t *testing.T) {
	m := openTwoPane(t)
	limit := m.chatPaneWidth()
	for i, line := range viewLines(m) {
		if n := strings.Count(plainText(line), "─"); n >= limit {
			t.Errorf("第 %d 行有 %d 个 ─（会话流一栏只有 %d 列），像是一条横贯的分割线：%q",
				i, n, limit, plainText(line))
		}
	}
}

// 每一行都不许超出终端宽度；主体那几行还必须**正好**占满。
//
// 渲染出去的东西比终端宽，终端会自己折行 —— 一折行，下面的行号全错位，
// 鼠标的命中测试跟着错，画面上看到的是「右边多了半截字」。这种错误在
// 真实终端里比在测试里难看得多，所以在这里挡住。
func TestView_NoLineOverflowsTheTerminal(t *testing.T) {
	m := openTwoPane(t)
	l := m.measureLayout()
	for i, line := range viewLines(m) {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("第 %d 行宽 %d 列，终端只有 %d 列", i, w, m.width)
		}
	}
	// 主体那几行是各栏拼出来的，必须严丝合缝 —— 差一列就说明某个栏
	// 自己少铺或多铺了。
	for i := l.bodyTop; i < l.bodyTop+l.bodyH; i++ {
		if w := lipgloss.Width(viewLines(m)[i]); w != m.width {
			t.Errorf("主体第 %d 行宽 %d 列，want %d", i, w, m.width)
		}
	}
}

// 单栏（窄终端）只剩一栏：没有分隔竖线，也不许把内容顶出去。
//
// 上一版这里守的是「不许出现侧栏」，断言 `View()` 里不含「邮箱」。侧栏
// 删掉之后（文件夹不占一栏，改成按需弹出）这条变成了**永真**：界面上已经
// 没有的东西，怎么改都不会再出现。一条永绿的判据和没有判据是一回事，
// 它还会让「单栏下多画了一栏」这类真错从旁边溜过去。
//
// 换成守那条现在真的可能出错的规矩：单栏不该有分隔竖线那一列。
//
// ⚠️ 量的是几何（ruleX），**不是**扫屏幕上的 │ 字符。滚动条用的也是同一个
// 字符（view.go 里 styleScrollBar 渲染的 track）—— 扫字符的话，哪天正文
// 的滚动条挪进单栏，这条就会为一个跟分隔线无关的原因变红。
func TestLayout_NarrowTerminalHasNoRail(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	l := m.measureLayout()
	if l.twoPane {
		t.Fatalf("80 列不该是双栏")
	}
	if l.ruleX >= 0 {
		t.Errorf("单栏下 ruleX = %d，应该是 -1：那一列分隔竖线是两栏才有的结构",
			l.ruleX)
	}
	for i, line := range viewLines(m) {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("第 %d 行宽 %d 列，终端只有 %d 列", i, w, m.width)
		}
	}
}

// ---- 浮起的输入区 ----

// 输入区靠**上下各一整行空白**被认出来 —— 它自己不铺底色，也没有边框。
//
// 上一版这条量的是「那两行除了分隔竖线没有别的内容」外加「中间那行铺着
// 输入卡底色」。输入卡和分隔竖线都撤掉之后，同一件事变得更纯粹：上下那
// 两行必须**整行空白**，中间那行才有内容。
//
// 它守的仍然不是「好不好看」，是「输入区还在不在」：三行挤成两行、或者
// 留白被别的内容占了，输入框就退回成又一条贴底的横带 —— 而那种变化在
// 宽终端上一点也不显眼。
func TestInputBlock_IsSurroundedByBlankLines(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.measureLayout()

	if l.inputRows != inputBlockHeight {
		t.Fatalf("输入区占 %d 行，want %d", l.inputRows, inputBlockHeight)
	}
	lines := viewLines(m)

	// ⚠️ 竖线撤掉之后这里可以**整行** TrimSpace 一把梭了。上一版不行：
	// 竖线按设计要贯到底，整行量会把「设计如此」当成故障（当时的注释里
	// 记着这一条）。现在那一列是空白，整行本来就该干净。
	for _, off := range []int{0, 2} {
		if got := strings.TrimSpace(plainText(lines[l.inputTop+off])); got != "" {
			t.Errorf("输入框上/下那行还有内容：%q", got)
		}
	}

	// 中间那行得**真的画出输入框**。少了这一条，「三行空白」也能让上面
	// 那两条全绿 —— 正是本仓库反复吃亏的「负向断言恒真」。
	if got := strings.TrimSpace(plainText(lines[l.inputTop+1])); got == "" {
		t.Error("输入那一行是空的 —— 输入框没画出来")
	}
	// （「输入行不铺底色」由 TestPanes_AreNotPainted 一起守着：它按列量
	// 底色，比在这里整行扫一遍序列更准。）
}

// 终端太矮时，留白让给正文：输入区退回单行贴底。
//
// 「留白是奢侈品，正文优先」这件事要有判据守着，否则某次改动会把矮
// 终端下的正文挤得只剩一行，而谁也不会在宽终端上发现。
func TestInputBlock_FallsBackToASingleLineWhenShort(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 10})

	if l := m.measureLayout(); l.inputRows != 1 {
		t.Errorf("10 行的终端里输入区占 %d 行，want 1", l.inputRows)
	}
	if got := len(viewLines(m)); got != 10 {
		t.Errorf("画面 %d 行，want 10", got)
	}
}

// 列表模式没有输入框，那一行就得像**提示**，不能像**输入框**。
//
// 上一版比的是底色（"别铺输入卡的底色"）。输入卡撤掉之后底色这条线没了，
// 判据改成比**内容**：那一行该报快捷键（列表界面上真正能用的是哪几个键），
// 而不是会话里那句「输入消息，回车发送」的占位提示 —— 后者出现在列表里，
// 用户会开始打字，而那时候打字什么也不会发生。
func TestInputRow_ListModeShowsHintsNotAnInput(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	if m.mode != modeList {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}

	l := m.measureLayout()
	row := plainText(viewLines(m)[l.inputTop+1])
	if !strings.Contains(row, "q") {
		t.Errorf("列表模式下这一行应该是常驻快捷键提示，实际是 %q", row)
	}
	// 会话里那句占位提示不许出现在这里。按**当前**的占位文案比，不写死
	// 字面量：文案改了这条不该跟着红（假红和假绿一样贵）。
	if ph := m.input.Placeholder; ph != "" && strings.Contains(row, ph) {
		t.Errorf("列表模式这一行里出现了会话的占位提示 %q —— 看着能打字，其实不能",
			ph)
	}
}

// 输入框那一行必须占满「侧栏 + 列表栏 + 那 1 格内缩 + 输入卡」，右边只留一列。
//
// 这条管的是**对齐**，不是 sizedInput 的补差（那是
// TestSizedInput_NeverWiderThanAsked 的事）：整行的宽度是外层补白定的，
// 输入框自己多吐 3 列也会被截掉，看不出来。
//
// ⚠️ 输入卡现在只占正文栏（列 paneX 起，宽 paneW），不再是通栏 ——
// 它两侧各留一格留白，"浮起"才成立（见 renderInputBlock）。
func TestInputLine_FillsItsWidth(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.measureLayout()
	row := viewLines(m)[l.inputTop+1]

	want := l.paneX + l.paneW
	if got := lipgloss.Width(row); got != want {
		t.Errorf("输入那一行宽 %d 列，want %d（面板带 %d + 内缩 %d + 卡宽 %d）；屏宽 %d，右边那一列留白",
			got, want, l.paneX-paneInset, paneInset, l.paneW, m.width)
	}
}

// 两栏之间那一格从顶到底都是**空白**，而且每一行的内容都从同一列起步。
//
// 这一条是从上一版的「分隔竖线要从顶贯到底」改过来的。竖线撤掉之后，
// 那个位置仍然承担两件事：
//
//   - **不许有别的字符**。那一格被收掉（两栏贴在一起）或者被谁写了个
//     字符进去，两栏的边界就糊了；
//   - **对齐不能断**。标签页 / 输入区 / 状态栏这三行**不走** renderThreadList，
//     而是各自拼出来的，很容易忘了补左边那一截（withRail 就是干这个的）。
//     漏了的话，只有主体区是齐的，而只看主体区的判据全绿。
//
// 上一版这条判据靠「那一列是不是 │」同时守住了这两件事；现在那一列是
// 空格，得**分开量**：既量它是空格，也量它右边的正文确实从 chatX 起步。
func TestRail_IsBlankAndAlignedAllTheWayDown(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.measureLayout()
	rows := viewLines(m)

	from := l.bodyTop
	if l.tabsRow >= 0 {
		from = l.tabsRow
	}
	for y := from; y <= l.statusRow; y++ {
		if got := plainText(ansiSlice(rows[y], l.ruleX, l.ruleX+1)); got != " " {
			t.Errorf("第 %d 行的第 %d 列（两栏之间那一格）是 %q，want 一格空格 —— "+
				"分区靠留白，那一格不许有字符", y, l.ruleX, got)
		}
	}

	// 底部那几行**不走** renderThreadList，左边那一截是 withRail 补的。
	// 漏补的话它们会整体左移 paneX 列，和上面的内容错开 —— 那种错在宽
	// 终端上一点也不显眼（都是空白），只有把左边缘量出来才看得见。
	//
	// 这里**不**量整行宽度：标签页那一行本来就只画到最后一个 chip，宽度
	// 天然短于屏宽（量它会得到一条永远红的判据）。输入行的宽度由
	// TestInputLine_FillsItsWidth 单独守着。
	for y := l.inputTop; y <= l.statusRow; y++ {
		if got := strings.TrimSpace(plainText(ansiSlice(rows[y], 0, l.paneX))); got != "" {
			t.Errorf("第 %d 行（底部，不走 renderThreadList）的左 %d 列有内容 %q —— "+
				"withRail 没补上，这一行整体左移了", y, l.paneX, got)
		}
	}
}

// 列表栏和正文栏**都不铺底色**：整屏只剩终端背景这一个"面"。
//
// 这是这一轮把两块面板底色撤掉之后要守住的那件事。上一版列表栏铺 235、
// 文件夹栏铺 237，一屏里同时坐着三块亮度不同的灰板，用户的原话是
// 「割裂」。撤掉之后分区只由那条竖线表达。
//
// ⚠️ 判据绑的是「**不出现**任何整栏底色」，不是具体色号：以后想加回来
// 一个别的灰，这条也会红 —— 那正是它该拦的（这一轮的决定是"面板不分层"，
// 不是"不要 235 这个灰"）。
func TestPanes_AreNotPainted(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.measureLayout()

	// 列表栏取它中间那一列，量一条整栏贯通的带子。避开选中行 ——
	// 那一行**本来就有**底色（它是"选中"的信号，不是分区）。
	rows := viewLines(m)
	x := l.listX + l.listW/2
	for y := l.bodyTop; y < l.bodyTop+l.bodyH; y++ {
		line := rows[y]
		if idx, ok := m.listRowAt(l.listW, l.bodyH, y-l.bodyTop); ok && idx == m.cursor {
			continue
		}
		if got := bgAtFirstCell(ansiSlice(line, x, x+1)); got != "" {
			t.Errorf("第 %d 行第 %d 列（列表栏）铺了底色 %q —— 面板底色这一轮撤掉了",
				y, x, got)
		}
	}
	// 正文栏这一侧量**输入区那三行 + 状态栏**。上一版这里要跳过输入行
	// （当时它铺着输入卡底色，是"这里能打字"的信号）—— 输入卡撤掉之后
	// 没有例外了，那一行也必须干净，于是这条判据顺带守住了「输入区不再
	// 是一块实心色」，这正是用户要的「去掉输入卡底色的重感」。
	for y := l.inputTop; y <= l.statusRow; y++ {
		if got := bgAtFirstCell(ansiSlice(rows[y], l.paneX, l.paneX+1)); got != "" {
			t.Errorf("第 %d 行第 %d 列（正文栏）铺了底色 %q —— 输入卡和面板底色都撤掉了",
				y, l.paneX, got)
		}
	}
}

// 输入行只在**正文栏**里，不越到左边的列表栏去。
//
// 通栏的输入框是这一版之前的样子。输入卡撤掉之后"浮起"无从谈起，但这条
// 判据守的那件事还在：输入区只属于正文栏，左边列表栏的列必须干净 ——
// 越过去的话，两栏的分界（现在只剩留白）就断了，而且点列表会点到输入框上。
func TestInputLine_LivesInsideTheChatPane(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.measureLayout()
	row := viewLines(m)[l.inputTop+1]

	// 列表栏那几列必须一点内容都没有。整段 TrimSpace 一把梭：列表栏在
	// 输入区那几行本来就是空的（withRail 补的是空格）。
	left := plainText(ansiSlice(row, 0, l.paneX))
	if got := strings.TrimSpace(left); got != "" {
		t.Errorf("输入行越到正文栏左边去了（第 0–%d 列是 %q）", l.paneX, got)
	}

	// 反过来，正文栏里得真的有东西 —— 否则上面那条会因为「整行都空」
	// 而恒真，判据变成空壳却一路绿。
	if got := strings.TrimSpace(plainText(ansiSlice(row, l.paneX, m.width))); got == "" {
		t.Error("正文栏里没有输入框 —— 这条判据量不到东西")
	}
}

// 输入框里那句占位提示，必须**真的画出来**，而且挂在我们配的样式上。
//
// 这就是「字在，但看不见」那类 bug 的判据。原来的实际情况：textinput 的
// PlaceholderStyle 默认是一个写死的 hex，降级到 256 色正好是 240，而输入卡
// 底色（inputRowBg）也是 240 —— 两者一撞，「输入消息，回车发送」八个字在
// 界面上整个消失，只剩一个孤零零的提示符。
//
// ⚠️ 这一版把「比色」那一段（前景 ≠ 脚下底色）**删掉了**，不是忘了写：
// 输入卡撤掉之后，占位提示不再坐在任何一块我们铺的底色上（判据
// TestPanes_AreNotPainted 守着"正文栏不许铺底色"），那个撞色不可能再发生。
// 留着一段「前景 ≠ 底色」的比较只会比到两个常量，看着有牙齿其实是空转。
//
// 剩下这两条仍然有分辨力：一是那句提示确实被画在了输入行上（文案改了、
// 布局挤没了，都会红 —— 所以按当下的 Placeholder 文案比，不写字面量）；
// 二是它用的是 stylePlaceholder 而不是 bubbles 的默认色（这一点即使底色
// 没了也仍然要紧：它是"我们统一在 newTextInput 里配样式"的唯一落点）。
func TestPlaceholder_IsDrawnWithOurStyle(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("enter"))
	m = loadBodies(t, m)

	// 一、那句话得真的画出来。按**当前**的 Placeholder 文案去找，不写字面量：
	// 文案改了这条不该跟着红（假红和假绿一样贵）。
	ph := m.input.Placeholder
	if ph == "" {
		t.Fatal("前提不成立：会话模式的输入框没有占位提示")
	}
	row := viewLines(m)[m.measureLayout().inputTop+1]
	if !strings.Contains(plainText(row), ph) {
		t.Fatalf("输入行里找不到占位提示 %q —— 字根本没画出来。\n这一行是：%q",
			ph, plainText(row))
	}

	// 二、它得挂在**我们配的**那个样式上。少了这一条，bubbles 用默认色
	// 渲染、我们那个 stylePlaceholder 根本没被用上，判据照样绿。
	want := bgSeqOf(stylePlaceholder)
	if want == "" {
		t.Fatal("拿不到占位提示的样式序列 —— 这个用例大概忘了 forceColor")
	}
	if !strings.Contains(row, want) {
		t.Errorf("输入行里没有出现占位提示该有的样式 %q —— "+
			"这一行多半不是用 stylePlaceholder 画的（检查 newTextInput）", want)
	}
}

// 文件夹选择器要真的分两级：组标题 + 组里的项。
//
// 这一段原来守着**左侧那条常驻导航列**。那一列被删掉了（四五个入口每天
// 占掉 12 列列表宽度），但它承载的两件事必须跟着搬过来，不能跟着一起丢 ——
// 两级层次，和「哪一项是当前所在」。这就是下面这几条判据的来历。
func TestNav_HasTwoLevels(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, cmd := update(m, keyMsg("tab"))
	m = runCmd(t, m, cmd)

	// ⚠️ 这份文件夹名单必须在**打开选择器之后**才写。
	//
	// 打开那一步自己会去 LIST 一遍（foldersCmd(true)），并把结果盖回
	// m.folders（见 foldersResultMsg）。先写的话，那一次 LIST 会把手写的
	// 名单冲掉，第二组「文件夹」连同「垃圾邮件」一起凭空消失 ——
	// 而判据只会报「选择器里没有 垃圾邮件」，看着像分组逻辑坏了。
	m.folders = []string{"INBOX", "Sent", "Trash", "垃圾邮件"}

	rows := m.folderPickRows()
	if len(rows) < 5 {
		t.Fatalf("选择器只有 %d 行，前提不成立", len(rows))
	}
	// 行结构**从组标题起步**：标题行和空行由渲染层自己加（见 viewFolderPicker
	// 的 "换个文件夹" + 空行），folderPickRows 只负责内容。
	//
	// 这里原来写的是 rows[2:]，因为上一版把标题和空行也算进了行结构里 ——
	// 于是把索引绑到了一个纯粹属于版式的偏移上，版式一改判据就开始指着
	// 一个不存在的项说"这该是组标题"。
	if rows[0].group == "" {
		t.Fatalf("第 1 行该是组标题，实际 %+v", rows[0])
	}
	// 组标题后面紧跟着组里的项 —— 这就是「两级」在数据层的样子。
	if rows[1].item == nil {
		t.Fatalf("第 2 行该是组里的第一项，实际 %+v", rows[1])
	}
	body := rows

	// 组里的项要缩进，和组标题错开 —— 这是「两级」在画面上唯一的表达。
	//
	// ⚠️ 差必须 **≥2 格**，不能只比大小。差 1 格隔着一层文字根本看不出来，
	// 只写「项 > 标题」的话缩进退回 1 格照样绿 —— 变异验证确认过，而
	// 「缩进只差一格、看不出层级」正是当初要修掉的那个毛病。
	const minGap = 2
	view := plainText(m.View())
	titleLine, itemLine := "", ""
	for _, l := range strings.Split(view, "\n") {
		switch {
		case titleLine == "" && strings.Contains(l, body[0].group):
			titleLine = l
		case itemLine == "" && titleLine != "" && strings.Contains(l, "全部"):
			itemLine = l
		}
	}
	if titleLine == "" || itemLine == "" {
		t.Fatalf("画出来的选择器里找不到组标题或第一项：\n%s", view)
	}
	if gap := labelIndentOf(itemLine) - labelIndentOf(titleLine); gap < minGap {
		t.Errorf("组里的项只比组标题深 %d 格（want ≥%d），看不出层级：标题 %q（第 %d 列）、项 %q（第 %d 列）",
			gap, minGap, titleLine, labelIndentOf(titleLine), itemLine, labelIndentOf(itemLine))
	}

	for _, want := range []string{"全部", "收件箱", "已发送", "已删除", "垃圾邮件"} {
		if !strings.Contains(view, want) {
			t.Errorf("选择器里没有 %q", want)
		}
	}
}

// 服务端上多出来的文件夹要自成一个组，且不和上面那组重复。
func TestNav_ServerFoldersGetTheirOwnGroup(t *testing.T) {
	m, _ := newFeatureModel(t)
	m.folders = []string{"INBOX", "Sent", "Trash", "垃圾邮件", "草稿箱"}

	groups := m.navGroups()
	if len(groups) != 2 {
		t.Fatalf("拿到的组数 %d，want 2", len(groups))
	}
	if groups[1].title != "文件夹" {
		t.Errorf("第二组的标题是 %q", groups[1].title)
	}
	var labels []string
	for _, it := range groups[1].items {
		labels = append(labels, it.label)
	}
	joined := strings.Join(labels, ",")
	if !strings.Contains(joined, "垃圾邮件") {
		t.Errorf("服务端文件夹没进第二组：%v", labels)
	}
	// INBOX / Sent / Trash 已经在上面的「邮箱」组里了，不许再来一遍。
	for _, dup := range []string{"INBOX", "Sent", "Trash"} {
		if strings.Contains(joined, dup) {
			t.Errorf("%s 在两个组里各出现了一次：%v", dup, labels)
		}
	}
}

// 选择器**不列服务端上不存在的文件夹**。
//
// 这一条和 navGroups 的行为**故意相反**，两边都由判据钉着：
//
//   - navGroups 里那四项永远在 —— 常驻的时候，项数随服务器状态忽增忽减
//     比列一个空文件夹更让人困惑（东西会自己跳位置）。
//   - 选择器里要滤掉 —— 它是用户专门打开的一张"我要去哪儿"的列表，
//     列出一个点进去必然空空如也的文件夹，就是纯误导。
//
// 写这条的时候注意夹具：testConfig 里配的 SENT / TRASH 在 fake 服务端上
// 并不存在，所以「已删除」应该被滤掉，而 INBOX / Sent 留着。
func TestFolderPicker_HidesFoldersThatDoNotExist(t *testing.T) {
	m, _ := newFeatureModel(t)
	// 夹具里的 fake 只认 INBOX / Sent，所以 TRASH 解析不出来。
	if got := m.folderName("TRASH"); got == "Trash" {
		t.Skipf("夹具变了：TRASH 现在能解析成 %q，这条用例的前提没了", got)
	}

	picked := map[string]bool{}
	for _, it := range m.folderPickerItems() {
		picked[it.label] = true
	}
	if picked["已删除"] {
		t.Errorf("选择器里列出了服务端上不存在的「已删除」（TRASH 解析不出来）")
	}
	for _, want := range []string{"全部", "收件箱", "已发送"} {
		if !picked[want] {
			t.Errorf("选择器里少了本来就存在的 %q：%v", want, picked)
		}
	}

	// 拿不到文件夹列表时**不过滤**：那时候本来就判断不了谁存在，
	// 不该把任何一项判死。
	m.folders = nil
	if got := len(m.folderPickerItems()); got != 4 {
		t.Errorf("拿不到文件夹列表时有 %d 项，want 4（四个固定入口都留着）", got)
	}
}

// 切文件夹的时候，各文件夹的未读计数不许跟着变。
//
// 变了的话，切到「已发送」之后「收件箱」会显示 0 —— 用户看到的是
// 「邮件不见了」，而它只是被过滤掉了。
func TestNav_CountsDoNotFollowTheCurrentFolder(t *testing.T) {
	m, _ := newFeatureModel(t)

	counts := func() map[string]int {
		out := map[string]int{}
		for _, g := range m.navGroups() {
			for _, it := range g.items {
				out[it.label] = it.unread
			}
		}
		return out
	}

	before := counts()
	if before["全部"] == 0 {
		t.Fatal("前提不成立：夹具里应该有一个未读")
	}
	m.setFolder(m.folderName("SENT"))
	after := counts()

	for k, v := range before {
		if after[k] != v {
			t.Errorf("切到「已发送」之后 %q 的未读数从 %d 变成了 %d", k, v, after[k])
		}
	}
}

// 选择器里的高亮直接就是 activeFolder：不许出现「高亮着已发送、
// 列表却在显示收件箱」这种对不上号的画面。
func TestFolderPicker_HighlightFollowsTheActiveFolder(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m, _ := newFeatureModel(t)

	for _, it := range m.folderPickerItems() {
		m.setFolder(it.folder)
		m.mode = modeFolder
		// 打开选择器时会把光标停在当前那一项上，这里照着做一遍。
		for i, x := range m.folderPickerItems() {
			if x.folder == m.activeFolder {
				m.folderCursor = i
			}
		}

		// 量的是**画出来的**那一行：只有铺了底色的那一个才算。
		//
		// ⚠️ 用 bgColsOf 而不是 bgAtFirstCell：选择器是**居中**的一列窄栏
		// （见 pickerGeom），选中块前面还有一段缩进。只看第 0 格的底色，
		// 量到的是那段缩进（终端背景），于是一个高亮都匹配不到 ——
		// 判据会退化成一红一片的「高亮丢了」，而画面上明明是对的。
		var highlighted []string
		for _, r := range strings.Split(m.View(), "\n") {
			if bgColsOf(r, want) > 0 {
				highlighted = append(highlighted, strings.TrimSpace(plainText(r)))
			}
		}
		if len(highlighted) != 1 {
			t.Fatalf("高亮的项有 %d 个（folder=%q）：%v", len(highlighted), it.folder, highlighted)
		}
		if !strings.Contains(highlighted[0], it.label) {
			t.Errorf("folder=%q 时高亮的是 %q，want %q", it.folder, highlighted[0], it.label)
		}
	}
}

// ---- 顶部会话标签页 ----

// 只开过一个会话时不该有标签页：那一行既没内容也没用，白占一行正文。
func TestTabs_AppearOnlyAfterASecondThreadIsOpened(t *testing.T) {
	m, _ := newFeatureModel(t)
	if l := m.measureLayout(); l.tabsRow != -1 {
		t.Errorf("一个会话都没开时就有标签页了（tabsRow=%d）", l.tabsRow)
	}

	m, _ = update(m, keyMsg("enter"))
	if l := m.measureLayout(); l.tabsRow != -1 {
		t.Errorf("只开了一个会话就有标签页了（tabsRow=%d）", l.tabsRow)
	}

	m, _ = update(m, keyMsg("ctrl+down"))
	if l := m.measureLayout(); l.tabsRow != 0 {
		t.Errorf("开过两个会话之后标签页该出现，实际 tabsRow=%d", l.tabsRow)
	}
}

// 标签页按**打开先后的顺序**排，当前那一个铺着底色。
//
// 顺序的语义是这条用例的重点。标签页回答的是「我打开过哪些会话」，那是
// 一段操作历史；按 MRU 排的话，点一下标签它自己就跳到第一个位置 —— 一排
// 会自己重排的标签，每点一次都得重新找一遍。浏览器的做法是位置只在
// 开 / 关标签时才变。
func TestTabs_OrderFollowsOpenSequenceAndActiveMarked(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m := openTwoPane(t)

	// openTwoPane 是「回车开第一条、Ctrl+↓ 换到第二条」，所以当前会话是
	// 第二个被打开的 ——「当前的在最前面」和「按打开顺序排」在这里会给出
	// 不同的答案，这条用例才分得开这两件事。
	if len(m.openTabs) != 2 {
		t.Fatalf("打开顺序是 %v，want 2 条", m.openTabs)
	}
	if m.openTabs[0] == m.activeID {
		t.Fatalf("前提不成立：当前会话正好是第一个打开的（openTabs=%v），分不出两种排序", m.openTabs)
	}

	var opened []string
	for _, c := range m.tabChips(m.measureLayout()) {
		if c.marker != "" {
			t.Fatalf("只有两个标签却出现了溢出指示符 %q", c.marker)
		}
		opened = append(opened, c.id)
	}
	if !sameStrings(opened, m.openTabs) {
		t.Errorf("标签顺序是 %v，打开顺序是 %v —— 没按打开先后排", opened, m.openTabs)
	}

	// 标签的坐标是正文栏里的相对列，画面是屏幕列，所以要加上 paneX
	// （面板带 + 那 1 格内缩）那一截。
	l := m.measureLayout()
	chips := m.tabChips(l)
	row := viewLines(m)[l.tabsRow]
	var marked []string
	for _, c := range chips {
		seg := ansiSlice(row, l.paneX+c.x0, l.paneX+c.x1)
		if bgAtFirstCell(seg) == want {
			marked = append(marked, c.id)
		}
	}
	if len(marked) != 1 || marked[0] != m.activeID {
		t.Errorf("铺了底色的标签是 %v，当前会话是 %q", marked, m.activeID)
	}

	// 第一个标签必须正好压在**正文栏内容的左边缘**上：下面的引用块、输入
	// 卡、状态栏全都从这一列起步，标签页偏出十几列的话，一眼就看得出
	// 没对齐。
	//
	// 判据取「**正文栏那一段**里第一个可见字符落在第几列」。不拿当前标签
	// 的底色去比 —— 按打开顺序排之后当前标签不一定是第一个，第一个是灰字、
	// 不铺底。字符还是有的：位置对了就说明整排没被漏算一段偏移（这正是它
	// 抓到过的那次错，withNavStrip 那一下被漏掉，整排标签右移了十几列）。
	//
	// ⚠️ 只量竖线**右边**那一段。竖线本身也是可见字符，而且它画在第
	// ruleX 列 —— 整行从头数的话，数到的是那条线，判据会红在一个跟标签
	// 无关的列号上（见 TestTabs_OrderFollowsOpenSequenceAndActiveMarked
	// 报过的「在第 25 列」）。
	if chips[0].x0 != 0 {
		t.Errorf("第一个标签的起点是第 %d 列，want 0（正文栏内容的左边缘）", chips[0].x0)
	}
	seg := plainText(ansiSlice(row, l.ruleX+1, m.width))
	lead := len(seg) - len(strings.TrimLeft(seg, " "))
	if got := l.ruleX + 1 + lead; got != l.paneX+1 {
		t.Errorf("标签行上第一个可见字符在第 %d 列，want %d（左边缘 + 1 格内边距）—— 第一个标签没和正文栏对齐",
			got, l.paneX+1)
	}
}

// 点标签页能切过去，而且**它自己一动不动**：位置、列宽都不变。
//
// **在画出来的那一行上去找标签所在的列**，而不是拿 tabChips 声明的坐标去点。
// 声明错了、而渲染和命中测试都跟着错的时候，只比对内部字段的判据全是绿的
// —— 这条用例就这么假绿过一次：chips 宣称第一个标签在第 13 列，实际画在
// 第 26 列（renderTabs 里 withNavStrip 的那一下被漏算了），点自己声明的地方
// 当然还是「对」的。
func TestTabs_ClickSwitchesThreadWithoutReordering(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.measureLayout()
	before := m.tabChips(l)

	row := plainText(viewLines(m)[l.tabsRow])
	for _, c := range before {
		if c.marker != "" {
			continue
		}
		col := strings.Index(row, c.label)
		if col < 0 {
			t.Fatalf("标签 %q 在画出来的那一行里找不到：%q", c.label, row)
		}

		got, _ := update(m, tea.MouseMsg{
			X: col, Y: l.tabsRow,
			Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		})

		if got.activeID != c.id {
			t.Errorf("点第 %d 列（%q 就画在这儿）切到的是 %q，want %q", col, c.label, got.activeID, c.id)
			continue
		}
		if got.mode != modeChat {
			t.Errorf("点了标签之后 mode=%v，want modeChat", got.mode)
		}
		// 位置连一列都不该挪。比的是整排标签（顺序 + 文字 + 列区间），
		// 只比 id 顺序的话，「三个标签整体右移一格」这种错照样绿。
		if after := got.tabChips(got.measureLayout()); !sameChips(before, after) {
			t.Errorf("点过 %q 之后标签从 %v 变成 %v —— 不该重排",
				c.id, chipIDs(before), chipIDs(after))
		}
	}
}

// 标签一多就放不下了：那一排的窗口跟着当前标签走，**当前这一个永远在
// 可见范围里**。
//
// 这是浏览器的 scroll-into-view。不这么做的话，开过七八个会话之后切到最后
// 一个，那排标签里根本没有亮着的那一格 —— 看着就像切换失败了。窗口取「最小
// 的、能覆盖当前标签的 start」，所以在保证当前可见的前提下还会尽量把左边的
// 标签也带进来。
func TestTabs_WindowFollowsActiveThread(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)

	const n = 8 // = tabLimit，最挤的情况
	m := manyThreadsModel(t, n)
	m, _ = update(m, keyMsg("enter"))
	for i := 1; i < n; i++ {
		m, _ = update(m, keyMsg("ctrl+down"))
	}
	m = loadBodies(t, m)

	if len(m.openTabs) != n {
		t.Fatalf("打开了 %d 个标签，want %d：%v", len(m.openTabs), n, m.openTabs)
	}
	l := m.measureLayout()
	if !l.twoPane || l.tabsRow < 0 {
		t.Fatalf("前提不成立：twoPane=%v tabsRow=%d width=%d", l.twoPane, l.tabsRow, m.width)
	}

	countReal := func(chips []tabChip) int {
		k := 0
		for _, c := range chips {
			if c.marker == "" {
				k++
			}
		}
		return k
	}
	// 前提：这么多标签在这个宽度下确实放不下。放得下的话，这条用例一行
	// 断言都没在路上走过，却一直是绿的。
	if countReal(m.tabChips(l)) >= n {
		t.Fatalf("前提不成立：%d 个标签在 paneW=%d 下全都放得下，没有溢出", n, l.paneW)
	}

	for i, id := range m.openTabs {
		th, ok := m.threadByID(id)
		if !ok {
			t.Fatalf("标签 %q 找不到对应会话", id)
		}
		cur, _ := m.enterThread(th, false, true)
		mm := cur.(Model)
		ml := mm.measureLayout()

		var real []string
		var left, right bool
		for _, c := range mm.tabChips(ml) {
			switch c.marker {
			case "‹":
				left = true
			case "›":
				right = true
			default:
				real = append(real, c.id)
			}
		}
		if len(real) == 0 {
			t.Errorf("把 %q 切成当前之后一个标签都不剩了", id)
			continue
		}

		// 1. 当前这一个必须在可见范围里 —— 这条一红就是「切过去了但看不见」。
		if !slices.Contains(real, id) {
			t.Errorf("第 %d 个标签（%s）是当前会话，却不在可见标签 %v 里", i, id, real)
		}
		// 2. 指示符只在那一侧**真有**藏起来的标签时才画。只判「指示符出现
		//    了」的话，两个方向各错一半（该画没画、不该画乱画）都测不出来。
		if wantLeft := real[0] != m.openTabs[0]; wantLeft != left {
			t.Errorf("当前是第 %d 个（%s）：左侧藏着标签=%v，但 ‹ 画的是 %v（可见 %v）",
				i, id, wantLeft, left, real)
		}
		if wantRight := real[len(real)-1] != m.openTabs[n-1]; wantRight != right {
			t.Errorf("当前是第 %d 个（%s）：右侧藏着标签=%v，但 › 画的是 %v（可见 %v）",
				i, id, wantRight, right, real)
		}
		// 3. 亮着的就是当前那一个 —— 窗口滚过去了，但底色要是跟着错位，
		//    看着还是「切换失败」。
		row := viewLines(mm)[ml.tabsRow]
		var marked []string
		for _, c := range mm.tabChips(ml) {
			if c.marker != "" {
				continue
			}
			if bgAtFirstCell(ansiSlice(row, ml.paneX+c.x0, ml.paneX+c.x1)) == want {
				marked = append(marked, c.id)
			}
		}
		if len(marked) != 1 || marked[0] != id {
			t.Errorf("当前是 %s，铺了底色的却是 %v", id, marked)
		}
	}
}

// sameStrings 说两个字符串切片是不是逐项相等（顺序也算）。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameChips 说两排标签是不是一模一样：同样的顺序、同样的文字、同样的列区间。
func sameChips(a, b []tabChip) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// chipIDs 把一排标签写成好读的一串，出错信息里用。
func chipIDs(chips []tabChip) []string {
	out := make([]string, 0, len(chips))
	for _, c := range chips {
		if c.marker != "" {
			out = append(out, c.marker)
			continue
		}
		out = append(out, fmt.Sprintf("%s@%d-%d", c.label, c.x0, c.x1))
	}
	return out
}

// ---- 列表分组 ----

// 分组标题只在两组都有内容时才画。
func TestList_GroupHeadersOnlyWhenBothGroupsExist(t *testing.T) {
	m, _ := newFeatureModel(t)
	text := strings.Join(plainTexts(strings.Split(m.renderThreadList(m.listWidth(), m.bodyHeight()), "\n")), "\n")
	if !strings.Contains(text, "未读") || !strings.Contains(text, "已读") {
		t.Fatalf("夹具里未读和已读都有，两个标题都该出现：\n%s", text)
	}

	// 全部标成已读之后，就不该再留一个孤零零的「已读」压在上面。
	for i := range m.visible {
		m.visible[i].Unread = 0
	}
	text = strings.Join(plainTexts(strings.Split(m.renderThreadList(m.listWidth(), m.bodyHeight()), "\n")), "\n")
	if strings.Contains(text, "已读 ") {
		t.Errorf("全已读时不该画分组标题：\n%s", text)
	}
}

// 未读的排在已读前面，而且组内保持原来的顺序（稳定分区）。
//
// 组内**必须**稳定：m.cursor 索引的就是这个切片，组内乱序会让按下箭头跑到
// 一条完全无关的会话上。要两条以上未读才测得出来 —— 只有一条未读时，
// 「保持原序」和「反过来」是同一件事（这条判据最初就是那么写的，把分组
// 改成倒序它照样绿）。
func TestList_UnreadFirstAndStableWithinGroups(t *testing.T) {
	mk := func(subject string, unread int) thread.Thread {
		return thread.Thread{Subject: subject, Unread: unread}
	}
	in := []thread.Thread{
		mk("a", 0), mk("b", 1), mk("c", 0), mk("d", 1), mk("e", 0),
	}
	got := subjects(groupUnreadFirst(in))
	want := []string{"b", "d", "a", "c", "e"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("分组后 = %v，want %v（未读在前、组内保持原序）", got, want)
	}

	// 端到端也走一遍：真的模型、真的列表。
	m, _ := newFeatureModel(t)
	// 夹具的顺序（按时间降序）：Sent、Bob、Alice；只有 Alice 未读。
	got = subjects(m.visible)
	want = []string{"会议安排", "发出去的", "周末爬山"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("会话顺序 = %v，want %v（未读在前、组内保持时间序）", got, want)
	}
}

// subjects 取一批会话的主题。
func subjects(list []thread.Thread) []string {
	out := make([]string, 0, len(list))
	for _, th := range list {
		out = append(out, th.Subject)
	}
	return out
}

// **光标挪一格，画的就必须是下一条。**
//
// 分组会插进标题行、每条会话又占两行，所以「第几行」和「第几条会话」
// 不再是一回事。这条判据从**渲染出来的画面**里把铺了底色块的两行找出来，
// 再和光标指向的那条会话对上 —— 它是这一轮最容易悄悄坏掉的地方：光标
// 和行号一旦漂移，按 ↓ 会跳到一条和屏幕上完全无关的会话上。
//
// 量的是「这一行有多少格铺着选中底色」，不是「第一个格子的底色」：
// 选中块两侧各内缩一格，第 0 列铺的是列表栏自己的带子。
func TestList_HighlightAlwaysMatchesTheCursor(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m, _ := newFeatureModel(t)

	for i := range m.visible {
		m.cursor = i
		lines := strings.Split(m.renderThreadList(m.listWidth(), m.bodyHeight()), "\n")

		var marked []string
		for _, ln := range lines {
			if bgColsOf(ln, want) > 0 {
				marked = append(marked, strings.TrimSpace(plainText(ln)))
			}
		}
		if len(marked) != 2 {
			t.Fatalf("光标在第 %d 条时，画面里铺底色的行有 %d 行，want 2（标题 + 主题）", i, len(marked))
		}

		th := m.visible[i]
		if !strings.Contains(marked[0], threadTitle(th)) {
			t.Errorf("光标在第 %d 条（%q），高亮的是 %q", i, threadTitle(th), marked[0])
		}
		// 第二行是主题。这里断言的是**主题文本**，不是缩进 —— 上面 TrimSpace
		// 之后缩进已经没了，绑缩进只会写出一条永远为真的判据。
		subject := th.Subject
		if subject == "" {
			subject = "(无主题)"
		}
		if !strings.Contains(marked[1], subject) {
			t.Errorf("光标在第 %d 条（%q），高亮的第二行是 %q", i, subject, marked[1])
		}
	}
}

// 选中项用底色块，不再整行反白。反白是终端里最大的对比度，一行反白会
// 在视野里炸开 —— 参照的设计里用的就是底色块。
//
// 顺着量一下底色块**够宽**：只数「有没有格子铺着」的话，某天渲染层把
// 块画成一格宽的小点也照样绿。块的实际宽度是内缩之后的 `inner`，
// 这里只要求它过半 —— 绑死具体列数就等于把内缩量也绑死了。
func TestList_SelectedRowUsesBackgroundNotReverse(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m, _ := newFeatureModel(t)
	m.cursor = 0

	width := m.listWidth()
	lines := strings.Split(m.renderThreadList(width, m.bodyHeight()), "\n")
	var marked []string
	for _, ln := range lines {
		if n := bgColsOf(ln, want); n > 0 {
			if n*2 < width {
				t.Errorf("选中块的底色只铺了 %d 格（列表栏宽 %d）—— 那不是一块，是一个点: %q",
					n, width, ln)
			}
			marked = append(marked, ln)
		}
	}
	if len(marked) == 0 {
		t.Fatal("选中项没有铺底色")
	}
	for _, ln := range marked {
		if strings.Contains(ln, "\x1b[7m") {
			t.Errorf("选中项还在用反白：%q", ln)
		}
	}
}

// listRowText（纯文本）和 listRowStyled（上色）必须**逐字对齐**。
//
// 两条路径共用 listRowSplit 算出来的同一组定宽格子，这里把两边的结果去样式
// 之后逐字节比一遍。这是唯一能挡住「改了配色顺手把宽度也改了」的判据 ——
// 一旦有人在上色那一路上自己补一格空格（或者漏补一格），屏幕上就是选中 /
// 未选中切换时文字左右跳一格，而两边各自的字都对，谁也看不出是宽度的错。
//
// ⚠️ 必须 forceColor：不上色的时候上色那一版退化成纯文本，这条判据就成了
// 同义反复（恒真）。
func TestList_StyledRowMatchesItsPlainText(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()

	inner := listRowInner(l.listW)
	rows := m.listRows()
	start, end := m.listWindow(rows, l.bodyH)
	if end-start < 3 {
		t.Fatalf("前提不成立：可见行只有 %d 行", end-start)
	}

	checked := 0
	for _, r := range rows[start:end] {
		if r.group != "" {
			continue
		}
		th := m.visible[r.idx]
		plain := m.listRowText(th, r.first, inner)
		styled := plainText(m.listRowStyled(th, r.first, inner))
		if plain != styled {
			t.Errorf("第 %d 条会话（first=%v）两条路径不一致 —— 上色那一路多/少了格子：\n"+
				"纯文本 %q（%d 列）\n去样式 %q（%d 列）",
				r.idx, r.first, plain, textWidth(plain), styled, textWidth(styled))
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("前提不成立：只量到 %d 行", checked)
	}
}

// 名字列的宽度**只由栏宽决定**，和时间串写成什么样无关。
//
// 这是用户那句「元素堆砌挤压」的正面要求：一栏里每条会话的名字列一样宽，
// 名字的右边缘才会对齐；时间列也一样宽，时间的右边缘才会对齐。
//
// ⚠️ 上一版是 `nameW = inner - markWidth - 2 - textWidth(timeStr)` —— 时间
// 串多长，名字就少多长。时间定长（5 列或 4 列）时看不太出来；改成
// 「前天 15:30」「2026-4-1 15:23」之后长度从 5 摇到 15，同一个列表里名字
// 宽度条条不同：老信的名字被挤成 `HTM…`，今天的名字却有一整片余量。
//
// 这条判据把那个式子钉死：**同一个 width 下，四种时间档必须给出同一个
// 名字列宽**。顺带守时间列 —— 它也必须定宽，否则右对齐无从谈起。
func TestListRowSplit_NameColumnKeepsItsWidth(t *testing.T) {
	m, _ := newFeatureModel(t)
	if len(m.visible) == 0 {
		t.Fatal("前提不成立：列表是空的")
	}
	inner := listRowInner(m.listWidth())

	wantTimeW := listTimeField(inner)
	if wantTimeW <= 0 {
		t.Fatalf("前提不成立：inner=%d 时这一档不画时间列", inner)
	}
	wantNameW := inner - markWidth - 2 - wantTimeW
	if wantNameW < 1 {
		t.Fatalf("前提不成立：算出来的名字列只有 %d 列", wantNameW)
	}

	// 时间点从「今天中午」往外推，避免凌晨跑测试时跨过午夜落到前一天
	// （间歇红 = 没有信息的判据）。
	now := time.Now()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 30, 0, 0, now.Location())

	for _, c := range []struct {
		name string
		at   time.Time
	}{
		{"今天", noon},
		{"昨天", noon.AddDate(0, 0, -1)},
		{"更早", noon.AddDate(0, 0, -40)},
		{"没有时间", time.Time{}},
	} {
		th := m.visible[0]
		th.LastDate = c.at
		cells := listRowSplit(th, inner)

		if got := textWidth(cells.name); got != wantNameW {
			t.Errorf("%s：名字列 %d 列，want %d 列 —— 名字的宽度被时间串的长短吃了\n%q",
				c.name, got, wantNameW, cells.name)
		}
		if got := textWidth(cells.time); got != wantTimeW {
			t.Errorf("%s：时间列 %d 列，want %d 列（时间串写的是 %q）",
				c.name, got, wantTimeW, strings.TrimSpace(cells.time))
		}
	}
}

// 一栏里**每一条会话的右边缘都在同一列**。
//
// 上面那条量的是算出来的格子，这条量屏幕上真的画出来的那一行 —— 用户看到
// 的参差在这里，不在格子函数里。行号不自己重算：走 listRows + listWindow，
// 和 renderThreadList 数行数的口径一致（第 0 行是列表标题，之后一行一条）。
func TestList_EveryRowEndsAtTheSameColumn(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()
	inner := listRowInner(l.listW)
	want := listRowInset + inner

	lines := strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n")
	rows := m.listRows()
	start, end := m.listWindow(rows, l.bodyH)

	checked := 0
	for i := start; i < end; i++ {
		if rows[i].group != "" || !rows[i].first {
			continue
		}
		line := strings.TrimRight(plainText(lines[1+(i-start)]), " ")
		if got := textWidth(line); got != want {
			t.Errorf("第 %d 条会话那一行的右边缘在第 %d 列，want 第 %d 列 —— "+
				"一栏里的右边缘是毛的\n%q", rows[i].idx, got, want, line)
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("前提不成立：只量到 %d 条会话", checked)
	}
}

// 一条会话的两行在屏幕上**共用同一条左边缘**（名字那一列）。
//
// 上一版副行缩的是写死的两个空格，而名字在 markWidth+1 = 3 列处 —— 副行比
// 名字靠左一格。一格而已，但一栏里十条会话就有十条这样的错位，整栏的左
// 边缘是毛的；而两行本是「一个块」，却各自站开了一格，这正是用户说的
// 「元素堆砌挤压」的另一半。
//
// 量法：名字那一列**不按公式算**，而是在渲染出来的标题行里把名字文本
// 找出来（`strings.Index`）—— 公式算的话，判据和被测代码就共用了同一个
// 假设，「缩进改成 2 列」这种错两边会一起错、一起绿。
func TestList_SubLineSharesTheNameColumn(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()
	inner := listRowInner(l.listW)

	lines := strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n")
	rows := m.listRows()
	start, end := m.listWindow(rows, l.bodyH)

	checked := 0
	for i := start; i+1 < end; i++ {
		if rows[i].group != "" || !rows[i].first || rows[i+1].first ||
			rows[i+1].idx != rows[i].idx {
			continue
		}
		th := m.visible[rows[i].idx]
		name := strings.TrimRight(listRowSplit(th, inner).name, " ")
		titleLine := plainText(lines[1+(i-start)])

		nameCol := strings.Index(titleLine, name)
		if nameCol < 0 {
			t.Fatalf("前提不成立：标题行 %q 里找不到名字 %q", titleLine, name)
		}
		// ⚠️ Index 给的是**字节**偏移，要的是**列**。标记那一格是 `▌`：
		// 3 字节、1 列，直接拿来比会让这条判据在**正确的代码**上红（第一版
		// 就是这么红的 —— 6 对 4）。位置一律换算成显示列再比。
		nameCol = textWidth(titleLine[:nameCol])

		sub := plainText(lines[1+(i+1-start)])
		subCol := len(sub) - len(strings.TrimLeft(sub, " "))
		if subCol != nameCol {
			t.Errorf("第 %d 条会话：名字从第 %d 列起，副行却从第 %d 列起 —— "+
				"两行本该是一个块，现在各自站开了一格\n标题 %q\n副行 %q",
				rows[i].idx, nameCol, subCol, titleLine, sub)
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("前提不成立：只量到 %d 对", checked)
	}
}

// ---- 鼠标 ----

// 列表栏里**每一行**都点一遍：点在标题行上要打开这条，点在主题行上要打开
// 同一条，点在分组标题或空白上什么也不该发生。
//
// 这条判据刻意不看内部字段：行号是从**渲染出来的那一栏**里数出来的，点击是
// 真的左键按下。渲染和命中测试只要错开一行，这里立刻红。
//
// 必须**逐行**点：一条会话占两行，而「差一行」的错位在标题行上恰好落到
// 同一条会话的主题行上，看着还是对的 —— 只点标题行的话，这种错位测不出来。
func TestMouse_EveryListRowOpensItsOwnThread(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()

	pane := strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n")
	if len(pane) < 6 {
		t.Fatalf("列表栏只有 %d 行，前提不成立", len(pane))
	}
	// 第 0 行是列表标题（当前文件夹 / 搜索词），不是会话。
	for k := 1; k < len(pane); k++ {
		text := plainText(pane[k])
		want, isThread := threadNamed(m, text)

		got, _ := update(m, tea.MouseMsg{
			X: l.listX + 2, Y: l.bodyTop + k,
			Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		})

		if !isThread {
			// 分组标题 / 空白：点了不该有任何反应。
			if got.mode != m.mode || got.activeID != m.activeID {
				t.Errorf("点第 %d 行（%q，不是会话）之后打开了 %q（mode=%v）",
					k, strings.TrimSpace(text), got.activeID, got.mode)
			}
			continue
		}
		if got.mode != modeChat || got.activeID != want.ID {
			t.Errorf("点第 %d 行（%q）之后打开的是 %q（mode=%v），want %q",
				k, strings.TrimSpace(text), got.activeID, got.mode, want.ID)
		}
	}
}

// threadNamed 从列表里一行的文字认出它是哪条会话（名字行、副行都算）。
//
// 认不出来就是分组标题或者空白行。取**最长**的那个匹配：摘要或主题里
// 提到对方名字的时候，短的名字会先撞上。
//
// ⚠️ 副行的文字要从**模型**取（listSubLine），不能在 thread.Thread 上找。
// 它现在是「最新一条的正文摘要」，而不在 Thread 结构里 —— 摘要没拉到时
// 才是主题。照着 th.Subject 去匹配的话，判据只在「摘要还没到」那个相位
// 成立，一旦夹具里把摘要装上了，它就会把真会话行认成空白行，然后**跳过
// 那些行不点** —— 一条不再检查任何东西、却依然绿着的判据。
func threadNamed(m Model, text string) (thread.Thread, bool) {
	// 渲染出来的那一行可能是**截断**过的（栏宽不够时以 "…" 收尾）。拿截断后的
	// 串去 Contains 完整的答案必然落空 —— 那是尺子的错，不是产品的错。
	// 所以两个方向都认：整段答案在这一行里，或者这一行是答案的开头。
	trimmed := strings.TrimSuffix(strings.TrimSpace(text), "…")
	best := -1
	var hit thread.Thread
	for _, th := range m.visible {
		for _, s := range []string{threadTitle(th), m.listSubLine(th)} {
			if s == "" {
				continue
			}
			if !strings.Contains(text, s) &&
				!(len(trimmed) > 0 && strings.HasPrefix(s, trimmed)) {
				continue
			}
			if len(s) > best {
				best, hit = len(s), th
			}
		}
	}
	return hit, best >= 0
}

// 点列表栏的**标题行**就弹出文件夹选择器。
//
// 这是那条常驻导航列被删掉之后补上的入口。少了它，鼠标用户就没有任何
// 办法换文件夹了 —— 而 Tab 和 Ctrl+G 都是键盘那边的路。
func TestMouse_ClickListHeaderOpensFolderPicker(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()

	if m.activeFolder != "" {
		t.Fatalf("前提不成立：一开始应该在「全部」，实际 %q", m.activeFolder)
	}

	// 整行都能点，不只标题那几个字 —— 取最左边那一格，它离文字很远。
	m, cmd := update(m, tea.MouseMsg{
		X: 0, Y: l.bodyTop, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if cmd == nil {
		t.Fatal("点标题行之后没有去要文件夹列表 —— 选择器打不开")
	}
	m = runCmd(t, m, cmd)

	if m.mode != modeFolder {
		t.Fatalf("点了列表标题之后 mode=%v，want modeFolder", m.mode)
	}
	// 光标要停在当前那一项上，而不是从头开始。
	if it := m.folderPickerItems()[m.folderCursor]; it.folder != m.activeFolder {
		t.Errorf("光标停在 %q 上，当前文件夹是 %q", it.label, m.activeFolder)
	}

	// 换一个文件夹 —— 走真实的点击，不是直接改字段。
	target := -1
	for i, it := range m.folderPickerItems() {
		if it.folder == m.folderName("SENT") {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("前提不成立：选择器里找不到「已发送」")
	}
	x, y, ok := m.pickerItemPos(target)
	if !ok {
		t.Fatal("「已发送」不在可见窗口里")
	}
	m, _ = update(m, tea.MouseMsg{
		X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})

	if want := m.folderName("SENT"); m.activeFolder != want {
		t.Fatalf("点了「已发送」之后 activeFolder = %q，want %q", m.activeFolder, want)
	}
	if m.mode != modeList {
		t.Errorf("选完该回列表，mode=%v", m.mode)
	}
	// 列表必须跟着只剩那一条，否则点了等于没点。
	if len(m.visible) == 0 {
		t.Fatal("切到「已发送」之后列表空了")
	}
	for _, th := range m.visible {
		if !threadInFolder(th, m.activeFolder) {
			t.Errorf("%q 不在 %s 里，却还在列表上", th.Subject, m.activeFolder)
		}
	}
}

// 点选择器里的**别处**（留白、组标题、页脚）等于取消。
//
// Esc 是键盘那边唯一的出路，鼠标必须有一条等价的 —— 否则一个用鼠标打开
// 选择器的人会卡在一个只能靠键盘离开的界面里。这条判据守的就是它。
func TestMouse_ClickOutsidePickerCancels(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	m, cmd := update(m, keyMsg("tab"))
	m = runCmd(t, m, cmd)
	if m.mode != modeFolder {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}
	before := m.activeFolder

	g := m.pickerGeom()
	for _, probe := range []struct {
		name string
		x, y int
	}{
		{"最底下的页脚那一行", g.left + 2, m.height - 1},
		{"标题那一行", g.left + 2, 0},
		{"那一列左边的留白", 0, 4},
	} {
		got, _ := update(m, tea.MouseMsg{
			X: probe.x, Y: probe.y,
			Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		})
		if got.mode != modeList {
			t.Errorf("点%s（%d,%d）之后 mode=%v，want modeList（该取消）",
				probe.name, probe.x, probe.y, got.mode)
		}
		// 取消**不该顺手改掉文件夹**：那是"点错地方就换了文件夹"的无妄之灾。
		if got.activeFolder != before {
			t.Errorf("点%s之后 activeFolder 从 %q 变成了 %q —— 取消不该改文件夹",
				probe.name, before, got.activeFolder)
		}
	}
}

// 列表栏的标题行要写明**当前所在**，而且它是那个入口的名字。
//
// 文件夹那一列被删掉之后，这一行是用户唯一能确认「我在哪儿」的地方。
// 上一版它写「会话 · 收件箱（3 个未读）」——「会话」那 3 个字是从
// "有个标题总比没有好"里来的，而"这一栏是会话列表"整个界面都在说。
func TestListHeader_ShowsTheCurrentFolder(t *testing.T) {
	m, _ := newFeatureModel(t)

	if got := m.listHeader(); !strings.Contains(got, "全部") {
		t.Errorf("没过滤时标题是 %q，该写明「全部」", got)
	}

	m.setFolder(m.folderName("INBOX"))
	l := m.listHeader()
	if !strings.Contains(l, "收件箱") {
		t.Errorf("切到收件箱之后标题是 %q", l)
	}
	// 绑的是**标签**（收件箱）不是服务端真名（INBOX）：机器名对用户没有
	// 意义，而且服务端上它可能叫 "Inbox" 或「收件箱」。
	if strings.Contains(l, "INBOX") {
		t.Errorf("标题里露出了服务端真名：%q", l)
	}
	// 列表里还有未读时要把数字带上 —— 换了文件夹之后这是最想知道的事。
	if !strings.Contains(l, "未读") {
		t.Errorf("标题 %q 里没有未读数", l)
	}
}

// 两栏 + 一条竖线首尾相接，**没有一格"缝"**：每一列都属于某一块。
//
// 它挡的是这类漂移：列表栏宽度改了、而 layout 或 hitTest 里某处还留着
// 老数字，于是屏幕上看着分得好好的、点下去却错一块 —— 而且只在边界那一
// 两列上错，最难看出来。
//
// ⚠️ 那条竖线**归正文栏**（hitChat），不归列表也不单设一个区域。理由：
// 它是"两栏之间"的一列，用户不会觉得点那里"不该有反应"。单设一个死区
// 的话，在正文里点空白处就会有概率撞上它、什么也不发生 —— 一个安静的坑。
func TestMouse_EveryBodyColumnBelongsToAPane(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()
	row := l.bodyTop + 2

	names := map[hitRegion]string{
		hitNone: "谁都不属于", hitListHeader: "列表标题行",
		hitList: "列表", hitChat: "正文",
	}
	for _, c := range []struct {
		x    int
		want hitRegion
	}{
		{0, hitList},                     // 列表第一列
		{l.ruleX - 1, hitList},           // 列表最后一列 —— 中间不许有缝
		{l.ruleX, hitChat},               // 两栏之间那一格（现在是空白），归正文
		{l.ruleX + 1, hitChat},           // 正文第一列
		{l.chatX + l.chatW - 1, hitChat}, // 正文最后一列
	} {
		if got, _ := m.hitTest(c.x, row); got != c.want {
			t.Errorf("第 %d 列命中的是「%s」，want「%s」", c.x, names[got], names[c.want])
		}
	}

	// 反过来再走一遍整行：不存在归属不明的列。有的话，点下去既不开会话
	// 也不换文件夹 —— 一个安静的坑，用户只会觉得"这里点不动"。
	for x := 0; x < m.width; x++ {
		if got, _ := m.hitTest(x, row); got == hitNone {
			t.Errorf("第 %d 列%s —— 点下去没有任何反应", x, names[got])
		}
	}
}

// 点会话流最右那一列（滚动条）能让正文跳过去。
func TestMouse_ClickScrollBarJumps(t *testing.T) {
	forceColor(t)
	m, fake := newFeatureModel(t)

	// 夹具里每个会话的正文都是一句话，一屏就放得下 —— 卷不动就没得点。
	// 这条判据要的是「卷得动」，所以自己造一封长的。
	fake.AddMessage("INBOX", mail.Header{
		MessageID: "<long@x>", From: "alice@example.com", FromName: "Alice",
		To: []string{"me@example.com"}, Subject: "很长的信", Date: time.Now(),
	}, strings.Repeat("这是一行很长的正文。\n", 80))
	m = syncOnce(t, m)

	// 走真实路径打开那个会话：光标挪过去 + 回车。
	idx := -1
	for i, th := range m.visible {
		if strings.Contains(strings.Join(th.Peers, ","), "alice@example.com") {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("前提不成立：列表里找不到那个会话")
	}
	m.cursor = idx
	m, _ = update(m, keyMsg("enter"))
	m = loadBodies(t, m)

	if max := m.maxChatScroll(); max == 0 {
		t.Fatalf("前提不成立：正文只有 %d 行，卷不动", m.chatBodyLines())
	}
	l := m.measureLayout()

	// 点最上面一格：应该滚到顶。
	m, _ = update(m, tea.MouseMsg{
		X: l.chatX + l.chatW - 1, Y: l.bodyTop + 1,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if want := m.maxChatScroll(); m.scroll != want {
		t.Errorf("点滚动条顶端之后 scroll = %d，want %d（卷到顶）", m.scroll, want)
	}

	// 点最下面一格：应该回到底部。
	m, _ = update(m, tea.MouseMsg{
		X: l.chatX + l.chatW - 1, Y: l.bodyTop + l.bodyH - 1,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if m.scroll != 0 {
		t.Errorf("点滚动条底端之后 scroll = %d，want 0（回到底部）", m.scroll)
	}
}

// 松开左键不该被当成一次点击 —— 否则一次点击会执行两遍。
//
// 用一个**会留下痕迹**的目标来测：列表标题行的那一下如果算数，会去要
// 文件夹列表并把选择器打开。改成量别的（比如光标位置）就没有这个痕迹，
// 判据会变成"松开左键没做事"的永真式。
func TestMouse_LeftReleaseIsIgnored(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.measureLayout()

	m, cmd := update(m, tea.MouseMsg{
		X: 0, Y: l.bodyTop, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft,
	})
	if cmd != nil || m.mode == modeFolder {
		t.Errorf("松开左键也触发了点击（cmd=%v mode=%v）", cmd != nil, m.mode)
	}
}

// sizedInput 的契约是「给我几列，最多就占几列」。
//
// 这条判据必须**直接量它的输出**：外层的补白和截断会把这个溢出的宽度悄悄
// 抹平 —— 实测过，把那段补差删掉，整行的宽度一点没变（多出来的 3 列被右边
// 那串提示挤掉了），光标也跟着跑到边界外面，而只量整行宽度的判据全绿。
// textinput 的 Width 口径比 lipgloss.Width 少算 3 列（提示符 2 + 光标 1），
// 补差就是为它存在的。
func TestSizedInput_NeverWiderThanAsked(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	m.input.Focus()
	// 必须**打点字进去**。空输入走的是占位符，textinput 那时恰好一列不多
	// （实测：空值 w=40 → 40），补差有没有它都绿 —— 量的会是一个不存在的溢出。
	m.input.SetValue("这是已经打进去的一行字") // 24 列
	m.input.CursorEnd()

	// 只量「字比框短」的那几个宽度。textinput 的可见窗口不是在设 Width 的
	// 时候重算的，而是跟着光标走（bubbles 的 textinput.View 里那句
	// `valWidth <= m.Width`）—— 框比字还窄的时候它把整段字吐出来，sizedInput
	// 压不住；那种情况下靠外层截断兜底（TestView_NoLineOverflowsTheTerminal
	// 量的是整幅画面过不过界），不在这里假装量得到。
	for _, w := range []int{40, 60, 86, 100} {
		if got := lipgloss.Width(m.sizedInput(w)); got != w {
			t.Errorf("要 %d 列，画出来 %d 列", w, got)
		}
	}
}

// 开机必须自己去要文件夹列表。
//
// 左侧导航里的「已发送 / 已删除」是**规范名**（SENT / TRASH），各家服务端上
// 真正的写法可能是 Sent、已发送邮件…… 列表没到手之前那两项会被原样拿去过滤，
// 点进去永远是空的。以前只有 Tab 选择器用得到这份列表，按一下 Tab 就有了；
// 现在导航一开机就把它们摆在那儿，所以判断必须落在 Init 上。
func TestInit_FetchesFolders(t *testing.T) {
	m, _ := newFeatureModel(t)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init 什么都没返回")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init 返回的不是一批命令：%T", cmd())
	}

	var got foldersResultMsg
	found := false
	for _, c := range batch {
		// 定时轮询那条（tea.Tick）会真的睡一觉，不等它。
		msg, inTime := awaitCmd(c, 300*time.Millisecond)
		if !inTime {
			continue
		}
		if r, isFolders := msg.(foldersResultMsg); isFolders {
			got, found = r, true
		}
	}
	if !found {
		t.Fatal("Init 里没有「拉文件夹列表」这一步 —— 导航会指着不存在的文件夹")
	}
	if got.open {
		t.Error("开机那次不该顺手把文件夹选择器打开")
	}
	if got.err != nil || len(got.folders) == 0 {
		t.Errorf("开机拉的文件夹列表是空的：%v（%v）", got.folders, got.err)
	}
}

// awaitCmd 跑一个命令，最多等 d。超时返回 false —— 给会睡觉的定时器用的。
func awaitCmd(cmd tea.Cmd, d time.Duration) (tea.Msg, bool) {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg, true
	case <-time.After(d):
		return nil, false
	}
}

// ---- 键盘 ----

// Esc 一次收起一层筛选：先退文件夹，再退搜索词。
//
// 两层筛选都必须有键盘退路 —— 侧栏是可以用鼠标点出来的、搜索词是回车
// 保留的，只进不出就成了死胡同。
func TestEsc_UnwindsFiltersOneLayerAtATime(t *testing.T) {
	m, _ := newFeatureModel(t)

	// Ctrl+G 循环到「收件箱」。
	m, _ = update(m, keyMsg("ctrl+g"))
	if m.activeFolder == "" {
		t.Fatal("Ctrl+G 没有切换文件夹")
	}

	m.query = "会议"
	m.refreshVisible()

	m, _ = update(m, keyMsg("esc"))
	if m.activeFolder != "" {
		t.Errorf("第一下 Esc 该退掉文件夹，实际还停在 %q", m.activeFolder)
	}
	if m.query != "会议" {
		t.Errorf("第一下 Esc 不该动搜索词，实际 %q", m.query)
	}

	m, _ = update(m, keyMsg("esc"))
	if m.query != "" {
		t.Errorf("第二下 Esc 该退掉搜索词，实际还留着 %q", m.query)
	}

	// 都干净了之后 Esc 什么也不做 —— 列表模式下没有「上一层」可退，
	// 凭空把光标挪回顶部只会让人以为按错了。
	m.cursor = 1
	m, _ = update(m, keyMsg("esc"))
	if m.cursor != 1 {
		t.Errorf("没有筛选时 Esc 挪了光标：%d", m.cursor)
	}
}

// Ctrl+G 在侧栏里循环，末尾绕回开头。
func TestCtrlG_CyclesThroughNavItems(t *testing.T) {
	m, _ := newFeatureModel(t)
	items := m.navItems()
	if len(items) < 2 {
		t.Fatal("前提不成立：侧栏项太少")
	}

	seen := map[string]bool{}
	for i := 0; i < len(items); i++ {
		m, _ = update(m, keyMsg("ctrl+g"))
		seen[m.activeFolder] = true
	}
	if len(seen) != len(items) {
		t.Errorf("循环了 %d 次只到过 %d 个不同的项", len(items), len(seen))
	}
	// 绕回开头：从最后一个再按一次回到第一个。
	if items[0].folder != "" {
		t.Fatalf("第一项应该是「全部」，实际 %q", items[0].folder)
	}
	m.activeFolder = items[len(items)-1].folder
	m, _ = update(m, keyMsg("ctrl+g"))
	if m.activeFolder != "" {
		t.Errorf("末尾再按 Ctrl+G 该绕回「全部」，实际 %q", m.activeFolder)
	}
}

// 换文件夹的三个入口必须落在**同一个地方**，不许各说各话。
//
// 三个入口是：Tab（弹出选择器）、Ctrl+G（循环）、点列表标题行（也是弹选择器）。
// 上一版它们各自算过一份自己的选项列表（选择器用 folderChoices、循环用
// navItems），两份在"列不列服务端上不存在的文件夹"这条上已经开始有别 ——
// 这正是"同一件事有两个出处"的典型症状，趁删列的时候一起收掉了。
//
// 判据绑的是**能到达的文件夹集合**，不是顺序：顺序本来就该不一样（循环
// 得绕圈、选择器要分组），而"哪些地方能去"必须一致。
func TestFolderEntrancesAgreeOnTheDestinations(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	m.folders = []string{"INBOX", "Sent", "Trash", "垃圾邮件"}

	// 入口一：Ctrl+G 循环，把能到的全走一遍。
	cycle := map[string]bool{}
	for i := 0; i < len(m.navItems())+1; i++ {
		m, _ = update(m, keyMsg("ctrl+g"))
		cycle[m.activeFolder] = true
	}

	// 入口二：选择器里列出来的那些。
	cycle[""] = true // 循环的第一项是「全部」，和下面那句话一起兜住边界
	pick := map[string]bool{}
	for _, it := range m.folderPickerItems() {
		pick[it.folder] = true
	}

	for f := range pick {
		if !cycle[f] {
			t.Errorf("选择器能去 %q，而 Ctrl+G 循环到不了", f)
		}
	}
	for f := range cycle {
		if !pick[f] {
			t.Errorf("Ctrl+G 能到 %q，而选择器里没列它", f)
		}
	}

	// 高亮跟着 activeFolder 走 —— 三个入口改的都是同一个字段，所以
	// 这里只需要确认「改完之后画面上的高亮没跑偏」。
	want := selectedBg(t)
	m.setFolder(m.folderName("SENT"))
	m.mode = modeFolder
	for i, it := range m.folderPickerItems() {
		if it.folder == m.activeFolder {
			m.folderCursor = i
		}
	}
	var marked []string
	for _, r := range strings.Split(m.View(), "\n") {
		// 同 TestFolderPicker_HighlightFollowsTheActiveFolder：选择器是居中
		// 的窄栏，底色块不在第 0 格上，得按可见格数数。
		if bgColsOf(r, want) > 0 {
			marked = append(marked, strings.TrimSpace(plainText(r)))
		}
	}
	if len(marked) != 1 || !strings.Contains(marked[0], "已发送") {
		t.Errorf("选择器里高亮的 = %v，want 「已发送」", marked)
	}
}

// ---- 辅助 ----

// labelIndentOf 给出「这一行的文字从第几列开始」，也就是缩进量。
//
// ⚠️ **不能数开头的空格数。** 侧栏选中项的最左一格是强调色标条 ▌（一个
// 可见字符），数空格的写法会把整行的开头算成第 2 列 —— 于是「选中项比组
// 标题缩进得少」，判据红在一个**根本不存在的版式问题**上（实测报的是
// 标题 " 邮箱"、项 "▌   全部"）。标条是缩进的装饰，它占的那一格也要算进去。
func labelIndentOf(s string) int {
	col := 0
	for _, r := range s {
		if r == ' ' || string(r) == blockMark {
			col += cellWidth(r)
			continue
		}
		return col // 撞上第一个真正的文字了
	}
	return col
}

// bgParamOf 从一条 SGR 序列（"\x1b[1;38;5;141;48;5;238m"）里取出**背景色**
// 那一段参数（"48;5;238"）。没有设背景色时返回 ""。
func bgParamOf(seq string) string {
	body, ok := strings.CutPrefix(seq, "\x1b[")
	if !ok {
		return ""
	}
	body, ok = strings.CutSuffix(body, "m")
	if !ok {
		return ""
	}
	toks := strings.Split(body, ";")
	for i := 0; i < len(toks); i++ {
		if toks[i] != "48" || i+1 >= len(toks) {
			continue
		}
		switch toks[i+1] {
		case "5": // 48;5;N（256 色）
			if i+2 < len(toks) {
				return "48;5;" + toks[i+2]
			}
		case "2": // 48;2;R;G;B（真彩）
			if i+4 < len(toks) {
				return "48;2;" + strings.Join(toks[i+2:i+5], ";")
			}
		}
	}
	return ""
}

// bgAtFirstCell 给出「这段字符串里第一个可见字符」是画在什么底色上的。
//
// 这才是用户看得见的事实，也是唯一站得住的量法。**不能**退化成「这段字节里
// 出现过哪些底色序列」：ansi.TruncateLeft 会把切掉的前缀塌成**空序列**留在
// 结果里，于是左边一格标签的底色序列会漏进右边一格的切片 —— 实测 Alice
// 那一段里带着 carol 的 "\x1b[1;38;5;141;48;5;238m\x1b[0m"，而它一个字也
// 没染上。按「出现过没有」去量，两个标签都会显示「铺了底色」。
func bgAtFirstCell(s string) string {
	cur := ""
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := strings.IndexByte(s[i+2:], 'm')
			if j < 0 {
				return cur
			}
			cur = applyBgParam(cur, s[i+2:i+2+j])
			i += 2 + j + 1
			continue
		}
		return cur // 撞上第一个可见字符了，底色就定格在这儿
	}
	return cur
}

// bgColsOf 数出这段字符串里有多少个**可见格**画在指定底色上。
//
// 这是 bgAtFirstCell 的加强版，用在「块不从第 0 列开始」的地方。列表选中项
// 的底色块两侧各内缩一格（见 renderListRow），只量第一个格子的底色，量到的
// 是列表栏自己那条带子 —— 判据会退化成「一个都不匹配」，看着像选中项丢了，
// 其实是尺子只肯看第 0 列。
//
// 它反过来也不能退化成「这段字节里出现过哪些底色序列」（那个坑见
// bgAtFirstCell 的说明）：这里按**可见格**计数，空转义序列一个格子也占不到。
func bgColsOf(s, want string) int {
	cur, n := "", 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := strings.IndexByte(s[i+2:], 'm')
			if j < 0 {
				break
			}
			cur = applyBgParam(cur, s[i+2:i+2+j])
			i += 2 + j + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if cur == want {
			n += cellWidth(r)
		}
		i += size
	}
	return n
}

// applyBgParam 把一条 SGR 参数串（"1;38;5;141;48;5;238"）作用在当前底色上。
//
// 只认和底色有关的那几个：0 / 49 清掉，48;5;N 和 48;2;R;G;B 设上。
func applyBgParam(cur, params string) string {
	toks := strings.Split(params, ";")
	for i := 0; i < len(toks); i++ {
		switch toks[i] {
		case "", "0", "49":
			cur = ""
		case "48":
			if i+1 >= len(toks) {
				continue
			}
			n := 0
			switch toks[i+1] {
			case "5":
				n = 2
			case "2":
				n = 4
			}
			if n > 0 && i+1+n <= len(toks) {
				cur = "48;" + strings.Join(toks[i+1:i+1+n], ";")
				i += n
			}
		}
	}
	return cur
}

// selectedBg 是「选中」那一块的底色参数，顺便把「终端根本没颜色」这件事
// 变成一条明确的失败。
//
// 少了这个守护，忘了 forceColor 的时候 bgSeqOf 会返回空串；空串拿去比，
// 要么恒真（判据退化成空壳还一路绿着），要么全部为假（红得莫名其妙）。
func selectedBg(t *testing.T) string {
	t.Helper()
	p := bgParamOf(bgSeqOf(styleRowSelected))
	if p == "" {
		t.Fatal("拿不到选中底色的序列 —— 这个用例大概忘了 forceColor")
	}
	return p
}

// plainTexts 把一组行转成纯文本。
func plainTexts(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, plainText(l))
	}
	return out
}

// ansiSlice 按**显示列**切出一段字符串（保留其中的样式序列）。
//
// 用 x/ansi 提供的两把尺子从左往右切到 x1、再去掉左边的 x0 列，不自己在
// 测试里数字符 —— 数错的话判据量的就是另一段文字，而它照样会绿。
func ansiSlice(s string, x0, x1 int) string {
	return ansi.TruncateLeft(ansi.Truncate(s, x1, ""), x0, "")
}

// listXInScreen 是列表栏在屏幕上的起始列。
func (m Model) listXInScreen() int {
	return m.measureLayout().listX
}
