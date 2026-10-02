package mail

import (
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

// Message 是一封邮件的**正文**。
//
// 只有正文，没有元信息 —— 主题 / 发件人 / 时间都从 Header 来（会话流渲染
// 读的是 thread.Header）。原先这里还挂着 Subject / From / FromName / Date
// 四个字段，但**一个读者都没有**，代价却是每拉一封正文都要在 FETCH 里多带
// 一项 ENVELOPE。删掉之后请求变小，也不会再有人误以为「拉正文顺便就拿到
// 了时间」。
type Message struct {
	UID    uint32
	Folder string
	// Body 是正文。
	//
	// HTML 邮件在入库前已经转成了 Markdown（见 htmlmd.go 的
	// HTMLToMarkdown），所以里面的 * _ [ ] 都是转义过的字面字符；
	// text/plain 邮件则原样保留作者写的字符，不做转义 —— 那是他自己
	// 的正文，不是标记，没有理由替他改。
	//
	// 引用历史已经被砍掉（见 TrimQuoted）。
	Body string
	// HTML 为真表示 Body 是 text/html 那一部分**转过来的** ——
	// 正文里的换行、列表、表格、标题都是转换器重排的结果，不是作者
	// 原样敲的（见 HTMLToMarkdown）。为假则是 text/plain 原样保留。
	//
	// 单独立一个字段而不是让调用方去猜：转换是有损的（字号、颜色、
	// 多栏布局都丢了），界面上要能把这件事告诉用户，否则「排版和邮件
	// 原文不一样」只会被当成渲染器坏了。
	HTML bool
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
