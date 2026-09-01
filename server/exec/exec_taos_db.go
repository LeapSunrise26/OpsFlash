package exec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opsflash/server/daemon"
	"opsflash/server/pty"
)

// ==================== TDengine 执行器（REST 接口） ====================
// 通过 taosAdapter 的 REST 接口（默认 6041 端口）执行 SQL：
//   POST http://host:port/rest/sql[/<db>]   Authorization: Taosd <base64(user:pass)>
// 纯 Go net/http 实现，无需 TDengine 客户端库（无 CGO），适合桌面应用分发。
// 响应 JSON：{status, code, desc, column_meta, data, rows, affected_rows}

// TDengineConfig TDengine 连接配置
type TDengineConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	Database string
}

type tdengineExecutor struct {
	cfg TDengineConfig
}

// NewTDengineExecutor 创建 TDengine 执行器
func NewTDengineExecutor(cfg TDengineConfig) Executor {
	return &tdengineExecutor{cfg: cfg}
}

// tdRestResponse TDengine REST 响应结构
type tdRestResponse struct {
	Status       string          `json:"status"`
	Code         int             `json:"code"`
	Desc         string          `json:"desc"`
	ColumnMeta   [][]interface{} `json:"column_meta"`
	Data         [][]interface{} `json:"data"`
	Rows         int             `json:"rows"`
	AffectedRows int64           `json:"affected_rows"`
}

// exec 发起 REST 请求并解析响应
func (e *tdengineExecutor) exec(ctx context.Context, stmt string) (*tdRestResponse, error) {
	if e.cfg.Host == "" {
		return nil, fmt.Errorf("主机地址为空")
	}
	if e.cfg.Port <= 0 {
		e.cfg.Port = 6041
	}
	// 认证头：Taosd <base64(user:pass)>（部分新版本 taosAdapter 只认标准 Basic，
	// 401 时自动用 Basic 前缀重试一次）。用户名/密码不允许包含冒号（分割符），
	// 也不能为空（TDengine 用户必须有密码，默认 root/taosdata）。
	if strings.TrimSpace(e.cfg.Username) == "" || e.cfg.Password == "" {
		return nil, fmt.Errorf("用户名/密码不能为空（TDengine 默认账号 root，密码 taosdata）")
	}
	if strings.Contains(e.cfg.Username, ":") || strings.Contains(e.cfg.Password, ":") {
		return nil, fmt.Errorf("用户名/密码不能包含冒号（:）字符")
	}
	token := base64.StdEncoding.EncodeToString([]byte(e.cfg.Username + ":" + e.cfg.Password))

	client := &http.Client{Timeout: 10 * time.Second}
	statusCode, body, err := e.doAuthRetry(client, ctx, stmt, token)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		if statusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("认证失败（HTTP 401）：请检查用户名/密码是否正确（TDengine 默认 root/taosdata）")
		}
		return nil, fmt.Errorf("HTTP %d: %s", statusCode, strings.TrimSpace(string(body)))
	}

	var r tdRestResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	return &r, nil
}

// doAuthRetry 发起 POST 请求：首次使用 Taosd 前缀，401 时用 Basic 前缀重试一次。
// 返回最终状态码、响应体、错误（网络层）。
func (e *tdengineExecutor) doAuthRetry(client *http.Client, ctx context.Context, stmt, token string) (int, []byte, error) {
	url := fmt.Sprintf("http://%s:%d/rest/sql", e.cfg.Host, e.cfg.Port)
	if e.cfg.Database != "" {
		url += "/" + e.cfg.Database
	}

	send := func(scheme string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(stmt))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Authorization", scheme+" "+token)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("请求失败: %w", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 限制 2MB
		return resp.StatusCode, b, nil
	}

	status, body, err := send("Taosd")
	if err != nil {
		return 0, nil, err
	}
	if status == http.StatusUnauthorized {
		// 401：切换 Basic 前缀重试一次（部分新版本 taosAdapter 只认标准 Basic）
		status2, body2, err2 := send("Basic")
		if err2 != nil {
			return status, body, nil // 重试网络失败时保留首次结果
		}
		return status2, body2, nil
	}
	return status, body, nil
}

// Query 执行一条 SQL（查询类返回表格，其他返回影响行数）
func (e *tdengineExecutor) Query(ctx context.Context, stmt string) (*QueryResult, error) {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return nil, fmt.Errorf("SQL 不能为空")
	}

	start := time.Now()
	r, err := e.exec(ctx, stmt)
	if err != nil {
		return nil, err
	}
	if r.Status == "error" || r.Code != 0 {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("执行超时（60 秒）")
		}
		desc := r.Desc
		if desc == "" {
			desc = fmt.Sprintf("TDengine 错误码 %d", r.Code)
		}
		return nil, fmt.Errorf("%s", desc)
	}

	durationMs := time.Since(start).Milliseconds()
	if len(r.ColumnMeta) == 0 {
		slog.Info("TDengine 语句执行完成", "affected", r.AffectedRows, "durationMs", durationMs)
		return &QueryResult{Affected: r.AffectedRows, DurationMs: durationMs}, nil
	}

	// 查询类：列头 + 行
	cols := make([]string, 0, len(r.ColumnMeta))
	for _, meta := range r.ColumnMeta {
		if len(meta) > 0 {
			cols = append(cols, fmt.Sprint(meta[0]))
		}
	}
	rows := make([][]string, 0, len(r.Data))
	for _, row := range r.Data {
		vals := make([]string, len(row))
		for i, v := range row {
			vals[i] = tdValueString(v)
		}
		rows = append(rows, vals)
	}
	return &QueryResult{Columns: cols, Rows: rows, DurationMs: durationMs}, nil
}

// tdValueString 序列化 TDengine 返回值（JSON number 整数去尾缀）
func tdValueString(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return "NULL"
	case string:
		return val
	case float64:
		if val == math.Trunc(val) && math.Abs(val) < 1e15 {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		if val {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// Run 非交互执行：执行 SQL 并格式化为文本（批量执行复用）
func (e *tdengineExecutor) Run(ctx context.Context, stmt string) (string, error) {
	r, err := e.Query(ctx, stmt)
	if err != nil {
		return "", err
	}
	return queryResultText(r), nil
}

// StartInteractive / StartDaemon 数据库类不支持
func (e *tdengineExecutor) StartInteractive(cmdText string) (pty.Transport, error) {
	return nil, fmt.Errorf("数据库模式不支持交互式执行")
}

func (e *tdengineExecutor) StartDaemon(cmdText string) (daemon.Record, error) {
	return nil, ErrDaemonUnsupported
}

// TestTDengine 测试 TDengine 连接：SELECT SERVER_VERSION()，返回往返延迟（毫秒）
func TestTDengine(host string, port int, username, password, database string) (int64, error) {
	ex := &tdengineExecutor{cfg: TDengineConfig{Host: host, Port: port, Username: username, Password: password, Database: database}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	r, err := ex.exec(ctx, "SELECT SERVER_VERSION()")
	if err != nil {
		return 0, err
	}
	if r.Status == "error" || r.Code != 0 {
		desc := r.Desc
		if desc == "" {
			desc = "连接失败"
		}
		return 0, fmt.Errorf("%s", desc)
	}
	return time.Since(start).Milliseconds(), nil
}
