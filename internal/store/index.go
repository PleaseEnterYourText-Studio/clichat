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
			ix.Headers[i].Seen = true
		}
	}
}

// Reset 清空索引。服务器重置 UID 空间时用。
func (ix *Index) Reset() {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	ix.Headers = nil
	ix.Folders = map[string]FolderState{}
	ix.pos = map[string]int{}
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
