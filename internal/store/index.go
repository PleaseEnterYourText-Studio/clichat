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

// Merge 把新拉到的头部并进索引，返回真正新增的条数。
//
// 去重按 Message-ID。同一封邮件可能同时出现在 INBOX 和 Sent
// （比如你把自己 CC 了进去），只保留先到的那个。
func (ix *Index) Merge(headers []mail.Header) int {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	added := 0
	for _, h := range headers {
		if h.MessageID != "" {
			if p, ok := ix.pos[h.MessageID]; ok {
				// 已经见过。把已读标志并进来 —— 任一副本已读即算已读。
				if h.Seen {
					ix.Headers[p].Seen = true
				}
				continue
			}
			ix.pos[h.MessageID] = len(ix.Headers)
		}
		ix.Headers = append(ix.Headers, h)
		added++
	}
	return added
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
