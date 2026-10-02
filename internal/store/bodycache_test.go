package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newCache 在临时目录里开一个上限为 limit 的缓存。
func newCache(t *testing.T, limit int) (*BodyCache, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bodies.json")
	return OpenBodyCache(path, limit), path
}

// 存进去、重启之后还在。这是这个缓存的**全部目的**：正文原本只在内存里，
// 一关就没了，于是每次启动都要把眼前那一屏重新下载一遍。
func TestBodyCache_SurvivesRestart(t *testing.T) {
	c, path := newCache(t, DefaultBodyCacheBytes)
	c.Put("<a@x>", CachedBody{Text: "甲", HTML: true})
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	again := OpenBodyCache(path, DefaultBodyCacheBytes)
	got, ok := again.Get("<a@x>")
	if !ok {
		t.Fatalf("重启后缓存是空的：%d 条", again.Len())
	}
	if got.Text != "甲" || !got.HTML {
		t.Errorf("读回来的是 %+v，想要 {甲 true}", got)
	}
}

// 上限是**字节**，不是条数：正文长度能差三个数量级，按条数算的
// 「缓存 500 封」既可能是 1 MB 也可能是 100 MB。
func TestBodyCache_EvictsByBytes(t *testing.T) {
	// 每条 100 字节，上限 250 → 最多装两条。
	body := strings.Repeat("x", 100)
	c, _ := newCache(t, 250)

	for _, id := range []string{"<a@x>", "<b@x>", "<c@x>"} {
		c.Put(id, CachedBody{Text: body})
	}

	if c.Bytes() > 250 {
		t.Errorf("字节数 %d 超了上限 250", c.Bytes())
	}
	if c.Len() != 2 {
		t.Errorf("留下了 %d 条，想要 2", c.Len())
	}
}

// 淘汰按「最久没被读到」来，不是按「最早写进来」。
//
// 这条区分才是 LRU 的意义所在：列表副行会反复读同一批会话，按写入时间
// 淘汰的话，你天天看的那几个会话反而会先被丢掉。
func TestBodyCache_EvictsLeastRecentlyUsedNotLeastRecentlyAdded(t *testing.T) {
	body := strings.Repeat("x", 100)
	c, _ := newCache(t, 250)

	c.Put("<old@x>", CachedBody{Text: body}) // 最早写进来
	c.Put("<mid@x>", CachedBody{Text: body})
	// 再读一次最早那条 —— 它现在是「最近用过的」。
	if _, ok := c.Get("<old@x>"); !ok {
		t.Fatal("前提不成立：<old@x> 没存进去")
	}
	// 再存一条，必须淘汰 <mid@x>（最久没用过），而不是 <old@x>。
	c.Put("<new@x>", CachedBody{Text: body})

	if _, ok := c.Get("<old@x>"); !ok {
		t.Error("最近读过的那条被淘汰了 —— 淘汰用的是写入时间，不是使用时间")
	}
	if _, ok := c.Get("<mid@x>"); ok {
		t.Error("最久没用的那条还在")
	}
}

// 「最近用过」这件事必须跨重启活着。
//
// 纯内存 LRU 每次启动都从零开始，于是重启后最先被淘汰的恰好是上次刚看过
// 的那些 —— 正好和意图相反。所以顺序号是**落盘**的。
func TestBodyCache_RecencySurvivesRestart(t *testing.T) {
	body := strings.Repeat("x", 100)

	c, path := newCache(t, 250)
	c.Put("<old@x>", CachedBody{Text: body})
	c.Put("<mid@x>", CachedBody{Text: body})
	c.Get("<old@x>") // 最早写、最近读
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	again := OpenBodyCache(path, 250)
	again.Put("<new@x>", CachedBody{Text: body})

	if _, ok := again.Get("<old@x>"); !ok {
		t.Error("重启后「最近用过」丢了 —— 顺序号没有落盘")
	}
	if _, ok := again.Get("<mid@x>"); ok {
		t.Error("该淘汰的那条没被淘汰")
	}
}

// 单封超大的不存，而且**不能顺手把别人挤掉**。
//
// 一封 512 KB 以上的正文能吃掉大半个预算，为了它淘汰几十封正常邮件不划算。
func TestBodyCache_SkipsOversizedEntryWithoutEvictingOthers(t *testing.T) {
	c, _ := newCache(t, 1<<20)
	c.Put("<small@x>", CachedBody{Text: "甲"})

	c.Put("<huge@x>", CachedBody{Text: strings.Repeat("x", MaxBodyEntryBytes+1)})

	if _, ok := c.Get("<huge@x>"); ok {
		t.Error("超大的那条被存进来了")
	}
	if _, ok := c.Get("<small@x>"); !ok {
		t.Error("超大条目把正常条目挤掉了")
	}
}

// 「拉过、但这一封就是没有正文」必须能被记住。
//
// 少了这条，每次滚动列表都会把那些没有正文的邮件重新拉一遍，而它们
// 永远也不会有正文 —— 这是一个**不会自己消失**的重复请求。
func TestBodyCache_EmptyBodyIsStillAHit(t *testing.T) {
	c, _ := newCache(t, DefaultBodyCacheBytes)
	c.Put("<empty@x>", CachedBody{})

	got, ok := c.Get("<empty@x>")
	if !ok {
		t.Fatal("空正文没被记住 —— 没有正文的邮件会被反复重拉")
	}
	if got.Text != "" {
		t.Errorf("读回来的是 %q", got.Text)
	}
}

// 空 Message-ID 不存：拿空串当键等于让所有畸形件共用同一个槽位，
// 于是 A 的正文会被当成 B 的正文返回。
func TestBodyCache_RefusesEmptyID(t *testing.T) {
	c, _ := newCache(t, DefaultBodyCacheBytes)
	c.Put("", CachedBody{Text: "甲的正文"})

	if _, ok := c.Get(""); ok {
		t.Error("空 Message-ID 被当成了合法键")
	}
	if c.Len() != 0 {
		t.Errorf("缓存里有 %d 条", c.Len())
	}
}

// 缓存坏了不该让程序起不来，也不该报错 —— 它只是缓存，重新下载就能补回来。
//
// 这一点和 index.json 正相反：那是唯一的一份数据，解析失败必须让调用方
// 知道（见 Index.Open）。两条路的差别写在 BodyCache 的文档里。
func TestBodyCache_CorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bodies.json")
	if err := os.WriteFile(path, []byte("{ 这不是 JSON"), 0o600); err != nil {
		t.Fatalf("造坏文件: %v", err)
	}

	c := OpenBodyCache(path, DefaultBodyCacheBytes)
	if c.Len() != 0 {
		t.Errorf("坏文件读出了 %d 条", c.Len())
	}
	// 坏掉之后还能正常用。
	c.Put("<a@x>", CachedBody{Text: "甲"})
	if _, ok := c.Get("<a@x>"); !ok {
		t.Error("坏文件之后缓存不能用了")
	}
}

// 上限调小了，打开时就该按新上限收拾干净，而不是等下一次写入才发现。
func TestBodyCache_LoweredLimitTakesEffectOnOpen(t *testing.T) {
	body := strings.Repeat("x", 100)
	path := filepath.Join(t.TempDir(), "bodies.json")

	big := OpenBodyCache(path, 1<<20)
	for _, id := range []string{"<a@x>", "<b@x>", "<c@x>", "<d@x>"} {
		big.Put(id, CachedBody{Text: body})
	}
	if err := big.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	small := OpenBodyCache(path, 250)
	if small.Bytes() > 250 {
		t.Errorf("按新上限打开后还有 %d 字节，超了 250", small.Bytes())
	}
}

// 没有改动就不该碰磁盘。
func TestBodyCache_SaveIsNoOpWhenNothingChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bodies.json")
	c := OpenBodyCache(path, DefaultBodyCacheBytes)

	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("没有任何改动却写了文件（err=%v）", err)
	}
}
