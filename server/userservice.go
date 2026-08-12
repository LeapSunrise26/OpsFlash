package server

import (
	"log/slog"

	"golang.org/x/crypto/bcrypt"
)

// User 用户信息
type User struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

// CreateUserRequest 创建用户请求
type CreateUserRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// UpdateUserRequest 更新用户请求
type UpdateUserRequest struct {
	Token    string `json:"token"`
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// DeleteUserRequest 删除用户请求
type DeleteUserRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

// UserListRequest 获取用户列表请求
type UserListRequest struct {
	Token string `json:"token"`
}

// UserListResponse 用户列表响应
type UserListResponse struct {
	Success bool   `json:"success"`
	Users   []User `json:"users"`
	Message string `json:"message"`
}

// UserResponse 通用用户操作响应
type UserResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// UserService 用户管理服务
type UserService struct{}

// GetUsers 获取所有用户列表
func (u *UserService) GetUsers(req UserListRequest) UserListResponse {
	if req.Token == "" {
		slog.Warn("GetUsers: 缺少 token")
		return UserListResponse{Success: false, Message: "未登录"}
	}
	username, ok := validateSession(req.Token)
	if !ok {
		slog.Warn("GetUsers: 会话验证失败")
		return UserListResponse{Success: false, Message: "会话已过期"}
	}

	slog.Info("获取用户列表", "operator", username)
	rows, err := db.Query("SELECT id, username, COALESCE(role, 'user') as role, created_at FROM users ORDER BY id")
	if err != nil {
		slog.Error("查询用户列表失败", "error", err)
		return UserListResponse{Success: false, Message: "查询用户列表失败"}
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		var createdAt string
		err := rows.Scan(&user.ID, &user.Username, &user.Role, &createdAt)
		if err != nil {
			slog.Warn("扫描用户行失败", "error", err)
			continue
		}
		// sqlite 返回的是 "2026-08-04 10:00:00" 格式，截取前16位
		if len(createdAt) >= 16 {
			user.CreatedAt = createdAt[:16]
		} else {
			user.CreatedAt = createdAt
		}
		users = append(users, user)
	}

	slog.Info("获取用户列表完成", "count", len(users))
	return UserListResponse{Success: true, Users: users}
}

// CreateUser 创建新用户
func (u *UserService) CreateUser(req CreateUserRequest) UserResponse {
	if req.Token == "" {
		slog.Warn("CreateUser: 缺少 token")
		return UserResponse{Success: false, Message: "未登录"}
	}
	operator, ok := validateSession(req.Token)
	if !ok {
		slog.Warn("CreateUser: 会话验证失败")
		return UserResponse{Success: false, Message: "会话已过期"}
	}
	if req.Username == "" || req.Password == "" {
		slog.Warn("CreateUser: 参数不完整", "targetUser", req.Username)
		return UserResponse{Success: false, Message: "用户名和密码不能为空"}
	}

	// 检查用户名是否已存在
	var count int
	db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", req.Username).Scan(&count)
	if count > 0 {
		slog.Warn("CreateUser: 用户名已存在", "targetUser", req.Username)
		return UserResponse{Success: false, Message: "用户名已存在"}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("CreateUser: 密码加密失败", "error", err)
		return UserResponse{Success: false, Message: "密码加密失败"}
	}

	role := req.Role
	if role == "" {
		role = "user"
	}

	_, err = db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)",
		req.Username, string(hash), role,
	)
	if err != nil {
		slog.Error("CreateUser: 插入数据库失败", "targetUser", req.Username, "error", err)
		return UserResponse{Success: false, Message: "创建用户失败: " + err.Error()}
	}

	slog.Info("用户创建成功", "operator", operator, "targetUser", req.Username, "role", role)
	return UserResponse{Success: true, Message: "用户创建成功"}
}

// UpdateUser 更新用户信息
func (u *UserService) UpdateUser(req UpdateUserRequest) UserResponse {
	if req.Token == "" {
		slog.Warn("UpdateUser: 缺少 token")
		return UserResponse{Success: false, Message: "未登录"}
	}
	operator, ok := validateSession(req.Token)
	if !ok {
		slog.Warn("UpdateUser: 会话验证失败")
		return UserResponse{Success: false, Message: "会话已过期"}
	}
	if req.ID == 0 {
		slog.Warn("UpdateUser: 用户ID无效")
		return UserResponse{Success: false, Message: "用户ID无效"}
	}

	if req.Username == "" {
		slog.Warn("UpdateUser: 用户名为空")
		return UserResponse{Success: false, Message: "用户名不能为空"}
	}

	// 检查用户名是否与其他用户冲突
	var count int
	db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ? AND id != ?", req.Username, req.ID).Scan(&count)
	if count > 0 {
		slog.Warn("UpdateUser: 用户名冲突", "targetUser", req.Username, "id", req.ID)
		return UserResponse{Success: false, Message: "用户名已被其他用户使用"}
	}

	role := req.Role
	if role == "" {
		role = "user"
	}

	updatingPassword := req.Password != ""
	if updatingPassword {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			slog.Error("UpdateUser: 密码加密失败", "error", err)
			return UserResponse{Success: false, Message: "密码加密失败"}
		}
		_, err = db.Exec(
			"UPDATE users SET username = ?, password_hash = ?, role = ? WHERE id = ?",
			req.Username, string(hash), role, req.ID,
		)
		if err != nil {
			slog.Error("UpdateUser: 更新失败（含密码）", "id", req.ID, "error", err)
			return UserResponse{Success: false, Message: "更新用户失败"}
		}
	} else {
		_, err := db.Exec(
			"UPDATE users SET username = ?, role = ? WHERE id = ?",
			req.Username, role, req.ID,
		)
		if err != nil {
			slog.Error("UpdateUser: 更新失败", "id", req.ID, "error", err)
			return UserResponse{Success: false, Message: "更新用户失败"}
		}
	}

	slog.Info("用户更新成功", "operator", operator, "id", req.ID, "targetUser", req.Username, "role", role, "passwordChanged", updatingPassword)
	return UserResponse{Success: true, Message: "用户信息更新成功"}
}

// DeleteUser 删除用户
func (u *UserService) DeleteUser(req DeleteUserRequest) UserResponse {
	if req.Token == "" {
		slog.Warn("DeleteUser: 缺少 token")
		return UserResponse{Success: false, Message: "未登录"}
	}
	operator, ok := validateSession(req.Token)
	if !ok {
		slog.Warn("DeleteUser: 会话验证失败")
		return UserResponse{Success: false, Message: "会话已过期"}
	}
	if req.ID == 0 {
		slog.Warn("DeleteUser: 用户ID无效")
		return UserResponse{Success: false, Message: "用户ID无效"}
	}

	// 不允许删除最后一个管理员
	if isOnlyAdmin(req.ID) {
		slog.Warn("DeleteUser: 拒绝删除唯一管理员", "id", req.ID)
		return UserResponse{Success: false, Message: "不能删除唯一的管理员账户"}
	}

	// 先获取被删除用户的用户名，用于日志
	var targetUser string
	db.QueryRow("SELECT username FROM users WHERE id = ?", req.ID).Scan(&targetUser)

	// 同时清除该用户的会话
	db.Exec("DELETE FROM sessions WHERE username = (SELECT username FROM users WHERE id = ?)", req.ID)

	result, err := db.Exec("DELETE FROM users WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("DeleteUser: 删除失败", "id", req.ID, "error", err)
		return UserResponse{Success: false, Message: "删除用户失败"}
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		slog.Warn("DeleteUser: 用户不存在", "id", req.ID)
		return UserResponse{Success: false, Message: "用户不存在"}
	}

	slog.Info("用户删除成功", "operator", operator, "targetUser", targetUser, "id", req.ID)
	return UserResponse{Success: true, Message: "用户已删除"}
}

// isOnlyAdmin 检查是否是最后一个管理员
func isOnlyAdmin(excludeID int) bool {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM users WHERE role = 'admin' AND id != ?",
		excludeID,
	).Scan(&count)
	if err != nil {
		return false
	}
	// 如果没有排除当前用户后的管理员，且当前用户确实是管理员
	var role string
	err = db.QueryRow("SELECT COALESCE(role, 'user') FROM users WHERE id = ?", excludeID).Scan(&role)
	if err != nil {
		return false
	}
	return role == "admin" && count == 0
}

// ensureRoleColumn 确保 users 表有 role 列（兼容旧数据库）


