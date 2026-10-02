"""变异验证：把源码里的一小段换掉，看对应判据会不会变红。

    python tools/mutate.py <文件> <旧文本> <新文本> <测试名正则>

目的是回答「这条判据到底有没有牙齿」—— 一条永远绿的判据和没有判据一样贵，
而且更危险：它让人以为自己被保护着。做法的详细理由见
`mutation-verify-tests` skill。

## 两条实现上的注意

1. **不碰 git**。还原是把原文（读进内存的那份）写回去，所以未提交的改动
   不会丢。用 `git checkout` 还原会把同一文件里别的改动一起清掉。

2. **`-run` 一条都没匹配到时要报错**。这是最容易骗过自己的地方：模式写错
   （名字少一段、正则打错）时测试进程返回 0，看起来就是"判据没抓到"。
   实际是"根本没有判据在跑"。所以输出里必须出现 `--- PASS` 或 `--- FAIL`。

用法示例：把输入卡左内缩去掉，确认布局判据会红

    python tools/mutate.py internal/tui/view.go \\
        'pad := strings.Repeat(" ", paneInset)' 'pad := ""' \\
        TestInputCapsule_LivesInsideTheChatPane
"""

import os
import re
import subprocess
import sys

PKG = "./internal/tui/"


def find_go():
    """找到 go 可执行文件。

    优先 PATH；找不到再退回几个常见安装位置（本机是 sdk 里的非标准路径）。
    写死一个绝对路径的话，换台机器这个脚本就直接不可用了。
    """
    for name in ("go", "go.exe"):
        try:
            subprocess.run([name, "version"], capture_output=True, check=True)
            return name
        except (OSError, subprocess.CalledProcessError):
            pass
    for root in (os.path.expanduser("~/sdk"), r"C:\Go", "/usr/local/go"):
        if not os.path.isdir(root):
            continue
        for entry in sorted(os.listdir(root), reverse=True):
            cand = os.path.join(root, entry, "bin", "go")
            if os.path.isfile(cand):
                return cand
    sys.exit("找不到 go 可执行文件 —— 把它加进 PATH 再跑")


def main():
    if len(sys.argv) != 5:
        sys.exit(__doc__)
    path, old, new, pattern = sys.argv[1:]

    with open(path, encoding="utf-8", newline="") as fh:
        src = fh.read()
    n = src.count(old)
    if n != 1:
        sys.exit("!! 旧文本在 %s 里出现了 %d 次，拒绝变异（要恰好 1 次）" % (path, n))

    go = find_go()
    with open(path, "w", encoding="utf-8", newline="") as fh:
        fh.write(src.replace(old, new))
    try:
        r = subprocess.run([go, "test", PKG, "-run", pattern, "-count=1", "-v"],
                           capture_output=True, text=True)
        out = r.stdout + r.stderr
        if "--- PASS" not in out and "--- FAIL" not in out:
            sys.exit("!! 模式 %r 一条测试也没匹配到 —— 这次变异没有意义" % pattern)
        verdict = "红（判据有牙齿）" if r.returncode != 0 else "绿（判据没抓到！）"
        print("变异 [%s] -> %s" % (old.strip()[:48], verdict))
        for line in out.splitlines():
            if re.search(r"FAIL|^--- |^\s+layout_test", line):
                print("    " + line.strip())
    finally:
        with open(path, "w", encoding="utf-8", newline="") as fh:
            fh.write(src)
        print("   已还原 " + path)


if __name__ == "__main__":
    main()
