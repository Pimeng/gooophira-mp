// Package autoupload 实现对局结束后把回放自动上传到分享站（延迟 30 秒执行）。
// 对应 TS server/replay/autoUpload.ts。仅在 REPLAY_AUTO_UPLOAD 开启且分享站已配置时生效。
package autoupload

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agent/integration/sharestation"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/replay"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// DefaultDelay 是对局结束到执行上传的延迟（给客户端完成成绩上报留出时间）。
const DefaultDelay = 30 * time.Second

// metaCapPerChart 限制每个谱面保留的上传元数据条数，防内存泄漏（对齐 TS）。
const metaCapPerChart = 50

// Uploader 调度并执行对局结束后的回放自动上传。
type Uploader struct {
	state  *server.ServerState
	delay  time.Duration
	mu     sync.Mutex
	timers map[*time.Timer]struct{}
	closed bool
}

// New 创建自动上传器。delay<=0 时回退 DefaultDelay。
func New(state *server.ServerState, delay time.Duration) *Uploader {
	if delay <= 0 {
		delay = DefaultDelay
	}
	return &Uploader{state: state, delay: delay, timers: make(map[*time.Timer]struct{})}
}

// Handle 是对局结束回调：满足条件则在 delay 后异步上传该用户该谱面的回放。
// 签名匹配 server.ServerState.AutoUploadCallback。
func (u *Uploader) Handle(userID, chartID int, timestamp int64, _recordID int) {
	cfg := u.snapshotConfig()
	if !cfg.EffectiveReplayAutoUpload() || !cfg.ShareStationConfigured() {
		return // 未启用或分享站未配置：保留本地文件
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(u.delay, func() {
		u.mu.Lock()
		delete(u.timers, t)
		u.mu.Unlock()
		u.uploadNow(userID, chartID, timestamp)
	})
	u.timers[t] = struct{}{}
}

// Close 取消所有待执行的上传计时器（关闭时调用）。
func (u *Uploader) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	for t := range u.timers {
		t.Stop()
	}
	u.timers = make(map[*time.Timer]struct{})
}

func (u *Uploader) snapshotConfig() *config.ServerConfig {
	u.state.Mu.Lock()
	defer u.state.Mu.Unlock()
	return u.state.Config
}

// uploadNow 执行一次实际上传：校验归属 → 上传 → 记录元数据 → 按用户配置设可见 → 删本地。
func (u *Uploader) uploadNow(userID, chartID int, timestamp int64) {
	cfg := u.snapshotConfig()
	if !cfg.EffectiveReplayAutoUpload() { // 期间可能被关闭
		return
	}
	client, ok := clientFromConfig(cfg)
	if !ok {
		return
	}
	baseDir := cfg.EffectiveReplayBaseDir()
	path := replay.FilePath(baseDir, userID, chartID, timestamp)

	header, err := replay.ReadReplayHeader(path)
	if err != nil || header.UserID != userID || header.ChartID != chartID {
		u.warn(fmt.Sprintf("auto upload skipped for user %d: replay not found or invalid", userID))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		u.warn(fmt.Sprintf("auto upload failed for user %d: read error: %v", userID, err))
		return
	}

	res, err := client.Upload(data, fmt.Sprintf("%d.phirarec", timestamp), header.ChartName, header.UserName)
	if err != nil {
		u.warn(fmt.Sprintf("auto upload failed for user %d: %v", userID, err))
		return
	}
	if res.ScoreID == 0 {
		return
	}

	// 先删本地文件再写 meta：上传已成功，本地文件可安全删除。
	// 调换顺序是为了避免「meta 已写、文件未删」的窗口被观察者（测试或状态查询）看到。
	if err := os.Remove(path); err != nil {
		u.warn(fmt.Sprintf("failed to delete local replay for user %d: %v", userID, err))
	}
	show := u.storeMetaAndCheckShow(userID, chartID, timestamp, res.ScoreID)
	if show {
		_ = client.SetVisibility(res.ScoreID, true)
	}
	u.info(fmt.Sprintf("auto upload completed for user %d, chart %d, scoreId %d", userID, chartID, res.ScoreID))
}

// storeMetaAndCheckShow 记录上传元数据（每谱面截断至 metaCapPerChart 条），并返回该用户是否设为可见。
func (u *Uploader) storeMetaAndCheckShow(userID, chartID int, timestamp int64, scoreID int) bool {
	u.state.Mu.Lock()
	defer u.state.Mu.Unlock()
	um := u.state.UploadedReplayMeta[userID]
	if um == nil {
		um = make(map[int][]server.UploadedReplayMeta)
		u.state.UploadedReplayMeta[userID] = um
	}
	list := append(um[chartID], server.UploadedReplayMeta{ScoreID: scoreID, ChartID: chartID, Timestamp: timestamp})
	if len(list) > metaCapPerChart {
		list = list[len(list)-metaCapPerChart:]
	}
	um[chartID] = list

	if c := u.state.AutoUploadConfigs[userID]; c != nil {
		return c.Show
	}
	return false
}

// clientFromConfig 按配置构造分享站客户端（未配置返回 ok=false）。
func clientFromConfig(cfg *config.ServerConfig) (*sharestation.Client, bool) {
	ss := cfg.ShareStation
	if ss == nil || ss.URL == "" || ss.Token == "" {
		return nil, false
	}
	return sharestation.NewClient(sharestation.Config{URL: ss.URL, Token: ss.Token}), true
}

func (u *Uploader) warn(msg string) {
	if u.state.Logger != nil {
		u.state.Logger.Warn(msg)
	}
}
func (u *Uploader) info(msg string) {
	if u.state.Logger != nil {
		u.state.Logger.Info(msg)
	}
}
