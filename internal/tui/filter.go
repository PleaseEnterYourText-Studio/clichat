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

// firstUnread 从 start 开始（含）朝 dir 方向找第一个未读会话的下标。
// 找不到返回 -1。dir 只能是 +1 或 -1。
//
// 列表顺序现在是**稳定的时间序**（不再按未读分区，见 refreshVisible），
// 所以「跳到下一条未读」必须靠**找**，不能靠「往下走一格」。这正是这个
// 函数存在的理由，也是 `u` 键比 `↓` 好用的原因：一屏已读里按 `↓` 要按
// 很多下，按 `u` 一步就到。
//
// （这里原本还有一个 groupUnreadFirst，把未读稳定地挪到前面。删掉它的
// 理由见 refreshVisible 的注释：**分组必然带来重排**，而重排发生在用户
// 正在操作的列表上，代价比「未读浮到顶上」那点好处大得多。）
func firstUnread(list []thread.Thread, start, dir int) int {
	for i := start; i >= 0 && i < len(list); i += dir {
		if list[i].Unread > 0 {
			return i
		}
	}
	return -1
}
