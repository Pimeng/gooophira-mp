# HTTP API 文档

本文档记录 `gooophira-mp-server` 当前已经实现的 HTTP API。除特别说明外，JSON 接口返回：

```json
{
  "ok": true
}
```

失败响应通常为：

```json
{
  "ok": false,
  "error": "错误码",
  "message": "本地化提示"
}
```

## 1. 服务基础

### 1.1 地址与配置

默认 HTTP 服务配置：

```yaml
HTTP_SERVICE: false
HTTP_PORT: 12347
HTTP_RATE_LIMIT_MAX_REQUESTS: 100
HTTP_RATE_LIMIT_WINDOW_MS: 60000
ADMIN_TOKEN: "replace_me"
ALLOW_TOKEN_IN_QUERY: false
CORS_ORIGINS: []
```

Docker 镜像默认开启 HTTP 服务。HTTP 服务由 `internal/httpapi` 提供；统一路由、CORS、限流和错误分发位于 `internal/httpapi/server.go`。

### 1.2 通用行为

- `OPTIONS` 请求返回 `204 No Content`。
- JSON 响应的 `Content-Type` 为 `application/json; charset=utf-8`。
- 配置了 `CORS_ORIGINS` 时才返回 CORS 响应头。
- 允许的方法为 `GET`、`POST`、`OPTIONS`。
- 普通 HTTP 请求按客户端 IP 限流；超过限制返回 `429`、`error: rate-limited` 和 `Retry-After` 响应头。
- `/ws` 是长连接，不计入普通 HTTP 限流。
- 未匹配路由返回 `404`。

### 1.3 管理员鉴权

除 `/admin/otp/*` 外，所有 `/admin/*` 接口都需要管理员 token。推荐通过请求头传递：

```http
X-Admin-Token: <token>
```

或者：

```http
Authorization: Bearer <token>
```

token 提取优先级为：`X-Admin-Token`、`Authorization: Bearer`、可选的查询参数 `?token=`。查询参数鉴权只有在 `ALLOW_TOKEN_IN_QUERY: true` 时启用，生产环境不建议开启。

常见鉴权错误：

| 状态码 | error | 说明 |
| ---: | --- | --- |
| 401 | `unauthorized` | token 缺失或无效 |
| 401 | `token-expired` | 临时 token 过期或 IP 不匹配 |
| 403 | `admin-disabled` | 未配置永久管理员 token |
| 429 | `rate-limited` | 请求超过 IP 限流 |

## 2. 公开接口

这里的“公开”表示不经过管理员鉴权，不代表一定匿名可用。回放接口仍需要用户 token 或回放会话 token；统计接口依赖 Agent。

### 2.1 房间列表

```http
GET /room
```

成功响应示例：

```json
{
  "rooms": [
    {
      "roomid": "room1",
      "cycle": false,
      "lock": false,
      "host": {"name": "alice", "id": "1"},
      "state": "select_chart",
      "chart": {"name": "Chart Name", "id": "42"},
      "players": [{"name": "alice", "id": 1}]
    }
  ],
  "total": 1
}
```

以下划线开头的私有房间不会返回。响应缓存约 2 秒。`total` 表示所有公开房间中的玩家总数，不是房间数量。

### 2.2 功能开关查询

```http
GET /room-creation/config
GET /replay/config
```

响应：

```json
{"ok": true, "enabled": true}
```

### 2.3 回放接口

#### 认证并列出回放

```http
POST /replay/auth
Content-Type: application/json
```

请求：

```json
{"token": "phira-user-token"}
```

成功响应包含 `userId`、`charts`、`sessionToken` 和 `expiresAt`。回放会话默认有效期为 30 分钟。

常见错误：缺少 token 返回 `400 bad-token`；用户 token 无效返回 `401 unauthorized`。

#### 下载回放

```http
GET /replay/download?sessionToken=<token>&chartId=42&timestamp=1710000000000
```

成功返回 `.phirarec` 二进制文件。会校验会话、用户、谱面和文件归属。参数错误返回 `400`，会话无效返回 `401`，文件不存在或归属不匹配返回 `404`。

#### 删除回放

```http
POST /replay/delete
Content-Type: application/json
```

请求：

```json
{
  "sessionToken": "uuid",
  "chartId": 42,
  "timestamp": 1710000000000
}
```

成功返回 `{"ok": true}`。

#### 上传回放

```http
POST /replay/upload
Content-Type: application/json
```

请求：

```json
{
  "token": "phira-user-token",
  "chartId": 42,
  "timestamp": 1710000000000
}
```

上传由 Agent 处理，Server 只负责认证并通过 IPC 转发。Agent 不可用返回 `503 agent-unavailable`，其他代理错误通常返回 `502`。

#### 自动上传配置

```http
GET /replay/auto-upload/config?token=<phira-user-token>
POST /replay/auto-upload/config
```

POST 请求：

```json
{
  "token": "phira-user-token",
  "show": true
}
```

该接口需要 Phira 用户 token，并依赖 Agent。

### 2.4 统计查询

以下接口不需要管理员 token，但依赖 Agent 的统计服务；Agent 离线时通常返回 `503`：

```http
GET /charts/hot?limit=20
GET /chart/:id
GET /player/:id
GET /player/:id/recent?limit=10
GET /leaderboard?sort=rating&limit=20
```

参数规则：

- `limit` 默认热门谱面和排行榜为 `20`，最大 `100`。
- 玩家近期成绩默认 `10`，只接受 `1~50`，非法值回退为 `10`。
- `sort` 支持 `rating`、`playtime`、`score`，非法值回退为 `rating`。
- `:id` 必须是正整数，否则返回 `400` 和 `invalid-chart-id` 或 `invalid-player-id`。

统计结果由 Agent 返回，Server 不重新定义完整业务字段。

## 3. GUI

```http
GET /gui
GET /gui/
GET /gui/guipage.css
GET /gui/guipage.js
```

页面和静态资源本身不要求管理员 token。页面内部访问 `/admin/*` 或管理员 WebSocket 频道时仍然必须提供 token。GUI 页面不缓存。

## 4. 管理接口

所有以下接口都需要管理员 token。

### 4.1 状态查询

```http
GET /admin/rooms
GET /admin/users
GET /admin/users/:id
GET /admin/metrics
```

`/admin/users/:id` 成功返回用户详情；用户 ID 非整数返回 `400 bad-user-id`，用户不存在返回 `404 user-not-found`。

`/admin/metrics` 返回进程、内存、CPU、业务状态和 Agent 状态。增加 `?history=1` 时返回采样历史。

### 4.2 用户操作

```http
POST /admin/users/:id/move
POST /admin/users/:id/disconnect
```

迁移请求：

```json
{"roomId": "target-room", "monitor": false}
```

断开接口无请求体。用户不存在、目标房间不存在或用户未连接时返回对应的 `404` 业务错误。

### 4.3 封禁、解散和广播

```http
POST /admin/ban/user
POST /admin/ban/room
POST /admin/disband
POST /admin/broadcast
```

全局封禁：

```json
{"userId": 100, "banned": true, "disconnect": true}
```

房间封禁：

```json
{"userId": 100, "roomId": "room1", "banned": true}
```

解散房间：

```json
{"roomid": "room1"}
```

全服广播：

```json
{"message": "服务器公告"}
```

### 4.4 运行时配置

```http
GET /admin/runtime-config
POST /admin/runtime-config
POST /admin/runtime-config/rollback
```

POST 接受可热更新配置项的 JSON patch。成功返回 `updatedKeys`、当前 `config` 和是否可回滚；非法键或不可热更新的键返回 `400 bad-runtime-config`。没有回滚快照时返回 `409 runtime-config-rollback-unavailable`。

### 4.5 开关配置

```http
GET /admin/replay/config
POST /admin/replay/config
GET /admin/room-creation/config
POST /admin/room-creation/config
```

POST 请求格式：

```json
{"enabled": true}
```

缺失或错误类型返回 `400 bad-enabled`。

### 4.6 控制台

```http
GET /admin/console/logs?limit=200
POST /admin/console/command
```

执行命令请求：

```json
{"command": "rooms"}
```

命令最大长度为 500。命令为空返回 `400 bad-command`，控制台未准备好返回 `503 console-not-ready`。

### 4.7 比赛模式

```http
POST /admin/contest/rooms/:id/config
POST /admin/contest/rooms/:id/whitelist
POST /admin/contest/rooms/:id/start
```

配置请求示例：

```json
{"enabled": true, "whitelist": [100, 101]}
```

白名单请求：

```json
{"userIds": [100, 101, 102]}
```

开始比赛可传：

```json
{"force": true}
```

## 5. OTP 和 CLI 提权

这些接口用于没有管理员 token 时，通过终端审批获取临时 token；它们不经过普通管理员鉴权。

### 5.1 申请会话

```http
POST /admin/otp/request
```

可选请求：

```json
{"mode": "otp"}
```

或：

```json
{"mode": "cli"}
```

返回 `ssid`、`expiresIn` 和 `mode`。OTP 验证码打印在服务端终端，不通过 HTTP 返回。配置永久 `ADMIN_TOKEN` 时该功能返回 `403 otp-disabled-when-token-configured`。

### 5.2 验证或查询审批

```http
POST /admin/otp/verify
```

OTP 模式：

```json
{"mode": "otp", "ssid": "session-id", "otp": "验证码"}
```

CLI 模式：

```json
{"mode": "cli", "ssid": "session-id"}
```

CLI 审批等待时返回 `202 pending-approval`；拒绝返回 `403 approval-denied`；成功返回临时管理员 token 及过期时间。

## 6. WebSocket

```text
WS /ws
```

连接本身不要求 token。单条入站消息最大 64 KiB，慢客户端或发送队列满时连接会被关闭。

### 6.1 Ping

```json
{"type": "ping"}
```

返回：

```json
{"type": "pong"}
```

### 6.2 房间订阅

```json
{"type": "subscribe", "roomId": "room1"}
```

成功返回 `subscribed` 和初始 `room_update`。取消订阅：

```json
{"type": "unsubscribe"}
```

### 6.3 管理员订阅

```json
{"type": "admin_subscribe", "token": "admin-token"}
```

成功返回 `admin_subscribed`，随后推送 `admin_update`。取消：

```json
{"type": "admin_unsubscribe"}
```

### 6.4 控制台日志订阅

```json
{"type": "console_subscribe", "token": "admin-token"}
```

成功先回填 `console_subscribed`，之后推送 `console_log`。取消：

```json
{"type": "console_unsubscribe"}
```

## 7. 飞书机器人快捷创建

该接口复用现有 HTTP 服务和 Agent IPC。管理员访问接口后，Agent 调用飞书 SDK 创建应用并返回二维码链接；服主扫码确认后，凭据会自动写入 Agent 配置文件，并立即更新运行中的 Webhook 配置。

### 7.1 架构边界

```text
现有 HTTP 服务
    ↓ /admin/feishu/app-registration
Server 通过 Agent IPC 转发
    ↓
Agent 调用 registration.RegisterApp
    ↓
Agent 写入 agent.yaml
    ↓
Dispatcher.SetConfig 热更新 Webhook
```

HTTP 服务只负责管理员鉴权和 IPC 转发；注册任务、二维码、飞书密钥和配置写入均由 Agent 处理。

### 7.2 创建注册任务

```http
POST /admin/feishu/app-registration
Content-Type: application/json
X-Admin-Token: <token>
```

请求示例：

```json
{
  "target_id": "feishu-primary",
  "receive_open_id": "",
  "events": ["game_start", "game_end"],
  "live_update": true
}
```

字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `target_id` | string | 是 | 要写入或替换的 Webhook 目标 ID |
| `receive_open_id` | string | 否 | 接收人 Open ID；为空时使用扫码用户 Open ID |
| `events` | string[] | 否 | 订阅事件；为空时默认 `game_start`、`game_end` |
| `live_update` | bool | 否 | 是否启用成绩卡片实时更新 |

成功时立即返回任务状态，不等待扫码完成：

```json
{
  "ok": true,
  "task_id": "fr_xxx",
  "status": "waiting_qr",
  "qr_url": "https://accounts.feishu.cn/...",
  "qr_expires_at": "2026-07-21T12:00:00Z"
}
```

如果 Agent 未连接，返回：

```json
{
  "ok": false,
  "error": "agent-unavailable"
}
```

状态接口通过现有 IPC 查询，HTTP 请求最长等待约 65 秒；正常情况下 Agent 会立即返回任务，不会等待用户完成扫码。

### 7.3 查询注册任务

```http
GET /admin/feishu/app-registration/:taskID
X-Admin-Token: <token>
```

二维码生成后会返回：

```json
{
  "ok": true,
  "task_id": "fr_xxx",
  "status": "qr_ready",
  "qr_url": "https://accounts.feishu.cn/...",
  "qr_expires_at": "2026-07-21T12:00:00Z",
  "interval": 5
}
```

前端可以使用 `qr_url` 生成二维码，也可以提供复制链接按钮。`interval` 是飞书 SDK 建议的下一次轮询间隔，单位为秒。

当前可能的状态包括：

```text
pending
waiting_qr
qr_ready
polling
slow_down
domain_switched
completed
failed
cancelled
```

注册成功时只返回 App ID 和扫码用户 Open ID，不返回 App Secret：

```json
{
  "ok": true,
  "task_id": "fr_xxx",
  "status": "completed",
  "client_id": "cli_xxx",
  "user_open_id": "ou_xxx"
}
```

任务不存在时返回 `404 task-not-found`。

### 7.4 取消注册任务

```http
DELETE /admin/feishu/app-registration/:taskID
X-Admin-Token: <token>
```

取消仍在运行的任务后返回：

```json
{
  "ok": true,
  "task_id": "fr_xxx",
  "status": "cancelled"
}
```

已完成、已失败或已取消的任务不能再次取消，返回 `409 task-not-active`。任务默认使用 10 分钟超时；取消会终止 SDK 注册流程。

### 7.5 SDK 注册参数

注册使用 [lark-go.md](../lark-go.md) 中的 `registration.RegisterApp`，权限严格限制为截图中的三个应用身份权限：

- `cardkit:card:write`
- `im:message:send_as_bot`
- `im:resource`

实际调用参数为：

```go
preset := false

result, err := registration.RegisterApp(ctx, &registration.Options{
    CreateOnly: true,
    Addons: &registration.AppAddons{
        Preset: &preset,
        Scopes: registration.AppAddonsScopes{
            Tenant: []string{
                "cardkit:card:write",
                "im:message:send_as_bot",
                "im:resource",
            },
        },
    },
    OnQRCode: func(info *registration.QRCodeInfo) {
        // 保存二维码 URL 和有效期
    },
    OnStatusChange: func(info *registration.StatusChangeInfo) {
        // 更新任务状态和建议轮询间隔
    },
})
```

不会添加用户身份权限、事件订阅或回调权限。`CreateOnly: true` 确保该入口只创建新应用。

### 7.6 注册完成后的配置

注册成功后：

- `result.ClientID` 写入 `APP_ID`；
- `result.ClientSecret` 写入 `APP_SECRET`；
- `result.UserInfo.OpenID` 在未指定 `receive_open_id` 时写入 `RECEIVE_OPEN_ID`；
- 请求中的 `target_id`、`events`、`live_update` 写入飞书目标；
- 已有 `TYPE: feishu` 目标会被替换；其他类型目标和其他 Agent 配置会保留。

目标配置示例：

```yaml
version: 1
ENABLED: true
TIMEOUT_MS: 5000
RETRIES: 2
TARGETS:
  - ID: feishu-primary
    TYPE: feishu
    APP_ID: cli_xxx
    APP_SECRET: xxx
    RECEIVE_OPEN_ID: ou_xxx
    EVENTS: [game_start, game_end]
    LIVE_UPDATE: true
```

Agent 使用 YAML 结构化写入、临时文件和替换操作更新 `agent.yaml`，随后重新加载配置并调用 `Dispatcher.SetConfig`。仅修改文件而不触发重载不会更新当前运行中的 Dispatcher。

### 7.7 凭据安全

- 所有注册接口都位于 `/admin/*` 下。
- `APP_SECRET` 不通过 HTTP 返回，不写入浏览器存储，也不输出到普通日志。
- 二维码链接只在任务状态中短期保存。
- 注册任务由 Agent 在内存中管理。
- Agent 重启后，未完成的注册任务不会恢复，需要重新创建任务。

### 7.8 错误映射

| HTTP 状态 | error | 说明 |
| ---: | --- | --- |
| 400 | `invalid-request` | 请求体或 action 无效 |
| 400 | `target_id-required` | 缺少 `target_id` |
| 404 | `task-not-found` | 注册任务不存在 |
| 409 | `task-not-active` | 任务已经完成、失败或取消 |
| 503 | `agent-unavailable` | Agent 未连接或 IPC 查询失败 |

飞书 SDK 错误会保存在任务的 `error` 字段，并将任务状态设置为 `failed`。二维码过期、用户拒绝授权和网络错误均不会返回 App Secret。

### 7.9 当前实现位置

- HTTP 路由：`internal/httpapi/feishu_registration.go`
- 管理路由接入：`internal/httpapi/admin.go`
- Agent 注册任务：`internal/agentfeishu/manager.go`
- IPC 方法和请求响应结构：`internal/agentproto/protocol.go`
- Agent 装配：`cmd/agent/main.go`
- Agent 配置加载：`internal/config/agent_file.go`
- Webhook 热更新：`internal/webhook/webhook.go`

## 8. 实现状态和代码索引

### 当前已实现

- HTTP 服务、CORS、OPTIONS 和 IP 限流
- GUI 页面和静态资源
- 房间、回放、统计查询
- 管理员鉴权、OTP、CLI 提权
- 管理员状态、用户、房间、比赛模式和运行时配置接口
- WebSocket 房间、管理员和控制台订阅
- Agent 统计、回放上传和 Webhook 相关的 IPC 边界
- 飞书应用二维码/链接快捷创建、状态查询和取消
- 飞书注册成功后自动写入 Agent 配置并热更新 Webhook

### 尚待实现

- 二维码/链接快捷创建的 GUI 专用页面
- 飞书目标测试发送接口
- Agent 重启后恢复未完成的飞书注册任务

### 主要实现位置

- HTTP 统一路由：`internal/httpapi/server.go`
- 管理员鉴权：`internal/httpapi/admin.go`
- 飞书注册接口：`internal/httpapi/feishu_registration.go`
- 飞书注册任务：`internal/agentfeishu/manager.go`
- 回放：`internal/httpapi/replay.go`
- 统计：`internal/httpapi/stats_handler.go`
- WebSocket：`internal/httpapi/ws.go`
- GUI：`internal/httpapi/guipage.*`
- Agent IPC：`internal/agentipc/`、`internal/agentproto/`
- Agent 配置：`internal/config/agent_file.go`、`internal/config/webhook_file.go`
- 飞书消息适配器：`internal/webhook/adapter/feishu.go`
- 飞书 SDK 注册说明：`lark-go.md`

## 9. 相关文档

- [Agent 分离部署与配置](agent.md)
- [项目 README](../README.md)
