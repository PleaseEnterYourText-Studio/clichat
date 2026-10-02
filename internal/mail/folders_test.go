package mail

import (
	"strings"
	"testing"
)

// 这几份文件夹列表是照着真实服务商抄的，不是编的 ——
// 网易系那份来自实测（Coremail 用中文名），QQ 和 Gmail 的来自公开文档。
var (
	// 网易系 163 / 126。**里面没有 "Sent"** —— 这就是配置里写 "Sent"
	// 会在 EXAMINE 上失败的原因。
	neteaseFolders = []string{
		"INBOX", "草稿箱", "已发送", "已删除", "垃圾邮件", "病毒文件夹", "订阅邮件",
	}
	qqFolders    = []string{"INBOX", "Sent Messages", "Drafts", "Deleted Messages", "Junk"}
	gmailFolders = []string{"INBOX", "Sent", "Drafts", "Spam", "Trash"}
)

func TestResolveFolder(t *testing.T) {
	tests := []struct {
		name      string
		want      string
		available []string
		expect    string
		ok        bool
	}{
		{
			name:      "网易系把 Sent 解析成中文的已发送",
			want:      "Sent",
			available: neteaseFolders,
			expect:    "已发送",
			ok:        true,
		},
		{
			name:      "网易系的 INBOX 原样命中",
			want:      "INBOX",
			available: neteaseFolders,
			expect:    "INBOX",
			ok:        true,
		},
		{
			name:      "QQ 的已发送叫 Sent Messages",
			want:      "Sent",
			available: qqFolders,
			expect:    "Sent Messages",
			ok:        true,
		},
		{
			name:      "Gmail 就叫 Sent",
			want:      "Sent",
			available: gmailFolders,
			expect:    "Sent",
			ok:        true,
		},
		{
			// 原样命中必须优先于别名：服务端同时有 "Sent" 和 "已发送" 时，
			// 用户配的 "Sent" 就该用 "Sent"，别被别名表抢走。
			name:      "原样命中优先于别名",
			want:      "Sent",
			available: []string{"已发送", "Sent"},
			expect:    "Sent",
			ok:        true,
		},
		{
			name:      "大小写不敏感",
			want:      "inbox",
			available: gmailFolders,
			expect:    "INBOX",
			ok:        true,
		},
		{
			// 别名表里没有 Drafts（各家叫法太多，不值得硬编），
			// 网易系上就解析不出来 —— 这是预期行为，不是漏配。
			name:      "解析不出来的规范名要明确失败",
			want:      "Drafts",
			available: neteaseFolders,
			expect:    "",
			ok:        false,
		},
		{
			name:      "用户自定义的文件夹名原样命中",
			want:      "Archive",
			available: []string{"INBOX", "Archive"},
			expect:    "Archive",
			ok:        true,
		},
		{
			// 前缀不算命中：别把 "Sent" 匹配到 "SentX" 上。
			name:      "不做前缀匹配",
			want:      "Sent",
			available: []string{"INBOX", "SentX"},
			expect:    "",
			ok:        false,
		},
		{
			// LIST 失败（拿不到列表）时不做猜测，原样放行。
			// 宁可让 SELECT 自然报错，也不要凭残缺信息把能用的文件夹判死。
			name:      "拿不到列表时原样放行",
			want:      "Sent",
			available: nil,
			expect:    "Sent",
			ok:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveFolder(tc.want, tc.available)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v（返回 %q）", ok, tc.ok, got)
			}
			if got != tc.expect {
				t.Errorf("解析结果 = %q, want %q", got, tc.expect)
			}
		})
	}
}

// 守住这条：别名表的键必须是大写，否则 ToUpper 查不到，
// 整张表会静默失效 —— 而失效的表现正是本次要修的那个 bug。
func TestFolderAliases_KeysAreUpper(t *testing.T) {
	for key := range folderAliases {
		if key != strings.ToUpper(key) {
			t.Errorf("别名表的键 %q 不是大写，ToUpper 查不到它", key)
		}
	}
}

// 别名表里必须包含网易系用的中文名 —— 这是本次修复的直接原因。
func TestFolderAliases_CoversNeteaseSentName(t *testing.T) {
	for _, alias := range folderAliases["SENT"] {
		if alias == "已发送" {
			return
		}
	}
	t.Error(`"SENT" 的别名表里没有「已发送」，网易系（163/126）会重新挂掉`)
}
