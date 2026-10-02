// Package app 是 clichat 的编排层。
//
// 它把 config / mail / store / thread 四块拼起来，对上层（tui）暴露
// 一组「同步 / 发信 / 标记已读」的操作。
//
// 单独分这一层的理由：这些逻辑放在 Bubble Tea 的 model 里会让它变成
// 一个又胖又难测的上帝对象。放这里，它可以脱离界面单独测。
package app

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/mail"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/store"
	"github.com/PleaseEnterYourText-Studio/clichat/internal/thread"
)

// App 持有一次运行的全部状态。
type App struct {
	cfg    *config.Config
	client mail.Client
	index  *store.Index

	mu      sync.Mutex
	threads []thread.Thread
	byRoot  map[string]thread.Thread

	bodyMu sync.Mutex
	bodies map[string]string // Message-ID -> 正文
}

// New 组装一个 App。
func New(cfg *config.Config, client mail.Client, index *store.Index) *App {
	a := &App{
		cfg:    cfg,
		client: client,
		index:  index,
		byRoot: map[string]thread.Thread{},
		bodies: map[string]string{},
	}
	a.reaggregate()
	return a
}

// Config 返回配置。tui 需要读账号信息来渲染状态栏。
func (a *App) Config() *config.Config { return a.cfg }

// Self 返回当前账号地址（小写）。
func (a *App) Self() string { return a.cfg.Self() }

// Threads 返回会话列表，按最后活跃时间降序。
func (a *App) Threads() []thread.Thread {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]thread.Thread, len(a.threads))
	copy(out, a.threads)
	return out
}

// Thread 按根 ID 取一个会话。
func (a *App) Thread(root string) (thread.Thread, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	t, ok := a.byRoot[root]
	return t, ok
}

// AllMail 报告是否处于「接收全部邮件」模式。
func (a *App) AllMail() bool { return a.cfg.Sync.AllMail }

// SetAllMail 切换「接收全部邮件」模式，并把选择落盘。
//
// 打开时顺手把本地游标全部退回起点。这一步不能省：这个模式的语义是
// 「历史邮件也要」，而已经同步过的文件夹游标停在半路，不退回去的话
// 只有将来新到的邮件会被拉下来 —— 用户按了开关，界面上什么都没变，
// 那是最糟的一种「成功」。
//
// 关掉时不动游标：那些邮件已经在索引里了，退回去只会让它们再走一遍。
//
// 配置写盘失败要原样返回：这时候内存里的开关已经改了，界面得知道
// 这次切换没落地，否则重启之后「设置自己变回去了」又是一桩悬案。
func (a *App) SetAllMail(on bool) error {
	prev := a.cfg.Sync.AllMail
	a.cfg.Sync.AllMail = on
	if err := a.cfg.Save(); err != nil {
		// 没落盘就不算切换成功，把内存里的值退回去。
		// 不退的话会对不上：游标没退、磁盘没写，而状态栏读的正是
		// AllMail()，于是界面上会挂着一个「已打开」的假标记。
		a.cfg.Sync.AllMail = prev
		return fmt.Errorf("保存配置失败: %w", err)
	}
	if on {
		a.index.Rewind()
	}
	return nil
}

// Sync 增量同步所有配置的文件夹。
//
// 返回 changed 表示有没有新数据 —— 界面靠它决定要不要重绘。
// 某个文件夹失败不会中断其它文件夹：能同步多少算多少，
// 错误一并返回给上层展示。
func (a *App) Sync() (bool, error) {
	changed := false
	var errs []error

	for _, folder := range a.cfg.Sync.Folders {
		c, err := a.syncFolder(folder)

		// 服务端上没有这个文件夹不算故障，跳过就好 —— 各家对「已发送」
		// 的叫法本来就不同（见 mail/folders.go 的别名表）。把它当错误
		// 报上去，界面会整个标成"离线"，反而掩盖了真正同步成功的那些。
		if err != nil {
			if errors.Is(err, mail.ErrNoSuchFolder) {
				log.Printf("同步 %s: 服务端没有这个文件夹，跳过", folder)
			} else {
				// 记下来。界面上只有一行会被截断的状态栏，靠它诊断等于没有。
				// 这条日志是「同步没跑完 / 卡住了」这类问题唯一的线索来源。
				log.Printf("同步 %s 失败: %v", folder, err)
				errs = append(errs, err)
			}
		}
		if c {
			changed = true
		}
	}

	if changed {
		a.reaggregate()
	}
	return changed, errors.Join(errs...)
}

// syncFolder 同步单个文件夹，返回**索引是否发生了变化**。
//
// 注意这里返回的不是「新增了几封」。索引被清空同样是一种变化，
// 早先的版本只看新增条数，结果 UIDVALIDITY 变化后索引已经空了、
// 界面却还在显示那批已经失效的旧会话。
func (a *App) syncFolder(folder string) (bool, error) {
	f, err := a.client.Folder(folder)
	if err != nil {
		return false, err
	}

	changed := false
	st, known := a.index.FolderState(folder)

	// UIDVALIDITY 变了说明服务器重置了 UID 空间，本地游标整体失效。
	//
	// 这里清空整个索引而不是只清这一个文件夹：Message-ID 本来就是全局
	// 去重的，而 UIDVALIDITY 变化极罕见，全量重来一次比维护分文件夹的
	// 精细失效逻辑更不容易出错。
	if known && st.UIDValidity != f.UIDValidity {
		log.Printf("同步 %s: UIDVALIDITY 变化 %d -> %d，清空本地索引重来",
			folder, st.UIDValidity, f.UIDValidity)
		a.index.Reset()
		st = store.FolderState{}
		known = false
		changed = true
	}

	// firstSync 判断「这是这个文件夹第一次真正拉东西」。
	//
	// 注意条件不是 !known 而是 !known || st.LastUID == 0：文件夹状态存在
	// 但游标还停在 0，说明上一次同步一条都没拉进来（目录状态是先写下的）。
	// 那是「还没同步过」，不是「同步过了」。
	//
	// 这个判断必须同时管两件事 —— 拉取起点、以及要不要按天数裁 ——
	// 否则会出现「起点按首次算、裁剪按非首次算」的组合：本该只拉 90 天的
	// 冷启动变成整份历史拉下来，用户会突然等很久。
	firstSync := !known || st.LastUID == 0

	var from uint32
	if firstSync {
		// 首次同步不拉全部历史：按封数先划一道线。
		// 「接收全部邮件」模式下这条线不划。
		from = 1
		if !a.cfg.Sync.AllMail && f.UIDNext > uint32(a.cfg.Sync.InitialMaxMessages) {
			from = f.UIDNext - uint32(a.cfg.Sync.InitialMaxMessages)
		}
	} else {
		from = st.LastUID + 1
	}

	headers, err := a.client.Headers(folder, from, 0)
	if err != nil {
		return changed, err
	}
	if firstSync && !a.cfg.Sync.AllMail {
		headers = a.withinInitialWindow(headers)
	}

	added := a.index.Merge(headers)
	if added > 0 {
		changed = true
	}

	last := st.LastUID
	for _, h := range headers {
		if h.UID > last {
			last = h.UID
		}
	}
	a.index.SetFolderState(folder, store.FolderState{
		UIDValidity: f.UIDValidity,
		LastUID:     last,
	})

	// 首拉要几十秒（实测 QQ 邮箱约 39ms/条），界面上只会显示「同步中…」。
	// 用户很容易以为卡死了然后退出 —— 日志里至少能留下「跑到哪一步了」。
	if firstSync {
		log.Printf("同步 %s: 首次同步，拉取 UID %d 起，%d 条，游标推进到 %d",
			folder, from, added, last)
	} else if added > 0 {
		log.Printf("同步 %s: 新增 %d 条，游标推进到 %d", folder, added, last)
	}

	// 服务端说有信，我们却一封都没拿到 —— 这是「界面空着但看不出原因」
	// 的那类故障，必须留一条线索。真发生过两次：
	//   ① 服务端不给 UIDNEXT 时被误判成空文件夹（见 mail.fetchRange）；
	//   ② 收件箱里只有 90 天以前的信，被首次同步的时间窗口整批裁掉。
	// 两种都表现为「配好了却一封信都看不到」，而日志里一个字都没有。
	if added == 0 && f.Messages > 0 {
		log.Printf("同步 %s: 服务端上有 %d 封，本次一条都没进索引（拉取区间 %d 起，"+
			"首次同步=%v，非全量模式时会按 %d 天裁剪）—— 若是历史邮件，"+
			"用「接收全部邮件」重新拉",
			folder, f.Messages, from, firstSync, a.cfg.Sync.InitialDays)
	}
	return changed, nil
}

// withinInitialWindow 按天数再裁一道。
//
// 按封数裁已经在上游做了，但一个每天只有三五封信的邮箱，
// 500 封可能是三年前的 —— 那不该被当成「最近的会话」铺满界面。
//
// 「接收全部邮件」模式下不会走到这里：那时连按封数那条线都不划，
// 再留着日期线就成了「划了两道、只放开一道」。
func (a *App) withinInitialWindow(headers []mail.Header) []mail.Header {
	if a.cfg.Sync.InitialDays <= 0 {
		return headers
	}
	cutoff := time.Now().AddDate(0, 0, -a.cfg.Sync.InitialDays)

	out := headers[:0]
	for _, h := range headers {
		// Date 缺失时保守地留下，宁可多显示也不要凭空丢掉邮件。
		if h.Date.IsZero() || h.Date.After(cutoff) {
			out = append(out, h)
		}
	}
	return out
}

// reaggregate 用当前索引重算全部会话。
func (a *App) reaggregate() {
	threads := thread.Aggregate(a.index.All(), a.cfg.Self())

	a.mu.Lock()
	defer a.mu.Unlock()

	a.threads = threads
	a.byRoot = make(map[string]thread.Thread, len(threads))
	for _, t := range threads {
		a.byRoot[t.Root] = t
	}
}

// Bodies 返回一个会话里所有消息的正文，key 是 Message-ID。
//
// 单封拉取失败不会让整个会话打不开 —— 那一封填一句提示，
// 其余的照常显示。返回的 error 只表示「有部分失败」。
func (a *App) Bodies(th thread.Thread) (map[string]string, error) {
	out := make(map[string]string, len(th.Messages))

	var missing []mail.Header
	for _, m := range th.Messages {
		if b, ok := a.cachedBody(m.MessageID); ok {
			out[m.MessageID] = b
			continue
		}
		if m.UID == 0 {
			// 乐观插入的本地消息还没有 UID，服务端拿不到。
			out[m.MessageID] = ""
			continue
		}
		missing = append(missing, m)
	}

	var errs []error
	for _, m := range missing {
		msg, err := a.client.Body(m.Folder, m.UID)
		if err != nil {
			out[m.MessageID] = "（正文加载失败）"
			errs = append(errs, fmt.Errorf("%s UID %d: %w", m.Folder, m.UID, err))
			continue
		}
		a.cacheBody(m.MessageID, msg.Body)
		out[m.MessageID] = msg.Body
	}
	return out, errors.Join(errs...)
}

func (a *App) cachedBody(id string) (string, bool) {
	a.bodyMu.Lock()
	defer a.bodyMu.Unlock()

	b, ok := a.bodies[id]
	return b, ok
}

func (a *App) cacheBody(id, body string) {
	a.bodyMu.Lock()
	defer a.bodyMu.Unlock()

	a.bodies[id] = body
}

// MarkRead 把一个会话标为已读，服务端和本地索引一起改。
func (a *App) MarkRead(th thread.Thread) error {
	byFolder := map[string][]uint32{}
	for _, m := range th.Messages {
		if m.Seen || m.UID == 0 {
			continue
		}
		byFolder[m.Folder] = append(byFolder[m.Folder], m.UID)
	}
	if len(byFolder) == 0 {
		return nil
	}

	var errs []error
	for folder, uids := range byFolder {
		if err := a.client.MarkSeen(folder, uids); err != nil {
			errs = append(errs, err)
		}
		// 服务端失败也要改本地，否则界面会一直显示未读。
		a.index.MarkSeen(folder, uids)
	}
	a.reaggregate()
	return errors.Join(errs...)
}

// Folders 返回服务端上实际存在的文件夹，供界面做切换。
func (a *App) Folders() ([]string, error) {
	return a.client.Folders()
}

// MarkUnread 把一个会话标回未读，服务端和本地索引一起改。
//
// 只动 UID 非 0 的消息：UID 为 0 的是本地乐观插入的副本，服务端上
// 根本不存在，拿它去 STORE 只会换回一个错误。
func (a *App) MarkUnread(th thread.Thread) error {
	byFolder, _ := a.splitByFolder(th)
	return a.applyFlag(byFolder, mail.FlagSeen, false, func(folder string, uids []uint32) {
		a.index.MarkUnread(folder, uids)
	})
}

// SetStarred 给整个会话打上或去掉星标。
func (a *App) SetStarred(th thread.Thread, on bool) error {
	byFolder, _ := a.splitByFolder(th)
	return a.applyFlag(byFolder, mail.FlagFlagged, on, func(folder string, uids []uint32) {
		a.index.SetFlagged(folder, uids, on)
	})
}

// applyFlag 按文件夹把标志改动同时打到服务端和本地索引上。
//
// 服务端失败也照样改本地：否则界面会一直显示旧状态，而用户已经
// 看到操作"没反应"了，更糟。错误照常返回给上层展示。
func (a *App) applyFlag(byFolder map[string][]uint32, flag string, add bool, local func(string, []uint32)) error {
	if len(byFolder) == 0 {
		return nil
	}

	var errs []error
	for folder, uids := range byFolder {
		if err := a.client.SetFlag(folder, uids, flag, add); err != nil {
			errs = append(errs, err)
		}
		local(folder, uids)
	}
	a.reaggregate()
	return errors.Join(errs...)
}

// splitByFolder 把会话里的消息按所在文件夹分组，顺便返回全部 Message-ID。
//
// UID 为 0 的本地乐观副本不进分组（服务端上没有），但仍算进 Message-ID
// 列表 —— 删除时它们也得从本地索引里移出去。
func (a *App) splitByFolder(th thread.Thread) (map[string][]uint32, []string) {
	byFolder := map[string][]uint32{}
	ids := make([]string, 0, len(th.Messages))
	for _, m := range th.Messages {
		ids = append(ids, m.MessageID)
		if m.UID == 0 {
			continue
		}
		byFolder[m.Folder] = append(byFolder[m.Folder], m.UID)
	}
	return byFolder, ids
}

// Delete 把一个会话移到垃圾箱。
//
// 「删除」在这里是移到服务端的已删除文件夹，不是永久删除 ——
// 用户还能从网页版找回来。不可恢复的操作不该挂在一个单键快捷键上。
func (a *App) Delete(th thread.Thread) error {
	dest, err := a.trashFolder()
	if err != nil {
		return err
	}

	byFolder, ids := a.splitByFolder(th)

	var errs []error
	for folder, uids := range byFolder {
		if err := a.client.Move(folder, uids, dest); err != nil {
			errs = append(errs, err)
		}
	}

	// 本地这份无论如何都要移出去：那些 UID 已经被 MOVE 走了，留着的话
	// 下一轮聚合又会把它们显示回列表里，用户会以为删除没生效。
	a.index.Remove(ids)
	a.reaggregate()
	return errors.Join(errs...)
}

// trashFolder 找出服务端的垃圾箱叫什么。
//
// 拿不到文件夹列表时退回 "Trash" 交给 Move 去报错 —— 那比在这里
// 编一个错误更利于排查，因为错误信息里会带上服务端真实回了什么。
func (a *App) trashFolder() (string, error) {
	folders, err := a.client.Folders()
	if err != nil {
		return "Trash", nil
	}
	if name, ok := mail.ResolveFolder("Trash", folders); ok {
		return name, nil
	}
	return "", fmt.Errorf("服务端上没有已删除文件夹（现有：%s），无法删除",
		strings.Join(folders, "、"))
}

// Forward 把会话里最后一条消息转发给指定收件人。
//
// 转发出去的是 **一封新邮件**：新的 Message-ID，不挂 References，
// 所以它在收件人那里会开一个新会话，而不是并进原来的讨论 ——
// 这正是转发该有的语义。
func (a *App) Forward(th thread.Thread, to []string) (string, error) {
	if len(to) == 0 {
		return "", errors.New("收件人为空")
	}

	body := a.forwardBody(th)
	if body == "" {
		return "", errors.New("这个会话里没有可转发的内容")
	}

	subject := th.Subject
	if subject == "" {
		subject = "（无主题）"
	}
	return a.send(mail.Outgoing{
		To:      to,
		Subject: "Fwd: " + subject,
		Body:    body,
	})
}

// LastBodyText 返回会话里最后一条消息的正文，供界面复制到剪贴板。
//
// 单独开这个方法而不是让界面自己去读缓存：列表模式下用户还没打开过
// 这个会话，缓存里根本没有，必须回源拉一次。
func (a *App) LastBodyText(th thread.Thread) (string, error) {
	return a.lastBody(th)
}

// lastBody 取会话里最后一条消息的正文，优先用缓存。
func (a *App) lastBody(th thread.Thread) (string, error) {
	if len(th.Messages) == 0 {
		return "", errors.New("这个会话里没有消息")
	}
	last := th.Messages[len(th.Messages)-1]

	if b, ok := a.cachedBody(last.MessageID); ok {
		return b, nil
	}
	if last.UID == 0 {
		// 本地乐观插入的副本还没同步到服务端，服务端上没有它。
		return "", nil
	}
	msg, err := a.client.Body(last.Folder, last.UID)
	if err != nil {
		return "", err
	}
	a.cacheBody(last.MessageID, msg.Body)
	return msg.Body, nil
}

// forwardBody 拼出转发正文：一段来源说明 + 原文。
func (a *App) forwardBody(th thread.Thread) string {
	if len(th.Messages) == 0 {
		return ""
	}
	last := th.Messages[len(th.Messages)-1]

	body, err := a.lastBody(th)
	if err != nil || strings.TrimSpace(body) == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("---------- 转发的消息 ----------\n")
	b.WriteString("发件人：" + last.From + "\n")
	if !last.Date.IsZero() {
		b.WriteString("时间：" + last.Date.Local().Format("2006-01-02 15:04") + "\n")
	}
	b.WriteString("主题：" + last.Subject + "\n\n")
	b.WriteString(body)
	return b.String()
}

// Reply 回复一个会话。
//
// replyAll 为 true 时发给会话里除自己外的所有人（默认，符合聊天语义）；
// 为 false 时只发给最后一位不是自己的发言人。
func (a *App) Reply(th thread.Thread, body string, replyAll bool) error {
	if body == "" {
		return errors.New("消息内容为空")
	}

	var to []string
	if replyAll {
		to = append(to, th.Participants...)
	} else if target := replyTarget(th, a.cfg.Self()); target != "" {
		to = append(to, target)
	}
	if len(to) == 0 {
		return errors.New("这个会话里没有可以回复的对象")
	}

	// 回复的返回值不重要 —— 会话本来就已经存在，界面不需要切过去。
	_, err := a.send(mail.Outgoing{
		To:         to,
		Subject:    "Re: " + th.Subject,
		Body:       body,
		InReplyTo:  th.LastMessageID(),
		References: th.MessageIDs(),
	})
	return err
}

// Start 新建一个会话并发出第一条消息。
//
// 返回新会话的根 ID（就是这条消息的 Message-ID）。界面需要它把视图
// 切到刚建好的会话上 —— 否则用户发完第一条消息会停在一个空白页面上。
func (a *App) Start(to []string, body string) (string, error) {
	if len(to) == 0 {
		return "", errors.New("收件人为空")
	}
	if body == "" {
		return "", errors.New("消息内容为空")
	}

	subject := mail.FirstLine(body, 60)
	if subject == "" {
		subject = "新会话"
	}
	return a.send(mail.Outgoing{To: to, Subject: subject, Body: body})
}

// send 发信，并把发出去的消息立刻并进本地索引。
//
// 这个「乐观插入」很重要：服务商把已发送邮件存进 Sent 需要一轮轮询，
// 不等它的话，用户发完消息要盯着空白的会话看半分钟。
// 等 Sent 的副本同步回来时，会因为 Message-ID 相同而被自动去重。
func (a *App) send(out mail.Outgoing) (string, error) {
	id, err := a.client.Send(out)
	if err != nil {
		// 只记条数，不记地址 —— 日志的约定是「只记事件，不记邮件内容」，
		// 收件人地址属于内容。
		log.Printf("发送失败（%d 个收件人）: %v", len(out.To)+len(out.Cc), err)
		return "", err
	}
	log.Printf("发送成功: Message-ID=%s，%d 个收件人", id, len(out.To)+len(out.Cc))

	a.cacheBody(id, out.Body)
	a.index.Merge([]mail.Header{{
		MessageID:  id,
		References: out.References,
		InReplyTo:  out.InReplyTo,
		From:       a.cfg.Self(),
		To:         out.To,
		Cc:         out.Cc,
		Subject:    out.Subject,
		Date:       time.Now(),
		Folder:     "Sent",
		Seen:       true,
	}})
	a.reaggregate()
	return id, nil
}

// replyTarget 找出「只回发件人」时该发给谁。
//
// 从最后一条往前找第一个不是自己发的消息 —— 如果最后一条是我发的，
// 直接回给自己没有意义。
func replyTarget(th thread.Thread, self string) string {
	for i := len(th.Messages) - 1; i >= 0; i-- {
		from := th.Messages[i].From
		if from != "" && from != self {
			return from
		}
	}
	return ""
}

// Close 断开连接并落盘索引。
func (a *App) Close() error {
	err := a.client.Close()
	if saveErr := a.index.Save(); saveErr != nil {
		err = errors.Join(err, saveErr)
	}
	return err
}
