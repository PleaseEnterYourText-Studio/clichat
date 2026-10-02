package mail

import (
	"bufio"
	"io"
	"log"
	"net"
	"regexp"
	"strings"
	"testing"

	imapclient "github.com/emersion/go-imap/client"
)

// fakeIMAPServer 是一个脚本化的假 IMAP 服务端。
//
// 它不实现任何邮箱语义 —— 收到什么就按固定的剧本回什么。它存在的理由很窄：
// **观察客户端在登录之后发了哪些命令**。
//
// 真实的网易服务端（那个会因为缺 ID 就拒掉 EXAMINE 的家伙）没法在单测里复现，
// 所以「发了 ID」这件事只能在客户端这一侧、对着一个假的接收端来判。
type fakeIMAPServer struct {
	ln      net.Listener
	caps    string // 问候语和 CAPABILITY 里声明的能力
	idReply string // 对 ID 命令的应答状态：OK / NO / BAD

	cmds chan string // 收到的命令行，已剥掉 tag
}

// newFakeIMAPServer 起一个假服务端，返回它和它的监听地址。
func newFakeIMAPServer(t *testing.T, caps, idReply string) (*fakeIMAPServer, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起不了假服务端: %v", err)
	}

	s := &fakeIMAPServer{ln: ln, caps: caps, idReply: idReply, cmds: make(chan string, 64)}
	go s.serve()

	t.Cleanup(func() { _ = ln.Close() })
	return s, ln.Addr().String()
}

func (s *fakeIMAPServer) serve() {
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	say := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}

	say("* OK [CAPABILITY " + s.caps + "] clichat test server")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		tag, rest := splitTag(line)
		// 先入队再应答：这样客户端那条命令返回时，这里一定已经记下了。
		s.cmds <- rest

		switch strings.ToUpper(firstWord(rest)) {
		case "CAPABILITY":
			say("* CAPABILITY " + s.caps)
			say(tag + " OK CAPABILITY completed")
		case "LOGIN":
			say(tag + " OK LOGIN completed")
		case "ID":
			// 真实服务端会回一条不带 tag 的 ID 响应，故意回上，
			// 确认它不会把客户端的命令处理搞乱。
			say(`* ID ("name" "Coremail" "version" "1.0")`)
			say(tag + " " + s.idReply + " ID completed")
		case "LOGOUT":
			say("* BYE bye")
			say(tag + " OK LOGOUT completed")
			return
		default:
			say(tag + " BAD unknown command")
		}
	}
}

// commands 取回服务端到目前为止收到的所有命令行（已剥掉 tag）。
//
// 只能调一次 —— 它会清空缓冲。想找多条就自己在这个切片上筛。
func (s *fakeIMAPServer) commands() []string {
	var out []string
	for {
		select {
		case c := <-s.cmds:
			out = append(out, c)
		default:
			return out
		}
	}
}

// splitTag 把 "a1 LOGIN x y" 拆成 ("a1", "LOGIN x y")。
func splitTag(line string) (tag, rest string) {
	if i := strings.IndexByte(line, ' '); i > 0 {
		return line[:i], line[i+1:]
	}
	return line, ""
}

// firstWord 取第一个空格前的部分。
func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

// findVerb 在命令行集合里找第一条以 verb 开头的。找不到返回空串。
func findVerb(cmds []string, verb string) string {
	for _, c := range cmds {
		if strings.EqualFold(firstWord(c), verb) {
			return c
		}
	}
	return ""
}

// quotedToken 匹配 IMAP 引号字符串里的内容。
var quotedToken = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// quotedTokens 按出现顺序取出所有引号字符串。
//
// 刻意不做完整语法解析：要判的是「里面有没有 name=clichat 这一对」，
// 不是「这一行的字节长什么样」。绑死字节的判据在库升级后会变成假红。
func quotedTokens(s string) []string {
	ms := quotedToken.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

// hasPair 判断 tokens 里有没有相邻的 key / value 对。
func hasPair(tokens []string, key, want string) bool {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == key && tokens[i+1] == want {
			return true
		}
	}
	return false
}

// dialFake 连上假服务端并包成一个 IMAP 客户端。
func dialFake(t *testing.T, addr string) *imapclient.Client {
	t.Helper()

	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("连不上假服务端: %v", err)
	}

	c, err := imapclient.New(nc)
	if err != nil {
		t.Fatalf("imapclient.New 失败: %v", err)
	}
	// 收尾时服务端可能已经关了，别让库把噪音打到测试输出里。
	c.ErrorLog = log.New(io.Discard, "", 0)

	t.Cleanup(func() { _ = c.Logout() })
	return c
}

// 登录后必须发 ID，且里面要带上客户端名和当前版本号。
//
// 这是本次修复的核心判据：网易系少了这条命令就会拒掉后续的 EXAMINE，
// 报 "Unsafe Login"，而 LOGIN 本身是成功的。
func TestSendClientID_AnnouncesNameAndVersion(t *testing.T) {
	const wantVersion = "9.9.9-test"

	SetClientIDVersion(wantVersion)
	t.Cleanup(func() { SetClientIDVersion("dev") })

	srv, addr := newFakeIMAPServer(t, "IMAP4rev1 ID", "OK")
	c := dialFake(t, addr)

	if err := c.Login("me@126.com", "authcode"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if err := sendClientID(c); err != nil {
		t.Fatalf("sendClientID 报错: %v", err)
	}

	line := findVerb(srv.commands(), "ID")
	if line == "" {
		t.Fatal("登录后没有发出 ID 命令 —— 网易系会因此在 EXAMINE 上报 Unsafe Login")
	}
	t.Logf("ID 命令 = %s", line)

	tokens := quotedTokens(line)
	if !hasPair(tokens, "name", clientIDName) {
		t.Errorf("ID 里没有 name=%s: %v", clientIDName, tokens)
	}
	if !hasPair(tokens, "version", wantVersion) {
		t.Errorf("ID 里没有 version=%s（版本注入没生效？）: %v", wantVersion, tokens)
	}
	if !hasPair(tokens, "vendor", clientIDVendor) {
		t.Errorf("ID 里没有 vendor=%s: %v", clientIDVendor, tokens)
	}
}

// 服务端没声明 ID 能力时不许发 —— ID 是可选扩展，对不支持的服务器
// 发过去只会换回一个 BAD。
func TestSendClientID_SkipsWhenUnsupported(t *testing.T) {
	srv, addr := newFakeIMAPServer(t, "IMAP4rev1", "OK")
	c := dialFake(t, addr)

	if err := c.Login("me@example.com", "pw"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if err := sendClientID(c); err != nil {
		t.Fatalf("不支持 ID 时不该报错: %v", err)
	}

	if line := findVerb(srv.commands(), "ID"); line != "" {
		t.Errorf("服务端没声明 ID 能力，却发了 ID: %s", line)
	}
}

// 服务端拒掉 ID 时必须报错，不能当成发成功。
//
// 不报的话，用户看到的会是 EXAMINE 阶段那个语焉不详的 Unsafe Login，
// 而不是「身份声明被拒」这个真正的原因。
func TestSendClientID_ReportsRejection(t *testing.T) {
	_, addr := newFakeIMAPServer(t, "IMAP4rev1 ID", "NO")
	c := dialFake(t, addr)

	if err := c.Login("me@example.com", "pw"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	err := sendClientID(c)
	if err == nil {
		t.Fatal("服务端回了 NO，sendClientID 却报成功")
	}
	t.Logf("错误 = %v", err)
}
