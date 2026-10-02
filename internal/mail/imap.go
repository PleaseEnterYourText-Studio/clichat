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

	real, mbox, err := c.selectLocked(name, true)
	if err != nil {
		return Folder{}, err
	}
	return Folder{
		Name:        mbox.Name,
		UIDValidity: mbox.UidValidity,
		UIDNext:     c.uidNextLocked(real, mbox.UidNext),
		Messages:    mbox.Messages,
	}, nil
}

// uidNextLocked 拿到文件夹的 UIDNEXT，SELECT 没给时用 STATUS 补查。
// 调用方必须已持有 c.mu。
//
// 拿不到就返回 0，由 fetchRange 退化成 `from:*` 兜底 —— 拉到的邮件可能
// 多一些，但不会整个文件夹漏掉。
//
// 注意 STATUS 用在「已选中的邮箱」上，RFC 3501 并不鼓励，所以失败要容忍：
// 这只是个优化（能让「只拉最近 N 封」的裁剪算得更准），不是必需品。
func (c *liveClient) uidNextLocked(mailbox string, fromSelect uint32) uint32 {
	if fromSelect > 0 {
		return fromSelect
	}
	st, err := c.conn.Status(mailbox, []imap.StatusItem{imap.StatusUidNext})
	if err != nil || st == nil {
		return 0
	}
	return st.UidNext
}

// Folders 列出服务端上实际存在的文件夹。
//
// 拿不到列表（LIST 失败）时报错而不是返回空切片：界面靠它渲染文件夹
// 切换器，给一个空列表等于告诉用户「你没有文件夹」，那是错的。
func (c *liveClient) Folders() ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLocked(); err != nil {
		return nil, err
	}
	if c.folderList == nil {
		return nil, errors.New("没能从服务端取到文件夹列表")
	}
	out := make([]string, len(c.folderList))
	copy(out, c.folderList)
	return out, nil
}

// Headers 拉取 UID 落在 [from, to] 区间内的邮件头部。
//
// to 传 0 表示「到最新」。
func (c *liveClient) Headers(folder string, from, to uint32) ([]Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, mbox, err := c.selectLocked(folder, true)
	if err != nil {
		return nil, err
	}

	lo, hi, ok := fetchRange(from, to, mbox.UidNext)
	if !ok {
		return nil, nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddRange(lo, hi)

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

	// hi 为 0 表示「到最新」，条数没法预估，给个合理的起始容量。
	capHint := 16
	if hi >= lo {
		capHint = int(hi-lo) + 1
	}
	out := make([]Header, 0, capHint)
	for msg := range fetched {
		out = append(out, headerFromMessage(msg, folder, section))
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("拉取 %s 头部失败: %w", folder, err)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

// fetchRange 算出一次头部拉取要用的 UID 区间。
//
// to 为 0 表示「到最新」：能拿 SELECT 给的 UIDNEXT 算准上界就算，
// 算不出来就让 hi 保持 0 —— 上层把它渲染成 `from:*`，也就是
// 「一直到最大的 UID」。ok 为 false 表示这个区间是空的（没有新邮件）。
//
// **这里踩过一个坑，别再踩回去。** 原先的写法是「uidNext == 0 就直接
// 返回空切片」，也就是把「服务端没告诉我们上界」当成了「文件夹是空的」。
// 网易 126 的 EXAMINE 响应里恰好没有 UIDNEXT，于是它的收件箱永远同步
// 不到一封邮件 —— 而它其实有几千封。见 fetchRange 的单元测试。
//
// 抽成独立函数是为了让 Fake 和真实客户端共用同一份边界逻辑。各写一份的话，
// 「服务端没给 UIDNEXT」这种只在真实世界里出现的情况在 Fake 里永远不会
// 发生，测试也就永远测不出来。
func fetchRange(from, to, uidNext uint32) (lo, hi uint32, ok bool) {
	if from == 0 {
		from = 1
	}

	hi = to
	switch {
	case hi != 0:
		// 调用方给了明确上界，照用。
	case uidNext > 0:
		hi = uidNext - 1
	default:
		// 上界未知。交给服务端用 `*` 自己定 —— 宁可多拉一些
		// （重复的会被 Message-ID 去重挡掉），也不能漏掉整个文件夹。
		return from, 0, true
	}

	if hi < from {
		return from, hi, false
	}
	return from, hi, true
}

// headerFromMessage 把一条 IMAP FETCH 结果转成 Header。
func headerFromMessage(msg *imap.Message, folder string, section *imap.BodySectionName) Header {
	h := Header{
		UID:     msg.Uid,
		Folder:  folder,
		Seen:    hasFlag(msg.Flags, imap.SeenFlag),
		Flagged: hasFlag(msg.Flags, imap.FlaggedFlag),
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

// Body 拉取单封邮件的正文。
//
// HTML 邮件会被转成 Markdown（不是降级成纯文本 —— 链接、表格、标题都
// 要留着，见 readPlainText），引用历史会被砍掉，这是聊天视图该有的样子。
// 返回的 Message.HTML 说明正文走的是哪一支。
func (c *liveClient) Body(folder string, uid uint32) (Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, _, err := c.selectLocked(folder, true); err != nil {
		return Message{}, err
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
			body, isHTML, err := readPlainText(r)
			if err != nil {
				return Message{}, fmt.Errorf("解析 %s 的正文失败: %w", folder, err)
			}
			out.Body = body
			out.HTML = isHTML
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
	return c.SetFlag(folder, uids, FlagSeen, true)
}

// SetFlag 加上或去掉一批邮件上的某个标志。
//
// 标回未读是 SetFlag(..., FlagSeen, false)，星标是
// SetFlag(..., FlagFlagged, true)。
func (c *liveClient) SetFlag(folder string, uids []uint32, flag string, add bool) error {
	if len(uids) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 用可写方式选中：打标志是修改操作。
	if _, _, err := c.selectLocked(folder, false); err != nil {
		return err
	}

	// 显式标注类型：go-imap 里只有 SetFlags 带 FlagsOp 类型，
	// AddFlags / RemoveFlags 是无类型字符串常量，`:=` 会推断成 string。
	op := imap.FlagsOp(imap.RemoveFlags)
	if add {
		op = imap.AddFlags
	}
	// 第二个参数 silent=true：让服务端别回 FETCH 更新，省一轮往返。
	item := imap.FormatFlagsOp(op, true)
	if err := c.conn.UidStore(seqSetOf(uids), item, []interface{}{flag}, nil); err != nil {
		return fmt.Errorf("更新标志 %s 失败: %w", flag, err)
	}
	return nil
}

// Move 把一批邮件移到另一个文件夹。界面上的「删除」走这里。
func (c *liveClient) Move(folder string, uids []uint32, dest string) error {
	if len(uids) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	realDest, err := c.resolveLocked(dest)
	if err != nil {
		return err
	}
	if _, _, err := c.selectLocked(folder, false); err != nil {
		return err
	}

	seqset := seqSetOf(uids)

	// 优先用 MOVE 扩展（RFC 6851）：它是原子的，服务端自己保证
	// 「复制 + 打删除标记 + 清空」这三步不会被打断。
	if ok, _ := c.conn.Support("MOVE"); ok {
		if err := c.conn.UidMove(seqset, realDest); err != nil {
			return fmt.Errorf("移动邮件到 %s 失败: %w", realDest, err)
		}
		return nil
	}

	// 退化路径：COPY 过去，打上 \Deleted，再 EXPUNGE。
	//
	// ⚠️ 这里的 EXPUNGE 会把当前文件夹里 **所有** 带 \Deleted 的邮件
	// 一起清掉，不只是我们刚标记的这批 —— go-imap v1 没有 UIDPLUS 的
	// UID EXPUNGE 可用，只能这样。所以能走 MOVE 就一定走 MOVE，
	// 这条路径只在服务端不支持 MOVE 时才会用到。
	if err := c.conn.UidCopy(seqset, realDest); err != nil {
		return fmt.Errorf("复制邮件到 %s 失败: %w", realDest, err)
	}
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := c.conn.UidStore(seqset, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return fmt.Errorf("标记待删除失败: %w", err)
	}
	if err := c.conn.Expunge(nil); err != nil {
		return fmt.Errorf("清空已删除邮件失败: %w", err)
	}
	return nil
}

// readPlainText 从一封邮件的 MIME 结构里取出可读正文。
//
// 两个部分都有时优先用 text/html，而不是反过来。看着反直觉，理由是这样：
//
//   - multipart/alternative 里的 text/plain 通常是同一封邮件的退化版：
//     链接摊成裸地址、按钮变成一行「点击此处」，排版信息全没了；
//   - HTML 这边转出来的 Markdown 把链接、按钮、表格、标题都留着。
//
// 代价是 HTML 分支要跑一遍 DOM 解析（比读纯文本贵）。正文上限
// 4 MB，这点开销可以忽略。
//
// 附件一律忽略 —— v1 不支持附件。
//
// 第二个返回值说明正文是从哪儿来的：true 表示走的是 HTML 那一支。
// 调用方要把它一路带到界面上（消息头的 HTML 标记），因为转换是有损的。
func readPlainText(r io.Reader) (body string, isHTML bool, err error) {
	mr, err := gomail.CreateReader(r)
	if err != nil {
		return "", false, err
	}
	defer mr.Close()

	var plain, htmlBody string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
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

	if strings.TrimSpace(htmlBody) != "" {
		return CleanBody(htmlBody, true), true, nil
	}
	return CleanBody(plain, false), false, nil
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
