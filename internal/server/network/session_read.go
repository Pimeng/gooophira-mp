// session_read.go 把「读循环、命令派发、新连接日志」从 session.go 拆出。
// 读循环负责 PROXY 解析、握手版本号、帧切分与心跳超时；命令派发按仅房间命令 / 全局分段锁策略
// 调度 hub.ProcessClientCommand，高频 hot path（Touches/Judges/Played）仅持 room.Mu。
package network

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

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
