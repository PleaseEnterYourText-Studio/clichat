package store

import (
	"path/filepath"
	"testing"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
)

func newIndex(t *testing.T) (*Index, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.json")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return ix, path
}

func hdr(id string, uid uint32, seen bool) mail.Header {
	return mail.Header{
		MessageID: id, UID: uid, Folder: "INBOX", Seen: seen,
		From: "a@x.com", To: []string{"me@example.com"},
	}
}

func TestIndex_MergeDedupesByMessageID(t *testing.T) {
	ix, _ := newIndex(t)

	if got := ix.Merge([]mail.Header{hdr("<a@x>", 1, false)}); got != 1 {
		t.Errorf("首次 Merge 应新增 1 条, got %d", got)
	}
	if got := ix.Merge([]mail.Header{hdr("<a@x>", 1, false)}); got != 0 {
		t.Errorf("重复 Merge 不该新增, got %d", got)
	}
	if got := ix.Len(); got != 1 {
		t.Errorf("Len = %d, want 1", got)
	}
}

// 同一封邮件在 INBOX 和 Sent 各有一份时，任一份已读即算已读。
func TestIndex_MergeMergesSeenFlag(t *testing.T) {
	ix, _ := newIndex(t)

	ix.Merge([]mail.Header{hdr("<a@x>", 1, false)})
	ix.Merge([]mail.Header{hdr("<a@x>", 1, true)})

	all := ix.All()
	if len(all) != 1 {
		t.Fatalf("Len = %d, want 1", len(all))
	}
	if !all[0].Seen {
		t.Error("后到的已读标志没有被合并进来")
	}
}

func TestIndex_PersistsAcrossOpen(t *testing.T) {
	ix, path := newIndex(t)

	ix.Merge([]mail.Header{hdr("<a@x>", 1, false), hdr("<b@x>", 2, false)})
	ix.SetFolderState("INBOX", FolderState{UIDValidity: 7, LastUID: 2})
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	if got := reopened.Len(); got != 2 {
		t.Errorf("重新打开后 Len = %d, want 2", got)
	}
	st, ok := reopened.FolderState("INBOX")
	if !ok || st.UIDValidity != 7 || st.LastUID != 2 {
		t.Errorf("同步游标没持久化: %+v ok=%v", st, ok)
	}

	// 重建的位置表必须能用，否则去重会失效。
	if got := reopened.Merge([]mail.Header{hdr("<a@x>", 1, false)}); got != 0 {
		t.Errorf("重新打开后去重失效, 新增了 %d 条", got)
	}
}

func TestIndex_MarkSeen(t *testing.T) {
	ix, _ := newIndex(t)

	ix.Merge([]mail.Header{
		hdr("<a@x>", 1, false),
		hdr("<b@x>", 2, false),
	})
	ix.MarkSeen("INBOX", []uint32{1})

	byID := map[string]bool{}
	for _, h := range ix.All() {
		byID[h.MessageID] = h.Seen
	}
	if !byID["<a@x>"] {
		t.Error("UID 1 应被标为已读")
	}
	if byID["<b@x>"] {
		t.Error("UID 2 不该被标为已读")
	}
}

// 文件夹名不匹配时不该误标。
func TestIndex_MarkSeenRespectsFolder(t *testing.T) {
	ix, _ := newIndex(t)

	sent := hdr("<a@x>", 1, false)
	sent.Folder = "Sent"
	ix.Merge([]mail.Header{sent})

	ix.MarkSeen("INBOX", []uint32{1})

	if ix.All()[0].Seen {
		t.Error("标的是 INBOX，却把 Sent 里的同 UID 邮件标了")
	}
}

func TestIndex_Reset(t *testing.T) {
	ix, _ := newIndex(t)

	ix.Merge([]mail.Header{hdr("<a@x>", 1, false)})
	ix.SetFolderState("INBOX", FolderState{UIDValidity: 1, LastUID: 1})

	ix.Reset()

	if got := ix.Len(); got != 0 {
		t.Errorf("Reset 后 Len = %d, want 0", got)
	}
	if _, ok := ix.FolderState("INBOX"); ok {
		t.Error("Reset 后同步游标应被清掉")
	}
	// Reset 之后旧的 Message-ID 不该再挡着新数据。
	if got := ix.Merge([]mail.Header{hdr("<a@x>", 1, false)}); got != 1 {
		t.Errorf("Reset 后重新 Merge 应新增 1 条, got %d", got)
	}
}

func TestOpen_MissingFileIsNotAnError(t *testing.T) {
	ix, err := Open(filepath.Join(t.TempDir(), "nope", "index.json"))
	if err != nil {
		t.Fatalf("文件不存在不该报错: %v", err)
	}
	if ix.Len() != 0 {
		t.Error("应该是空索引")
	}
}

// Rewind 是「接收全部邮件」的支点。它必须只把游标退回去，
// 不能顺手把 UIDValidity 也抹掉 —— 那会让下一轮同步以为服务器
// 重置了 UID 空间，走 Reset 把整个索引删掉重来，用户标过的已读和
// 星标会一起丢。
func TestIndex_RewindKeepsValidityAndHeaders(t *testing.T) {
	ix, _ := newIndex(t)

	ix.Merge([]mail.Header{hdr("<a@x>", 7, true)})
	ix.SetFolderState("INBOX", FolderState{UIDValidity: 42, LastUID: 7})

	ix.Rewind()

	st, ok := ix.FolderState("INBOX")
	if !ok {
		t.Fatal("Rewind 不该把文件夹状态整条删掉")
	}
	if st.LastUID != 0 {
		t.Errorf("LastUID = %d, want 0", st.LastUID)
	}
	if st.UIDValidity != 42 {
		t.Errorf("UIDValidity 被改成了 %d —— 下一轮同步会因此清空索引", st.UIDValidity)
	}
	// 已经同步下来的邮件必须留着：Rewind 说的是「再拉一遍」，
	// 不是「删了重来」。
	if ix.Len() != 1 {
		t.Errorf("Rewind 把邮件也删了: Len = %d", ix.Len())
	}
	if h := ix.All(); len(h) == 1 && !h[0].Seen {
		t.Error("Rewind 把已读状态弄丢了")
	}
}
