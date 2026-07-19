# Agent 分离部署与配置

本文是 `phira-mp-agent` 的统一使用文档，覆盖进程关系、配置所有权、前台启动、systemd、Windows、升级迁移和故障排查。配置字段的可直接使用模板见 [`config.example/agent.yaml`](../config.example/agent.yaml)。

## 1. 什么时候需要 Agent

`phira-mp-server` 始终负责实时游戏、房间、回放录制、本地回放下载和删除。下列可选扩展由 Agent 负责：

- SQLite 统计、排行榜和热门谱面；
- Webhook、Discord、OneBot v11 和飞书通知；
- 分享站手动上传和对局结束后的自动上传。

只需要核心游戏服务时，不必安装或启动 Agent，并在 `config/server.yaml` 保持 `AGENT_IPC.ENDPOINT: disabled`。

启用任一上述扩展时，应同时运行 server 和 Agent。两者仍是独立进程：

- server 不依赖 Agent 才能启动；
- Agent 可以晚启动、单独重启或临时离线；
- Agent 离线时，对局、房间和回放录制继续工作；
- 统计查询以及上传相关 HTTP 接口会返回 `503`；
- server 将事件保存在持久化 outbox，Agent 恢复后继续消费。

```text
phira-mp-server
  |-- 游戏 TCP / 管理 HTTP / 回放写入
  |-- agent-outbox（持久事件）
  `-- 本地 IPC + discovery 文件
            |
            v
phira-mp-agent
  |-- agent-inbox（持久消费状态）
  |-- SQLite 统计
  |-- Webhook / 飞书
  `-- 分享站上传（只读取已关闭的回放）
```

## 2. 构建与目录

构建两个二进制，不需要 CGO：

```bash
CGO_ENABLED=0 go build -o phira-mp-server ./cmd/server
CGO_ENABLED=0 go build -o phira-mp-agent ./cmd/agent
```

本地运行推荐目录：

```text
gooophira-mp/
├── phira-mp-server
├── phira-mp-agent
├── config/
│   ├── server.yaml
│   ├── agent.yaml
│   └── replay.yaml        # 需要回放时
├── agent-outbox/
├── agent-inbox/
├── record/
└── agent-ipc.json
```

复制示例：

```bash
mkdir -p config
cp config.example/server.yaml config/server.yaml
cp config.example/agent.yaml config/agent.yaml
# 使用回放或分享站上传时：
cp config.example/replay.yaml config/replay.yaml
```

不要把整个 `config.example` 复制为活动配置；旧 `stats.yaml` 和 `webhook.yaml` 仅用于迁移参考，server 不再执行其中的扩展逻辑。

## 3. Server 配置

在 `config/server.yaml` 启用本地 Agent IPC：

```yaml
version: 1

AGENT_IPC:
  ENDPOINT: auto
  INSTANCE: default
  DISCOVERY_FILE: agent-ipc.json
  OUTBOX_DIR: agent-outbox
  OUTBOX_MAX_MB: 64
  WEBHOOK_OWNER: agent
  TOKEN: ""
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `ENDPOINT` | `disabled` 关闭；`auto` 在 Unix 使用 Unix Domain Socket、Windows 使用 Named Pipe，本地 IPC 创建失败时才回退到随机回环 TCP。也可显式配置 `unix://`、`npipe://` 或仅回环 `tcp://`。 |
| `INSTANCE` | 多实例标识，用于生成默认 socket 或 pipe 名。 |
| `DISCOVERY_FILE` | server 原子写入的发现文件，包含实际 endpoint 和随机 token。Agent 必须能读取。 |
| `OUTBOX_DIR` | server 持久事件目录。Agent 离线时事件保留在这里。 |
| `OUTBOX_MAX_MB` | outbox 硬容量上限。 |
| `WEBHOOK_OWNER` | 固定使用 `agent`；`server` 已不再受支持。 |
| `TOKEN` | 通常留空，由 server 每次启动随机生成并写入受限权限的 discovery 文件。固定 token 可通过 `AGENT_IPC_TOKEN` 注入。 |

`AGENT_IPC` 属于启动配置，修改后需要重启 server。IPC 只接受本机 transport；显式 TCP 地址必须是字面量回环地址。

## 4. Agent 配置

Agent 默认读取 `config/agent.yaml`，可用 `-config <path>` 显式覆盖。完整可加载模板位于 [`config.example/agent.yaml`](../config.example/agent.yaml)。没有该文件时 Agent 仍可连接并消费事件，但不会启用扩展。Agent 当前不读取 server 的扩展环境变量，敏感值应写入受限权限的 Agent 配置文件，或由部署系统在启动前安全生成该文件。

### 4.1 Webhook

Webhook 配置位于 Agent 文件顶层：

```yaml
version: 1
ENABLED: true
TIMEOUT_MS: 5000
RETRIES: 2
TARGETS:
  - ID: primary-generic
    TYPE: generic
    URL: "https://example.com/hook"
    SECRET: "replace_me"
    EVENTS: [room_create, game_start, game_end]
```

支持 `generic`、`discord`、`onebot_v11` 和 `feishu`。各目标的完整字段及事件列表见 [`config.example/webhook.yaml`](../config.example/webhook.yaml)，将其中的 `ENABLED`、`TIMEOUT_MS`、`RETRIES` 和 `TARGETS` 合并到 `agent.yaml` 顶层即可。

每个目标建议设置稳定且唯一的 `ID`。Agent 会按事件 ID 和目标 ID 持久化投递结果，重启或重复事件不会重复投递已完成目标。

### 4.2 SQLite 统计

```yaml
STATS:
  ENABLED: true
  DB_PATH: data/stats.db
  DETAIL_RETENTION_DAYS: 90
  DB_MAX_MB: 500
```

- `DB_PATH` 是 Agent 拥有的 SQLite 文件；从旧部署迁移时应指向原数据库或先复制原文件。
- Agent 每日清理过期明细并在需要时整理数据库。
- Agent 离线时 `/player/*`、`/leaderboard`、`/chart/*` 等统计查询返回 `503`，server 不会回退到本地 SQLite。

### 4.3 分享站上传

```yaml
REPLAY_UPLOAD:
  ENABLED: true
  AUTO_UPLOAD: true
  BASE_DIR: ./record
  URL: "https://replay.example.com"
  TOKEN: "replace_me"
  STATE_PATH: agent-inbox/upload-state.json
  DELAY_MS: 30000
```

要求：

- server 的 `config/replay.yaml` 必须启用回放；
- `BASE_DIR` 必须与 server 的 `REPLAY_BASE_DIR` 指向同一目录；
- Agent 只接收结构化 replay ID，不接受事件中的任意文件路径；
- Agent 需要读取回放并在上传成功后删除文件的权限；
- 上传任务、尝试次数、结果、幂等记录和用户可见性先写入 `STATE_PATH`，之后才删除本地回放；
- 自动上传失败会持久化并按指数退避重试。

分享站 URL 和 token 不再放入 server 配置。旧 `config/replay.yaml` 中的 `REPLAY_AUTO_UPLOAD` 与 `SHARE_STATION` 仅用于迁移，不应继续作为新部署配置。

### 4.4 配置所有权与重载

| 配置 | 所有者 | 热重载 |
| --- | --- | --- |
| `config/server.yaml` 的核心服务字段 | server | 部分字段支持，`AGENT_IPC` 需重启 |
| `config/replay.yaml` 的录制目录和保留策略 | server | 按 server 现有配置规则 |
| `config/agent.yaml` 的 Webhook、SQLite、分享站密钥 | Agent | 当前需重启 Agent |

两个进程应使用同一工作目录，或者全部使用绝对路径。生产环境推荐绝对路径，避免 systemd 的工作目录与交互式终端不同。

## 5. 前台联合启动

开发或临时部署可在同一终端用以下脚本启动。server 先启动，Agent 自己等待 discovery 文件并重试；Agent 退出不会主动终止 server。

### Linux / macOS

保存为 `start-all.sh`：

```bash
#!/usr/bin/env bash
set -u

SERVER=${SERVER:-./phira-mp-server}
AGENT=${AGENT:-./phira-mp-agent}

"$SERVER" -config-dir config &
server_pid=$!

"$AGENT" -config config/agent.yaml -discovery agent-ipc.json &
agent_pid=$!

cleanup() {
  kill -TERM "$agent_pid" 2>/dev/null || true
  kill -TERM "$server_pid" 2>/dev/null || true
  wait "$agent_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

wait "$server_pid"
```

```bash
chmod +x start-all.sh
./start-all.sh
```

### Windows PowerShell

保存为 `start-all.ps1`：

```powershell
$server = Start-Process -FilePath ".\phira-mp-server.exe" `
  -ArgumentList "-config-dir", "config" -PassThru -NoNewWindow

$agent = Start-Process -FilePath ".\phira-mp-agent.exe" `
  -ArgumentList "-config", "config\agent.yaml", "-discovery", "agent-ipc.json" `
  -PassThru -NoNewWindow

try {
  Wait-Process -Id $server.Id
} finally {
  if (-not $agent.HasExited) { Stop-Process -Id $agent.Id }
  if (-not $server.HasExited) { Stop-Process -Id $server.Id }
}
```

生产环境不建议依赖前台脚本管理重启和日志；Linux 请使用 systemd。

## 6. systemd 正式部署

以下示例使用同一个低权限用户 `phira-mp`，这是共享 discovery、回放目录和持久状态最简单可靠的方式。这种方式提供进程职责隔离，但不提供操作系统级密钥隔离；同一用户运行的 server 理论上可以读取 `agent.yaml`。若需要严格限制 server 对扩展密钥的访问，应拆分运行用户，并通过专用共享组只开放 discovery、outbox 和回放目录。

### 6.1 安装目录

```bash
sudo useradd --system --home /var/lib/phira-mp --shell /usr/sbin/nologin phira-mp
sudo install -d -o phira-mp -g phira-mp -m 0700 /var/lib/phira-mp
sudo install -d -o root -g phira-mp -m 0750 /etc/phira-mp
sudo install -d -o root -g root -m 0755 /opt/phira-mp
sudo install -o root -g root -m 0755 phira-mp-server phira-mp-agent /opt/phira-mp/
sudo install -o root -g phira-mp -m 0640 config.example/server.yaml /etc/phira-mp/server.yaml
sudo install -o root -g phira-mp -m 0640 config.example/agent.yaml /etc/phira-mp/agent.yaml
```

在 `/etc/phira-mp/server.yaml` 使用绝对路径：

```yaml
AGENT_IPC:
  ENDPOINT: auto
  INSTANCE: default
  DISCOVERY_FILE: /var/lib/phira-mp/agent-ipc.json
  OUTBOX_DIR: /var/lib/phira-mp/agent-outbox
  OUTBOX_MAX_MB: 64
  WEBHOOK_OWNER: agent
  TOKEN: ""
```

在 `/etc/phira-mp/agent.yaml` 中同样使用绝对路径，例如：

```yaml
STATS:
  ENABLED: true
  DB_PATH: /var/lib/phira-mp/stats.db
  DETAIL_RETENTION_DAYS: 90
  DB_MAX_MB: 500

REPLAY_UPLOAD:
  ENABLED: true
  AUTO_UPLOAD: true
  BASE_DIR: /var/lib/phira-mp/record
  URL: "https://replay.example.com"
  TOKEN: "replace_me"
  STATE_PATH: /var/lib/phira-mp/agent-inbox/upload-state.json
  DELAY_MS: 30000
```

需要回放时，把 `replay.yaml` 安装到 `/etc/phira-mp/`，并将 `REPLAY_BASE_DIR` 设为 `/var/lib/phira-mp/record`。

### 6.2 Server unit

创建 `/etc/systemd/system/phira-mp-server.service`：

```ini
[Unit]
Description=Phira Multiplayer Server
After=network-online.target
Wants=network-online.target
PartOf=phira-mp.target

[Service]
Type=simple
User=phira-mp
Group=phira-mp
WorkingDirectory=/var/lib/phira-mp
ExecStart=/opt/phira-mp/phira-mp-server -config-dir /etc/phira-mp
Restart=on-failure
RestartSec=3s
TimeoutStopSec=20s
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/phira-mp

[Install]
WantedBy=multi-user.target
```

### 6.3 Agent unit

创建 `/etc/systemd/system/phira-mp-agent.service`：

```ini
[Unit]
Description=Phira Multiplayer Optional Agent
After=network-online.target phira-mp-server.service
Wants=network-online.target phira-mp-server.service
PartOf=phira-mp.target

[Service]
Type=simple
User=phira-mp
Group=phira-mp
WorkingDirectory=/var/lib/phira-mp
ExecStart=/opt/phira-mp/phira-mp-agent \
  -config /etc/phira-mp/agent.yaml \
  -discovery /var/lib/phira-mp/agent-ipc.json \
  -inbox /var/lib/phira-mp/agent-inbox/events.log
Restart=on-failure
RestartSec=3s
TimeoutStopSec=20s
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/phira-mp

[Install]
WantedBy=multi-user.target
```

这里刻意只让 Agent `Wants` server。server unit 不包含 `Requires=phira-mp-agent.service`、`BindsTo=` 或反向依赖，因此 Agent 崩溃不会导致核心服务停止。

### 6.4 聚合 target

创建 `/etc/systemd/system/phira-mp.target`：

```ini
[Unit]
Description=Phira Multiplayer Server and Optional Agent
Wants=phira-mp-server.service phira-mp-agent.service
After=network-online.target

[Install]
WantedBy=multi-user.target
```

加载并启动：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now phira-mp.target
systemctl status phira-mp-server.service phira-mp-agent.service
```

常用维护命令：

```bash
journalctl -u phira-mp-server -f
journalctl -u phira-mp-agent -f
sudo systemctl restart phira-mp-agent
sudo systemctl stop phira-mp.target
```

仅运行核心服务时：

```bash
sudo systemctl disable --now phira-mp.target
sudo systemctl disable --now phira-mp-agent.service
sudo systemctl enable --now phira-mp-server.service
```

## 7. 安全和权限

- `agent.yaml` 包含 Webhook、飞书和分享站密钥，不要提交到版本库或写入普通事件。
- discovery 文件包含本次启动的 IPC token，目录和文件应仅运行用户可读。
- 推荐 `UMask=0077`，配置文件权限至少为 `0640`，状态目录为 `0700`。
- 不要把 IPC 暴露到公网。代码只允许本地 socket、Named Pipe 或字面量回环 TCP。
- Agent 对回放目录只需要读取已关闭文件以及成功上传后的删除权限。
- 备份时至少包含 SQLite 数据库、`agent-inbox`、上传状态和 server outbox；不要只备份数据库。

## 8. 升级与旧配置迁移

- Agent 新默认配置是 `config/agent.yaml`。
- 根目录 `agent_config.yml` 仍会在新默认文件不存在时被兼容读取，并输出弃用警告。
- `-webhook-config` 仍是 `-config` 的兼容别名，但已弃用。
- 将旧 `config/webhook.yaml` 的四个顶层字段合并进 `agent.yaml`。
- 将旧 `config/stats.yaml` 转成 `agent.yaml` 的 `STATS` 块，并保留原 SQLite 文件。
- 将旧 `config/replay.yaml` 的分享站凭据迁入 `agent.yaml` 的 `REPLAY_UPLOAD`，server 的 replay 文件仅保留录制目录和保留策略。

迁移后先单独启动 server，确认核心服务正常，再启动 Agent 观察积压事件被消费。

## 9. 运行状态与排查

管理接口 `GET /admin/metrics` 的 `agent` 字段提供：

- `enabled`、`online`；
- endpoint、consumer ID 和 Agent 版本；
- ACK/最新序号、待处理事件数和 outbox 大小；
- 被丢弃的普通优先级事件数。

常见问题：

| 现象 | 检查 |
| --- | --- |
| Agent 一直等待 | server 是否配置 `ENDPOINT: auto`；两边 `DISCOVERY_FILE` 是否相同；运行用户是否有读取权限。 |
| `503 stats-unavailable` | Agent 是否在线；`STATS.ENABLED` 是否为 `true`；SQLite 路径是否可写。 |
| 上传返回 `503` | Agent 是否在线；`REPLAY_UPLOAD.ENABLED`、URL、token 是否完整。 |
| 自动上传没有发生 | `AUTO_UPLOAD` 是否开启；两边回放目录是否相同；回放是否已完成落盘。 |
| Webhook 重复或不投递 | 每个目标是否有稳定唯一的 `ID`；检查 Agent ledger、inbox 和日志。 |
| outbox 持续增长 | Agent 是否能连接；处理器是否因无效配置或外部服务失败而重试；查看 `pendingEvents`。 |

验证部署时建议依次执行：

1. 只启动 server，确认游戏与管理接口可用。
2. 启动 Agent，确认 `/admin/metrics` 中 `agent.online=true`。
3. 重启 Agent，确认 server 不退出且积压事件继续消费。
4. 停止 Agent，确认统计和上传返回明确 `503`，核心对局不受影响。
5. 恢复 Agent，确认 outbox 的 `pendingEvents` 逐步归零。
