package tui

import (
	"strings"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// filterThreads 按文件夹、搜索词和「只看 clichat 消息」过滤会话。
//
// folder 为空串表示不按文件夹过滤，query 为空串表示不搜索，chatOnly 为假
// 表示不管这封邮件是谁发的。
//
// 三个条件写在同一个函数里而不是叠成三层调用：它们是**同一种操作**
// （从列表里挑出要显示的那些），界面上任何一处改变过滤条件都要重算这一份
// 结果。分开写的话，「改了条件却只重算了其中一层」迟早会发生，而症状是
// 列表显示的和条件对不上 —— 那种错很难看出来。
func filterThreads(list []thread.Thread, folder, query string, chatOnly bool) []thread.Thread {
	q := strings.ToLower(strings.TrimSpace(query))
	if folder == "" && q == "" && !chatOnly {
		// 不过滤时原样返回：省一次拷贝，也让「没在过滤」这件事
		// 在测试里可以直接断言。
		return list
	}

	out := make([]thread.Thread, 0, len(list))
	for _, th := range list {
		if folder != "" && !threadInFolder(th, folder) {
			continue
		}
		if chatOnly && !th.HasClichat() {
			// 判据问的是整个会话（HasClichat 看的是「至少有一条」）：
			// 一个会话里常常混着对方从网页邮箱发的邮件，按「这个会话
			// 里有没有 clichat 消息」过滤才符合「跟用 clichat 的人聊天」
			// 这个诉求。只认最后一条的话，对方换个客户端回一句，整个
			// 会话就从列表里消失了。
			continue
		}
		if q != "" && !threadMatches(th, q) {
			continue
		}
		out = append(out, th)
	}
	return out
}

// threadInFolder 判断会话在某个文件夹里有没有消息。
//
// 问的是「有没有」而不是「属不属于」：一个会话本来就可能横跨文件夹
// —— 对方发到 INBOX，我的回复在 Sent。只认一个文件夹会让会话在
// 切换文件夹时忽隐忽现。
func threadInFolder(th thread.Thread, folder string) bool {
	for _, m := range th.Messages {
		if m.Folder == folder {
			return true
		}
	}
	return false
}

// threadMatches 判断会话是否命中搜索词。query 必须已经是小写。
//
// 匹配范围刻意放宽到参与者地址和显示名：这个界面里最常见的诉求是
// 「找那个人」，只搜主题会漏掉一大半。会话标题本身就是对方的名字，
// 搜不到人比搜不到主题难受得多。
func threadMatches(th thread.Thread, query string) bool {
	if strings.Contains(strings.ToLower(th.Subject), query) {
		return true
	}
	for _, p := range th.Peers {
		// Peers 里的地址在聚合时已经小写化过了。
		if strings.Contains(p, query) {
			return true
		}
	}
	for _, m := range th.Messages {
		if strings.Contains(strings.ToLower(m.FromName), query) {
			return true
		}
	}
	return false
}

// firstUnread 从 start 开始（含）朝 dir 方向找第一个未读会话的下标。
// 找不到返回 -1。dir 只能是 +1 或 -1。
func firstUnread(list []thread.Thread, start, dir int) int {
	for i := start; i >= 0 && i < len(list); i += dir {
		if list[i].Unread > 0 {
			return i
		}
	}
	return -1
}
