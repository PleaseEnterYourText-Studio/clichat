#!/usr/bin/env python3
"""把 docs/images/raw/*.ansi 渲染成 README 用的终端截图 PNG。

    python tools/render-screenshots.py            # 渲染全部
    python tools/render-screenshots.py --dump     # 只打印底色分布，不渲染

素材（.ansi）从哪来见 internal/tui/screenshot_test.go 顶部的注释。本脚本
负责「ANSI -> 好看的 PNG」这一段。

## 为什么不再用 freeze

前面几版是 `freeze（ANSI -> SVG）+ Edge（SVG -> PNG）`。它挑不出毛病
的是**字体**（自带 JetBrains Mono，不依赖系统装了什么），但**底色矩形
画不对**，而且不对得很有系统性：

  - 它给底色矩形算宽度用的字符步进是 **9.03px**，而排文字用的
    JetBrains Mono 是 **7.8px**（0.6em @ 13px）。于是每一块底色的右沿
    都比它盖住的文字多出约 16% —— 一条通栏的色块看不出这个差，
    **一块贴合内容的色块一眼就看出来了**。
  - `\\x1b[0m`（SGR 重置）之后的底色它接不上：一行里被重置切开的底色
    会被它画成**几块互相重叠的矩形**，最后一块还可能一直铺到窗口右沿
    之外。浏览器里那句 `48;5;238m ABC 0m 48;5;238m  49m` 应该是一块
    宽 6 列的气泡，截图里却是「从气泡左边一直拉到屏幕右边」。

这两条合起来，恰好把这一版最要紧的设计（**气泡贴合内容、不通栏**）
在截图里抹平了 —— README 反倒成了唯一看不出「气泡是贴合的」的地方。
之前那版气泡本来就是通栏的，所以看不出来；这一版把它暴露了。

（上面两条都是量出来的：拿最小 ANSI 输入喂 freeze，比对 SVG 里 rect 的
x/width。不是猜的。）

所以这一版改成**自己解析**：ANSI -> 单元格网格 -> HTML -> Edge 截图。
底色范围于是完全由我们说了算，一格都不会错。

字体仍然用 JetBrains Mono —— 从 freeze 早先产出的 SVG 里把内嵌的字体
抠出来存到 `docs/images/raw/_font.*`（见 ensure_font）。那份 SVG 还在，
所以这条依赖不会平白断掉。

## 字体的两个坑（都踩过，都很难看出来）

**一、`@font-face` 的 `format()` 写错，浏览器是「静默退回」，不会报错。**

SVG 里那份字体的 MIME 写的是 `application/x-font-woff2`，我们照抄成
`format('woff2')`。可它的字节头是 `\x00\x01\x00\x00` —— 那是**裸 TTF**，
不是 WOFF2（WOFF2 的头是 `wOF2`）。声明对不上，浏览器就丢弃整个
`@font-face`，一路退到通用字体。整个页面的字全变了，**但一张图也不会
报错**，只有字形看着"有点怪"。所以 ensure_font 现在按**字节头**判格式，
而且 measure_font 会把"字体到底加载上没有"当成一条硬失败。

**二、退回成比例字体时，连"等宽"这件事都没了。**

上面那个 bug 真发生的时候，量出来的步进是 `M`=10.56、`0`=7.22、
空格=2.91 —— 一个比例字体。底色块是用空格铺的，空格宽度不对，
底色宽度就不对；而**贴合内容的底色块**（这一版的气泡）于是全线偏。

**三、CJK 有个原生步进，不等于 2 格。**

就算 JetBrains Mono 正常加载（ASCII 步进 7.8，正好 0.6em），
汉字是回退到系统 CJK 字体画的，实测步进 **13.0px**（它占的 2 格应该是
15.6）。汉字比自己的格子**窄**，所以每多一个汉字，这一行后面所有内容
就**左漂 2.6px** —— 一行里二十个汉字就是 52px，六个多格。截图上的表现
是滚动条碎成几段小竖线、右边缘参差不齐，看着像渲染坏了。

所以现在：底色矩形和文字 run 都**绝对定位到格子坐标**（底色矩形的
宽度由列数算，与字体无关）；宽字符**逐字**放进一个 2 格宽、居中的盒子，
位置只由格子决定。字体怎么变，格子都不动。

## 一次渲染要经过什么

    .ansi ──parse──> grid（每格一个字符 + 前景/背景/粗体）
         ──to_html──> 一段 <pre>，相邻同样式的格子合并成一个 <span>
         ──Edge──>    PNG（2 倍像素密度，截完按 alpha 裁掉透明边）

## 依赖

- Edge 或 Chrome：Windows 自带 Edge，路径见 EDGE_CANDIDATES
- Pillow（裁透明边用）：`pip install pillow`
- 字体：首次运行会自动从旧 SVG 里抠出 `_font.*`；抠不到就退回
  Consolas，字形不同但不会失败
"""

import base64
import html
import os
import re
import subprocess
import sys
import unicodedata

# ---- 外观 ----
#
# 配色跟着 Catppuccin Mocha 走：背景 base #1e1e2e，边框 surface0 #313244。
# 注意这里**只**决定窗口底色和边框；应用发出的 256 色号（如 38;5;42）
# 由 xterm256() 照实还原，没有被美化过。
BG = "#1e1e2e"
BORDER = "#313244"
FG_DEFAULT = "#c4c4c4"
FONT_SIZE = 13
# 行高是 f(字号)，不是常数：13px 那一档对齐 freeze 那一版（13 * 1.477…）。
# 单独放大某张图时（见 BIGGER_FONT），行高必须跟着放大，否则字会挤在一起。
LINE_RATIO = 19.2 / 13
CELL_W = FONT_SIZE * 0.6  # JetBrains Mono 的 advance width 正好是 0.6em
PADDING = 20
MARGIN = 24
SCALE = 2  # 设备像素比：截图按 2x 出，文字锐利得多
# 窗口标题栏：三点独占一条横带，不与内容抢位置（11px 球 + 上下各留一点，
# 26px 是"看得像标题栏、又不浪费高度"的那个值）。
TITLEBAR_H = 26
TITLEBAR_DOT_TOP = 8

# 单独放大的几张图。
#
# 05-zen-home 是唯一一张把 **ANSI Shadow 字标**当主体的图。那种字形的笔画是
# 2 格宽、里面还嵌一条 1 格宽的"阴影线"—— 13px 下那条线正好吃掉笔画的一半，
# 字标看着像一堆空心方框。放大一档（笔画和阴影线按比例一起变粗）之后才认得出
# 是字标。
#
# 只影响截图：产品里字标就是终端字号，用户自己调。
BIGGER_FONT = {"05-zen-home": 20}


def line_height(size):
    """字号 -> 行高。渲染的每一处都得走它，别退回写死的常数。"""
    return size * LINE_RATIO


# 实际用的格子宽度由 measure_font 量出来（见那个函数的注释）。这个常量
# 只是「JetBrains Mono 正常加载时应该是多少」，用来做合理性区间检查 ——
# 量出来差太多，说明字体根本没加载、又退回比例字体了。
CELL_W_MIN_RATIO = 0.5
CELL_W_MAX_RATIO = 0.75  # 相对 FONT_SIZE 的步进比例；等宽字体都在这个带里

EDGE_CANDIDATES = [
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    # macOS：--headless=new 在 Chrome / Edge 的 mac 版上都能用。路径是
    # .app 里的可执行文件本体，不是 .app 目录。
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
]


def find_edge():
    for p in EDGE_CANDIDATES:
        if os.path.isfile(p):
            return p
    return None


# ---------------------------------------------------------------- 颜色

def xterm256():
    """xterm 的 256 色表：16 基础色 + 6x6x6 色立方 + 24 级灰阶。

    值直接取自 xterm 的实现。必须自己算而不是「差不多给个灰」——
    这一版整个设计就是靠几档底色的**差值**撑起来的（相邻面板至少差 2 格），
    色表偏一格，截图里两块面板就分不开了。
    """
    t = {}
    base = [(0, 0, 0), (128, 0, 0), (0, 128, 0), (128, 128, 0),
            (0, 0, 128), (128, 0, 128), (0, 128, 128), (192, 192, 192),
            (128, 128, 128), (255, 0, 0), (0, 255, 0), (255, 255, 0),
            (0, 0, 255), (255, 0, 255), (0, 255, 255), (255, 255, 255)]
    for i, c in enumerate(base):
        t[i] = c
    lv = [0, 95, 135, 175, 215, 255]
    n = 16
    for r in range(6):
        for g in range(6):
            for b in range(6):
                t[n] = (lv[r], lv[g], lv[b])
                n += 1
    for i in range(24):
        v = 8 + i * 10
        t[232 + i] = (v, v, v)
    return t


XTERM = xterm256()


def css(cell_fg):
    """把一个颜色值（256 号或 (r,g,b)）变成 CSS 颜色。"""
    if cell_fg is None:
        return None
    if isinstance(cell_fg, tuple):
        return "#%02x%02x%02x" % cell_fg
    rgb = XTERM.get(cell_fg)
    if rgb is None:
        return None
    return "#%02x%02x%02x" % rgb


# ---------------------------------------------------------------- 解析

# lipgloss 会输出的序列就这几种；不认识的一律跳过（宁可丢样式，不能丢文字）。
SGR_RE = re.compile(r"\x1b\[([0-9;]*)m")
OSC8_RE = re.compile(r"\x1b\]8;;[^\x1b\x07]*(?:\x1b\\|\x07)")
CSI_RE = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")


def cell_width(ch):
    """一个字符占几列。

    不能只看 `wcwidth`：『East Asian Ambiguous』那一类（• — … ①）在不同
    终端里是 1 列还是 2 列没有共识，而**应用侧已经钉死了口径**（见
    internal/tui/styles.go 的 cellWidth：一律 1 列）。两边口径必须一致，
    否则网格会比实际输出多算一格，整幅图右侧错位一格。
    组合符（比如 emoji 的变体选择符）占 0 列。
    """
    if unicodedata.combining(ch):
        return 0
    if unicodedata.east_asian_width(ch) in ("W", "F"):
        return 2
    return 1


class Cell:
    __slots__ = ("ch", "fg", "bg", "bold", "italic")

    def __init__(self):
        self.ch = " "
        self.fg = None
        self.bg = None
        self.bold = False
        self.italic = False

    def style(self):
        return (self.fg, self.bg, self.bold, self.italic)

    def text_style(self):
        """文字 run 的合并键：**不含底色**。

        底色是另一套元素（绝对定位的矩形），文字不靠它定位 —— 把底色
        也当合并键的话，气泡里和气泡外同样颜色的字会被切成两段，
        run 变多而图没变好看。
        """
        return (self.fg, self.bold, self.italic)


def plain_width(s):
    """一行 ANSI 文本的**显示宽度**（列数），按应用侧的口径算。

    ⚠️ 不能数字符个数。一行里只要有汉字，字符数就比列数少 —— 数 ch
    的话 104 列的图会被认成 90 列，网格右边 14 列全是空的，而屏幕上
    看起来只是「右边少了一截」，很容易被当成布局问题去查。
    """
    s = re.sub(r"\x1b\[[0-9;?]*[A-Za-z]|\x1b\]8;;[^\x1b]*\x1b\\", "", s)
    return sum(cell_width(ch) for ch in s)


def parse_ansi(text, cols):
    """把 ANSI 文本铺进一个网格。

    cols 用来处理折行之外的情况：超出右沿的字符丢弃（和真终端一样，
    终端不会因为一行的字符多了就把它推到下一行）。
    """
    grid = []
    row = None
    col = 0
    cur = Cell()  # 当前 SGR 状态

    def new_row():
        nonlocal row, col
        row = [Cell() for _ in range(cols)]
        grid.append(row)
        col = 0

    def put(index, ch):
        """写一格，**把当前样式快照进去**。

        ⚠️ 样式必须在**写字符的那一刻**取，不能在建行时统一复制一遍 ——
        后者看起来等价，其实整幅图会一点底色都没有：底色是在行中间
        （`48;5;237m` + 空格）才设上的，建行时那个样式还不存在。
        """
        c = row[index]
        c.ch = ch
        c.fg, c.bg, c.bold, c.italic = cur.fg, cur.bg, cur.bold, cur.italic

    new_row()

    i = 0
    n = len(text)
    while i < n:
        ch = text[i]

        if ch == "\x1b":
            m = OSC8_RE.match(text, i)
            if m:  # 超链接：应用侧会把它包成 OSC 8，这里只留文字（真终端
                   # 会显示成可点，静态截图里点不动，加下划线反而是假的）
                i = m.end()
                continue
            m = SGR_RE.match(text, i)
            if m:
                apply_sgr(cur, m.group(1))
                i = m.end()
                continue
            m = CSI_RE.match(text, i)
            if m:
                i = m.end()
                continue
            i += 1
            continue

        if ch == "\n":
            new_row()
            i += 1
            continue
        if ch == "\r":
            col = 0
            i += 1
            continue
        if ch == "\t":
            w = 4 - (col % 4)
            for _ in range(w):
                if col < cols:
                    put(col, " ")
                    col += 1
            i += 1
            continue
        if ch == "\x00":
            i += 1
            continue

        w = cell_width(ch)
        if w == 0:  # 组合符并到前一格上
            if col > 0:
                row[col - 1].ch += ch
            i += 1
            continue
        if col + w > cols:  # 超出右沿：丢弃（同真终端）
            i += 1
            continue
        put(col, ch)
        col += 1
        for k in range(1, w):
            # 宽字符的第二格：留一个空字符串占位，样式跟随前格 —— 这样
            # 合并 run 的时候它不会把一段连续样式切断。
            put(col, "")
            col += 1
        i += 1

    return grid


def apply_sgr(cur, params):
    """按分号参数更新当前样式。

    必须**顺序消费**：`38;5;141` / `48;2;30;30;46` 这类序列里，参数不是一个
    个独立开关，而是一组。逐个查表的话 5、141、2、30 都会被当成单独的设置，
    颜色当场乱掉 —— 而乱掉的表现是「有几行颜色怪」，很容易被当成设计问题。
    """
    if params == "":
        parts = [0]
    else:
        parts = [int(p) if p.isdigit() else 0 for p in params.split(";")]

    i = 0
    while i < len(parts):
        p = parts[i]
        if p == 0:
            cur.fg = cur.bg = None
            cur.bold = cur.italic = False
        elif p == 1:
            cur.bold = True
        elif p == 3:
            cur.italic = True
        elif p == 22:
            cur.bold = False
        elif p == 23:
            cur.italic = False
        elif p == 39:
            cur.fg = None
        elif p == 49:
            cur.bg = None
        elif 30 <= p <= 37:
            cur.fg = p - 30
        elif 90 <= p <= 97:
            cur.fg = p - 90 + 8
        elif 40 <= p <= 47:
            cur.bg = p - 40
        elif 100 <= p <= 107:
            cur.bg = p - 100 + 8
        elif p in (38, 48):
            target_fg = p == 38
            if i + 1 < len(parts) and parts[i + 1] == 5 and i + 2 < len(parts):
                v = parts[i + 2]
                if target_fg:
                    cur.fg = v
                else:
                    cur.bg = v
                i += 2
            elif i + 1 < len(parts) and parts[i + 1] == 2 and i + 4 < len(parts):
                rgb = (parts[i + 2], parts[i + 3], parts[i + 4])
                if target_fg:
                    cur.fg = rgb
                else:
                    cur.bg = rgb
                i += 4
        i += 1


# ---------------------------------------------------------------- HTML

def cell_class(ch):
    """这一格占几列：2 = 宽字符（汉字、全角标点），1 = 其余。

    按**基础字符**判：组合符是并到前一格上的（见 parse_ansi），
    一个格子可能是 "e" + 三个组合符拼出来的。
    """
    return 2 if ch and cell_width(ch[0]) == 2 else 1


# 用 CSS 画的几何符号。值 = (形状, 线宽占几列)。
#
# 为什么不排字形：这些字符是**图形**，不是文字。终端的规矩是它们撑满
# 整格（连成一条不断的线），而字体里的字形只画在 em box 内 ——
# JetBrains Mono 的 `│` 高度约 13px，行高却是 19.2px，于是**每行之间留
# 约 6px 的缝**。一根滚动条在截图里碎成十几段小竖线，看着像渲染坏了。
#
# 换成 CSS 矩形之后：几何完全由我们定，和行高严丝合缝，也不依赖任何
# 字体有没有这个字形。下面这几个是素材里实际用到的全部（统计过）。
GEOMETRY = {
    0x2500: ("hline", 1),  # ─ 分隔线（Markdown 的 --- 和表格横线）
    0x2502: ("vline", 1),  # │ 滚动条轨道
    0x2503: ("vline", 2),  # ┃ 滚动条滑块
    0x258C: ("half", 0),   # ▌ 未读标记（左半格实心块）
}
GEOMETRY_CHARS = frozenset(chr(cp) for cp in GEOMETRY)


def geometry_html(ch, x, y, cell, line_h, fg):
    """把一个几何符号画成绝对定位的矩形。返回 "" 表示"照常排字形"。"""
    spec = GEOMETRY.get(ord(ch[0])) if ch else None
    if not spec or not fg:
        return ""
    shape, weight = spec
    top, left = y * line_h, x * cell
    if shape == "vline":
        w = max(1.0, weight * cell / 7.8)  # 线宽按列折：1 列 ≈ 1px @13px
        return ('<i style="left:%.2fpx;top:%.2fpx;width:%.2fpx;height:%.2fpx;'
                'background:%s"></i>'
                % (left + (cell - w) / 2, top, w, line_h, fg))
    if shape == "hline":
        h = max(1.0, weight * cell / 7.8)
        return ('<i style="left:%.2fpx;top:%.2fpx;width:%.2fpx;height:%.2fpx;'
                'background:%s"></i>'
                % (left, top + (line_h - h) / 2, cell, h, fg))
    if shape == "half":
        return ('<i style="left:%.2fpx;top:%.2fpx;width:%.2fpx;height:%.2fpx;'
                'background:%s"></i>'
                % (left, top, cell / 2, line_h, fg))
    return ""


def to_html(grid, cols, cell, line_h):
    """网格 -> 一串**绝对定位**的元素：底色矩形 + 文字 run。

    为什么不用 `<pre>` 顺排（前几版是那么写的）：

    - 底色是"贴合内容"的（气泡只铺文字那么宽），它的宽度必须**只由列数
      决定**。顺排的话宽度由字体步进决定，字体一变宽度就变 —— 而
      README 里唯一要给人看的，恰恰就是"气泡贴合"这件事。
    - 文本 run 一旦顺排，前面所有格的步进误差会**累积**到后面去。
      CJK 的步进天生不等于 2 格（实测 13.0 vs 15.6），一行里几个汉字
      就能把后半行推歪几十像素：右边缘参差、滚动条碎成几段。
      绝对定位之后，每个 run 都从自己的格子坐标起步，**误差不传递**。

    底色矩形和文字是两套独立元素，谁也不影响谁的位置。底色按同底色
    连续段合并，文字按「同前景 + 同粗斜 + 同宽窄」合并 —— 宽窄必须同类，
    因为宽字符要**逐字**排（见下面 cls == 2 那段），窄字符整段排。
    """
    blocks = []  # 底色矩形，位置和宽度都由列数算出，与字体无关
    texts = []   # 文字 run，从自己的格子坐标起步
    for y, row in enumerate(grid):
        top = y * line_h
        x = 0
        while x < cols:
            # ---- 同底色的连续段 ----
            j = x + 1
            while j < cols and row[j].bg == row[x].bg:
                j += 1
            if row[x].bg is not None:
                color = css(row[x].bg)
                if color:
                    blocks.append(
                        '<i style="left:%.2fpx;top:%.2fpx;width:%.2fpx;height:%.2fpx;'
                        'background:%s"></i>'
                        % (x * cell, top, (j - x) * cell, line_h, color))
            # ---- 段内的文字 run ----
            k = x
            while k < j:
                # 空占位格（宽字符的第二格）不排文字：字形已经把两格占了。
                # 它也不能切断 run —— 中文里每个字后面都跟着一个这种格子，
                # 断了就退化成"一格一个 span"。
                if row[k].ch == "":
                    k += 1
                    continue
                # 几何符号（滚动条、分隔线、未读标记）画成 CSS 矩形，
                # 不排字形 —— 字体画不满整格，会碎成一段一段的。
                shape = geometry_html(row[k].ch, k, y, cell, line_h, css(row[k].fg))
                if shape:
                    texts.append(shape)
                    k += 1
                    continue
                cls = cell_class(row[k].ch)
                style = row[k].text_style()
                m = k + 1
                while m < j:
                    if row[m].ch == "":
                        m += 1
                        continue
                    if row[m].ch in GEOMETRY_CHARS or \
                            row[m].text_style() != style or cell_class(row[m].ch) != cls:
                        break
                    m += 1
                text = "".join(row[t].ch for t in range(k, m))
                bits = ["top:%.2fpx" % top]
                fg = css(row[k].fg)
                if fg:
                    bits.append("color:" + fg)
                if row[k].bold:
                    bits.append("font-weight:bold")
                if row[k].italic:
                    bits.append("font-style:italic")
                style = ";".join(bits)
                if cls == 2:
                    # 宽字符**逐字居中**在它占的那两格里，位置只由格子决定。
                    #
                    # 为什么不整段排 + letter-spacing 补宽度：letter-spacing
                    # 加在**每个字符后面**（包括最后一个），于是「回车」会被
                    # 排成「回 车 」—— 一个词看起来像两个字。而且补多少还得
                    # 先量出回退字体的步进（实测 13.0，应为 15.6）。
                    #
                    # 为什么居中而不是左对齐：左对齐时那 2.6px 的差全落在
                    # 字的右边，两个相邻汉字之间会空出 2.6px，看着还是散的；
                    # 居中之后两边各分 1.3px，才是终端里的观感。
                    for t in range(k, m):
                        if row[t].ch == "":
                            continue
                        texts.append(
                            '<span style="left:%.2fpx;width:%.2fpx;text-align:center;%s">%s</span>'
                            % (t * cell, 2 * cell, style, html.escape(row[t].ch)))
                else:
                    texts.append('<span style="left:%.2fpx;%s">%s</span>'
                                 % (k * cell, style, html.escape(text)))
                k = m
            x = j
    return "".join(blocks) + "".join(texts)


def build_html(inner, cols, rows, font_uri, font_family, font_fmt, cell, size, line_h):
    width = cols * cell
    height = rows * line_h
    face = ("@font-face { font-family: 'ChatShot'; src: url(%s) format('%s'); "
            "font-display: block; }" % (font_uri, font_fmt)) if font_uri else ""
    return """<!DOCTYPE html>
<html><head><meta charset="utf-8">
<style>
{face}
html, body {{ margin: 0; padding: 0; background: transparent; }}
.frame {{ padding: {margin}px; width: fit-content; }}
.win {{
  position: relative;
  background: {bg};
  border: 1px solid {border};
  border-radius: 10px;
  overflow: hidden;          /* 让底色块被圆角切一下，不然四角会露方角 */
  box-shadow: 0 12px 30px rgba(0,0,0,0.45);
  width: fit-content;
}}
/* 标题栏是一条**独立的横带**，内容从它下面开始。
   ⚠️ 以前三点是 position:absolute 直接压在内容上的 —— 内容第一行
   正好在那一带，于是被红黄绿三个球盖住半行（帮助页的「会话列表」
   就是那么没的）。图上只是「标题有点糊」，很容易当成字体问题。 */
.titlebar {{
  position: relative;
  height: {barh}px;
}}
.titlebar i {{
  position: absolute;
  top: {bardot}px;
  width: 11px; height: 11px; border-radius: 50%;
}}
.titlebar .r {{ left: 14px; background: #ff5f56; }}
.titlebar .y {{ left: 33px; background: #ffbd2e; }}
.titlebar .g {{ left: 52px; background: #27c93f; }}
/* 网格容器：几何完全由我们定，不让字体参与排版。 */
.grid {{
  position: relative;
  width: {w}px;
  height: {h}px;
  margin: 0 {padding}px {padding}px {padding}px;
  font-family: 'ChatShot', {font_family};
  font-size: {size}px;
  line-height: {lh}px;
  color: {fg};
  white-space: pre;
  font-variant-ligatures: none;
}}
.grid i {{ position: absolute; display: block; font-style: normal; }}
.grid span {{ position: absolute; white-space: pre; }}
</style></head>
<body><div class="frame"><div class="win">
<div class="titlebar"><i class="r"></i><i class="y"></i><i class="g"></i></div>
<div class="grid">{inner}</div>
</div></div></body></html>
""".format(face=face, font_uri=font_uri, font_family=font_family, margin=MARGIN,
           padding=PADDING, bg=BG, border=BORDER, size=size, lh=line_h,
           fg=FG_DEFAULT, w=round(width, 2), h=round(height, 2),
           barh=TITLEBAR_H, bardot=TITLEBAR_DOT_TOP, inner=inner)


# ---------------------------------------------------------------- 字体

FONT_MAGIC = [
    # (字节头, MIME, CSS format())
    #
    # 按**字节头**判，不按文件后缀、也不照抄 SVG 里写的 MIME —— 那份 SVG
    # 把一份裸 TTF 标成了 `application/x-font-woff2`，照抄就静默失败。
    (b"wOF2", "font/woff2", "woff2"),
    (b"wOFF", "font/woff", "woff"),
    (b"OTTO", "font/otf", "opentype"),
    (b"\x00\x01\x00\x00", "font/ttf", "truetype"),
    (b"true", "font/ttf", "truetype"),
    (b"ttcf", "font/collection", "collection"),
]


def font_data_uri(data):
    """把字体字节包成 data URI，并给出它**真实的** format()。

    认不出格式就返回 (None, None) —— 宁可明确失败，也不要再让浏览器
    静默退回通用字体：那种失败在图上只表现为「字形有点怪」，没人会去查。
    """
    for magic, mime, fmt in FONT_MAGIC:
        if data.startswith(magic):
            return "data:%s;base64,%s" % (mime, base64.b64encode(data).decode()), fmt
    return None, None


def cached_font(raw):
    """找已经抠好的字体缓存，返回 (路径, 字节)。没有就 (None, None)。

    **不做**后缀过滤：缓存可能是上一版脚本留下的，一律按字节头重新判格式。
    """
    if not os.path.isdir(raw):
        return None, None
    for name in sorted(os.listdir(raw)):
        if not name.startswith("_font.") or name.endswith(".json"):
            continue
        path = os.path.join(raw, name)
        if not os.path.isfile(path):
            continue
        with open(path, "rb") as fh:
            data = fh.read()
        if font_data_uri(data)[0]:
            return path, data
    return None, None


def ensure_font(root):
    """拿到一个可用的等宽字体，返回 (data-uri, 回退字体族, CSS format())。

    优先从 freeze 早先产出的 SVG 里抠出它内嵌的 JetBrains Mono —— 那是
    README 既有截图的字形，换掉的话新老图一眼就能看出不是同一套。
    抠不到就退回 Consolas：字形不同，但不会因为字体缺失整条链路挂掉。
    """
    raw = os.path.join(root, "docs", "images", "raw")

    path, data = cached_font(raw)
    if data is not None:
        uri, fmt = font_data_uri(data)
        print("字体：用缓存 %s（format=%s）" % (os.path.basename(path), fmt))
        return uri, "'JetBrains Mono'", fmt

    if os.path.isdir(raw):
        for name in sorted(os.listdir(raw)):
            if not name.endswith(".svg"):
                continue
            try:
                svg = open(os.path.join(raw, name), encoding="utf-8").read()
            except OSError:
                continue
            m = re.search(r"base64,([A-Za-z0-9+/=]+)\)", svg)
            if not m:
                continue
            data = base64.b64decode(m.group(1))
            if len(data) < 10000:  # 不像一份字体
                continue
            uri, fmt = font_data_uri(data)
            if not uri:
                print("字体：%s 里嵌的那份认不出格式，跳过" % name)
                continue
            # 后缀按**真实格式**取：那份 SVG 把 TTF 标成了 woff2，跟着它
            # 取名会把下一个人也带进沟里。
            ext = {"woff2": "woff2", "woff": "woff", "opentype": "otf"}.get(fmt, "ttf")
            with open(os.path.join(raw, "_font." + ext), "wb") as fh:
                fh.write(data)
            print("字体：从 %s 抠出 JetBrains Mono（%d 字节，format=%s）"
                  % (name, len(data), fmt))
            return uri, "'JetBrains Mono'", fmt

    print("字体：没找到内嵌的 JetBrains Mono，退回 Consolas")
    return "", "Consolas, 'DejaVu Sans Mono', monospace", None


# ---------------------------------------------------------------- 量字体

def measure_font(edge, tmp_dir, font_uri, font_family, font_fmt, size):
    """在浏览器里**量**这套字体的 ASCII 步进，返回 cell。量不准就直接停。

    为什么不直接用 `FONT_SIZE * 0.6`：

    - **ASCII 步进**：0.6em 是 JetBrains Mono 名义上的值。字体没加载上
      就完全不是这个数（实测退回比例字体时 `M`=10.56、`0`=7.22、空格
      =2.91），而底色矩形是按**列数**算宽度的 —— 步进错了，每一块底色
      的右沿都错，贴合的色块一眼就看出来。
    - **CJK 步进**：汉字是回退字体画的，实测 13.0px，而 2 格是 15.6px。
      每多一个汉字，这一行后面就左漂 2.6px，二十个汉字就是六个多格。

    这两件事只有浏览器自己知道，所以让浏览器自己量：造一个探针页，
    用 getBoundingClientRect 读宽度，`--dump-dom` 把结果带回来。顺带把
    「字体到底加载上没有」一起带回来 —— 那个静默回退的坑就靠它兜底。
    """
    probe = os.path.join(tmp_dir, "_probe.html")
    # format() 必须用**真实**的那个：写错就等于没写，浏览器静默退回。
    face = ("@font-face { font-family: 'ChatShot'; src: url(%s) format('%s'); "
            "font-display: block; }" % (font_uri, font_fmt)) if font_uri else ""
    page = """<!DOCTYPE html><html><head><meta charset="utf-8"><style>
%s
body { margin:0; font-size:%dpx; font-family: 'ChatShot', %s; }
span { display: inline-block; white-space: pre; }
</style></head><body>
<span id="ascii">%s</span><br>
<span id="cjk">%s</span><br>
<div id="out">PENDING</div>
<script>
function go(){
  var g=function(i){return document.getElementById(i).getBoundingClientRect().width;};
  var loaded=false;
  try { loaded=document.fonts.check('%dpx ChatShot'); } catch(e){}
  document.getElementById('out').textContent='METRICS loaded='+loaded
    +' cell='+(g('ascii')/40)+' cjk='+(g('cjk')/10);
}
if (document.fonts && document.fonts.ready) {
  document.fonts.ready.then(go); setTimeout(go, 400);
} else { go(); }
</script></body></html>""" % (face, size, font_family, "0" * 40, "\u7b49" * 10, size)
    with open(probe, "w", encoding="utf-8") as fh:
        fh.write(page)

    out = subprocess.run([
        edge, "--headless=new", "--disable-gpu", "--no-sandbox",
        "--virtual-time-budget=4000", "--dump-dom",
        "file:///" + probe.replace("\\", "/"),
    ], capture_output=True, text=True)
    m = re.search(r"METRICS loaded=(\w+) cell=([\d.]+) cjk=([\d.]+)", out.stdout)
    if not m:
        raise RuntimeError("量字体失败，探针没给出结果：\n%s\n%s"
                           % (out.stdout[:2000], out.stderr[:2000]))
    loaded = m.group(1) == "true"
    cell, cjk = float(m.group(2)), float(m.group(3))

    if not loaded:
        raise SystemExit(
            "字体没加载上 —— @font-face 被浏览器丢弃了（多半是 format() 和字节头对不上）。\n"
            "量出来的 ASCII 步进是 %.4fpx（%g em），这不是等宽字体的数，"
            "所有底色的右沿都会偏。\n宁可停在这里，也不要出一批字形悄悄变了的图。"
            % (cell, cell / size))

    ratio = cell / size
    if not (CELL_W_MIN_RATIO <= ratio <= CELL_W_MAX_RATIO):
        raise SystemExit("量出来的 ASCII 步进是 %.4fpx（%.3f em），不像等宽字体。\n"
                         "按这个数渲染，每一块底色的右沿都会偏。先查字体。"
                         % (cell, ratio))

    return cell, cjk


# ---------------------------------------------------------------- 渲染

def render(edge, html_path, png_path, cols, rows, cell, line_h):
    """HTML -> PNG。

    视口刻意给大再按 alpha 裁：窗口的实际尺寸受字体加载、阴影、亚像素
    影响，与其在 Python 里把宽度重新算一遍（算错了就多一条白边），不如
    让浏览器自己排，排完按像素裁。
    """
    w = int(cols * cell) + 2 * (MARGIN + PADDING) + 200
    h = int(rows * line_h) + 2 * (MARGIN + PADDING) + TITLEBAR_H + 200

    r = subprocess.run([
        edge, "--headless=new", "--disable-gpu", "--no-sandbox",
        "--hide-scrollbars",
        "--force-device-scale-factor=%d" % SCALE,
        "--default-background-color=00000000",
        "--virtual-time-budget=4000",  # 等内嵌字体解码完再截，否则会截到回退字形
        "--window-size=%d,%d" % (w, h),
        "--screenshot=" + png_path,
        "file:///" + html_path.replace("\\", "/"),
    ], capture_output=True, text=True)
    if not os.path.isfile(png_path):
        raise RuntimeError("Edge 截图失败：\n%s\n%s" % (r.stdout, r.stderr))

    crop_alpha(png_path, SCALE)
    return png_path


def crop_alpha(png_path, scale):
    """裁掉四周的透明像素（窗口圆角外的部分）。"""
    try:
        from PIL import Image
    except ImportError:
        print("  （没有 Pillow，跳过裁边）")
        return
    im = Image.open(png_path).convert("RGBA")
    bbox = im.getbbox()  # 非零 alpha 的包围盒
    if bbox is None:
        return
    pad = 4 * scale  # 阴影最外圈很淡，留一点余量别切得太紧
    x0, y0, x1, y1 = bbox
    x0 = max(0, x0 - pad)
    y0 = max(0, y0 - pad)
    x1 = min(im.size[0], x1 + pad)
    y1 = min(im.size[1], y1 + pad)
    im.crop((x0, y0, x1, y1)).save(png_path)


# ---------------------------------------------------------------- 验色模式

DUMP_MARK = {235: "1", 237: "2", 238: "3", 240: "4"}


def dump(grid, cols, name):
    """打印底色分布：`.` 是终端背景，1/2/3/4 是那几层底色。

    这是给「改完设计要确认底色真的铺在它该在的地方」用的。截图能看气质，
    但看不错一格的边界 —— 气泡到底铺到哪一列、列表栏有没有多铺一格，
    数格子比看图可靠。
    """
    print("=== %s (%d 列 x %d 行) ===" % (name, cols, len(grid)))
    for y, row in enumerate(grid):
        out = []
        for c in row:
            bg = c.bg
            if isinstance(bg, tuple):
                out.append("?")
            else:
                out.append(DUMP_MARK.get(bg, ".") if bg is not None else ".")
        print("%2d %s" % (y, "".join(out)))
    print()


# ---------------------------------------------------------------- main

def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    raw = os.path.join(root, "docs", "images", "raw")
    out = os.path.join(root, "docs", "images")
    mode_dump = "--dump" in sys.argv

    if not os.path.isdir(raw):
        sys.exit("找不到 %s —— 先跑 TestGenerateScreenshots 生成素材" % raw)

    names = sorted(n for n in os.listdir(raw) if n.endswith(".ansi"))
    if not names:
        sys.exit("%s 里没有 .ansi 文件" % raw)

    font_uri, font_family, font_fmt = "", "", None
    cell = CELL_W
    # 每个用到的字号各量一次步进。尺寸只有寥寥几种（BIGGER_FONT 里那几档），
    # 但**不能**按 13px 量完再等比缩放：步进未必线性（字体可能开 hinting），
    # 而底色矩形的宽度完全由步进决定 —— 差一点就是每一块底色都偏。
    metrics = {FONT_SIZE: (CELL_W, CELL_W * 2)}
    if not mode_dump:
        edge = find_edge()
        if not edge:
            sys.exit("找不到 Edge / Chrome，没法栅格化")
        font_uri, font_family, font_fmt = ensure_font(root)
        print("浏览器: %s" % edge)
        for size in sorted({FONT_SIZE} | set(BIGGER_FONT.values())):
            cell, cjk = measure_font(edge, raw, font_uri, font_family, font_fmt, size)
            metrics[size] = (cell, cjk)
            print("步进: %2dpx -> ASCII %.4fpx（%g em）· CJK %.4fpx（两格 %.4fpx）"
                  % (size, cell, cell / size, cjk, 2 * cell))
            if cjk > 2 * cell + 0.5:
                print("      提示: CJK 比它占的两格宽，汉字之间可能会挤在一起。")
        print()

    tmp = os.path.join(raw, "_tmp.html")
    for name in names:
        stem = name[:-5]
        with open(os.path.join(raw, name), encoding="utf-8") as fh:
            ansi = fh.read()

        cols = max(plain_width(ln) for ln in ansi.split("\n"))
        grid = parse_ansi(ansi, cols)

        if mode_dump:
            dump(grid, cols, stem)
            continue

        size = BIGGER_FONT.get(stem, FONT_SIZE)
        cell, _ = metrics[size]
        lh = line_height(size)

        inner = to_html(grid, cols, cell, lh)
        with open(tmp, "w", encoding="utf-8") as fh:
            fh.write(build_html(inner, cols, len(grid), font_uri, font_family,
                                font_fmt, cell, size, lh))

        png = os.path.join(out, stem + ".png")
        render(edge, tmp, png, cols, len(grid), cell, lh)
        tag = "" if size == FONT_SIZE else "  @%dpx" % size
        try:
            from PIL import Image
            with Image.open(png) as im:
                print("%-12s -> %s.png  (%dx%d)%s"
                      % (stem, stem, im.size[0], im.size[1], tag))
        except ImportError:
            print("%-12s -> %s.png%s" % (stem, stem, tag))

    if not mode_dump and os.path.isfile(tmp):
        os.remove(tmp)


if __name__ == "__main__":
    main()
