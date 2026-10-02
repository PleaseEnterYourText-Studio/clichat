// Package logging 把运行日志落到配置目录下的 clichat.log。
//
// 设计文档承诺过这个文件（「日志写到配置目录下的 clichat.log。只记事件，
// 不记邮件正文。」），但一直没人实现 —— config.LogPath() 声明了却无人调用，
// 出问题时用户手上什么都没有，只能靠界面上一行会被截断的状态栏去猜。
//
// 两条约束：
//   - 只记事件，不记邮件正文。错误、连接、文件夹列表可以记；正文不可以。
//   - 日志本身不该成为新的故障点。打不开就退回 stderr，绝不让程序起不来。
package logging

import (
	"fmt"
	"log"
	"os"

	"github.com/PleaseEnterYourText-Studio/clichat/internal/config"
)

// maxSize 是日志文件超过多大就轮转一次。
//
// 1 MiB 足够装下很多次启动的错误记录；clichat 是低频使用的桌面工具，
// 真到了这个量级说明在反复重连出错，那时候最需要的是最近一段而不是全部。
const maxSize = 1 << 20

// Init 打开日志文件，把标准 logger 的输出接过去。
//
// 返回的关闭函数负责收尾。打不开日志文件时返回错误，但调用方应当
// 只是记一笔继续跑 —— 日志是诊断手段，不该变成新的故障点。
func Init() (func() error, error) {
	path, err := config.LogPath()
	if err != nil {
		return func() error { return nil }, err
	}

	rotate(path)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return func() error { return nil }, fmt.Errorf("打开日志文件 %s 失败: %w", path, err)
	}

	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("--- clichat 启动 pid=%d ---", os.Getpid())

	return f.Close, nil
}

// rotate 在日志超过 maxSize 时把它挪成 .1，旧的直接丢掉。
//
// 只留一份历史就够了：日志是用来诊断最近一次故障的，不是审计流水，
// 没必要上按天分片或者多份轮转那一套。
func rotate(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxSize {
		return
	}
	_ = os.Rename(path, path+".1")
}

// Path 返回日志文件的路径，供界面提示用户去哪里找。
func Path() (string, error) { return config.LogPath() }
