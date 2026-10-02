// Package mail 负责与邮箱服务商通信：IMAP 拉取、SMTP 发送、MIME 解析。
//
// 它对外只暴露一个 Client 接口，上层（app / tui）不关心底层是 IMAP
// 还是别的什么。抽出接口的唯一目的是让自动化测试能用 fake 实现 ——
// 测试永远不碰真实邮箱。
package mail

// 标志名。上层（app / tui）用这两个常量，不必去 import go-imap。
const (
	// FlagSeen 是「已读」。去掉它就是把邮件标回未读。
	FlagSeen = `\Seen`
	// FlagFlagged 是「星标」。
	FlagFlagged = `\Flagged`
)

// Client 是邮箱服务的抽象。
type Client interface {
	// Folder 选中一个文件夹并返回它的状态。
	Folder(name string) (Folder, error)

	// Folders 列出服务端上实际存在的文件夹名。
	//
	// 界面靠它做文件夹切换 —— 用户能切的只能是服务端真的有的，
	// 拿配置里的规范名去切会切到不存在的文件夹上。
	Folders() ([]string, error)

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

	// SetFlag 加上或去掉一批邮件上的某个标志。
	// add 为 false 表示去掉。标未读（去掉 FlagSeen）和星标
	// （加上 FlagFlagged）都走它。
	SetFlag(folder string, uids []uint32, flag string, add bool) error

	// Move 把一批邮件移到另一个文件夹。
	// 界面的「删除」就是移到垃圾箱，不是真删。
	Move(folder string, uids []uint32, dest string) error

	// Send 发送一封邮件，返回它的 Message-ID。
	Send(msg Outgoing) (string, error)

	// Close 断开连接。
	Close() error
}
