package server

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// ==================== 脚本库输出清洗辅助 ====================
// 从主目录 opsruntime.go 抽取的 ConPTY 噪声清洗与输出解码辅助（脚本执行复用），
// 与 exec.DecodeOutput 逻辑一致（本地副本，避免依赖精简版 exec 包）。

// outputDecoder 跨块输出解码器：UTF-8 优先、GBK 兜底，
// 并处理 8192 字节块边界截断的多字节字符（xterm 需要完整 UTF-8 流，不能按块二次解码）。
type outputDecoder struct {
	pending []byte // 上一块末尾未完成的多字节序列
}

// Decode 解码一块输出，返回完整的 UTF-8 字符串（保留 ANSI 控制码）
func (d *outputDecoder) Decode(chunk []byte) string {
	all := append(d.pending, chunk...)
	cut := utf8CompleteLen(all)
	d.pending = append(d.pending[:0], all[cut:]...)
	if cut == 0 {
		return ""
	}
	return decodeOutputLocal(all[:cut])
}

// utf8CompleteLen 返回 b 中最长的"完整 UTF-8 前缀"长度（从末尾扫描最多 4 字节找序列边界）
func utf8CompleteLen(b []byte) int {
	n := len(b)
	for i := n - 1; i >= 0 && i >= n-4; i-- {
		c := b[i]
		if c < 0x80 {
			return i + 1 // ASCII 之后的字节已在之前迭代确认完整
		}
		if utf8.RuneStart(c) {
			_, size := utf8.DecodeRune(b[i:])
			if size > 1 && i+size <= n {
				return i + size // 完整多字节序列
			}
			// 不完整序列（被块边界截断）→ 截断保留到 pending，等待下一块补全
			return i
		}
	}
	return n
}

// decodeOutputLocal 将原始字节解码为 UTF-8 字符串（UTF-8 优先，GBK 兜底）
func decodeOutputLocal(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	decoder := simplifiedchinese.GBK.NewDecoder()
	decoded, err := decoder.Bytes(raw)
	if err != nil {
		return string(raw) // 解码失败，返回原始字符串
	}
	return string(decoded)
}

// ==================== ConPTY 启动噪声清洗 ====================
// Windows 11 ConPTY 在子进程（cmd/powershell/bash 均可）启动时统一注入初始化序列：
//
//	\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H
//
// 其中 \x1b[2J（清屏）+ \x1b[H（光标归位）被原样推给 xterm 会清掉前端已写入的命令行、
// 把输出挤到最顶部、覆盖可见历史。cmd 额外在每条命令输出后输出 \x1b[K\r\n 填满 30 行
// ConPTY 屏幕。这些序列对运维工具无展示价值，统一剥离。
var (
	// ConPTY 统一初始化序列（启用 VT、光标显隐、清屏、重置、归位）
	reConPTYInit = regexp.MustCompile(`\x1b\[\?9001h\x1b\[\?1004h\x1b\[\?25l\x1b\[2J\x1b\[m\x1b\[H`)
	// 兜底：孤立清屏+重置+归位（其他 Windows 版本/终端序列可能有差异）
	reClearHome = regexp.MustCompile(`\x1b\[2J\x1b\[m\x1b\[H`)
	// cmd 填屏：连续"清行+换行"（≥2 次），可尾随光标定位（cmd 把光标放回屏幕顶部附近）
	reCmdFillScreen = regexp.MustCompile(`(?:\x1b\[K\r?\n){2,}(?:\x1b\[[0-9]+;[0-9]+H)?`)
	// 孤立清行残片：\x1b[K 后跟 ESC 序列或行尾（cmd 填屏的最后一个 \x1b[K 无 \r\n）
	reLoneClearK = regexp.MustCompile(`\x1b\[K(\x1b|$)`) // 捕获组保留后随 ESC/结尾，替换为 $1
	// cmd 光标定位序列：\x1b[<行>;<列>H（cmd 用屏幕坐标重绘提示符，剥掉避免 xterm 光标跳位）
	reCmdCursorPos = regexp.MustCompile(`\x1b\[[0-9]+;[0-9]+H`)
	// 光标显隐切换：\x1b[?25l / \x1b[?25h（cmd 重绘提示符时反复切换，剥掉保持 xterm 光标可见）
	reCursorShow = regexp.MustCompile(`\x1b\[\?25[hl]`)
	// OSC 标题序列：\x1b]0;...\x07（设置窗口标题，工具内无意义；跨帧拆分会让 xterm 挂起解析）
	reOSCTitle = regexp.MustCompile(`\x1b\][^\x07]*\x07`)
)

// sanitizeStartupNoise 剥离 ConPTY 初始化清屏序列、cmd 填屏/定位/光标显隐序列与 OSC 标题，
// 避免它们被推给 xterm 导致清屏覆盖历史、输出置顶。
func sanitizeStartupNoise(s string) string {
	s = reConPTYInit.ReplaceAllString(s, "")
	s = reClearHome.ReplaceAllString(s, "")
	s = reCmdFillScreen.ReplaceAllString(s, "")
	s = reLoneClearK.ReplaceAllString(s, "$1")
	s = reCmdCursorPos.ReplaceAllString(s, "")
	s = reCursorShow.ReplaceAllString(s, "")
	s = reOSCTitle.ReplaceAllString(s, "")
	return s
}

// oscStripper 全流跨帧剥离 OSC 标题序列（\x1b]...\x07）。
// 背景：PowerShell 在命令执行**后**输出 OSC 更新窗口标题，且可能被 Read 分块；
// 帧级正则（reOSCTitle 需要完整闭合）跨帧匹配不上，残留的未闭合 OSC 会让 xterm
// 挂起解析等待 \x07。OSC 设置标题对工具无内容价值，任意位置剥离。
type oscStripper struct {
	pending string // 未闭合 OSC 前缀（等待 \x07）
}

func (os *oscStripper) Process(chunk string) string {
	data := os.pending + chunk
	os.pending = ""
	if !strings.Contains(data, "\x1b]") {
		return data
	}
	var out strings.Builder
	for {
		idx := strings.Index(data, "\x1b]")
		if idx < 0 {
			out.WriteString(data)
			break
		}
		out.WriteString(data[:idx])
		rest := data[idx+2:]
		if end := strings.IndexByte(rest, 0x07); end >= 0 {
			data = rest[end+1:] // 完整 OSC，丢弃
			continue
		}
		os.pending = rest // 未闭合 → 缓存等待下一帧
		data = ""
		break
	}
	return out.String()
}

// noiseStripper 跨帧剥离启动阶段噪声（ConPTY 初始化序列常被 Read 分块到达，
// 帧级正则匹配不上 → 按"开头前缀匹配"跨帧累积剥离，遇到第一个非噪声内容即结束）。
type noiseStripper struct {
	pending []byte // 未确认的启动噪声前缀缓存（等待后续帧补齐）
	done    bool
}

// startupNoisePatterns 启动噪声完整序列（长序列在前，避免短序列抢先匹配）。
// 含 \r\n 换行：ConPTYInit/填屏序列之间常夹换行，启动阶段的换行是布局噪声。
var startupNoisePatterns = [][]byte{
	[]byte("\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H"), // ConPTYInit（Win11）
	[]byte("\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H"),
	[]byte("\x1b[?25l\x1b[2J\x1b[m\x1b[H"),
	[]byte("\x1b[?25l"),
	[]byte("\x1b[?25h"),
	[]byte("\x1b[2J"),
	[]byte("\x1b[m"),
	[]byte("\x1b[H"),
	[]byte("\x1b[K"),
	[]byte("\r\n"),
	[]byte("\n"),
	[]byte("\r"),
}

// Process 处理一帧数据：剥离开头的启动噪声，返回应推送的内容。
// 调用方须按序传入所有帧（含空帧无需调用）。
func (ns *noiseStripper) Process(chunk string) string {
	if ns.done {
		return chunk
	}
	data := append([]byte(nil), ns.pending...)
	data = append(data, chunk...)
	ns.pending = ns.pending[:0]

	for len(data) > 0 {
		// 1. OSC 标题序列：\x1b]...\x07（未闭合则缓存等待）
		if data[0] == 0x1b && len(data) > 1 && data[1] == ']' {
			if idx := bytes.IndexByte(data, 0x07); idx >= 0 {
				data = data[idx+1:]
				continue
			}
			ns.pending = append(ns.pending, data...)
			return ""
		}
		// 2. 完整噪声序列
		matched := false
		for _, pat := range startupNoisePatterns {
			if bytes.HasPrefix(data, pat) {
				data = data[len(pat):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// 3. data 是某噪声序列的前缀（需更多帧补齐）
		for _, pat := range startupNoisePatterns {
			if len(data) < len(pat) && bytes.HasPrefix(pat, data) {
				ns.pending = append(ns.pending, data...)
				return ""
			}
		}
		// 4. 非噪声内容 → 启动阶段结束，剩余内容正常推送
		ns.done = true
		return string(data)
	}
	return ""
}
