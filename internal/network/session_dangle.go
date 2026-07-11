// session_dangle.go 把「断线挂起 / 重连宽限 / 用户移除 / 房间清理」链路与相关日志从 session.go
// 拆出。挂在房间内仍可重连的 dangling 状态由 User.DangleToken 跟踪，宽限窗到期后由本
// 文件中的 processDangle 在 timer goroutine 中收尾。
package network

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// dangleWindowNonPlaying 是非对局态断线后保留房间、等待重连的窗口（对应 TS DANGLE_WINDOW_MS）。
// 为 atomic.Int64 便于测试安全调短（避免并发 data race）。对局态断线用 config.playing_reconnect_grace（0=立即移除）。
var dangleWindowNonPlaying atomic.Int64

func init() { dangleWindowNonPlaying.Store(int64(10 * time.Second)) }

func (s *Session) cleanup() {
	s.state.Mu.Lock()
	u := s.user
	if u == nil || u.Session() != server.Session(s) {
		s.state.Mu.Unlock()
		return // 已被顶号或未认证
	}
	u.SetSession(nil)
	s.logDisconnectDetail(u)

	// 被封禁用户、宽限为 0、或服务器关闭中：不等待重连，立即移除。
	_, banned := s.state.BannedUsers[u.ID]
	grace := s.dangleGrace(u)
	if banned || grace <= 0 || s.closing.Load() {
		// 对齐原版：封禁用户记 INFO 挂起日志；宽限为 0（对局态配置或非对局窗口）且仍在房间则记房间作用域 WARN（强制退出）。
		reason := "grace-zero"
		if banned {
			reason = "banned"
		} else if s.closing.Load() {
			reason = "server-closing"
		}
		s.logDangleSkip(u, reason)
		if banned {
			s.logLocalized("INFO", "log-user-dangle", map[string]string{"user": u.Name})
		} else if u.Room != nil {
			s.logLocalized("WARN", "log-user-disconnect-playing", map[string]string{"user": u.Name, "room": string(u.Room.ID)})
		}
		s.removeUser(u)
		s.state.Mu.Unlock()
		return
	}
	// 否则标记 dangling，保留房间/用户一段时间，等待同账号重连（重连时 SetSession 清 token）。
	s.logDangleGrace(u, grace)
	s.logLocalized("INFO", "log-user-dangle", map[string]string{"user": u.Name})
	deadline := time.Now().Add(grace).UnixMilli()
	token := u.MarkDangle(&deadline)
	s.state.Mu.Unlock()
	t := time.AfterFunc(grace, func() { s.processDangle(u, token) })
	u.SetDangleTimer(t)
}

// logDisconnectDetail 在 cleanup 入口记录 DEBUG 级断线详情（含房间状态），便于排查重连链路。
// 调用方须持 state.Mu。
func (s *Session) logDisconnectDetail(u *server.User) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	inRoom, roomID, roomState := s.snapshotUserRoom(u)
	lg.Debug(fmt.Sprintf("连接ID：%s，“%s” 断线清理：在房间=%s，房间ID=%s，房间状态=%s",
		s.id, u.Name, strconv.FormatBool(inRoom), roomID, roomState))
}

// logDangleGrace 记录 DEBUG 级挂起宽限详情（含对局态判定）。
// 调用方须持 state.Mu。
func (s *Session) logDangleGrace(u *server.User, grace time.Duration) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	playing := false
	roomID := ""
	if u.Room != nil {
		roomID = string(u.Room.ID)
		u.Room.Mu.Lock()
		_, playing = u.Room.State.(server.StatePlaying)
		u.Room.Mu.Unlock()
	}
	lg.Debug(fmt.Sprintf("“%s” 进入挂起：宽限 %s 秒，对局态=%s，房间 “%s”",
		u.Name, strconv.Itoa(int(grace.Seconds())), strconv.FormatBool(playing), roomID))
}

// logDangleSkip 记录 DEBUG 级跳过挂起立即移除原因。
// 调用方须持 state.Mu。
func (s *Session) logDangleSkip(u *server.User, reason string) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	roomID := ""
	if u.Room != nil {
		roomID = string(u.Room.ID)
	}
	lg.Debug(fmt.Sprintf("“%s” 跳过挂起立即移除：原因=%s，房间=%s", u.Name, reason, roomID))
}

// snapshotUserRoom 读取用户当前房间状态（无锁读取 room.State 须持 room.Mu；调用方持 state.Mu）。
func (s *Session) snapshotUserRoom(u *server.User) (inRoom bool, roomID, roomState string) {
	if u.Room == nil {
		return false, "", ""
	}
	roomID = string(u.Room.ID)
	u.Room.Mu.Lock()
	roomState = fmt.Sprintf("%T", u.Room.State)
	u.Room.Mu.Unlock()
	return true, roomID, roomState
}

// dangleGrace 返回该用户断线后的保留窗口：对局态用配置宽限（0=立即），否则非对局窗口。
func (s *Session) dangleGrace(u *server.User) time.Duration {
	if u.Room != nil {
		room := u.Room
		room.Mu.Lock()
		_, playing := room.State.(server.StatePlaying)
		room.Mu.Unlock()
		if playing {
			return time.Duration(s.state.Config.EffectivePlayingReconnectGrace()) * time.Second
		}
	}
	return time.Duration(dangleWindowNonPlaying.Load())
}

// removeUser 退房并移除用户（调用方须持 state.Mu）。
func (s *Session) removeUser(u *server.User) {
	if u.Room != nil {
		room := u.Room
		lc := s.hub.MakeRoomLifecycle(room)
		room.Mu.Lock()
		shouldDrop, disband := room.OnUserLeave(lc, u)
		room.Mu.Unlock()
		if shouldDrop {
			delete(s.state.Rooms, room.ID)
		}
		// 比赛 AutoDisband：room.Mu 释放后再调 DisbandRoom（避免重入自死锁）。
		// 调用方持 state.Mu，DisbandRoom 内部 room.Mu.Lock() 安全。
		if disband {
			s.hub.DisbandRoom(room)
		}
	}
	delete(s.state.Users, u.ID)
	s.state.CleanupUserData(u.ID)
	// 清除 dangle 状态：防止残留 dangleToken 导致旧 session 的 stale timer
	// 在 IsStillDangling 中误判为 true（removeUser 不清 token 是原 bug 的根源之一）。
	u.ClearDangle()
}

// processDangle 在宽限窗到期后检查用户是否仍 dangling（未重连），是则移除。
func (s *Session) processDangle(u *server.User, token *server.DangleToken) {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	// 防御：用户可能已被先前 timer 或其他路径移除（stale timer 场景）。
	// SetSession 重连时已取消本 timer，正常不会走到这里；此守卫防御 timer.Stop
	// 返回 false（timer 已触发）但 goroutine 已启动的竞态。
	if _, exists := s.state.Users[u.ID]; !exists {
		s.logDangleStaleSkip(u)
		return
	}
	stillDangling := u.IsStillDangling(token)
	s.logDangleTimerFire(u, stillDangling)
	if !stillDangling {
		return // 已重连（SetSession 清除了 token 并取消了 timer）
	}
	// 对齐原版：挂起超时移除时，若仍在房间内记房间作用域 WARN。
	if u.Room != nil {
		s.logLocalized("WARN", "log-user-dangle-timeout-remove", map[string]string{"user": u.Name, "room": string(u.Room.ID)})
	}
	s.removeUser(u)
}

// logDangleTimerFire 记录 DEBUG 级挂起宽限到期检查结果。
// 调用方须持 state.Mu。
func (s *Session) logDangleTimerFire(u *server.User, stillDangling bool) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 挂起宽限到期，检查重连状态：仍挂起=%s",
		u.Name, strconv.FormatBool(stillDangling)))
}

// logDangleStaleSkip 记录 DEBUG 级 stale timer 跳过：用户已不在 state.Users 中。
// 调用方须持 state.Mu。
func (s *Session) logDangleStaleSkip(u *server.User) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 挂起 timer 触发但用户已不在用户表（stale timer 跳过）", u.Name))
}

// logLocalized 按级别记录一条本地化日志（连接/挂起生命周期用，nil 日志器时静默）。
func (s *Session) logLocalized(level, key string, args map[string]string) {
	lg := s.state.Logger
	if lg == nil {
		return
	}
	msg := l10n.TL(s.state.ServerLang, key, args)
	if level == "WARN" {
		lg.Warn(msg)
	} else {
		lg.Info(msg)
	}
}
