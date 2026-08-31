package server

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var db *sql.DB

// InitDB 初始化 SQLite 数据库，创建表并插入默认用户
func InitDB() error {
	slog.Info("正在初始化数据库...")

	// 确保 data 目录存在
	dataDir := filepath.Join(".", "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		slog.Error("创建数据目录失败", "dir", dataDir, "error", err)
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	dbPath := filepath.Join(dataDir, "opsflash.db")
	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		slog.Error("打开数据库失败", "path", dbPath, "error", err)
		return fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置连接池参数
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// 创建用户表、会话表、连接表和隧道表
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS connections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL DEFAULT 'ssh',
			host TEXT NOT NULL,
			port INTEGER NOT NULL DEFAULT 22,
			username TEXT DEFAULT '',
			auth_method TEXT NOT NULL DEFAULT 'password',
			password TEXT DEFAULT '',
			private_key TEXT DEFAULT '',
			private_key_path TEXT DEFAULT '',
			passphrase TEXT DEFAULT '',
			database TEXT DEFAULT '', -- redis DB 索引 / mysql、tdengine 库名
			remark TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS tunnels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			connection_id INTEGER NOT NULL,
			type TEXT NOT NULL DEFAULT 'local',
			local_host TEXT NOT NULL DEFAULT '127.0.0.1',
			local_port INTEGER NOT NULL,
			remote_host TEXT NOT NULL DEFAULT '127.0.0.1',
			remote_port INTEGER NOT NULL DEFAULT 0,
			auto_start INTEGER NOT NULL DEFAULT 0,
			auto_reconnect INTEGER NOT NULL DEFAULT 1,
			group_name TEXT NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 10,
			remark TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (connection_id) REFERENCES connections(id)
		);
		CREATE INDEX IF NOT EXISTS idx_tunnels_connection ON tunnels(connection_id);
		CREATE TABLE IF NOT EXISTS environments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			env_key TEXT NOT NULL UNIQUE,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS scripts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			environment_id INTEGER NOT NULL DEFAULT 0,
			remark TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_env_name ON scripts(environment_id, name);
		CREATE TABLE IF NOT EXISTS commands (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			command TEXT NOT NULL,
			remark TEXT DEFAULT '',
			type TEXT NOT NULL DEFAULT 'non-interactive',
			sort_order INTEGER NOT NULL DEFAULT 10,
			environment_id INTEGER NOT NULL,
			interpreter TEXT NOT NULL DEFAULT 'cmd',
			mode TEXT NOT NULL DEFAULT 'terminal',
			connection_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (environment_id) REFERENCES environments(id),
			FOREIGN KEY (connection_id) REFERENCES connections(id)
		);
		CREATE INDEX IF NOT EXISTS idx_commands_connection ON commands(connection_id);
	`)
	if err != nil {
		slog.Error("创建数据库表失败", "error", err)
		return fmt.Errorf("创建表失败: %w", err)
	}

	// 迁移：为旧库的 connections 表补充 database 列（v0.4.0 数据库执行新增，幂等可重复执行）
	_, _ = db.Exec("ALTER TABLE connections ADD COLUMN database TEXT DEFAULT ''")
	slog.Info("数据库表初始化完成")

	// 如果 environments 表为空，插入默认环境（开发/测试/生产，带英文 key）
	var envCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM environments").Scan(&envCount); err != nil {
		slog.Error("查询环境数量失败", "error", err)
		return fmt.Errorf("查询环境失败: %w", err)
	}
	if envCount == 0 {
		slog.Info("未检测到环境数据，创建默认环境")
		if _, err := db.Exec(`
			INSERT INTO environments (name, env_key, sort_order) VALUES ('开发环境', 'dev', 100);
			INSERT INTO environments (name, env_key, sort_order) VALUES ('测试环境', 'test', 101);
			INSERT INTO environments (name, env_key, sort_order) VALUES ('生产环境', 'prod', 102);
		`); err != nil {
			slog.Error("创建默认环境失败", "error", err)
			return fmt.Errorf("创建默认环境失败: %w", err)
		}
		slog.Info("默认环境创建成功")
	} else {
		slog.Info("已有环境数据", "count", envCount)
	}

	// 检查是否已有用户，若无则插入默认管理员账号
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		slog.Error("查询用户数量失败", "error", err)
		return fmt.Errorf("查询用户失败: %w", err)
	}

	if count == 0 {
		slog.Info("未检测到用户，创建默认管理员账号", "username", "admin")
		hash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.DefaultCost)
		if err != nil {
			slog.Error("密码加密失败", "error", err)
			return fmt.Errorf("密码加密失败: %w", err)
		}
		_, err = db.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, ?, 'admin')", "admin", string(hash))
		if err != nil {
			slog.Error("创建默认用户失败", "error", err)
			return fmt.Errorf("创建默认用户失败: %w", err)
		}
		slog.Info("默认管理员账号创建成功")
	} else {
		slog.Info("已有用户数据", "count", count)
	}

	return nil
}

// CloseDB 关闭数据库连接
func CloseDB() {
	if db != nil {
		db.Close()
	}
}

// generateToken 生成随机会话令牌
func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}

// authenticateUser 验证用户名和密码
func authenticateUser(username, password string) (bool, error) {
	var hash string
	err := db.QueryRow("SELECT password_hash FROM users WHERE username = ?", username).Scan(&hash)
	if err == sql.ErrNoRows {
		slog.Warn("登录失败：用户不存在", "username", username)
		return false, nil
	}
	if err != nil {
		slog.Error("查询用户密码哈希失败", "username", username, "error", err)
		return false, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		slog.Warn("登录失败：密码错误", "username", username)
		return false, nil
	}
	return true, nil
}

// createSession 创建用户会话，返回令牌
func createSession(username string) (string, error) {
	// 删除该用户所有旧会话，确保一个用户只有一个有效 token
	if _, err := db.Exec("DELETE FROM sessions WHERE username = ?", username); err != nil {
		slog.Warn("清理旧会话失败", "username", username, "error", err)
	}

	token := generateToken()
	expiresAt := time.Now().Add(24 * time.Hour)
	_, err := db.Exec(
		"INSERT INTO sessions (username, token, expires_at) VALUES (?, ?, ?)",
		username, token, expiresAt,
	)
	if err != nil {
		slog.Error("创建会话失败", "username", username, "error", err)
		return "", err
	}
	slog.Info("会话创建成功", "username", username, "expiresAt", expiresAt.Format(time.RFC3339))
	return token, nil
}

// validateSession 验证会话令牌，返回用户名和是否有效
func validateSession(token string) (string, bool) {
	var username string
	var expiresAt time.Time
	err := db.QueryRow(
		"SELECT username, expires_at FROM sessions WHERE token = ?",
		token,
	).Scan(&username, &expiresAt)
	if err != nil {
		return "", false
	}
	if time.Now().After(expiresAt) {
		slog.Info("会话已过期，自动清理", "username", username)
		db.Exec("DELETE FROM sessions WHERE token = ?", token)
		return "", false
	}
	return username, true
}

// invalidateSession 使会话令牌失效（登出）
func invalidateSession(token string) error {
	_, err := db.Exec("DELETE FROM sessions WHERE token = ?", token)
	if err != nil {
		slog.Error("删除会话失败", "error", err)
	} else {
		slog.Info("会话已删除（登出）")
	}
	return err
}
