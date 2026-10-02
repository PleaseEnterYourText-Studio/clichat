#!/usr/bin/env python3
"""把 docs/images/raw/*.ansi 渲染成 README 用的终端截图 PNG。

用法（需要 Pillow）：

    python tools/ansi2png.py

素材从哪来见 internal/tui/screenshot_test.go 顶部的注释。

为什么不用 vhs / asciinema 那一套：它们要么依赖 ttyd（Windows 上装不动），
要么只能录 GIF。这里要的只是静态图，自己解析 SGR 反而最可控 ——
字体、留白、窗口边框都能精确调，也不引入任何外部二进制。

配色是 Catppuccin Mocha（https://github.com/catppuccin/catppuccin）。

关于「颜色是不是被美化过」：**没有**。应用发出的都是 256 色号（如 38;5;42），
其中 16-231 号是 xterm 固定的 6×6×6 色立方，任何终端渲染出来都一样，
所以这里照实映射。主题只决定背景、默认前景和 0-15 号色 —— 这几项本来就
是用户自己的终端主题说了算。
"""

import os
import re
import sys
import unicodedata

from PIL import Image, ImageDraw, ImageFont

# ---- 渲染倍率 ----
#
# 2 倍超采样：按两倍尺寸画，再缩回一半。字形的抗锯齿和细线（分隔线、
# 边框）会明显更干净，缩到 README 的显示宽度时尤其明显。
SCALE = 2

# ---- Catppuccin Mocha ----
BASE = (0x1E, 0x1E, 0x2E)      # 终端背景
MANTLE = (0x18, 0x18, 0x25)    # 标题栏
CRUST = (0x11, 0x11, 0x1B)     # 窗口描边
TEXT = (0xCD, 0xD6, 0xF4)      # 默认前景
OVERLAY1 = (0x7F, 0x84, 0x9C)  # 标题文字
SURFACE0 = (0x31, 0x32, 0x44)  # 标题栏分隔线
SURFACE1 = (0x45, 0x47, 0x5A)

# 三个窗口按钮。用 Catppuccin 自己的红/黄/绿，比经典红绿灯更协调。
DOTS = [(0xF3, 0x8B, 0xA8), (0xF9, 0xE2, 0xAF), (0xA6, 0xE3, 0xA1)]

# Catppuccin 的 0-15 号 ANSI 色。应用目前只用 16 号以上的色，
# 但万一以后用了基础色，这里也应当是主题该有的样子。
ANSI16 = [
    (0x45, 0x47, 0x5A), (0xF3, 0x8B, 0xA8), (0xA6, 0xE3, 0xA1), (0xF9, 0xE2, 0xAF),
    (0x89, 0xB4, 0xFA), (0xF5, 0xC2, 0xE7), (0x94, 0xE2, 0xD5), (0xBA, 0xC2, 0xDE),
    (0x58, 0x5B, 0x70), (0xF3, 0x8B, 0xA8), (0xA6, 0xE3, 0xA1), (0xF9, 0xE2, 0xAF),
    (0x89, 0xB4, 0xFA), (0xF5, 0xC2, 0xE7), (0x94, 0xE2, 0xD5), (0xA6, 0xAD, 0xC8),
]

# ---- 字号与尺寸（都是 1 倍下的值，实际会乘 SCALE）----

FONT_SIZE = 15
CELL_H = 20
PAD = 18            # 终端内容四周留白
CHROME_H = 34       # 标题栏高度
RADIUS = 11
OUTER = 22          # 窗口外的透明边距

FONTS_DIR = r"C:\Windows\Fonts"
# ASCII 走等宽字体。系统里没有 Cascadia / JetBrains Mono，Consolas 是
# 最接近的一档，也是终端里最常见的默认。
FONT_ASCII = os.path.join(FONTS_DIR, "consola.ttf")
FONT_ASCII_BOLD = os.path.join(FONTS_DIR, "consolab.ttf")
# 中文走 Noto Sans SC：比微软雅黑现代得多，和 Catppuccin 的调子也合。
# 这是个可变字体，PIL 会取默认的 Regular 实例。
FONT_CJK = os.path.join(FONTS_DIR, "NotoSansSC-VF.ttf")
FONT_CJK_BOLD = os.path.join(FONTS_DIR, "NotoSansSC-VF.ttf")

# 每个 .ansi 对应的窗口标题。
TITLES = {
    "01-chat": "clichat — 会话视图",
    "02-list": "clichat — 会话列表",
    "03-search": "clichat — 搜索",
    "04-help": "clichat — 帮助（按 ? 打开）",
}


def xterm256(n):
    """把 xterm 256 色号转成 RGB。"""
    if n < 16:
        return ANSI16[n]
    if n < 232:
        n -= 16
        r, g, b = n // 36, (n % 36) // 6, n % 6
        step = lambda v: 0 if v == 0 else 55 + v * 40
        return (step(r), step(g), step(b))
    v = 8 + (n - 232) * 10
    return (v, v, v)


def dim(color, factor=0.62):
    """把颜色往背景方向压暗，用来实现 SGR 的 faint(2)。

    系数不能压太狠：应用把「主题」「时间戳」这些次要信息都设成了 faint，
    压到一半会糊成一片灰，看不清内容。
    """
    return tuple(int(BASE[i] + (color[i] - BASE[i]) * factor) for i in range(3))


def char_width(ch):
    """一个字符占几列。

    必须和 Go 侧 go-runewidth 的默认行为对齐：只有东亚「宽」和「全角」
    算两列，ambiguous（●、★、↑ 这类）算一列。

    这一条是硬要求 —— 用「字符个数」当列宽的话，中文行会被算窄一半，
    整个窗口宽度跟着算错，右边直接被裁掉。
    """
    if unicodedata.combining(ch):
        return 0
    return 2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1


def is_ascii(ch):
    return 0x20 <= ord(ch) <= 0x7E


# 匹配所有 CSI 序列；只有以 m 结尾的是颜色/样式，其余（清行、光标等）直接丢掉。
CSI = re.compile(r"\x1b\[([0-9;?]*)([A-Za-z])")


def parse_line(line):
    """把一行 ANSI 文本拆成 [(字符, 前景, 背景, 粗体, 列宽)]。

    列宽按显示宽度算，中文是 2。
    """
    cells = []
    fg, bg = TEXT, None
    bold = reverse = faint = False

    i = 0
    while i < len(line):
        ch = line[i]

        if ch == "\x1b":
            m = CSI.match(line, i)
            if m:
                params, kind = m.group(1), m.group(2)
                if kind == "m":
                    codes = [int(p) for p in params.split(";") if p != ""] or [0]
                    j = 0
                    while j < len(codes):
                        c = codes[j]
                        if c == 0:
                            fg, bg, bold, reverse, faint = TEXT, None, False, False, False
                        elif c == 1:
                            bold = True
                        elif c == 2:
                            faint = True
                        elif c == 22:
                            bold = faint = False
                        elif c == 7:
                            reverse = True
                        elif c == 27:
                            reverse = False
                        elif c == 39:
                            fg = TEXT
                        elif c == 49:
                            bg = None
                        elif c == 38 and j + 2 < len(codes) and codes[j + 1] == 5:
                            fg = xterm256(codes[j + 2])
                            j += 2
                        elif c == 48 and j + 2 < len(codes) and codes[j + 1] == 5:
                            bg = xterm256(codes[j + 2])
                            j += 2
                        elif c == 38 and j + 4 < len(codes) and codes[j + 1] == 2:
                            fg = tuple(codes[j + 2:j + 5])
                            j += 4
                        elif c == 48 and j + 4 < len(codes) and codes[j + 1] == 2:
                            bg = tuple(codes[j + 2:j + 5])
                            j += 4
                        j += 1
                i = m.end()
                continue
            i += 1
            continue

        # 反白就是把前景背景对调。lipgloss 的选中行用的就是这个。
        f, b = (bg or BASE, fg) if reverse else (fg, bg)
        if faint:
            f = dim(f)
        cells.append((ch, f, b, bold, char_width(ch)))
        i += 1

    return cells


def render(ansi_path, png_path, title):
    with open(ansi_path, "r", encoding="utf-8") as fh:
        text = fh.read()

    lines = text.split("\n")
    parsed = [parse_line(ln) for ln in lines]
    # 按 **显示宽度** 取最宽的一行，不是字符个数。
    cols = max((sum(c[4] for c in p) for p in parsed), default=0)

    fs = FONT_SIZE * SCALE
    cell_h = CELL_H * SCALE
    pad = PAD * SCALE
    chrome_h = CHROME_H * SCALE
    radius = RADIUS * SCALE
    outer = OUTER * SCALE

    f_ascii = ImageFont.truetype(FONT_ASCII, fs)
    f_ascii_b = ImageFont.truetype(FONT_ASCII_BOLD, fs)
    f_cjk = ImageFont.truetype(FONT_CJK, fs)
    f_cjk_b = ImageFont.truetype(FONT_CJK_BOLD, fs)

    cell_w = f_ascii.getlength("M")
    term_w = int(round(cell_w * cols))
    term_h = cell_h * len(lines)

    win_w = term_w + pad * 2
    win_h = term_h + pad * 2 + chrome_h
    img_w = win_w + outer * 2
    img_h = win_h + outer * 2

    img = Image.new("RGBA", (img_w, img_h), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # 窗口外的柔和阴影：叠几层半透明的圆角矩形，越远越淡。
    for k in range(14, 0, -1):
        alpha = int(10 * (1 - k / 15.0) ** 1.5) + 2
        d.rounded_rectangle(
            [outer - k, outer - k + 3 * SCALE, outer + win_w + k, outer + win_h + k + 3 * SCALE],
            radius=radius + k,
            fill=(0, 0, 0, alpha),
        )

    x0, y0 = outer, outer
    d.rounded_rectangle([x0, y0, x0 + win_w, y0 + win_h], radius=radius, fill=BASE)

    # 标题栏：上面两角圆、下面两角方，所以先画圆角块再补一块矩形。
    d.rounded_rectangle(
        [x0, y0, x0 + win_w, y0 + chrome_h + radius],
        radius=radius, fill=MANTLE,
    )
    d.rectangle([x0, y0 + chrome_h - 1, x0 + win_w, y0 + chrome_h + 1], fill=MANTLE)
    # 标题栏下沿一条细线，把窗口和内容分开。
    d.rectangle([x0, y0 + chrome_h, x0 + win_w, y0 + chrome_h + 1], fill=SURFACE0)

    for i, color in enumerate(DOTS):
        cx = x0 + int(18 * SCALE) + i * int(19 * SCALE)
        cy = y0 + chrome_h // 2
        r = int(6.5 * SCALE)
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=color)

    f_title = ImageFont.truetype(FONT_CJK, int(12.5 * SCALE))
    d.text((x0 + win_w // 2, y0 + chrome_h // 2), title,
           font=f_title, fill=OVERLAY1, anchor="mm")

    # 终端内容
    tx, ty = x0 + pad, y0 + pad + chrome_h
    for row, cells in enumerate(parsed):
        cy = ty + row * cell_h
        col = 0
        for ch, fg, bg, bold, width in cells:
            w = cell_w * width
            if bg is not None:
                d.rectangle([tx + col, cy, tx + col + w, cy + cell_h], fill=bg)
            if ch != " ":
                if is_ascii(ch):
                    # ASCII 走等宽字体，左对齐到格子上，行才齐。
                    font = f_ascii_b if bold else f_ascii
                    d.text((tx + col, cy + cell_h / 2), ch,
                           font=font, fill=fg, anchor="lm")
                else:
                    # 非 ASCII（中文、●、★、↑…）一律走中文字体：
                    # Consolas 里没有这些字形，交给它会渲染成豆腐块。
                    # 居中到自己的格子里 —— 这类字符宽度和格子对不齐。
                    font = f_cjk_b if bold else f_cjk
                    d.text((tx + col + w / 2, cy + cell_h / 2), ch,
                           font=font, fill=fg, anchor="mm")
            col += w

    # 缩回 1 倍，抗锯齿更干净。
    img = img.resize((img_w // SCALE, img_h // SCALE), Image.LANCZOS)
    img.convert("RGB").save(png_path, "PNG", optimize=True)
    return img.width, img.height


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    raw = os.path.join(root, "docs", "images", "raw")
    out = os.path.join(root, "docs", "images")

    if not os.path.isdir(raw):
        sys.exit("找不到 %s —— 先跑 TestGenerateScreenshots 生成素材" % raw)

    names = sorted(n for n in os.listdir(raw) if n.endswith(".ansi"))
    if not names:
        sys.exit("%s 里没有 .ansi 文件" % raw)

    for name in names:
        stem = name[:-5]
        ansi_path = os.path.join(raw, name)
        png_path = os.path.join(out, stem + ".png")
        w, h = render(ansi_path, png_path, TITLES.get(stem, stem))
        print("%-12s -> %s  (%dx%d)" % (stem, os.path.basename(png_path), w, h))


if __name__ == "__main__":
    main()
