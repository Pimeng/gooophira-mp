# tphira-mp → gooophira-mp 迁移进度 (TypeScript → Go)

> 本文件是迁移工作的**持久化进度追踪**。每次中断后，从「当前进度」一节继续。
> 源码位于 `tphira-mp-src/`（只读参考），Go 实现写入仓库根目录。
> 原项目 ~18k LOC / 97 源文件 / 67 测试文件。
>
> **状态：全部迁移完毕** ✅ — 协议/网络/房间状态机/命令派发/配置（加载+热重载）
> /admin（HTTP+CLI，含 OTP 提权/比赛/迁移/封禁/指标/控制台）/日志（轮转+压缩+IP 黑名单）/回放
> （录制+下载+上传+自动上传）/观战聚合/GUI 网页控制台（内嵌单文件页 + 进程 CPU/内存采样 +
> WS 实时日志频道 + 浏览器窗口启动器）/**缓存（谱面…token/记录，本地内存+LRU+落盘，可切 Redis
> 多实例共享）** 全部就位并测试覆盖。**303 测试**全绿（16 包），真机已验。原 tphira-mp 源码功能链路 100% 迁移。

## 总体策略（用户指定优先级）

1. **大致框架** — 先把模块、目录、类型定义、函数签名/桩搭好，`go build ./...` 能过。
2. **测试文件** — 移植测试，锁定行为契约。
3. **完全体** — 填满实现逻辑，`go test ./...` 全绿。

分阶段进行，每阶段可独立提交、随时中断、从断点续作。

## Go 设计约定（关键决策）

- **模块路径**: `github.com/Pimeng/gooophira-mp`，`go 1.26`。
- **目录**: 应用代码放 `internal/`，入口放 `cmd/server/`。
- **Tagged union**（`ClientCommand`/`ServerCommand`/`Message`/`RoomState`）→ Go 接口 + 标记方法（`isClientCommand()`），具体类型实现之。比 type-tag struct 更类型安全。
- **`Option<T>`** → Go 指针 `*T`（nil = None）。
- **`Result<Ok,Err>` / `StringResult<T>`** → 泛型结构体 `StringResult[T]{Ok bool; Value T; Error string}`（线上格式需要，故不用 `(T, error)`）。
- **`bigint` (U64)** → 原生 `uint64`/`int64`。
- **`Buffer`** → `[]byte`；`BinaryReader`/`BinaryWriter` → 自定义 struct。
- **`Map<number,UserInfo>`** → `map[int32]UserInfo`，**编码时需按 key 排序**（原 TS 显式排序，保证确定性）。
- **`Float16Array`** → 手写 IEEE-754 binary16 转换（round-to-nearest-even）。
- **⚠️ 零值陷阱**: 多数 config 字段是可选 + 非零默认值（如 `replay_ttl_days=4`、`room_max_users` 等）。Go 中 `bool→false`、`int→0`、`string→""`、`slice/map/ptr→nil`。
  - 需要「未设置 vs 显式零」区分的 → 用指针字段（`*int`/`*bool`）解析，再 `applyDefaults()` 落地。
  - 不需要区分的 → 解析后统一 `applyDefaults()` 填默认值。
- **外部依赖**（后续阶段引入）: YAML→`gopkg.in/yaml.v3`；Redis→`github.com/redis/go-redis/v9`；WebSocket→`github.com/coder/websocket`（待定）；Fluent/FTL→自实现轻量解析（无成熟 Go 库）。

## 目录映射 (TS → Go)

| TS 源 | Go 目标包 | 阶段 |
|-------|-----------|------|
| `src/common/*` | `internal/protocol/` | 1 |
| `src/server/core/*` | `internal/core/` | 2 |
| `src/server/game/*` | `internal/game/` | 3 |
| `src/server/network/*` | `internal/network/` | 4 |
| `src/server/replay/*` | `internal/replay/` | 5 |
| `src/server/cli/*` | `internal/cli/` | 5 |
| `src/server/utils/*` | `internal/util/`, `internal/l10n/`, `internal/logging/`, `internal/cache/`（含 Redis） | 5 / 8 |
| `src/server/gui/*` | `internal/httpapi/guipage.html`（embed）、`internal/guiwindow/`、`internal/procstats/` | 7 ✅ |
| `src/client/*` | `internal/client/`（测试用） | 4 |
| `src/server/main.ts`,`index.ts` | `cmd/server/main.go` | 6 |
| `locales/*.ftl` | `internal/l10n/locales/`（embed） | 5 |

## 阶段进度

### Stage 0 — Scaffolding  ✅ 完成
- [x] `go.mod`
- [x] `MIGRATION.md`（本文件）
- [x] 目录骨架（`internal/`、`cmd/`）

### Stage 1 — protocol 包（基础层，无外部依赖）  ✅ 完成
源: `binary.ts` `commands.ts` `framing.ts` `half.ts` `roomId.ts` `uuid.ts`
- [x] `half.go`（f16 ↔ f32，手写 IEEE-754 binary16）
- [x] `uuid.go`（v4 生成 + u64pair 互转，自实现免依赖）
- [x] `roomid.go`
- [x] `binary.go`（Reader/Writer，panic/recover 包边界错误）
- [x] `framing.go`
- [x] `commands.go`（全部 codec + tagged union 接口）
- [x] 测试: half（含全 65536 位模式穷举回归）/binary/framing/commands → **39 tests, 93.4% cover, vet+fmt 干净**

> 备注：common/ 中 `http.ts` `httpClient.ts` `httpProxy.ts` `httpServerUtils.ts`
> `stream.ts` `validation.ts` 属服务端辅助（非纯线协议），随其消费方放到 Stage 4/5。
> `utils.ts`（NOOP/EMPTY_RECORD）以 `protocol.Unit{}` + Go 习惯替代，无需单独文件。
> `index.ts` barrel 导出 Go 不需要。

### Stage 2 — core (config/types/state)  🟡 框架完成
源: `types.ts` `configValues.ts` `runtimeConfig.ts` `state.ts` `configPersist.ts` `configWatcher.ts` `server.ts`
- [x] `internal/config/`（types/config/fields + 测试）：ServerConfig 指针字段、Effective* 默认值、
      LoadEnv/BuildFromMap/Merge/ChangedKeys/KeepStartupOnly。**所有默认值集中一处**。
- [x] `internal/l10n/`：Language 占位（完整 Fluent 留 Stage 5）。
- [x] `internal/server/` 框架：ServerState 容器 + 构造 + ApplyConfig；User（完整）；
      Room（字段完整 + 简单方法，状态机留 Stage 3）；接口 Session/Logger/WsBroadcaster/
      WebSocketService/ReplayRecorder（**打破 network↔server 循环**）；ConsoleHub 占位。
- [x] **runtimeConfig + configPersist + configWatcher 全链路**：`config/runtime.go`（可热更新项
      描述表 + 快照 + 补丁解析分类，复用 configFields 元数据）、`config/persist.go`（逐行原地更新
      YAML 保留注释 + 原子写）、`config/watcher.go`（轮询 mtime/size 侦测变更，零外部依赖）。
      `runtime_test.go`/`persist_test.go`/`watcher_test.go` 共 16 测。
- [x] server.ts（startServer 生命周期）→ 见 Stage 6 main 装配（已可运行）。
- [x] ServerState 的 LoadAdminData/SaveAdminData/StartCleanup 实体（见下 Stage 5）。

> **架构决策**：state↔room↔user↔session 互相引用紧密，按 Go 惯例放进单一 `server` 包
> （多文件组织）。传输层用 `server` 定义的 consumer 接口反向接入，打破包循环。用户 ID
> 在域内统一用 `int`（对应 TS number），在 protocol 边界转 int32。

### Stage 3 — game (room/user 状态机 + 逻辑)  🟡 核心完成
源: `game/room.ts`（大）`game/user.ts` `game/roomUtils.ts` `game/adminViews.ts`
- [x] `user.go`：User 完整（toInfo/canMonitor/setSession/trySend/markDangle/dangle）。
- [x] `room.go`：Room 类型 + 成员（有序 slice，cycle 依赖加入序）+ 简单方法 + RefreshLive(roomUtils)。
- [x] `roomlogic.go`：RoomLifecycle 注入 + 全状态机：
      AddUser、ClientState、Send/formatMessageForLog、OnUserLeave（房主转移）、
      CheckAllReady（WaitForReady→Playing；Playing→结算→SelectChart）、游戏摘要、
      dangling 重连播报、cycle 轮换、contest 自动解散、ValidateJoin/Start/SelectChart、HandleJoin。
- [x] `roomlogic_test.go`：13 单测，server 包覆盖 71.9%。
- [ ] `adminViews.ts`（183 行，管理面板房间/用户视图）→ 随 admin HTTP 路由放 **Stage 4**。
- [ ] 命令派发（CmdReady→started、CmdPlayed→results、CmdAbort→aborted、RequestStart→WaitForReady…）
      属 session/dispatch 层 → **Stage 4**。

> ⚠️ 已知细节差异：游戏摘要平局时取 id 升序首名（TS 取 Played 提交序）；纯显示差异，已注释。
> 房间日志文本目前是 l10n key（TL 占位），Stage 5 接入 FTL 后变为真实译文。

### Stage 4 — network (session/http/ws) + 命令派发  🟡 核心完成
源: `network/session.ts`(1051) `network/session/commandRouter.ts` `network/httpService.ts` `network/websocketService.ts`
- [x] `server/phira.go`：PhiraAPI 接口 + PhiraUserInfo。`server/interfaces.go` 扩展 ReplayRecorder + MonitorBuffer。
- [x] `server/hub.go`：编排层（广播、MakeRoomLifecycle、CreateRoom/JoinRoom/LeaveRoom/Disband、Fetch*）。
- [x] `server/dispatch.go`：ProcessClientCommand 全量 switch（Chat/Touches/Judges/房间生命周期/
      选谱就绪/Played/Abort）。`dispatch_test.go` 全流程 + 错误用例。
- [x] `internal/phira/client.go`：Phira HTTP 客户端（/me、/chart/:id、/record/:id）实现 PhiraAPI。
- [x] `internal/network/`：Session（实现 server.Session）+ TCP 监听 + 握手 + 心跳(read deadline) +
      命令循环 + 发送通道/writer goroutine。`network_test.go` 真实 TCP 端到端 + 12 并发压测。
- [x] **并发模型**：全局 ServerState.Mu 串行化命令处理（等价 TS 事件循环）；发送非阻塞入队，
      socket 写在 writer goroutine 锁外完成；溢出**异步** Close（避免持锁自死锁，已修）。
- [x] **顶号踢旧连接 + 重连恢复房间**（handleAuthenticate：复用同 id 用户、先重绑新会话再 Close
      旧会话使 cleanup 短路保留房间；维护模式拒绝新连接但放行已在线重连）。`network_test.go` 已测。
- [x] **HTTP 服务（公开路由）**：`internal/httpapi`（net/http）。服务骨架 = CORS + 每 IP 固定窗口
      限流 + 优雅关闭；公开路由 `/room`（房间列表，2s 缓存，过滤 `_` 私有房）、`/room-creation/config`、
      `/replay/config`。`HTTP_SERVICE` 开启时由 main 启动（默认端口 12347）。`server_test.go` 6 测 +
      真机 curl 验证。错误响应按 Accept-Language 本地化。
- [x] **HTTP 管理路由（核心）**：`internal/httpapi/admin.go` 鉴权（Bearer vs admin_token + 临时
      token IP 绑定 + 每 IP 失败 5 次封禁）+ `server/adminviews.go`（房间/用户详细视图，含状态机细节/
      game_time(-Inf→null)/语言/contest/日志）。路由：GET /admin/rooms、GET /admin/users、
      POST /admin/broadcast、POST /admin/disband。`admin_test.go` 6 测 + 真机 curl 验证。
- [x] **HTTP admin 配置路由**：`httpapi/adminconfig.go`（GET/POST /admin/runtime-config +
      /rollback + /admin/replay/config + /admin/room-creation/config）。改动经 ApplyRuntimePatch
      落盘 + 热生效 + 记录回滚快照。`adminconfig_test.go` 6 测。
- [x] **HTTP admin 用户管理路由**：`httpapi/adminusers.go`（GET /admin/users/:id 单用户、
      POST /admin/ban/user 封禁可选断开、POST /admin/ban/room 房间封禁、POST /admin/users/:id/disconnect
      踢下线）。复用 BannedUsers/BannedRoomUsers + SaveAdminData + Session.Close。`adminusers_test.go` 5 测。
- [x] **HTTP admin /admin/metrics**：`httpapi/adminmetrics.go`（server/process/memory/business 指标，
      CPU 历史采样以 Go runtime 指标 goroutines/GC 替代）。`adminmetrics_test.go` 2 测。
- [x] **比赛模式**：`server/contest.go`（Hub EnableContest/DisableContest/SetContestWhitelist/StartContest，
      白名单始终并入当前参与者；startPlaying 抽出供「自动开赛」与「强制开赛」共用）+ `httpapi/admincontest.go`
      （POST /admin/contest/rooms/:id/config|whitelist|start）+ CLI `contest <room> enable|disable|whitelist|start`。
      白名单入房校验/手动开赛/结束自动解散原已在 roomlogic。`contest_test.go` 6+5+4 测。
- [x] **HTTP admin users/:id/move**：`server/move.go`（Hub MoveUser：离线用户在两个 SelectChart 空闲
      房间间迁移，入目标→退源→源空解散）+ `httpapi/adminusers.go` POST /admin/users/:id/move。
      `move_test.go` 3 测 + 路由 1 测。
- [x] **GUI 控制台路由**：`server/consolehub.go`（ConsoleHub 环形缓冲，日志器 SetOnLog 旁路喂入）
      + `httpapi/adminconsole.go`（GET /admin/console/logs 回填、POST /admin/console/command 执行）
      + `cli.NewExecutor`（独立 Console + 缓冲捕获命令输出，串行）。装配进 main。真机 curl 验证
      命令执行（list→「当前没有房间」）与日志回填。`consolehub_test.go` 2 测 + 路由 3 测 + 执行器 2 测。
- [x] **WebSocket 实时推送**：`internal/httpapi/ws.go`（`github.com/coder/websocket`）。`/ws` 升级 +
      订阅模型（subscribe/unsubscribe/ping/admin_subscribe/admin_unsubscribe）+ room_update / admin_update
      推送（admin 100ms 防抖）。每客户端发送通道 + writer goroutine（慢客户端 CloseNow 断开）。
      注入 `state.WSService`，dispatch 经 Room.NotifyWebSocket 推送。`ws_test.go` 真连接 4 测。
      `server/adminviews.go` 加 BuildRoomUpdate。
- [x] **WebSocket console 日志流**：`ws.go` 加 console_subscribe/console_unsubscribe（管理员鉴权，
      回环 GUI token 亦可）→ 回 console_subscribed{lines 快照} → ConsoleHub.Subscribe 实时推
      console_log{level,message,timestamp}；断线自动退订。`ws_test.go` TestWS_ConsoleSubscribe 真连接验证。
- [x] **HTTP replay 公开路由**：`httpapi/replay.go`（POST /replay/auth 认证+列回放+签发会话、
      GET /replay/download 凭会话下载、POST /replay/delete 删除、GET/POST /replay/auto-upload/config
      显示开关）。配套 `replay/reader.go`（ReadReplayHeader/ListReplaysForUser/DeleteReplayForUser）。
      `replay_test.go` 6 测 + `reader_test.go` 4 测。POST /replay/upload（分享站）随 autoUpload 接入。
- [x] **OTP / CLI 提权**：`httpapi/otp.go`（POST /admin/otp/request + /admin/otp/verify，otp 模式终端
      打印验证码、cli 模式控制台审批；失败累计封禁 IP/会话；成功签发 IP 绑定的 4h 临时 token；配了
      静态 admin_token 时整体禁用）+ `cli/cli.go` approve/deny/pending（前缀短码定位、歧义检测、签发
      token 入 TempAdminTokens）。验证码仅打印终端不写日志文件。`otp_test.go` 4 测 + `approval_test.go` 7 测。
- [x] **欢迎消息**：`server/welcome.go`（认证后系统聊天：清屏+欢迎+版本+可加入房间列表+提示+一言）
      + `network` 一言 HTTP 拉取。房间列表过滤（私有/锁定/满员/非选谱或游玩态）+ 用户语言本地化。
      `welcome_test.go` 4 测 + **真机演示**（真 Phira 认证 → 真一言「海内存知己，天涯若比邻」）。
- [x] **dangle 定时宽限窗**：`session.go` 断线后非对局保留 10s（`dangleWindowNonPlaying`）/ 对局
      `playing_reconnect_grace` 秒（0=立即），窗口内同账号重连拿回房间；超时未回则退房移除。
      封禁用户立即移除。`network_test.go` 加重连保留房间测试。
- [x] **命令限流**：`network/ratelimit.go` 每会话令牌桶（chat 10/3、api 12/3、room 20/6），
      Touches/Judges/Ping 不限；超限回「操作过于频繁」错误。`COMMAND_RATE_LIMIT=false` 可关。
      `ratelimit_test.go` 4 测（分类/桶补充/不限类/响应）。
- [x] **HAProxy PROXY protocol**：`network/proxyprotocol.go` 解析 v1 文本 + v2 二进制头，
      启用 `HAPROXY_PROTOCOL` 时握手前解出真实客户端 IP（`session.go` 用 `bufio.Reader` 的
      Peek/Discard 取代 socket.unshift，非 PROXY 数据零消费、宽松回退 TCP 对端）。IPv6 用
      `net.IP` 规范形式（优于原版逐组拼接）。`proxyprotocol_test.go` 13 测（v1/v2/分片/边界）。
- [x] **连接接纳保护**：`server.go` accept 路径加全服并发硬上限（`MAX_CONNECTIONS`）+ 每 IP
      连接速率限制（`network/connlimit.go` 滑动窗口+封禁，对应 TS ConnectionRateLimiter，默认
      30/10s、封禁 30s）。限速以 **TCP 对端**为键（PROXY 头可伪造，不可作限流键）。定时清理
      过期项。`connlimit_test.go` 6 测。新连接 debug 日志（`log-new-connection`，含真实来源）。
- [x] **MonitorBuffer 观战聚合**：`server/monitorbuffer.go`（`AggregatingMonitorBuffer`）按 ~50ms
      动态窗口合并同一玩家的 Touches/Judges 后批量转发观战者，降低高频帧的网络冲击。装配进 main
      （`hub.Monitor`），关闭时 Stop 刷写残留。锁序分析见文件注释（b.mu 与 state.Mu 不同持）。
      `monitorbuffer_test.go` 5 测。

> ⚠️ race 检测器需 CGO/gcc（本机无）→ 不用；改以 `-count=N` 重复跑 + 并发压测验证无死锁。
### Stage 5 — replay/cli/utils/i18n（含 yaml/redis/fluent 依赖）  🟡 进行中
源: `replay/{replayFormat,replayStorage,replayRecorder,autoUpload,replayCleanup}.ts`
- [x] `internal/replay/format.go`：PHIRAREC 头(magic+version+compression) + 判定事件 I32 编码。
      **压缩用 DEFLATE（compress/flate）替代 ZSTD**——压缩字节标明算法，TS 读取侧兼容、免依赖。
- [x] `internal/replay/storage.go`：路径 `<base>/<userID>/<chartID>/<ts>.phirarec`、默认 `<cwd>/record`。
- [x] `internal/replay/recorder.go`：Recorder 实现 server.ReplayRecorder + StartRoom/EndRoom/
      ListRoomFiles/ClearRoomFiles/CloseAll/FakeMonitorInfo。StartRoom 纯内存，EndRoom 落盘
      （main 中以 goroutine 调用，避免阻塞持锁的命令处理）。`recorder_test.go` 4 测 + 网络层端到端测。
- [x] 装配进 `cmd/server/main.go`（state.ReplayRecorder + OnEnterPlaying/OnGameEnd + 关闭刷写）。
- [x] **l10n 接 Fluent/FTL**：`internal/l10n/fluent.go` 自实现 FTL 子集解析器（变量插值 /
      布尔选择表达式 / 多行值 / 字符串字面量），`go:embed` zh-CN + en-US（其余语言后补）。
      `NewLanguage` 做 POSIX→BCP47 规范化 + 语言协商（默认 zh-CN）。`l10n_test.go` 8 测覆盖
      插值/选择/多行/协商/双语键集一致。`TL` 缺键回退默认语言再回退 key 本身（不崩）。
- [x] **logging 对齐原版**：`[ts] [LEVEL] msg` 行格式 + 按级别 ANSI 配色（终端且未设 NO_COLOR）
      + WARN/ERROR 走 stderr。房间日志注入 `room` 参数。main 启动日志按服务端语言本地化。
      实测 zh：「服务端运行在 [::]:port」/ en：「Listening on ...」。
- [x] **全部 6 种语言**：en-US/zh-CN/zh-TW/ja-JP/ko-KR/ru-RU 全部 embed + 协商（zh-TW/HK→繁体、
      ja→ja-JP 等）。`l10n_test.go` 验证 6 语言键集与 en-US 一致。真机演示日语日志/CLI 正常。
- [x] **logging 文件输出 + 按日轮转**：写 `logs/<date>.log`（无色、追加、按日切换），main 装配
      `logs` 目录 + 关闭刷写。`logger_test.go` 3 测 + 真机验证生成日志文件。
- [x] **日志维护**：`logging/maintenance.go`（历史日志超 `LOG_COMPRESS_AFTER_DAYS` 天 gzip 化、
      目录总占用超 `LOG_MAX_TOTAL_MB` 从最旧删起，活动日志永不动；每日午夜 + 启动各跑一次，
      getter 读配置支持热重载）。装配进 main。`maintenance_test.go` 6 测。
- [x] **locales/<lang>.ftl 运行时覆盖**：`l10n/override.go` `LoadOverrides(dir)`（仅覆盖文件中出现
      的键，其余沿用内置）。装配进 main（启动期加载 `locales/`）。`override_test.go` 2 测。
- [x] **回放 TTL 清理**：`replay/cleanup.go`（删除早于 ttl_days 的 .phirarec + 清空目录）。
      main 启动清一次 + 每日定时。`cleanup_test.go` 2 测。
- [x] **replayStorage 读取列表 API**：`replay/reader.go`（ReadReplayHeader/ListReplaysForUser/
      DeleteReplayForUser）供 HTTP /replay/* 使用。`reader_test.go` 4 测。
- [x] **autoUpload 分享站子系统**：`sharestation/client.go`（Upload multipart /upload_direct +
      SetVisibility show/hide，支持出站代理；httptest mock 4 测）+ `httpapi/replay.go` POST /replay/upload
      手动上传（校验归属→上传→记元数据→设可见→删本地）+ `autoupload/autoupload.go` 对局结束延迟
      30s 自动上传（按用户 show 配置设可见，元数据每谱面截断 50 条）。装配进 main（OnGameEnd 钩子 +
      `state.AutoUploadCallback`）。`autoupload_test.go` 4 测 + 上传路由 2 测。
- [x] **CLI 控制台**：`internal/cli`（读 stdin 分发）。命令：help/list/users/broadcast/roomsay/
      disband/maxusers/kick/ban/unban/banlist/replay/roomcreation/maintenance/stop。输出经 l10n
      本地化。装配进 main（stop 命令触发优雅关闭，与信号并列）。`cli_test.go` 10 测 + 真机管道验证。
- [x] **管理员数据持久化**：`server/admindata.go`（封禁/房间封禁落盘 admin_data.json，原子写）。
      main 启动 Load + 关闭 Flush；CLI ban/unban/banroom/unbanroom 改动后即 Save。`admindata_test.go` 2 测。
- [x] CLI banroom/unbanroom 命令。
- [x] **CLI approve/deny/pending**（见上 OTP 提权）。
- [x] **连接日志 IP 黑名单**：`logging/connratelimit.go`（单 IP 连接日志超阈值即拉黑抑制，防日志洪水）
      + `logging.Logger` `ConnectionLog/GetBlacklistedIPs/RemoveFromBlacklist/ClearBlacklist`；session 新连接
      日志改走 ConnectionLog（按真实 IP 抑制）；CLI `ipblacklist list/remove/clear`。`connratelimit_test.go`
      4 测 + `ipblacklist_test.go` 2 测。
- [x] CLI `contest`（见上比赛模式）。
- [x] **CLI `user <id>`（用户详情）+ `reject`（deny 别名）**：`cli.go` cmdUserInfo（id/名称/在线/角色/
      房间/封禁/游戏时间/语言）；并恢复 TS 的 4 类输出（print/printError/printSuccess/printInfo）→
      `ConsoleOutputLine{Kind,Text}`（终端配色 + GUI 着色）。`executor_test.go` 增 kind 断言。
- [x] **CLI `kick <id> [preserve]` 的 preserve 语义**：`network.Session.AdminDisconnect(preserveRoom)`
      （false=普通断线走 dangle 后移除；true=断开但保留房间占位、可重连，不 dangle）。cmdKick 经可选接口
      `adminDisconnecter` 调用；preserve 且玩家在对局中时先判退本局并 `CheckRoomAllReady`（对齐 TS
      abortPlayingUserAndCheckReady）。`cli_test.go` 3 测（默认/preserve/对局中判退）。
- [x] **配置热重载全链路**：runtimeConfig 描述表 + configPersist（保留注释逐行写）+ configWatcher
      （轮询侦测）+ `ServerState.ReloadConfig/ApplyRuntimePatch`（diff/startup-only/回放开关副作用）
      + 各组件经 `OnConfigReload` 监听器热更新（连接限速阈值 / HTTP 限速 / 日志级别）。CLI
      replay/roomcreation 开关改走 ApplyRuntimePatch（落盘 + 副作用）。**真机验证**：改文件后
      日志输出 `config reloaded: REPLAY_ENABLED, LOG_LEVEL`。
- [x] **ServerState 周期内存清理**：`state.go` `StartCleanup/StopCleanup/RunCleanupOnce`（每小时
      清理 7 天前上传元数据 / 离线用户自动上传配置 / 过期或拒绝的 CLI 审批会话 / 过期或封禁的临时
      token）。装配进 main（启动启、关闭停）。`cleanup_test.go` 2 测。

### Stage 6 — main 集成 + server.ts 生命周期  🟡 可正常运行
- [x] `cmd/server/main.go`：config→logging→state→phira→hub→录制→TCP 监听 + 信号优雅关闭。
- [x] **YAML 配置加载**：`internal/config/load.go`（yaml.v3）`LoadFile`/`LoadMerged`，优先级 env > 文件 > 默认。
      `-config` 旗标（默认 `server_config.yml`）。`load_test.go` 测嵌套块/列表/env 覆盖/缺文件回退。
- [x] **IPv6 监听修复**：`net.JoinHostPort` 正确处理 `HOST: "::"`（曾因 `%s:%d` 产生 `:::port` 报错）。
- [x] **配置文件热重载**：`config.NewFileWatcher` 轮询装配进 main，变更时 LoadMerged + ReloadConfig，
      打印变更键 / 需重启键。真机验证通过。
- [x] 冒烟验证：`server -config server_config.example.yml` 加载配置、监听 `[::]:port`、接受连接。
- [x] 仓库根放 `server_config.example.yml` 模板。
- [ ] HTTP/WS 服务启动 / configWatcher 热重载 / CLI 控制台 / 完整 startServer 编排。

## 当前进度（断点续作从这里看）

**已完成**:
- Stage 0 脚手架。
- Stage 1 protocol 包（39 测试，93.4% 覆盖；含半精度全 65536 位模式穷举回归）。
- Stage 2 框架：config 包（完整+测试）、l10n 占位、server 包骨架（ServerState/User 完整，Room 骨架）。
- Stage 3 核心：Room 状态机 + RoomLifecycle + 校验 + RefreshLive（13 单测，server 包 71.9%）。
- Stage 4 核心：命令派发（hub/dispatch）+ TCP 传输（network）+ Phira HTTP 客户端 + 并发模型。
  端到端：真实 TCP 上 连接→握手→认证→建房→选谱→开始→交成绩→结算 全跑通；12 并发压测。
- **可运行里程碑**：`internal/logging` 日志器 + `cmd/server/main.go` 装配（config→state→hub→TCP）。
  `go run ./cmd/server`（或 `PORT=xxx 二进制`）已能启动并监听，接受真实客户端连接。
- 顶号踢旧连接 + 重连恢复房间（含维护模式放行重连）。
- 测试保真度核对：half/framing/binary 与 TS 测试逐项对齐；补齐 half 随机样本穷举、
  abort→结算、monitor 必须 Ready、非房主越权拒绝、非游玩态丢帧、顶号等场景测试。
- **回放录制器可用**：`internal/replay`（PHIRAREC 格式 + DEFLATE 压缩 + Recorder），装配进
  main 与 Hub 钩子；网络层端到端测试验证真实一局落盘出可解码的 .phirarec 文件。
- **可正常运行**：YAML 配置加载（env>文件>默认）+ IPv6 监听修复。`server -config xxx.yml`
  加载配置、`[::]:port` 监听、接受连接。仓库根有 `server_config.example.yml` 模板。
- **真实 Phira API 端到端验证通过**：`TestNetwork_LivePhiraEndToEnd`（env-gated，默认跳过）用真
  token 走 认证(/me)→建房→选真实谱面(/chart/:id) 全链路。实测 id=1902111「皮梦测试号」、
  谱面 61959「g.r.i.s」正确拉取入库。证明 TCP 协议 + 真实上游集成无误。
- **l10n + 日志本地化**：自实现 FTL 子集解析器，内置中英文，日志格式/配色对齐原版。
  启动日志实测中英双语正确输出。
- **HTTP 服务（公开 + 核心管理路由）**：`internal/httpapi`，CORS + 限流 + `/room` 房间列表 +
  配置开关；`/admin/*` 鉴权 + 房间/用户详细视图 + broadcast/disband。`HTTP_SERVICE` 开启时随 main
  启动。真机 curl 验证（含鉴权 401/403/200）。
- **欢迎消息**：认证后推送本地化欢迎（含房间列表 + 真实一言）。真机演示通过。
- **CLI 控制台**：`internal/cli` 读 stdin 执行管理命令，装配进 main（stop 触发优雅关闭）。真机管道验证。
- **全部 6 种语言**（en/zh-CN/zh-TW/ja/ko/ru）+ **日志文件按日轮转**（`logs/<date>.log`）。
- **版本号**：`internal/version`（`go:embed VERSION` 文件，当前 0.0.1）。优先级 PHIRA_MP_VERSION 环境
  变量 > ldflags 注入 > VERSION 文件 > 构建信息 > "dev"。release 用
  `-ldflags "-X .../internal/version.injected=$(git describe --tags --always)"` 注入。
- **WebSocket 实时推送**（coder/websocket，room/admin 订阅+推送）、**命令限流**（每会话令牌桶）、
  **dangle 重连宽限窗**（断线保留房间、窗口内重连拿回）。
- **HAProxy PROXY protocol**（v1/v2，启用时握手前解出真实 IP）+ **连接接纳保护**（并发硬上限
  `MAX_CONNECTIONS` + 每 IP 连接速率限制 `CONNECTION_RATE_LIMIT`，限速键为 TCP 对端防伪造绕过）。
- **配置热重载全链路**：runtimeConfig（可热更新项描述/快照/补丁分类）+ configPersist（保留注释逐行写）
  + configWatcher（轮询侦测）+ `ReloadConfig/ApplyRuntimePatch`（diff / startup-only 仅提示 / 回放开关
  副作用 / 各组件经 OnConfigReload 热更新限速阈值与日志级别）。HTTP `/admin/runtime-config` 等路由 +
  CLI replay/roomcreation 改走持久化。**真机验证**：改文件即 `config reloaded: …`。

### Stage 7 — GUI 网页控制台  ✅ 完成
GUI 页面是固定客户端，迁移即「内嵌单文件页 + 对齐其依赖的数据契约」。
- [x] **进程 CPU/内存采样器**：`internal/procstats/`（2s 周期、300 点环形历史；cpuPercent 整机口径、
      rss/heapUsed/heapTotal）。纯标准库、无 CGO：Windows 经 syscall LazyDLL（GetProcessTimes /
      GetProcessMemoryInfo / GlobalMemoryStatusEx），Linux 读 /proc，其它平台 getrusage 兜底
      （build tag 分平台，已 GOOS=windows/linux/darwin/freebsd 交叉编译验证）。`procstats_test.go` 3 测。
- [x] **`/admin/metrics` 对齐 GUI 契约**：补 memory.{rss,heapUsed,heapTotal,systemTotal}、cpu.{cores,percent}、
      `?history=1` 历史采样、process.runtime（Go 版本，供副标题）。采样器装入 Service（New 启、Close 停）。
- [x] **控制台输出 kind**：`ConsoleOutputLine{Kind,Text}`（out/error/success/info），恢复 TS 4 类 printer
      语义（终端配色 + GUI 着色）；`ConsoleLogLine` JSON 改 `{level,message,timestamp}`；ConsoleHub 加
      Subscribe 实时订阅。
- [x] **WS console 频道**：见 Stage 6 WebSocket console 日志流。
- [x] **X-Admin-Token 头 + 回环 GUI token**：修正 extractAdminToken 优先识别 `X-Admin-Token`（GUI 用），
      checkAdmin/verifyAdminToken 接受回环地址下的 `state.GUILocalToken`（窗口模式免登录）。
- [x] **内嵌 GUI 页**：`httpapi/guipage.html`（从 guiPage.ts 逐字提取，仅把 Node 版本副标题改为通用 runtime）
      经 `//go:embed` 内嵌，`GET /gui`、`/gui/` 公开返回（no-store + nosniff）。`guipage_test.go` 验证。
- [x] **浏览器窗口启动器**：`internal/guiwindow/`（Edge/Chrome --app 模式，Win 路径探测 / mac open / Linux
      bin 探测 + xdg-open 兜底）。`--gui` 启动参数或 `GUI: true` 配置触发；隐含开启 HTTP 服务、生成回环
      token、弹窗。`guiwindow_test.go` 4 测。
- [x] **真机验证**：`GET /gui`→37KB 页；`/admin/metrics?history=1`→rss/cpu.cores/history/runtime 实数；
      console command→`{kind,text}`（list→info、ban→error）；console logs→`{level,message,timestamp}`；
      `/gui` 免 token、`/admin/*` 401；console `stop` 经执行器触发关闭。

### Stage 8 — 缓存 + Redis 多实例共享  ✅ 完成
源: `utils/cache.ts`、`session/phiraApiClient.ts`。Go 版 phira 客户端原本无缓存（stage-5 TODO），本阶段补齐。
- [x] **通用缓存** `internal/cache/`：泛型 `Cache[K,V]`（`NewString`/`NewInt` 构造）——本地后端为
      内存（采样近似 LRU 淘汰）+ 防抖落盘（原子 temp+rename），带 TTL；`GetOrSet` 带 in-flight 去重
      （并发同 key 合并一次工厂调用）。`cache_test.go` 9 测（TTL/LRU/去重/落盘往返/过期跳过/清空）。
- [x] **Redis 后端** `internal/cache/redis.go`：`InitRedis/CloseRedis/RedisEnabled`（go-redis v9）。
      启用时所有读写走进程级共享 Redis（键 `cache:<name>:<key>`，per-entry TTL）；连接成功后把各缓存
      内存数据 pipeline 迁移进 Redis；Redis 出错按 miss 降级（不阻塞业务）；静默 go-redis 内部日志。
      `redis_test.go` 用 miniredis（进程内、纯 Go）实测 set/get/delete/clear(SCAN)/TTL 过期/命名空间隔离/
      迁移；另有 `REDIS_TEST_ADDR` 真机门控测试。
- [x] **接入 phira 客户端**：`tokenCache`（token→UserInfo，30s，不落盘）+ `recordCache`（id→Record，
      1h，落盘）。`FetchUserInfo`/`FetchRecord` 走 `GetOrSet`（失败结果不缓存）。`client_test.go` 3 测
      （token 命中省 HTTP、record 命中省 HTTP、失败不缓存）。
- [x] **装配 main**：启动调 `cache.InitRedis(cfg.Redis)`（REDIS startup-only），关闭 `CloseRedis`。
- [x] **真机验证**：`REDIS.ENABLED=true` 但 Redis 不可达 → 日志 `Redis 连接失败，回退本地缓存` 后正常
      启动（go-redis 内部日志已静默），优雅降级为本地缓存。

**模块健康**：`go build ./...`、`go vet ./...`、`go test ./... -count=2` 全部通过（**303 测试，16 包**，gofmt 干净）。
**运行**：`go run ./cmd/server -config server_config.example.yml`（认证需可达 Phira API）；加 `--gui` 或配置 `GUI: true` 弹出控制台窗口；配置 `REDIS.ENABLED: true` 启用多实例共享缓存。
**依赖**：`yaml.v3`（配置）+ `coder/websocket`（WS）+ `redis/go-redis/v9`（缓存后端）+ 标准库（net/http、compress、mime/multipart、syscall）。测试另用 `alicebob/miniredis/v2`（进程内 Redis，不入生产二进制）。

**迁移完成**：原 tphira-mp 源码的服务端 + GUI + 缓存功能链路已 100% 迁移，无剩余待办。

## 验证基线

每阶段结束跑：
```
go build ./...
go vet ./...
go test ./...
```
TS 端对照真值可用 `cd tphira-mp-src && npx vitest run <file>` 抽取测试向量。
