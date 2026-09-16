package pty

import "testing"

// ==================== 输出归一化单元测试（全平台）====================

func TestNormalizePTY(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"CRLF转LF", "a\r\nb", "a\nb"},
		{"独立CR转LF", "a\rb", "a\nb"},
		{"无CR原样", "a\nb", "a\nb"},
		{"剥离CSI颜色", "\x1b[31mred\x1b[0m", "red"},
		{"剥离CSI光标移动", "ab\x1b[2Kcd", "abcd"},
		{"剥离OSC标题", "x\x1b]0;title\x07y", "xy"},
		{"剥离单字符转义", "\x1b7a\x1b8b", "ab"},
		{"混合", "a\r\n\x1b[1;32mok\x1b[0m\r", "a\nok\n"},
		{"折叠连续空行", "a\n\n\n\n\nb", "a\n\nb"},
		{"清屏序列折叠", "\x1b[2J\x1b[K\r\n\x1b[K\r\n\x1b[K\r\nx", "\n\nx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(NormalizePTY([]byte(c.in))); got != c.want {
				t.Fatalf("NormalizePTY(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
