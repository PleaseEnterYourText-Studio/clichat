# clichat

### 在你已经有的邮箱上，用终端聊天

[English](README.md) | **中文**

[![Release](https://img.shields.io/github/v/release/PleaseEnterYourText-Studio/clichat?include_prereleases&sort=semver&label=release&color=blue)](https://github.com/PleaseEnterYourText-Studio/clichat/releases)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey)](https://github.com/PleaseEnterYourText-Studio/clichat/releases)
[![Downloads](https://img.shields.io/github/downloads/PleaseEnterYourText-Studio/clichat/total?color=green)](https://github.com/PleaseEnterYourText-Studio/clichat/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/PleaseEnterYourText-Studio/clichat?color=00ADD8&logo=go)](go.mod)

[下载](#下载) · [快速开始](#快速开始) · [功能](#功能) · [快捷键](#快捷键) · [常见问题](#常见问题)

---

## 为什么做这个

邮箱是这个世界上通用性最好的开放消息系统 —— 谁都有，而且**对方不需要装任何东西**
就能收到你的消息。这一点是任何即时通讯软件都做不到的。

但邮件客户端一直把它当成**公文**来处理：主题、称呼、正文、落款、引用历史、
「尊敬的 XXX」。一件两小时内来回四条的小事，最后长得像一叠待办文件。

clichat 换个看法：**把邮件当消息。**

- **按会话读，不按邮件读。** 回复关系直接用邮件本来就带的 `References` /
  `In-Reply-To` 头部聚合，不做主题猜测 —— 把「同标题的两场不同讨论」错误合并，
  比留下断链更糟。
- **一个二进制，没有服务端。** IMAP 和 SMTP 从你的机器直连你的邮箱服务商。
  没有 clichat 账号，没有中转服务器，中间不经过任何第三方。
- **凭据只属于你。** 授权码用你自己设的主密码加密后存在本地，除了你自己的
  邮件服务器，不会被发往任何地方。
- **不发明任何自定义邮件格式。** v1 在协议层没有加任何私货 —— 所以它发出去的
  每一封信，在任何别的客户端里都读得懂；别人从别处发来的信，在 clichat 里
  也能正确成会话。

## 截图

**会话视图** —— 群聊里每个发言人一种颜色，自己的消息靠右：

[![会话视图](docs/images/01-chat.png)](docs/images/01-chat.png)

**会话列表** —— 标题行显示未读数，`●` 是未读、`★` 是星标：

[![会话列表](docs/images/02-list.png)](docs/images/02-list.png)

**搜索** —— 实时过滤，匹配参与者、主题和显示名。窄终端会自动降级成单栏：

[![搜索](docs/images/03-search.png)](docs/images/03-search.png)

**帮助页** —— 按 `?` 打开完整快捷键表：

[![帮助](docs/images/04-help.png)](docs/images/04-help.png)

## 下载

到 [Releases](https://github.com/PleaseEnterYourText-Studio/clichat/releases) 页面下载。
**每次提交到 main 都会自动发一个新的测试版**，所以列表里会有很多条，挑最新那个即可。

| 平台 | 文件 |
|---|---|
| Windows (x64) | `clichat-windows-amd64.exe` |
| Windows (ARM64) | `clichat-windows-arm64.exe` |
| macOS（Apple 芯片） | `clichat-darwin-arm64` |
| macOS（Intel） | `clichat-darwin-amd64` |
| Linux (x64) | `clichat-linux-amd64` |
| Linux (ARM64) | `clichat-linux-arm64` |

**macOS** —— 二进制没有做代码签名，Gatekeeper 会拦。先去掉隔离标记：

```bash
chmod +x ./clichat-darwin-arm64
xattr -d com.apple.quarantine ./clichat-darwin-arm64
./clichat-darwin-arm64
```

**Linux** —— 先加可执行权限：

```bash
chmod +x ./clichat-linux-amd64
./clichat-linux-amd64
```

**Windows** —— 未签名，SmartScreen 可能提示，点「更多信息」→「仍要运行」。

每个 Release 都带一个 `SHA256SUMS.txt`，校验：

```bash
sha256sum -c SHA256SUMS.txt
```

## 快速开始

```bash
clichat
```

首次启动会走一个四步向导：

1. **选服务商** —— QQ 邮箱 / 163 / 126 / Gmail，或「自定义」（自己填服务器）
2. **邮箱地址**
3. **授权码** —— ⚠️ **不是登录密码**。几乎所有服务商都要求用应用专用密码或授权码
   才能走 IMAP/SMTP，向导里会告诉你所选服务商的授权码去哪拿。
4. **主密码** —— 至少 4 位。它用来加密授权码，永远不会被发送到任何地方。

之后**每次启动都要输这个主密码**。忘了就只能重新配置账号 —— 在主密码界面按
`Ctrl+R` 可以重来。

### 命令行开关

| 开关 | 作用 |
|---|---|
| `-check` | 只读连接自检：连一次邮箱，报出断在哪一步。用的是已保存的配置，除了主密码不会再问你任何东西。 |
| `-check-deep` | 同上，外加实测一次首拉耗时。明显更慢，隐含 `-check`。 |
| `-version` | 打印版本号后退出。这个版本号同时也是发给服务端的 IMAP `ID`。 |
| `-config-dir` | 打印配置目录后退出。 |

脚本里用环境变量 `CLICHAT_MASTER` 传主密码 —— `-check` 会优先读它，读不到才回退到
交互式输入。**刻意没有「密码」这个命令行开关**：命令行参数会进 shell 历史，也会出现
在进程列表里。

## 功能

**读**

- 按 `References` / `In-Reply-To` 聚合会话，不靠主题猜测
- 自动识别群聊；每个发言人一个**稳定**的颜色，同一个人在每次启动后都是同一个色
- HTML 邮件降级成纯文本，引用历史砍掉 —— 聊天视图里引用是冗余的
- 两栏布局，宽度低于 80 列自动降级成单栏

**写**

- 写新会话、回复、回复所有人（`Tab` 切换）、转发
- 星标（`*`）、标记未读（`u`）、删除（`d`）
- 删除是移到服务端的**已删除**文件夹，可恢复，而且会先弹确认框
- 复制最后一条消息正文到系统剪贴板（`y`）
- 搜索参与者、主题、显示名，边打字边过滤
- 切换文件夹（列表里按 `Tab`）—— 文件夹列表来自服务端 IMAP `LIST`，
  所以不管你用哪家邮箱、它把文件夹叫什么，都能对上

**让它一直能用**

- 冷启动只拉最近 90 天 / 500 封（先到者为准）
- 按 UID 增量同步；断线重连从上次的 UID 续上
- 检测到 `UIDVALIDITY` 变化就重建本地索引
- 单封邮件解析失败只跳过那一封并记日志，不崩
- 只要服务商 IMAP 声明了 `ID` 扩展就能用（见「常见问题」）

**实现上**

- 纯 Go，`CGO_ENABLED=0`，每个平台一个静态二进制
- 凭据加密用 Argon2id + NaCl secretbox
- 本地索引就是 JSON，没有数据库
- 落盘的东西全在一个目录里，删掉即重置

## 快捷键

任何界面按 `?` 打开内置帮助页。列表底部常驻显示最常用的几个键。

**会话列表**

| 键 | 作用 |
|---|---|
| `↑` / `k`、`↓` / `j` | 上下移动 |
| `g` / `Home`、`G` / `End` | 跳到首个 / 末个 |
| `[` / `]` | 上一个 / 下一个未读（走到头绕回另一端） |
| `Enter` | 打开会话 |
| `R` | 打开，并且只回发件人 |
| `n` | 新建会话 |
| `/` | 搜索 |
| `Tab` | 切换文件夹 |
| `r` | 立即同步一次 |
| `u` | 标记为未读 |
| `*` | 星标 / 取消星标 |
| `d` | 删除（移到「已删除」） |
| `f` | 转发最后一条消息 |
| `y` | 复制最后一条消息的正文 |
| `?` | 帮助 |
| `q` / `Ctrl+C` | 退出 |

**会话内**

这里输入框是有焦点的，单个字母会被当成正文打进去 —— 所以动作键一律用 `Ctrl` 组合。

| 键 | 作用 |
|---|---|
| `Enter` | 发送 |
| `Tab` | 发给所有人 / 只回发件人 |
| `PgUp` / `PgDn` | 上下翻页 |
| `Esc` | 返回列表 |
| `Ctrl+Y` | 复制最后一条消息的正文 |
| `Ctrl+U` | 标记未读并返回列表 |
| `Ctrl+T` | 星标 / 取消星标 |
| `Ctrl+D` | 删除 |
| `F1` | 帮助 |
| `Ctrl+C` | 退出 |

## 常见问题

**哪些邮箱开箱即用？**

QQ 邮箱、163、126、Gmail 都有预设 —— 自动填好服务器和端口，并告诉你授权码去哪拿。
其它的用「自定义」手填即可。

**为什么预设里没有 Outlook / Outlook.com 个人版？**

微软从 2024-09-16 起停用了个人版账号的 IMAP/SMTP 基本认证。填密码连不上，
必须走 OAuth2，而 v1 不做 OAuth2。**这是刻意不给，不是忘了。**
iCloud 同理排除 —— 它的服务器地址没有核实过，给一个可能错的预设比不给更糟。

**163 / 126 报 `Unsafe Login. Please contact kefu@188.com`**

网易系要求客户端登录后先发一条 IMAP `ID` 命令表明身份（RFC 2971），
否则后续命令一律被拒。这个错很坑：**`LOGIN` 是成功的**，挂在紧接着的 `EXAMINE` 上。
clichat 从 v0.1.0-beta.9 起会发这条 ID。如果你用的是更早的版本，升级即可。

**连不上，怎么知道断在哪一步？**

```bash
clichat -check
```

它会用你已经保存的配置连一次邮箱，然后按步骤报结果：① 登录并选中 INBOX、
② 拉一段邮件头部、③ 探测「已发送」文件夹。**停在哪一步，就说明问题在哪一层** ——
是授权码不对，还是服务商限流，还是端口到不了。

自检是**只读**的：不读正文、不打印主题和发件人、也不会把邮件标成已读。所以它的输出
可以直接贴到 issue 里。

加 `-check-deep` 会额外实测一次首拉耗时。首拉几百条头部要几十秒，而那段时间界面上
只显示「同步中…」，很容易被误当成卡死 —— 这个数就是用来判断「慢」还是「真挂了」。

**每次都要输主密码，能不能记住？**

v1 不能。存到系统钥匙串在路线图上。

**我的数据存在哪？**

| 平台 | 目录 |
|---|---|
| Windows | `%AppData%\clichat` |
| macOS | `~/Library/Application Support/clichat` |
| Linux | `~/.config/clichat` |

| 文件 | 内容 | 是否加密 |
|---|---|---|
| `config.json` | 账号、服务器、同步策略 | 否 |
| `credentials.enc` | 授权码 | **是**，主密码加密 |
| `index.json` | 邮件头索引 | 否 |
| `clichat.log` | 运行日志 —— 只记事件，不记正文 | 否 |

整个目录删掉就是彻底重置。

**搜索搜不到很早以前的邮件。**

搜索只覆盖**本地已同步**的部分 —— 默认最近 90 天或 500 封，不会去查服务端。
需要更多的话，调 `config.json` 里的 `sync.initial_days` / `sync.initial_max_messages`。

**按 `d` 是真的把邮件删了吗？**

不是。它把邮件**移到服务端的「已删除」文件夹**，你还能从网页版找回来。
服务端支持 IMAP `MOVE` 扩展时用它；否则退化成 `COPY` + 打 `\Deleted` + `EXPUNGE`。

**支持附件吗？**

v1 不支持。解析时会忽略附件，只显示文本部分。

**支持多账号吗？**

v1 不支持。

**出问题了，怎么拿到细节？**

日志在上面那个目录里的 `clichat.log`。如果是连接层的问题，启动前设
`CLICHAT_IMAP_DEBUG=1`，完整的 IMAP 协议对话会写进同一个文件 ——
**其中包含邮件头部和正文**，排查完记得关掉。

## 开发

需要 Go 1.26.2 或更新。

```bash
git clone git@github.com:PleaseEnterYourText-Studio/clichat.git
cd clichat

go vet ./...
go test -race ./...
go build -o clichat .
```

分层（依赖方向自上而下）：

| 包 | 职责 |
|---|---|
| `internal/thread` | 纯函数聚合算法（并查集）。无 IO，可完全单测 |
| `internal/mail` | IMAP 拉取 / SMTP 发送 / MIME 解析，藏在 `Client` 接口后面 |
| `internal/store` | 邮件头索引，JSON 持久化 |
| `internal/config` | 配置、服务商预设、Argon2id + secretbox 凭据加密 |
| `internal/logging` | 运行日志 |
| `internal/app` | 编排层，把上面几块拼起来 |
| `internal/tui` | Bubble Tea 界面 |

自动化测试**永远不碰真实邮箱**，`mail.Fake` 提供内存实现。
`internal/tui/screenshot_test.go` 负责重新生成本文里的截图，用法见该文件顶部的注释。

## 贡献

欢迎提 issue 和 PR。开 PR 之前：

- `go vet ./...` 和 `go test -race ./...` 要通过
- 保持现有的注释风格：**说清为什么**，尤其是那些看着能简化、但其实不能的地方

注意：推到 `main` 会自动发一个 Release，所以请在分支上工作。

## 许可证

尚未指定。在此之前，默认保留所有权利。如果你希望别人能用，加一个 `LICENSE`
文件即可改变这一点。
