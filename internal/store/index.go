// Package store 负责 header 索引的本地持久化。
//
// v1 刻意不引入数据库。索引在内存里是一张切片加一张位置表，退出时
// 序列化成一个 JSON 文件。5000 条头部的量级，JSON 完全够用。
// 真要涨到 5 万条以上再换 bbolt 不迟 —— 现在换就是过早优化。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

// FolderState 记录一个文件夹的同步游标。
type FolderState struct {
	// UIDValidity 是 IMAP 给这个文件夹的世代号。它一变就说明服务器
	// 重置了 UID 空间，本地索引必须整个丢掉重来。
	UIDValidity uint32 `json:"uid_validity"`
	// LastUID 是我们已经同步过的最大 UID。
	LastUID uint32 `json:"last_uid"`
	// Messages 是上一次看到的**服务端**总封数。
	//
	// 它不是同步状态的一部分，而是「服务端那边有没有少东西」的信号：
	// 增量同步只往后拉（from = LastUID+1），服务端删掉一封本地永远不知道。
	// 封数下降说明服务端上确实少了东西，此时才值得花一次对账
	// （列出全部 UID，见 app.syncFolder）—— 那一次要传回整个文件夹的
	// UID 表，不能每轮都做。
	//
	// 0 表示「还没观察过」，此时不做对账。从旧版本升上来的索引就是这个
	// 样子：第一轮先把真实封数记下来，之后才谈得上「下降了」。
	Messages uint32 `json:"messages"`
}

// Index 是本地 header 索引。
type Index struct {
	Headers []mail.Header          `json:"headers"`
	Folders map[string]FolderState `json:"folders"`

	mu   sync.Mutex
	path string
	pos  map[string]int // Message-ID -> Headers 里的下标
}

// Open 读取索引文件。文件不存在时返回一个空索引且不报错 ——
// 首次启动就该是这个样子。
func Open(path string) (*Index, error) {
	ix := &Index{
		Folders: map[string]FolderState{},
		path:    path,
		pos:     map[string]int{},
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ix, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取索引失败: %w", err)
	}
	if err := json.Unmarshal(data, ix); err != nil {
		return nil, fmt.Errorf("解析索引失败: %w", err)
	}
	if ix.Folders == nil {
		ix.Folders = map[string]FolderState{}
	}
	ix.rebuildPos()
	return ix, nil
}

// rebuildPos 依据当前 Headers 重建 Message-ID 位置表。
func (ix *Index) rebuildPos() {
	ix.pos = make(map[string]int, len(ix.Headers))
	for i, h := range ix.Headers {
		if h.MessageID == "" {
			continue
		}
		if _, dup := ix.pos[h.MessageID]; !dup {
			ix.pos[h.MessageID] = i
		}
	}
}

// Merge 把新拉到的头部并进索引。
//
// 返回 added（真正新增的条数）和 updated（命中已有条目、但把它补全了的条数）。
// 两个数必须分开报：added 会进日志、还被用来判断「服务端说有信却一封都没
// 进索引」这个诊断（见 app.syncFolder），把补全也算进去会让那条诊断说谎。
// 而 updated 是「索引内容变了」的信号，界面要靠它决定重不重绘。
//
// 去重按 Message-ID。同一封邮件可能同时出现在 INBOX 和 Sent
// （比如你把自己 CC 了进去），只保留先到的那个。
//
// ⚠️ 命中已有条目时**要把 UID 补上**。本地发出的一封信是先以「乐观副本」
// 进来的（见 app.send：发送成功就插一条 UID=0 的记录，免得用户盯着空会话
// 等一轮轮询）。服务端那份同步回来时 Message-ID 相同，于是走到下面这条
// 「已经见过」的分支 —— 早先这里只合并已读标志就 continue 了，UID 永远是 0。
// 后果有两个，都是用户看得见的：
//
//   - 界面按 UID == 0 判「本地待同步」（见 tui.renderMessage），于是**每一封
//     自己发过的信，每次重启都还挂着那个标记**，而且正文也拉不下来
//     （app.Bodies 对 UID==0 直接跳过）。
//   - 所有按 (文件夹, UID) 定位的操作 —— 标已读 / 星标 / 移动 —— 都找不到它。
func (ix *Index) Merge(headers []mail.Header) (added, updated int) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	for _, h := range headers {
		if h.MessageID != "" {
			if p, ok := ix.pos[h.MessageID]; ok {
				// 已经见过。把已读标志并进来 —— 任一副本已读即算已读。
				if h.Seen && !ix.Headers[p].Seen {
					ix.Headers[p].Seen = true
					updated++
				}
				// 只补不覆盖：已经有 UID 的那一份才是权威的（同一封邮件被
				// 重复拉到时不该把 UID 换掉）。文件夹要跟着 UID 一起补 ——
				// 乐观副本把 Folder 写死成 "Sent"，而服务端把它归在哪儿
				// 只有它自己知道；两者对不上的话，按 (文件夹, UID) 的定位
				// 照样会落空。
				if ix.Headers[p].UID == 0 && h.UID != 0 {
					ix.Headers[p].UID = h.UID
					ix.Headers[p].Folder = h.Folder
					updated++
				}
				continue
			}
			ix.pos[h.MessageID] = len(ix.Headers)
		}
		ix.Headers = append(ix.Headers, h)
		added++
	}

	// 自愈：同一 (文件夹, UID) 只留一条。
	//
	// 必须放在最后，而且**不能省**：没有 Message-ID 的邮件在上面那个循环里
	// 没法按 Message-ID 命中，只能一路 append。取头那条路已经修了（见
	// mail.applyRawHeaders），但已经写进 index.json 的重复还在，而且它们
	// 每一条的 UID 在服务端都还活着 —— RemoveMissing 一个都不会删。
	if m := ix.dedupeLocked(); m > 0 {
		// 被合掉的那些**不算新增**：它们本来就在索引里（只是当时没有
		// Message-ID 没法按它去重）。不扣掉的话，每次同步都会报「新增 N 条」
		// 并把界面白刷一遍，而实际上一封新邮件都没有。
		added -= m
		if added < 0 {
			added = 0
		}
	}
	return added, updated
}

// dedupeLocked 把「同一个 (文件夹, UID) 出现多次」的条目合成一条，返回合掉的
// 条数。调用方必须已持有 mu。
//
// # 凭什么敢删
//
// **(文件夹, UID) 就是「服务端上哪一封」的身份**，同一个身份出现两次一定是错的。
// 2026-10-03 实测：126 的收件箱 196 封里有 13 封的 ENVELOPE 没有 Message-ID
// （原始头里有），而 Merge 只按 Message-ID 去重 —— 于是每同步一轮就追加一份，
// 攒到 86 条重复，索引 311 条而服务端只有 243 封。
//
// 合并规则：
//   - 标志取**或**（任一副本已读即算已读，别把用户标过的已读弄丢）；
//   - Message-ID 优先留**非空**的那个 —— 空的那个正是要被修掉的东西，
//     补上之后下一轮 Merge 就能按它正常去重了。
//
// ⚠️ UID == 0 的不参与：那是本地刚发出、还没同步回来的乐观副本，它们本来
// 就没有服务端身份，共用 (Sent, 0) 是正常的（见 RemoveMissing 的同一条注释）。
func (ix *Index) dedupeLocked() int {
	type key struct {
		folder string
		uid    uint32
	}

	at := make(map[key]int, len(ix.Headers))
	kept := ix.Headers[:0]
	merged := 0

	for _, h := range ix.Headers {
		if h.UID == 0 {
			kept = append(kept, h)
			continue
		}
		k := key{folder: h.Folder, uid: h.UID}
		p, dup := at[k]
		if !dup {
			at[k] = len(kept)
			kept = append(kept, h)
			continue
		}
		merged++
		dst := &kept[p]
		dst.Seen = dst.Seen || h.Seen
		dst.Flagged = dst.Flagged || h.Flagged
		if dst.MessageID == "" && h.MessageID != "" {
			dst.MessageID = h.MessageID
			dst.InReplyTo = h.InReplyTo
			dst.References = h.References
		}
	}

	if merged == 0 {
		return 0
	}
	ix.Headers = kept
	ix.rebuildPos()
	return merged
}

// All 返回索引里的全部头部（副本）。
func (ix *Index) All() []mail.Header {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	out := make([]mail.Header, len(ix.Headers))
	copy(out, ix.Headers)
	return out
}

// Len 返回索引里的邮件条数。
func (ix *Index) Len() int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return len(ix.Headers)
}

// FolderState 返回某个文件夹的同步游标。
func (ix *Index) FolderState(name string) (FolderState, bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	st, ok := ix.Folders[name]
	return st, ok
}

// SetFolderState 记录某个文件夹的同步游标。
func (ix *Index) SetFolderState(name string, st FolderState) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.Folders == nil {
		ix.Folders = map[string]FolderState{}
	}
	ix.Folders[name] = st
}

// MarkSeen 把本地索引里对应邮件标为已读。
//
// 服务端的已读状态由 mail.Client.MarkSeen 负责，这里同步本地副本 ——
// 否则刚点开的会话在下次聚合时又会被算成未读。
func (ix *Index) MarkSeen(folder string, uids []uint32) {
	ix.eachUID(folder, uids, func(h *mail.Header) { h.Seen = true })
}

// MarkUnread 把本地索引里对应邮件标回未读。
func (ix *Index) MarkUnread(folder string, uids []uint32) {
	ix.eachUID(folder, uids, func(h *mail.Header) { h.Seen = false })
}

// SetFlagged 设置本地索引里对应邮件的星标状态。
func (ix *Index) SetFlagged(folder string, uids []uint32, on bool) {
	ix.eachUID(folder, uids, func(h *mail.Header) { h.Flagged = on })
}

// eachUID 对 (folder, uids) 命中的每条 header 跑一次 mutate。
//
// 三个标记操作（已读 / 未读 / 星标）的命中逻辑完全一样，只有改动内容
// 不同，所以抽出来 —— 复制三遍的话，以后改命中规则必然漏改其中一两处。
func (ix *Index) eachUID(folder string, uids []uint32, mutate func(*mail.Header)) {
	if len(uids) == 0 {
		return
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()

	want := make(map[uint32]bool, len(uids))
	for _, u := range uids {
		want[u] = true
	}
	for i := range ix.Headers {
		if ix.Headers[i].Folder == folder && want[ix.Headers[i].UID] {
			mutate(&ix.Headers[i])
		}
	}
}

// Remove 按 Message-ID 从索引里删掉若干邮件，返回实际删掉的条数。
//
// 界面上说的「删除」是移到已删除文件夹，服务端那份还在。但本地这份
// 必须先移出去 —— 它在原文件夹的 UID 已经被 MOVE 走了，本地索引却还
// 记着，不删的话下一轮聚合又会把它显示回列表里。
func (ix *Index) Remove(messageIDs []string) int {
	if len(messageIDs) == 0 {
		return 0
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()

	drop := make(map[string]bool, len(messageIDs))
	for _, id := range messageIDs {
		drop[id] = true
	}

	kept := ix.Headers[:0]
	removed := 0
	for _, h := range ix.Headers {
		if h.MessageID != "" && drop[h.MessageID] {
			removed++
			continue
		}
		kept = append(kept, h)
	}
	ix.Headers = kept
	ix.rebuildPos()
	return removed
}

// RemoveMissing 删掉 folder 里 UID **不在 alive 中**的条目，返回删掉的条数。
//
// alive 是服务端现在实际存在的 UID 表（见 mail.Client.UIDs）。它和 Remove
// 是两件事：Remove 是「我刚把这几封移走了」，这个是「服务端说这几封没了」。
//
// ⚠️ **UID == 0 的条目不参与对账。** 那是本地刚发出、还没同步回来的乐观
// 副本（见 app.send），它在服务端上本来就还没有 UID —— 按「不在 alive 里」
// 删掉，用户刚发出去的那封信会从列表里凭空消失，而且没有任何错误提示。
//
// ⚠️ 只在**同一个文件夹内**比。UID 只在文件夹内唯一，拿收件箱的 UID 表去
// 对已发送的条目会把两个文件夹全删光。条目上的 Folder 就是当初写进去的
// 那个规范名（见 headerFromMessage），所以这里比的是同一个坐标系。
//
// alive 为空表示服务端上这个文件夹**真的空了**，此时该文件夹的非零 UID
// 条目全部删掉 —— 那是合法状态（用户把信全删了），不是故障。
func (ix *Index) RemoveMissing(folder string, alive []uint32) int {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	keep := make(map[uint32]bool, len(alive))
	for _, u := range alive {
		keep[u] = true
	}

	kept := ix.Headers[:0]
	removed := 0
	for _, h := range ix.Headers {
		if h.Folder == folder && h.UID != 0 && !keep[h.UID] {
			removed++
			continue
		}
		kept = append(kept, h)
	}
	ix.Headers = kept
	ix.rebuildPos()
	return removed
}

// CountFolder 返回某个文件夹里有 UID 的条数。
//
// ⚠️ **UID == 0 的乐观副本不算**：它们在服务端上还没有身份，拿它们去和
// 「服务端上有几封」比，差值永远对不上（本地会比服务端多，而那正是
// 「本地有幽灵」的反面，会让人往错的方向查）。
func (ix *Index) CountFolder(folder string) int {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	n := 0
	for _, h := range ix.Headers {
		if h.Folder == folder && h.UID != 0 {
			n++
		}
	}
	return n
}

// MissingMessageIDRange 返回某个文件夹里 **Message-ID 为空**的条目的 UID
// 区间 [lo, hi]（含）。没有这样的条目时 ok=false。
//
// 存在的理由是「一次性自愈」：取头那条路 2026-10-03 才改成从原始头取
// Message-ID（见 mail.applyRawHeaders），在那之前同步进来的条目 Message-ID
// 是空的 —— 它们不能按 Message-ID 去重，也没法串会话。这些 UID 都在游标
// 之下，正常增量同步永远不会再取到它们，只能照这个区间主动补一次。
//
// ⚠️ 返回的是**区间**不是集合：`Client.Headers` 只认区间。调用方必须自己
// 限制跨度（见 app.maxHealRangeSpan）—— 区间太宽会顺手把整个文件夹拉下来。
func (ix *Index) MissingMessageIDRange(folder string) (lo, hi uint32, ok bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	for _, h := range ix.Headers {
		if h.Folder != folder || h.UID == 0 || h.MessageID != "" {
			continue
		}
		if !ok || h.UID < lo {
			lo = h.UID
		}
		if !ok || h.UID > hi {
			hi = h.UID
		}
		ok = true
	}
	return lo, hi, ok
}

// Reset 清空索引。服务器重置 UID 空间时用。
func (ix *Index) Reset() {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	ix.Headers = nil
	ix.Folders = map[string]FolderState{}
	ix.pos = map[string]int{}
}

// Rewind 把每个文件夹的同步游标退回起点（LastUID 归零），保留 UIDValidity。
//
// 用途是「接收全部邮件」：光把配置开关打开还不够。已经同步过的文件夹，
// 本地游标停在半路，下一轮同步只会从 LastUID+1 往后拉 —— 用户按了开关
// 却什么都没变，那是最糟的一种「成功」。
//
// UIDValidity 必须留着。清成零的话，下一轮同步会认为服务端重置了 UID
// 空间，走 Reset() 那条更贵的路径把整个索引删掉重来，用户已经标过的
// 已读/星标会一起丢。
func (ix *Index) Rewind() {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	for name, st := range ix.Folders {
		if st.LastUID == 0 {
			continue
		}
		st.LastUID = 0
		ix.Folders[name] = st
	}
}

// Save 把索引写回磁盘。
//
// 先写临时文件再原子改名：中途崩了不会留下一个半截的索引文件，
// 那比没有索引更糟 —— 下次启动会解析失败。
func (ix *Index) Save() error {
	ix.mu.Lock()
	data, err := json.Marshal(ix)
	path := ix.path
	ix.mu.Unlock()

	if err != nil {
		return fmt.Errorf("序列化索引失败: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建索引目录失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "index-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时索引文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 改名成功后这次删除是无害的空操作

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入索引失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("刷盘索引失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时索引文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("设置索引权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换索引文件失败: %w", err)
	}
	return nil
}
