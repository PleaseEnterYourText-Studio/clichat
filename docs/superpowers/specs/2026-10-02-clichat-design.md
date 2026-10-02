# clichat 设计文档（v1）

- **日期**：2026-10-02
- **状态**：待审阅
- **范围**：v1（最简版）

---

## 0. 一句话

clichat 是一个跨平台终端程序：用你**现有的邮箱账号**收发邮件，但把邮件按会话线程呈现成 **IM 聊天流**。单二进制，Windows / macOS / Linux 通用。

**v1 不发明任何自定义邮件格式。** 会话边界直接用邮件原生的 `References` / `In-Reply-To`。任何邮件客户端发来的邮件都能进聊天流。

---

## 1. 目标与非目标

### v1 目标

- 单账号
- 纯文本消息收发
- 邮件线程 → 聊天流视图（单聊 + 群聊自动识别）
- 服务商预设，免手填服务器配置
- 单二进制跨三平台

### v1 非目标（推到 v2 或更后）

- ❌ 自定义 clichat 载荷 / 群 ID / 成员表
- ❌ 端到端加密
- ❌ OAuth2（因此**不支持 Outlook 个人版**）
- ❌ 附件
- ❌ 全文搜索
- ❌ 多账号
- ❌ HTML 邮件渲染与发送
- ❌ 联系人通讯录
- ❌ 已读回执、在线状态、「正在输入」

---

## 2. 技术选型

| 项 | 选择 | 理由 |
|---|---|---|
| 语言 | Go 1.26+ | 交叉编译单二进制零摩擦 |
| TUI | `charmbracelet/bubbletea` + `lipgloss` | Go 生态事实标准 |
| IMAP | `emersion/go-imap` | 见下方风险说明 |
| MIME | `emersion/go-message` | 处理编码与多字节 |
| SMTP | `net/smtp`（标准库） | 只发信，够用，零依赖 |
| 加密 | `golang.org/x/crypto`（argon2 + nacl/secretbox） | 凭据本地加密 |
| 配置格式 | JSON（标准库） | 零依赖 |
| 本地存储 | 单文件 header 索引 | 不引入数据库 |

**硬约束**：`CGO_ENABLED=0`，全链路纯 Go，保证交叉编译无障碍。

### ⚠️ IMAP 库的版本选择

- `emersion/go-imap` **v1**：成熟稳定、示例多、上手最快 → **v1 首选**
- `emersion/go-imap/v2`：支持 IMAP4rev2、API 更干净，但**官方 README 自述「still in development」** → 留作后续升级

实现时先用 v1。若遇到 v1 无法覆盖的需求（例如更好的 IDLE 支持），再评估 v2。

---

## 3. 架构

### 模块

| 模块 | 职责 | 依赖 |
|---|---|---|
| `config` | 服务商预设表、配置读写、凭据加解密 | 无 |
| `mail` | IMAP 拉取、SMTP 发送、MIME 解析。定义 `MailClient` interface | `config` |
| `thread` | 把散装邮件头部聚合成会话。**纯函数** | 无 |
| `store` | header 索引的持久化 | `mail` 的数据类型 |
| `tui` | Bubble Tea 渲染与交互 | 以上全部 |

### 依赖方向

```
tui  ──→  thread  ──→  (纯数据结构，无依赖)
 │
 ├──→  mail (interface MailClient)
 ├──→  store
 └──→  config
```

`thread` 不依赖 `mail` 的具体实现，只吃数据结构。`mail` 定义 interface，IMAP/SMTP 是真实实现，`fake.go` 是测试实现。

### 目录结构

```
clichat/
├── go.mod
├── main.go
├── internal/
│   ├── config/
│   │   ├── config.go       # 配置读写
│   │   ├── crypto.go       # Argon2id + secretbox
│   │   └── providers.go    # 服务商预设表
│   ├── mail/
│   │   ├── client.go       # MailClient interface
│   │   ├── types.go        # MessageHeader / MessageBody
│   │   ├── imap.go         # IMAP 实现
│   │   ├── smtp.go         # SMTP 实现
│   │   └── fake.go         # 测试用假实现
│   ├── thread/
│   │   ├── thread.go       # 聚合算法
│   │   └── thread_test.go
│   ├── store/
│   │   └── index.go        # header 索引缓存
│   └── tui/
│       ├── model.go
│       ├── update.go
│       ├── view.go
│       └── keys.go
└── docs/
    └── superpowers/specs/
```

---

## 4. 核心设计

### 4.1 线程聚合

**输入**

```go
type MessageHeader struct {
    MessageID  string
    References []string
    InReplyTo  string
    From       string
    To         []string
    Cc         []string
    Subject    string
    Date       time.Time
    UID        uint32
    Folder     string
}
```

**输出**

```go
type Thread struct {
    Root         string          // 根 Message-ID（可能不存在于本地索引）
    MessageIDs   []string        // 按 Date 升序
    Participants map[string]bool // 出现过的所有地址（小写规范化）
    Subject      string
    LastDate     time.Time
}
```

**算法（并查集 union-find）**

1. 为每条消息建立 `MessageID → 消息` 的映射
2. 初始化并查集，每条消息一个集合
3. 对每条消息 `m`：
   - 若 `len(m.References) > 0` → `parent = m.References[0]`
   - 否则若 `m.InReplyTo != ""` → `parent = m.InReplyTo`
   - 否则 → 自己是根，跳过
   - 若 `parent != ""` → `union(m.MessageID, parent)`
4. 按并查集根分组；根 ID 即 `Thread.Root`

**关键点**

- `parent` **不需要**存在于本地索引中。两个都引用同一个未拉取根的消息，依然会被正确合并——这正是冷启动能工作的原因。
- 循环引用天然安全：并查集不做遍历，不会死循环。
- 缺 `Message-ID` 的消息：用 `sha1(Folder + UID + Date + From + Subject)` 合成，前缀 `synthetic-`。

**明确不做**：Subject 兜底匹配。理由：它会把「同标题的两场不同讨论」错误合并。**宁可有断链，不要错并。**

### 4.2 冷启动两阶段

**问题**：首次运行时本地索引为空。若边拉边聚合，`References` 指向的消息还没拉到，历史邮件会全部断成独立会话。

**流程**

1. **阶段 1 · 建索引**——只拉头部，不拉正文：
   ```
   FETCH (UID FLAGS ENVELOPE
          BODY.PEEK[HEADER.FIELDS (Message-ID References In-Reply-To
                                   From To Cc Subject Date)])
   ```
   头部拉取比正文快一个数量级。

2. **阶段 2 · 聚合**——在**完整**索引上跑并查集（4.1）。

3. **阶段 3 · 按需拉正文**——只在用户点开会话时 `FETCH BODY[]` 拉那几封的正文。

**范围限制**：首次启动默认只拉**最近 90 天或 500 封**（先到者为准），可配。避免老邮箱一次拉几万封。

### 4.3 群聊识别

- `Thread.Participants` = 该会话所有消息的 `From` + `To` + `Cc` 地址去重（小写规范化）
- 参与者数 **≥ 3（含自己）** → 群聊，列表项显示 `carol, dave` 形式的摘要
- 参与者数 **== 2** → 单聊，列表项显示对方地址
- **不做显式群定义**。群是会话的属性，不是用户创建的对象。
- 注意一个预期行为：一次 CC 了第三人的 1:1 对话会被算作群聊。这是规则的正常结果，不做特例。

### 4.4 发送

**回复现有会话**

- `To` = `Thread.Participants` 去掉自己（默认 Reply-All，符合聊天语义）
- `In-Reply-To` = 会话中 Date 最大的消息的 Message-ID
- `References` = 会话所有 Message-ID 按 Date 升序
- `Subject` = `Re: ` + 根消息 Subject（若原 Subject 已以 `Re:` 开头则不重复加）
- 按键 `Tab` 把 `To` 切成「仅发件人」（只发给最后一条消息的 `From`）

**新建会话**

- 用户按 `n` 输入邮箱地址
- `To` = 该地址；无 `In-Reply-To` / `References`
- `Subject` = 消息正文第一行截断到 60 字符

**发送后**

- **乐观本地插入**：立刻在会话里显示这条消息，状态标记「发送中」
- 轮询到 Sent 文件夹里的副本后，按 `Message-ID` 匹配，替换掉乐观条目
- 发送失败 → 条目标红，保留在输入区可重发

**关于 Sent 文件夹**：v1 **不主动 IMAP APPEND**，依赖服务商自动保存已发送邮件（QQ / 163 / Gmail 预期都会）。若实测发现某服务商不保存，再补 APPEND 逻辑（需配合 `Message-ID` 去重）。

### 4.5 轮询与增量

- 间隔 **30 秒**（可配）
- 每次轮询：
  1. `SELECT INBOX`，记录 `UIDVALIDITY`
  2. `UID SEARCH UID <lastUID+1>:*`
  3. 对命中的 UID 执行 `FETCH (UID FLAGS ENVELOPE BODY.PEEK[HEADER.FIELDS (Message-ID References In-Reply-To From To Cc Subject Date)])`
  4. 对 `Sent` 文件夹重复 1–3
  5. 增量并入索引，然后对**全体**重跑并查集聚合（5000 条量级下开销可忽略，且避免增量 union-find 的边界 bug）
- **必须用 `BODY.PEEK`，不能用 `BODY`**——后者会顺手把邮件标记为已读
- `UIDVALIDITY` 变化 → 服务器重置了 UID 空间 → 丢弃索引，全量重拉
- 已读状态：打开会话时 `UID STORE +FLAGS \Seen`；列表里的未读数来自 `\Seen` 标志

### 4.6 本地存储

**不引入数据库。** 两个文件：

| 文件 | 内容 |
|---|---|
| `index.json` | header 索引：所有已知邮件的头部 + 每文件夹的 `lastUID` / `UIDVALIDITY` |
| `credentials.enc` | 加密后的凭据 |

索引在内存里以 map 结构持有，退出时序列化。500~5000 条头部的量级，JSON 完全够用。

若日后量级变大（> 5 万条），再换 bbolt。v1 不做。

---

## 5. 配置与凭据

### 文件位置

`os.UserConfigDir()` 下：

- macOS：`~/Library/Application Support/clichat/`
- Windows：`%AppData%\clichat\`
- Linux：`~/.config/clichat/`

### config.json

```json
{
  "version": 1,
  "account": {
    "provider": "qq",
    "email": "me@qq.com",
    "display_name": "me"
  },
  "imap": { "host": "imap.qq.com", "port": 993, "tls": "ssl" },
  "smtp": { "host": "smtp.qq.com", "port": 465, "tls": "ssl" },
  "sync": {
    "poll_interval_seconds": 30,
    "initial_days": 90,
    "initial_max_messages": 500,
    "folders": ["INBOX", "Sent"]
  }
}
```

**不存任何密码。**

### credentials.enc

二进制格式：

```
[magic "CLIC" 4B][version 1B][salt 16B][nonce 24B][secretbox ciphertext]
```

- 密钥派生：`argon2.IDKey(masterPassword, salt, time=3, memory=64*1024, threads=4, keyLen=32)`
- 加密：`secretbox.Seal`（XSalsa20-Poly1305）
- 明文内容：`{"password": "<授权码或应用专用密码>"}`

> Argon2id 参数取的是常见起步值，实现时会按本机性能实测调整（目标：解密耗时 200–500ms）。

**已知摩擦**：每次启动要输主密码。v1 提供「本次会话记住」（只存内存）。系统钥匙串（macOS Keychain / Windows Credential Manager / Linux Secret Service）留作 v2。

### 服务商预设

| 预设 ID | 名称 | IMAP | SMTP | 密码栏填 | 引导文案要点 |
|---|---|---|---|---|---|
| `qq` | QQ 邮箱 | imap.qq.com:993 SSL | smtp.qq.com:465 SSL | 授权码 | 设置 → 账户 → 开启 IMAP/SMTP → 按提示发短信获取授权码 |
| `163` | 163 邮箱 | imap.163.com:993 SSL | smtp.163.com:465 SSL | 授权码 | 设置 → POP3/SMTP/IMAP → 开启服务 → 获取授权码 |
| `126` | 126 邮箱 | imap.126.com:993 SSL | smtp.126.com:465 SSL | 授权码 | 同 163 |
| `gmail` | Gmail | imap.gmail.com:993 SSL | smtp.gmail.com:465 SSL | 应用专用密码 | 先开两步验证 → 账号安全 → 应用专用密码 → 生成 16 位 |
| `custom` | 自定义 | 手填 | 手填 | — | — |

> 以上地址来源：常见邮箱服务器配置汇总（更新于 2025-10-22）。实现时会再逐条实测连通性。

**不在列表里**

- **Outlook / Outlook.com 个人版**——微软 2024-09-16 起停用 IMAP/SMTP 基本认证，填密码连不上，必须 OAuth2（v2 再说）
- **iCloud**——服务器地址**尚未核实**，暂不提供预设

每个预设**必须**带引导文案。这是「免配置」承诺能否兑现的关键——用户最容易卡在「去哪开服务、授权码怎么生成」这一步。

---

## 6. TUI

### 布局：两栏

```
┌─ 会话 ────────┬─ bob@x.com ──────────────┐
│ ● bob         │ bob  14:02               │
│   今天讨论下… │ 今天讨论下协议格式       │
│               │                          │
│   carol, dave │               me  14:05  │
│   我倾向用…   │              我倾向用 JSON│
├───────────────┴──────────────────────────┤
│ > 输入消息…                              │
└──────────────────────────────────────────┘
```

**宽度 < 80 列时自动降级为单栏**：整屏只显示当前会话，`Esc` 返回列表。

### 配色

- 自己的消息：右对齐，蓝色
- 他人的消息：左对齐，绿色；同一会话内不同发件人用不同颜色
- 时间戳：暗色
- 未读会话：列表项前加实心圆点

### 按键

| 键 | 功能 |
|---|---|
| `↑` `↓` `j` `k` | 导航 |
| `Enter` | 打开会话 / 发送消息 |
| `Esc` | 返回会话列表 |
| `Tab` | 切换 To 列表（Reply-All ↔ 仅发件人） |
| `r` | 回复当前会话 |
| `n` | 新建会话 |
| `PgUp` `PgDn` | 滚动消息 |
| `c` | 打开配置 |
| `q` / `Ctrl+C` | 退出 |

### 状态栏（底部一行）

```
● 在线 · 上次同步 14:02:31 · me@qq.com
```

连接状态 / 上次同步时间 / 当前账号。

---

## 7. 错误处理

| 情况 | 行为 |
|---|---|
| 网络断开 | 状态栏显示「离线」，指数退避重试 1s→2s→4s→…→封顶 60s |
| 认证失败 | 弹回配置界面，**不静默重试**（避免账号被锁） |
| SMTP 发送失败 | 消息标红，保留在输入区，可重发。**绝不假装发出去了** |
| IMAP 连接被掐断 | 自动重连，从上次 UID 继续 |
| `UIDVALIDITY` 变化 | 丢弃索引，全量重拉 |
| 单封邮件解析失败 | 跳过该封、记日志，不崩 |
| 主密码错误 | 提示重输，不写文件 |

日志写到配置目录下的 `clichat.log`。**只记事件，不记邮件正文。**

---

## 8. 测试策略

**`thread` 层**——纯函数单测，必须覆盖：

- 正常 References 链
- 断裂的 References（指向本地不存在的消息）
- 无 References、只有 In-Reply-To
- 两者都没有
- 循环引用
- 缺 Message-ID
- 冷启动全量聚合（一批乱序输入，输出稳定）
- **同 Subject 的不同线程不得被合并**

**`mail` 层**——interface + `fake.go`，测「拉取 → 聚合 → 渲染」全链路。

**自动化测试不碰真实邮箱。**

**`--mock` 模式**：用本地 fixture 目录跑完整 TUI，便于手工验收与演示。

**手动验收清单**：QQ 与 163 各真机跑一遍收发、群聊识别、断网重连。

---

## 9. 跨平台构建

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o clichat.exe
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o clichat-mac
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o clichat-linux
```

goreleaser + GitHub Actions **暂不做**（已定缓做）。

---

## 10. v2 路线（clichat 协议）

v1 的邮件格式天然是 v2 的超集。v2 的增量只有两处：

1. 发信时多塞一个 MIME part（结构化载荷）
2. 收信时多解析一个 part

`thread` 与 `tui` 层基本不动。

v2 要加：

- 自定义 MIME part 承载 JSON 载荷（群 ID、成员表、最近 K 条历史、`history_root`）
- 噪音过滤：只有带载荷的会话 + 用户手动标记的会话进聊天流
- 端到端加密
- OAuth2（解锁 Gmail / Outlook）
- 附件、全文搜索、多账号

**v2 待决问题**（已提出，NoWint 未定）：

1. 历史嵌入策略（推荐滑动窗口 K=100）
2. 「只回给我」的消息是否自动并入群历史（推荐否）
3. 收件箱标签页（推荐只读版）
4. 群成员变更（推荐 v1 只加不减）
5. 载荷是否含成员邮箱地址（推荐含）

---

## 11. 未决与风险

| # | 风险 | 影响 | 处理 |
|---|---|---|---|
| 1 | `go-imap` v2 官方自述仍在开发中 | 库选型 | 先用 v1；v2 留作升级 |
| 2 | 服务商是否自动保存已发送邮件到 Sent **未实测** | 自己发的消息可能丢失 | 实现时先实测 QQ/163；若不保存则补 IMAP APPEND |
| 3 | iCloud 服务器地址未核实 | 少一个预设 | 需要时再查 |
| 4 | 部分客户端不写 References | 会话断裂 | 已知限制，v1 接受 |
| 5 | 中文/多字节 Subject 的 MIME 编码 | 乱码 | 由 `go-message` 处理，需实测 |
| 6 | 主密码每次输入 = 摩擦 | 体验 | v1 提供「记住本次会话」；钥匙串留 v2 |
| 7 | 老邮箱首次聚合性能 | 启动慢 | 限制首拉范围；必要时改 gob 或 bbolt |
| 8 | 终端 Unicode 宽度（中文占 2 列） | 对齐错乱 | 用 `mattn/go-runewidth`（bubbletea 已依赖） |
