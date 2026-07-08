// Package network 实现 TCP 传输层：监听、连接握手、帧编解码、会话生命周期与命令循环。
// Session 实现 server.Session 接口，把解码后的命令喂给 server.Hub 驱动房间状态机。
package network

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

const (
	// protocolVersion 是支持的唯一协议版本（握手首字节）。
	protocolVersion  = 1
	handshakeTimeout = 10 * time.Second
	// proxyParseTimeout 是解析 HAProxy PROXY 头的读取超时（对应 TS parseProxyProtocol 的 1000ms）。
	proxyParseTimeout = 1 * time.Second
	// heartbeatTimeout 是无数据断连阈值（对应 protocol.HeartbeatDisconnectTimeoutMS）。
	heartbeatTimeout = time.Duration(protocol.HeartbeatDisconnectTimeoutMS) * time.Millisecond
	maxFrameSize     = 4 * 1024 * 1024
	readChunk        = 16 * 1024 // 4KB→16KB：一次读可容纳多帧，syscall 频率降 4x；100k 连接 × 32KB(bufio+tmp) = 3.2GB 可接受
	sendChanBuffer   = 256
)

// frameWriterPool 是 BinaryWriter（预留 5 字节 LEB128(u32) 头部）的对象池，
// 用于复用 encodeServerFrame 中的编码缓冲区，减少热路径上的分配。
var frameWriterPool = &sync.Pool{
	New: func() any { return protocol.NewFrameWriter(5) },
}

// dangleWindowNonPlaying 是非对局态断线后保留房间、等待重连的窗口（对应 TS DANGLE_WINDOW_MS）。
// 为 atomic.Int64 便于测试安全调短（避免并发 data race）。对局态断线用 config.playing_reconnect_grace（0=立即移除）。
var dangleWindowNonPlaying atomic.Int64

func init() { dangleWindowNonPlaying.Store(int64(10 * time.Second)) }

// Session 管理单个 TCP 连接：握手、认证、心跳、命令循环、断线清理。实现 server.Session。
type Session struct {
	id    string
	conn  net.Conn
	state *server.ServerState
	hub   *server.Hub

	user *server.User // 认证成功后设置（仅 readLoop goroutine 写）
	rl   *commandRateLimiter

	// 真实客户端地址：默认取 TCP 对端，启用 HAProxy PROXY protocol 时由头覆盖。
	// 仅 readLoop goroutine 读写。
	remoteIP   string
	remotePort int

	sendCh    chan []byte
	done      chan struct{}
	closeOnce sync.Once

	// closing由 Server.Close 标记：cleanup 走立即移除路径，不再设置 dangleTimer。
	// 避免关闭后 timer 触发访问已不稳定的状态。
	closing atomic.Bool
}

// 确保 Session 满足 server.Session。
var _ server.Session = (*Session)(nil)

func newSession(conn net.Conn, state *server.ServerState, hub *server.Hub) *Session {
	// TCP 优化：NODELAY 禁用 Nagle（小帧立即发，避免 40ms 合并延迟）；
	// 64KB 读写缓冲减少 syscall 频率（默认 4KB 在高频小包下 syscall 过多）。
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetWriteBuffer(64 * 1024)
		_ = tcp.SetReadBuffer(64 * 1024)
	}
	host, port := splitHostPort(conn.RemoteAddr())
	return &Session{
		id:         protocol.NewUUID(),
		conn:       conn,
		state:      state,
		hub:        hub,
		rl:         newCommandRateLimiter(time.Now()),
		remoteIP:   host,
		remotePort: port,
		sendCh:     make(chan []byte, sendChanBuffer),
		done:       make(chan struct{}),
	}
}

// splitHostPort 拆分 net.Addr 为主机与端口；解析失败时回退（host=原串, port=0）。
func splitHostPort(addr net.Addr) (string, int) {
	if addr == nil {
		return "", 0
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String(), 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

// ID 返回会话唯一标识。
func (s *Session) ID() string { return s.id }

// TrySend 把命令编码成帧并非阻塞入队；缓冲满（慢客户端）则断开连接。
func (s *Session) TrySend(cmd protocol.ServerCommand) {
	select {
	case <-s.done:
		return
	default:
	}
	frame, err := encodeServerFrame(cmd)
	if err != nil {
		return
	}
	s.trySendFrame(frame)
}

// TrySendFrame 尝试发送预编码的二进制帧（广播优化用）。为防止调用方把池化缓冲区
// 传给发送循环后又复用，此处拷贝一份；调用方若已自行拷贝（如 encodeServerCommandFrame
// 的输出）可改用 TrySendFrameOwned 节省一次分配。
func (s *Session) TrySendFrame(frame []byte) {
	select {
	case <-s.done:
		return
	default:
	}
	f := make([]byte, len(frame))
	copy(f, frame)
	s.trySendFrame(f)
}

// TrySendFrameOwned 尝试发送「调用方拥有所有权」的二进制帧——不再拷贝。调用方必须
// 保证 frame 在 sendCh 读取前不会被复用或修改。当前仅 encodeServerCommandFrame 输出的
// 「新建切片」符合此契约；其他来源请用 TrySendFrame。
func (s *Session) TrySendFrameOwned(frame []byte) {
	select {
	case <-s.done:
		return
	default:
	}
	s.trySendFrame(frame)
}

// trySendFrame 把帧入队发送缓冲；满则异步关闭。
//
// 满缓冲的关闭路径有两个选择：
//   - 同步 close：在持锁广播路径上会与 cleanup 抢锁导致自死锁——注释解释了为什么 go 出去。
//   - 异步 close：go s.Close() 在多慢消费者下会爆发 goroutine 风暴（N 个慢连接就有 N 个 close goroutine）。
//
// 折中：用 select+done 短路掉已经被关闭 / 已经在清理的会话。Close 内部有 closeOnce
// 幂等保护，并发安全。
func (s *Session) trySendFrame(frame []byte) {
	select {
	case <-s.done:
		return
	default:
	}
	select {
	case s.sendCh <- frame:
	default:
		// 慢消费者：异步关闭。closeOnce 防止重复清理；s.done 由 Close 关闭，
		// 即便下个广播再调用此处也会被 done 分支短路。
		if lg := s.state.Logger; lg != nil && lg.DebugEnabled() {
			lg.Debug(fmt.Sprintf("连接ID：%s，慢消费者：发送缓冲已满，异步关闭", s.id))
		}
		go s.Close()
	}
}

// Close 关闭会话（幂等）：停写、关 socket、清理用户与房间。
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
		s.cleanup()
	})
}

// closeForShutdown 由 Server.Close 调用：标记关闭中并关闭会话。
// closing=true 让 cleanup 走立即移除路径（不设 dangleTimer），避免关闭后 timer 触发；
// 同时取消 User 上已设置的 dangleTimer（边缘情况：cleanup 在 closing 标志前已执行）。
// 取消 timer 后 dangling 用户不会被 processDangle 清理，但进程退出时仅内存操作，无副作用。
func (s *Session) closeForShutdown() {
	s.closing.Store(true)
	if s.user != nil {
		s.user.StopDangleTimer()
	}
	s.Close()
}

// AdminDisconnect 管理员触发的断开（对应 TS session.adminDisconnect）：
//   - preserveRoom=false：等同普通断线（dangle 重连宽限后最终移除）；
//   - preserveRoom=true：仅断开连接并解绑会话，保留该用户在房间内（离线占位、可重连），不 dangle、不移除。
func (s *Session) AdminDisconnect(preserveRoom bool) {
	if !preserveRoom {
		s.Close()
		return
	}
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
		s.detachKeepRoom()
	})
}

// detachKeepRoom 解绑会话但保留用户的房间成员关系（不 dangle、不移除）。
func (s *Session) detachKeepRoom() {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	u := s.user
	if u == nil || u.Session() != server.Session(s) {
		return // 已被顶号或未认证
	}
	u.SetSession(nil)
}

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

// logAuthStart 在认证开始时记录 DEBUG（含 token 脱敏前缀），便于关联 phira 重试链路。
func (s *Session) logAuthStart(token string) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	prefix := token
	if len(prefix) > 8 {
		prefix = prefix[:8]
	} else if len(prefix) == 0 {
		prefix = "(empty)"
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，开始 Phira 认证（token 前缀：%s…）", s.id, prefix))
}

// logAuthFetchError 在 Phira 获取用户信息失败时记录 DEBUG（含原始翻译键/错误串），
// 上层 failAuth 仍只记 WARN 本地化原因；此处补充 DEBUG 用于排查上游问题。
func (s *Session) logAuthFetchError(err error) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() || err == nil {
		return
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，Phira 获取用户信息失败：%s", s.id, err.Error()))
}

// logReconnectDetected 在重连路径识别后记录 DEBUG（含房间信息）。调用方须持 state.Mu。
func (s *Session) logReconnectDetected(u *server.User, room *server.Room, roomStateStr string) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	inRoom := room != nil
	roomID := ""
	if inRoom {
		roomID = string(room.ID)
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，“%s” 走重连路径：在房间=%s，房间ID=%s，房间状态=%s",
		s.id, u.Name, strconv.FormatBool(inRoom), roomID, roomStateStr))
}

// logReconnectStaleKicked 记录 DEBUG 级重连顶号事件。
func (s *Session) logReconnectStaleKicked(u *server.User, stale server.Session) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 重连顶号：踢出旧会话，旧连接ID=%s", u.Name, stale.ID()))
}

// logReconnectRoomRestored 记录 DEBUG 级重连房间状态恢复结果。
func (s *Session) logReconnectRoomRestored(u *server.User, room *server.Room, roomStateStr string, hack bool) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 重连恢复房间状态：房间 “%s”，状态=%s，触发协议修正=%s",
		u.Name, string(room.ID), roomStateStr, strconv.FormatBool(hack)))
}

// logReconnectNoRoom 记录 DEBUG 级重连时已无房间。
func (s *Session) logReconnectNoRoom(u *server.User) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 重连时已无房间（可能已超时移除或房间解散）", u.Name))
}

// run 启动写循环并运行读循环（阻塞至连接结束）。
func (s *Session) run() {
	go s.writeLoop()
	s.readLoop()
}

// connLogger 是可选的「连接日志（带每 IP 频率抑制 / 黑名单）」能力。logging.Logger 实现之。
type connLogger interface {
	ConnectionLog(ip, msg string)
}

// logNewConnection 记录新连接（debug 级，含会话 ID 与真实来源地址）。对应 TS log-new-connection。
// 若日志器支持连接日志限流（ConnectionLog），则交由其按来源 IP 做频率抑制，避免日志洪水。
func (s *Session) logNewConnection() {
	lg := s.state.Logger
	if lg == nil {
		return
	}
	msg := fmt.Sprintf("收到新连接，连接ID：%s，来源：%s",
		s.id, net.JoinHostPort(s.remoteIP, strconv.Itoa(s.remotePort)))
	if cl, ok := lg.(connLogger); ok {
		cl.ConnectionLog(s.remoteIP, msg)
		return
	}
	lg.Debug(msg)
}

// writeLoop 用 net.Buffers.WriteTo 批量写：先取一帧，再 non-blocking 拉空 sendCh，
// 合并到一次 writev（Linux）或顺序 Write（其他平台）。单帧时退化为普通 Write，
// 多帧时把 N 次 syscall 合并为 1 次。预分配 64 长度的数组避免 append 扩容分配。
//
// 注意：net.Buffers.WriteTo 的 fallback 路径（非 Linux TCPConn）会消费 *v
// （*v = (*v)[1:]），所以每次循环必须重新构造 buffers 切片头，不能复用。
func (s *Session) writeLoop() {
	var bufArr [64][]byte
	for {
		buffers := net.Buffers(bufArr[:0])
		select {
		case <-s.done:
			return
		case frame := <-s.sendCh:
			buffers = append(buffers, frame)
		}
		// non-blocking 拉空 sendCh，最多合并 64 帧（受 bufArr 容量限制）。
		drained := false
		for !drained && len(buffers) < cap(buffers) {
			select {
			case f := <-s.sendCh:
				buffers = append(buffers, f)
			default:
				drained = true
			}
		}
		if _, err := buffers.WriteTo(s.conn); err != nil {
			if lg := s.state.Logger; lg != nil {
				lg.Warn(fmt.Sprintf("连接ID：%s，写循环 Write 失败：%v", s.id, err))
			}
			s.Close()
			return
		}
	}
}

func (s *Session) readLoop() {
	defer s.Close()
	r := bufio.NewReaderSize(s.conn, readChunk)

	// HAProxy PROXY protocol（可选）：在握手前解析真实客户端 IP。失败/非 PROXY 数据
	// 则保持 TCP 对端地址，宽松放行（对应 TS：解析返回 null 时继续，不断开）。
	if s.state.Config.EffectiveHAProxyProtocol() {
		_ = s.conn.SetReadDeadline(time.Now().Add(proxyParseTimeout))
		if info := ParseProxyHeader(r); info != nil {
			s.remoteIP = info.SourceAddr
			s.remotePort = info.SourcePort
		}
	}
	s.logNewConnection()

	// 握手：读 1 字节协议版本。
	_ = s.conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	ver, err := r.ReadByte()
	if err != nil {
		if lg := s.state.Logger; lg != nil && lg.DebugEnabled() {
			lg.Debug(fmt.Sprintf("连接ID：%s，握手读取失败：%v", s.id, err))
		}
		return
	}
	if ver != protocolVersion {
		if lg := s.state.Logger; lg != nil && lg.DebugEnabled() {
			lg.Debug(fmt.Sprintf("连接ID：%s，握手版本不符：期望 %d 实际 %d", s.id, protocolVersion, ver))
		}
		return // 版本不符：直接断开（不触发认证）
	}

	// buf 预分配 readChunk*2 容量，避免从 nil 起步多次扩容；
	// 处理完帧后若 cap 远大于 len 会缩容，防止大缓冲长期驻留。
	buf := make([]byte, 0, readChunk*2)
	tmp := make([]byte, readChunk)
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(heartbeatTimeout))
		n, err := r.Read(tmp)
		if err != nil {
			if lg := s.state.Logger; lg != nil {
				if errors.Is(err, io.EOF) {
					if lg.DebugEnabled() {
						lg.Debug(fmt.Sprintf("连接ID：%s，读循环结束（EOF）", s.id))
					}
				} else {
					lg.Warn(fmt.Sprintf("连接ID：%s，读循环 Read 失败：%v", s.id, err))
				}
			}
			return
		}
		buf = append(buf, tmp[:n]...)
		for {
			res := protocol.TryDecodeFrame(buf, maxFrameSize)
			if res.Kind == protocol.FrameNeedMore {
				break
			}
			if res.Kind == protocol.FrameError {
				if lg := s.state.Logger; lg != nil && lg.DebugEnabled() {
					lg.Debug(fmt.Sprintf("连接ID：%s，帧解码错误，断开", s.id))
				}
				return
			}
			cmd, derr := protocol.DecodePacket(res.Payload, protocol.DecodeClientCommand)
			// Remaining 是 buf 的子切片；拷到 buf 头部以复用底层数组，避免每次重新分配。
			remaining := len(res.Remaining)
			copy(buf, res.Remaining)
			buf = buf[:remaining]
			if derr != nil {
				if lg := s.state.Logger; lg != nil && lg.DebugEnabled() {
					lg.Debug(fmt.Sprintf("连接ID：%s，包解码错误：%v", s.id, derr))
				}
				return
			}
			s.onCommand(cmd)
		}
		// 缩容：buf 曾因大帧膨胀（cap > 32KB）但当前残留很少（< 8KB）时，
		// 重新分配小切片释放大块内存，避免每个 session 长期持有 4MB 缓冲。
		if cap(buf) > readChunk*2 && len(buf) < readChunk/2 {
			newBuf := make([]byte, len(buf), readChunk*2)
			copy(newBuf, buf)
			buf = newBuf
		}
	}
}

// isRoomOnlyCmd 判断命令是否仅需房间级锁（不需要全局 state.Mu）。
// Touches/Judges/CmdPlayed 是 Playing 阶段高频命令，无房间间依赖，可用分段锁并行。
// CmdPlayed 触发的 DisbandRoom（delete state.Rooms）由 handlePlayed 异步执行，
// 不在 room.Mu 临界区内同步获取 state.Mu，避免 lock ordering inversion。
func isRoomOnlyCmd(cmd protocol.ClientCommand) bool {
	switch cmd.(type) {
	case protocol.CmdTouches, protocol.CmdJudges, protocol.CmdPlayed:
		return true
	}
	return false
}

func (s *Session) onCommand(cmd protocol.ClientCommand) {
	if _, ok := cmd.(protocol.CmdPing); ok {
		s.TrySend(protocol.SrvPong{})
		return
	}
	if s.user == nil {
		if auth, ok := cmd.(protocol.CmdAuthenticate); ok {
			s.handleAuthenticate(auth.Token)
		}
		return // 认证前忽略其他命令
	}
	// 命令级限流：操作桶限制离散操作（聊天/房间/API）≤2 次/秒，总包桶限制所有命令包
	// （含 Touches/Judges）≤15 个/秒。可由 COMMAND_RATE_LIMIT=false 关闭（内网/比赛）。
	if s.state.Config.EffectiveCommandRateLimit() {
		if cat := categorize(cmd); !s.rl.allow(cat, time.Now()) {
			if lg := s.state.Logger; lg != nil && lg.DebugEnabled() {
				lg.Debug(fmt.Sprintf("连接ID：%s，用户“%s”触发限流：命令=%T，分类=%v", s.id, s.user.Name, cmd, cat))
			}
			if resp, ok := rateLimitedResponse(s.user.Lang, cmd); ok {
				s.TrySend(resp)
			}
			return
		}
	}
	// 已认证：持锁调度命令。
	// Touches/Judges/Played 仅持 room.Mu（分段锁，房间间并行），其余命令持 state.Mu（全局串行）。
	var resp protocol.ServerCommand
	var has bool
	if isRoomOnlyCmd(cmd) {
		room := s.user.Room
		if room != nil {
			room.Mu.Lock()
			resp, has = s.hub.ProcessClientCommand(s.user, cmd)
			room.Mu.Unlock()
		} else {
			// room 为空（房间已解散/用户已离开）时改用 state.Mu 处理，确保给客户端返回响应。
			s.state.Mu.Lock()
			resp, has = s.hub.ProcessClientCommand(s.user, cmd)
			s.state.Mu.Unlock()
		}
	} else {
		s.state.Mu.Lock()
		resp, has = s.hub.ProcessClientCommand(s.user, cmd)
		s.state.Mu.Unlock()
	}
	if has {
		s.TrySend(resp)
	}
}

func (s *Session) handleAuthenticate(token string) {
	s.logAuthStart(token)
	if len(token) > 32 {
		s.failAuth("auth-invalid-token")
		return
	}
	// Phira HTTP 认证：阻塞调用，不持锁。Hub 内部派生 ctx 控制超时与关闭取消。
	info, err := s.hub.FetchUserInfo(token)
	if err != nil {
		s.logAuthFetchError(err)
		s.failAuth(err.Error())
		return
	}

	// 两阶段认证：降低 state.Mu 持有时间。
	// 阶段 1 — 快速检查 + 判断新用户还是重连，尽量减少锁内操作。
	s.state.Mu.Lock()

	// 维护模式：拒绝新连接，但放行已在线用户重连，让其回原房间完成对局。
	if s.state.Maintenance {
		if _, online := s.state.Users[info.ID]; !online {
			reason := "server-maintenance"
			if s.state.MaintenanceMessage != nil && *s.state.MaintenanceMessage != "" {
				reason = *s.state.MaintenanceMessage
			}
			s.state.Mu.Unlock()
			s.failAuth(reason)
			return
		}
	}

	var stale server.Session
	var user *server.User
	var roomState *protocol.ClientRoomState
	var restoreChartID *int32

	existing := s.state.Users[info.ID]
	if existing != nil {
		// ---- 重连路径：全程持锁（需读取/修改 Session、Room 等状态） ----
		if existing.Session() != nil && existing.Session() != server.Session(s) {
			stale = existing.Session()
		}
		existing.SetSession(s) // 先重绑到新会话——旧会话随后 Close 时 cleanup 会因此短路、保留房间
		user = existing
		s.user = user

		// 断线重连：构建客户端房间状态
		var room *server.Room
		var roomStateStr string
		if user.Room != nil {
			room = user.Room
			room.Mu.Lock()
			cs := room.ClientState(user, func(id int) *server.User { return s.state.Users[id] })
			roomStateStr = fmt.Sprintf("%T", room.State)
			// ProtocolHack：WaitForReady 态但已选谱 → 伪装为 SelectChart 让客户端先获知谱面 ID，
			// 随后 session 在延迟 20ms 后再切回 WaitingForReady。
			if _, wfr := room.State.(server.StateWaitForReady); wfr && room.Chart != nil {
				cs.State = s.hub.ClientRoomStateForJoin(room)
				cid := int32(room.Chart.ID)
				restoreChartID = &cid
			}
			room.Mu.Unlock()
			roomState = &cs
		}
		me := user.ToInfo()
		monitor := user.Monitor
		s.logReconnectDetected(user, room, roomStateStr)
		s.state.Mu.Unlock()

		// 踢旧会话（锁外）。此时 user.Session 已指向新会话，旧会话 cleanup 将短路，不会退房。
		if stale != nil {
			s.logReconnectStaleKicked(user, stale)
			stale.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{
				User:    0,
				Content: l10n.TL(s.state.ServerLang, "error-logged-in-elsewhere", nil),
			}})
			stale.Close()
		}

		s.TrySend(protocol.SrvAuthenticate{Result: protocol.Ok(protocol.AuthInfo{Me: me, Room: roomState})})

		// 重连进 WaitForReady：通过 ProtocolHack 把客户端状态修回。
		// 两次延迟：第一次让客户端把构造的 SelectChart 落地，第二次切回 WaitingForReady。
		if restoreChartID != nil && room != nil {
			ph := s.hub.NewProtocolHack()
			ph.FixClientRoomState(room, user)
			s.logReconnectRoomRestored(user, room, roomStateStr, true)
		} else if room != nil {
			s.logReconnectRoomRestored(user, room, roomStateStr, false)
		} else {
			s.logReconnectNoRoom(user)
		}

		s.logAuthSuccess(user, monitor)
		go s.sendWelcome(user)
		return
	}

	// ---- 新用户路径：快速解锁，将 NewUser 分配移出锁外 ----
	s.state.Mu.Unlock()

	user = server.NewUser(info.ID, info.Name, info.Language, s.state)
	user.SetSession(s)

	// 阶段 2 — 重新持锁完成注册（双检避免竞态）
	s.state.Mu.Lock()
	if existing := s.state.Users[info.ID]; existing != nil {
		// 极低概率的竞态：另一个连接在我们 unlock→relock 间注册了同 ID 用户。
		var stale server.Session
		if existing.Session() != nil && existing.Session() != server.Session(s) {
			stale = existing.Session()
		}
		s.state.Mu.Unlock()
		// 关键：丢弃的 user 仍持有 s 的引用（user.SetSession(s) 已建立反向指针），
		// 显式断开该引用避免 user 存活期间 s 不会 GC；user 之后会被 GC 回收。
		user.SetSession(nil)
		// 让 existing 接管此会话，丢弃我们刚创建的 user（未被注册，GC 回收）。
		existing.SetSession(s)
		s.user = existing
		// 踢旧会话（锁外）。
		if stale != nil {
			stale.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{
				User:    0,
				Content: l10n.TL(s.state.ServerLang, "error-logged-in-elsewhere", nil),
			}})
			stale.Close()
		}
		me := existing.ToInfo()
		monitor := existing.Monitor
		s.TrySend(protocol.SrvAuthenticate{Result: protocol.Ok(protocol.AuthInfo{Me: me, Room: nil})})
		s.logAuthSuccess(existing, monitor)
		go s.sendWelcome(existing)
		return
	}
	s.state.Users[info.ID] = user
	s.user = user
	me := user.ToInfo()
	monitor := user.Monitor
	s.state.Mu.Unlock()

	s.TrySend(protocol.SrvAuthenticate{Result: protocol.Ok(protocol.AuthInfo{Me: me, Room: nil})})

	s.logAuthSuccess(user, monitor)
	go s.sendWelcome(user)
}

// logAuthSuccess 记录认证成功：DEBUG 级 log-auth-ok 与 INFO 级 log-player-join（对齐原版）。
func (s *Session) logAuthSuccess(user *server.User, monitor bool) {
	lg := s.state.Logger
	if lg == nil {
		return
	}
	suffix := ""
	if monitor {
		suffix = l10n.TL(s.state.ServerLang, "label-monitor-suffix", nil)
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，“ %s ” %s 认证成功，协议版本：“%s”",
		s.id, user.Name, suffix, strconv.Itoa(protocolVersion)))
	lg.Info(l10n.TL(s.state.ServerLang, "log-player-join", map[string]string{
		"user": user.Name, "id": strconv.Itoa(user.ID), "monitorSuffix": suffix,
	}))
}

// sendWelcome 拉取一言（可选）并把欢迎系统聊天发给用户。
func (s *Session) sendWelcome(user *server.User) {
	var hk *server.Hitokoto
	if url := s.state.Config.EffectiveHitokotoAPIURL(); url != "" {
		hk = fetchHitokoto(url)
	}
	s.state.Mu.Lock()
	text := s.state.BuildWelcomeText(user, hk)
	sysID := s.state.SystemChatUserID()
	s.state.Mu.Unlock()
	user.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{User: sysID, Content: text}})
}

// fetchHitokoto 拉取一言；失败返回 nil（欢迎消息照常发，只是不带一言）。
func fetchHitokoto(url string) *server.Hitokoto {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil
	}
	var data struct {
		Hitokoto string `json:"hitokoto"`
		From     string `json:"from"`
		FromWho  string `json:"from_who"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return nil
	}
	quote := strings.TrimSpace(data.Hitokoto)
	if quote == "" {
		return nil
	}
	// 部分一言源把换行写成字面量 "\n"（反斜杠+n 两字符），JSON 解码不会还原；这里转成真实换行。
	quote = strings.ReplaceAll(quote, "\\r\\n", "\n")
	quote = strings.ReplaceAll(quote, "\\n", "\n")
	// 出处优先用 from_who（一言官方 API 的具体出处常在此字段），为空再回退 from（对齐原版）。
	from := strings.TrimSpace(data.FromWho)
	if from == "" {
		from = strings.TrimSpace(data.From)
	}
	return &server.Hitokoto{Quote: quote, From: from}
}

func (s *Session) failAuth(reasonKey string) {
	// reasonKey 可能是翻译键（auth-invalid-token / server-maintenance）或原始错误串（如 API 超时）；
	// TL 对非键原样返回，故可统一本地化。对齐原版：发本地化原因给客户端并记 WARN 日志。
	reason := l10n.TL(s.state.ServerLang, reasonKey, nil)
	if lg := s.state.Logger; lg != nil {
		lg.Warn(l10n.TL(s.state.ServerLang, "log-auth-failed", map[string]string{"id": s.id, "reason": reason}))
	}
	s.TrySend(protocol.SrvAuthenticate{Result: protocol.Errr[protocol.AuthInfo](reason)})
	s.Close()
}

// encodeServerFrame 把服务端命令编码为「长度前缀 + body」帧（复用对象池中的编码器）。
func encodeServerFrame(cmd protocol.ServerCommand) (frame []byte, err error) {
	w := frameWriterPool.Get().(*protocol.BinaryWriter)
	defer frameWriterPool.Put(w)
	defer func() {
		if rec := recover(); rec != nil {
			err = errEncode
		}
	}()
	w.Reset()
	protocol.EncodeServerCommand(w, cmd)
	fb := w.ToFrameBuffer()
	// fb 引用 w 的内部缓冲区；拷出后再归还，避免 writeLoop 使用时被覆写。
	frame = make([]byte, len(fb))
	copy(frame, fb)
	return frame, nil
}
