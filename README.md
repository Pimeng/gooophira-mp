# gooophira-mp

Phira 多人游戏服务端的 Go 实现（自 TypeScript 版 `tphira-mp` 迁移而来）。

涵盖完整的服务端功能链路：二进制协议 / TCP 网络（含 HAProxy PROXY 协议、连接限速）/ 房间状态机 /
命令派发 / 配置（YAML + 环境变量，支持热重载）/ 管理接口（HTTP + 终端 CLI）/ 日志（按天轮转 + gzip 压缩 +
IP 黑名单）/ 回放（录制 + 下载 + 上传 + 自动上传）/ 观战聚合 / GUI 网页控制台 / 缓存（本地内存 +
LRU + 落盘，可切 Redis 多实例共享）。

> 纯 Go 实现，无需 CGO，单文件二进制，跨平台。

---

## 目录

- [特性](#特性)
- [快速开始](#快速开始)
- [配置](#配置)
- [运行与管理](#运行与管理)
  - [终端 CLI 命令](#终端-cli-命令)
  - [GUI 网页控制台](#gui-网页控制台)
  - [HTTP 管理接口](#http-管理接口)
- [高级功能](#高级功能)
  - [回放录制与上传](#回放录制与上传)
  - [Redis 共享缓存](#redis-共享缓存)
  - [比赛模式](#比赛模式)
  - [Webhook 通知](#webhook-通知)
- [构建与开发](#构建与开发)
  - [Docker 部署](#docker-部署)
- [项目结构](#项目结构)
- [测试](#测试)

---

## 特性

- 协议与网络：自定义二进制协议、TCP 帧解析、HAProxy PROXY v1/v2、单 IP 连接限速、全局连接上限
- 房间与对局：选谱/准备/游戏中状态机、循环模式、锁定、断线重连宽限、比赛模式
- 管理方式：HTTP API、终端 CLI、GUI 网页控制台（实时监控、命令执行、日志流）
- 配置灵活：YAML + 环境变量覆盖，运行时热重载（大部分配置即时生效）
- 日志系统：按天轮转、gzip 压缩、目录总量上限、IP 黑名单防洪水
- 回放系统：录制 `.phirarec`、HTTP 下载/删除、自动上传分享站
- 缓存方案：本地内存 + LRU + 落盘；支持 Redis 多实例共享
- 跨平台：Windows / Linux / macOS / FreeBSD，纯 Go 无 CGO

---

## 快速开始

1. 准备配置文件（复制示例并按需修改）：

   ```bash
   cp server_config.example.yml server_config.yml
   ```

2. 启动服务：

   ```bash
   ./phira-mp-server -config server_config.yml
   ```

   默认监听：
   - TCP 游戏端口：`12346`
   - HTTP 服务端口：`12347`（需在配置中开启 `HTTP_SERVICE: true`）

3. 管理服务：
   - 终端中直接输入 `help` 查看 CLI 命令
   - 浏览器打开 `http://<服务器IP>:12347/gui`，输入 `ADMIN_TOKEN`（配置中设置）进入网页控制台

---

## 配置

配置优先级：**环境变量 > 配置文件 > 内置默认值**。  
完整带注释的示例见 [`server_config.example.yml`](server_config.example.yml)。

### 关键配置项

完整带注释的示例见 [`server_config.example.yml`](server_config.example.yml)。

| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `HOST` / `PORT` | `HOST` / `PORT` | `::` / `12346` | TCP 游戏服务监听地址/端口 |
| `HTTP_SERVICE` / `HTTP_PORT` | 同名 | `false` / `12347` | 是否开启 HTTP 管理服务 |
| `GUI` | `GUI` | `false` | 启动时自动弹出 GUI 窗口（需 HTTP 服务） |
| `ADMIN_TOKEN` | `ADMIN_TOKEN` | 空 | HTTP 管理接口鉴权令牌（不配则禁用管理接口） |
| `LOG_LEVEL` | `LOG_LEVEL` | `INFO` | 日志级别：`DEBUG`/`INFO`/`MARK`/`WARN`/`ERROR` |
| `LANG` | `PHIRA_MP_LANG` > `LANG` | `zh-CN` | 界面语言：`zh-CN`/`en-US` |
| `ROOM_MAX_USERS` | `ROOM_MAX_USERS` | `8` | 单个房间最大人数 |
| `REPLAY_ENABLED` | `REPLAY_ENABLED` | `false` | 是否录制回放（可运行时切换） |
| `REDIS` | `REDIS_ENABLED` / `REDIS_HOST` ... | 关闭 | Redis 多实例共享缓存（见高级功能） |

**网络与安全：**
| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `HAPROXY_PROTOCOL` | 同名 | `false` | 是否启用 HAProxy PROXY v1/v2 协议 |
| `MAX_CONNECTIONS` | 同名 | `0`（不限） | 全服 TCP 连接数上限 |
| `CONNECTION_RATE_LIMIT` | 同名 | `30` | 单 IP 每 10s 窗口允许新建连接数 |
| `COMMAND_RATE_LIMIT` | 同名 | `true` | 会话级命令令牌桶限流 |
| `HTTP_RATE_LIMIT_MAX_REQUESTS` | 同名 | `100` | HTTP API 单 IP 限流窗口内最大请求数 |
| `HTTP_RATE_LIMIT_WINDOW_MS` | 同名 | `60000` | HTTP API 限流窗口（毫秒） |
| `CORS_ORIGINS` | 同名 | `[]`（不返回 CORS 头） | HTTP CORS 允许来源列表；`["*"]` 显式允许所有 |
| `REAL_IP_HEADER` | 同名 | `""`（关闭） | HTTP 真实 IP 头名称（仅可信反代场景启用） |
| `ALLOW_TOKEN_IN_QUERY` | 同名 | `false` | 是否允许 URL 查询参数传 token |

**房间与对局：**
| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `ROOM_CREATION_ENABLED` | 同名 | `true` | 是否允许玩家创建房间 |
| `MAX_ROOMS` | 同名 | `0`（不限） | 全服同时存在的房间数上限 |
| `PLAYING_RECONNECT_GRACE` | 同名 | `5` | 对局断线重连宽限时长（秒） |
| `CHAT_ENABLED` | 同名 | `true` | 是否启用聊天 |
| `SERVER_NAME` | 同名 | `"Phira MP"` | 服务器名称（显示在欢迎信息中） |
| `MONITORS` | 同名 | `[2]` | 观战用户 ID 列表 |
| `TEST_ACCOUNT_IDS` | 同名 | `[1739989]` | 测试账号 ID（日志不写入文件） |
| `ROOM_LIST_TIP` | 同名 | 空 | 房间列表后追加显示的提示文案 |

**回放：**
| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `REPLAY_BASE_DIR` | 同名 | `./record` | 回放录制目录 |
| `REPLAY_TTL_DAYS` | 同名 | `4` | 回放文件保留天数（每日凌晨清理） |
| `REPLAY_AUTO_UPLOAD` | 同名 | `false` | 是否自动上传回放到分享站 |
| `SHARE_STATION` | `SHARE_STATION_URL` / `TOKEN` | 未设置 | 分享站地址与 token |

**日志：**
| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `LOG_COMPRESS_AFTER_DAYS` | 同名 | `14` | 历史日志超天数后 gzip 压缩；0 关闭 |
| `LOG_MAX_TOTAL_MB` | 同名 | `500` | 日志目录总占用上限（MB）；0 不限制 |

**外部服务：**
| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `PHIRA_API_ENDPOINT` | 同名 | `https://phira.5wyxi.com` | Phira API 端点地址 |
| `HITOKOTO_API_URL` | 同名 | `https://v1.hitokoto.cn/` | 一言 API 地址 |
| `OUTBOUND_PROXY` | `OUTBOUND_PROXY` | 未设置 | 出站代理（false=直连 / URL=指定代理） |
| `WEBHOOK` | 仅 YAML | 未设置 | Webhook 事件通知（见高级功能） |

**管理数据与统计：**
| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `ADMIN_DATA_PATH` | 同名 | `./admin_data.json` | 管理数据持久化路径 |
| `STATS_DB_PATH` | 同名 | `stats.db` | 统计数据库路径（SQLite） |
| `STATS_DETAIL_RETENTION_DAYS` | 同名 | `90` | 统计数据明细保留天数 |
| `STATS_DB_MAX_MB` | 同名 | `500` | 统计数据库文件大小上限（MB） |

> **热重载**：修改配置文件后服务自动加载新配置（连接限速、日志级别、回放开关等即时生效）；仅端口、GUI、Redis 等启动项需重启。

---

## 运行与管理

### 终端 CLI 命令

服务在前台运行时，直接在终端输入命令（支持彩色输出）：

```
help                          显示帮助
list, rooms                   列出所有房间
users                         列出在线用户
user <id>                     查看用户信息
kick <userId> [preserve]      踢出用户（preserve=true 保留槽位允许重连）
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
pending / approve <ssid> / deny <ssid> / reject <ssid>   CLI 提权审批
stop, shutdown                优雅关闭
```

### GUI 网页控制台

类似 Minecraft 服务端面板，零外部依赖，离线可用：

- 访问地址：`http://<服务器IP>:<HTTP_PORT>/gui`
- 功能：CPU/内存实时曲线、业务计数器、房间/玩家列表、WebSocket 实时日志、在线命令行
- 自动弹出：启动时加 `-gui` 参数或配置 `GUI: true`，会自动打开浏览器窗口并生成临时 token

### HTTP 管理接口

所有 `/admin/*` 接口需 `X-Admin-Token` 或 `Authorization: Bearer <token>` 头鉴权。

**公开接口**（无需鉴权）：
- `GET /room` – 房间列表
- `GET /gui` – GUI 页面
- `GET /room-creation/config`、`/replay/config` – 开关状态
- `GET /replay/download`、`POST /replay/auth`、`POST /replay/delete`、`POST /replay/upload` – 回放文件操作
- `GET/POST /replay/auto-upload/config` – 自动上传配置读写
- `GET /charts/hot` – 热门谱面排行
- `GET /chart/:id` – 谱面信息
- `GET /player/:id` – 玩家信息
- `GET /player/:id/recent` – 玩家近期成绩
- `GET /leaderboard` – 综合排行榜
- `WS /ws` – WebSocket 实时订阅

**管理接口**（需 token）：
- `GET /admin/rooms`、`/admin/users`、`/admin/metrics` – 状态查询
- `GET /admin/users/:id` – 查看用户详情
- `POST /admin/users/:id/move` – 迁移离线用户
- `POST /admin/users/:id/disconnect` – 断开用户连接
- `POST /admin/ban/user`、`/admin/ban/room`、`/admin/disband`、`/admin/broadcast` – 封禁/解散/广播
- `GET/POST /admin/runtime-config`、`POST /admin/runtime-config/rollback` – 运行时配置读写/回滚
- `GET/POST /admin/replay/config` – 回放配置读写
- `GET/POST /admin/room-creation/config` – 建房配置读写
- `GET/POST /admin/console/logs`、`/admin/console/command` – 控制台日志与命令
- `POST /admin/contest/rooms/:id/config`、`.../whitelist`、`.../start` – 比赛模式管理
- `POST /admin/otp/request`、`/admin/otp/verify` – OTP 提权（无 token 时经终端审批获取临时 token）

---

## 高级功能

### 回放录制与上传

- 开启 `REPLAY_ENABLED` 后，对局结束自动生成 `.phirarec` 文件
- 可通过 HTTP 接口下载、删除
- 配置 `SHARE_STATION_URL` 后支持对局结束自动上传至分享站

### Redis 共享缓存

多实例部署时启用 Redis，使玩家鉴权 token（6h）和成绩记录（1h）在实例间共享。

配置示例：
```yaml
REDIS:
  ENABLED: true
  HOST: "127.0.0.1"
  PORT: 6379
  # PASSWORD: "your_password"
  DB: 0
```

或用环境变量：`REDIS_ENABLED=true REDIS_HOST=... REDIS_PORT=...`。  
启用后所有缓存读写走共享 Redis（键前缀 `cache:<name>:<key>`，按 TTL 过期），启动时把本地内存数据迁移过去。Redis 不可达时自动降级为本地缓存。

### 比赛模式

通过 CLI 或 HTTP API 管理房间的比赛模式（开启/关闭/白名单/开始），适合组织竞技赛事。

### Webhook 通知

把服务器事件异步推送到群机器人或自建服务，投递带缓冲队列、超时与失败重试，目标不可达也不阻塞对局逻辑；配置热重载（改 `WEBHOOK` 块即时生效）。

- 事件类型：`room_create`、`room_disband`、`user_join`、`maintenance`
- 载荷格式（`TYPE`）：`generic`（结构化 JSON，自定义机器人自行渲染）、`discord`、`feishu`
- 可选 `SECRET`：请求带 `X-Phira-Signature: sha256=<HMAC(body)>` 头供接收端验签

```yaml
WEBHOOK:
  ENABLED: true
  TIMEOUT_MS: 5000   # 单次请求超时(ms)，默认 5000
  RETRIES: 2         # 仅对 5xx/429/网络错误重试，默认 2
  TARGETS:
    - URL: "https://discord.com/api/webhooks/xxx/yyy"
      TYPE: discord
      EVENTS: [room_create, room_disband, maintenance]   # 省略 = 订阅全部
    - URL: "https://example.com/hook"
      TYPE: generic
      SECRET: "shared_secret"
```

---

## 构建与开发

### 环境要求

- Go 1.26+
- 无需 CGO（`CGO_ENABLED=0` 即可）
- 可选：Redis（用于共享缓存）

### 编译当前平台

```bash
go build -o phira-mp-server ./cmd/server
```

版本号可注入（优先级：环境变量 `PHIRA_MP_VERSION` > ldflags > 内嵌 VERSION 文件 > `dev`）：

```bash
go build -ldflags "-X github.com/Pimeng/gooophira-mp/internal/version.injected=$(git describe --tags --always)" -o phira-mp-server ./cmd/server
```

### 交叉编译（纯 Go，无 CGO）

```bash
# Linux x86-64
GOOS=linux  GOARCH=amd64 go build -o phira-mp-server-linux-amd64 ./cmd/server

# Linux ARM64
GOOS=linux  GOARCH=arm64 go build -o phira-mp-server-linux-arm64 ./cmd/server

# Windows
GOOS=windows GOARCH=amd64 go build -o phira-mp-server.exe ./cmd/server

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o phira-mp-server-darwin-arm64 ./cmd/server

# macOS (Intel)
GOOS=darwin GOARCH=amd64 go build -o phira-mp-server-darwin-amd64 ./cmd/server
```

### Docker 部署

镜像多阶段构建（`CGO_ENABLED=0` 静态编译，运行层为带 CA 证书的最小 Alpine），非 root 运行，工作目录 `/data` 即数据目录（配置/日志/回放/缓存均持久化于此）。

```bash
# 单容器
docker build -t phira-mp:local .
docker run -d --name phira-mp -p 12346:12346 -p 12347:12347 \
  -e ADMIN_TOKEN=changeme -v phira-data:/data phira-mp:local

# 一键栈（server + Redis）
cp .env.example .env      # 按需填 ADMIN_TOKEN 等
docker compose up -d --build
```

容器内默认 `HTTP_SERVICE=true` 并内置 `HEALTHCHECK`；首次运行自动生成 `/data/server_config.yml`。`internal/version/VERSION` 升高或手动触发 [`docker-image.yml`](.github/workflows/docker-image.yml) 会构建 `amd64`+`arm64` 多架构镜像推送 GHCR。

### 测试

```bash
go build ./...
go vet ./...
go test ./...            # 全部单元测试
go test ./... -count=2   # 并发稳定性测试（无竞态检测）
```

Redis 后端测试默认使用内嵌 miniredis；如需验证真实 Redis，设置 `REDIS_TEST_ADDR=127.0.0.1:6379`。

---

## 项目结构

```
cmd/server/         入口（装配、信号、GUI 窗口启动）
internal/
  protocol/         二进制协议、帧、命令、ID 生成
  config/           配置加载（YAML+env）、热重载、运行时持久化
  server/           全局状态、Hub、房间、用户、命令派发、比赛、控制台缓冲
  network/          TCP 监听、会话、PROXY 协议、连接限速
  httpapi/          HTTP 服务、管理路由、WebSocket、OTP、GUI 页面
  cli/              终端 CLI 命令分发
  logging/          分级日志、轮转压缩、IP 黑名单
  replay/           回放录制、存储、读取、清理
  sharestation/     回放分享站客户端
  autoupload/       对局结束自动上传
  webhook/          事件外发（对局/房间/维护 → Discord/飞书/通用 JSON）
  cache/            通用缓存（内存+LRU+落盘）+ Redis 后端
  phira/            Phira API 客户端（认证/谱面/成绩，带缓存）
  procstats/        进程 CPU/内存采样（GUI 监控，平台适配）
  guiwindow/        浏览器应用窗口启动器
  l10n/             本地化（Fluent/FTL，内嵌 + 运行时覆盖）
```

---

## 许可证

[AGPL-3.0](License)

## 想说的话
这个项目是用AI赶工赶出来的，更新比较频繁，建议别在正式服务器上使用，可以用来测试玩一下，等后面完善后，再部署到正式环境叭
