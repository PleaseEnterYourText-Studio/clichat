package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// DefaultBodyCacheBytes 是正文缓存默认的字节上限。
//
// 8 MB 是权衡出来的：
//
//   - 太小没意义 —— 这个缓存要解决的问题是「重启后，刚才看过的那些不用
//     重新下载」。一封典型邮件的正文是几 KB，8 MB 装得下两千来封，
//     覆盖「最近翻过的那一段」绰绰有余。
//   - 太大有代价 —— 这份文件在启动时**同步**读进内存（见 OpenBodyCache），
//     8 MB 的 JSON 解析是几十毫秒量级；再往上就会顶到「界面迟迟不出来」。
//
// 上限是按**字节**而不是按条数：正文的长度能差三个数量级（一句回执 vs
// 一整篇带表格的营销邮件），按条数算的话「缓存 500 封」既可能是 1 MB，
// 也可能是 100 MB。
const DefaultBodyCacheBytes = 8 << 20

// MaxBodyEntryBytes 是单封正文允许进缓存的上限，超了就整个不存。
//
// 不是「存下来然后挤掉别人」：一封 512 KB 以上的正文能吃掉大半个预算，
// 为了它淘汰几十封正常邮件不划算。这种邮件本来也罕见，下次打开时重新
// 下载就是了 —— 而它每次打开都要重新下载，正是这里愿意付的代价。
//
// 参考：mail 层单封正文的读取上限是 4 MB（maxBodyBytes）。
const MaxBodyEntryBytes = 512 << 10

// CachedBody 是缓存里的一封正文。
//
// 字段和 app.Body 一一对应，但**不共用类型**：store 是 app 的下层
// （app import store），让它去 import app 就成环了。两边各留一份的代价是
// 加字段时要记得改两处 —— 用一层转换换掉一个环，值得。
type CachedBody struct {
	Text string `json:"text"`
	HTML bool   `json:"html"`

	// Seq 是「最后一次被读到」的逻辑序号，越大越新。
	//
	// 用逻辑序号而不是时间戳：淘汰要的是一个**全序**，而时间戳在
	// 「同一秒里读了好几条」时会打平 —— 打平就意味着淘汰谁由 map 的
	// 遍历顺序决定，同一份数据两次跑出来不一样。序号落盘之后跨重启
	// 继续递增，「哪几封最近看过」照样活着（这正是纯内存 LRU 做不到的：
	// 它每次启动都从零开始，于是重启后最先被淘汰的恰好是上次刚看过的）。
	Seq int64 `json:"seq"`
}

// BodyCache 是按 Message-ID 索引的正文缓存，带字节上限和 LRU 淘汰。
//
// 它和 Index 是两份**独立**的文件，这是有意的：index.json 每次同步都会
// 重写（147 KB 量级），而这份是几 MB —— 混在一起等于每次同步都重写几 MB。
//
// 两者的可丢弃性也完全不同：index.json 是唯一的一份数据，坏了就没了；
// 这份只是缓存，坏了重新下载就能补回来。所以这里对文件损坏的处理是
// 「丢掉重来」，而不是像 Index 那样把错误抛给调用方。
type BodyCache struct {
	Entries map[string]CachedBody `json:"entries"`

	mu    sync.Mutex
	path  string
	limit int
	// bytes 是 Entries 里 Text 的字节总数。
	//
	// 内存里维护而不是每次现算：淘汰要在每次 Put 之后判断，而现算是
	// O(条数)；条数上万时那一遍扫描比淘汰本身还贵。
	bytes int
	// seq 是逻辑时钟，见 CachedBody.Seq。
	seq int64
	// dirty 表示内存里的内容和磁盘不一致，需要写回。
	dirty bool
}

// OpenBodyCache 打开（或新建）正文缓存。
//
// ⚠️ **它不会失败**，这是刻意的：这是一份可丢弃的缓存，任何读不了的情况
// 都只是「这次启动慢一点」，不该让整个程序起不来。文件损坏、权限不足、
// 半截文件，一律退化成空缓存并留一条日志。
//
// 打开时会顺手做两件事，都是为了「上限改了以后不用等下次写入才生效」：
// 重算字节数（不信文件里记的），然后按当前上限淘汰一遍。
func OpenBodyCache(path string, limit int) *BodyCache {
	c := &BodyCache{
		Entries: map[string]CachedBody{},
		path:    path,
		limit:   limit,
	}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// 第一次运行，正常。
		return c
	case err != nil:
		log.Printf("正文缓存读不了（这次不带缓存跑）: %v", err)
		return c
	}
	if err := json.Unmarshal(data, c); err != nil {
		log.Printf("正文缓存解析失败（丢掉重来）: %v", err)
		c.Entries = map[string]CachedBody{}
		return c
	}
	if c.Entries == nil {
		c.Entries = map[string]CachedBody{}
	}

	// 重算字节数、丢掉超大的单条、然后按当前上限淘汰。
	//
	// 重算而不是信文件里的数字：那个数字是上一次运行写的，而上限可能
	// 变小了（改了常量、或者将来做成配置）—— 信它的话缓存会一直超着，
	// 直到下一次 Put 才发现。
	for id, b := range c.Entries {
		if len(b.Text) > MaxBodyEntryBytes {
			delete(c.Entries, id)
			continue
		}
		c.bytes += len(b.Text)
		if b.Seq > c.seq {
			c.seq = b.Seq
		}
	}
	c.evictLocked()
	return c
}

// Get 取一封正文，并把它标成「刚用过」。
//
// ⚠️ **空正文也算命中。** 「拉过、但这一封就是没有正文」是一个结论，
// 它必须能被记住 —— 否则每次滚动列表都会把那些没有正文的邮件重新拉一遍，
// 而它们永远也不会有正文。app 那边正是靠这个约定把「问过了」和
// 「没问过」分开的（见 app.LastBodyTexts）。
func (c *BodyCache) Get(id string) (CachedBody, bool) {
	if id == "" {
		return CachedBody{}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	b, ok := c.Entries[id]
	if !ok {
		return CachedBody{}, false
	}
	// 已经是最新的就不用再动 —— 免得白置一次 dirty。
	if b.Seq != c.seq {
		c.seq++
		b.Seq = c.seq
		c.Entries[id] = b
		c.dirty = true
	}
	return b, true
}

// Put 存一封正文。
//
// 空 Message-ID 不存：真实邮件都有，没有的那些是畸形件，而拿空串当键等于
// 让所有畸形件共用同一个槽位 —— 于是 A 的正文会被当成 B 的正文返回。
func (c *BodyCache) Put(id string, b CachedBody) {
	if id == "" || len(b.Text) > MaxBodyEntryBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if old, ok := c.Entries[id]; ok {
		c.bytes -= len(old.Text)
	}
	c.seq++
	b.Seq = c.seq
	c.Entries[id] = b
	c.bytes += len(b.Text)
	c.dirty = true
	c.evictLocked()
}

// Len 返回缓存里的条数。
func (c *BodyCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.Entries)
}

// Bytes 返回缓存里正文的字节总数。
func (c *BodyCache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// evictLocked 淘汰到字节数不超上限为止。调用方必须已持有 mu。
//
// 按 Seq 从小到大丢，也就是先丢最久没被读到的。
func (c *BodyCache) evictLocked() {
	if c.bytes <= c.limit {
		return
	}

	ids := make([]string, 0, len(c.Entries))
	for id := range c.Entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		si, sj := c.Entries[ids[i]].Seq, c.Entries[ids[j]].Seq
		if si != sj {
			return si < sj
		}
		// 序号打平时按 id 定序。正常情况下不会打平（序号来自递增计数器），
		// 但从旧文件读进来的条目可能全是 0 —— 那时排序必须仍然是个全序，
		// 否则同一份数据两次跑出来的淘汰结果不一样。
		return ids[i] < ids[j]
	})

	for _, id := range ids {
		if c.bytes <= c.limit {
			break
		}
		c.bytes -= len(c.Entries[id].Text)
		delete(c.Entries, id)
		c.dirty = true
	}
}

// Save 把缓存写回磁盘。没有改动时是空操作。
//
// 写盘时机是**退出时**（见 app.Close），不是每次 Put —— 这份文件有 MB 级，
// 每存一封正文就重写一遍等于把「取正文省下的时间」原样还回去。
// 代价说清楚：进程被强杀（而不是正常退出）时，这一次会话新拉的正文
// 不会留下；缓存本来就是可丢弃的，那只是下次重新下载。
func (c *BodyCache) Save() error {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	data, err := json.Marshal(c)
	savedSeq := c.seq
	path := c.path
	c.mu.Unlock()

	if err != nil {
		return fmt.Errorf("序列化正文缓存失败: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建正文缓存目录失败: %w", err)
	}

	// 先写临时文件再原子改名，和 Index.Save 同一个理由：中途崩了不会留下
	// 一个半截的缓存文件。这里坏了只是多一条启动日志，但少一条是一条。
	tmp, err := os.CreateTemp(dir, "bodies-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时正文缓存失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 改名成功后这次删除是无害的空操作

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入正文缓存失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("刷盘正文缓存失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时正文缓存失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("设置正文缓存权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换正文缓存失败: %w", err)
	}

	c.mu.Lock()
	// 只有在落盘期间没有新的读/写时才清标记 —— 否则那一次改动就被吞了
	// （下次 Save 会因为 dirty 为假而跳过，缓存就永远停在旧内容上）。
	if c.seq == savedSeq {
		c.dirty = false
	}
	c.mu.Unlock()
	return nil
}
