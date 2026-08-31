package exec

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"opsflash/server/daemon"
	"opsflash/server/pty"
)

// ==================== 数据库类执行器（Redis / MySQL） ====================
// 数据库模式不执行 shell 命令，而是执行 Redis 指令 / SQL 语句。
// 通过 QueryExecutor 扩展接口返回结构化结果（Columns/Rows/Affected），
// 前端渲染为结果表格；Executor.Run 将其格式化为文本（批量执行复用）。
// TDengine 执行器见 exec_taos_db.go（REST 接口）。

// QueryResult 一条指令/SQL 的结构化结果
type QueryResult struct {
	Columns   []string   `json:"columns"`
	Rows      [][]string `json:"rows"`
	Affected  int64      `json:"affected"`  // 非查询类语句影响行数
	DurationMs int64     `json:"durationMs"`
}

// QueryExecutor 数据库类执行器的扩展接口
type QueryExecutor interface {
	Executor
	// Query 执行一条指令/SQL，返回行列结果
	Query(ctx context.Context, stmt string) (*QueryResult, error)
}

// ==================== Redis 执行器 ====================

// RedisConfig Redis 连接配置（凭据已解密）
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

type redisExecutor struct {
	cfg RedisConfig
}

// NewRedisExecutor 创建 Redis 执行器
func NewRedisExecutor(cfg RedisConfig) Executor {
	return &redisExecutor{cfg: cfg}
}

// dial 建立 Redis 连接
func (e *redisExecutor) dial() (*redis.Client, error) {
	if e.cfg.Host == "" {
		return nil, fmt.Errorf("主机地址为空")
	}
	if e.cfg.Port <= 0 {
		e.cfg.Port = 6379
	}
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", e.cfg.Host, e.cfg.Port),
		Password: e.cfg.Password,
		DB:       e.cfg.DB,
	}), nil
}

// Query 执行一条 Redis 指令（首词为命令名，其余为参数）
func (e *redisExecutor) Query(ctx context.Context, stmt string) (*QueryResult, error) {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return nil, fmt.Errorf("指令不能为空")
	}
	fields := strings.Fields(stmt)
	if len(fields) == 0 {
		return nil, fmt.Errorf("指令不能为空")
	}
	client, err := e.dial()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	start := time.Now()
	args := make([]interface{}, 0, len(fields))
	for _, f := range fields {
		args = append(args, f)
	}
	cmd := client.Do(ctx, args...)
	if err := cmd.Err(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("执行超时（60 秒）")
		}
		return nil, err
	}
	durationMs := time.Since(start).Milliseconds()

	// 序列化结果：单值单行单列；数组展开为多行
	val := cmd.Val()
	rows := make([][]string, 0, 4)
	switch v := val.(type) {
	case nil:
		rows = append(rows, []string{"(nil)"})
	case []interface{}:
		for _, item := range v {
			rows = append(rows, []string{redisValueString(item)})
		}
	default:
		rows = append(rows, []string{redisValueString(v)})
	}
	if len(rows) == 0 {
		rows = append(rows, []string{"(empty)"})
	}
	return &QueryResult{
		Columns:    []string{"result"},
		Rows:       rows,
		DurationMs: durationMs,
	}, nil
}

// redisValueString 序列化 Redis 返回值
func redisValueString(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return "(nil)"
	case string:
		return val
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case []byte:
		return string(val)
	case []interface{}:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			parts = append(parts, redisValueString(item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", val)
	}
}

// Run 非交互执行：执行指令并格式化为文本（批量执行复用）
func (e *redisExecutor) Run(ctx context.Context, stmt string) (string, error) {
	r, err := e.Query(ctx, stmt)
	if err != nil {
		return "", err
	}
	return queryResultText(r), nil
}

// StartInteractive / StartDaemon 数据库类不支持
func (e *redisExecutor) StartInteractive(cmdText string) (pty.Transport, error) {
	return nil, fmt.Errorf("数据库模式不支持交互式执行")
}

func (e *redisExecutor) StartDaemon(cmdText string) (*daemon.Record, error) {
	return nil, ErrDaemonUnsupported
}

// ==================== MySQL 执行器 ====================

// MySQLConfig MySQL 连接配置（凭据已解密）
type MySQLConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	Database string
}

type mysqlExecutor struct {
	cfg MySQLConfig
}

// NewMySQLExecutor 创建 MySQL 执行器
func NewMySQLExecutor(cfg MySQLConfig) Executor {
	return &mysqlExecutor{cfg: cfg}
}

// dsn 构建 MySQL 连接串
func (e *mysqlExecutor) dsn() string {
	if e.cfg.Port <= 0 {
		e.cfg.Port = 3306
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&timeout=5s&parseTime=true",
		e.cfg.Username, e.cfg.Password, e.cfg.Host, e.cfg.Port, e.cfg.Database)
}

// open 打开连接并 Ping
func (e *mysqlExecutor) open(ctx context.Context) (*sql.DB, error) {
	if e.cfg.Host == "" {
		return nil, fmt.Errorf("主机地址为空")
	}
	db, err := sql.Open("mysql", e.dsn())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接失败: %w", err)
	}
	return db, nil
}

// Query 执行一条 SQL：查询类返回表格，其他返回影响行数
func (e *mysqlExecutor) Query(ctx context.Context, stmt string) (*QueryResult, error) {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return nil, fmt.Errorf("SQL 不能为空")
	}

	db, err := e.open(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	start := time.Now()
	if isQueryStatement(stmt) {
		rows, err := db.QueryContext(ctx, stmt)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("执行超时（60 秒）")
			}
			return nil, err
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		var resultRows [][]string
		for rows.Next() {
			values := make([]driver.Value, len(cols))
			dest := make([]interface{}, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return nil, err
			}
			row := make([]string, len(cols))
			for i, v := range values {
				row[i] = fmtDBValue(v)
			}
			resultRows = append(resultRows, row)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return &QueryResult{
			Columns:    cols,
			Rows:       resultRows,
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	// 非查询类：执行并返回影响行数
	result, err := db.ExecContext(ctx, stmt)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("执行超时（60 秒）")
		}
		return nil, err
	}
	affected, _ := result.RowsAffected()
	slog.Info("MySQL 语句执行完成", "affected", affected, "durationMs", time.Since(start).Milliseconds())
	return &QueryResult{
		Affected:   affected,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// isQueryStatement 判断是否查询类语句（返回结果集）
func isQueryStatement(stmt string) bool {
	first := strings.ToUpper(strings.TrimSpace(strings.Fields(stmt)[0]))
	switch first {
	case "SELECT", "SHOW", "DESC", "DESCRIBE", "EXPLAIN", "WITH":
		return true
	}
	return false
}

// fmtDBValue 将数据库值格式化为字符串
func fmtDBValue(v driver.Value) string {
	switch val := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(val)
	case time.Time:
		return val.Format("2006-01-02 15:04:05")
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
func (e *mysqlExecutor) Run(ctx context.Context, stmt string) (string, error) {
	r, err := e.Query(ctx, stmt)
	if err != nil {
		return "", err
	}
	return queryResultText(r), nil
}

// StartInteractive / StartDaemon 数据库类不支持
func (e *mysqlExecutor) StartInteractive(cmdText string) (pty.Transport, error) {
	return nil, fmt.Errorf("数据库模式不支持交互式执行")
}

func (e *mysqlExecutor) StartDaemon(cmdText string) (*daemon.Record, error) {
	return nil, ErrDaemonUnsupported
}

// queryResultText 将结构化结果格式化为文本（批量执行 / 纯文本展示）
func queryResultText(r *QueryResult) string {
	if len(r.Columns) == 0 {
		return fmt.Sprintf("%d 行受影响", r.Affected)
	}
	// 列头
	var sb strings.Builder
	sb.WriteString(strings.Join(r.Columns, "\t"))
	for _, row := range r.Rows {
		sb.WriteString("\n")
		sb.WriteString(strings.Join(row, "\t"))
	}
	return sb.String()
}

// ==================== 连接测试 ====================

// TestRedis 测试 Redis 连接：Ping，返回往返延迟（毫秒）
func TestRedis(host string, port int, password, database string) (int64, error) {
	dbIdx := 0
	if database != "" {
		dbIdx, _ = strconv.Atoi(database)
	}
	ex := &redisExecutor{cfg: RedisConfig{Host: host, Port: port, Password: password, DB: dbIdx}}
	client, err := ex.dial()
	if err != nil {
		return 0, err
	}
	defer client.Close()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}

// TestMySQL 测试 MySQL 连接：Ping，返回往返延迟（毫秒）
func TestMySQL(host string, port int, username, password, database string) (int64, error) {
	ex := &mysqlExecutor{cfg: MySQLConfig{Host: host, Port: port, Username: username, Password: password, Database: database}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	db, err := ex.open(ctx)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return time.Since(start).Milliseconds(), nil
}
