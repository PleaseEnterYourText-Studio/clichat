package config

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/nacl/secretbox"
)

// 凭据文件格式：
//
//	[magic "CLIC" 4B][version 1B][salt 16B][nonce 24B][secretbox ciphertext]
//
// 不需要额外存校验值：主密码错误时 secretbox.Open 会因为
// Poly1305 认证标签不匹配而失败，这本身就是校验。
const (
	credsMagic   = "CLIC"
	credsVersion = 1
	saltLen      = 16
	nonceLen     = 24
	keyLen       = 32
	headerLen    = 4 + 1 + saltLen + nonceLen
)

// Argon2id 参数。
//
// 这三个值是「安全强度」与「启动延迟」的权衡：定得太低挡不住离线爆破，
// 定得太高每次启动都要等。目标是解密耗时落在 200–500ms。
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB，即 64 MiB
	argonThreads = 4
)

var (
	// ErrBadCredsFile 表示凭据文件损坏或格式不认识。
	ErrBadCredsFile = errors.New("凭据文件格式不正确")
	// ErrWrongPassword 表示主密码错误。
	ErrWrongPassword = errors.New("主密码错误")
)

// deriveKey 用 Argon2id 从主密码派生 32 字节密钥。
func deriveKey(masterPassword string, salt []byte) *[32]byte {
	k := argon2.IDKey([]byte(masterPassword), salt, argonTime, argonMemory, argonThreads, keyLen)
	var key [32]byte
	copy(key[:], k)
	return &key
}

// SealCredentials 用主密码加密明文。
//
// salt 与 nonce 每次调用都重新随机生成，所以同样的输入会得到不同的密文。
func SealCredentials(masterPassword string, plaintext []byte) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("生成 salt 失败: %w", err)
	}
	var nonce [nonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("生成 nonce 失败: %w", err)
	}

	out := make([]byte, 0, headerLen+len(plaintext)+secretbox.Overhead)
	out = append(out, credsMagic...)
	out = append(out, credsVersion)
	out = append(out, salt...)
	out = append(out, nonce[:]...)
	return secretbox.Seal(out, plaintext, &nonce, deriveKey(masterPassword, salt)), nil
}

// OpenCredentials 用主密码解密密文。
//
// 主密码错误返回 ErrWrongPassword；文件格式不对返回 ErrBadCredsFile。
func OpenCredentials(masterPassword string, data []byte) ([]byte, error) {
	if len(data) < headerLen {
		return nil, ErrBadCredsFile
	}
	if string(data[:4]) != credsMagic {
		return nil, ErrBadCredsFile
	}
	if data[4] != credsVersion {
		return nil, fmt.Errorf("%w: 不支持的版本 %d", ErrBadCredsFile, data[4])
	}

	var salt [saltLen]byte
	copy(salt[:], data[5:5+saltLen])
	var nonce [nonceLen]byte
	copy(nonce[:], data[5+saltLen:5+saltLen+nonceLen])

	plain, ok := secretbox.Open(nil, data[headerLen:], &nonce, deriveKey(masterPassword, salt[:]))
	if !ok {
		return nil, ErrWrongPassword
	}
	return plain, nil
}
