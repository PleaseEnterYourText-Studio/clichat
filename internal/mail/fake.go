package mail

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Fake 是 Client 的内存实现，**只供测试使用**。
//
// 它不出现在任何产品代码路径里 —— clichat 没有离线演示模式，运行时
// 要么连上真实邮箱，要么什么都不做。Fake 存在的唯一理由是让整套
// 自动化测试能在不碰真实邮箱的前提下跑完。
//
// 因为它只被 _test.go 引用，Go 链接器的可达性分析会把它整个裁掉，
// 不会进入最终二进制。
type Fake struct {
	mu      sync.Mutex
	folders map[string]*fakeFolder
	sent    []Outgoing
	sendErr error

	// noUIDNext 为真时，Folder 报告 UIDNext=0，模拟「SELECT 响应里
	// 没有 UIDNEXT」的服务端。
	//
	// 网易 126 就是这样：它的 EXAMINE 不给 UIDNEXT，而真实代码曾经
	// 把这个当成「文件夹是空的」，于是收件箱永远同步不出邮件。
	// 这个开关存在的唯一意义，就是让那条分支在测试里真的被执行到 ——
	// 让 Fake 永远算得出 UIDNEXT 的话，那个 bug 谁都测不出来。
	noUIDNext bool
}

// SimulateNoUIDNext 让这个 Fake 表现得像不返回 UIDNEXT 的服务端。
func (f *Fake) SimulateNoUIDNext() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noUIDNext = true
}

type fakeFolder struct {
	name        string
	uidValidity uint32
	nextUID     uint32
	messages    map[uint32]*fakeMessage
}

type fakeMessage struct {
	header Header
	body   string
	// html 为真表示 body 是 HTML 转过来的（见 AddHTMLMessage）。
	html bool
}

// NewFake 创建一个空的假客户端，自带 INBOX、Sent、Trash 三个文件夹。
//
// Trash 是必须有的：界面的「删除」是移到垃圾箱，没有这个文件夹的话
// app.Delete 会直接报「服务端上没有已删除文件夹」，那条路径就测不到。
func NewFake() *Fake {
	f := &Fake{folders: map[string]*fakeFolder{}}
	f.folder("INBOX")
	f.folder("Sent")
	f.folder("Trash")
	return f
}

// AddMessage 往文件夹里塞一封纯文本邮件，返回分配到的 UID。
func (f *Fake) AddMessage(folder string, h Header, body string) uint32 {
	return f.add(folder, h, body, false)
}

// AddHTMLMessage 往文件夹里塞一封 **HTML** 邮件，返回分配到的 UID。
//
// rawHTML 是**原始 HTML**，不是转好的 Markdown：这里会走一遍真实的
// CleanBody(raw, true)，存进去的东西和线上取信得到的完全一致。Fake 的
// 意义就是让测试跑在生产那条路径上，正文转出来的形状也是这条路径的一部分
// —— 直接把转好的 Markdown 塞进去，等于把生成器从这条链路上摘掉了，
// 「HTML 邮件被标成 HTML」这类判据也就没了依据（那个标记正是从这一支
// 冒出来的，见 readPlainText）。
//
// 单独一个方法而不是给 AddMessage 加个 bool 参数：绝大多数测试用的是
// 纯文本，给它们全部改一遍调用点，换来的只是每个调用点多一个 false。
func (f *Fake) AddHTMLMessage(folder string, h Header, rawHTML string) uint32 {
	return f.add(folder, h, CleanBody(rawHTML, true), true)
}

func (f *Fake) add(folder string, h Header, body string, isHTML bool) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(folder)
	fl.nextUID++
	h.UID = fl.nextUID
	h.Folder = folder
	fl.messages[h.UID] = &fakeMessage{header: h, body: body, html: isHTML}
	return h.UID
}

// SentMessages 返回所有已发送的邮件，按发送顺序。
func (f *Fake) SentMessages() []Outgoing {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]Outgoing, len(f.sent))
	copy(out, f.sent)
	return out
}

// SetSendError 让后续的 Send 调用返回指定错误。
// 传 nil 恢复正常。
func (f *Fake) SetSendError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendErr = err
}

// ResetUIDValidity 模拟服务商重置 UID 空间。
func (f *Fake) ResetUIDValidity(folder string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(folder)
	fl.uidValidity++
	fl.nextUID = 0
	fl.messages = map[uint32]*fakeMessage{}
}

func (f *Fake) folder(name string) *fakeFolder {
	fl, ok := f.folders[name]
	if !ok {
		fl = &fakeFolder{name: name, uidValidity: 1, messages: map[uint32]*fakeMessage{}}
		f.folders[name] = fl
	}
	return fl
}

// Folder 选中一个文件夹并返回它的状态。
// Folder 返回文件夹状态。
//
// UIDNext 在 noUIDNext 模式下报 0 —— 真实客户端这时会去 STATUS 补查，
// 补不到就交给 `from:*` 兜底。Fake 直接把 0 交出去，让上层走同一条兜底路径。
func (f *Fake) Folder(name string) (Folder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(name)
	uidNext := fl.nextUID + 1
	if f.noUIDNext {
		uidNext = 0
	}
	return Folder{
		Name:        fl.name,
		UIDValidity: fl.uidValidity,
		UIDNext:     uidNext,
		Messages:    uint32(len(fl.messages)),
	}, nil
}

// Headers 拉取 UID 落在 [from, to] 区间内的邮件头部。to 为 0 表示到最新。
//
// 边界逻辑走的是和真实客户端同一个 fetchRange —— 各写一份的话，
// 「上界未知」这类只在真实世界里出现的情况在 Fake 里永远不发生。
func (f *Fake) Headers(folder string, from, to uint32) ([]Header, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(folder)

	uidNext := fl.nextUID + 1
	if f.noUIDNext {
		uidNext = 0
	}
	lo, hi, ok := fetchRange(from, to, uidNext)
	if !ok {
		return nil, nil
	}
	// 上界未知时按「到最新」处理。
	if hi == 0 {
		maxUID := uint32(0)
		for uid := range fl.messages {
			if uid > maxUID {
				maxUID = uid
			}
		}
		hi = maxUID
	}

	out := make([]Header, 0, len(fl.messages))
	for uid, m := range fl.messages {
		if uid >= lo && uid <= hi {
			out = append(out, m.header)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

// Body 拉取单封邮件的正文。
func (f *Fake) Body(folder string, uid uint32) (Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	m, ok := f.folder(folder).messages[uid]
	if !ok {
		return Message{}, fmt.Errorf("在 %s 里找不到 UID %d", folder, uid)
	}
	return Message{
		UID:      uid,
		Folder:   folder,
		Subject:  m.header.Subject,
		From:     m.header.From,
		FromName: m.header.FromName,
		Date:     m.header.Date,
		Body:     m.body,
		HTML:     m.html,
	}, nil
}

// MarkSeen 给一批邮件打上已读标志。
func (f *Fake) MarkSeen(folder string, uids []uint32) error {
	return f.SetFlag(folder, uids, FlagSeen, true)
}

// Folders 返回假客户端里现有的文件夹名（排序后，便于断言）。
func (f *Fake) Folders() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, 0, len(f.folders))
	for name := range f.folders {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// SetFlag 加上或去掉一批邮件上的某个标志。
func (f *Fake) SetFlag(folder string, uids []uint32, flag string, add bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(folder)
	for _, uid := range uids {
		m, ok := fl.messages[uid]
		if !ok {
			continue
		}
		switch flag {
		case FlagSeen:
			m.header.Seen = add
		case FlagFlagged:
			m.header.Flagged = add
		}
	}
	return nil
}

// Move 把一批邮件挪到另一个文件夹，并给它们分配新的 UID。
//
// 「换文件夹就换 UID」是照真实服务端的行为模仿的。这一点必须模仿到位：
// 删除之后本地索引里那些老 UID 就失效了，如果 Fake 保留原 UID，
// 测试就盖不住「索引没跟着更新」这类 bug。
func (f *Fake) Move(folder string, uids []uint32, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	src := f.folder(folder)
	dst := f.folder(dest)

	for _, uid := range uids {
		m, ok := src.messages[uid]
		if !ok {
			continue
		}
		delete(src.messages, uid)

		dst.nextUID++
		m.header.UID = dst.nextUID
		m.header.Folder = dest
		dst.messages[dst.nextUID] = m
	}
	return nil
}

// Send 记录一封发出的邮件，并模仿服务商把它存进 Sent 文件夹。
//
// 这个模仿很重要：真实环境里 clichat 依赖服务商自动保存已发送邮件
// （见设计文档风险 #2）。如果这里不模仿，测试就盖不住那条路径。
func (f *Fake) Send(msg Outgoing) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.sendErr != nil {
		return "", f.sendErr
	}
	if len(msg.To)+len(msg.Cc) == 0 {
		return "", fmt.Errorf("收件人为空")
	}

	id := GenerateMessageID("me@clichat.local")
	f.sent = append(f.sent, msg)

	fl := f.folder("Sent")
	fl.nextUID++
	fl.messages[fl.nextUID] = &fakeMessage{
		header: Header{
			MessageID:  id,
			References: msg.References,
			InReplyTo:  msg.InReplyTo,
			From:       "me@clichat.local",
			To:         msg.To,
			Cc:         msg.Cc,
			Subject:    msg.Subject,
			Date:       time.Now(),
			UID:        fl.nextUID,
			Folder:     "Sent",
			Seen:       true,
		},
		body: msg.Body,
	}
	return id, nil
}

// Close 什么也不做 —— 假客户端没有连接要断。
func (f *Fake) Close() error { return nil }
