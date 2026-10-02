package mail

import (
	"errors"
	"strings"
)

// ErrNoSuchFolder 表示服务端上没有这个文件夹。
//
// 上层拿它和真正的连接错误区分开：配置里写了某个文件夹、但这台服务器
// 上没有，不是故障，跳过就好 —— 不该让整个界面变成"离线"。
var ErrNoSuchFolder = errors.New("服务端上没有这个文件夹")

// folderAliases 把配置里用的规范名，映射到各家服务商实际用的名字。
//
// 键一律大写 —— 查找时统一 ToUpper，免得 "Sent" / "SENT" 两套写法对不上。
//
// 为什么必须有这张表：IMAP 没有规定「已发送」该叫什么，各家自己起名 ——
//
//	Gmail            Sent
//	QQ               Sent Messages
//	网易系 163/126   已发送        （Coremail，中文名）
//	阿里云           已发送
//	微软系           Sent Items
//
// 配置里只写 "Sent" 的话，在网易系上 EXAMINE 会直接失败。实测过：
// 126 的文件夹列表是 INBOX / 草稿箱 / 已发送 / 已删除 / 垃圾邮件 /
// 病毒文件夹 / 订阅邮件 —— 里面没有 "Sent"。
//
// 匹配顺序就是切片顺序，第一个命中的生效。
var folderAliases = map[string][]string{
	"INBOX": {"INBOX"},
	"SENT": {
		"Sent",
		"Sent Messages",
		"Sent Items",
		"Sent Mail",
		"已发送",
		"已发送邮件",
		"已发件箱",
	},
}

// resolveFolder 把配置里的文件夹名解析成服务端上真实存在的名字。
//
// available 为 nil 表示拿不到文件夹列表（LIST 失败）。这时不做任何
// 猜测，原样放行 —— 宁可在 SELECT 时自然报错，也不要凭一个残缺的
// 列表把用户本来能用的文件夹判死。
func resolveFolder(want string, available []string) (string, bool) {
	if available == nil {
		return want, true
	}

	// 第一优先：原样能对上就用它。大小写不敏感 —— INBOX 是唯一被 RFC
	// 规定大小写不敏感的文件夹名，别的按各家实际写法来也无妨。
	for _, a := range available {
		if strings.EqualFold(a, want) {
			return a, true
		}
	}

	// 第二优先：按别名逐个试。
	for _, alias := range folderAliases[strings.ToUpper(want)] {
		for _, a := range available {
			if strings.EqualFold(a, alias) {
				return a, true
			}
		}
	}

	return "", false
}
