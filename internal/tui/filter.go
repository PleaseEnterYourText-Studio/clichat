package tui

import (
	"strings"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// filterThreads 按文件夹和搜索词过滤会话。
//
// folder 为空串表示不按文件夹过滤，query 为空串表示不搜索。
func filterThreads(list []thread.Thread, folder, query string) []thread.Thread {
	q := strings.ToLower(strings.TrimSpace(query))
	if folder == "" && q == "" {
		// 不过滤时原样返回：省一次拷贝，也让「没在过滤」这件事
		// 在测试里可以直接断言。
		return list
	}

	out := make([]thread.Thread, 0, len(list))
	for _, th := range list {
		if folder != "" && !threadInFolder(th, folder) {
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

// groupUnreadFirst 把未读会话稳定地挪到前面，让列表能按「未读 / 已读」
// 分组。
//
// **分组必然带来重排。** 想分组又不重排是不可能的：一边要「未读的都
// 挨在一起」，一边又要「严格按时间排」，遇到一封两天前的未读夹在今天
// 两封已读中间就无解了。所以这里选了「分组优先」，组内仍然按时间
// （也就是原顺序保持不变，稳定分区）。
//
// 为什么必须是**稳定**分区：光标和列表位置是一一对应的（m.cursor 索引
// 的就是这个切片），组内乱序会让按下箭头跑到一条完全无关的会话上。
//
// 代价要说清楚：列表不再是严格的时间序。换来的是未读会浮到顶上 ——
// 这也是导航上那些未读数字能派上用场的地方。
func groupUnreadFirst(list []thread.Thread) []thread.Thread {
	n := 0
	for _, th := range list {
		if th.Unread > 0 {
			n++
		}
	}
	if n == 0 || n == len(list) {
		// 全已读或全未读：分组标题不会画，顺序也就没必要动。
		// 原样返回还省一次拷贝。
		return list
	}

	out := make([]thread.Thread, 0, len(list))
	for _, th := range list {
		if th.Unread > 0 {
			out = append(out, th)
		}
	}
	for _, th := range list {
		if th.Unread == 0 {
			out = append(out, th)
		}
	}
	return out
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
