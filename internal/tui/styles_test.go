package tui

import (
	"strconv"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// 这一组守的是用户那句「通过深浅白色灰色表示不同元素 text」。
//
// 它翻译成可判的东西只有两件：
//   - 各档**真的不一样**（两档填同一个值 = 没有层次）；
//   - 注释里写了先后关系的那些，**方向是对的**。
//
// ⚠️ 为什么值得单独写一组：上一版所有"次要文字"都靠 `Faint(true)`（SGR 2），
// 而 Windows Terminal 不支持它 —— 实测截图里该变灰的字**一点没变灰**，标题、
// 条目、说明文字亮度一样，整幅画面塌成一片平。那正是「元素堆砌挤压」的另一半。
//
// 现在每一档都是显式的灰。显式的好处是跨终端一致，代价是**可以被静默改坏**：
// 把 fgPreview 填成 fgMuted 那个值，代码照样编译、程序照样跑、其余判据一条
// 也不会红 —— 而列表副行会和已读名字糊成一档。

// greyTier 是灰阶梯子上的一档。
type greyTier struct {
	name  string
	color lipgloss.AdaptiveColor
}

var (
	tierMuted   = greyTier{"fgMuted（已读名字 / 引用条 / 状态栏）", fgMuted}
	tierPreview = greyTier{"fgPreview（列表副行）", fgPreview}
	tierDim     = greyTier{"fgDim（分组标题 / HTML 标记）", fgDim}
	tierTime    = greyTier{"fgTime（时间）", fgTime}
)

// noColorSet 是 lipgloss 用来表示「这一项没设值」的哨兵。
//
// 单独提出来是因为它在 if 条件里不能直接写字面量（`!= lipgloss.NoColor{}`
// 会被解析成块的开始，编译不过），而且写成变量之后，判据的意图也更清楚。
var noColorSet lipgloss.TerminalColor = lipgloss.NoColor{}

// greyIdx 取某一档在指定明暗底上的 256 色号。
//
// ⚠️ 灰阶带是 **232–255**（232 最暗 → 255 最亮），而且是单调的。所以
// 「谁更弱」不能拿数字大小直接比 —— 方向**取决于底色**：
//
//	深色终端：越暗越弱 → 色号越小越弱
//	浅色终端：越亮越弱 → 色号越大越弱
//
// 忘了分底色，这条判据就会在其中一个变体上把梯子读反（而且它自己不会红，
// 只会得出一个反的结论）。
func greyIdx(t *testing.T, c lipgloss.AdaptiveColor, dark bool) int {
	t.Helper()
	s := c.Light
	if dark {
		s = c.Dark
	}
	idx, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%s 的色号 %q 不是 256 色号", c, s)
	}
	if idx < 232 || idx > 255 {
		t.Errorf("灰阶用的是 %d，落在 232–255 这条灰阶带之外 —— "+
			"这一档的语义是「弱」，带色彩的值会被读成「另一种状态」而不是「次要」", idx)
	}
	return idx
}

// 注释里写了先后关系的那些，方向必须对。
//
// 只钉**注释真的claim过**的关系，不自己凑一条全序：把 fgDim 和 fgTime 之间
// 也硬排个序的话，某天有人把这两档对调（完全合理的一次设计调整）就会撞出
// 一条假红 —— 而假红和假绿一样贵。
func TestGreyScale_WeakerTiersAreActuallyWeaker(t *testing.T) {
	relations := []struct {
		stronger greyTier
		weaker   greyTier
		why      string
	}{
		{tierMuted, tierPreview, "副行排在被真正阅读的名字之下（stylePreview 的注释）"},
		{tierMuted, tierDim, "分组标题只是分区、不需要读，比说明文字再弱一档"},
		{tierMuted, tierTime, "时间是背景信息，不该和名字抢注意力"},
		{tierPreview, tierTime, "副行是这一栏里被阅读的那一行，比时间亮一点"},
	}

	for _, dark := range []bool{true, false} {
		variant := "浅色终端"
		if dark {
			variant = "深色终端"
		}
		weaker := func(prev, cur int) bool {
			if dark {
				return cur < prev
			}
			return cur > prev
		}

		for _, r := range relations {
			a := greyIdx(t, r.stronger.color, dark)
			b := greyIdx(t, r.weaker.color, dark)
			if !weaker(a, b) {
				t.Errorf("%s：%s(%d) 并不比 %s(%d) 更弱 —— %s\n"+
					"梯子在这一档断了，两种文字在屏幕上会是同一个分量",
					variant, r.weaker.name, b, r.stronger.name, a, r.why)
			}
		}
	}
}

// 五个灰阶角色两两不许同值。
//
// 这一条比上面那条更要紧：上面那条只管注释claim过的方向，这条管**没有一档
// 白给**。真出现「两档一样」时，屏幕上是两种不同的东西共用一个分量，
// 而所有别的判据（包括上面那条）都可能照样绿。
func TestGreyScale_NoTwoRolesCollapseIntoOneTier(t *testing.T) {
	tiers := []greyTier{
		tierMuted, tierPreview, tierDim, tierTime,
		// 占位提示也放进来：它和别人的**关系**随底色变（注释里那句
		// 「比 fgMuted 亮一点」在浅色底上读法都不一样），但"不许和谁撞上"
		// 这一条与底色无关，一定要守。
		{"fgPlaceholder（输入框占位）", fgPlaceholder},
	}

	for _, dark := range []bool{true, false} {
		variant := "浅色终端"
		if dark {
			variant = "深色终端"
		}
		seen := map[int]string{}
		for _, tier := range tiers {
			idx := greyIdx(t, tier.color, dark)
			if other, dup := seen[idx]; dup {
				t.Errorf("%s：%s 和 %s 都是色号 %d —— 两档一样弱就是没有层次，"+
					"画面上这两种文字会被读成同一类", variant, tier.name, other, idx)
			}
			seen[idx] = tier.name
		}
	}
}

// 「次要」这一类的颜色**不许写死**，必须随底色自适应。
//
// 一个写死的灰在两种底色上必错一种：为深底挑的 240 到了浅底上就是一条
// #585858 的重线；为浅底挑的 250 到了深底上则几乎看不见。而"次要"这个
// 语义本身就和底色绑着 —— 它要求的是「比正文弱」，不是「等于某个色号」。
//
// ⚠️ 这一条和上面 Faint 那条是同一个病的两种形态：`Faint(true)` 把"变弱"
// 交给终端（Windows Terminal 直接不支持），写死的色号把"变弱"交给一个
// 特定的底色。**两者都会在别人的终端上静默失效** —— 自己这里是好的。
//
// 彩色（链接蓝 45、强调色 141）不在此列：它们的语义是**色相**，不是深浅，
// 一个固定的青蓝在深浅底上都还是青蓝。
func TestSecondaryTones_MustBeAdaptive(t *testing.T) {
	for _, c := range []struct {
		name  string
		style lipgloss.Style
	}{
		{"styleMuted（说明文字 / 非当前标签页）", styleMuted},
		{"styleTime（时间）", styleTime},
		{"styleGroupTitle（分组标题）", styleGroupTitle},
		{"styleRowNameRead（已读名字）", styleRowNameRead},
		{"stylePreview（列表副行）", stylePreview},
		{"styleTag（HTML 标记）", styleTag},
		{"styleSenderSelf（自己发的）", styleSenderSelf},
		{"styleSideline（引用竖条）", styleSideline},
		{"stylePlaceholder（输入占位）", stylePlaceholder},
		{"styleMDRule（正文分隔线）", styleMDRule},
		{"styleScrollBar（滚动条轨道）", styleScrollBar},
		{"styleScrollThumb（滚动条滑块）", styleScrollThumb},
	} {
		fg := c.style.GetForeground()
		if fg == noColorSet {
			// 没设前景 = 用渲染默认（最亮那一档）。这对"次要"这一类是
			// 明确的错，不是遗漏。
			t.Errorf("%s 没有前景色 —— 它会落到渲染默认（最亮的一档），"+
				"而它要表达的是「比正文弱」", c.name)
			continue
		}
		if _, ok := fg.(lipgloss.AdaptiveColor); !ok {
			t.Errorf("%s 的前景色是 %#v —— 写死的颜色在深浅两种终端上必错一种，"+
				"这一档必须随底色自适应", c.name, fg)
		}
	}
}

// 列表行里那几段文字**一律不许带背景**。
//
// 名字、时间、副行、分组标题的格子都是**补过空格的定宽格子**（见
// listRowSplit / padRight）。一加底色，右边那些补位空格就变成一条淌到
// 下一列跟前的色带 —— 而这一版列表里唯一的底色块是「当前选中」。
// 两块底色并排出现，用户就分不清哪一条是选中的了。
//
// 查的是**样式自身**，不是渲染出来的那一行：渲染之后再验的话，选中那一行
// 本来就铺着底色，判据会在正确代码上红。
//
// ⚠️ 「没设背景」在 lipgloss 里**不是 nil**：`GetBackground()` 没设值时返回
// 的是 `NoColor{}` 这个哨兵（见 lipgloss v1.1.0 get.go 的 getAsColor ——
// `if !s.isSet(k) { return noColor }`）。写成 `!= nil` 的话它**永远成立**，
// 这条判据就成了一句永远为真的废话 —— 第一版正是这么写的，六行红条全是
// 尺子的错（用户那条规矩：先问"如果被测代码完全正确，这条判据会不会也红"）。
func TestListRowStyles_CarryNoBackground(t *testing.T) {
	for _, c := range []struct {
		name  string
		style lipgloss.Style
	}{
		{"未读名字", styleRowNameUnread},
		{"已读名字", styleRowNameRead},
		{"时间", styleTime},
		{"列表副行", stylePreview},
		{"分组标题", styleGroupTitle},
		{"未读标记", styleUnread},
	} {
		if bg := c.style.GetBackground(); bg != noColorSet {
			t.Errorf("%s 带了背景色 %v —— 它的格子是补过空格的，"+
				"底色会顺着补位空格淌成一条色带", c.name, bg)
		}
	}
}

// 未读名字和已读名字必须是**两档**。
//
// 上一版未读只加了粗体、已读用的是渲染默认前景 —— 而默认前景恰好是最亮的
// 那一档，于是"读过"的比"没读过"的还亮，一栏里所有字一样重。这条判据钉的是
// 「两者前景必须不同」：谁把已读也改成默认前景（哪怕加粗体去区分），
// 两个前景就撞在一起了。
// ⚠️ 「没设前景」是 `NoColor{}` 而不是 nil（同上），所以这里比的是哨兵。
func TestRowNames_UnreadAndReadAreTwoTiers(t *testing.T) {
	un := styleRowNameUnread.GetForeground()
	rd := styleRowNameRead.GetForeground()

	if un == rd {
		t.Errorf("未读和已读的名字用了同一个前景（%v）—— 层次塌了", un)
	}
	if rd == noColorSet {
		t.Error("已读名字没有显式前景色 —— 它会落到渲染默认（也就是最亮的那一档），" +
			"比未读还亮")
	}
	if !styleRowNameUnread.GetBold() {
		t.Error("未读名字没有加粗 —— 未读是这一栏里最该被一眼看见的")
	}
}

// 自己发的消息，名字要比别人的**暗**。
//
// 用户的原话是「通过深浅白色灰色表示不同元素 text」。落到消息头上就是：
// 一屏里最亮的名字应该是**别人**的 —— 那是等你说的话的人；自己那几条是
// 你已经知道内容的旧话。反过来（自己最亮）会让每次翻聊天都先看到自己。
//
// 判据是「两者前景不同，且自己的那一档是显式给的灰」：显式给灰意味着它
// 一定比渲染默认暗，不管终端主题是深是浅。
func TestSenderSelf_IsDimmerThanOthers(t *testing.T) {
	self := styleSenderSelf.GetForeground()
	other := styleSender.GetForeground()

	if self == other {
		t.Errorf("自己发的和别人的名字用了同一个前景（%v）—— "+
			"一屏里最亮的名字会变成自己的", self)
	}
	if self == noColorSet {
		t.Error("自己发的名字没有显式前景色 —— 它会落到渲染默认，也就是最亮的那一档")
	}
}
