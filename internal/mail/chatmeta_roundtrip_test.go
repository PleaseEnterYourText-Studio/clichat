package mail

import (
	"strings"
	"testing"
)

// 这一组是**跨包**的：发信写出来的头，必须能被收信那条路径认出并解回来。
//
// 为什么不能只测 EncodeChatMeta / DecodeChatMeta 的往返：那两个函数互相
// 认识不算数。真正会坏的是它们**中间那道缝** —— 头名写在哪儿、列表那条
// 路径有没有点名要这个字段、真服务端会不会把折行弄丢。本仓库栽过的
// 「两个包各自都绿、中间那道缝是空的」就是这个形状。
//
// 所以这里起真的假服务端（roundtrip_test.go 那套），把 BuildMessage 的
// 产物原样塞进去，再走 liveClient.Headers —— 拿到的就是列表那一层的证据。

// 发出去 → 落到服务端 → 列表认出来，三点连线。
func TestChatMeta_RoundTripThroughServer(t *testing.T) {
	ms := startMailServer(t)

	meta := fullMeta()
	// 昵称填满上限：编码之后会长到触发折行，于是这条同时把
	// 「折行会不会在真服务端那儿弄丢」也一起钉住了。
	meta.Nick = strings.Repeat("昵", maxChatNick)

	raw, _, err := BuildMessage("衡", "me@example.com", Outgoing{
		To:      []string{"me@clichat.local"},
		Subject: "从 clichat 发出的",
		Body:    "喂",
	}, meta)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	// 先确认这条路真的走了折行 —— 否则这条判据绿了也说明不了什么。
	if !strings.Contains(string(raw), ChatMetaHeader+": ") ||
		!strings.Contains(string(raw), "\r\n ") {
		t.Fatalf("这封信里的 %s 头没有折行，判据没走到那条路上:\n%s",
			ChatMetaHeader, raw)
	}

	ms.add(t, "INBOX", string(raw), false)
	ms.add(t, "INBOX", rawMail("普通邮件", "alice@example.com", "plain@example.com"), false)

	c := ms.client(t)
	headers, err := c.Headers("INBOX", 1, 0)
	if err != nil {
		t.Fatalf("拉头部失败: %v", err)
	}
	// memory 后端预置了一封，加上我们塞的两封。
	if len(headers) != 3 {
		t.Fatalf("头部条数 = %d，想要 3", len(headers))
	}

	chat, plain := 0, 0
	for _, h := range headers {
		if h.Clichat == nil {
			plain++
			continue
		}
		chat++
		if *h.Clichat != meta {
			t.Errorf("收信解出来的身份卡和发出去的不一样:\n got %+v\nwant %+v",
				*h.Clichat, meta)
		}
		if !h.FromClichat() {
			t.Error("Clichat 有值但 FromClichat() 说是假的 —— " +
				"「是不是 clichat 消息」的判据必须只有一份")
		}
	}
	if chat != 1 || plain != 2 {
		t.Errorf("认出 %d 封 clichat、%d 封普通，想要 1 / 2 —— "+
			"认错方向的两个后果都很难看：普通邮件被标成 clichat（骗人），"+
			"或者 clichat 消息认不出来（功能等于没有）", chat, plain)
	}

	// ⚠️ 列表这一层认出 clichat 消息必须是**零额外往返**的。
	//
	// 这就是「把 meta 放在自定义头里，而不是塞进正文」这个决定的全部
	// 理由：同步列表只拉头，正文要打开会话才拉。放正文里的话，列表要
	// 认出 clichat 消息就得把每封的正文都拉一遍 —— O(消息数) 次往返。
	//
	// 判据钉的是协议层的事实（那条 FETCH 到底要了什么），不是对代码的
	// 推断：只要有人把 headerSection 改成整封取，这里立刻变红。
	fetches := 0
	for _, line := range ms.rawLines() {
		up := strings.ToUpper(line)
		if !strings.Contains(up, "FETCH") {
			continue
		}
		fetches++
		if !strings.Contains(up, "HEADER.FIELDS") {
			t.Errorf("拉头部那条 FETCH 没点名要 HEADER.FIELDS:\n%s", line)
		}
		if strings.Contains(up, "BODY[]") || strings.Contains(up, "BODY.PEEK[]") {
			t.Errorf("拉头部那条 FETCH 把整封正文也拉下来了 —— "+
				"这正是把 meta 放头里要避免的 O(消息数):\n%s", line)
		}
	}
	if fetches != 1 {
		t.Errorf("拉头部发了 %d 条 FETCH，想要 1 条", fetches)
	}

	// 打开会话那条路径（Bodies）也不该让 meta 漏进正文。
	//
	// 这是「对方在邮件客户端里看着没区别」在收信侧的对应判据：头是头、
	// 正文是正文，两者不能串。正文里出现那串 base64 的话，用户会看到
	// 一段莫名其妙的字母数字。
	bodies, err := c.Bodies("INBOX", uidsOf(headers))
	if err != nil {
		t.Fatalf("批量拉正文失败: %v", err)
	}
	for uid, m := range bodies {
		if strings.Contains(m.Body, ChatMetaHeader) ||
			strings.Contains(m.Body, "X-Clichat") ||
			strings.Contains(m.Body, EncodeChatMeta(meta)) {
			t.Errorf("UID %d 的正文里漏进了 meta:\n%s", uid, m.Body)
		}
	}
}
