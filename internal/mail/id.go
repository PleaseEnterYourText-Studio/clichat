package mail

import (
	"fmt"

	"github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
)

// ID 命令（RFC 2971）里上报的固定字段。
//
// 字段名用的是 RFC 2971 注册过的名字（name / version / vendor /
// support-url），不是自造的 —— 服务端日志里按这些名字解析。
const (
	clientIDName       = "clichat"
	clientIDVendor     = "PleaseEnterYourText-Studio"
	clientIDSupportURL = "https://github.com/PleaseEnterYourText-Studio/clichat"
)

// clientIDVersion 是 ID 命令里上报的版本号。
//
// 默认 dev，由 main 通过 SetClientIDVersion 注入构建版本，
// 好让服务端看到的版本和 -version 打印的一致。
var clientIDVersion = "dev"

// SetClientIDVersion 设置 ID 命令上报的版本号。空串忽略，保持默认。
func SetClientIDVersion(v string) {
	if v != "" {
		clientIDVersion = v
	}
}

// sendClientID 在登录成功后向服务端表明客户端身份。
//
// 为什么非发不可：网易系（163 / 126 / yeah.net）在 IMAP 登录后要求客户端
// 先发 ID 表明身份，否则后续第一条命令就被拒：
//
//	NO [UNAVAILABLE] Unsafe Login. Please contact kefu@188.com
//
// 这个错有个很坑的表现：LOGIN 是成功的，挂在紧接着的 EXAMINE INBOX 上。
// 只看"连不上"很容易误判成授权码填错了，实际是少了这条 ID。
//
// 服务端没声明 ID 能力时直接跳过：ID 是可选扩展（RFC 2971），
// 对不支持的服务器发过去只会换回一个 BAD。
func sendClientID(conn *imapclient.Client) error {
	supported, err := conn.Support("ID")
	if err != nil {
		// 拿不到能力列表不该挡登录 —— 顶多这次没发 ID，
		// 比因为一次 CAPABILITY 抖动就整个连不上强。
		return nil
	}
	if !supported {
		return nil
	}

	// go-imap v1 没实现 ID 扩展（v2 才有），所以借它的 Execute 口子自己发。
	// Execute 会自己分配 tag；参数按 IMAP 语法编码 —— 外层 []interface{}
	// 是参数列表，内层那个 []interface{} 渲染成 (key value ...) 的列表。
	cmd := &imap.Command{
		Name: "ID",
		Arguments: []interface{}{
			[]interface{}{
				"name", clientIDName,
				"version", clientIDVersion,
				"vendor", clientIDVendor,
				"support-url", clientIDSupportURL,
			},
		},
	}

	status, err := conn.Execute(cmd, nil)
	if err != nil {
		return fmt.Errorf("发送 ID 命令失败: %w", err)
	}
	// Execute 只报网络层错误：服务端回 NO / BAD 时 err 是 nil，
	// 得自己从状态行里看出来。不回查的话会把"被拒"当成"发成功"，
	// 然后在 EXAMINE 那里收到一个看不懂的 Unsafe Login。
	if err := status.Err(); err != nil {
		return fmt.Errorf("服务端拒绝 ID 命令: %w", err)
	}
	return nil
}
