// Command server 启动 Phira 多人服务器（Go 实现）。
//
// 装配：加载配置（YAML 文件 + 环境变量覆盖）→ 日志 → 全局状态 → Phira 客户端 →
// 编排 Hub → 回放录制 → TCP 监听 → 信号优雅关闭。HTTP/WebSocket 服务后续接入。
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentbridge"
	"github.com/Pimeng/gooophira-mp/internal/agentipc"
	"github.com/Pimeng/gooophira-mp/internal/agentoutbox"
	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/cache"
	"github.com/Pimeng/gooophira-mp/internal/cli"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/diagnostics"
	"github.com/Pimeng/gooophira-mp/internal/guiwindow"
	"github.com/Pimeng/gooophira-mp/internal/httpapi"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/logging"
	"github.com/Pimeng/gooophira-mp/internal/netutil"
	"github.com/Pimeng/gooophira-mp/internal/network"
	"github.com/Pimeng/gooophira-mp/internal/phira"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
	"github.com/Pimeng/gooophira-mp/internal/server"
	"github.com/Pimeng/gooophira-mp/internal/version"
)

// 默认监听端口（标准 Phira MP 端口）。
const (
	defaultPort     = 12346
	defaultHTTPPort = 12347
)

func main() {
	maybeRunConfigCommand()
	configPath := flag.String("config", defaultConfigPath, "配置文件路径（YAML）")
	flag.StringVar(configPath, "c", defaultConfigPath, "配置文件路径（YAML）（-config 的别名）")
	configDir := flag.String("config-dir", defaultConfigDir, "多文件配置目录（默认优先使用）")
	// 校验类参数用字符串接收（默认空 = 未传），便于非法时按启动期语言本地化报错——
	// 对齐 TS main 的 requireParse。实际取值在 applyCLIOverrides 经 flag.Visit + f.Value 读取。
	portFlag := flag.String("port", "", "TCP 监听端口（覆盖配置中的 PORT）")
	flag.StringVar(portFlag, "p", "", "TCP 监听端口（-port 的别名）")
	flag.String("host", "", "监听地址（覆盖配置中的 HOST）")
	flag.String("http-service", "", "启用 HTTP 查询/管理服务 true/false（覆盖配置中的 HTTP_SERVICE）")
	flag.String("http-port", "", "HTTP 服务端口（覆盖配置中的 HTTP_PORT）")
	flag.String("room-max-users", "", "房间最大用户数（覆盖配置中的 ROOM_MAX_USERS）")
	flag.String("server-name", "", "服务器名称（覆盖配置中的 SERVER_NAME）")
	flag.String("monitors", "", "观战权限用户 ID 列表，逗号分隔（覆盖配置中的 MONITORS）")
	// GUI 走字符串标志：可显式传 "-gui=false" 关闭（flag.Bool 默认值不可被显式赋值）。
	// applyCLIOverrides 用 ParseBool 严格校验非法值并按 lang 本地化报错。
	flag.String("gui", "", "启动时打开服务端 GUI 控制台窗口 true/false（覆盖配置中的 GUI）")
	flag.String("protocol-hack-delay", "", "ProtocolHack 客户端补偿延迟（5ms 默认；单位 ms；0=关闭）")
	flag.Parse()

	loadedConfig, err := loadStartupConfig(*configPath, *configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	cfg := loadedConfig.cfg

	// 启动期语言在配置加载后即可确定（与 state.ServerLang 同源：PHIRA_MP_LANG > LANG >
	// 配置 LANG），供 CLI 参数校验报错与启动期日志本地化。
	lang := l10n.NewLanguage(cfg.EffectiveLang())

	// 命令行参数覆盖（优先级最高）：仅处理显式传入的标志，校验失败按 lang 本地化错误并退出。
	applyCLIOverrides(cfg, lang)

	// 把配置的 Android 公共 DNS 列表同步到 netutil；未配置则保留内置默认。
	netutil.SetDNSServers(cfg.EffectiveDNSServers())
	// 把配置的出站代理同步到 netutil；优先级：环境变量 > 配置文件 > 直连。
	netutil.SetProxy(cfg.EffectiveProxyURL(), cfg.OutboundProxy != nil && cfg.OutboundProxy.Direct)

	logger := logging.New(cfg.EffectiveLogLevel(), "logs")
	defer logger.Close()
	pprofSvc, err := diagnostics.Start()
	if err != nil {
		logger.Warn(l10n.TL(lang, "log-pprof-start-failed", map[string]string{"error": err.Error()}))
	}
	pprofURL := ""
	if pprofSvc != nil {
		pprofURL = pprofSvc.URL()
		logger.Info(l10n.TL(lang, "log-pprof-listen", map[string]string{"addr": pprofURL}))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := pprofSvc.Close(ctx); err != nil {
				logger.Warn(l10n.TL(lang, "log-pprof-shutdown-failed", map[string]string{"error": err.Error()}))
			}
		}()
	}

	// 运行时本地化覆盖：locales/<lang>.ftl 存在则覆盖对应内置文案（须在服务前加载）。
	if n := l10n.LoadOverrides("locales"); n > 0 {
		logger.Info(l10n.TL(lang, "log-locale-overrides-loaded", map[string]string{"count": strconv.Itoa(n)}))
	}
	if loadedConfig.created {
		logger.Info(l10n.TL(lang, "log-config-created", map[string]string{"path": loadedConfig.path}))
	} else if loadedConfig.legacy && loadedConfig.fromLegacy {
		logger.Info(l10n.TL(lang, "log-config-loaded", map[string]string{"path": loadedConfig.path}))
		logger.Warn("legacy single-file configuration is deprecated; run `phira-mp-server config migrate`")
	} else {
		logger.Info(l10n.TL(lang, "log-config-loaded", map[string]string{"path": loadedConfig.path}))
	}

	// Redis 缓存（启用时谱面/记录/token 缓存转为多实例共享）。仅启动期生效，只在启动时初始化。
	cache.SetLogger(logger)
	if err := cache.InitRedis(cfg.Redis); err != nil {
		logger.Warn(l10n.TL(lang, "log-redis-fallback", map[string]string{"error": err.Error()}))
	} else if cache.RedisEnabled() {
		logger.Mark(l10n.TL(lang, "log-redis-enabled", nil))
	}
	defer cache.CloseRedis()

	adminDataPath := strOr(cfg.AdminDataPath, "admin_data.json")
	state := server.NewServerState(cfg, logger, cfg.EffectiveServerName(), adminDataPath, loadedConfig.path)
	if !loadedConfig.legacy {
		state.ConfigPath = ""
		state.ConfigDir = loadedConfig.dir
	}
	// 配置热重载时同步日志级别。
	state.OnConfigReload(func(c *config.ServerConfig) { logger.SetLevel(c.EffectiveLogLevel()) })
	state.OnConfigReload(func(c *config.ServerConfig) {
		netutil.SetDNSServers(c.EffectiveDNSServers())
		netutil.SetProxy(c.EffectiveProxyURL(), c.OutboundProxy != nil && c.OutboundProxy.Direct)
	})
	// 日志旁路到 GUI 控制台缓冲（供 /admin/console/logs 回填与 WS 推送）。
	logger.SetOnLog(state.ConsoleHub.Push)

	// 可选 Agent IPC。启动失败仅表示扩展降级，绝不能阻止实时游戏服务启动。
	agentCfg := cfg.EffectiveAgentIPC()
	var outbox *agentoutbox.Store
	if agentCfg.Endpoint != "disabled" {
		outbox, err = agentoutbox.Open(agentoutbox.Config{
			Dir: agentCfg.OutboxDir, MaxBytes: int64(agentCfg.OutboxMaxMB) * 1024 * 1024,
		})
		if err != nil {
			logger.Warn(l10n.TL(lang, "log-agent-outbox-unavailable", map[string]string{"error": err.Error()}))
		}
	}
	var agentPublisher *agentbridge.Publisher
	if outbox != nil {
		agentPublisher = agentbridge.New(outbox, logger, 1024)
		defer func() {
			agentPublisher.Close()
			if err := outbox.Close(); err != nil {
				logger.Warn(l10n.TL(lang, "log-agent-outbox-shutdown-failed", map[string]string{"error": err.Error()}))
			}
		}()
	}
	agentSvc, agentErr := agentipc.Start(agentipc.Config{
		Endpoint: agentCfg.Endpoint, Token: agentCfg.Token,
		DiscoveryFile: agentCfg.DiscoveryFile, Instance: agentCfg.Instance,
		ServerVersion: version.Get(), Outbox: outbox,
	})
	if agentErr != nil {
		logger.Warn(l10n.TL(lang, "log-agent-ipc-unavailable", map[string]string{"error": agentErr.Error()}))
	} else if agentSvc != nil {
		logger.Info(l10n.TL(lang, "log-agent-ipc-listen", map[string]string{"addr": agentSvc.Endpoint()}))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := agentSvc.Close(ctx); err != nil {
				logger.Warn(l10n.TL(lang, "log-agent-ipc-shutdown-failed", map[string]string{"error": err.Error()}))
			}
		}()
	}
	// Webhook 投递由 Agent 负责。主进程只发送持久化领域事件，
	// 不引入平台 SDK，也不持有扩展凭据。
	eventSinks := server.EventSinks{}
	if cfg.EffectiveWebhook() != nil {
		logger.Warn("server webhook.yaml is deprecated and not delivered; move targets to config/agent.yaml")
	}
	if loadedConfig.extensionEnabled("stats.yaml") {
		logger.Warn("server stats.yaml is deprecated and not opened; move STATS settings to config/agent.yaml")
	}
	if agentCfg.WebhookOwner != "agent" {
		logger.Warn("AGENT_IPC.WEBHOOK_OWNER=server is no longer supported; use agent")
	}
	if agentPublisher != nil {
		eventSinks = append(eventSinks, agentPublisher)
	}
	state.Events = eventSinks
	if err := state.LoadAdminData(); err != nil {
		logger.Warn(l10n.TL(lang, "log-admin-data-load-failed", map[string]string{"error": err.Error()}))
	}
	state.StartCleanup() // 周期内存清理（过期元数据/离线配置/过期 token）
	defer state.StopCleanup()

	// 日志维护：每日压缩历史日志 + 控制目录总占用（getter 读配置，支持热重载）。
	logMaint := logging.NewMaintenance("logs",
		func() int {
			state.Mu.Lock()
			defer state.Mu.Unlock()
			return state.Config.EffectiveLogCompressAfterDays()
		},
		func() int64 {
			state.Mu.Lock()
			defer state.Mu.Unlock()
			return int64(state.Config.EffectiveLogMaxTotalMB()) * 1024 * 1024
		},
		logger)
	logMaint.Start()
	defer logMaint.Stop()

	phiraEndpoint := strOr(cfg.PhiraAPIEndpoint, "")
	// rootCtx 在服务器关闭时取消（rootCancel），用于中断进行中的上游 HTTP 调用。
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	phiraClient := phira.NewClient(phiraEndpoint)
	phiraClient.SetLogger(logger)
	hub := server.NewHub(state, phiraClient)
	hub.SetContext(rootCtx)

	// 观战数据聚合缓冲：高频 Touches/Judges 按 ~50ms 窗口合并后批量转发观战者。
	monitorBuf := server.NewMonitorBuffer()
	hub.Monitor = monitorBuf

	// 回放录制：录制器注入全局状态（供 dispatch 的 Append*/SetRecordID），并通过
	// OnEnterPlaying/OnGameEnd 钩子驱动每局的开始/落盘。
	// 配置 SYSTEM_USER_ID（>0）后，假观战者改用该 bot 真实身份（异步拉取 /user/:id 昵称），
	// 与系统聊天发送者（MsgChat.User=SYSTEM_USER_ID）统一呈现为 bot 昵称；未配置（=0）时
	// 假观战者用固定 ID + 本地化名「回放录制器（系统）」，系统聊天发送者按「系统」渲染。
	recorder := replay.NewRecorder(
		cfg.EffectiveReplayBaseDir(), logger,
		replay.WithSystemUser(int32(cfg.EffectiveSystemUserID()), phiraClient.FetchUserName),
	)
	state.ReplayRecorder = recorder

	hub.OnEnterPlaying = func(room *server.Room) {
		ev := server.Event{Type: server.EventGameStart, RoomID: room.ID.String(), UserCount: room.UserCount()}
		if room.Chart != nil {
			ev.ChartID, ev.ChartName = room.Chart.ID, room.Chart.Name
			// 飞书模板相关字段：难度取 level、谱师 charter、封面 illustration（投递时下载并
			// 经飞书上传图片接口换 image_key 后填 chart_pic 模板变量）。
			ev.ChartDifficulty = room.Chart.Level
			ev.ChartCharter = room.Chart.Charter
			ev.ImageURL = room.Chart.Illustration
		}
		// 玩家清单：用 room.UserIDs()（已排除观战者）与全局用户表取昵称，组装为
		// "玩家A(玩家ID)、玩家B(玩家ID)" 形式作为飞书模板变量 player_list。
		playerParts := make([]string, 0, room.UserCount())
		for _, uid := range room.UserIDs() {
			name := ""
			if u := state.Users[uid]; u != nil {
				name = u.Name
			}
			if name == "" {
				name = strconv.Itoa(uid)
			}
			playerParts = append(playerParts, fmt.Sprintf("%s(%d)", name, uid))
			ev.Players = append(ev.Players, server.EventPlayer{ID: uid, Name: name})
		}
		ev.PlayerList = strings.Join(playerParts, "、")
		state.EmitEvent(ev) // 无视回放开关，每局开始都发
		if !state.ReplayEnabled || !room.ReplayEligible || room.Chart == nil {
			return
		}
		users := make([]replay.Participant, 0, room.UserCount())
		for _, id := range room.UserIDs() {
			name := ""
			if u := state.Users[id]; u != nil {
				name = u.Name
			}
			users = append(users, replay.Participant{ID: id, Name: name})
		}
		recorder.StartRoom(room.ID, room.Chart.ID, room.Chart.Name, users)
	}
	hub.OnGameEnd = func(room *server.Room) {
		serverName := state.ServerName
		roomID := room.ID.String()
		ev := server.Event{Type: server.EventGameEnd, RoomID: room.ID.String()}
		if room.Chart != nil {
			ev.ChartID, ev.ChartName = room.Chart.ID, room.Chart.Name
		}
		// 构建成绩排行（按 score 降序），供飞书模板 player_score_rank 变量使用。
		if st, ok := room.State.(server.StatePlaying); ok {
			ev.PlayerScoreRank = server.BuildScoreRank(room, st)
		}
		state.EmitEvent(ev)
		match, hasMatch := agentbridge.CaptureMatchFinished(serverName, room)
		if !hasMatch && agentPublisher != nil {
			ended := agentproto.GameEndedV1{Server: serverName, RoomID: roomID}
			if room.Chart != nil {
				ended.Chart = agentproto.ChartV1{ID: room.Chart.ID, Name: room.Chart.Name, Difficulty: room.Chart.Level, Charter: room.Chart.Charter, Illustration: room.Chart.Illustration}
			}
			go func() {
				if err := agentPublisher.PublishCritical(agentproto.EventGameEndedV1, ended); err != nil {
					logger.Warn("Agent game-end event append failed: " + err.Error())
				}
			}()
		}
		go func() {
			recorder.EndRoom(room.ID) // 落盘放到 goroutine，避免阻塞命令处理（持有 state.Mu）
			replayIDs := make(map[int]string)
			// 回放文件可靠关闭后再发布完成事件；上传调度和分享站凭据由可选 Agent 管理。
			for _, f := range recorder.ListRoomFiles(room.ID) {
				replayID := replay.IDFromFile(f).String()
				replayIDs[f.UserID] = replayID
				if agentPublisher != nil {
					if err := agentPublisher.PublishCritical(agentproto.EventReplayCompletedV1, agentproto.ReplayCompletedV1{
						Server: serverName, RoomID: roomID, ReplayID: replayID, UserID: f.UserID, ChartID: f.ChartID,
					}); err != nil {
						logger.Warn("Agent replay event append failed: " + err.Error())
					}
				}
			}
			if hasMatch && agentPublisher != nil {
				agentbridge.AttachReplayIDs(&match, replayIDs)
				if err := agentPublisher.PublishCritical(agentproto.EventMatchFinishedV1, match); err != nil {
					logger.Warn("Agent match event append failed: " + err.Error())
				}
			}
		}()
	}

	// 回放过期清理：启动时清一次，并每日定时清理。
	go func() {
		startedAt := time.Now()
		recorder.CleanupExpired(time.Now(), cfg.EffectiveReplayTTLDays())
		if logger.DebugEnabled() {
			logger.Debug(fmt.Sprintf("[Replay] 过期回放清理完成，耗时：%s", time.Since(startedAt)))
		}
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			startedAt = time.Now()
			recorder.CleanupExpired(time.Now(), cfg.EffectiveReplayTTLDays())
			if logger.DebugEnabled() {
				logger.Debug(fmt.Sprintf("[Replay] 过期回放清理完成，耗时：%s", time.Since(startedAt)))
			}
		}
	}()

	host := strOr(cfg.Host, "::")
	port := defaultPort
	if cfg.Port != nil {
		port = *cfg.Port
	}
	// JoinHostPort 正确处理 IPv6（如 HOST: "::" → "[::]:port"）。
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	srv, err := network.Listen(addr, state, hub)
	if err != nil {
		logger.Error(l10n.TL(lang, "log-listen-failed", map[string]string{"addr": addr, "error": err.Error()}))
		os.Exit(1)
	}
	logger.Info(l10n.TL(lang, "log-server-name", map[string]string{"name": state.ServerName}))
	logger.Info(l10n.TL(lang, "log-server-version", map[string]string{"version": state.Version}))
	logger.Info(l10n.TL(lang, "log-server-listen", map[string]string{"addr": srv.Addr().String()}))

	// 可选启动 HTTP 查询/管理服务（HTTP_SERVICE 开启时）。GUI 窗口依赖 HTTP 服务：
	// 启用 GUI 时自动开启 HTTP 服务（对齐 TS：gui===true 时隐含 http_service）。
	httpForcedByGUI := cfg.EffectiveGUI() && !cfg.EffectiveHTTPService()
	var httpSvc *httpapi.Service
	if cfg.EffectiveHTTPService() || cfg.EffectiveGUI() {
		if httpForcedByGUI {
			logger.Mark(l10n.TL(lang, "log-gui-http-forced", nil))
		}
		httpPort := defaultHTTPPort
		if cfg.HTTPPort != nil {
			httpPort = *cfg.HTTPPort
		}
		httpSvc = httpapi.New(state, hub, agentSvc, pprofURL)
		httpSvc.SetAgentService(agentSvc)
		httpAddr, herr := httpSvc.Start(net.JoinHostPort(host, strconv.Itoa(httpPort)))
		if herr != nil {
			logger.Error(l10n.TL(lang, "log-http-start-failed", map[string]string{"error": herr.Error()}))
		} else {
			logger.Info(l10n.TL(lang, "log-http-listen", map[string]string{"addr": httpAddr.String()}))
			if cfg.EffectiveGUI() {
				launchGUIWindow(state, logger, lang, httpAddr) // GUI 窗口模式：弹出独立控制台窗口
			}
		}
	}

	// 配置文件热重载：轮询配置文件变更，重新加载并热生效（仅启动期配置只提示需重启）。
	watcher := loadedConfig.watcher(func() {
		next, lerr := loadedConfig.reload()
		if lerr != nil {
			logger.Warn(l10n.TL(lang, "log-config-reload-skipped", map[string]string{"error": lerr.Error()}))
			return
		}
		applyCLIOverrides(next, lang)
		changed, restart := state.ReloadConfig(next)
		if len(changed) > 0 {
			logger.Mark(l10n.TL(lang, "log-config-reloaded", map[string]string{"items": strings.Join(changed, ", ")}))
		}
		if len(restart) > 0 {
			logger.Warn(l10n.TL(lang, "log-config-reload-restart", map[string]string{"items": strings.Join(restart, ", ")}))
		}
	})
	watcher.Start()
	defer watcher.Stop()

	// phira 缓存被动失效刷新：每 1h 取快到期键随机 Delete 几个，
	// 下次 GetOrSet 时自然重拉。Redis 模式 KeysNearExpiry 返回 nil，自动 no-op。
	phiraClient.StartRefresh(state, 1*time.Hour)
	defer phiraClient.Stop()

	// 关闭由「终止信号」或 CLI 的 stop 命令任一触发。
	done := make(chan struct{})
	var once sync.Once
	shutdown := func() { once.Do(func() { close(done) }) }

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; shutdown() }()

	// GUI 控制台命令执行器（供 HTTP /admin/console/command 捕获输出执行 CLI 命令）。
	state.ConsoleExecutor = cli.NewExecutor(state, hub, shutdown)

	// 控制台：读 stdin 执行管理命令（list/users/broadcast/ban/maintenance/stop…）。
	go cli.New(state, hub, shutdown).Run()

	<-done
	logger.Info(l10n.TL(lang, "log-server-stopped", nil))
	if httpSvc != nil {
		_ = httpSvc.Close()
	}
	_ = srv.Close()
	monitorBuf.Stop()             // 刷写残留观战帧
	recorder.Stop()               // 停止后台昵称拉取 goroutine
	recorder.CloseAll()           // 刷写进行中的录制
	_ = state.FlushAdminDataNow() // 落盘封禁数据
}

// applyCLIOverrides 把显式传入的命令行参数覆盖到 cfg（优先级高于环境变量与配置文件）。
// 仅遍历 flag.Visit（被设置过的标志）；数值/布尔/列表类参数复用 config 的解析规则，
// 非法时按 lang 本地化错误并退出（对齐 TS main 的 requireParse + cli-invalid-* 文案）。
func applyCLIOverrides(cfg *config.ServerConfig, lang *l10n.Language) {
	fail := func(key string) {
		fmt.Fprintln(os.Stderr, l10n.TL(lang, key, nil))
		os.Exit(1)
	}
	flag.Visit(func(f *flag.Flag) {
		raw := f.Value.String()
		switch f.Name {
		case "host":
			// host/server-name 沿用 TS 语义：空白视同未传，不报错、不覆盖。
			if s, ok := config.ParseString(raw); ok {
				cfg.Host = &s
			}
		case "server-name":
			if s, ok := config.ParseString(raw); ok {
				cfg.ServerName = &s
			}
		case "port", "p":
			v, ok := config.ParsePort(raw)
			if !ok {
				fail("cli-invalid-port")
				return
			}
			cfg.Port = &v
		case "http-port":
			v, ok := config.ParsePort(raw)
			if !ok {
				fail("cli-invalid-http-port")
				return
			}
			cfg.HTTPPort = &v
		case "http-service":
			v, ok := config.ParseBool(raw)
			if !ok {
				fail("cli-invalid-http-service")
				return
			}
			cfg.HTTPService = &v
		case "room-max-users":
			v, ok := config.ParseRoomMaxUsers(raw)
			if !ok {
				fail("cli-invalid-room-max-users")
				return
			}
			cfg.RoomMaxUsers = &v
		case "monitors":
			v, ok := config.ParseIntegerList(raw)
			if !ok {
				fail("cli-invalid-monitors")
				return
			}
			cfg.Monitors = v
		case "gui":
			// 字符串标志：传 "-gui=true" 或 "-gui=false" 显式覆盖；未传则沿用配置值。
			// 之前用 flag.Bool 时无法在命令行显式关闭（"出现即覆盖"行为与文档冲突）。
			if v, ok := config.ParseBool(raw); ok {
				cfg.GUI = &v
			}
		case "protocol-hack-delay":
			v, ok := config.ParseInteger(raw)
			if !ok {
				fail("cli-invalid-protocol-hack-delay")
				return
			}
			server.SetProtocolHackDelay(time.Duration(v) * time.Millisecond)
		}
	})
}

func strOr(p *string, def string) string {
	if p != nil && *p != "" {
		return *p
	}
	return def
}

// launchGUIWindow 生成本机回环专用 token 并在独立浏览器窗口中打开 GUI 控制台。
// token 仅经 URL 片段（#）传入页面——片段不进入请求与日志，且仅回环地址被接受。
func launchGUIWindow(state *server.ServerState, logger *logging.Logger, lang *l10n.Language, httpAddr net.Addr) {
	token := protocol.NewUUID()
	state.Mu.Lock()
	state.GUILocalToken = &token
	state.Mu.Unlock()

	_, portStr, err := net.SplitHostPort(httpAddr.String())
	if err != nil {
		return
	}
	baseURL := "http://127.0.0.1:" + portStr + "/gui"
	windowURL := baseURL + "#token=" + token
	// 异步拉起，避免阻塞启动主流程。
	go func() {
		if guiwindow.Launch(windowURL) {
			logger.Mark(l10n.TL(lang, "log-gui-window-launched", map[string]string{"url": baseURL}))
		} else {
			// 打开失败时输出带 token 的完整地址，便于本机手动访问（日志仅本机可读）。
			logger.Warn(l10n.TL(lang, "log-gui-window-failed", map[string]string{"url": windowURL}))
		}
	}()
}
