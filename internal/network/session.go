// Package network 实现 TCP 传输层：监听、连接握手、帧编解码、会话生命周期与命令循环。
// Session 实现 server.Session 接口，把解码后的命令喂给 server.Hub 驱动房间状态机。
package network

import (
	"bufio"
	"encoding/json"
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
	readChunk        = 4096
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
}

// 确保 Session 满足 server.Session。
var _ server.Session = (*Session)(nil)

func newSession(conn net.Conn, state *server.ServerState, hub *server.Hub) *Session {
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
	select {
	case s.sendCh <- frame:
	default:
		// 发送缓冲溢出：慢消费者，断开。必须异步关闭——TrySend 可能在持有
		// state.Mu 的广播路径中被调用，而 Close→cleanup 会再次抢锁，同 goroutine
		// 同步关闭将自死锁。go 出去等当前命令处理释放锁后再清理。
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
	if u == nil || u.Session != server.Session(s) {
		return // 已被顶号或未认证
	}
	u.SetSession(nil)
}

func (s *Session) cleanup() {
	s.state.Mu.Lock()
	u := s.user
	if u == nil || u.Session != server.Session(s) {
		s.state.Mu.Unlock()
		return // 已被顶号或未认证
	}
	u.SetSession(nil)

	// 被封禁用户或宽限为 0：不等待重连，立即移除。
	_, banned := s.state.BannedUsers[u.ID]
	grace := s.dangleGrace(u)
	if banned || grace <= 0 {
		// 对齐原版：封禁用户记 INFO 挂起日志；对局态宽限为 0 则记房间作用域 WARN（强制退出）。
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
	s.logLocalized("INFO", "log-user-dangle", map[string]string{"user": u.Name})
	token := u.MarkDangle()
	s.state.Mu.Unlock()
	time.AfterFunc(grace, func() { s.processDangle(u, token) })
}

// dangleGrace 返回该用户断线后的保留窗口：对局态用配置宽限（0=立即），否则非对局窗口。
func (s *Session) dangleGrace(u *server.User) time.Duration {
	if u.Room != nil {
		if _, playing := u.Room.State.(server.StatePlaying); playing {
			return time.Duration(s.state.Config.EffectivePlayingReconnectGrace()) * time.Second
		}
	}
	return time.Duration(dangleWindowNonPlaying.Load())
}

// removeUser 退房并移除用户（调用方须持 state.Mu）。
func (s *Session) removeUser(u *server.User) {
	if u.Room != nil {
		room := u.Room
		if room.OnUserLeave(s.hub.MakeRoomLifecycle(room), u) {
			delete(s.state.Rooms, room.ID)
		}
	}
	delete(s.state.Users, u.ID)
	s.state.CleanupUserData(u.ID)
}

// processDangle 在宽限窗到期后检查用户是否仍 dangling（未重连），是则移除。
func (s *Session) processDangle(u *server.User, token *server.DangleToken) {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	if !u.IsStillDangling(token) {
		return // 已重连（SetSession 清除了 token）
	}
	// 对齐原版：挂起超时移除时，若仍在房间内记房间作用域 WARN。
	if u.Room != nil {
		s.logLocalized("WARN", "log-user-dangle-timeout-remove", map[string]string{"user": u.Name, "room": string(u.Room.ID)})
	}
	s.removeUser(u)
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
	msg := l10n.TL(s.state.ServerLang, "log-new-connection", map[string]string{
		"id":     s.id,
		"remote": net.JoinHostPort(s.remoteIP, strconv.Itoa(s.remotePort)),
	})
	if cl, ok := lg.(connLogger); ok {
		cl.ConnectionLog(s.remoteIP, msg)
		return
	}
	lg.Debug(msg)
}

func (s *Session) writeLoop() {
	for {
		select {
		case <-s.done:
			return
		case frame := <-s.sendCh:
			if _, err := s.conn.Write(frame); err != nil {
				s.Close()
				return
			}
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
		return
	}
	if ver != protocolVersion {
		return // 版本不符：直接断开（不触发认证）
	}

	var buf []byte
	tmp := make([]byte, readChunk)
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(heartbeatTimeout))
		n, err := r.Read(tmp)
		if err != nil {
			return
		}
		buf = append(buf, tmp[:n]...)
		for {
			res := protocol.TryDecodeFrame(buf, maxFrameSize)
			if res.Kind == protocol.FrameNeedMore {
				break
			}
			if res.Kind == protocol.FrameError {
				return
			}
			cmd, derr := protocol.DecodePacket(res.Payload, protocol.DecodeClientCommand)
			// Remaining 是 buf 的子切片；拷到 buf 头部以复用底层数组，避免每次重新分配。
			remaining := len(res.Remaining)
			copy(buf, res.Remaining)
			buf = buf[:remaining]
			if derr != nil {
				return
			}
			s.onCommand(cmd)
		}
	}
}

// isRoomOnlyCmd 判断命令是否仅需房间级锁（不需要全局 state.Mu）。
// Touches/Judges 是 Playing 阶段高频命令，无房间间依赖，可用分段锁并行。
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
	// 命令级限流：放行心跳与实时数据（catNone），挡下异常高频的聊天/房间/上游 API 命令。
	// 可由 COMMAND_RATE_LIMIT=false 关闭（内网/比赛）。
	if s.state.Config.EffectiveCommandRateLimit() {
		if cat := categorize(cmd); !s.rl.allow(cat, time.Now()) {
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
	if len(token) > 32 {
		s.failAuth("auth-invalid-token")
		return
	}
	// Phira HTTP 认证：阻塞调用，不持锁。
	info, err := s.hub.Phira.FetchUserInfo(token)
	if err != nil {
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
		if existing.Session != nil && existing.Session != server.Session(s) {
			stale = existing.Session
		}
		existing.SetSession(s) // 先重绑到新会话——旧会话随后 Close 时 cleanup 会因此短路、保留房间
		user = existing
		s.user = user

		// 断线重连：构建客户端房间状态
		if user.Room != nil {
			cs := user.Room.ClientState(user, func(id int) *server.User { return s.state.Users[id] })
			if _, wfr := user.Room.State.(server.StateWaitForReady); wfr && user.Room.Chart != nil {
				cid := int32(user.Room.Chart.ID)
				cs.State = protocol.RoomStateSelectChart{ID: &cid}
				restoreChartID = &cid
			}
			roomState = &cs
		}
		me := user.ToInfo()
		monitor := user.Monitor
		s.state.Mu.Unlock()

		// 踢旧会话（锁外）。此时 user.Session 已指向新会话，旧会话 cleanup 将短路，不会退房。
		if stale != nil {
			stale.Close()
		}

		s.TrySend(protocol.SrvAuthenticate{Result: protocol.Ok(protocol.AuthInfo{Me: me, Room: roomState})})

		// 重连进 WaitForReady：延迟两步把客户端状态修回。
		if restoreChartID != nil {
			cid := *restoreChartID
			time.AfterFunc(20*time.Millisecond, func() {
				user.TrySend(protocol.SrvChangeState{State: protocol.RoomStateSelectChart{ID: &cid}})
				time.AfterFunc(20*time.Millisecond, func() {
					user.TrySend(protocol.SrvChangeState{State: protocol.RoomStateWaitingForReady{}})
				})
			})
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
		if existing.Session != nil && existing.Session != server.Session(s) {
			stale = existing.Session
		}
		s.state.Mu.Unlock()
		// 让 existing 接管此会话，丢弃我们刚创建的 user（未被注册，GC 回收）。
		existing.SetSession(s)
		s.user = existing
		// 踢旧会话（锁外）。
		if stale != nil {
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
	lg.Debug(l10n.TL(s.state.ServerLang, "log-auth-ok", map[string]string{
		"id": s.id, "user": user.Name, "monitorSuffix": suffix, "version": strconv.Itoa(protocolVersion),
	}))
	lg.Info(l10n.TL(s.state.ServerLang, "log-player-join", map[string]string{
		"user": user.Name, "id": strconv.Itoa(user.ID), "monitorSuffix": suffix,
	}))
}

// sendWelcome 拉取一言（可选）并把欢迎系统聊天发给用户。
func (s *Session) sendWelcome(user *server.User) {
	var hk *server.Hitokoto
	if url := s.state.Config.HitokotoAPIURL; url != nil && *url != "" {
		hk = fetchHitokoto(*url)
	}
	s.state.Mu.Lock()
	text := s.state.BuildWelcomeText(user, hk)
	s.state.Mu.Unlock()
	user.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{User: 0, Content: text}})
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
