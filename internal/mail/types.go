package mail

import (
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// Header 是聚合算法需要的邮件头部。
//
// 直接复用 thread.Header，不另立一套再转换 —— 两边字段完全一致，
// 多一层转换只会多一个出错的地方。
type Header = thread.Header

// Folder 是一次 SELECT 的结果。
type Folder struct {
	Name        string
	UIDValidity uint32
	UIDNext     uint32
	Messages    uint32
}

// Message 是一封邮件的正文与元信息。
type Message struct {
	UID      uint32
	Folder   string
	Subject  string
	From     string
	FromName string
	Date     time.Time
	// Body 是正文。
	//
	// HTML 邮件在入库前已经转成了 Markdown（见 htmlmd.go 的
	// HTMLToMarkdown），所以里面的 * _ [ ] 都是转义过的字面字符；
	// text/plain 邮件则原样保留作者写的字符，不做转义 —— 那是他自己
	// 的正文，不是标记，没有理由替他改。
	//
	// 引用历史已经被砍掉（见 TrimQuoted）。
	Body string
}

// Outgoing 是一封待发送的邮件。
type Outgoing struct {
	To         []string
	Cc         []string
	Subject    string
	Body       string
	InReplyTo  string
	References []string
}
