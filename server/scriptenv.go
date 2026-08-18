package server

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ==================== 脚本库环境管理 ====================
// v0.2.0：环境 CRUD 挂在 ScriptsService 上（与主目录的 OpsService 同源逻辑）。
// 每个环境有英文 key（作脚本磁盘子目录名 data/scripts/{key}/）与排序值；
// 删除环境时将其脚本迁移到目标环境（磁盘目录 + DB 一并迁移），不保留「通用」概念。

// Environment 环境信息
type Environment struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`       // 英文 key，作脚本磁盘子目录名 data/scripts/{key}/
	SortOrder int    `json:"sortOrder"`
	CreatedAt string `json:"createdAt"`
}

type GetEnvironmentsRequest struct {
	Token string `json:"token"`
}

type EnvironmentsResponse struct {
	Success      bool          `json:"success"`
	Environments []Environment `json:"environments"`
	Message      string        `json:"message"`
}

type CreateEnvironmentRequest struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	SortOrder int    `json:"sortOrder"`
}

type UpdateEnvironmentRequest struct {
	Token     string `json:"token"`
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	SortOrder int    `json:"sortOrder"`
}

type DeleteEnvironmentRequest struct {
	Token    string `json:"token"`
	ID       int    `json:"id"`
	TargetId int    `json:"targetId"` // 删除时脚本迁移目标环境（不能等于自身；至少保留一个环境）
}

type DeleteEnvResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	DeletedCount int    `json:"deletedCount"`
}

// GetEnvironments 获取环境列表
func (s *ScriptsService) GetEnvironments(req GetEnvironmentsRequest) EnvironmentsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return EnvironmentsResponse{Success: false, Message: "会话已过期"}
	}

	rows, err := db.Query("SELECT id, name, env_key, sort_order, created_at FROM environments ORDER BY sort_order ASC")
	if err != nil {
		slog.Error("查询环境列表失败", "error", err)
		return EnvironmentsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var envs []Environment
	for rows.Next() {
		var env Environment
		if err := rows.Scan(&env.ID, &env.Name, &env.Key, &env.SortOrder, &env.CreatedAt); err != nil {
			slog.Error("扫描环境记录失败", "error", err)
			continue
		}
		envs = append(envs, env)
	}
	return EnvironmentsResponse{Success: true, Environments: envs}
}

// CreateEnvironment 创建环境（含英文 key 与排序）
func (s *ScriptsService) CreateEnvironment(req CreateEnvironmentRequest) EnvironmentsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return EnvironmentsResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return EnvironmentsResponse{Success: false, Message: "环境名称不能为空"}
	}
	key := sanitizeEnvKey(req.Key)
	if key == "" {
		return EnvironmentsResponse{Success: false, Message: "环境 key 不能为空（仅限小写字母、数字、_、-）"}
	}
	// key 唯一性
	var cnt int
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE env_key = ?", key).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境 key 已存在"}
	}
	// 名称唯一性
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE name = ?", name).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境名称已存在"}
	}

	sortOrder := req.SortOrder
	if sortOrder <= 0 {
		var maxOrder int
		db.QueryRow("SELECT COALESCE(MAX(sort_order), 99) FROM environments").Scan(&maxOrder)
		sortOrder = maxOrder + 1
	}

	result, err := db.Exec("INSERT INTO environments (name, env_key, sort_order) VALUES (?, ?, ?)", name, key, sortOrder)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return EnvironmentsResponse{Success: false, Message: "环境名称或 key 已存在"}
		}
		slog.Error("创建环境失败", "name", name, "error", err)
		return EnvironmentsResponse{Success: false, Message: "创建失败: " + err.Error()}
	}

	id, _ := result.LastInsertId()
	slog.Info("环境创建成功", "id", id, "name", name, "key", key)

	return s.GetEnvironments(GetEnvironmentsRequest{Token: req.Token})
}

// UpdateEnvironment 编辑环境（名称 / key / 排序）；key 变更会同步迁移磁盘脚本目录
func (s *ScriptsService) UpdateEnvironment(req UpdateEnvironmentRequest) EnvironmentsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return EnvironmentsResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return EnvironmentsResponse{Success: false, Message: "环境名称不能为空"}
	}
	key := sanitizeEnvKey(req.Key)
	if key == "" {
		return EnvironmentsResponse{Success: false, Message: "环境 key 不能为空（仅限小写字母、数字、_、-）"}
	}

	// 原记录
	var oldName, oldKey string
	err := db.QueryRow("SELECT name, env_key FROM environments WHERE id = ?", req.ID).Scan(&oldName, &oldKey)
	if err == sql.ErrNoRows {
		return EnvironmentsResponse{Success: false, Message: "环境不存在"}
	}
	if err != nil {
		return EnvironmentsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 名称唯一（排除自身）
	var cnt int
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE name = ? AND id != ?", name, req.ID).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境名称已存在"}
	}
	// key 唯一（排除自身）
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE env_key = ? AND id != ?", key, req.ID).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境 key 已存在"}
	}

	sortOrder := req.SortOrder
	if sortOrder <= 0 {
		sortOrder = 100
	}

	if _, err := db.Exec("UPDATE environments SET name = ?, env_key = ?, sort_order = ? WHERE id = ?",
		name, key, sortOrder, req.ID); err != nil {
		slog.Error("更新环境失败", "id", req.ID, "error", err)
		return EnvironmentsResponse{Success: false, Message: "更新失败: " + err.Error()}
	}

	// key 变更 → 迁移磁盘脚本目录 data/scripts/{oldKey} → data/scripts/{key}
	if oldKey != "" && oldKey != key {
		moveScriptsDir(oldKey, key)
	}

	slog.Info("环境更新成功", "id", req.ID, "name", name, "key", key)
	return s.GetEnvironments(GetEnvironmentsRequest{Token: req.Token})
}

// sanitizeEnvKey 清洗英文 key：仅保留小写字母、数字、下划线、连字符
func sanitizeEnvKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// moveScriptsDir 将脚本从 data/scripts/{oldKey}/ 迁移到 data/scripts/{newKey}/
// （合并内容并删除旧目录；任一为空或相等则跳过）
func moveScriptsDir(oldKey, newKey string) {
	if oldKey == "" || newKey == "" || oldKey == newKey {
		return
	}
	root := scriptsDir()
	oldDir := filepath.Join(root, oldKey)
	if _, err := os.Stat(oldDir); err != nil {
		return // 旧目录不存在（可能本就无脚本）
	}
	newDir := filepath.Join(root, newKey)
	_ = os.MkdirAll(newDir, 0755)
	if fes, err := os.ReadDir(oldDir); err == nil {
		for _, fe := range fes {
			if fe.IsDir() {
				continue
			}
			_ = os.Rename(filepath.Join(oldDir, fe.Name()), filepath.Join(newDir, fe.Name()))
		}
	}
	_ = os.Remove(oldDir)
	slog.Info("脚本目录已迁移", "from", oldKey, "to", newKey)
}

// DeleteEnvironment 删除环境（脚本迁移到目标环境，磁盘目录一并迁移）
func (s *ScriptsService) DeleteEnvironment(req DeleteEnvironmentRequest) DeleteEnvResponse {
	if _, ok := validateSession(req.Token); !ok {
		return DeleteEnvResponse{Success: false, Message: "会话已过期"}
	}
	if req.TargetId == req.ID {
		return DeleteEnvResponse{Success: false, Message: "脚本迁移目标不能是自身"}
	}

	var envName, oldKey string
	err := db.QueryRow("SELECT name, env_key FROM environments WHERE id = ?", req.ID).Scan(&envName, &oldKey)
	if err == sql.ErrNoRows {
		return DeleteEnvResponse{Success: false, Message: "环境不存在"}
	}
	if err != nil {
		return DeleteEnvResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 至少保留一个环境
	var otherCount int
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE id != ?", req.ID).Scan(&otherCount)
	if otherCount == 0 {
		return DeleteEnvResponse{Success: false, Message: "至少保留一个环境，无法删除"}
	}

	// 目标环境校验
	var targetName, newKey string
	if req.TargetId > 0 {
		terr := db.QueryRow("SELECT name, env_key FROM environments WHERE id = ?", req.TargetId).Scan(&targetName, &newKey)
		if terr == sql.ErrNoRows {
			return DeleteEnvResponse{Success: false, Message: "目标环境不存在"}
		}
		if terr != nil {
			return DeleteEnvResponse{Success: false, Message: "查询失败: " + terr.Error()}
		}
	} else {
		// 兜底：挂首个其他环境（前端通常已传目标）
		var fallback int
		var fbKey string
		db.QueryRow("SELECT MIN(id), COALESCE((SELECT env_key FROM environments WHERE id = (SELECT MIN(id) FROM environments WHERE id != ?)), '')", req.ID).Scan(&fallback, &fbKey)
		if fallback > 0 {
			req.TargetId = fallback
			newKey = fbKey
			if newKey == "" {
				newKey = fmt.Sprintf("env_%d", fallback)
			}
			var tn string
			db.QueryRow("SELECT name FROM environments WHERE id = ?", fallback).Scan(&tn)
			targetName = tn
		}
	}

	// 统计该环境脚本数（用于返回）
	var scriptCount int
	db.QueryRow("SELECT COUNT(*) FROM scripts WHERE environment_id = ?", req.ID).Scan(&scriptCount)

	// 脚本迁移到目标环境（磁盘目录 + DB）
	if req.TargetId > 0 {
		moveScriptsDir(oldKey, newKey)
		db.Exec("UPDATE scripts SET environment_id = ? WHERE environment_id = ?", req.TargetId, req.ID)
	}

	_, err = db.Exec("DELETE FROM environments WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("删除环境失败", "id", req.ID, "error", err)
		return DeleteEnvResponse{Success: false, Message: "删除失败: " + err.Error()}
	}

	slog.Info("环境删除成功", "id", req.ID, "name", envName, "targetKey", newKey, "migratedScripts", scriptCount)
	return DeleteEnvResponse{Success: true, Message: "环境「" + envName + "」已删除，脚本迁移至「" + targetName + "」", DeletedCount: scriptCount}
}
