//go:build !windows

package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// ==================== 非 Windows 凭据加密（AES-256-GCM）====================
// 随机生成 32 字节密钥存 data/.app_key（权限 0600），AES-256-GCM 加解密。
// 空字符串明文不做加密（直接返回 ""），解密时同样原样返回。

// appKey 读取或生成应用级加密密钥
func appKey() ([]byte, error) {
	keyPath := filepath.Join(".", "data", ".app_key")
	if b, err := os.ReadFile(keyPath); err == nil && len(b) == 32 {
		return b, nil
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return nil, fmt.Errorf("创建 data 目录失败: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return nil, fmt.Errorf("写入密钥文件失败: %w", err)
	}
	return key, nil
}

// EncryptSecret 加密敏感字段（密码/私钥/口令）
func EncryptSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	key, err := appKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptSecret 解密敏感字段
func DecryptSecret(cipherB64 string) (string, error) {
	if cipherB64 == "" {
		return "", nil
	}
	key, err := appKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return "", fmt.Errorf("密文格式错误: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("密文长度非法")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}
	return string(plain), nil
}
