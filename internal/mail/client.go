// Package mail 负责与邮箱服务商通信：IMAP 拉取、SMTP 发送、MIME 解析。
//
// 它对外只暴露一个 Client 接口，上层（app / tui）不关心底层是 IMAP
// 还是别的什么。抽出接口的唯一目的是让自动化测试能用 fake 实现 ——
// 测试永远不碰真实邮箱。
package mail

// Client 是邮箱服务的抽象。
type Client interface {
	// Folder 选中一个文件夹并返回它的状态。
	Folder(name string) (Folder, error)

	// Headers 拉取 UID 落在 [from, to] 区间内的邮件头部。
	// to 传 0 表示「到最新」。
	//
	// 只拉头部不拉正文，并且用 BODY.PEEK 以免把邮件误标为已读。
	Headers(folder string, from, to uint32) ([]Header, error)

	// Body 拉取单封邮件的纯文本正文。
	Body(folder string, uid uint32) (Message, error)

	// MarkSeen 给一批邮件打上 \Seen 标志。
	// 已读状态只存在服务端，本地不另存一份。
	MarkSeen(folder string, uids []uint32) error

	// Send 发送一封邮件，返回它的 Message-ID。
	Send(msg Outgoing) (string, error)

	// Close 断开连接。
	Close() error
}
