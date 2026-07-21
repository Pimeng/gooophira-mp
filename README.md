# gooophira-mp

Phira 多人游戏服务端的 Go 实现

优点：
✅️ 纯 Go 实现，无需 CGO，单文件二进制，跨平台。

国内用户请转至 [CNB镜像仓库](https://cnb.cool/Pimeng233/gooophira-mp) [下载Release](https://cnb.cool/Pimeng233/gooophira-mp/-/releases) 更快哦

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
  - [可选 Agent](#可选-agent)
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
- 回放系统：核心进程录制 `.phirarec` 并提供 HTTP 下载/删除，可选 Agent 上传分享站
- 可选扩展进程：SQLite 统计、Webhook/飞书和分享站上传与核心游戏服务隔离
- 缓存方案：本地内存 + LRU + 落盘；支持 Redis 多实例共享
- 跨平台：Windows / Linux / macOS / FreeBSD，纯 Go 无 CGO

---

## 快速开始

1. 启动服务。首次运行会自动生成最小核心配置 `config/server.yaml`：

   ```bash
   ./phira-mp-server
   ```

   默认监听：
   - TCP 游戏端口：`12346`
   - HTTP 服务端口：`12347`（需在配置中开启 `HTTP_SERVICE: true`）

2. 按需编辑 `config/server.yaml`，或从 [`config.example`](config.example) 选择单个扩展文件放入 `config/`。

3. 管理服务：
   - 终端中直接输入 `help` 查看 CLI 命令
   - 浏览器打开 `http://<服务器IP>:12347/gui`，输入 `ADMIN_TOKEN`（配置中设置）进入网页控制台

---

## 配置

server 配置优先级：**环境变量 > 配置文件 > 内置默认值**。Agent 当前通过 `-config` 选择独立配置文件，不复用 server 的环境变量覆盖。新配置采用固定文件名的目录布局：

```text
config/
├── server.yaml       # 必须存在；首次启动自动生成
├── agent.yaml        # 可选：Agent 拥有的统计、通知和上传配置
├── network.yaml      # 可选：代理、DNS、CORS、真实 IP、HAProxy
├── replay.yaml       # 可选：存在即启用回放录制
└── redis.yaml        # 可选：存在即启用 Redis
```

每个文件必须包含 `version: 1`。当前 server 配置使用 `server.yaml`、`network.yaml`、`replay.yaml` 和 `redis.yaml`，Agent 独立加载 `agent.yaml`，不会把备份文件或其它 YAML 意外当成配置。server 仍识别旧 `webhook.yaml` 和 `stats.yaml` 以输出迁移警告，但不会执行其中的扩展逻辑。服务端可选文件不存在时，对应功能不会初始化；文件存在但为空、含未知键或非法值时，启动失败，热重载时则保留上一份有效配置。Agent 配置修改后当前需要重启 Agent。

不要把 [`config.example`](config.example) 整个目录复制为活动配置；只复制需要启用的扩展。例如启用回放：

```bash
cp config.example/replay.yaml config/replay.yaml
```

核心配置常用项见 [`config.example/server.yaml`](config.example/server.yaml)，其它字段按功能查看对应示例文件。`config.example/legacy/` 仅用于旧部署迁移，不能直接作为当前活动配置。多文件模式下，环境变量只能覆盖已存在的 server 扩展，不能单独安装扩展。

启用可选 Agent 扩展时，将 [`config.example/agent.yaml`](config.example/agent.yaml) 复制为 `config/agent.yaml`，再启动 `phira-mp-agent`。进程关系、配置所有权、联合启动、systemd、Windows 和迁移教程统一见 [Agent 分离部署与配置](docs/Agent.md)。旧的根目录 `agent_config.yml` 会被兼容读取，但已弃用。

### 旧配置迁移

已有 `server_config.yml` 时，服务会继续按旧格式启动并打印弃用提示。可以先预览拆分结果，再执行迁移：

```bash
./phira-mp-server config migrate --dry-run
./phira-mp-server config migrate --from server_config.yml --to config
```

迁移不会删除旧文件，也不会覆盖 `config/` 中已经存在的目标文件。迁移器会把核心配置拆入 server 文件，并把旧统计、Webhook 和分享站配置写入 `config/agent.yaml`，同时启用本地 Agent IPC；迁移后需要同时启动 `phira-mp-agent` 才能继续使用这些扩展。新旧配置同时存在时默认优先 `config/server.yaml`；可用 `-config` 显式启动旧格式，或用 `-config-dir` 指定新目录。

> **热重载**：任一已知 server 配置文件新增、修改或删除都会重新加载完整配置快照。端口、GUI、Redis 和 Agent IPC 等启动项修改后提示重启；其它支持项即时生效。`agent.yaml` 修改后需要重启 Agent。

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

除 `/admin/otp/*`（用于无 token 时经终端审批获取临时 token）外，所有 `/admin/*` 接口需 `X-Admin-Token` 或 `Authorization: Bearer <token>` 头鉴权。

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

### 可选 Agent

SQLite 统计、Webhook/飞书通知和分享站上传由独立的 `phira-mp-agent` 承担。Agent 未安装、启动较晚、重启或离线均不影响核心游戏服务；相关查询和上传接口在离线时明确返回 `503`，持久事件会在 Agent 恢复后继续处理。

完整启用、配置和生产部署教程见 [Agent 分离部署与配置](docs/Agent.md)。

### 回放录制与上传

- 添加 `config/replay.yaml` 后，对局结束自动生成 `.phirarec` 文件
- 可通过 HTTP 接口下载、删除
- 分享站手动上传和自动上传由 Agent 承担，在 `config/agent.yaml` 的 `REPLAY_UPLOAD` 中配置

### Redis 共享缓存

多实例部署时启用 Redis，使玩家鉴权 token（6h）和成绩记录（1h）在实例间共享。

配置示例：
```yaml
# config/redis.yaml
version: 1
HOST: "127.0.0.1"
PORT: 6379
# PASSWORD: "your_password"
DB: 0
```

文件存在后可用环境变量覆盖：`REDIS_ENABLED=true REDIS_HOST=... REDIS_PORT=...`。

启用后所有缓存读写走共享 Redis（键前缀 `cache:<name>:<key>`，按 TTL 过期），启动时把本地内存数据迁移过去。Redis 不可达时自动降级为本地缓存。

### 比赛模式

通过 CLI 或 HTTP API 管理房间的比赛模式（开启/关闭/白名单/开始），适合组织竞技赛事。

### Webhook 通知

Agent 把服务器持久事件异步推送到群机器人或自建服务，投递带幂等账本、超时与失败重试，目标不可达也不阻塞对局逻辑。配置位于 `config/agent.yaml`，修改后重启 Agent。

- 事件类型：`room_create`、`room_disband`、`user_join`、`game_start`、`game_end`、`maintenance`
- 载荷格式（`TYPE`）：`generic`（结构化 JSON，自定义机器人自行渲染）、`discord`、`onebot_v11`、`feishu`
- 可选 `SECRET`：请求带 `X-Phira-Signature: sha256=<HMAC(body)>` 头供接收端验签

完整配置示例见 [`config.example/agent.yaml`](config.example/agent.yaml) 和 [Agent 配置教程](docs/Agent.md#41-webhook)。

---

## 构建与开发

### 环境要求

- Go 1.26+
- 无需 CGO（`CGO_ENABLED=0` 即可）
- 可选：Redis（用于共享缓存）

### 编译当前平台

```bash
go build -o phira-mp-server ./cmd/server
go build -o phira-mp-agent ./cmd/agent
```

版本号可注入（优先级：环境变量 `PHIRA_MP_VERSION` > ldflags > 内嵌 VERSION 文件 > 构建信息 > `dev`）：

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

镜像多阶段构建（`CGO_ENABLED=0` 静态编译，运行层为带 CA 证书的最小 Alpine），同时包含 `phira-mp-server` 和 `phira-mp-agent`。默认入口是 server，容器以非 root 用户运行，工作目录 `/data` 即数据目录（配置/日志/回放/缓存均持久化于此）。

```bash
# 单容器
docker build -t phira-mp:local .
docker run -d --name phira-mp -p 12346:12346 -p 12347:12347 \
  -e ADMIN_TOKEN=changeme -v phira-data:/data phira-mp:local

# 一键栈（server + Redis）
cp .env.example .env      # 按需填 ADMIN_TOKEN 等
docker compose up -d --build

# 可选：在 .env 中配置 AGENT_IPC_ENDPOINT 与 AGENT_CONFIG_FILE 后启动 Agent
docker compose --profile agent up -d --build
```

容器内默认 `HTTP_SERVICE=true` 并内置 `HEALTHCHECK`；首次运行自动生成 `/data/config/server.yaml`，已有 `/data/server_config.yml` 的数据卷继续兼容旧格式。Compose 会挂载 `redis.yaml` 以启用 Redis。需要 Agent 时，在 `.env` 中把 `AGENT_IPC_ENDPOINT` 设为 `unix:///data/agent.sock`，将 `AGENT_CONFIG_FILE` 指向实际的 `agent.yaml`，再启用 `agent` profile；完整要求见 [Agent 部署文档](docs/Agent.md)。`internal/common/platform/version/VERSION` 升高或手动触发 [`docker-image.yml`](.github/workflows/docker-image.yml) 会构建 `amd64`+`arm64` 多架构镜像推送 GHCR。

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
cmd/agent/          可选扩展进程入口（IPC 消费、统计、通知、上传）
cmd/bench/          基准/压测工具
cmd/tcpconnectbench/ TCP 连接洪水压测工具
internal/
  protocol/         二进制协议、帧、命令、ID 生成
  config/           配置加载（YAML+env）、热重载、运行时持久化
  server/           全局状态、Hub、房间、用户、命令派发、比赛、控制台缓冲
  network/          TCP 监听、会话、PROXY 协议、连接限速
  httpapi/          HTTP 服务、管理路由、WebSocket、OTP、GUI 页面
  cli/              终端 CLI 命令分发
  logging/          分级日志、轮转压缩、IP 黑名单
  replay/           回放录制、存储、读取、清理
  agent*/           server/Agent IPC、持久事件、处理器和扩展状态
  sharestation/     Agent 使用的回放分享站客户端
  webhook/          Agent 使用的事件外发适配器
  cache/            通用缓存（内存+LRU+落盘）+ Redis 后端
  phira/            Phira API 客户端（认证/谱面/成绩，带缓存）
  procstats/        进程 CPU/内存采样（GUI 监控，平台适配）
  guiwindow/        浏览器应用窗口启动器
  l10n/             本地化（Fluent/FTL，内嵌 + 运行时覆盖）
  stats/            Agent 使用的 SQLite 统计存储
  version/          版本号（环境变量/ldflags/VERSION 文件/构建信息/dev）
```

---

## 许可证

[AGPL-3.0](License)

## 想说的话

这个项目是用AI赶工赶出来的，更新比较频繁，建议别在正式服务器上使用，可以用来测试玩一下，等后面完善后，再部署到正式环境叭

## 画饼

- [x] 优化成绩输出，使用服务器的消息系统，而不是使用自带的成绩包
- [x] 准备倒计时
