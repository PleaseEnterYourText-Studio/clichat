#!/usr/bin/env python3
"""把 docs/images/raw/*.ansi 渲染成 README 用的终端截图 PNG。

    python tools/render-screenshots.py

素材（.ansi）从哪来见 internal/tui/screenshot_test.go 顶部的注释。本脚本
只负责「ANSI -> 好看的 PNG」这一段。

## 为什么是这么条链路

终端截图要好看，关键在两件事：**字体**和**渲染器**。自己写一个光栅化器
不是不行（这仓库早先就是那么干的），但拿不到 JetBrains Mono 这种字体 ——
它的下载地址在国内不可达，自己画也画不出连字和正确的字形。

所以改用 [charmbracelet/freeze](https://github.com/charmbracelet/freeze)：
它把 ANSI 渲染成 SVG，并且**把字体以 base64 内嵌进 SVG**，自带
JetBrains Mono，不依赖系统装了什么字体。

    1. freeze   ANSI -> SVG      （自带字体、窗口边框、阴影）
    2. Edge     SVG  -> PNG      （无头模式栅格化）

## 为什么第 2 步要绕一圈

freeze 的 **PNG 输出在 Windows 上是坏的** —— 对任何输入都直接段错误
（`unexpected fault address`），试过用 Go 1.24 重建也一样，所以不是
工具链版本的问题。SVG 输出完全正常。好在浏览器就是最好的 SVG 光栅化器，
用系统自带的 Edge 无头模式转一道即可。

## 两个踩过的坑（都实测过，别重查）

**一、素材必须是 256 色，16 色会丢背景色。**

freeze 认 `48;5;N`（→ `<rect fill=.../>`）和 `48;2;r;g;b`，但**完全不画
`\x1b[40m` 这类 16 色背景**。而截图测试若跟着环境自动判色深，在无 TTY 的
测试进程里会判成 16 色，于是「对方消息的灰底」被压成 `\x1b[40m`，截出来
一片空白 —— README 反倒成了唯一看不见那个功能的地方。

修法在 `internal/tui/screenshot_test.go`：显式
`lipgloss.SetColorProfile(termenv.ANSI256)`，不靠环境判。所以本脚本这边
不需要任何处理，但**素材换了生成方式时要先确认 `.ansi` 里是 `48;5;` 而不是
`40m`**，否则会静默拍出一张没有底色的图。

**二、freeze 的列宽取整会让底色右沿在同列文字上差 1 列。**

实测：两行 ANSI 逐字节同构（都是 `48;5;236m` + 前置空格 + 纯 CJK + 空格 +
`49m`），freeze 给其中一行的底色矩形 `x=322.57`、另一行 `x=314.83`，差
7.74px ≈ 一个列宽。这是 freeze 自己的取整，**不是我们输出的问题** ——
真终端里底色是按单元格上的，不存在这个现象。

所以：**别拿截图 PNG 去量右侧对齐**。要判「底色有没有铺满」在 Go 层判
（`internal/tui/peerrow_test.go`），它量的是 SGR 序列的实际列数，那才是
终端里真正生效的东西。

## 依赖

- `freeze`：`go install github.com/charmbracelet/freeze@latest`
  （装到 `$GOPATH/bin`；如果它崩，试 `GOTOOLCHAIN=go1.24.6` 重建，
  虽然本项目实测两者都崩在 PNG 那步、SVG 那步都好）
- Edge 或 Chrome：Windows 自带 Edge，路径见下面 EDGE_CANDIDATES
"""

import os
import re
import subprocess
import sys

# ---- 外观 ----
#
# 配色跟着 Catppuccin Mocha 走：背景 base #1e1e2e，边框 surface0 #313244。
# 注意这里**只**决定窗口底色和边框；应用发出的 256 色号（如 38;5;42）
# 由 freeze 按 xterm 固定色立方照实渲染，没有被美化过。
BG = "#1e1e2e"
BORDER = "#313244"
FONT_SIZE = 13
LINE_HEIGHT = 1.4

EDGE_CANDIDATES = [
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
]


def find_edge():
    for p in EDGE_CANDIDATES:
        if os.path.isfile(p):
            return p
    return None


def find_freeze():
    """找 freeze 可执行文件。"""
    for name in ("freeze.exe", "freeze"):
        # PATH 上
        for d in os.environ.get("PATH", "").split(os.pathsep):
            cand = os.path.join(d, name)
            if os.path.isfile(cand):
                return cand
    # GOPATH/bin 兜底（go install 的默认落点）
    gopath = os.environ.get("GOPATH") or os.path.join(os.path.expanduser("~"), "go")
    for sub in ("bin", "bin-freeze-old"):
        cand = os.path.join(gopath, sub, "freeze.exe")
        if os.path.isfile(cand):
            return cand
    return None


def svg_size(svg_text):
    """读出 SVG 的自然尺寸，栅格化时要按它设视口。"""
    m = re.search(r'<svg width="([0-9.]+)" height="([0-9.]+)"', svg_text)
    if not m:
        raise RuntimeError("SVG 里没找到 width/height")
    return int(float(m.group(1)) + 0.999), int(float(m.group(2)) + 0.999)


def render(freeze, edge, ansi_path, svg_path, png_path):
    with open(ansi_path, "r", encoding="utf-8") as fh:
        ansi = fh.read()

    cmd = [
        freeze, "-o", svg_path,
        "--window",
        "--border.radius", "10",
        "--border.width", "1",
        "--border.color", BORDER,
        "--shadow.blur", "30",
        "--shadow.x", "0",
        "--shadow.y", "12",
        "--padding", "20",
        "--margin", "24",
        "--background", BG,
        "--font.size", str(FONT_SIZE),
        "--line-height", str(LINE_HEIGHT),
    ]
    r = subprocess.run(cmd, input=ansi, text=True, capture_output=True)
    if r.returncode != 0 or not os.path.isfile(svg_path):
        raise RuntimeError("freeze 失败：\n%s\n%s" % (r.stdout, r.stderr))

    svg = open(svg_path, encoding="utf-8").read()
    w, h = svg_size(svg)

    # 无头浏览器截图的视口要和 SVG 一样大，否则会留白。
    r = subprocess.run([
        edge, "--headless=new", "--disable-gpu", "--no-sandbox",
        "--hide-scrollbars",
        "--default-background-color=00000000",  # 窗口外的边距保持透明
        "--window-size=%d,%d" % (w, h),
        "--screenshot=" + png_path,
        "file:///" + svg_path.replace("\\", "/"),
    ], capture_output=True, text=True)
    if not os.path.isfile(png_path):
        raise RuntimeError("Edge 栅格化失败：\n%s\n%s" % (r.stdout, r.stderr))

    return w, h


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    raw = os.path.join(root, "docs", "images", "raw")
    out = os.path.join(root, "docs", "images")

    if not os.path.isdir(raw):
        sys.exit("找不到 %s —— 先跑 TestGenerateScreenshots 生成素材" % raw)

    freeze = find_freeze()
    if not freeze:
        sys.exit("找不到 freeze。装一个：go install github.com/charmbracelet/freeze@latest")
    edge = find_edge()
    if not edge:
        sys.exit("找不到 Edge / Chrome，没法把 SVG 栅格化成 PNG")

    print("freeze: %s" % freeze)
    print("浏览器: %s\n" % edge)

    names = sorted(n for n in os.listdir(raw) if n.endswith(".ansi"))
    if not names:
        sys.exit("%s 里没有 .ansi 文件" % raw)

    for name in names:
        stem = name[:-5]
        w, h = render(
            freeze, edge,
            os.path.join(raw, name),
            os.path.join(raw, stem + ".svg"),
            os.path.join(out, stem + ".png"),
            
        )
        print("%-12s -> %s.png  (%dx%d)" % (stem, stem, w, h))


if __name__ == "__main__":
    main()
