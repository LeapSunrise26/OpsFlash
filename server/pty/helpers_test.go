package pty

import (
	"fmt"
	"strings"
	"time"
)

// drainUntil 持续从 chunks 通道读取直到输出包含目标字符串或超时
func drainUntil(chunks <-chan []byte, target string, timeout time.Duration) (string, error) {
	var out strings.Builder
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				if strings.Contains(out.String(), target) {
					return out.String(), nil
				}
				return out.String(), fmt.Errorf("流已关闭，未找到 %q，实际输出: %q", target, out.String())
			}
			out.Write(chunk)
			if strings.Contains(out.String(), target) {
				return out.String(), nil
			}
		case <-deadline.C:
			return out.String(), fmt.Errorf("等待 %q 超时，实际输出: %q", target, out.String())
		}
	}
}

// startDrain 启动读取 goroutine，将 PTY 输出按块送入通道
func startDrain(tr Transport) <-chan []byte {
	chunks := make(chan []byte, 64)
	go func() {
		defer close(chunks)
		buf := make([]byte, 8192)
		for {
			n, err := tr.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	return chunks
}
