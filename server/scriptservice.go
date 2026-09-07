package server

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"opsflash/server/cmd"
	"opsflash/server/pty"
)

// ==================== 脚本库模块 ====================
// 脚本库 = data/scripts/ 下的 .bat/.ps1/.sh 素材管理 + 一键执行。
// 设计要点：
//   - 文件平铺于 data/scripts/，文件名即脚本名，扩展名限定 bat/ps1/sh
//   - 元数据存 scripts 表，内容存磁盘（可用任意编辑器修改）
//   - 执行：复用 pty.StartPTY（ConPTY 实时输出 + 可停止），命令包装保证退出码可靠
//   - sh 的 CRLF：执行时自动转 LF 副本（不改源文件）

// Script 脚本信息
type Script struct {
	ID            int    `json:"id"`
	Name          string `json:"name"` // 脚本名（不含扩展名，同环境唯一）
	Type          string `json:"type"` // bat | ps1 | sh
	EnvironmentId int    `json:"environmentId"`
	EnvKey        string `json:"envKey"` // 所属环境 key（磁盘子目录名 data/scripts/{envKey}/）
	Remark        string `json:"remark"`
	Ts            int64  `json:"ts"` // 修改时间（unix 秒）
	Size          int64  `json:"size"`
}

// 请求/响应类型
type ListScriptsRequest struct {
	Token         string `json:"token"`
	EnvironmentId int    `json:"environmentId"`
}

type ReadScriptRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type SaveScriptRequest struct {
	Token         string `json:"token"`
	ID            int    `json:"id"` // 0=新建
	Name          string `json:"name"`
	Type          string `json:"type"`
	Content       string `json:"content"`
	EnvironmentId int    `json:"environmentId"`
	Remark        string `json:"remark"`
}

type DeleteScriptRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type RunScriptRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
	Args  string `json:"args"`
}

type StopScriptRunRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type ScriptsResponse struct {
	Success bool     `json:"success"`
	Scripts []Script `json:"scripts"`
	Message string   `json:"message"`
}

type ScriptResponse struct {
	Success bool    `json:"success"`
	Script  *Script `json:"script"`
	Message string  `json:"message"`
}

type ScriptContentResponse struct {
	Success bool   `json:"success"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Message string `json:"message"`
}

// ScriptRunProgress 脚本执行进度（前端轮询）
type ScriptRunProgress struct {
	Success    bool   `json:"success"`
	Done       bool   `json:"done"`
	Output     string `json:"output"`
	ExitError  string `json:"exitError"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Message    string `json:"message"`
}

// scriptRun 一次脚本执行会话
type scriptRun struct {
	scriptID  int
	name      string
	typ       string
	mu        sync.Mutex
	done      bool
	stopped   bool
	output    []byte
	readPos   int
	exitError string
	exitCode  int
	startedAt time.Time
	duration  int64
	transport pty.Transport
	// cleared 标记会话已被清理（防止旧的延迟清理定时器误删新会话）
	cleared bool
}

// ScriptsService 脚本库服务
type ScriptsService struct {
	mu         sync.Mutex
	scriptRuns map[int]*scriptRun // scriptID -> 执行会话（同一脚本并发只允许一个）
}

// scriptsDir 返回脚本根目录（data/scripts/）
func scriptsDir() string {
	return filepath.Join(".", "data", "scripts")
}

// scriptExts 合法扩展名（不含点）
var scriptExts = map[string]bool{"bat": true, "ps1": true, "sh": true}

// scriptExtToInterpreter 脚本类型 → PTY 解释器
var scriptExtToInterpreter = map[string]string{
	"bat": cmd.InterpreterCmd,
	"ps1": cmd.InterpreterPowerShell,
	"sh":  cmd.InterpreterBash,
}

// scriptPath 返回脚本文件路径：data/scripts/{envKey}/{name}.{typ}
// （相对 cwd；WSL bash 从 Windows 目录启动时继承 cwd 映射为 /mnt/<drive>/...，
// 相对路径可解析，Windows 盘符绝对路径反而无法识别）
func scriptPath(envKey, name, typ string) string {
	return filepath.Join(scriptsDir(), envKey, name+"."+typ)
}

// ensureScriptsDir 确保脚本目录存在
func ensureScriptsDir() error {
	return os.MkdirAll(scriptsDir(), 0755)
}

// ==================== 列表 / 读取 / 保存 / 删除 ====================

// ListScripts 扫描各环境子目录 data/scripts/{envKey}/ 并与 DB 合并返回脚本列表
// 规则：以磁盘文件为准——DB 有记录但文件丢失 → 清理记录；
//      文件存在但 DB 无记录 → 自动补录（便于用户手动放入脚本文件）。
// 子目录名即环境 key；根目录遗留平铺文件兜底归入首个环境并迁移进子目录。
func (s *ScriptsService) ListScripts(req ListScriptsRequest) ScriptsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ScriptsResponse{Success: false, Message: "会话已过期"}
	}
	if err := ensureScriptsDir(); err != nil {
		return ScriptsResponse{Success: false, Message: "创建脚本目录失败: " + err.Error()}
	}

	// 1. 加载环境：key->id、id->key，以及首个环境（兜底）
	envByKey := make(map[string]int)
	idToKey := make(map[int]string)
	var firstEnv int
	erows, err := db.Query("SELECT id, env_key FROM environments ORDER BY sort_order ASC")
	if err != nil {
		slog.Error("查询环境失败", "error", err)
		return ScriptsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	for erows.Next() {
		var id int
		var key string
		if err := erows.Scan(&id, &key); err != nil {
			continue
		}
		envByKey[key] = id
		idToKey[id] = key
		if firstEnv == 0 {
			firstEnv = id
		}
	}
	erows.Close()

	// 2. 扫描磁盘：各环境子目录 + 根目录遗留平铺文件
	type diskFile struct {
		name  string
		typ   string
		envID int
		size  int64
		mod   int64
	}
	disk := make(map[string]*diskFile)
	addDisk := func(fname string, envID int, size, mod int64) {
		dot := strings.LastIndex(fname, ".")
		if dot <= 0 || dot == len(fname)-1 {
			return
		}
		typ := fname[dot+1:]
		if !scriptExts[typ] {
			return // 忽略非法扩展名
		}
		disk[strings.ToLower(fname[:dot]+"."+typ)] = &diskFile{
			name: fname[:dot], typ: typ, envID: envID, size: size, mod: mod,
		}
	}

	root := scriptsDir()
	// 2a. 各环境子目录 data/scripts/{envKey}/
	for key, envID := range envByKey {
		if key == "" {
			continue
		}
		fes, serr := os.ReadDir(filepath.Join(root, key))
		if serr != nil {
			continue
		}
		for _, fe := range fes {
			if fe.IsDir() {
				continue
			}
			info, _ := fe.Info()
			addDisk(fe.Name(), envID, info.Size(), info.ModTime().Unix())
		}
	}
	// 2b. 根目录遗留平铺文件（迁移兜底）：归入首个环境并移入子目录
	if firstEnv > 0 {
		rents, rerr := os.ReadDir(root)
		if rerr == nil {
			tkey := idToKey[firstEnv]
			if tkey == "" {
				tkey = fmt.Sprintf("env_%d", firstEnv)
			}
			sub := filepath.Join(root, tkey)
			for _, fe := range rents {
				if fe.IsDir() {
					continue
				}
				fname := fe.Name()
				k := strings.ToLower(fname)
				if !strings.Contains(k, ".") {
					continue
				}
				typ := fname[strings.LastIndex(fname, ".")+1:]
				if !scriptExts[typ] {
					continue
				}
				if _, exists := disk[strings.ToLower(fname[:strings.LastIndex(fname, ".")]+"."+typ)]; exists {
					continue // 已被子目录收录
				}
				_ = os.MkdirAll(sub, 0755)
				old := filepath.Join(root, fname)
				newp := filepath.Join(sub, fname)
				if old != newp {
					if rerr2 := os.Rename(old, newp); rerr2 != nil {
						slog.Warn("迁移遗留脚本文件失败", "file", fname, "error", rerr2)
						continue
					}
					slog.Info("迁移遗留脚本文件", "file", fname, "to", tkey)
				}
				info, _ := fe.Info()
				addDisk(fname, firstEnv, info.Size(), info.ModTime().Unix())
			}
		}
	}

	// 3. 读 DB 记录（全部环境）
	rows, err := db.Query(`SELECT id, name, type, environment_id, remark, ts FROM scripts ORDER BY name ASC`)
	if err != nil {
		slog.Error("查询脚本记录失败", "error", err)
		return ScriptsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	type dbRec struct {
		id, envID int
		name, typ, remark string
		ts       int64
	}
	var dbRows []dbRec
	for rows.Next() {
		var r dbRec
		if err := rows.Scan(&r.id, &r.name, &r.typ, &r.envID, &r.remark, &r.ts); err != nil {
			continue
		}
		dbRows = append(dbRows, r)
	}

	// 4. 合并：以磁盘为准（磁盘子目录决定真实归属环境）
	var result []Script
	for _, r := range dbRows {
		fname := strings.ToLower(r.name + "." + r.typ)
		f, ok := disk[fname]
		if !ok {
			// 文件丢失 → 清理 DB 记录
			_, _ = db.Exec("DELETE FROM scripts WHERE id = ?", r.id)
			slog.Warn("脚本文件丢失，清理记录", "id", r.id, "name", r.name)
			continue
		}
		// 磁盘实际环境（可能已迁移）与 DB 不一致 → 以磁盘为准更新
		dbEnvID := r.envID
		if f.envID != dbEnvID {
			db.Exec("UPDATE scripts SET environment_id = ? WHERE id = ?", f.envID, r.id)
			dbEnvID = f.envID
		}
		result = append(result, Script{
			ID: r.id, Name: r.name, Type: r.typ, EnvironmentId: dbEnvID,
			EnvKey: idToKey[dbEnvID], Remark: r.remark, Ts: f.mod, Size: f.size,
		})
		delete(disk, fname) // 磁盘已消费
	}
	// 磁盘有但 DB 无 → 自动补录
	for _, f := range disk {
		res, err := db.Exec(`INSERT INTO scripts (name, type, environment_id, remark, ts) VALUES (?, ?, ?, '', ?)`,
			f.name, f.typ, f.envID, f.mod)
		if err != nil {
			slog.Warn("补录脚本记录失败", "name", f.name, "error", err)
			continue
		}
		id, _ := res.LastInsertId()
		result = append(result, Script{ID: int(id), Name: f.name, Type: f.typ,
			EnvironmentId: f.envID, EnvKey: idToKey[f.envID], Ts: f.mod, Size: f.size})
	}

	return ScriptsResponse{Success: true, Scripts: result}
}

// loadScriptByID 加载脚本记录（同时带出所属环境 key，用于构造磁盘路径）
func loadScriptByID(id int) (*Script, error) {
	var sc Script
	err := db.QueryRow(`SELECT s.id, s.name, s.type, s.environment_id, s.remark, s.ts,
		COALESCE((SELECT env_key FROM environments WHERE id = s.environment_id), '') AS env_key
		FROM scripts s WHERE s.id = ?`, id).
		Scan(&sc.ID, &sc.Name, &sc.Type, &sc.EnvironmentId, &sc.Remark, &sc.Ts, &sc.EnvKey)
	if err != nil {
		return nil, errors.New("脚本不存在")
	}
	return &sc, nil
}

// ReadScript 读取脚本内容（文件丢失 → 清理 DB 记录并报错）
func (s *ScriptsService) ReadScript(req ReadScriptRequest) ScriptContentResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ScriptContentResponse{Success: false, Message: "会话已过期"}
	}
	sc, err := loadScriptByID(req.ID)
	if err != nil {
		return ScriptContentResponse{Success: false, Message: err.Error()}
	}
	path := scriptPath(sc.EnvKey, sc.Name, sc.Type)
	content, err := os.ReadFile(path)
	if err != nil {
		_, _ = db.Exec("DELETE FROM scripts WHERE id = ?", sc.ID)
		slog.Warn("读取脚本文件失败，清理记录", "id", sc.ID, "name", sc.Name, "error", err)
		return ScriptContentResponse{Success: false, Message: "脚本文件不存在或已被移除"}
	}
	return ScriptContentResponse{Success: true, Name: sc.Name, Type: sc.Type, Content: string(content)}
}

// SaveScript 新建（id=0）或保存脚本：覆盖文件 + 更新 DB
func (s *ScriptsService) SaveScript(req SaveScriptRequest) ScriptResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ScriptResponse{Success: false, Message: "会话已过期"}
	}
	name := strings.TrimSpace(req.Name)
	typ := strings.ToLower(strings.TrimSpace(req.Type))
	if name == "" {
		return ScriptResponse{Success: false, Message: "脚本名称不能为空"}
	}
	if !scriptExts[typ] {
		return ScriptResponse{Success: false, Message: "脚本类型仅支持 bat / ps1 / sh"}
	}
	if strings.TrimSpace(req.Content) == "" {
		return ScriptResponse{Success: false, Message: "脚本内容不能为空"}
	}
	if err := ensureScriptsDir(); err != nil {
		return ScriptResponse{Success: false, Message: "创建脚本目录失败: " + err.Error()}
	}
	// 脚本必须归属一个环境（去除「通用」概念）
	if req.EnvironmentId <= 0 {
		return ScriptResponse{Success: false, Message: "请选择所属环境"}
	}
	var envKey string
	if err := db.QueryRow("SELECT env_key FROM environments WHERE id = ?", req.EnvironmentId).Scan(&envKey); err != nil {
		return ScriptResponse{Success: false, Message: "所属环境不存在"}
	}
	if envKey == "" {
		envKey = fmt.Sprintf("env_%d", req.EnvironmentId)
	}
	// 确保环境子目录存在
	if err := os.MkdirAll(filepath.Join(scriptsDir(), envKey), 0755); err != nil {
		return ScriptResponse{Success: false, Message: "创建环境目录失败: " + err.Error()}
	}

	now := time.Now().Unix()

	// 新建：重名校验（同环境唯一）
	if req.ID == 0 {
		var cnt int
		_ = db.QueryRow(`SELECT COUNT(*) FROM scripts WHERE environment_id = ? AND name = ? AND type = ?`,
			req.EnvironmentId, name, typ).Scan(&cnt)
		if cnt > 0 {
			return ScriptResponse{Success: false, Message: "脚本「" + name + "." + typ + "」已存在"}
		}
	} else {
		sc, err := loadScriptByID(req.ID)
		if err != nil {
			return ScriptResponse{Success: false, Message: err.Error()}
		}
		// 重命名场景：校验新名字不与其他记录冲突（排除自身）
		var cnt int
		_ = db.QueryRow(`SELECT COUNT(*) FROM scripts WHERE environment_id = ? AND name = ? AND type = ? AND id != ?`,
			req.EnvironmentId, name, typ, req.ID).Scan(&cnt)
		if cnt > 0 {
			return ScriptResponse{Success: false, Message: "脚本「" + name + "." + typ + "」已存在"}
		}
		// 文件名/类型/环境变化时删除旧文件
		if sc.Name != name || sc.Type != typ || sc.EnvironmentId != req.EnvironmentId {
			_ = os.Remove(scriptPath(sc.EnvKey, sc.Name, sc.Type))
		}
	}

	path := scriptPath(envKey, name, typ)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		slog.Error("写入脚本文件失败", "path", path, "error", err)
		return ScriptResponse{Success: false, Message: "保存失败: " + err.Error()}
	}

	if req.ID == 0 {
		res, err := db.Exec(`INSERT INTO scripts (name, type, environment_id, remark, ts) VALUES (?, ?, ?, ?, ?)`,
			name, typ, req.EnvironmentId, strings.TrimSpace(req.Remark), now)
		if err != nil {
			slog.Error("插入脚本记录失败", "error", err)
			return ScriptResponse{Success: false, Message: "保存失败: " + err.Error()}
		}
		id, _ := res.LastInsertId()
		return ScriptResponse{Success: true, Script: &Script{ID: int(id), Name: name, Type: typ,
			EnvironmentId: req.EnvironmentId, Remark: strings.TrimSpace(req.Remark), Ts: now}}
	}

	_, err := db.Exec(`UPDATE scripts SET name = ?, type = ?, environment_id = ?, remark = ?, ts = ? WHERE id = ?`,
		name, typ, req.EnvironmentId, strings.TrimSpace(req.Remark), now, req.ID)
	if err != nil {
		slog.Error("更新脚本记录失败", "error", err)
		return ScriptResponse{Success: false, Message: "保存失败: " + err.Error()}
	}
	return ScriptResponse{Success: true, Script: &Script{ID: req.ID, Name: name, Type: typ,
		EnvironmentId: req.EnvironmentId, Remark: strings.TrimSpace(req.Remark), Ts: now}}
}

// DeleteScript 删除脚本（文件 + DB 记录）
func (s *ScriptsService) DeleteScript(req DeleteScriptRequest) ScriptResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ScriptResponse{Success: false, Message: "会话已过期"}
	}
	sc, err := loadScriptByID(req.ID)
	if err != nil {
		return ScriptResponse{Success: false, Message: err.Error()}
	}
	// 停止运行中的执行
	s.stopScriptRun(req.ID)
	_ = os.Remove(scriptPath(sc.EnvKey, sc.Name, sc.Type))
	_, err = db.Exec("DELETE FROM scripts WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("删除脚本记录失败", "id", req.ID, "error", err)
		return ScriptResponse{Success: false, Message: "删除失败: " + err.Error()}
	}
	slog.Info("脚本删除成功", "id", req.ID, "name", sc.Name+"."+sc.Type)
	return ScriptResponse{Success: true, Message: "脚本已删除"}
}

// ==================== 脚本执行 ====================

// scriptWrapCommand 构造脚本执行的 PTY 命令行（保证退出码可靠）
//   - bat: cmd /Q /s /c "chcp 65001 >nul && call "<abs>" <args> & exit /b %errorlevel%"
//   - ps1: powershell -NoProfile -ExecutionPolicy Bypass -File "<abs>" <args>; exit $LASTEXITCODE
//   - sh:  bash "<bash-abs>" <args>  （Windows 走 bash：路径经 ToBashPath 转为 bash 挂载路径，退出码天然透传）
func scriptWrapCommand(absPath, typ, args string) string {
	args = strings.TrimSpace(args)
	switch typ {
	case "bat":
		inner := fmt.Sprintf(`call "%s"`, absPath)
		if args != "" {
			inner += " " + args
		}
		return inner + " & exit /b %errorlevel%"
	case "ps1":
		inner := fmt.Sprintf(`& "%s"`, absPath)
		if args != "" {
			inner += " " + args
		}
		return inner + "; exit $LASTEXITCODE"
	default: // sh
		// Windows：Windows 路径 bash 无法识别，转为 bash 自身挂载路径（WSL /mnt/d、Git Bash /d 等）
		inner := fmt.Sprintf(`"%s"`, cmd.ToBashPath(absPath))
		if args != "" {
			inner += " " + args
		}
		return inner
	}
}

// prepareScriptForExec 准备执行：
//   - 读文件内容，sh 含 CRLF 时写 LF 副本（不改源文件）
//   - 返回实际执行路径 + 临时副本路径（需清理）
func prepareScriptForExec(sc *Script) (execPath string, cleanup string, err error) {
	path := scriptPath(sc.EnvKey, sc.Name, sc.Type)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", errors.New("脚本文件不存在或已被移除")
	}
	if sc.Type == "sh" && bytesContainsCR(content) {
		// CRLF → LF 副本
		tmp := path + ".lf.tmp"
		lf := strings.ReplaceAll(string(content), "\r\n", "\n")
		if err := os.WriteFile(tmp, []byte(lf), 0644); err != nil {
			return "", "", errors.New("创建脚本临时副本失败: " + err.Error())
		}
		return tmp, tmp, nil
	}
	return path, "", nil
}

// bytesContainsCR 检测字节流是否含 \r（判断 CRLF）
func bytesContainsCR(b []byte) bool {
	for _, c := range b {
		if c == '\r' {
			return true
		}
	}
	return false
}

// RunScript 启动脚本执行（异步：立即返回，输出经 GetScriptRunProgress 轮询获取）
func (s *ScriptsService) RunScript(req RunScriptRequest) ScriptRunProgress {
	if _, ok := validateSession(req.Token); !ok {
		return ScriptRunProgress{Success: false, Message: "会话已过期"}
	}
	sc, err := loadScriptByID(req.ID)
	if err != nil {
		return ScriptRunProgress{Success: false, Message: err.Error()}
	}

	s.mu.Lock()
	if s.scriptRuns == nil {
		s.scriptRuns = make(map[int]*scriptRun)
	}
	if existing, ok := s.scriptRuns[req.ID]; ok {
		existing.mu.Lock()
		active := !existing.done
		existing.mu.Unlock()
		if active {
			s.mu.Unlock()
			return ScriptRunProgress{Success: false, Message: "该脚本正在执行中"}
		}
	}
	run := &scriptRun{
		scriptID:  req.ID,
		name:      sc.Name,
		typ:       sc.Type,
		startedAt: time.Now(),
	}
	s.scriptRuns[req.ID] = run
	s.mu.Unlock()

	// 构造执行命令（sh CRLF 转副本）
	execPath, cleanup, err := prepareScriptForExec(sc)
	if err != nil {
		s.clearScriptRun(req.ID)
		return ScriptRunProgress{Success: false, Message: err.Error()}
	}

	wrapped := scriptWrapCommand(execPath, sc.Type, req.Args)
	interpreter := scriptExtToInterpreter[sc.Type]
	slog.Info("脚本开始执行", "id", req.ID, "name", sc.Name+"."+sc.Type, "args", req.Args, "crlfConverted", cleanup != "")

	transport, err := pty.StartPTY(wrapped, interpreter)
	if err != nil {
		s.clearScriptRun(req.ID)
		slog.Error("脚本启动失败", "id", req.ID, "error", err)
		return ScriptRunProgress{Success: false, Message: "启动失败: " + err.Error()}
	}
	run.mu.Lock()
	run.transport = transport
	run.mu.Unlock()

	go s.runScriptProcess(run, transport, cleanup)

	return ScriptRunProgress{Success: true, Done: false, Message: "脚本已开始执行"}
}

// runScriptProcess 后台执行脚本：读取输出 + 等待退出码 + 标记结束
// 注意：结束后**保留 run 在 map 中**（前端轮询拿到最终输出与退出码后再清理；
// 下一次 RunScript 同脚本会覆盖该会话）
func (s *ScriptsService) runScriptProcess(run *scriptRun, transport pty.Transport, cleanup string) {
	if cleanup != "" {
		defer os.Remove(cleanup)
	}

	// 读取 goroutine（与 runStreamLines 一致：跨帧剥噪声 + OSC + 实时累积）
	decoder := &outputDecoder{}
	buf := make([]byte, 8192)
	readDone := make(chan struct{})
	stripper := &noiseStripper{}
	osc := &oscStripper{}
	go func() {
		defer close(readDone)
		for {
			n, rerr := transport.Read(buf)
			if n > 0 {
				data := osc.Process(sanitizeStartupNoise(stripper.Process(decoder.Decode(buf[:n]))))
				if data != "" {
					run.mu.Lock()
					if len(run.output) < 100000 {
						run.output = append(run.output, []byte(data)...)
					}
					run.mu.Unlock()
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// 等待结束（Wait 取退出码；ConPTY 管道不保证 EOF → 静默排空）
	werr := transport.Wait()
	select {
	case <-readDone:
	case <-time.After(400 * time.Millisecond):
		transport.Close()
		<-readDone
	}
	transport.Close()

	run.mu.Lock()
	run.done = true
	run.duration = time.Since(run.startedAt).Milliseconds()
	if werr != nil {
		run.exitError = werr.Error()
		run.exitCode = exitCodeFromErr(werr)
	}
	run.transport = nil
	run.mu.Unlock()

	slog.Info("脚本执行结束", "id", run.scriptID, "name", run.name+"."+run.typ,
		"durationMs", run.duration, "exitCode", run.exitCode, "error", run.exitError)

	// 延迟清理：保留会话供前端轮询取最终输出（10s 后自动移除）
	// 竞态保护：清理前校验 map 中仍是本会话（防止旧定时器误删新会话）
	time.AfterFunc(10*time.Second, func() {
		s.mu.Lock()
		cur, exists := s.scriptRuns[run.scriptID]
		if exists && cur == run {
			delete(s.scriptRuns, run.scriptID)
		}
		s.mu.Unlock()
	})
}

// clearScriptRun 从执行会话表移除（保留 run 对象供最后一次快照）
func (s *ScriptsService) clearScriptRun(id int) {
	s.mu.Lock()
	delete(s.scriptRuns, id)
	s.mu.Unlock()
}

// exitCodeFromErr 从 Wait() 返回的 error 中提取退出码（失败时返回 1 兜底）
func exitCodeFromErr(err error) int {
	type exitCoder interface{ ExitCode() int }
	if ec, ok := err.(exitCoder); ok {
		return ec.ExitCode()
	}
	return 1
}

// GetScriptRunProgress 获取脚本执行进度（前端轮询）
func (s *ScriptsService) GetScriptRunProgress(req ReadScriptRequest) ScriptRunProgress {
	if _, ok := validateSession(req.Token); !ok {
		return ScriptRunProgress{Success: false, Message: "会话已过期"}
	}
	s.mu.Lock()
	run, exists := s.scriptRuns[req.ID]
	s.mu.Unlock()
	if !exists {
		return ScriptRunProgress{Success: false, Message: "脚本未在执行"}
	}
	return s.snapshotScriptRun(run, req.ID)
}

// snapshotScriptRun 生成进度快照（增量输出）
func (s *ScriptsService) snapshotScriptRun(run *scriptRun, id int) ScriptRunProgress {
	run.mu.Lock()
	defer run.mu.Unlock()
	var newOutput string
	if run.readPos < len(run.output) {
		newOutput = string(run.output[run.readPos:])
		run.readPos = len(run.output)
	}
	return ScriptRunProgress{
		Success:    true,
		Done:       run.done,
		Output:     newOutput,
		ExitError:  run.exitError,
		ExitCode:   run.exitCode,
		DurationMs: run.duration,
	}
}

// StopScriptRun 停止执行中脚本
func (s *ScriptsService) StopScriptRun(req StopScriptRunRequest) ScriptRunProgress {
	if _, ok := validateSession(req.Token); !ok {
		return ScriptRunProgress{Success: false, Message: "会话已过期"}
	}
	s.stopScriptRun(req.ID)
	s.mu.Lock()
	run, exists := s.scriptRuns[req.ID]
	s.mu.Unlock()
	if !exists {
		return ScriptRunProgress{Success: true, Done: true, Message: "脚本已停止"}
	}
	run.mu.Lock()
	run.stopped = true
	run.mu.Unlock()
	return s.snapshotScriptRun(run, req.ID)
}

// stopScriptRun 终止脚本执行（Kill 进程树）
func (s *ScriptsService) stopScriptRun(id int) {
	s.mu.Lock()
	run, exists := s.scriptRuns[id]
	s.mu.Unlock()
	if !exists {
		return
	}
	run.mu.Lock()
	if run.transport != nil {
		_ = run.transport.Kill()
		_ = run.transport.Close()
	}
	run.mu.Unlock()
}
