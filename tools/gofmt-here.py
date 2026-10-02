"""按每个文件**原本的行尾**跑 gofmt。

为什么需要它：这个仓库工作区里行尾是混合的（有的文件 CRLF、有的 LF，
`core.autocrlf=true` 只在 checkout 那一刻转换，之后新建/改写的文件不受
影响）。直接 `gofmt -w` 会把 CRLF 文件整个重写成 LF —— diff 里整个文件
都变了，真正的改动淹没在里面。这个脚本先记下原风格，格式化完再换回去。

用法：python tools/gofmt-here.py [路径...]（默认 internal/tui/*.go）
"""

import glob
import os
import subprocess
import sys


def main():
    targets = sys.argv[1:] or glob.glob("internal/tui/*.go")
    changed = []
    for path in sorted(targets):
        with open(path, "rb") as fh:
            raw = fh.read()
        crlf = b"\r\n" in raw
        text = raw.replace(b"\r\n", b"\n")
        out = subprocess.run(["gofmt"], input=text, capture_output=True)
        if out.returncode != 0:
            sys.exit("gofmt 报错 %s：%s" % (path, out.stderr.decode("utf-8", "replace")))
        new = out.stdout
        if new == text:
            continue
        if crlf:
            new = new.replace(b"\n", b"\r\n")
        with open(path, "wb") as fh:
            fh.write(new)
        changed.append("%s (%s)" % (os.path.basename(path), "CRLF" if crlf else "LF"))

    if changed:
        print("已格式化：")
        for c in changed:
            print("  " + c)
    else:
        print("全部已合规")


if __name__ == "__main__":
    main()
