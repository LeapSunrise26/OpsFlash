//go:build windows

package secret

import (
	"encoding/base64"
	"fmt"
	"syscall"
	"unsafe"
)

// ==================== Windows 凭据加密（DPAPI）====================
// 使用 CryptProtectData / CryptUnprotectData（crypt32.dll），
// 密钥绑定当前 Windows 用户，无需额外口令，密文经 base64 存储。
// 空字符串明文不做加密（直接返回 ""），解密时同样原样返回。

// dataBlob 与 Windows DATA_BLOB 结构一致
type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

// EncryptSecret 加密敏感字段（密码/私钥/口令）
func EncryptSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	b := []byte(plain)
	var in dataBlob
	in.cbData = uint32(len(b))
	in.pbData = &b[0]

	var out dataBlob
	r1, _, e1 := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)), // pDataIn
		0,                            // szDataDescr = NULL
		0,                            // pOptionalEntropy = NULL
		0,                            // pvReserved = NULL
		0,                            // pPromptStruct = NULL
		0,                            // dwFlags = 0
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return "", fmt.Errorf("CryptProtectData 失败: %v", e1)
	}
	// 释放 DPAPI 分配的输出缓冲区
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))

	data := unsafe.Slice(out.pbData, out.cbData)
	return base64.StdEncoding.EncodeToString(data), nil
}

// DecryptSecret 解密敏感字段
func DecryptSecret(cipherB64 string) (string, error) {
	if cipherB64 == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return "", fmt.Errorf("密文格式错误: %w", err)
	}
	if len(data) == 0 {
		return "", nil
	}
	var in dataBlob
	in.cbData = uint32(len(data))
	in.pbData = &data[0]

	var out dataBlob
	r1, _, e1 := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return "", fmt.Errorf("CryptUnprotectData 失败: %v", e1)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))

	dec := unsafe.Slice(out.pbData, out.cbData)
	return string(dec), nil
}
