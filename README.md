# OpsFlash

轻量级运维极速引擎 (The Flash Engine for Micro-Ops)

![logo](./docs/images/logo64.png)

OpsFlash 是一款基于 **Wails v3 + Go + Vue 3** 的跨平台桌面运维工具。单二进制分发，本地 SQLite 存储，敏感凭据加密保护，让日常服务器运维轻快如一瞬闪电。

> 当前版本：v0.1.0 · License：MIT

## 仓库地址

- GitHub：<https://github.com/LeapSunrise26/OpsFlash>
- Gitee：<https://gitee.com/LeapSunrise/OpsFlash>

## 特性

- **用户与认证**：登录 / 登出，Token 会话（24h 过期），密码 bcrypt 哈希，用户管理（创建/修改/删除/角色）
- **连接管理**：SSH 连接统一管理，私钥 / 口令凭据本地加密存储，连接测试
- **SSH 隧道**：local / remote / dynamic 三种端口转发，一键启停、断线自动重连、应用启动自启、分组与批量创建
- **桌面体验**：系统托盘（关闭最小化）、每日日志文件

## 技术栈

| 层 | 技术 |
|----|------|
| 桌面框架 | [Wails v3](https://v3.wails.io/)（v3.0.0-beta.4） |
| 后端语言 | Go 1.25 |
| 前端 | Vue 3 + TypeScript + Vite 8 |
| SSH | golang.org/x/crypto/ssh（纯 Go，无 CGO） |
| 存储 | SQLite（modernc.org/sqlite，纯 Go 驱动） |

## 快速开始

### 环境要求

- Go 1.25+
- Node.js 20+（前端构建）
- [Wails3 CLI](https://v3.wails.io/)

### 开发模式

```bash
wails3 dev
```

热重载前端与后端改动。

### 构建产物

```bash
wails3 build
```

生成生产可执行文件到 `bin/` 目录。构建后端为服务模式（无 GUI，纯 HTTP）：

```bash
task build:server   # 或 task run:server
```

### 默认账号

首次启动内置管理员账号：`admin` / `123456`（登录后请尽快修改）。

## 项目结构

```
├── main.go                 # 应用入口（窗口、托盘、菜单、事件）
├── server/                 # Go 后端
│   ├── authservice.go      # 登录/会话
│   ├── userservice.go      # 用户管理
│   ├── connections.go      # SSH 连接管理
│   ├── tunnelservice.go    # SSH 隧道 CRUD
│   ├── tunnelcontrol.go    # 隧道启停/分组控制
│   ├── tunnelruntime.go    # 隧道运行时管理
│   ├── exec/               # SSH 拨号与连接测试
│   ├── secret/             # 凭据加密（DPAPI / AES-256-GCM）
│   └── tunnel/             # 隧道实现（local/remote/dynamic）
├── frontend/               # Vue 3 前端
│   └── src/components/pages/  # 页面：登录/首页/连接/隧道/用户
├── docs/                   # 设计文档与数据库结构
└── build/                  # 平台打包配置
```

## 发布路线图

按阶段逐步开放能力（当前为第一阶段）：

| 阶段 | 版本 | 范围 |
|------|------|------|
| **第一阶段** | v0.1.0 | 用户管理、登录、首页、连接管理（SSH + 隧道管理） |
| **第二阶段** | v0.2.0 | 本地脚本：cmd、powershell、bash |
| **第三阶段** | v0.3.0 | 数据库执行：Redis、MySQL、TDengine |
| **第四阶段** | v0.4.0 | 批量执行 |

## 文档

- [连接设计](./docs/connections-design.md)
- [隧道设计](./docs/tunnel-design.md)
- [数据库结构](./docs/database-schema.sql)

## 软件截图

连接管理
![opsflash01](./docs/images/opsflash01.png)
隧道管理
![opsflash02](./docs/images/opsflash02.png)

## 开源协议

本项目基于 [MIT](./LICENSE) 协议开源。使用、修改、商用、分发均需保留版权声明。

## 反馈与贡献

- 欢迎提 Issue / PR
- 安全漏洞请私信或邮件至维护者，勿直接公开
