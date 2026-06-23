# gooophira-mp

Phira 多人游戏服务端的 **Go 实现**（自 TypeScript 版 `tphira-mp` 迁移而来）。

涵盖完整的服务端功能链路：二进制协议 / TCP 网络（含 HAProxy PROXY 协议、连接限速）/ 房间状态机 /
命令派发 / 配置（YAML + 环境变量，支持热重载）/ 管理接口（HTTP + 终端 CLI）/ 日志（按天轮转 + gzip 压缩 +
IP 黑名单）/ 回放（录制 + 下载 + 上传 + 自动上传）/ 观战聚合 / **GUI 网页控制台** / **缓存（本地内存 +
LRU + 落盘，可切 Redis 多实例共享）**。

> 纯 Go 实现，**无需 CGO**，单文件二进制，跨平台。

---

## 目录

- [功能特性](#功能特性)
- [环境要求](#环境要求)
- [快速开始](#快速开始)
- [构建](#构建)
  - [跨平台交叉编译](#跨平台交叉编译)
  - [注入版本号](#注入版本号)
- [配置](#配置)
- [运行](#运行)
- [GUI 网页控制台](#gui-网页控制台)
- [HTTP 接口](#http-接口)
- [终端 CLI 命令](#终端-cli-命令)
- [Redis 多实例缓存](#redis-多实例缓存)
- [项目结构](#项目结构)
- [测试](#测试)

---

## 功能特性

| 模块 | 说明 |
|------|------|
| **协议 / 网络** | 自定义二进制协议、TCP 帧解析、HAProxy PROXY v1/v2、单 IP 连接限速 + 全局连接上限 |
| **房间 / 对局** | 选谱 / 准备 / 游戏中状态机、循环模式、锁定、断线重连宽限、比赛模式 |
| **配置** | YAML 文件 + 环境变量覆盖；运行时热重载（改文件即生效，startup-only 项提示重启） |
| **管理（HTTP）** | `/admin/*`：房间/用户管理、封禁、广播、指标、运行时配置、OTP 提权、比赛、迁移、控制台 |
| **管理（CLI）** | 终端读 stdin 执行命令（list/users/ban/broadcast/maintenance/contest/…），输出分级配色 |
| **日志** | 按天轮转 + 旧日志 gzip 压缩 + 目录总量上限 + 连接日志 IP 黑名单（防洪水） |
| **回放** | `.phirarec` 录制、HTTP 下载、上传分享站、对局结束自动上传 |
| **GUI 控制台** | 内嵌单文件网页：性能曲线 + 房间/玩家列表 + WS 实时日志 + 命令行；可自动弹出浏览器窗口 |
| **缓存** | 谱面认证（token 30s）/ 成绩（1h）缓存，本地内存+LRU+落盘；启用 Redis 后多实例共享 |

## 环境要求

- **Go 1.26+**
- 无需 CGO（`CGO_ENABLED=0` 即可，默认纯 Go 构建）
- 可选：Redis（多实例部署时共享缓存）
- 运行时需能访问 Phira API（默认 `https://phira.5wyxi.com`）用于用户认证 / 谱面 / 成绩

## 快速开始

```bash
# 1. 准备配置（复制示例并按需修改）
cp server_config.example.yml server_config.yml

# 2. 运行（默认读取 ./server_config.yml）
go run ./cmd/server

# 或指定配置文件
go run ./cmd/server -config /path/to/server_config.yml
```

默认监听：TCP 游戏端口 `12346`，HTTP 服务端口 `12347`（需 `HTTP_SERVICE: true` 开启）。

## 构建

```bash
# 构建当前平台二进制（版本号取自内嵌的 VERSION 文件）
go build -o phira-mp-server ./cmd/server

# 运行
./phira-mp-server -config server_config.yml
```

运行时会在工作目录生成 `logs/`、`record/`（回放）、`cache/`（落盘缓存）、`admin_data.json`（封禁数据）等目录/文件。

### 跨平台交叉编译

纯 Go、无 CGO，可直接交叉编译到任意目标平台（在任一开发机上）：

```bash
# Linux x86-64
GOOS=linux  GOARCH=amd64 go build -o phira-mp-server-linux-amd64 ./cmd/server

# Linux ARM64（树莓派 / 云 ARM 实例）
GOOS=linux  GOARCH=arm64 go build -o phira-mp-server-linux-arm64 ./cmd/server

# Windows x86-64
GOOS=windows GOARCH=amd64 go build -o phira-mp-server.exe ./cmd/server

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o phira-mp-server-darwin-arm64 ./cmd/server

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -o phira-mp-server-darwin-amd64 ./cmd/server
```

> 进程性能采样（`internal/procstats`）按平台分别用 Windows 系统调用 / Linux `/proc` / 其它平台
> `getrusage` 实现；GUI 窗口启动器按平台探测 Edge/Chrome。均在 Windows、Linux、macOS、FreeBSD 上
> 验证可编译，无需任何平台专属工具链。

PowerShell（Windows）下设置交叉编译目标：

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o phira-mp-server ./cmd/server
```

### 注入版本号

版本号优先级：`PHIRA_MP_VERSION` 环境变量 > `ldflags` 注入 > 内嵌 `VERSION` 文件 > 构建信息 > `dev`。
release 构建可用 git 描述覆盖：

```bash
go build -ldflags "-X github.com/Pimeng/gooophira-mp/internal/version.injected=$(git describe --tags --always)" -o phira-mp-server ./cmd/server
```

## 配置

配置来源优先级：**环境变量 > 配置文件 > 内置默认值**。完整带注释的示例见
[`server_config.example.yml`](server_config.example.yml)。常用项：

| 配置项 | 环境变量 | 默认 | 说明 |
|--------|----------|------|------|
| `HOST` / `PORT` | `HOST` / `PORT` | `::` / `12346` | TCP 游戏服务监听地址/端口 |
| `HTTP_SERVICE` / `HTTP_PORT` | 同名 | `false` / `12347` | HTTP 查询/管理服务 |
| `GUI` | `GUI` | `false` | 启动时弹出 GUI 控制台窗口（隐含开启 HTTP 服务） |
| `LOG_LEVEL` | `LOG_LEVEL` | `INFO` | `DEBUG`/`INFO`/`MARK`/`WARN`/`ERROR` |
| `LANG` | `PHIRA_MP_LANG` > `LANG` > 此项 | `zh-CN` | 日志/CLI/HTTP 输出语言（`zh-CN`/`en-US`） |
| `ADMIN_TOKEN` | `ADMIN_TOKEN` | 空 | HTTP `/admin/*` 鉴权 token（不配则管理接口禁用） |
| `ROOM_MAX_USERS` | `ROOM_MAX_USERS` | `12` | 单房间最大人数 |
| `REPLAY_ENABLED` | `REPLAY_ENABLED` | `false` | 回放录制开关（可运行时切换） |
| `REDIS` | `REDIS_ENABLED` / `REDIS_HOST` / … | 关闭 | Redis 多实例共享缓存（见下） |

环境变量覆盖嵌套块时使用前缀展开，例如 `REDIS_ENABLED`、`REDIS_HOST`、`REDIS_PORT`、`SHARE_STATION_URL` 等。

**热重载**：服务运行时修改配置文件即自动重新加载并生效（连接限速、HTTP 限速、日志级别、回放/建房开关等）；
仅启动时生效的项（端口、GUI、Redis 等）会提示需重启。运行时切换（如 CLI `replay on`）会写回配置文件并保留注释。

## 运行

```
phira-mp-server [-config <path>] [-gui]
```

| 参数 | 说明 |
|------|------|
| `-config <path>` | 配置文件路径，默认 `server_config.yml` |
| `-gui` | 启动时打开 GUI 控制台窗口（等价于配置 `GUI: true`，覆盖配置） |

服务通过 `Ctrl+C`（SIGINT/SIGTERM）或 CLI `stop` / `shutdown` 命令优雅关闭。

## GUI 网页控制台

类似 Minecraft 服务端 GUI 的管理面板：CPU/内存实时曲线、业务计数器、房间/玩家列表、
WebSocket 实时日志流 + 命令行（与终端 CLI 同一套命令）。零外部资源依赖，离线可用。

- **访问**：开启 HTTP 服务后浏览器打开 `http://<host>:<http_port>/gui`，输入 `ADMIN_TOKEN` 登录。
- **窗口模式**：`-gui` 启动参数（或 `GUI: true`）会自动开启 HTTP 服务、生成本机回环专用 token 并弹出
  一个浏览器「应用模式」窗口（Edge/Chrome），免手动输入 token。
- 页面本身公开（不含敏感数据），其数据接口（`/admin/*`、`/ws`）均需管理员 token。

## HTTP 接口

需 `HTTP_SERVICE: true`。管理接口通过 `X-Admin-Token`（推荐）或 `Authorization: Bearer <token>` 头鉴权。

**公开接口**

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/room` | 公开房间列表 |
| GET | `/gui` | GUI 控制台页面 |
| GET | `/room-creation/config`、`/replay/config` | 开关状态 |
| POST | `/replay/auth`、`/replay/download`、`/replay/delete` | 回放认证/下载/删除 |
| WS | `/ws` | 实时订阅（房间更新、管理面板、控制台日志） |

**管理接口（`/admin/*`，需 token）**

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/rooms`、`/admin/users`、`/admin/metrics` | 房间/用户/性能与业务指标 |
| POST | `/admin/ban/user`、`/admin/ban/room`、`/admin/disband`、`/admin/broadcast` | 封禁/解散/广播 |
| POST | `/admin/users/:id/move` | 离线用户在房间间迁移 |
| GET/POST | `/admin/runtime-config`、`/admin/runtime-config/rollback` | 运行时配置读写/回滚 |
| GET/POST | `/admin/console/logs`、`/admin/console/command` | GUI 控制台日志回填/执行命令 |
| POST | `/admin/contest/rooms/:id/...` | 比赛模式管理 |
| POST | `/admin/otp/request`、`/admin/otp/verify` | OTP 提权（无 token 时经终端审批获取临时 token） |

**OTP 提权**：未配置/无法直接拿到 `ADMIN_TOKEN` 时，可 `POST /admin/otp/request` 发起申请，终端会打印验证码；
管理员在终端 `approve <ssid>` 后，调用方 `POST /admin/otp/verify` 即可换取 IP 绑定的临时 token。

## 终端 CLI 命令

服务端在前台运行时，可直接在终端输入命令（输出按级配色：错误红 / 成功绿 / 提示青）：

```
help                          显示帮助
list, rooms                   列出所有房间
users                         列出在线用户
user <id>                     查看用户信息
kick <userId> [preserve]      踢出用户（preserve|true=断开但保留房间槽位、可重连）
ban / unban <userId>          服务器封禁 / 解封
banlist                       查看封禁列表
banroom / unbanroom <uid> <rid>   房间禁入 / 解除
broadcast | say <message>     全服广播
roomsay <roomId> <message>    向指定房间发消息
maxusers <roomId> <count>     设置房间最大人数
disband <roomId>              解散房间
replay <on|off|status>        回放录制开关
roomcreation <on|off|status>  建房开关
maintenance <on|off|status> [消息]   维护模式
contest <roomId> <enable|disable|whitelist|start> [...]   比赛模式
ipblacklist <list|remove <ip>|clear>   连接日志 IP 黑名单
pending / approve <ssid> / deny <ssid>   CLI 提权审批
stop, shutdown                优雅关闭
```

同一套命令也可经 GUI 控制台或 `POST /admin/console/command` 执行。

## Redis 多实例缓存

默认缓存（谱面认证 token 30s、成绩记录 1h）走本地内存 + LRU + 落盘。多实例部署时启用 Redis 即可让各实例
共享缓存：

```yaml
REDIS:
  ENABLED: true
  HOST: "127.0.0.1"
  PORT: 6379
  # PASSWORD: "your_password"
  DB: 0
```

或用环境变量 `REDIS_ENABLED=true REDIS_HOST=... REDIS_PORT=...`。启用后所有缓存读写走共享 Redis
（键前缀 `cache:<name>:<key>`，按 TTL 过期），启动时把本地内存数据迁移过去。**Redis 不可达时自动降级为
本地缓存**，不影响服务启动。

## 项目结构

```
cmd/server/         入口（装配、信号关闭、GUI 窗口启动）
internal/
  protocol/         二进制协议、帧、命令、房间 ID、UUID
  config/           配置加载（YAML+env）、热重载、运行时补丁、持久化
  server/           全局状态、Hub、房间、用户、命令派发、比赛、迁移、控制台缓冲
  network/          TCP 监听、会话、PROXY 协议、连接限速
  httpapi/          HTTP 服务、管理路由、WebSocket、OTP、回放路由、GUI 页面
  cli/              终端控制台命令分发
  logging/          分级日志、按天轮转、压缩维护、连接 IP 黑名单
  replay/           回放录制、存储、读取、清理
  sharestation/     回放分享站客户端
  autoupload/       对局结束自动上传
  cache/            通用缓存（内存+LRU+落盘）+ Redis 后端
  phira/            Phira 上游 API 客户端（认证/谱面/成绩，带缓存）
  procstats/        进程 CPU/内存采样（GUI 监控，分平台、无 CGO）
  guiwindow/        浏览器「应用模式」窗口启动器
  l10n/             Fluent/FTL 本地化（内嵌 + 运行时覆盖）
```

## 测试

```bash
go build ./...
go vet ./...
go test ./...            # 全部单元测试
go test ./... -count=2   # 重复跑，验证并发稳定性（项目无 CGO，不用竞态检测器）
```

Redis 后端测试默认用进程内的 [miniredis](https://github.com/alicebob/miniredis)（纯 Go，自动运行）；
如需对真实 Redis 验证，设置 `REDIS_TEST_ADDR=127.0.0.1:6379` 后运行 `go test ./internal/cache/`。

涉及 Phira 凭证的集成测试经环境变量门控（如 `PHIRA_TEST_TOKEN`），默认跳过，凭证不入库。
