package mail

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"

	"github.com/emersion/go-imap"
	gomail "github.com/emersion/go-message/mail"
)

// maxHeaderBytes 是单个头部块的最大读取量。
// References 链再长也不会超过这个数；设上限是为了防止畸形邮件撑爆内存。
const maxHeaderBytes = 64 * 1024

// maxBodyBytes 是单封邮件正文的最大读取量。
const maxBodyBytes = 4 * 1024 * 1024

// Folder 选中一个文件夹并返回它的状态。
func (c *liveClient) Folder(name string) (Folder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLocked(); err != nil {
		return Folder{}, err
	}
	mbox, err := c.conn.Select(name, true)
	if err != nil {
		return Folder{}, fmt.Errorf("选中文件夹 %s 失败: %w", name, err)
	}
	return Folder{
		Name:        mbox.Name,
		UIDValidity: mbox.UidValidity,
		UIDNext:     mbox.UidNext,
		Messages:    mbox.Messages,
	}, nil
}

// Headers 拉取 UID 落在 [from, to] 区间内的邮件头部。
//
// to 传 0 表示「到最新」—— 这时会用 SELECT 返回的 UIDNext-1 兜底，
// 因为 IMAP 的 "*" 在库里的表达方式容易踩坑，不如自己算准。
func (c *liveClient) Headers(folder string, from, to uint32) ([]Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLocked(); err != nil {
		return nil, err
	}
	mbox, err := c.conn.Select(folder, true)
	if err != nil {
		return nil, fmt.Errorf("选中文件夹 %s 失败: %w", folder, err)
	}

	high := to
	if high == 0 {
		if mbox.UidNext == 0 {
			return nil, nil // 空文件夹
		}
		high = mbox.UidNext - 1
	}
	if from == 0 {
		from = 1
	}
	if high < from {
		return nil, nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddRange(from, high)

	section := headerSection()
	items := []imap.FetchItem{
		imap.FetchUid,
		imap.FetchFlags,
		imap.FetchEnvelope,
		section.FetchItem(),
	}

	fetched := make(chan *imap.Message, 32)
	done := make(chan error, 1)
	go func() { done <- c.conn.UidFetch(seqset, items, fetched) }()

	out := make([]Header, 0, high-from+1)
	for msg := range fetched {
		out = append(out, headerFromMessage(msg, folder, section))
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("拉取 %s 头部失败: %w", folder, err)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

// headerFromMessage 把一条 IMAP FETCH 结果转成 Header。
func headerFromMessage(msg *imap.Message, folder string, section *imap.BodySectionName) Header {
	h := Header{
		UID:    msg.Uid,
		Folder: folder,
		Seen:   hasFlag(msg.Flags, imap.SeenFlag),
	}

	if env := msg.Envelope; env != nil {
		h.MessageID = env.MessageId
		h.InReplyTo = env.InReplyTo
		h.Subject = decodeMIMEHeader(env.Subject)
		h.Date = env.Date
		if len(env.From) > 0 {
			h.From = addressOf(env.From[0])
			h.FromName = decodeMIMEHeader(env.From[0].PersonalName)
		}
		h.To = addressesOf(env.To)
		h.Cc = addressesOf(env.Cc)
	}

	if r := msg.GetBody(section); r != nil {
		if raw, err := io.ReadAll(io.LimitReader(r, maxHeaderBytes)); err == nil {
			h.References = ParseReferences(headerValue(string(raw), "References"))
		}
	}

	return h
}

// Body 拉取单封邮件的纯文本正文。
//
// HTML 邮件会被降级成纯文本，引用历史会被砍掉 —— 这是聊天视图该有的样子。
func (c *liveClient) Body(folder string, uid uint32) (Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLocked(); err != nil {
		return Message{}, err
	}
	if _, err := c.conn.Select(folder, true); err != nil {
		return Message{}, fmt.Errorf("选中文件夹 %s 失败: %w", folder, err)
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, section.FetchItem()}

	fetched := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() { done <- c.conn.UidFetch(seqset, items, fetched) }()

	var out Message
	found := false
	for msg := range fetched {
		found = true
		out = Message{UID: msg.Uid, Folder: folder}
		if env := msg.Envelope; env != nil {
			out.Subject = decodeMIMEHeader(env.Subject)
			out.Date = env.Date
			if len(env.From) > 0 {
				out.From = addressOf(env.From[0])
				out.FromName = decodeMIMEHeader(env.From[0].PersonalName)
			}
		}
		if r := msg.GetBody(section); r != nil {
			body, err := readPlainText(r)
			if err != nil {
				return Message{}, fmt.Errorf("解析 %s 的正文失败: %w", folder, err)
			}
			out.Body = body
		}
	}
	if err := <-done; err != nil {
		return Message{}, fmt.Errorf("拉取 %s 正文失败: %w", folder, err)
	}
	if !found {
		return Message{}, fmt.Errorf("在 %s 里找不到 UID %d", folder, uid)
	}
	return out, nil
}

// MarkSeen 给一批邮件打上 \Seen 标志。
func (c *liveClient) MarkSeen(folder string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLocked(); err != nil {
		return err
	}
	if _, err := c.conn.Select(folder, false); err != nil {
		return fmt.Errorf("选中文件夹 %s 失败: %w", folder, err)
	}

	seqset := new(imap.SeqSet)
	for _, u := range uids {
		seqset.AddNum(u)
	}
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := c.conn.UidStore(seqset, item, []interface{}{imap.SeenFlag}, nil); err != nil {
		return fmt.Errorf("标记已读失败: %w", err)
	}
	return nil
}

// readPlainText 从一封邮件的 MIME 结构里取出可读正文。
//
// 优先用 text/plain；没有的话把 text/html 降级。附件一律忽略 ——
// v1 不支持附件。
func readPlainText(r io.Reader) (string, error) {
	mr, err := gomail.CreateReader(r)
	if err != nil {
		return "", err
	}
	defer mr.Close()

	var plain, htmlBody string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}

		switch h := part.Header.(type) {
		case *gomail.InlineHeader:
			ct, _, _ := h.ContentType()
			b, err := io.ReadAll(io.LimitReader(part.Body, maxBodyBytes))
			if err != nil {
				continue
			}
			switch {
			case strings.HasPrefix(ct, "text/plain") && plain == "":
				plain = string(b)
			case strings.HasPrefix(ct, "text/html") && htmlBody == "":
				htmlBody = string(b)
			}
		case *gomail.AttachmentHeader:
			// v1 不支持附件，跳过。
		}
	}

	if strings.TrimSpace(plain) != "" {
		return CleanBody(plain, false), nil
	}
	return CleanBody(htmlBody, true), nil
}

// hasFlag 判断标志集合里有没有某个标志。
func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// addressOf 把 IMAP 地址结构转成纯地址（小写）。
func addressOf(a *imap.Address) string {
	if a == nil {
		return ""
	}
	if a.HostName == "" {
		return thread.NormalizeAddress(a.MailboxName)
	}
	return thread.NormalizeAddress(a.MailboxName + "@" + a.HostName)
}

// addressesOf 批量转换地址列表。
func addressesOf(addrs []*imap.Address) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if s := addressOf(a); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// headerSection 描述同步时我们唯一想要的头部字段。
//
// 两条约束都不能松：
//
//   - 必须用 BODY.PEEK 而不是 BODY。后者会顺手把邮件标记为已读 ——
//     那意味着每轮轮询都会把整个收件箱变成已读。
//   - 必须限定为 HEADER.FIELDS。不限定的话每轮轮询都要把每封邮件的
//     完整头部（甚至正文）拉下来，在几千封的邮箱上是灾难。
//
// Envelope 已经给了 From / To / Cc / Subject / Date / Message-ID /
// In-Reply-To，唯独 References 不在里面，所以只需要补这一个。
func headerSection() *imap.BodySectionName {
	return &imap.BodySectionName{
		// Specifier 挂在嵌入的 BodyPartName 上，不能用提升字段名
		// 直接在 BodySectionName 字面量里初始化。
		BodyPartName: imap.BodyPartName{
			Specifier: imap.HeaderSpecifier,
			Fields:    []string{"References"},
		},
		Peek: true,
	}
}

// headerValue 从 "Name: value" 形式的原始头部块里取出某个头的值，
// 并展开折行。找不到返回空串。
func headerValue(raw, name string) string {
	prefix := strings.ToLower(name) + ":"
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")

	var b strings.Builder
	collecting := false
	for _, line := range lines {
		if !collecting {
			if strings.HasPrefix(strings.ToLower(line), prefix) {
				b.WriteString(strings.TrimSpace(line[len(prefix):]))
				collecting = true
			}
			continue
		}
		// 折行的续行以空白开头；碰到别的就说明这个头结束了。
		if line == "" || (line[0] != ' ' && line[0] != '\t') {
			break
		}
		b.WriteString(" ")
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}
