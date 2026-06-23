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
)

// 默认监听端口（标准 Phira MP 端口）。
const (
	defaultPort     = 12346
	defaultHTTPPort = 12347
)

func main() {
	configPath := flag.String("config", "server_config.yml", "配置文件路径（YAML）")
	guiFlag := flag.Bool("gui", false, "启动时打开服务端 GUI 控制台窗口（覆盖配置中的 GUI）")
	flag.Parse()

	// 配置优先级：环境变量 > 配置文件 > 内置默认值。
	cfg, fromFile, err := config.LoadMerged(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config %s: %v\n", *configPath, err)
		os.Exit(1)
	}
	if *guiFlag {
		cfg.GUI = guiFlag // 启动参数 --gui 强制开启 GUI（含隐含的 HTTP 服务）
	}

	logger := logging.New(cfg.EffectiveLogLevel(), "logs")
	defer logger.Close()

	// 运行时本地化覆盖：locales/<lang>.ftl 存在则覆盖对应内置文案（须在服务前加载）。
	if n := l10n.LoadOverrides("locales"); n > 0 {
		logger.Info(fmt.Sprintf("loaded locale overrides for %d language(s)", n))
	}
	if fromFile {
		logger.Info("loaded config from " + *configPath)
	} else {
		logger.Info("config file not found, using environment variables and defaults")
	}

	// Redis 缓存（启用时谱面/记录/token 缓存转为多实例共享）。startup-only：仅启动时初始化。
	if err := cache.InitRedis(cfg.Redis); err != nil {
		logger.Warn(fmt.Sprintf("Redis 连接失败，回退本地缓存: %v", err))
	} else if cache.RedisEnabled() {
		logger.Mark("Redis 缓存已启用（多实例共享）")
	}
	defer cache.CloseRedis()

	adminDataPath := strOr(cfg.AdminDataPath, "admin_data.json")
	state := server.NewServerState(cfg, logger, cfg.EffectiveServerName(), adminDataPath, *configPath)
	// 配置热重载时同步日志级别。
	state.OnConfigReload(func(c *config.ServerConfig) { logger.SetLevel(c.EffectiveLogLevel()) })
	// 日志旁路到 GUI 控制台缓冲（供 /admin/console/logs 回填与 WS 推送）。
	logger.SetOnLog(state.ConsoleHub.Push)
	if err := state.LoadAdminData(); err != nil {
		logger.Warn(fmt.Sprintf("failed to load admin data: %v", err))
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
	monitorBuf := server.NewMonitorBuffer(state)
	hub.Monitor = monitorBuf

	// 回放录制：录制器注入全局状态（供 dispatch 的 Append*/SetRecordID），并通过
	// OnEnterPlaying/OnGameEnd 钩子驱动每局的开始/落盘。
	recorder := replay.NewRecorder(cfg.EffectiveReplayBaseDir(), logger)
	state.ReplayRecorder = recorder

	// 对局结束自动上传分享站（延迟 30s）。仅在 REPLAY_AUTO_UPLOAD 开启且分享站配置时实际生效。
	autoUploader := autoupload.New(state, autoupload.DefaultDelay)
	state.AutoUploadCallback = autoUploader.Handle
	defer autoUploader.Close()

	hub.OnEnterPlaying = func(room *server.Room) {
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
		go func() {
			recorder.EndRoom(room.ID) // 落盘放到 goroutine，避免阻塞命令处理（持有 state.Mu）
			// 落盘完成后逐个触发自动上传（Handle 内部判断开关/分享站/延迟）。
			for _, f := range recorder.ListRoomFiles(room.ID) {
				autoUploader.Handle(f.UserID, f.ChartID, f.Timestamp, 0)
			}
		}()
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

	host := strOr(cfg.Host, "0.0.0.0")
	port := defaultPort
	if cfg.Port != nil {
		port = *cfg.Port
	}
	// JoinHostPort 正确处理 IPv6（如 HOST: "::" → "[::]:port"）。
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	lang := state.ServerLang
	srv, err := network.Listen(addr, state, hub)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to listen on %s: %v", addr, err))
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
		httpSvc = httpapi.New(state, hub)
		httpAddr, herr := httpSvc.Start(net.JoinHostPort(host, strconv.Itoa(httpPort)))
		if herr != nil {
			logger.Error(fmt.Sprintf("failed to start HTTP service: %v", herr))
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
			logger.Warn("config reload skipped: " + lerr.Error())
			return
		}
		changed, restart := state.ReloadConfig(next)
		if len(changed) > 0 {
			logger.Mark("config reloaded: " + strings.Join(changed, ", "))
		}
		if len(restart) > 0 {
			logger.Warn("config changes require restart to take effect: " + strings.Join(restart, ", "))
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
