// Package secret 用 AES-256-GCM 加解密 OpenList 凭据。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrNotConfigured 表示 Configure 尚未成功调用，或配置已被清空。
var ErrNotConfigured = errors.New("凭据加解密尚未初始化")

const keySize = 32

var (
	mu   sync.RWMutex
	aead cipher.AEAD
)

// Configure 从 CILIHUB_SECRET（base64 32 字节）或 <dataDir>/secret.key 读取密钥；
// 文件不存在时生成并以 0600 权限写入。
func Configure(dataDir string) error {
	key, err := loadKey(dataDir)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("初始化加密算法失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("初始化加密模式失败: %w", err)
	}
	mu.Lock()
	aead = gcm
	mu.Unlock()
	return nil
}

func loadKey(dataDir string) ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv("CILIHUB_SECRET")); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != keySize {
			return nil, errors.New("CILIHUB_SECRET 必须是 base64 编码的 32 字节密钥")
		}
		return key, nil
	}
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("未提供数据目录，无法读取或生成密钥文件")
	}

	path := filepath.Join(dataDir, "secret.key")
	data, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr != nil || len(key) != keySize {
			return nil, fmt.Errorf("密钥文件 %s 已损坏", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取密钥文件失败: %w", err)
	}

	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成密钥失败: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("写入密钥文件失败: %w", err)
	}
	return key, nil
}

// Seal 加密明文，返回 base64(nonce|ciphertext)。
func Seal(plain string) (string, error) {
	mu.RLock()
	g := aead
	mu.RUnlock()
	if g == nil {
		return "", ErrNotConfigured
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}
	out := make([]byte, 0, len(nonce)+len(plain)+g.Overhead())
	out = append(out, nonce...)
	out = g.Seal(out, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open 解密 Seal 产生的密文。
func Open(enc string) (string, error) {
	mu.RLock()
	g := aead
	mu.RUnlock()
	if g == nil {
		return "", ErrNotConfigured
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", errors.New("密文格式错误：不是合法的 base64")
	}
	if len(raw) < g.NonceSize()+g.Overhead() {
		return "", errors.New("密文格式错误：长度不足")
	}
	nonce, body := raw[:g.NonceSize()], raw[g.NonceSize():]
	plain, err := g.Open(nil, nonce, body, nil)
	if err != nil {
		return "", errors.New("解密失败：密文已损坏或密钥不匹配")
	}
	return string(plain), nil
}