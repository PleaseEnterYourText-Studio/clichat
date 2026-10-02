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

// AddMessage 往文件夹里塞一封邮件，返回分配到的 UID。
func (f *Fake) AddMessage(folder string, h Header, body string) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(folder)
	fl.nextUID++
	h.UID = fl.nextUID
	h.Folder = folder
	fl.messages[h.UID] = &fakeMessage{header: h, body: body}
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
func (f *Fake) Folder(name string) (Folder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(name)
	return Folder{
		Name:        fl.name,
		UIDValidity: fl.uidValidity,
		UIDNext:     fl.nextUID + 1,
		Messages:    uint32(len(fl.messages)),
	}, nil
}

// Headers 拉取 UID 落在 [from, to] 区间内的邮件头部。to 为 0 表示到最新。
func (f *Fake) Headers(folder string, from, to uint32) ([]Header, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fl := f.folder(folder)
	high := to
	if high == 0 {
		high = fl.nextUID
	}
	if from == 0 {
		from = 1
	}

	out := make([]Header, 0, len(fl.messages))
	for uid, m := range fl.messages {
		if uid >= from && uid <= high {
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
