// Package thread 把散装的邮件头部聚合成会话。
//
// 这是纯函数模块：无 IO、无全局状态、可完全单测。
//
// # 聚合规则
//
// 一个会话 = 和同一批人的全部往来。具体地，把每条消息的 From / To / Cc
// 合起来、去掉自己，得到一个**参与人集合**；参与人集合相同的消息属于
// 同一个会话。
//
// 这是「IM 语义」而不是「邮件线程语义」，是本包有意做的取舍：clichat 把
// 邮件按层级呈现成聊天，会话列表要回答的是「和谁」，不是「哪个话题」。
//
//   - **双向**：Alice 发我的、我发 Alice 的，参与人都是 {alice} —— 同一个
//     会话。IM 里给某人发的消息本来就留在同一个窗口里。
//   - **同样一批人的群发单独一个会话**：To 是 [Alice, Bob] 的讨论不会和
//     只跟 Alice 的私聊混在一起。
//   - 集合按**排序去重**后当键，所以聚合结果与输入顺序无关。
//
// # References / In-Reply-To 不再参与聚合
//
// 这是本包最反直觉的一处，理由写在这里：
//
//   - 一个话题里的消息本来就引用彼此、共用同一个参与人集合，所以按参与人
//     聚合**天然蕴含**了线程合并 —— 回复链不可能被拆散，不需要并查集。
//   - 「参与人相同但引用链断了」（对方换了客户端、中间几封没同步下来、
//     根消息早于拉取窗口）现在也能并起来，比按引用链更抗噪。
//
// 代价是「同一个人 + 不同话题」也会合到一起。这是选定的行为，不是副作用：
// 会话的名字是「和谁」，「聊什么」降到列表里的副行（最新一条的主题）。
//
// ⚠️ 因为这条规则，**界面上的一个会话不等于一个话题**，回复一个会话时
// 引用链只能描述最后那一条消息的祖先（见 Thread.ReplyRefs），不能把会话
// 里所有 Message-ID 都挂上去 —— 和同一个人来往几年的邮件有几千封，
// 那个 References 头部会长到让服务端拒收。
//
// （旧版本这里写着「不做 Subject 兜底匹配……宁可有断链，不要错并」。
// 那条规矩是跟着引用链聚合一起的，两者一起作废了。）
package thread

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Header 是界面需要用到的邮件头部信息。
//
// 名字里那个「聚合算法需要」已经被 Flagged / Clichat 撑破了 —— 它们
// 不参与聚合，但和其余字段一样属于「这封邮件的元信息」，而元信息在
// 这个仓库里只有这一处存放点（正文走 mail.Message）。与其为它们另开
// 一条从 IMAP 到界面的路，不如都挂在这里。
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
	// Flagged 对应 IMAP 的 \Flagged 标志，界面上呈现为星标。
	Flagged bool

	// Clichat 是发件人随信带来的客户端身份信息，nil 表示这封邮件
	// **不是** clichat 发的（普通的邮件客户端、或者网页邮箱）。
	//
	// 形状定义在这里而不是 mail 包：thread 不能依赖 mail（是 mail
	// 依赖 thread），而 Header 是两者的公共面 —— 和上面那堆地址字段
	// 的归属方式一样。线上格式的唯一权威是 mail/chatmeta.go，那边
	// 负责把 X-Clichat-Meta 头解成这个结构。
	Clichat *ClichatMeta
}

// ClichatMeta 是 clichat 客户端之间互传的身份信息。
//
// 它描述的全是**发件人自己**的偏好，所以每个字段都可能为空：对面可能
// 关掉了设备信息上报，也可能用的是刚装上的默认配置。界面上每个字段
// 都得有「没有」的呈现方式，不能假定它一定有值。
//
// json tag 就是它在 X-Clichat-Meta 头里的字段名（值本身还要再过一层
// base64，见 mail/chatmeta.go）。用单字母是为了压小载荷 —— 这个头会
// 跟着**每一封**同步下来的邮件走。
type ClichatMeta struct {
	// Version 是发信方的 clichat 版本号，同时充当我们判断
	// 「这是不是 clichat 消息」的**唯一依据**：它是空的就整个不算数
	// （见 mail.DecodeChatMeta）。
	Version   string `json:"v"`
	Nick      string `json:"n,omitempty"`
	NameColor string `json:"nc,omitempty"`
	TextColor string `json:"tc,omitempty"`
	Device    string `json:"d,omitempty"`
}

// FromClichat 报告这封邮件是不是 clichat 客户端发出来的。
//
// 提供这个方法而不是让调用方各自判 Clichat != nil：「是不是 clichat
// 消息」的判据只能有一份，否则以后多一种识别方式时必然有一处忘掉。
func (h Header) FromClichat() bool {
	return h.Clichat != nil
}

// Thread 里有没有 clichat 消息。
//
// 问的是「至少有一条」：一个会话可能混着 clichat 发的和网页邮箱发的，
// 列表上要认的是「这个会话里有 clichat 消息」。
func (t Thread) HasClichat() bool {
	for _, m := range t.Messages {
		if m.FromClichat() {
			return true
		}
	}
	return false
}

// Thread 是一次聚合产出的会话。
type Thread struct {
	// ID 是会话的身份，由参与人集合派生（见 conversationKey）。
	//
	// 它取代了旧版的 Root。按参与人聚合之后不存在「线程根」—— 根是
	// 引用链起点的名字，而引用链已经不参与聚合了。
	//
	// ID 与具体消息无关，所以它是稳定的：拉到新邮件、删掉旧邮件都不会
	// 改变它。界面上用它记住「当前打开的是哪个会话」。
	ID string

	// Messages 按 (Date, MessageID) 升序排列。
	Messages []Header

	// Peers 是会话里除自己以外的全部地址，**按地址排序**。
	//
	// 排序而不是按出现顺序：它同时是会话键的一部分，必须与输入顺序
	// 无关。会话里每条消息的参与人集合都等于它（这正是它们被分到
	// 一起的原因）。
	Peers []string

	// Subject 是会话中**最新**那条有主题的消息的 Subject，
	// 已剥掉回复/转发前缀。
	//
	// 用最新那条而不是最早那条：一个会话横跨多个话题时，「最早的
	// 主题」什么都不是，最新那条才代表「现在在聊什么」。
	//
	// 从最后往前找第一条非空的：偶尔会有客户端发出不带主题的回复，
	// 那时候退回上一条比整条会话失去主题强。
	Subject string

	// LastDate 是会话中最新一条消息的时间。
	LastDate time.Time

	// Unread 是会话中未读消息的条数。
	Unread int

	// Starred 是会话中被星标的消息条数。
	//
	// 存条数而不是 bool：界面上要显示「这个会话里有几条星标」，
	// 而 IsStarred() 由它派生，两个信息不会打架。
	Starred int
}

// IsStarred 报告会话里是否至少有一条星标消息。
func (t Thread) IsStarred() bool { return t.Starred > 0 }

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

// ReplyRefs 返回「回复会话里最后一条消息」时该带的引用链：
// 最后一条自己的 References，加上它本身。
//
// ⚠️ 别图省事直接用 MessageIDs()。会话是按参与人聚合的，可以横跨几年、
// 上千封邮件，把它们全塞进 References 会写出一个几十 KB 的头部 ——
// RFC 5322 建议单行不超过 998 字符，很多服务端直接拒收。引用链只该
// 描述这一条消息的祖先，而它的父就是会话里的最后一条。
func (t Thread) ReplyRefs() []string {
	if len(t.Messages) == 0 {
		return nil
	}
	last := t.Messages[len(t.Messages)-1]

	seen := make(map[string]bool, len(last.References)+1)
	out := make([]string, 0, len(last.References)+1)
	for _, id := range last.References {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if last.MessageID != "" && !seen[last.MessageID] {
		out = append(out, last.MessageID)
	}
	return out
}

// NameOf 返回某个地址在会话里出现过的显示名。
//
// 只认「作为发件人时带的显示名」—— Header 只有 FromName，To / Cc 那边
// 不带名字。从最新往前找：对方改过显示名的话，最新的那个才对。
//
// 从来没露过名字就返回空串，由调用方决定拿什么兜底（界面用的是
// 地址 @ 之前的那一段）。
func (t Thread) NameOf(addr string) string {
	addr = NormalizeAddress(addr)
	if addr == "" {
		return ""
	}
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if t.Messages[i].FromName != "" && NormalizeAddress(t.Messages[i].From) == addr {
			return t.Messages[i].FromName
		}
	}
	return ""
}

// IsGroup 判断是否为群聊。
//
// 规则：会话中出现过的地址（不含自己）≥ 2 个即为群聊，
// 等价于「含自己 ≥ 3」。
//
// 注意一个预期行为：一次 CC 了第三人的 1:1 对话会被算作群聊，
// 而且**自成一个会话**、不会并进那两个人和我的私聊里。
// 这是规则的正常结果，不做特例。
func (t Thread) IsGroup() bool {
	return len(t.Peers) >= 2
}

// Aggregate 把邮件头部聚合成会话，按 LastDate 降序返回。
//
// self 是当前账号地址（内部会做小写规范化），用于把「自己」
// 从参与人集合里剔除。
//
// 输出是确定的：同样的输入集合，无论顺序如何，结果都一致。
func Aggregate(headers []Header, self string) []Thread {
	self = NormalizeAddress(self)

	// 先补齐缺失的 Message-ID，再按 ID 去重 —— 这样合成 ID 撞车
	// 的情况也能被去重覆盖到。
	//
	// 顺序有讲究：conversationKey 在参与人为空时会退化成用 Message-ID
	// 当键，所以它必须在合成 ID 之后跑。
	msgs := make([]Header, len(headers))
	copy(msgs, headers)
	for i := range msgs {
		if msgs[i].MessageID == "" {
			msgs[i].MessageID = syntheticID(msgs[i])
		}
	}
	msgs = dedupeByMessageID(msgs)

	// 按会话键分组，同时保持首次出现的顺序 —— 输出确定靠它。
	groups := make(map[string]*group, len(msgs))
	order := make([]*group, 0, len(msgs))
	for i := range msgs {
		key, peers := conversationKey(msgs[i], self)
		g, ok := groups[key]
		if !ok {
			g = &group{key: key, peers: peers}
			groups[key] = g
			order = append(order, g)
		}
		g.idx = append(g.idx, i)
	}

	threads := make([]Thread, 0, len(order))
	for _, g := range order {
		threads = append(threads, buildThread(g, msgs))
	}

	sort.Slice(threads, func(i, j int) bool {
		if threads[i].LastDate.Equal(threads[j].LastDate) {
			return threads[i].ID < threads[j].ID
		}
		return threads[i].LastDate.After(threads[j].LastDate)
	})

	return threads
}

// group 是分组过程中的临时状态：一个会话键、它的参与人集合、命中的消息下标。
type group struct {
	key   string
	peers []string
	idx   []int
}

// conversationKey 算出一条消息的会话键与参与人集合。
//
// 参与人集合 = From ∪ To ∪ Cc − 自己，排序去重。排序是必须的：键是分组
// 依据，「A 发给 B」和「B 发给 A」要算出同一个键，双向语义才成立。
func conversationKey(m Header, self string) (string, []string) {
	seen := make(map[string]bool, len(m.To)+len(m.Cc)+1)
	peers := make([]string, 0, len(m.To)+len(m.Cc)+1)
	add := func(addr string) {
		a := NormalizeAddress(addr)
		if a == "" || a == self || seen[a] {
			return
		}
		seen[a] = true
		peers = append(peers, a)
	}

	add(m.From)
	for _, addr := range m.To {
		add(addr)
	}
	for _, addr := range m.Cc {
		add(addr)
	}
	sort.Strings(peers)

	// 分隔符用 NUL：地址里不可能有它，逗号/空格都可能（带引号的本地部分）。
	key := strings.Join(peers, "\x00")
	if key == "" {
		// 没有别的参与人 —— 自己发给自己，或者头部残缺到地址全空。
		// 这种消息没有「和谁」可言，一条一个会话。硬塞进同一个空键
		// 的会话里，会把互不相干的邮件藏到一起（而且藏得很深：
		// 列表上只显示成一条「(只有你)」）。
		key = "\x01" + m.MessageID
	}
	return key, peers
}

// buildThread 把一组同键的消息组装成一个 Thread。
func buildThread(g *group, msgs []Header) Thread {
	list := make([]Header, len(g.idx))
	for i, k := range g.idx {
		list[i] = msgs[k]
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Date.Equal(list[j].Date) {
			return list[i].MessageID < list[j].MessageID
		}
		return list[i].Date.Before(list[j].Date)
	})

	t := Thread{
		ID:       g.key,
		Messages: list,
		Peers:    g.peers,
		Subject:  latestSubject(list),
		LastDate: list[len(list)-1].Date,
	}

	for _, m := range list {
		if !m.Seen {
			t.Unread++
		}
		if m.Flagged {
			t.Starred++
		}
	}
	return t
}

// latestSubject 取「现在在聊什么」：从最新往前找第一条有主题的消息。
func latestSubject(list []Header) string {
	for i := len(list) - 1; i >= 0; i-- {
		if s := StripSubjectPrefix(list[i].Subject); s != "" {
			return s
		}
	}
	return ""
}

// dedupeByMessageID 去掉 Message-ID 重复的邮件。
//
// 同一封邮件可能同时出现在 INBOX 和 Sent（比如你把自己 CC 了进去），
// 也可能被两个文件夹规则同时命中。保留首次出现的那个，
// 但 Seen 标志按「只要有一份已读就算已读」合并。
//
// Flagged 同理按「任一副本被星标即算星标」合并 —— 否则在 INBOX 里
// 星标了一封同时存在于 Sent 的邮件，下一轮聚合又会把它算成未星标。
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
			if h.Flagged {
				out[i].Flagged = true
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
