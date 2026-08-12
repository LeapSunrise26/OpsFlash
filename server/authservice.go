package server

import "log/slog"

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

// LogoutRequest 登出请求
type LogoutRequest struct {
	Token string `json:"token"`
}

// LogoutResponse 登出响应
type LogoutResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// AuthService 认证服务
type AuthService struct{}

// Login 用户登录，验证账号密码并创建会话
func (a *AuthService) Login(req LoginRequest) LoginResponse {
	slog.Info("收到登录请求", "username", req.Username)

	if req.Username == "" || req.Password == "" {
		slog.Warn("登录请求参数不完整", "username", req.Username)
		return LoginResponse{
			Success: false,
			Message: "用户名和密码不能为空",
		}
	}

	ok, err := authenticateUser(req.Username, req.Password)
	if err != nil {
		slog.Error("登录认证查询失败", "username", req.Username, "error", err)
		return LoginResponse{
			Success: false,
			Message: "登录失败，请稍后重试",
		}
	}

	if !ok {
		return LoginResponse{
			Success: false,
			Message: "用户名或密码错误",
		}
	}

	token, err := createSession(req.Username)
	if err != nil {
		slog.Error("创建会话失败", "username", req.Username, "error", err)
		return LoginResponse{
			Success: false,
			Message: "创建会话失败",
		}
	}

	slog.Info("用户登录成功", "username", req.Username)
	return LoginResponse{
		Success:  true,
		Message:  "登录成功",
		Username: req.Username,
		Token:    token,
	}
}

// Logout 用户登出，使会话令牌失效
func (a *AuthService) Logout(req LogoutRequest) LogoutResponse {
	if req.Token == "" {
		slog.Warn("登出请求缺少 token")
		return LogoutResponse{
			Success: false,
			Message: "无效的会话",
		}
	}

	err := invalidateSession(req.Token)
	if err != nil {
		slog.Error("登出失败", "error", err)
		return LogoutResponse{
			Success: false,
			Message: "登出失败",
		}
	}

	// 同时清理该用户的过期会话
	cleanupExpiredSessions()

	slog.Info("用户登出成功")
	return LogoutResponse{
		Success: true,
		Message: "登出成功",
	}
}

// cleanupExpiredSessions 清理过期的会话记录
func cleanupExpiredSessions() {
	if db != nil {
		db.Exec("DELETE FROM sessions WHERE expires_at < CURRENT_TIMESTAMP")
	}
}