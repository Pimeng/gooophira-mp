// Package network 实现 TCP 传输层：监听、连接握手、帧编解码、会话生命周期与命令循环。
// Session 实现 server.Session 接口，把解码后的命令喂给 server.Hub 驱动房间状态机。
//
// session.go 集中放会话的「骨架」：常量、状态字段、构造、发送原语、关闭路径与写循环。
// 重大功能按职责拆分到同包下其它文件，避免单文件过长难读：
//   - session.go          骨架 / 写循环
//   - session_read.go      读循环 / 命令派发 / 新连接日志
//   - session_auth.go      Phira 鉴权 / 重连恢复 / 欢迎语
//   - session_dangle.go    断线挂起 / 重连宽限 / 用户移除
//   - session_encode.go    服务端命令帧编码 / 对象池
package network

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
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
	// s.user 由 readLoop goroutine 在 handleAuthenticate 中写（持 state.Mu），
	// 此处从 Server.Close goroutine 调用，须持 state.Mu 同步避免 data race。
	s.state.Mu.Lock()
	u := s.user
	s.state.Mu.Unlock()
	if u != nil {
		u.StopDangleTimer()
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

// run 启动写循环并运行读循环（阻塞至连接结束）。
func (s *Session) run() {
	go s.writeLoop()
	s.readLoop()
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
