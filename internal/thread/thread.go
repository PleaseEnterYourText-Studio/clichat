// Package thread 把散装的邮件头部聚合成会话线程。
//
// 这是纯函数模块：无 IO、无全局状态、可完全单测。
//
// # 聚合规则
//
// 对每条消息，按优先级挑出它的「父消息」：
//
//  1. References[0]  —— 引用链的第一个 ID 就是线程根
//  2. In-Reply-To    —— 引用链缺失时的退化路径
//  3. 都没有          —— 自己就是线程根
//
// 关键点：父消息 **不需要** 存在于本地索引里。两条都引用同一个尚未拉取的
// 根消息的邮件，依然会被正确合并。这正是冷启动能工作的原因。
//
// # 明确不做
//
// 不做 Subject 兜底匹配。它会把「同标题的两场不同讨论」错误合并。
// 宁可有断链，不要错并。
package thread

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Header 是聚合算法需要的最小邮件头部信息。
//
// 地址字段一律是 **纯地址且已小写规范化**（如 alice@example.com），
// 显示名单独放在 FromName。解析与规范化由 mail 包负责，
// 这样本包能保持零依赖、零 IO。
type Header struct {
	MessageID  string
	References []string
	InReplyTo  string
	From       string
	FromName   string
	To         []string
	Cc         []string
	Subject    string
	Date       time.Time
	UID        uint32
	Folder     string
	Seen       bool
}

// Thread 是一次聚合产出的会话。
type Thread struct {
	// Root 是线程根的 Message-ID。它可能并不存在于输入里
	// （根消息比我们拉取的时间窗口更早），此时仅作标识用。
	Root string

	// Messages 按 (Date, MessageID) 升序排列。
	Messages []Header

	// Participants 是会话中出现过的所有地址（不含 self），
	// 按首次出现顺序排列。
	Participants []string

	// Subject 是会话中最早那条消息的 Subject，已剥掉回复/转发前缀。
	Subject string

	// LastDate 是会话中最新一条消息的时间。
	LastDate time.Time

	// Unread 是会话中未读消息的条数。
	Unread int
}

// MessageIDs 返回会话内所有消息的 Message-ID，按 Date 升序。
func (t Thread) MessageIDs() []string {
	ids := make([]string, len(t.Messages))
	for i, m := range t.Messages {
		ids[i] = m.MessageID
	}
	return ids
}

// LastMessageID 返回会话中最新一条消息的 Message-ID。
func (t Thread) LastMessageID() string {
	if len(t.Messages) == 0 {
		return ""
	}
	return t.Messages[len(t.Messages)-1].MessageID
}

// LastFrom 返回会话中最新一条消息的发件人地址。
func (t Thread) LastFrom() string {
	if len(t.Messages) == 0 {
		return ""
	}
	return t.Messages[len(t.Messages)-1].From
}

// IsGroup 判断是否为群聊。
//
// 规则：会话中出现过的地址（不含自己）≥ 2 个即为群聊，
// 等价于「含自己 ≥ 3」。
//
// 注意一个预期行为：一次 CC 了第三人的 1:1 对话会被算作群聊。
// 这是规则的正常结果，不做特例。
func (t Thread) IsGroup() bool {
	return len(t.Participants) >= 2
}

// Aggregate 把邮件头部聚合成会话，按 LastDate 降序返回。
//
// self 是当前账号地址（内部会做小写规范化），用于把「自己」
// 从参与者列表里剔除。
//
// 输出是确定的：同样的输入集合，无论顺序如何，结果都一致。
func Aggregate(headers []Header, self string) []Thread {
	self = NormalizeAddress(self)

	// 先补齐缺失的 Message-ID，再按 ID 去重 —— 这样合成 ID 撞车
	// 的情况也能被去重覆盖到。
	msgs := make([]Header, len(headers))
	copy(msgs, headers)
	for i := range msgs {
		if msgs[i].MessageID == "" {
			msgs[i].MessageID = syntheticID(msgs[i])
		}
	}
	msgs = dedupeByMessageID(msgs)

	uf := newUnionFind()
	for i := range msgs {
		uf.add(msgs[i].MessageID)
	}
	for i := range msgs {
		if parent := threadParent(msgs[i]); parent != "" {
			uf.union(msgs[i].MessageID, parent)
		}
	}

	// 按并查集的根分组，同时保持首次出现的顺序，保证输出确定。
	groups := make(map[string][]int)
	roots := make([]string, 0, len(msgs))
	for i := range msgs {
		root := uf.find(msgs[i].MessageID)
		if _, seen := groups[root]; !seen {
			roots = append(roots, root)
		}
		groups[root] = append(groups[root], i)
	}

	threads := make([]Thread, 0, len(roots))
	for _, root := range roots {
		threads = append(threads, buildThread(root, groups[root], msgs, self))
	}

	sort.Slice(threads, func(i, j int) bool {
		if threads[i].LastDate.Equal(threads[j].LastDate) {
			return threads[i].Root < threads[j].Root
		}
		return threads[i].LastDate.After(threads[j].LastDate)
	})

	return threads
}

// threadParent 按优先级挑出这条消息的父消息 ID。
// 返回空串表示它自己是线程根。
func threadParent(m Header) string {
	if len(m.References) > 0 {
		if id := strings.TrimSpace(m.References[0]); id != "" {
			return id
		}
	}
	return strings.TrimSpace(m.InReplyTo)
}

// buildThread 把一组消息组装成一个 Thread。
func buildThread(root string, idx []int, msgs []Header, self string) Thread {
	list := make([]Header, len(idx))
	for i, k := range idx {
		list[i] = msgs[k]
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Date.Equal(list[j].Date) {
			return list[i].MessageID < list[j].MessageID
		}
		return list[i].Date.Before(list[j].Date)
	})

	t := Thread{
		Root:     root,
		Messages: list,
		Subject:  StripSubjectPrefix(list[0].Subject),
		LastDate: list[len(list)-1].Date,
	}

	seen := make(map[string]bool)
	for _, m := range list {
		if !m.Seen {
			t.Unread++
		}
		for _, addr := range addressesOf(m) {
			a := NormalizeAddress(addr)
			if a == "" || a == self || seen[a] {
				continue
			}
			seen[a] = true
			t.Participants = append(t.Participants, a)
		}
	}

	return t
}

func addressesOf(m Header) []string {
	out := make([]string, 0, 2+len(m.To)+len(m.Cc))
	out = append(out, m.From)
	out = append(out, m.To...)
	out = append(out, m.Cc...)
	return out
}

// dedupeByMessageID 去掉 Message-ID 重复的邮件。
//
// 同一封邮件可能同时出现在 INBOX 和 Sent（比如你把自己 CC 了进去），
// 也可能被两个文件夹规则同时命中。保留首次出现的那个，
// 但 Seen 标志按「只要有一份已读就算已读」合并。
//
// 调用前必须保证所有消息都有非空 Message-ID。
func dedupeByMessageID(headers []Header) []Header {
	out := make([]Header, 0, len(headers))
	pos := make(map[string]int, len(headers))
	for _, h := range headers {
		if i, ok := pos[h.MessageID]; ok {
			if h.Seen {
				out[i].Seen = true
			}
			continue
		}
		pos[h.MessageID] = len(out)
		out = append(out, h)
	}
	return out
}

// syntheticID 给没有 Message-ID 的消息合成一个稳定 ID。
//
// 必须稳定：同一封邮件在多次同步之间要得到相同结果，
// 否则每次轮询都会多出一个新会话。
func syntheticID(m Header) string {
	h := sha1.New()
	parts := []string{
		m.Folder,
		strconv.FormatUint(uint64(m.UID), 10),
		m.Date.UTC().Format(time.RFC3339Nano),
		m.From,
		m.Subject,
	}
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return "synthetic-" + hex.EncodeToString(h.Sum(nil))
}

// NormalizeAddress 把地址统一成小写去空格的形式，便于比较。
func NormalizeAddress(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}

// subjectPrefixes 是要从 Subject 开头剥掉的回复/转发前缀。
var subjectPrefixes = []string{
	"回复：", "回复:", "答复：", "答复:", "回覆：", "回覆:",
	"转发：", "转发:", "转寄：", "转寄:",
	"re:", "fw:", "fwd:", "aw:", "sv:", "vs:",
}

// StripSubjectPrefix 反复剥掉 Subject 开头的回复/转发前缀与空白，
// 直到没有可剥的为止。剥不掉时原样返回（去掉首尾空白）。
func StripSubjectPrefix(s string) string {
	s = strings.TrimSpace(s)
	for {
		// 前缀都是 ASCII 或中文，ToLower 不会改变它们的字节长度。
		lower := strings.ToLower(s)
		cut := 0
		for _, p := range subjectPrefixes {
			if strings.HasPrefix(lower, p) {
				cut = len(p)
				break
			}
		}
		if cut == 0 {
			return s
		}
		s = strings.TrimSpace(s[cut:])
	}
}

// unionFind 是不带 rank 的并查集，但有一条关键约定：
// union(child, parent) 永远让 **被引用的那一方当根**。
//
// 这样并查集的根会自然收敛到真正的线程根（References 链的起点），
// 而不是任意一个节点。路径压缩保证复杂度依然接近 O(1)。
type unionFind struct {
	parent map[string]string
}

func newUnionFind() *unionFind {
	return &unionFind{parent: make(map[string]string)}
}

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
}

func (u *unionFind) find(x string) string {
	u.add(x)
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
	}
	// 路径压缩
	for u.parent[x] != root {
		next := u.parent[x]
		u.parent[x] = root
		x = next
	}
	return root
}

func (u *unionFind) union(child, parent string) {
	rc, rp := u.find(child), u.find(parent)
	if rc == rp {
		return
	}
	u.parent[rc] = rp
}
