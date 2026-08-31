package server

import (
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"opsflash/server/exec"
	"opsflash/server/secret"
)

// ==================== 连接管理（数据层 + 管理 API）====================
// 连接（Connection）= 服务器 / 数据库服务信息描述（ssh / redis / mysql / tdengine）。
// 敏感字段（密码/私钥/口令）经 secret 包加密后落库，接口返回时脱敏。

// Connection 连接信息
type Connection struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`       // 连接名称（唯一）
	Type           string `json:"type"`       // ssh | redis | mysql | tdengine
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`   // ssh/mysql/tdengine 使用
	AuthMethod     string `json:"authMethod"` // ssh: password | key
	Password       string `json:"password"`   // 库中加密存储，返回时脱敏
	PrivateKey     string `json:"privateKey"` // 私钥 PEM 内容（加密存储）
	PrivateKeyPath string `json:"privateKeyPath"`
	Passphrase     string `json:"passphrase"` // 私钥口令（加密存储）
	Database       string `json:"database"`   // redis DB 索引 / mysql、tdengine 库名
	Remark         string `json:"remark"`
	HasSecret      bool   `json:"hasSecret"` // 是否已设置密码/私钥（脱敏后用于前端展示）
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// 请求/响应类型
type GetConnectionsRequest struct {
	Token string `json:"token"`
	Type  string `json:"type"` // 可空，筛选类型
}

type CreateConnectionRequest struct {
	Token          string `json:"token"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	AuthMethod     string `json:"authMethod"`
	Password       string `json:"password"`
	PrivateKey     string `json:"privateKey"`
	PrivateKeyPath string `json:"privateKeyPath"`
	Passphrase     string `json:"passphrase"`
	Database       string `json:"database"`
	Remark         string `json:"remark"`
}

type UpdateConnectionRequest struct {
	Token          string `json:"token"`
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	AuthMethod     string `json:"authMethod"`
	Password       string `json:"password"`
	PrivateKey     string `json:"privateKey"`
	PrivateKeyPath string `json:"privateKeyPath"`
	Passphrase     string `json:"passphrase"`
	Database       string `json:"database"`
	Remark         string `json:"remark"`
}

type DeleteConnectionRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type TestConnectionRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"` // >0 测试已保存连接
}

type TestConnectionConfigRequest struct {
	Token          string `json:"token"`
	Type           string `json:"type"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	AuthMethod     string `json:"authMethod"`
	Password       string `json:"password"`
	PrivateKey     string `json:"privateKey"`
	PrivateKeyPath string `json:"privateKeyPath"`
	Passphrase     string `json:"passphrase"`
	Database       string `json:"database"`
}

type ConnectionsResponse struct {
	Success     bool          `json:"success"`
	Connections []Connection  `json:"connections"`
	Message     string        `json:"message"`
}

type ConnectionResponse struct {
	Success    bool        `json:"success"`
	Connection *Connection `json:"connection"`
	Message    string      `json:"message"`
}

type DeleteConnectionResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Blocked  bool   `json:"blocked"`  // 被命令引用时阻止删除
	RefCount int    `json:"refCount"` // 引用该连接的命令数量
}

type TestConnectionResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	LatencyMs int64  `json:"latencyMs"`
}

// ConnService 连接管理服务
type ConnService struct{}

// 连接类型合法性 + 默认端口（v0.4.0：ssh / redis / mysql / tdengine）
var connectionTypePorts = map[string]int{
	"ssh":      22,
	"redis":    6379,
	"mysql":    3306,
	"tdengine": 6041, // taosAdapter REST 接口
}

// ==================== 工具函数 ====================

// validateConnectionType 校验连接字段，返回规范化后的类型与默认端口
func validateConnectionType(connType string) (string, error) {
	connType = strings.TrimSpace(connType)
	if connType == "" {
		return "", errors.New("连接类型不能为空")
	}
	if _, ok := connectionTypePorts[connType]; !ok {
		return "", errors.New("连接类型无效，仅支持 ssh、redis、mysql、tdengine")
	}
	return connType, nil
}

// defaultPort 返回类型默认端口，0 表示按类型取默认
func defaultPort(connType string, port int) int {
	if port > 0 {
		return port
	}
	if p, ok := connectionTypePorts[connType]; ok {
		return p
	}
	return 22
}

// sanitizeConnection 脱敏：清空敏感字段，仅标记是否已设置
func sanitizeConnection(c *Connection) {
	hasSecret := c.Password != "" || c.PrivateKey != "" || c.Passphrase != ""
	c.HasSecret = hasSecret
	c.Password = ""
	c.PrivateKey = ""
	c.Passphrase = ""
}

// scanConnection 从查询结果扫描一行连接记录
func scanConnection(row interface{ Scan(...interface{}) error }) (*Connection, error) {
	var c Connection
	var password, privateKey, passphrase sql.NullString
	err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Host, &c.Port, &c.Username,
		&c.AuthMethod, &password, &privateKey, &c.PrivateKeyPath, &passphrase,
		&c.Database, &c.Remark, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.Password = password.String
	c.PrivateKey = privateKey.String
	c.Passphrase = passphrase.String
	return &c, nil
}

const connectionCols = "id, name, type, host, port, username, auth_method, password, private_key, private_key_path, passphrase, database, remark, created_at, updated_at"

// loadConnectionByID 加载连接并解密敏感字段；id<=0 返回 nil（本地模式）
func loadConnectionByID(id int) (*Connection, error) {
	if id <= 0 {
		return nil, nil
	}
	row := db.QueryRow("SELECT "+connectionCols+" FROM connections WHERE id = ?", id)
	c, err := scanConnection(row)
	if err == sql.ErrNoRows {
		return nil, errors.New("连接不存在")
	}
	if err != nil {
		return nil, err
	}
	// 解密敏感字段（供执行器使用）
	if c.Password, err = secret.DecryptSecret(c.Password); err != nil {
		slog.Error("解密连接密码失败", "id", c.ID, "name", c.Name, "error", err)
	}
	if c.PrivateKey, err = secret.DecryptSecret(c.PrivateKey); err != nil {
		slog.Error("解密连接私钥失败", "id", c.ID, "name", c.Name, "error", err)
	}
	if c.Passphrase, err = secret.DecryptSecret(c.Passphrase); err != nil {
		slog.Error("解密连接私钥口令失败", "id", c.ID, "name", c.Name, "error", err)
	}
	return c, nil
}

// ==================== 连接管理 API ====================

// GetConnections 获取连接列表（可筛选类型，敏感字段脱敏）
func (s *ConnService) GetConnections(req GetConnectionsRequest) ConnectionsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ConnectionsResponse{Success: false, Message: "会话已过期"}
	}

	query := "SELECT " + connectionCols + " FROM connections"
	var args []interface{}
	if req.Type != "" {
		query += " WHERE type = ?"
		args = append(args, req.Type)
	}
	query += " ORDER BY type ASC, id ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("查询连接列表失败", "error", err)
		return ConnectionsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var conns []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			slog.Error("扫描连接记录失败", "error", err)
			continue
		}
		sanitizeConnection(c)
		conns = append(conns, *c)
	}
	slog.Debug("获取连接列表", "count", len(conns))
	return ConnectionsResponse{Success: true, Connections: conns}
}

// CreateConnection 创建连接
func (s *ConnService) CreateConnection(req CreateConnectionRequest) ConnectionResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ConnectionResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	connType, err := validateConnectionType(req.Type)
	if err != nil {
		return ConnectionResponse{Success: false, Message: err.Error()}
	}
	if name == "" {
		return ConnectionResponse{Success: false, Message: "连接名称不能为空"}
	}
	if strings.TrimSpace(req.Host) == "" {
		return ConnectionResponse{Success: false, Message: "主机地址不能为空"}
	}
	authMethod := strings.TrimSpace(req.AuthMethod)
	if connType == "ssh" && authMethod == "" {
		authMethod = "password"
	}

	password, err := secret.EncryptSecret(req.Password)
	if err != nil {
		return ConnectionResponse{Success: false, Message: "密码加密失败: " + err.Error()}
	}
	privateKey, err := secret.EncryptSecret(req.PrivateKey)
	if err != nil {
		return ConnectionResponse{Success: false, Message: "私钥加密失败: " + err.Error()}
	}
	passphrase, err := secret.EncryptSecret(req.Passphrase)
	if err != nil {
		return ConnectionResponse{Success: false, Message: "口令加密失败: " + err.Error()}
	}

	port := defaultPort(connType, req.Port)
	result, err := db.Exec(`INSERT INTO connections
		(name, type, host, port, username, auth_method, password, private_key, private_key_path, passphrase, database, remark)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, connType, strings.TrimSpace(req.Host), port, strings.TrimSpace(req.Username),
		authMethod, password, privateKey, strings.TrimSpace(req.PrivateKeyPath), passphrase,
		strings.TrimSpace(req.Database), strings.TrimSpace(req.Remark))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ConnectionResponse{Success: false, Message: "连接名称已存在"}
		}
		slog.Error("创建连接失败", "name", name, "error", err)
		return ConnectionResponse{Success: false, Message: "创建失败: " + err.Error()}
	}

	id, _ := result.LastInsertId()
	slog.Info("连接创建成功", "id", id, "name", name, "type", connType, "host", req.Host, "port", port)

	return ConnectionResponse{
		Success: true,
		Connection: &Connection{
			ID:         int(id),
			Name:       name,
			Type:       connType,
			Host:       strings.TrimSpace(req.Host),
			Port:       port,
			Username:   strings.TrimSpace(req.Username),
			AuthMethod: authMethod,
			Database:   strings.TrimSpace(req.Database),
			Remark:     strings.TrimSpace(req.Remark),
			HasSecret:  req.Password != "" || req.PrivateKey != "" || req.Passphrase != "",
		},
		Message: "连接创建成功",
	}
}

// UpdateConnection 修改连接（敏感字段留空 = 保持不变）
func (s *ConnService) UpdateConnection(req UpdateConnectionRequest) ConnectionResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ConnectionResponse{Success: false, Message: "会话已过期"}
	}

	var oldType string
	err := db.QueryRow("SELECT type FROM connections WHERE id = ?", req.ID).Scan(&oldType)
	if err == sql.ErrNoRows {
		return ConnectionResponse{Success: false, Message: "连接不存在"}
	}
	if err != nil {
		return ConnectionResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	name := strings.TrimSpace(req.Name)
	connType, err := validateConnectionType(req.Type)
	if err != nil {
		return ConnectionResponse{Success: false, Message: err.Error()}
	}
	if name == "" {
		return ConnectionResponse{Success: false, Message: "连接名称不能为空"}
	}
	if strings.TrimSpace(req.Host) == "" {
		return ConnectionResponse{Success: false, Message: "主机地址不能为空"}
	}
	authMethod := strings.TrimSpace(req.AuthMethod)
	if connType == "ssh" && authMethod == "" {
		authMethod = "password"
	}

	// 敏感字段：留空保持原值，非空加密更新
	password, privateKey, passphrase := "", "", ""
	hasSecretUpdate := false
	if req.Password != "" {
		if password, err = secret.EncryptSecret(req.Password); err != nil {
			return ConnectionResponse{Success: false, Message: "密码加密失败: " + err.Error()}
		}
		hasSecretUpdate = true
	}
	if req.PrivateKey != "" {
		if privateKey, err = secret.EncryptSecret(req.PrivateKey); err != nil {
			return ConnectionResponse{Success: false, Message: "私钥加密失败: " + err.Error()}
		}
		hasSecretUpdate = true
	}
	if req.Passphrase != "" {
		if passphrase, err = secret.EncryptSecret(req.Passphrase); err != nil {
			return ConnectionResponse{Success: false, Message: "口令加密失败: " + err.Error()}
		}
		hasSecretUpdate = true
	}

	port := defaultPort(connType, req.Port)

	// 动态构造 UPDATE：只更新需要改动的敏感字段
	query := `UPDATE connections SET
		name = ?, type = ?, host = ?, port = ?, username = ?, auth_method = ?,
		database = ?, remark = ?, updated_at = CURRENT_TIMESTAMP`
	args := []interface{}{name, connType, strings.TrimSpace(req.Host), port, strings.TrimSpace(req.Username),
		authMethod, strings.TrimSpace(req.Database), strings.TrimSpace(req.Remark)}
	if req.Password != "" {
		query += ", password = ?"
		args = append(args, password)
	}
	if req.PrivateKey != "" {
		query += ", private_key = ?"
		args = append(args, privateKey)
	}
	if req.Passphrase != "" {
		query += ", passphrase = ?"
		args = append(args, passphrase)
	}
	if req.PrivateKeyPath != "" {
		query += ", private_key_path = ?"
		args = append(args, strings.TrimSpace(req.PrivateKeyPath))
	}
	query += " WHERE id = ?"
	args = append(args, req.ID)

	_, err = db.Exec(query, args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ConnectionResponse{Success: false, Message: "连接名称已存在"}
		}
		slog.Error("修改连接失败", "id", req.ID, "name", name, "error", err)
		return ConnectionResponse{Success: false, Message: "修改失败: " + err.Error()}
	}

	slog.Info("连接修改成功", "id", req.ID, "name", name, "type", connType, "hasSecretUpdate", hasSecretUpdate)
	return ConnectionResponse{
		Success: true,
		Connection: &Connection{
			ID:         req.ID,
			Name:       name,
			Type:       connType,
			Host:       strings.TrimSpace(req.Host),
			Port:       port,
			Username:   strings.TrimSpace(req.Username),
			AuthMethod: authMethod,
			Database:   strings.TrimSpace(req.Database),
			Remark:     strings.TrimSpace(req.Remark),
		},
		Message: "连接修改成功",
	}
}

// DeleteConnection 删除连接（被命令引用时阻止）
func (s *ConnService) DeleteConnection(req DeleteConnectionRequest) DeleteConnectionResponse {
	if _, ok := validateSession(req.Token); !ok {
		return DeleteConnectionResponse{Success: false, Message: "会话已过期"}
	}

	var connName string
	err := db.QueryRow("SELECT name FROM connections WHERE id = ?", req.ID).Scan(&connName)
	if err == sql.ErrNoRows {
		return DeleteConnectionResponse{Success: false, Message: "连接不存在"}
	}
	if err != nil {
		return DeleteConnectionResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 检查隧道引用
	var tunnelRefCount int
	db.QueryRow("SELECT COUNT(*) FROM tunnels WHERE connection_id = ?", req.ID).Scan(&tunnelRefCount)
	if tunnelRefCount > 0 {
		slog.Warn("删除连接被阻止：存在引用隧道", "id", req.ID, "name", connName, "tunnelRefCount", tunnelRefCount)
		return DeleteConnectionResponse{
			Success:  false,
			Message:  "该连接被 " + strconv.Itoa(tunnelRefCount) + " 条隧道引用，请先在隧道管理中删除对应隧道",
			Blocked:  true,
			RefCount: tunnelRefCount,
		}
	}

	_, err = db.Exec("DELETE FROM connections WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("删除连接失败", "id", req.ID, "error", err)
		return DeleteConnectionResponse{Success: false, Message: "删除失败: " + err.Error()}
	}

	slog.Info("连接删除成功", "id", req.ID, "name", connName)
	return DeleteConnectionResponse{Success: true, Message: "连接「" + connName + "」已删除"}
}

// ==================== 连接测试 ====================

// TestConnection 测试已保存的连接
func (s *ConnService) TestConnection(req TestConnectionRequest) TestConnectionResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TestConnectionResponse{Success: false, Message: "会话已过期"}
	}
	c, err := loadConnectionByID(req.ID)
	if err != nil {
		return TestConnectionResponse{Success: false, Message: err.Error()}
	}
	if c == nil {
		return TestConnectionResponse{Success: false, Message: "连接不存在"}
	}

	return testConnection(c.Type, c.Host, c.Port, c.Username, c.AuthMethod,
		c.Password, c.PrivateKey, c.PrivateKeyPath, c.Passphrase, c.Database)
}

// TestConnectionConfig 测试未保存的连接配置（编辑弹窗内直接测试）
func (s *ConnService) TestConnectionConfig(req TestConnectionConfigRequest) TestConnectionResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TestConnectionResponse{Success: false, Message: "会话已过期"}
	}
	connType, err := validateConnectionType(req.Type)
	if err != nil {
		return TestConnectionResponse{Success: false, Message: err.Error()}
	}
	if strings.TrimSpace(req.Host) == "" {
		return TestConnectionResponse{Success: false, Message: "主机地址不能为空"}
	}
	return testConnection(connType, req.Host, defaultPort(connType, req.Port), req.Username,
		req.AuthMethod, req.Password, req.PrivateKey, req.PrivateKeyPath, req.Passphrase, req.Database)
}

// testConnection 按类型执行连接测试
func testConnection(connType, host string, port int, username, authMethod, password, privateKey, privateKeyPath, passphrase, database string) TestConnectionResponse {
	switch connType {
	case "ssh":
		latency, err := exec.TestSSH(host, port, username, authMethod, password, privateKey, privateKeyPath, passphrase)
		if err != nil {
			return TestConnectionResponse{Success: false, Message: "SSH 连接失败: " + err.Error()}
		}
		return TestConnectionResponse{Success: true, Message: "SSH 连接成功", LatencyMs: latency}
	case "redis":
		latency, err := exec.TestRedis(host, port, password, database)
		if err != nil {
			return TestConnectionResponse{Success: false, Message: "Redis 连接失败: " + err.Error()}
		}
		return TestConnectionResponse{Success: true, Message: "Redis 连接成功", LatencyMs: latency}
	case "mysql":
		latency, err := exec.TestMySQL(host, port, username, password, database)
		if err != nil {
			return TestConnectionResponse{Success: false, Message: "MySQL 连接失败: " + err.Error()}
		}
		return TestConnectionResponse{Success: true, Message: "MySQL 连接成功", LatencyMs: latency}
	case "tdengine":
		latency, err := exec.TestTDengine(host, port, username, password, database)
		if err != nil {
			return TestConnectionResponse{Success: false, Message: "TDengine 连接失败: " + err.Error()}
		}
		return TestConnectionResponse{Success: true, Message: "TDengine 连接成功", LatencyMs: latency}
	default:
		return TestConnectionResponse{Success: false, Message: "该连接类型暂不支持测试（将在后续版本支持）"}
	}
}
