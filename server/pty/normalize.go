package pty

// NormalizePTY 归一化 PTY 原始输出：剥离 ANSI 转义序列，统一 CRLF/CR 为 LF，
// 并将连续 3 行以上的空行折叠为 2 行（控制台初始化时会输出大量清屏序列）
func NormalizePTY(b []byte) []byte {
	b = stripANSI(b)
	out := make([]byte, 0, len(b))
	nlCount := 0
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '\r' {
			if i+1 < len(b) && b[i+1] == '\n' {
				continue // \n 会在下一轮追加
			}
			c = '\n'
		}
		if c == '\n' {
			nlCount++
			if nlCount <= 2 {
				out = append(out, c)
			}
			continue
		}
		nlCount = 0
		out = append(out, c)
	}
	return out
}

// stripANSI 移除 ANSI 转义序列（CSI / OSC / DCS / PM / APC / 单字符转义）
func stripANSI(b []byte) []byte {
	out := make([]byte, 0, len(b))
	i, n := 0, len(b)
	for i < n {
		c := b[i]
		if c != 0x1b {
			out = append(out, c)
			i++
			continue
		}
		if i+1 >= n {
			i++
			continue
		}
		nxt := b[i+1]
		switch {
		case nxt == '[': // CSI：跳过到终结字节 0x40-0x7E
			i += 2
			for i < n && !(b[i] >= 0x40 && b[i] <= 0x7e) {
				i++
			}
			if i < n {
				i++
			}
		case nxt == ']' || nxt == 'P' || nxt == '^' || nxt == '_' || nxt == 'X':
			// OSC/DCS/PM/APC/SOS：跳过到 BEL(0x07) 或 ST(ESC \)
			i += 2
			for i < n {
				if b[i] == 0x07 {
					i++
					break
				}
				if b[i] == 0x1b && i+1 < n && b[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default: // 单字符转义（ESC 7/8/M 等）：丢弃 ESC 与其后一个字节
			i += 2
		}
	}
	return out
}
