// Command server 启动 Phira 多人服务器（Go 实现）。
//
// 装配：加载配置（YAML 文件 + 环境变量覆盖）→ 日志 → 全局状态 → Phira 客户端 →
// 编排 Hub → 回放录制 → TCP 监听 → 信号优雅关闭。HTTP/WebSocket 服务后续接入。
package main

import (
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

	"github.com/Pimeng/gooophira-mp/internal/autoupload"
	"github.com/Pimeng/gooophira-mp/internal/cache"
	"github.com/Pimeng/gooophira-mp/internal/cli"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/guiwindow"
	"github.com/Pimeng/gooophira-mp/internal/httpapi"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/logging"
	"github.com/Pimeng/gooophira-mp/internal/network"
	"github.com/Pimeng/gooophira-mp/internal/phira"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
	"github.com/Pimeng/gooophira-mp/internal/server"
	"github.com/Pimeng/gooophira-mp/internal/stats"
	"github.com/Pimeng/gooophira-mp/internal/webhook"
)

// 默认监听端口（标准 Phira MP 端口）。
const (
	defaultPort     = 12346
	defaultHTTPPort = 12347
	// defaultConfigPath 是配置文件默认路径，被 -config/-c 共用；常量集中避免复制粘贴
	// 不一致（曾因两处默认值漂移导致 -c 实际指向老路径）。
	defaultConfigPath = "server_config.yml"
)

func main() {
	configPath := flag.String("config", defaultConfigPath, "配置文件路径（YAML）")
	flag.StringVar(configPath, "c", defaultConfigPath, "配置文件路径（YAML）（-config 的别名）")
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

	// 首次运行未找到配置文件时自动生成默认配置（本地示例 > 内置模板），当次即加载生效——
	// 对齐 TS autoCreateConfig。失败不致命，继续用内存默认值。
	configCreated, _ := config.EnsureDefaultFile(*configPath)

	// 配置优先级：命令行参数 > 环境变量 > 配置文件 > 内置默认值。
	cfg, fromFile, err := config.LoadMerged(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config %s: %v\n", *configPath, err)
		os.Exit(1)
	}

	// 启动期语言在配置加载后即可确定（与 state.ServerLang 同源：PHIRA_MP_LANG > LANG >
	// 配置 LANG），供 CLI 参数校验报错与启动期日志本地化。
	lang := l10n.NewLanguage(cfg.EffectiveLang())

	// 命令行参数覆盖（优先级最高）：仅处理显式传入的标志，校验失败按 lang 本地化后退出。
	applyCLIOverrides(cfg, lang)

	logger := logging.New(cfg.EffectiveLogLevel(), "logs")
	defer logger.Close()

	// 运行时本地化覆盖：locales/<lang>.ftl 存在则覆盖对应内置文案（须在服务前加载）。
	if n := l10n.LoadOverrides("locales"); n > 0 {
		logger.Info(l10n.TL(lang, "log-locale-overrides-loaded", map[string]string{"count": strconv.Itoa(n)}))
	}
	if configCreated {
		logger.Info(l10n.TL(lang, "log-config-created", map[string]string{"path": *configPath}))
	} else if fromFile {
		logger.Info(l10n.TL(lang, "log-config-loaded", map[string]string{"path": *configPath}))
	} else {
		logger.Info(l10n.TL(lang, "log-config-not-found", nil))
	}

	// Redis 缓存（启用时谱面/记录/token 缓存转为多实例共享）。startup-only：仅启动时初始化。
	if err := cache.InitRedis(cfg.Redis); err != nil {
		logger.Warn(l10n.TL(lang, "log-redis-fallback", map[string]string{"error": err.Error()}))
	} else if cache.RedisEnabled() {
		logger.Mark(l10n.TL(lang, "log-redis-enabled", nil))
	}
	defer cache.CloseRedis()

	adminDataPath := strOr(cfg.AdminDataPath, "admin_data.json")
	state := server.NewServerState(cfg, logger, cfg.EffectiveServerName(), adminDataPath, *configPath)
	// 配置热重载时同步日志级别。
	state.OnConfigReload(func(c *config.ServerConfig) { logger.SetLevel(c.EffectiveLogLevel()) })
	// 日志旁路到 GUI 控制台缓冲（供 /admin/console/logs 回填与 WS 推送）。
	logger.SetOnLog(state.ConsoleHub.Push)

	// Webhook 事件外发（对局/房间/维护事件 → 群机器人等）。非阻塞，未配置则 no-op；热重载生效。
	webhookDispatcher := webhook.New(logger)
	webhookDispatcher.SetConfig(cfg.EffectiveWebhook())
	state.Events = webhookDispatcher
	state.OnConfigReload(func(c *config.ServerConfig) { webhookDispatcher.SetConfig(c.EffectiveWebhook()) })
	defer webhookDispatcher.Close()
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
	hub := server.NewHub(state, phira.NewClient(phiraEndpoint))

	// 观战数据聚合缓冲：高频 Touches/Judges 按 ~50ms 窗口合并后批量转发观战者。
	monitorBuf := server.NewMonitorBuffer()
	hub.Monitor = monitorBuf

	// 回放录制：录制器注入全局状态（供 dispatch 的 Append*/SetRecordID），并通过
	// OnEnterPlaying/OnGameEnd 钩子驱动每局的开始/落盘。
	recorder := replay.NewRecorder(cfg.EffectiveReplayBaseDir(), logger)
	state.ReplayRecorder = recorder

	// 对局结束自动上传分享站（延迟 30s）。仅在 REPLAY_AUTO_UPLOAD 开启且分享站配置时实际生效。
	autoUploader := autoupload.New(state, autoupload.DefaultDelay)
	state.AutoUploadCallback = autoUploader.Handle
	defer autoUploader.Close()

	// 对局成绩持久化（SQLite）：每局结算写入 match_results + 增量 rollup，
	// 支撑玩家档案、排行榜、谱面热度榜。DB 文件路径可经 STATS_DB_PATH 配置。
	statsPath := cfg.EffectiveStatsDBPath()
	statsStore, statsErr := stats.Open(statsPath)
	if statsErr != nil {
		logger.Warn(l10n.TL(lang, "log-stats-open-failed", map[string]string{"path": statsPath, "error": statsErr.Error()}))
	} else {
		logger.Mark(l10n.TL(lang, "log-stats-opened", map[string]string{"path": statsPath}))
		defer statsStore.Close()
	}

	hub.OnEnterPlaying = func(room *server.Room) {
		ev := server.Event{Type: server.EventGameStart, RoomID: room.ID.String(), UserCount: room.UserCount()}
		if room.Chart != nil {
			ev.ChartID, ev.ChartName = room.Chart.ID, room.Chart.Name
		}
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
		ev := server.Event{Type: server.EventGameEnd, RoomID: room.ID.String()}
		if room.Chart != nil {
			ev.ChartID, ev.ChartName = room.Chart.ID, room.Chart.Name
		}
		state.EmitEvent(ev)
		go func() {
			recorder.EndRoom(room.ID) // 落盘放到 goroutine，避免阻塞命令处理（持有 state.Mu）
			// 落盘完成后逐个触发自动上传（Handle 内部判断开关/分享站/延迟）。
			for _, f := range recorder.ListRoomFiles(room.ID) {
				autoUploader.Handle(f.UserID, f.ChartID, f.Timestamp, 0)
			}
		}()
		// 成绩持久化：异步写入 SQLite（不阻塞命令循环）。拷贝状态避免 room 状态
		// 被重置为 StateSelectChart 后原 map/slice 被覆写。
		if statsStore != nil {
			st, ok := room.PlayingState()
			if ok && len(st.Results) > 0 {
				roomID := room.ID.String()
				chartID := 0
				chartName := ""
				if room.Chart != nil {
					chartID = room.Chart.ID
					chartName = room.Chart.Name
				}
				userIDs := room.UserIDs()
				results := make(map[int]config.RecordData, len(st.Results))
				userNames := make(map[int]string, len(st.Results))
				for uid, rd := range st.Results {
					results[uid] = rd
					if u := state.Users[uid]; u != nil {
						userNames[uid] = u.Name
					}
				}
				durationSec := time.Since(st.StartedAt).Seconds()
				go func() {
					mr, err := statsStore.RecordMatch(roomID, chartID, chartName, userIDs, results, userNames, durationSec)
					if err != nil {
						logger.Warn("stats write failed: " + err.Error())
						return
					}
					// 同步 Redis 排行榜（使用事务内计算好的聚合值，免 N+1 回查）。
					for _, r := range mr {
						stats.SyncLeaderboard(r.UserID, r.Rating, r.PlayTimeSec, r.TotalScore)
					}
					if chartID != 0 && len(mr) > 0 && mr[0].ChartPop > 0 {
						stats.SyncChartHot(chartID, mr[0].ChartPop)
					}
				}()
			}
		}
	}

	// 回放过期清理：启动时清一次，并每日定时清理。
	recorder.CleanupExpired(time.Now(), cfg.EffectiveReplayTTLDays())
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			recorder.CleanupExpired(time.Now(), cfg.EffectiveReplayTTLDays())
		}
	}()

	// 成绩统计明细裁剪：每日清理超过保留期的 match_results 明细（rollup 不受影响）。
	if statsStore != nil {
		statsStore.CleanupDetail(cfg.EffectiveStatsDetailRetentionDays())
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				retDays := cfg.EffectiveStatsDetailRetentionDays()
				if err := statsStore.CleanupDetail(retDays); err != nil {
					logger.Warn("stats cleanup failed: " + err.Error())
				}
				if err := statsStore.VacuumIfNeeded(statsPath, cfg.EffectiveStatsDBMaxMB()); err != nil {
					logger.Warn("stats vacuum failed: " + err.Error())
				}
			}
		}()
	}

	host := strOr(cfg.Host, "0.0.0.0")
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
		httpSvc = httpapi.New(state, hub, statsStore)
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

	// 配置文件热重载：轮询配置文件变更，重新加载并热生效（startup-only 项仅提示需重启）。
	watcher := config.NewFileWatcher(*configPath, 2*time.Second, func() {
		next, _, lerr := config.LoadMerged(*configPath)
		if lerr != nil {
			logger.Warn(l10n.TL(lang, "log-config-reload-skipped", map[string]string{"error": lerr.Error()}))
			return
		}
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
