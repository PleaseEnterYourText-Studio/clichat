package tui

import (
	"strings"
	"testing"
	"time"

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
// 顶部标签页只在「最近开过两个会话」之后才出现，所以要真的走一遍切换，
// 不能直接改 recent —— 那样测的是夹具，不是功能。
func openTwoPane(t *testing.T) Model {
	t.Helper()
	m, _ := newFeatureModel(t)
	m, _ = update(m, keyMsg("enter"))     // 打开第一条
	m, _ = update(m, keyMsg("ctrl+down")) // 换到第二条（这一步才产生第二个「最近打开」）
	m = loadBodies(t, m)

	l := m.layout()
	if !l.twoPane {
		t.Fatalf("前提不成立：宽度 %d 降级成了单栏", m.width)
	}
	if l.tabsRow < 0 {
		t.Fatalf("前提不成立：标签页没出现（recent=%d 条）", len(m.recent))
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
			l := m.layout()

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

			// 横向同理：导航 + 列表 + 竖线 + 会话流 = 整幅宽。
			if l.twoPane {
				if l.navW != navWidth+navGutter {
					t.Errorf("%dx%d：导航栏宽 %d，want %d（侧栏 + 空隙）",
						w, h, l.navW, navWidth+navGutter)
				}
				if l.chatX+l.chatW != w {
					t.Errorf("%dx%d：会话流右边缘在 %d 列，屏幕 %d 列", w, h, l.chatX+l.chatW, w)
				}
				if l.chatX != l.listX+l.listW+1 {
					t.Errorf("%dx%d：竖线不在列表和会话流中间（chatX=%d）", w, h, l.chatX)
				}
			} else if l.navW != 0 || l.listX != 0 {
				t.Errorf("%dx%d：单栏模式下不该有导航栏（navW=%d listX=%d）", w, h, l.navW, l.listX)
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
	l := m.layout()
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

// 单栏（窄终端）不许出现侧栏，也不许把内容顶出去。
func TestLayout_NarrowTerminalDropsTheSidebar(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if l := m.layout(); l.twoPane {
		t.Fatalf("80 列不该是双栏")
	}
	if strings.Contains(m.View(), "邮箱") {
		t.Error("单栏模式里画出了侧栏的内容")
	}
	for i, line := range viewLines(m) {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("第 %d 行宽 %d 列，终端只有 %d 列", i, w, m.width)
		}
	}
}

// ---- 浮起的输入区 ----

// 输入框要「浮起」：上下各一整行空白，那一行自己铺着底色。
//
// 那两行留白是**这个方案成立的前提**：终端里没有圆角、没有阴影、没有
// z 轴，把留白去掉，输入框就退回成又一条贴底的横带。所以这条判据量的
// 不是「好不好看」，是「浮起还在不在」。
func TestInputBlock_FloatsWithBlankLinesAround(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.layout()

	if l.inputRows != inputBlockHeight {
		t.Fatalf("输入区占 %d 行，want %d", l.inputRows, inputBlockHeight)
	}
	lines := viewLines(m)

	for _, off := range []int{0, 2} {
		if got := strings.TrimSpace(plainText(lines[l.inputTop+off])); got != "" {
			t.Errorf("输入框上/下那行不是空白：%q", got)
		}
	}

	row := lines[l.inputTop+1]
	if !strings.Contains(row, bgSeqOf(styleInputRow)) {
		t.Error("输入那一行没有铺底色，浮不起来")
	}
	// 底色块不许贴到终端右边缘：留出那一格，它才像一块浮着的东西，
	// 而不是一条从边到边的横带。
	if w := lipgloss.Width(row); w >= m.width {
		t.Errorf("输入那一行铺到了第 %d 列（终端 %d 列），右边没留出边距", w, m.width)
	}
	// 左边也不许压到侧栏上。
	if !strings.Contains(row, navBgSeq()) {
		t.Error("侧栏没有贯到输入区这一行，看着像导航栏只有一半高")
	}
}

// 终端太矮时，留白让给正文：输入区退回单行贴底。
//
// 「留白是奢侈品，正文优先」这件事要有判据守着，否则某次改动会把矮
// 终端下的正文挤得只剩一行，而谁也不会在宽终端上发现。
func TestInputBlock_FallsBackToASingleLineWhenShort(t *testing.T) {
	m, _ := newFeatureModel(t)
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 10})

	if l := m.layout(); l.inputRows != 1 {
		t.Errorf("10 行的终端里输入区占 %d 行，want 1", l.inputRows)
	}
	if got := len(viewLines(m)); got != 10 {
		t.Errorf("画面 %d 行，want 10", got)
	}
}

// 列表模式没有输入框，那一行就**不该**铺输入框的底色 —— 铺了等于告诉
// 用户那里可以打字。
func TestInputBlock_NoCapsuleInListMode(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	if m.mode != modeList {
		t.Fatalf("前提不成立：mode=%v", m.mode)
	}

	l := m.layout()
	if strings.Contains(viewLines(m)[l.inputTop+1], bgSeqOf(styleInputRow)) {
		t.Error("列表模式的那一行铺了输入框的底色")
	}
	if !strings.Contains(viewLines(m)[l.inputTop+1], "q") {
		t.Error("列表模式下这一行应该是常驻快捷键提示")
	}
}

// 输入框那一行必须占满「侧栏 + 空隙 + 内容区」，右边只留一列。
//
// 这条管的是**对齐**，不是 sizedInput 的补差（那是
// TestSizedInput_NeverWiderThanAsked 的事）：整行的宽度是外层补白定的，
// 输入框自己多吐 3 列也会被截掉，看不出来。
func TestInputLine_FillsItsWidth(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.layout()
	row := viewLines(m)[l.inputTop+1]

	// 这一行的宽度：侧栏 + 空隙 + 输入块。块宽就是内容区宽，右边那格留白
	// 不在块里 —— 所以整行应该是屏宽减一，而不是屏宽。
	want := l.contentW + l.contentX
	if got := lipgloss.Width(row); got != want {
		t.Errorf("输入那一行宽 %d 列，want %d（侧栏 %d + 空隙 %d + 块宽 %d）；屏宽 %d，右边那一列留白",
			got, want, navWidth, navGutter, l.contentW, m.width)
	}
}

// ---- 两级侧栏 ----

// 侧栏是一条**从上到下贯通的**竖带：每一行都铺着底色（选中那一项压着
// 一块更亮的，其余是同一条带子）。
//
// 这不是审美问题：底色只在有内容的那几行上，侧栏就变成几块飘着的色斑，
// 和列表栏之间那道竖向的分界也就断了。而这件事最容易坏的方式是
// 「用 styleNav.Render(整块) 一次铺完」—— 那样只有第一行有底色
// （原因见 paintRowBg 的注释），画面看着还行，用户只会觉得
// 「那条带子下面淡了」，没人会报 bug。
func TestNav_IsOneContinuousBand(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	navBg := bgParamOf(bgSeqOf(styleNav))
	if navBg == "" {
		t.Fatal("拿不到侧栏底色的序列 —— 这个用例大概忘了 forceColor")
	}

	rows := strings.Split(m.renderNav(m.layout()), "\n")
	if len(rows) < 5 {
		t.Fatalf("侧栏只有 %d 行，前提不成立", len(rows))
	}
	active := selectedBg(t)
	for i, r := range rows {
		switch got := bgAtFirstCell(r); got {
		case "":
			t.Errorf("侧栏第 %d 行没铺底色 —— 一条贯通的带子断在这里", i)
		case navBg, active:
			// 正常：要么是那条带子，要么是压在带子上的选中块。
		default:
			t.Errorf("侧栏第 %d 行的底色是 %q，want %q（选中的那项是 %q）", i, got, navBg, active)
		}
	}
}

// 侧栏要真的分两级：组标题 + 组里的项。
func TestNav_HasTwoLevels(t *testing.T) {
	m, _ := newFeatureModel(t)
	rows := m.navRows()

	if len(rows) == 0 {
		t.Fatal("侧栏一行都没有")
	}
	// 第一行是组标题，第二行起才是项。
	if !strings.Contains(plainText(rows[0]), "邮箱") {
		t.Errorf("侧栏第一行应该是「邮箱」这个组标题，实际 %q", plainText(rows[0]))
	}
	// 组里的项要缩进，和组标题错开 —— 这是「两级」在画面上唯一的表达。
	title := plainText(rows[0])
	item := plainText(rows[1])
	if indentOf(item) <= indentOf(title) {
		t.Errorf("组里的项没比组标题缩进：标题 %q、项 %q", title, item)
	}

	for _, want := range []string{"全部", "收件箱", "已发送", "已删除"} {
		if !strings.Contains(strings.Join(plainTexts(rows), "\n"), want) {
			t.Errorf("侧栏里没有 %q", want)
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

// 切文件夹的时候，侧栏上的数字不许跟着变。
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

// 侧栏的选中态直接就是 activeFolder：不许出现「侧栏高亮着已发送、
// 列表却在显示收件箱」这种对不上号的画面。
func TestNav_HighlightFollowsTheActiveFolder(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m, _ := newFeatureModel(t)

	for _, it := range m.navItems() {
		m.setFolder(it.folder)
		// 量的是**画出来的**侧栏：每一行都由 renderNav 铺过底色，所以
		// 「第一个格子的底色」就是用户看到的那块色块。
		var highlighted []string
		for _, r := range strings.Split(m.renderNav(m.layout()), "\n") {
			if bgAtFirstCell(r) == want {
				highlighted = append(highlighted, strings.TrimSpace(plainText(r)))
			}
		}
		if len(highlighted) != 1 {
			t.Fatalf("选中的侧栏项有 %d 个（folder=%q）：%v", len(highlighted), it.folder, highlighted)
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
	if l := m.layout(); l.tabsRow != -1 {
		t.Errorf("一个会话都没开时就有标签页了（tabsRow=%d）", l.tabsRow)
	}

	m, _ = update(m, keyMsg("enter"))
	if l := m.layout(); l.tabsRow != -1 {
		t.Errorf("只开了一个会话就有标签页了（tabsRow=%d）", l.tabsRow)
	}

	m, _ = update(m, keyMsg("ctrl+down"))
	if l := m.layout(); l.tabsRow != 0 {
		t.Errorf("开过两个会话之后标签页该出现，实际 tabsRow=%d", l.tabsRow)
	}
}

// 标签页按 MRU 排，当前那一个在最前面并且铺着底色。
func TestTabs_MRUFrontAndActiveMarked(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m := openTwoPane(t)

	chips := m.tabChips(m.layout())
	if len(chips) != 2 {
		t.Fatalf("标签页有 %d 个，want 2", len(chips))
	}
	if chips[0].id != m.activeID {
		t.Errorf("第一个标签是 %q，当前会话是 %q —— 不是 MRU 序", chips[0].id, m.activeID)
	}

	// 标签的坐标是内容区里的相对列，画面是屏幕列，所以要加上侧栏那一截。
	l := m.layout()
	row := viewLines(m)[l.tabsRow]
	var marked []string
	for _, c := range chips {
		seg := ansiSlice(row, l.contentX+c.x0, l.contentX+c.x1)
		if bgAtFirstCell(seg) == want {
			marked = append(marked, c.id)
		}
	}
	if len(marked) != 1 || marked[0] != m.activeID {
		t.Errorf("铺了底色的标签是 %v，当前会话是 %q", marked, m.activeID)
	}

	// 第一个标签必须正好压在**内容区的左边缘**上：左边侧栏、中间列表、
	// 下面输入框全都从这一列起步，标签页偏出十几列的话，一眼就看得出没对齐。
	if got := bgAtFirstCell(ansiSlice(row, l.contentX, l.contentX+1)); got != want {
		t.Errorf("第 %d 列（内容区左边缘）的底色是 %q，want %q —— 第一个标签没和内容区对齐",
			l.contentX, got, want)
	}
}

// 点标签页能切过去，而且切过去之后它自己会排到最前面。
//
// **在画出来的那一行上去找标签所在的列**，而不是拿 tabChips 声明的坐标去点。
// 声明错了、而渲染和命中测试都跟着错的时候，只比对内部字段的判据全是绿的
// —— 这条用例就这么假绿过一次：chips 宣称第一个标签在第 13 列，实际画在
// 第 26 列（renderTabs 里 withNavStrip 的那一下被漏算了），点自己声明的地方
// 当然还是「对」的。
func TestTabs_ClickSwitchesThread(t *testing.T) {
	forceColor(t)
	m := openTwoPane(t)
	l := m.layout()

	row := plainText(viewLines(m)[l.tabsRow])
	for _, c := range m.tabChips(l) {
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
		if first := got.tabChips(got.layout())[0].id; first != c.id {
			t.Errorf("刚点过的 %q 没排到最前面（第一个是 %q）", c.id, first)
		}
	}
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
func TestList_HighlightAlwaysMatchesTheCursor(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m, _ := newFeatureModel(t)

	for i := range m.visible {
		m.cursor = i
		lines := strings.Split(m.renderThreadList(m.listWidth(), m.bodyHeight()), "\n")

		var marked []string
		for _, ln := range lines {
			if bgAtFirstCell(ln) == want {
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
func TestList_SelectedRowUsesBackgroundNotReverse(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m, _ := newFeatureModel(t)
	m.cursor = 0

	lines := strings.Split(m.renderThreadList(m.listWidth(), m.bodyHeight()), "\n")
	var marked []string
	for _, ln := range lines {
		if bgAtFirstCell(ln) == want {
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
	l := m.layout()

	pane := strings.Split(m.renderThreadList(l.listW, l.bodyH), "\n")
	if len(pane) < 6 {
		t.Fatalf("列表栏只有 %d 行，前提不成立", len(pane))
	}
	// 第 0 行是列表标题（当前文件夹 / 搜索词），不是会话。
	for k := 1; k < len(pane); k++ {
		text := plainText(pane[k])
		want, isThread := threadNamed(m.visible, text)

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

// threadNamed 从列表里一行的文字认出它是哪条会话（标题行、主题行都算）。
//
// 认不出来就是分组标题或者空白行。取**最长**的那个匹配：主题里提到对方名字
// 的时候，短的标题会先撞上。
func threadNamed(list []thread.Thread, text string) (thread.Thread, bool) {
	best := -1
	var hit thread.Thread
	for _, th := range list {
		for _, s := range []string{threadTitle(th), th.Subject} {
			if s != "" && strings.Contains(text, s) && len(s) > best {
				best, hit = len(s), th
			}
		}
	}
	return hit, best >= 0
}

// 点侧栏的一项就是切文件夹。
func TestMouse_ClickNavSwitchesFolder(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.layout()

	// 在**画出来的**侧栏里找「已发送」在第几行。
	rows := m.navRows()
	y := -1
	for i, r := range rows {
		if strings.Contains(plainText(r), "已发送") {
			y = l.bodyTop + i
			break
		}
	}
	if y < 0 {
		t.Fatal("侧栏里没有「已发送」")
	}

	m, _ = update(m, tea.MouseMsg{
		X: 1, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})

	if want := m.folderName("SENT"); m.activeFolder != want {
		t.Fatalf("点了「已发送」之后 activeFolder = %q，want %q", m.activeFolder, want)
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

// 侧栏右边那一格空隙不属于任何一栏：点它不该有任何反应。
//
// 归给左边会让人误切文件夹，归给右边会误开一封邮件 —— 留白就该是留白。
func TestMouse_ClickOnNavGutterDoesNothing(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.layout()

	before := m.activeFolder
	cursorBefore := m.cursor
	m, _ = update(m, tea.MouseMsg{
		X: navWidth, Y: l.bodyTop + 2,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})

	if m.activeFolder != before {
		t.Errorf("点空隙把文件夹换成了 %q", m.activeFolder)
	}
	if m.cursor != cursorBefore || m.mode != modeList {
		t.Errorf("点空隙打开了会话（mode=%v cursor=%d）", m.mode, m.cursor)
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
	l := m.layout()

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
func TestMouse_LeftReleaseIsIgnored(t *testing.T) {
	forceColor(t)
	m, _ := newFeatureModel(t)
	l := m.layout()

	rows := m.navRows()
	y := -1
	for i, r := range rows {
		if strings.Contains(plainText(r), "已发送") {
			y = l.bodyTop + i
			break
		}
	}
	if y < 0 {
		t.Fatal("侧栏里没有「已发送」")
	}

	m, _ = update(m, tea.MouseMsg{
		X: 1, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft,
	})
	if m.activeFolder != "" {
		t.Errorf("松开左键也触发了点击：activeFolder = %q", m.activeFolder)
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

// 侧栏和 Tab 那个文件夹选择器操作的是同一件事，不许各说各话。
func TestNavAndFolderPickerAgreeOnTheFolder(t *testing.T) {
	forceColor(t)
	want := selectedBg(t)
	m, _ := newFeatureModel(t)
	m.folders = []string{"INBOX", "Sent", "Trash"}

	m.setFolder(m.folderName("SENT"))
	m.mode = modeFolder
	m.folderCursor = 0
	for i, name := range m.folderChoices() {
		if name == m.activeFolder {
			m.folderCursor = i
		}
	}
	m, _ = update(m, keyMsg("enter"))

	if m.activeFolder != m.folderName("SENT") {
		t.Errorf("从选择器里确认之后 activeFolder = %q", m.activeFolder)
	}
	// 侧栏上高亮的还应该是「已发送」那一项。
	var marked []string
	for _, r := range strings.Split(m.renderNav(m.layout()), "\n") {
		if bgAtFirstCell(r) == want {
			marked = append(marked, strings.TrimSpace(plainText(r)))
		}
	}
	if len(marked) != 1 || !strings.Contains(marked[0], "已发送") {
		t.Errorf("侧栏高亮 = %v，want 「已发送」", marked)
	}
}

// ---- 辅助 ----

// indentOf 数一行开头的空格数（缩进）。
func indentOf(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
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
	return m.layout().listX
}
