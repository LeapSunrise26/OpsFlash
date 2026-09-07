-- ============================================================================
-- OpsFlash v0.5.0 数据库结构文档 & 排查 SQL
-- 数据库：SQLite（modernc.org/sqlite 纯 Go 驱动）
-- 位置：data/opsflash.db（应用工作目录下）
-- 查看方式：sqlite3 data/opsflash.db  或任意 SQLite 客户端
-- 生成日期：2026-08-14（v0.5.0 增量更新：2026-09-07）
-- 范围：v0.2.0 基线（认证 / 用户 / 连接 / 隧道 / 脚本库，共 6 张表）
--       历史欠账：commands / environments.env_key 等 v0.3+ 结构未同步到本文档，
--                 以 server/database.go 实际建表为准（见文末 v0.5.0 增量追加）
-- ============================================================================


-- ============================================================================
-- 一、完整建表 DDL（v0.2.0 实际结构，新建库可直接执行）
-- ============================================================================

-- 1. users 用户账号（默认 admin / 123456，密码 bcrypt 哈希）
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 2. sessions 登录会话（token 24h 过期；同一用户仅保留一个有效 token）
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    token TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL
);

-- 3. connections 连接（SSH）
--    敏感字段（password/private_key/passphrase）经 secret 包加密存储
CREATE TABLE IF NOT EXISTS connections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL DEFAULT 'ssh',                 -- ssh（v0.2.0 仅 SSH）
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    username TEXT DEFAULT '',
    auth_method TEXT NOT NULL DEFAULT 'password',     -- ssh: password | key
    password TEXT DEFAULT '',                         -- 加密存储
    private_key TEXT DEFAULT '',                      -- 加密存储（PEM）
    private_key_path TEXT DEFAULT '',
    passphrase TEXT DEFAULT '',                       -- 加密存储
    remark TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 4. tunnels 隧道（SSH 端口转发）
CREATE TABLE IF NOT EXISTS tunnels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    connection_id INTEGER NOT NULL,                   -- FK connections（必须 SSH 类型）
    type TEXT NOT NULL DEFAULT 'local',               -- local | remote | dynamic
    local_host TEXT NOT NULL DEFAULT '127.0.0.1',     -- 本地监听/目标地址
    local_port INTEGER NOT NULL,
    remote_host TEXT NOT NULL DEFAULT '127.0.0.1',    -- 远程目标/监听地址（相对 SSH 服务器）
    remote_port INTEGER NOT NULL DEFAULT 0,
    auto_start INTEGER NOT NULL DEFAULT 0,            -- 应用启动自动启动
    auto_reconnect INTEGER NOT NULL DEFAULT 1,        -- 断线自动重连
    group_name TEXT NOT NULL DEFAULT '',              -- 分组标签（空=未分组）
    sort_order INTEGER NOT NULL DEFAULT 10,
    remark TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (connection_id) REFERENCES connections(id)
);
CREATE INDEX IF NOT EXISTS idx_tunnels_connection ON tunnels(connection_id);

-- 5. environments 环境（脚本库 / 后续运维命令共用）
CREATE TABLE IF NOT EXISTS environments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 6. scripts 脚本库
--    文件平铺于 data/scripts/（bat/ps1/sh），元数据存此表，内容存磁盘
CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,                               -- 脚本名（不含扩展名，同环境唯一）
    type TEXT NOT NULL,                               -- bat | ps1 | sh
    environment_id INTEGER NOT NULL DEFAULT 0,        -- 所属环境（0=通用）
    remark TEXT NOT NULL DEFAULT '',
    ts INTEGER NOT NULL                               -- 修改时间（unix 秒）
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_env_name ON scripts(environment_id, name);

-- ============================================================================
-- 二、v0.5.0 增量追加（指令编排）
-- 由 server/database.go InitDB 幂等创建（CREATE TABLE IF NOT EXISTS）
-- ============================================================================

-- 7. batch_tasks 批量任务（指令编排）
--    failure_policy：stop_on_error（遇错停止，后续 skipped）| continue_on_error（忽略错误继续）
CREATE TABLE IF NOT EXISTS batch_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    remark TEXT DEFAULT '',
    failure_policy TEXT NOT NULL DEFAULT 'stop_on_error',
    sort_order INTEGER NOT NULL DEFAULT 10,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 8. batch_task_items 批量任务步骤项（有序；命令步骤 command_id>0，脚本步骤 script_id>0 且 args 支持 {{var}} 注入）
CREATE TABLE IF NOT EXISTS batch_task_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL,
    command_id INTEGER NOT NULL DEFAULT 0,
    script_id INTEGER NOT NULL DEFAULT 0,
    args TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 10,
    FOREIGN KEY (task_id) REFERENCES batch_tasks(id)
);
CREATE INDEX IF NOT EXISTS idx_batch_items_task ON batch_task_items(task_id);
