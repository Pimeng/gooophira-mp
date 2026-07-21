package server

import (
	"slices"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/platform/l10n"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/version"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
)

// 周期内存清理参数（对应 TS ServerState.CLEANUP_INTERVAL_MS / UPLOAD_META_RETENTION_MS）。
const (
	cleanupInterval       = time.Hour
	uploadMetaRetentionMS = int64(7 * 24 * 60 * 60 * 1000) // 上传元数据保留 7 天
)

// ContestRoomEntry 是比赛房间在全局状态中的白名单条目。
type ContestRoomEntry struct {
	Whitelist map[int]struct{}
}

// ServerState 是服务器全局状态容器：用户/会话/房间、封禁列表、回放、管理员认证、
// 各类回调与定时清理。并发修改需经 Mu 保护（对应 TS 的 async Mutex；Go 用 sync.Mutex
// 配合 Lock/Unlock，关键临界区在各方法内部加锁）。
type ServerState struct {
	// Mu 保护并发状态修改。
	Mu sync.Mutex

	// Config 当前配置（运行时可热重载）。
	Config *config.ServerConfig
	// Logger 日志记录器。
	Logger Logger
	// ServerName 显示名称。
	ServerName string
	// ServerLang 服务器本地化语言。
	ServerLang *l10n.Language
	// AdminDataPath 管理员数据文件路径。
	AdminDataPath string
	// ConfigPath 配置文件路径（持久化运行时配置变更用）。
	ConfigPath string
	// ConfigDir 非空表示使用多文件配置；运行时改动按字段写入对应模块文件。
	ConfigDir string
	// ReplayEnabled 是否启用回放录制。
	ReplayEnabled bool
	// RoomCreationEnabled 是否允许建房。
	RoomCreationEnabled bool
	// Maintenance 维护模式（纯内存，重启复位）。
	Maintenance bool
	// MaintenanceMessage 维护模式自定义提示（nil = 用默认文案）。
	MaintenanceMessage *string
	// Version 服务器版本。
	Version string

	// ---------- 核心数据存储 ----------

	// Sessions 活跃会话（sessionID -> Session）。
	Sessions map[string]Session
	// Users 在线用户（userID -> User）。
	Users map[int]*User
	// Rooms 活跃房间（roomID -> Room）。
	Rooms map[protocol.RoomID]*Room

	// ---------- 封禁管理 ----------

	// BannedUsers 全局封禁用户集合。
	BannedUsers map[int]struct{}
	// BannedRoomUsers 房间级封禁（roomID -> userID 集合）。
	BannedRoomUsers map[protocol.RoomID]map[int]struct{}
	// ContestRooms 比赛房间白名单（roomID -> 条目）。
	ContestRooms map[protocol.RoomID]*ContestRoomEntry

	// ---------- 回放录制 ----------

	// ReplayRecorder 回放录制器（Stage 5 注入具体实现）。
	ReplayRecorder ReplayRecorder

	// ---------- 服务引用与回调 ----------

	// WSService WebSocket 服务（仅 HTTP 服务启用时存在）。
	WSService WebSocketService
	// Events 服务器事件外发槽（Webhook 等）；nil = 不外发。注入见 cmd/server。
	Events EventSink
	// ConsoleHub 控制台日志中心。
	ConsoleHub *ConsoleHub
	// ConsoleExecutor GUI 控制台命令执行器（nil = 未就绪）。
	ConsoleExecutor func(line string) ([]ConsoleOutputLine, error)
	// RuntimeConfigReloader 运行时配置重载器（admin 配置接口写回后立即生效）。
	RuntimeConfigReloader func(patch *config.ServerConfig) error
	// LastRuntimeConfigRollback 最近一次运行时配置修改的回滚快照（envName -> value）。
	LastRuntimeConfigRollback map[string]any
	// AutoUploadCallback 游戏结束触发回放上传（由 HttpService 设置）。
	AutoUploadCallback func(userID, chartID int, timestamp int64, recordID int)

	// ---------- 管理员认证 ----------

	// TempAdminTokens 临时管理员 token 表。
	TempAdminTokens map[string]*TempAdminToken
	// CLIApprovalSessions CLI 提权批准会话表。
	CLIApprovalSessions map[string]*CLIApprovalSession

	// ---------- 自动上传 ----------

	// AutoUploadConfigs 用户自动上传显示配置（仅内存）。
	AutoUploadConfigs map[int]*AutoUploadConfig
	// UploadedReplayMeta 已上传回放元数据（userID -> chartID -> metas），去重用。
	UploadedReplayMeta map[int]map[int][]UploadedReplayMeta

	// configListeners 配置热重载后的组件更新回调（更新限速器阈值/日志级别等）。
	// 注册在启动期（单线程），重载时在 Mu 外逐个调用。
	configListeners []func(*config.ServerConfig)

	// cleanupStop/cleanupDone 周期清理 goroutine 的生命周期通道（nil = 未启动）。
	cleanupStop chan struct{}
	cleanupDone chan struct{}
}

// NewServerState 创建服务器状态实例。
func NewServerState(cfg *config.ServerConfig, logger Logger, serverName, adminDataPath, configPath string) *ServerState {
	return &ServerState{
		Config:              cfg,
		Logger:              logger,
		ServerName:          serverName,
		ServerLang:          l10n.NewLanguage(cfg.EffectiveLang()),
		AdminDataPath:       adminDataPath,
		ConfigPath:          configPath,
		ReplayEnabled:       cfg.EffectiveReplayEnabled(),
		RoomCreationEnabled: cfg.EffectiveRoomCreationEnabled(),
		Version:             readAppVersion(),
		Sessions:            make(map[string]Session),
		Users:               make(map[int]*User),
		Rooms:               make(map[protocol.RoomID]*Room),
		BannedUsers:         make(map[int]struct{}),
		BannedRoomUsers:     make(map[protocol.RoomID]map[int]struct{}),
		ContestRooms:        make(map[protocol.RoomID]*ContestRoomEntry),
		ConsoleHub:          NewConsoleHub(),
		TempAdminTokens:     make(map[string]*TempAdminToken),
		CLIApprovalSessions: make(map[string]*CLIApprovalSession),
		AutoUploadConfigs:   make(map[int]*AutoUploadConfig),
		UploadedReplayMeta:  make(map[int]map[int][]UploadedReplayMeta),
	}
}

// EmitEvent 异步外发一个服务器事件（Webhook 等）。Events 未注入时为 no-op。
// 缺省补齐 Time/Server。调用方可能持有 Mu——实现保证非阻塞，故此处直接调用安全。
func (s *ServerState) EmitEvent(ev Event) {
	if s.Events == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.Server == "" {
		ev.Server = s.ServerName
	}
	s.Events.Emit(ev)
}

// ShareStation 返回分享站配置（未配置时为 nil）。
func (s *ServerState) ShareStation() *config.ShareStation {
	return s.Config.ShareStation
}

// ShareStationConfigured 报告分享站是否已配置（URL 与 Token 均存在）。
func (s *ServerState) ShareStationConfigured() bool {
	return s.Config.ShareStationConfigured()
}

// SystemChatUserID 返回系统聊天消息发送者的 User ID（int32 形式，可直接填入 MsgChat.User）。
// 未配置 SYSTEM_USER_ID 时返回 0（保留「系统」语义，客户端按系统消息渲染）；
// 配置为真实 Phira 用户 ID 后，所有系统消息将以该身份发送，客户端可凭此 ID 拉取头像与昵称。
func (s *ServerState) SystemChatUserID() int32 {
	return int32FromInt(s.Config.EffectiveSystemUserID())
}

// ApplyConfig 应用新配置到服务器状态（热重载时调用，须持 Mu）。
func (s *ServerState) ApplyConfig(cfg *config.ServerConfig) {
	s.Config = cfg
	s.ServerName = cfg.EffectiveServerName()
	s.ServerLang = l10n.NewLanguage(cfg.EffectiveLang())
	s.ReplayEnabled = cfg.EffectiveReplayEnabled()
	s.RoomCreationEnabled = cfg.EffectiveRoomCreationEnabled()
	if s.ReplayRecorder != nil {
		s.ReplayRecorder.SetBaseDir(cfg.EffectiveReplayBaseDir())
	}
}

// OnConfigReload 注册配置热重载回调（启动期调用）。重载完成后会以最新生效配置回调，
// 供各组件更新运行参数（如连接限速阈值、HTTP 限速选项、日志级别）。
func (s *ServerState) OnConfigReload(fn func(*config.ServerConfig)) {
	s.Mu.Lock()
	s.configListeners = append(s.configListeners, fn)
	s.Mu.Unlock()
}

// ReloadConfig 用 next 热重载配置：保留仅启动期配置（只记录需重启的键），计算变更键，
// 应用到状态，处理回放开关副作用（刷新房间 live / 关闭时结束进行中的录制），并回调各监听器。
// 返回实际变更键与「需重启才能生效」的键。无变更时为 no-op。对应 TS reloadRuntimeConfig。
func (s *ServerState) ReloadConfig(next *config.ServerConfig) (changed, restart []string) {
	s.Mu.Lock()
	prev := s.Config
	var effective *config.ServerConfig
	effective, restart = config.KeepStartupOnly(prev, next)
	changed = config.ChangedKeys(prev, effective)
	if len(changed) == 0 && len(restart) == 0 {
		s.Mu.Unlock()
		return nil, restart
	}
	replayWas := s.ReplayEnabled
	s.ApplyConfig(effective)
	replayNow := s.ReplayEnabled

	// 回放开关变化：重算各房间 live；关闭回放时收集需结束录制的房间（落盘放到锁外）。
	var endRooms []protocol.RoomID
	if replayWas != replayNow {
		for id, room := range s.Rooms {
			room.RefreshLive(replayNow)
			if !replayNow {
				endRooms = append(endRooms, id)
			}
		}
	}
	listeners := slices.Clone(s.configListeners)
	s.Mu.Unlock()

	for _, fn := range listeners {
		fn(effective)
	}
	if s.ReplayRecorder != nil {
		for _, id := range endRooms {
			s.ReplayRecorder.EndRoom(id)
		}
	}
	return changed, restart
}

// ApplyRuntimePatch 把一份已校验的运行时补丁落盘（保留注释）并热生效，记录回滚快照。
// 供 CLI / HTTP admin 配置接口调用。返回变更键与需重启的键。
func (s *ServerState) ApplyRuntimePatch(res config.RuntimePatchResult) (changed, restart []string) {
	s.Mu.Lock()
	rollback := config.PickRuntimeConfigSnapshot(s.Config, res.Keys)
	clone := *s.Config // 浅拷贝：apply 重新赋值指针字段，不改动共享 pointee
	s.Mu.Unlock()

	res.Apply(&clone)

	if s.ConfigDir != "" {
		if err := config.PersistConfigDirValues(s.ConfigDir, res.Persist); err != nil && s.Logger != nil {
			s.Logger.Warn("failed to persist config: " + err.Error())
		}
	} else if s.ConfigPath != "" {
		if err := config.PersistConfigValues(s.ConfigPath, res.Persist); err != nil && s.Logger != nil {
			s.Logger.Warn("failed to persist config: " + err.Error())
		}
	}
	changed, restart = s.ReloadConfig(&clone)

	s.Mu.Lock()
	s.LastRuntimeConfigRollback = rollback
	s.Mu.Unlock()
	return changed, restart
}

// CleanupUserData 清理指定用户的自动上传配置与元数据（用户完全退出时调用）。
// 调用方必须持 Mu。
func (s *ServerState) CleanupUserData(userID int) {
	delete(s.AutoUploadConfigs, userID)
	delete(s.UploadedReplayMeta, userID)
}

// LoadAdminData / SaveAdminData / FlushAdminDataNow 见 admindata.go。

// StartCleanup 启动周期性内存清理任务（每小时一次）。幂等：已启动则不重复启动。
// 对应 TS ServerState.startCleanup。
func (s *ServerState) StartCleanup() {
	s.Mu.Lock()
	if s.cleanupStop != nil {
		s.Mu.Unlock()
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	s.cleanupStop, s.cleanupDone = stop, done
	s.Mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.RunCleanupOnce(time.Now())
			}
		}
	}()
}

// StopCleanup 停止周期性清理任务并等待其退出（幂等）。
func (s *ServerState) StopCleanup() {
	s.Mu.Lock()
	stop, done := s.cleanupStop, s.cleanupDone
	s.cleanupStop, s.cleanupDone = nil, nil
	s.Mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

// RunCleanupOnce 执行一次内存清理：过期上传元数据、离线用户的自动上传配置、过期/拒绝的
// CLI 审批会话、过期/封禁的临时管理员 token。now 注入便于测试。对应 TS runCleanup。
func (s *ServerState) RunCleanupOnce(now time.Time) {
	nowMS := now.UnixMilli()
	cutoffUpload := nowMS - uploadMetaRetentionMS

	s.Mu.Lock()
	defer s.Mu.Unlock()

	// 1. 清理 7 天前的上传回放元数据。
	for userID, chartMap := range s.UploadedReplayMeta {
		for chartID, metas := range chartMap {
			kept := make([]UploadedReplayMeta, 0, len(metas))
			for _, m := range metas {
				if m.Timestamp >= cutoffUpload {
					kept = append(kept, m)
				}
			}
			if len(kept) == 0 {
				delete(chartMap, chartID)
			} else if len(kept) != len(metas) {
				chartMap[chartID] = kept
			}
		}
		if len(chartMap) == 0 {
			delete(s.UploadedReplayMeta, userID)
		}
	}

	// 2. 清理已离线用户的自动上传配置（无时间戳，离线即回收；dangling 仅 10s 远短于小时级周期）。
	for userID := range s.AutoUploadConfigs {
		if _, online := s.Users[userID]; !online {
			delete(s.AutoUploadConfigs, userID)
		}
	}

	// 3. 清理已过期或被拒绝的 CLI 审批会话。
	for key, sess := range s.CLIApprovalSessions {
		if sess == nil || nowMS > sess.ExpiresAt || sess.Status == CLIApprovalDenied {
			delete(s.CLIApprovalSessions, key)
		}
	}

	// 4. 清理已过期或被封禁的临时管理员 token。
	for token, data := range s.TempAdminTokens {
		if data == nil || data.Banned || nowMS > data.ExpiresAt {
			delete(s.TempAdminTokens, token)
		}
	}
}

// readAppVersion 读取应用版本（来源见 internal/common/version）。
func readAppVersion() string {
	return version.Get()
}
