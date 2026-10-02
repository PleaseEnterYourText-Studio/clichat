package mail

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"
	"github.com/emersion/go-imap/backend/memory"
	imapclient "github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/server"
)

// 这个文件里的东西只为一件事服务：**数网络往返次数**。
//
// 为什么不能只数方法调用：一次 liveClient.Body(folder, uid) 在协议上是
// **三条**命令 —— ensureLocked 的 NOOP、selectLocked 的 SELECT、以及
// 真正取正文的 UID FETCH（见 live.go / imap.go）。用假 Client 数方法
// 调用只数到三分之一，而"慢"恰恰是这三分之三乘上消息条数。
//
// 所以这里起一个**进程内的真服务端**（go-imap 自带的 server + memory
// 后端），在它前面挂一层计数代理，数客户端实际发出的命令。数出来的
// 是协议层的事实，不是对代码的推断。
//
// 用明文而不是 TLS：假服务端跑在回环上，没有证书可用，而生产那条
// dialEndpointTLS 要求 TLS。这就是 live.go 里 dialEndpoint 这个变量
// 存在的全部理由 —— 测试把它换成明文拨号（见 mailServer.client）。
type mailServer struct {
	front *net.Listener
	srv   *server.Server
	user  backend.User

	mu     sync.Mutex
	counts map[string]int

	// pairs 是在途的（前端，后端）连接对。
	//
	// 留着它是为了能在测试里模拟「服务商单方面把连接掐了」—— 126 真的会
	// 这么干（见 live.go 的 ensureLocked），而那正是重连那条路上最容易
	// 悄悄坏掉的地方：它平时不跑，坏了也没人发现，直到用户在空闲之后
	// 第一次操作时看到一句「同步失败」。
	pairs [][2]net.Conn
}

// startMailServer 起一个进程内 IMAP 服务端，自带 INBOX 和 Sent。
func startMailServer(t *testing.T) *mailServer {
	t.Helper()

	bkd := memory.New()
	// memory.New 预置的用户名/密码是 username/password，字段未导出、改不了，
	// 测试就用这一对登录。
	user, err := bkd.Login(nil, "username", "password")
	if err != nil {
		t.Fatalf("登录假后端失败: %v", err)
	}
	if err := user.CreateMailbox("Sent"); err != nil {
		t.Fatalf("建 Sent 失败: %v", err)
	}

	srv := server.New(bkd)
	srv.AllowInsecureAuth = true

	back, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听后端失败: %v", err)
	}
	go srv.Serve(back)

	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听前端失败: %v", err)
	}

	ms := &mailServer{
		front:  &front,
		srv:    srv,
		user:   user,
		counts: map[string]int{},
	}
	go ms.acceptLoop(back)

	t.Cleanup(func() {
		srv.Close()
		front.Close()
	})
	return ms
}

// acceptLoop 每来一条客户端连接，就在它和后端之间接一层代理。
func (ms *mailServer) acceptLoop(back net.Listener) {
	for {
		cl, err := (*ms.front).Accept()
		if err != nil {
			return
		}
		be, err := net.Dial("tcp", back.Addr().String())
		if err != nil {
			cl.Close()
			continue
		}
		go ms.pipe(cl, be)
	}
}

// pipe 把两条连接对接起来，并数**客户端发往服务端**那一侧的命令。
func (ms *mailServer) pipe(cl, be net.Conn) {
	defer cl.Close()
	defer be.Close()

	ms.addPair(cl, be)
	defer ms.removePair(cl)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(be, &commandReader{r: cl, ms: ms})
	}()
	_, _ = io.Copy(cl, be)
	wg.Wait()
}

func (ms *mailServer) addPair(cl, be net.Conn) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.pairs = append(ms.pairs, [2]net.Conn{cl, be})
}

func (ms *mailServer) removePair(cl net.Conn) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	kept := ms.pairs[:0]
	for _, p := range ms.pairs {
		if p[0] != cl {
			kept = append(kept, p)
		}
	}
	ms.pairs = kept
}

// dropAll 掐断所有在途连接，模拟服务商单方面断连。
//
// 两边都关：只关一边的话，另一边的 io.Copy 未必立刻返回，测试会
// 间歇性地量到「连接还在」—— 那就成了一条会随机变绿的判据。
func (ms *mailServer) dropAll() {
	ms.mu.Lock()
	pairs := append([][2]net.Conn(nil), ms.pairs...)
	ms.pairs = nil
	ms.mu.Unlock()

	for _, p := range pairs {
		p[0].Close()
		p[1].Close()
	}
}

// commandReader 按 IMAP 的行语法识别命令。
//
// 判据用协议本身，不靠猜：客户端发出的命令行第一个 token 是它自己编的
// 流水号，第二个 token 就是命令名；而服务端的应答行第二个 token 永远是
// OK / NO / BAD，未标记行以 `*` 开头，续行以 `+` 开头。
type commandReader struct {
	r   io.Reader
	ms  *mailServer
	buf []byte
}

func (cr *commandReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 {
		cr.buf = append(cr.buf, p[:n]...)
		cr.drain()
	}
	return n, err
}

func (cr *commandReader) drain() {
	for {
		i := bytes.Index(cr.buf, []byte("\r\n"))
		if i < 0 {
			return
		}
		line := string(cr.buf[:i])
		cr.buf = cr.buf[i+2:]
		cr.ms.record(line)
	}
}

func (ms *mailServer) record(line string) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "+") {
		return
	}
	f := strings.Fields(line)
	if len(f) < 2 || f[1] != strings.ToUpper(f[1]) {
		return
	}
	key := f[1]
	// UID FETCH 和 UID STORE 是两回事，分开数。
	if key == "UID" && len(f) > 2 {
		key = "UID " + f[2]
	}
	ms.counts[key]++
}

// reset 把计数清零，好让一段被测行为独占一张计数表。
func (ms *mailServer) reset() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.counts = map[string]int{}
}

// count 返回某条命令发出的次数。
func (ms *mailServer) count(name string) int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.counts[name]
}

// total 返回发出去的命令总条数。
func (ms *mailServer) total() int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	n := 0
	for _, c := range ms.counts {
		n += c
	}
	return n
}

func (ms *mailServer) dump() string {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	var b strings.Builder
	for k, v := range ms.counts {
		fmt.Fprintf(&b, "%s=%d ", k, v)
	}
	return strings.TrimSpace(b.String())
}

// add 往某个文件夹里塞一封原始邮件。
func (ms *mailServer) add(t *testing.T, mailbox, raw string, seen bool) {
	t.Helper()

	mbox, err := ms.user.GetMailbox(mailbox)
	if err != nil {
		t.Fatalf("取 %s 失败: %v", mailbox, err)
	}
	var flags []string
	if seen {
		flags = append(flags, imap.SeenFlag)
	}
	if err := mbox.CreateMessage(flags, time.Now(), bytes.NewReader([]byte(raw))); err != nil {
		t.Fatalf("往 %s 存信失败: %v", mailbox, err)
	}
}

// client 返回一个指向这个假服务端的真实 liveClient。
func (ms *mailServer) client(t *testing.T) *liveClient {
	t.Helper()

	prev := dialEndpoint
	dialEndpoint = func(config.Endpoint) (*imapclient.Client, error) {
		c, err := net.Dial("tcp", (*ms.front).Addr().String())
		if err != nil {
			return nil, err
		}
		return imapclient.New(c)
	}
	t.Cleanup(func() { dialEndpoint = prev })

	return &liveClient{
		cfg:  &config.Config{IMAP: config.Endpoint{Host: "127.0.0.1", Port: 1}},
		user: "username",
		pass: "password",
	}
}

// rawMail 拼一封最小的纯文本邮件。
//
// references 非空时写进 References 头 —— 会话聚合靠它串成一条线。
func rawMail(subject, from, messageID string, references ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: me@clichat.local\r\n")
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s>\r\n", messageID)
	if len(references) > 0 {
		fmt.Fprintf(&b, "References: %s\r\n", strings.Join(references, " "))
	}
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "%s 的正文。", subject)
	return b.String()
}

// uidsOf 取出若干头部的 UID。
func uidsOf(headers []Header) []uint32 {
	out := make([]uint32, 0, len(headers))
	for _, h := range headers {
		out = append(out, h.UID)
	}
	return out
}

// TestBodies_CostsOneFetchRegardlessOfMessageCount 是这一轮的核心判据：
// 拉 N 封正文必须只发**一条** UID FETCH。
//
// 改之前这里是 3N 条 —— 每封一条 NOOP + SELECT + UID FETCH。实测 21 封
// 是 63 条命令。真实邮箱（126，索引 310 条 header / 64 个会话）里最大的
// 会话有 19 封（用真实聚合逻辑量的），逐封的话打开它要 57 次往返。
// 这条判据钉住的就是那个 O(消息数)：只要有人把批量拆回逐封，它立刻变红。
func TestBodies_CostsOneFetchRegardlessOfMessageCount(t *testing.T) {
	ms := startMailServer(t)

	const n = 20
	for i := 0; i < n; i++ {
		ms.add(t, "INBOX", rawMail(fmt.Sprintf("第%d封", i), "alice@example.com",
			fmt.Sprintf("msg-%d@example.com", i)), i%2 == 0)
	}

	c := ms.client(t)
	headers, err := c.Headers("INBOX", 1, 0)
	if err != nil {
		t.Fatalf("拉头部失败: %v", err)
	}
	// memory 后端预置了一封。
	if len(headers) != n+1 {
		t.Fatalf("头部条数 = %d，想要 %d", len(headers), n+1)
	}

	ms.reset()
	got, err := c.Bodies("INBOX", uidsOf(headers))
	if err != nil {
		t.Fatalf("批量拉正文失败: %v", err)
	}

	t.Logf("%d 封正文 → 命令 %d 条：%s", len(got), ms.total(), ms.dump())

	if len(got) != len(headers) {
		t.Fatalf("拉到 %d 封，请求了 %d 封", len(got), len(headers))
	}
	if n := ms.count("UID FETCH"); n != 1 {
		t.Errorf("UID FETCH 发了 %d 条，要 1 条 —— 批量被拆回逐封了", n)
	}
	if n := ms.count("EXAMINE"); n > 1 {
		t.Errorf("EXAMINE 发了 %d 条，最多 1 条", n)
	}
	if n := ms.total(); n > 3 {
		t.Errorf("命令总数 %d，最多 3（NOOP + EXAMINE + UID FETCH）: %s", n, ms.dump())
	}

	// 批量不等于串行：内容也得各归各的。
	seeded := 0
	for _, h := range headers {
		body := got[h.UID].Body
		if body == "" {
			t.Fatalf("uid=%d（%q）的正文是空的", h.UID, h.Subject)
		}
		if !strings.HasPrefix(h.Subject, "第") {
			continue
		}
		seeded++
		if want := h.Subject + " 的正文。"; body != want {
			t.Errorf("uid=%d 的正文 = %q，想要 %q", h.UID, body, want)
		}
	}
	if seeded != n {
		t.Errorf("按主题认出来的邮件只有 %d 封，想要 %d —— 头部没带上 Subject？", seeded, n)
	}
}

// TestBodies_MissingUIDIsNotFatal：批量里混进一个服务端上不存在的 UID，
// 其余的照常返回 —— 一封坏邮件不该让整批作废。
func TestBodies_MissingUIDIsNotFatal(t *testing.T) {
	ms := startMailServer(t)
	ms.add(t, "INBOX", rawMail("只有一封", "alice@example.com", "only@example.com"), false)

	c := ms.client(t)
	headers, err := c.Headers("INBOX", 1, 0)
	if err != nil {
		t.Fatalf("拉头部失败: %v", err)
	}

	got, err := c.Bodies("INBOX", append(uidsOf(headers), 9999))
	if err != nil {
		t.Fatalf("混进一个不存在的 UID 就整批失败了: %v", err)
	}
	if len(got) != len(headers) {
		t.Errorf("拉到 %d 封，想要 %d 封", len(got), len(headers))
	}
	if _, ok := got[9999]; ok {
		t.Error("不存在的 UID 不该出现在结果里")
	}
}

// TestBodies_EmptyRequestSendsNothing：没有要拉的就不该发出任何命令。
func TestBodies_EmptyRequestSendsNothing(t *testing.T) {
	ms := startMailServer(t)

	c := ms.client(t)
	// 先握一次手，把 LOGIN / CAPABILITY / LIST 这些命令走完，再清零 ——
	// 这条判据量的是「空请求本身」的代价，不是首次连接的代价。
	if _, err := c.Folders(); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	ms.reset()

	got, err := c.Bodies("INBOX", nil)
	if err != nil {
		t.Fatalf("空请求报错了: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("空请求返回了 %d 条", len(got))
	}
	if n := ms.total(); n != 0 {
		t.Errorf("空请求发了 %d 条命令: %s", n, ms.dump())
	}
}

// legCost 是一段被测行为发出的命令总数与明细。
type legCost struct {
	total   int
	examine int
	noop    int
	dump    string
}

// openThreadCost 量「打开一个会话」这条路上发出去的全部命令。
//
// 路径取的是界面按下回车时**真的并发发出去**的那三条命令（见
// tui/model.go）：syncCmd → app.Sync（每个文件夹 Folder + Headers）、
// loadActiveBodiesCmd → app.Bodies（每个文件夹一次批量）、
// markReadCmd → app.MarkRead（每个文件夹一次 MarkSeen）。
//
// 这里刻意不经过 app / tui，直接在 mail 层把同样三条腿跑一遍 ——
// 协议代价全在这一层，而这一层可以脱离界面单独量。
func openThreadCost(t *testing.T, n int) legCost {
	t.Helper()

	ms := startMailServer(t)
	for i := 0; i < n; i++ {
		// seen=false：打开会话要真的去标已读，否则那条腿是空跑。
		ms.add(t, "INBOX", rawMail(fmt.Sprintf("第%d封", i), "alice@example.com",
			fmt.Sprintf("open-%d-%d@example.com", n, i)), false)
	}

	c := ms.client(t)
	// 先把连接和文件夹列表握好再清零 —— 量的是「打开会话」本身，
	// 不是首次连接的代价（LOGIN / CAPABILITY / LIST 只发生一次）。
	if _, err := c.Folders(); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	ms.reset()

	// 两个文件夹都要走一遍：真实配置同步的就是 INBOX + Sent，
	// 而「每个文件夹各自一遍」正是当前实现里最主要的固定开销。
	for _, folder := range []string{"INBOX", "Sent"} {
		if _, err := c.Folder(folder); err != nil {
			t.Fatalf("Folder(%s): %v", folder, err)
		}
		headers, err := c.Headers(folder, 1, 0)
		if err != nil {
			t.Fatalf("Headers(%s): %v", folder, err)
		}
		if _, err := c.Bodies(folder, uidsOf(headers)); err != nil {
			t.Fatalf("Bodies(%s): %v", folder, err)
		}
		if err := c.MarkSeen(folder, uidsOf(headers)); err != nil {
			t.Fatalf("MarkSeen(%s): %v", folder, err)
		}
	}

	return legCost{
		total:   ms.total(),
		examine: ms.count("EXAMINE"),
		noop:    ms.count("NOOP"),
		dump:    ms.dump(),
	}
}

// TestOpenThread_CostDoesNotGrowWithMessageCount 钉住一个比「命令数 ≤ K」
// 更硬的性质：**打开会话的命令数不随会话里邮件条数增长**。
//
// 写法是量两个规模差 10 倍的会话、断言总数**相等**，而不是钉一个魔法数字。
// 这样换后端、加一条探活、少一次 STATUS 都不会让它假红；而只要有谁把
// 某一段改回「逐封」，规模大的那次立刻变大、它立刻变红。
//
// 这条判据是这一轮的收尾尺子：上一条（TestBodies_...）只盯正文那一段，
// 而「打开会话」这条路上正文只占一部分 —— 剩下的是同步和标已读。
// 两个都量，才能知道下一次该改哪儿。
func TestOpenThread_CostDoesNotGrowWithMessageCount(t *testing.T) {
	small := openThreadCost(t, 3)
	big := openThreadCost(t, 30)

	t.Logf("会话 3 封 → 命令 %d 条：%s", small.total, small.dump)
	t.Logf("会话 30 封 → 命令 %d 条：%s", big.total, big.dump)

	if small.total != big.total {
		t.Errorf("打开会话的命令数随邮件条数变了：3 封 %d 条，30 封 %d 条\n  3 封:  %s\n  30 封: %s",
			small.total, big.total, small.dump, big.dump)
	}

	// 分开钉住那一轮优化的两个具体效果。理由不是「想看到数字变小」，
	// 而是这两件事各自会以不同的方式退化，而且退化后**功能照常能用**，
	// 只有命令数悄悄涨回去 —— 正是那种没人会注意到的回归。
	//
	//   EXAMINE：每个文件夹只允许选一次。复用的记忆一旦失效，它立刻
	//            跟着腿数（同步 / 正文 / 标已读）一起长。
	//   NOOP：   刚用过的连接不该再探活。这条一旦失效就每步一个 NOOP。
	if n := big.examine; n > 2 {
		t.Errorf("EXAMINE 发了 %d 条，每个文件夹最多 1 条（INBOX + Sent = 2）: %s", n, big.dump)
	}
	if n := big.noop; n > 1 {
		t.Errorf("NOOP 发了 %d 条，最多 1 条 —— 刚用过的连接又在反复探活: %s", n, big.dump)
	}
}

// TestConn_ServerDropCostsOneCallThenRecovers 钉住重连那条路：
// 服务商单方面掐断连接之后，**最多失败一次**，下一次必须自己重连、
// 重新选中文件夹，并给出正确结果。
//
// 这条判据存在的理由是 live.go 里那两处优化的取舍：省掉重复探活之后，
// 「连接死了」不再是下一条命令立刻就能发现的（见 livenessWindow 与
// markSuspectLocked）。取舍是允许的 —— 但**必须只赔一条命令**。
// 谁要是把 markSuspectLocked 从失败路径上去掉，就会变成「连接死了之后
// 每条命令都失败一遍」，这条立刻变红。
func TestConn_ServerDropCostsOneCallThenRecovers(t *testing.T) {
	ms := startMailServer(t)
	const n = 5
	for i := 0; i < n; i++ {
		ms.add(t, "INBOX", rawMail(fmt.Sprintf("第%d封", i), "alice@example.com",
			fmt.Sprintf("drop-%d@example.com", i)), true)
	}

	c := ms.client(t)
	first, err := c.Headers("INBOX", 1, 0)
	if err != nil {
		t.Fatalf("首次拉头部就失败了: %v", err)
	}

	// 服务端把连接掐了。客户端此刻还以为它活着（verifiedAt 在窗口内），
	// 这正是真实世界里的样子。
	ms.dropAll()

	// 第一条：**允许失败** —— 省掉重复探活的代价就是这一条。
	//
	// 判据只能写成「最多一条」，不能写成「一条都不许失败」：连接死掉这件事
	// 现在要等到一条命令真的跑起来才暴露（见 livenessWindow）。实测过，
	// 败下来的通常是 FETCH 那一步而不是 SELECT —— go-imap 把写缓冲了，
	// 死在读应答时才浮出来。所以下面刻意不假设失败发生在哪条命令上。
	if _, err := c.Headers("INBOX", 1, 0); err != nil {
		t.Logf("掐断后第一条失败（预期内）: %v", err)
	}

	// 第二条：必须自己活过来，而且结果要对。
	got, err := c.Headers("INBOX", 1, 0)
	if err != nil {
		t.Fatalf("掐断之后没能自动重连（只赔一条命令是允许的，第二条就不行）: %v", err)
	}
	if len(got) != len(first) {
		t.Errorf("重连后拉到 %d 条，掐断前是 %d 条 —— 重连之后漏了或重了",
			len(got), len(first))
	}

	// 重连之后选中状态是**新连接**上的，正文照样得拉得下来。
	// 这条专门盯「选中记忆跟着旧连接一起活了下来」那个坑。
	if _, err := c.Bodies("INBOX", uidsOf(got)); err != nil {
		t.Fatalf("重连后拉正文失败: %v", err)
	}
}
