package secret

import "testing"

// TestSecretRoundTrip 验证加密-解密往返一致性
// 空字符串保持为空；非空明文解密后与原文一致
func TestSecretRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"simple",
		"P@ssw0rd! 中文密码 密码",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nMIIE...\n-----END OPENSSH PRIVATE KEY-----\n",
		"a very long password: xyz1234567890",
	}
	for _, plain := range cases {
		cipherB64, err := EncryptSecret(plain)
		if err != nil {
			t.Fatalf("EncryptSecret(%q) 失败: %v", plain, err)
		}
		dec, err := DecryptSecret(cipherB64)
		if err != nil {
			t.Fatalf("DecryptSecret(%q) 失败: %v", cipherB64, err)
		}
		if dec != plain {
			t.Errorf("往返不一致: got %q, want %q", dec, plain)
		}
	}
}
