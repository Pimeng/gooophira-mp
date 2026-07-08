package network

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/phira"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// fakePhira 是集成测试用的 Phira API 桩。token "aaaaaaaa" 固定映射到 Alice(100)，
// 其余 token 按内容派生不同用户 id（供并发测试用）。
type fakePhira struct{}

func (fakePhira) FetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
	if token == "aaaaaaaa" {
		return server.PhiraUserInfo{ID: 100, Name: "Alice", Language: "zh-CN"}, nil
	}
	h := 0
	for _, ch := range token {
		h = h*31 + int(ch)
	}
	if h < 0 {
		h = -h
	}
	return server.PhiraUserInfo{ID: 1000 + h%100000, Name: token, Language: "zh-CN"}, nil
}
func (fakePhira) FetchChart(ctx context.Context, id int) (config.Chart, error) {
	return config.Chart{ID: id, Name: "chart"}, nil
}
func (fakePhira) FetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	return config.RecordData{ID: id, Player: 100, Score: 1000000, Accuracy: 1, FullCombo: true}, nil
}

// testClient 是一个最小的 TCP 协议客户端（握手 + 帧编解码）。
type testClient struct {
	conn net.Conn
	buf  []byte
	t    *testing.T
}

func dial(t *testing.T, addr string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write([]byte{protocolVersion}); err != nil { // 握手：发版本字节
		t.Fatalf("handshake write: %v", err)
	}
	return &testClient{conn: conn, t: t}
}

func (c *testClient) send(cmd protocol.ClientCommand) {
	c.t.Helper()
	w := protocol.NewBinaryWriter()
	protocol.EncodeClientCommand(w, cmd)
	if _, err := c.conn.Write(protocol.FrameWithLengthPrefix(w.ToBuffer())); err != nil {
		c.t.Fatalf("send: %v", err)
	}
}

// next 读取下一条服务端命令（必要时阻塞读取更多数据）。
func (c *testClient) next() protocol.ServerCommand {
	c.t.Helper()
	for {
		res := protocol.TryDecodeFrame(c.buf, maxFrameSize)
		if res.Kind == protocol.FrameOK {
			cmd, err := protocol.DecodePacket(res.Payload, protocol.DecodeServerCommand)
			c.buf = append([]byte(nil), res.Remaining...)
			if err != nil {
				c.t.Fatalf("decode server command: %v", err)
			}
			return cmd
		}
		tmp := make([]byte, 4096)
		_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := c.conn.Read(tmp)
		if err != nil {
			c.t.Fatalf("read: %v", err)
		}
		c.buf = append(c.buf, tmp[:n]...)
	}
}

// expectAuthOK 读取直到认证响应，断言成功。
func (c *testClient) expectAuthOK() server.PhiraUserInfo {
	c.t.Helper()
	for {
		switch cmd := c.next().(type) {
		case protocol.SrvAuthenticate:
			if !cmd.Result.Ok {
				c.t.Fatalf("auth failed: %s", cmd.Result.Error)
			}
			return server.PhiraUserInfo{ID: int(cmd.Result.Value.Me.ID), Name: cmd.Result.Value.Me.Name}
		}
	}
}

func (c *testClient) close() { _ = c.conn.Close() }

// expectSystemChat 读取直到一条系统聊天（MsgChat user=0），返回内容。
func (c *testClient) expectSystemChat() string {
	c.t.Helper()
	for {
		if sm, ok := c.next().(protocol.SrvMessage); ok {
			if chat, ok := sm.Message.(protocol.MsgChat); ok && chat.User == 0 {
				return chat.Content
			}
		}
	}
}

func newTestServer(t *testing.T) (*Server, *server.ServerState) {
	t.Helper()
	rateLimitOff := false
	cfg := &config.ServerConfig{CommandRateLimit: &rateLimitOff}
	state := server.NewServerState(cfg, nil, "test", "", "")
	hub := server.NewHub(state, fakePhira{})
	srv, err := Listen("127.0.0.1:0", state, hub)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return srv, state
}

// newTestServerWithReplay 复刻 main.go 的录制器装配（启用回放 + OnEnterPlaying/OnGameEnd）。
func newTestServerWithReplay(t *testing.T) (*Server, *replay.Recorder) {
	t.Helper()
	dir := t.TempDir()
	enabled := true
	rateLimitOff := false
	cfg := &config.ServerConfig{ReplayEnabled: &enabled, ReplayBaseDir: &dir, CommandRateLimit: &rateLimitOff}
	state := server.NewServerState(cfg, nil, "test", "", "")
	hub := server.NewHub(state, fakePhira{})
	recorder := replay.NewRecorder(dir, nil)
	state.ReplayRecorder = recorder
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
	hub.OnGameEnd = func(room *server.Room) { go recorder.EndRoom(room.ID) }
	srv, err := Listen("127.0.0.1:0", state, hub)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return srv, recorder
}

// TestNetwork_ReplayRecording 端到端验证录制器：真实 TCP 上跑完单人一局并上传成绩后，
// 应落盘一份可解码的回放文件，且包含游戏中上报的触摸帧。
func TestNetwork_ReplayRecording(t *testing.T) {
	srv, recorder := newTestServerWithReplay(t)
	defer srv.Close()
	c := dial(t, srv.Addr().String())
	defer c.close()

	c.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	c.expectAuthOK()
	c.send(protocol.CmdCreateRoom{ID: "room1"})
	c.expectResultOK("CreateRoom")
	c.send(protocol.CmdSelectChart{ID: 7})
	c.expectResultOK("SelectChart")
	c.send(protocol.CmdRequestStart{}) // 单人 → 立即 Playing，触发 StartRoom
	c.expectResultOK("RequestStart")

	// 游玩中上报一帧触摸（应被录制）。
	c.send(protocol.CmdTouches{Frames: []protocol.TouchFrame{
		{Time: 1, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.5}}}},
	}})
	c.send(protocol.CmdPlayed{ID: 1}) // → 结算 → EndRoom（异步落盘）
	c.expectResultOK("Played")

	// 等待异步落盘完成。
	waitFor(t, func() bool { return len(recorder.ListRoomFiles("room1")) > 0 })
	files := recorder.ListRoomFiles("room1")
	raw, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatalf("read replay file: %v", err)
	}
	if len(raw) < 13 || string(raw[0:8]) != "PHIRAREC" {
		t.Fatalf("replay file missing PHIRAREC header (len=%d)", len(raw))
	}
}

// TestNetwork_LivePhiraEndToEnd 用真实 Phira API 做端到端验证：真 token 认证 → 建房 →
// 选真实谱面。默认跳过（需设 PHIRA_TEST_TOKEN），故不提交任何凭据。
//
// 运行：PHIRA_TEST_TOKEN=<token> go test ./internal/network/ -run LivePhira -v
func TestNetwork_LivePhiraEndToEnd(t *testing.T) {
	token := os.Getenv("PHIRA_TEST_TOKEN")
	if token == "" {
		t.Skip("set PHIRA_TEST_TOKEN to run the live Phira integration test")
	}
	hitokoto := "https://v1.hitokoto.cn/"
	state := server.NewServerState(&config.ServerConfig{HitokotoAPIURL: &hitokoto}, nil, "Phira MP", "", "")
	hub := server.NewHub(state, phira.NewClient(os.Getenv("PHIRA_API_ENDPOINT"))) // 空端点用默认 phira.5wyxi.com
	srv, err := Listen("127.0.0.1:0", state, hub)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()

	c := dial(t, srv.Addr().String())
	defer c.close()

	c.send(protocol.CmdAuthenticate{Token: token})
	me := c.expectAuthOK()
	t.Logf("authenticated via live Phira API: id=%d name=%q", me.ID, me.Name)
	if me.ID <= 0 {
		t.Fatalf("expected a valid user id, got %d", me.ID)
	}

	// 欢迎消息（含真实一言 + 房间列表），认证后异步推送。
	welcome := c.expectSystemChat()
	t.Logf("welcome message (live):\n%s", strings.TrimLeft(welcome, "\n"))

	c.send(protocol.CmdCreateRoom{ID: "live-room"})
	c.expectResultOK("CreateRoom")
	t.Log("room created")

	c.send(protocol.CmdSelectChart{ID: 61959}) // 真实谱面 g.r.i.s
	c.expectResultOK("SelectChart")
	t.Log("chart 61959 selected (fetched from live Phira API)")

	// 验证服务端确实拉到了谱面信息。
	state.Mu.Lock()
	chart := state.Rooms["live-room"].Chart
	state.Mu.Unlock()
	if chart == nil || chart.ID != 61959 {
		t.Fatalf("chart not set from live API: %+v", chart)
	}
	t.Logf("server stored chart: id=%d name=%q", chart.ID, chart.Name)
}

// TestNetwork_DangleReconnectKeepsRoom 验证断线宽限窗内同账号重连能拿回房间。
func TestNetwork_DangleReconnectKeepsRoom(t *testing.T) {
	old := time.Duration(dangleWindowNonPlaying.Load())
	dangleWindowNonPlaying.Store(int64(5 * time.Second)) // 足够长，确保重连发生在窗口内
	defer dangleWindowNonPlaying.Store(int64(old))

	srv, state := newTestServer(t)
	defer srv.Close()

	c1 := dial(t, srv.Addr().String())
	c1.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	c1.expectAuthOK()
	c1.send(protocol.CmdCreateRoom{ID: "room1"})
	c1.expectResultOK("CreateRoom")
	c1.close() // 断线（非顶号）

	// 等服务端处理断线并进入 dangling（房间应被保留，未回收）。
	waitFor(t, func() bool {
		state.Mu.Lock()
		defer state.Mu.Unlock()
		u := state.Users[100]
		return u != nil && u.Session() == nil // dangling
	})
	state.Mu.Lock()
	_, roomKept := state.Rooms["room1"]
	state.Mu.Unlock()
	if !roomKept {
		t.Fatal("room should be kept during dangle window")
	}

	// 同账号重连 → 认证响应应带回原房间状态。
	c2 := dial(t, srv.Addr().String())
	defer c2.close()
	c2.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	for {
		if sa, ok := c2.next().(protocol.SrvAuthenticate); ok {
			if !sa.Result.Ok {
				t.Fatalf("reconnect auth failed: %s", sa.Result.Error)
			}
			if sa.Result.Value.Room == nil || sa.Result.Value.Room.ID != "room1" {
				t.Fatalf("reconnect should restore room1, got %+v", sa.Result.Value.Room)
			}
			break
		}
	}
}

// TestNetwork_StaleDangleTimerCancelled 验证重连时 SetSession 取消旧 session 遗留的
// dangleTimer：断线 → 宽限窗内重连 → 等待宽限窗过期 → 用户应仍在线（旧 timer 被取消，
// 不会触发 processDangle 误移除）。对应日志中 stale timer 三次误触发的 bug。
func TestNetwork_StaleDangleTimerCancelled(t *testing.T) {
	// 用较短窗口让测试快速完成，但需足够长以区分「重连前断线」和「重连后宽限窗过期」。
	old := time.Duration(dangleWindowNonPlaying.Load())
	dangleWindowNonPlaying.Store(int64(200 * time.Millisecond))
	defer dangleWindowNonPlaying.Store(int64(old))

	srv, state := newTestServer(t)
	defer srv.Close()

	c1 := dial(t, srv.Addr().String())
	c1.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	c1.expectAuthOK()
	c1.send(protocol.CmdCreateRoom{ID: "room1"})
	c1.expectResultOK("CreateRoom")
	c1.close() // 断线 → dangle，200ms 后 timer 会触发

	// 等服务端进入 dangling。
	waitFor(t, func() bool {
		state.Mu.Lock()
		defer state.Mu.Unlock()
		u := state.Users[100]
		return u != nil && u.Session() == nil
	})

	// 宽限窗内重连。
	c2 := dial(t, srv.Addr().String())
	defer c2.close()
	c2.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	c2.expectAuthOK()

	// 等待宽限窗过期（500ms >> 200ms），旧 timer 应已被 SetSession 取消。
	time.Sleep(500 * time.Millisecond)

	// 用户应仍在线（绑定到 c2），未被旧 stale timer 移除。
	state.Mu.Lock()
	u := state.Users[100]
	online := u != nil && u.Session() != nil
	state.Mu.Unlock()
	if !online {
		t.Fatal("user should still be online after dangle window expired (stale timer should be cancelled)")
	}

	// 房间也应保留。
	state.Mu.Lock()
	_, roomKept := state.Rooms["room1"]
	state.Mu.Unlock()
	if !roomKept {
		t.Fatal("room should be kept after reconnect")
	}
}

// TestNetwork_RapidReconnectCycle 验证快速断线-重连循环中不会累积 stale timer：
// 多次断线-重连后，用户最终保持在线且无异常移除。
func TestNetwork_RapidReconnectCycle(t *testing.T) {
	old := time.Duration(dangleWindowNonPlaying.Load())
	dangleWindowNonPlaying.Store(int64(300 * time.Millisecond))
	defer dangleWindowNonPlaying.Store(int64(old))

	srv, state := newTestServer(t)
	defer srv.Close()

	// 3 轮断线-重连，每轮间隔 100ms（在 300ms 宽限窗内）。
	for i := 0; i < 3; i++ {
		c := dial(t, srv.Addr().String())
		c.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
		c.expectAuthOK()
		c.close() // 断线 → dangle

		// 等 dangling。
		waitFor(t, func() bool {
			state.Mu.Lock()
			defer state.Mu.Unlock()
			u := state.Users[100]
			return u != nil && u.Session() == nil
		})
	}

	// 第 4 次重连后保持在线。
	c4 := dial(t, srv.Addr().String())
	defer c4.close()
	c4.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	c4.expectAuthOK()

	// 等待所有旧宽限窗过期（3 次 × 300ms = 900ms，等 1.2s 确保全部过期）。
	time.Sleep(1200 * time.Millisecond)

	// 用户应仍在线。
	state.Mu.Lock()
	u := state.Users[100]
	online := u != nil && u.Session() != nil
	state.Mu.Unlock()
	if !online {
		t.Fatal("user should still be online after rapid reconnect cycle")
	}

	// 验证能正常通信（未被误移除）。
	c4.send(protocol.CmdPing{})
	for {
		if _, ok := c4.next().(protocol.SrvPong); ok {
			break
		}
	}
}

func TestNetwork_HandshakeRejectsBadVersion(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// 错误版本字节 → 服务端应断开
	if _, err := conn.Write([]byte{2}); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 1)
	if _, err := conn.Read(b); err == nil {
		t.Fatal("server should disconnect on bad protocol version")
	}
}

func TestNetwork_AuthThenPing(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	c := dial(t, srv.Addr().String())
	defer c.close()

	c.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	me := c.expectAuthOK()
	if me.ID != 100 || me.Name != "Alice" {
		t.Fatalf("unexpected auth user: %+v", me)
	}

	c.send(protocol.CmdPing{})
	for {
		if _, ok := c.next().(protocol.SrvPong); ok {
			break
		}
	}
}

func TestNetwork_FullFlowOverTCP(t *testing.T) {
	srv, state := newTestServer(t)
	defer srv.Close()
	c := dial(t, srv.Addr().String())
	defer c.close()

	c.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	c.expectAuthOK()

	// 建房
	c.send(protocol.CmdCreateRoom{ID: "room1"})
	c.expectResultOK("CreateRoom")

	// 选谱
	c.send(protocol.CmdSelectChart{ID: 7})
	c.expectResultOK("SelectChart")

	// 请求开始（单人房 → 立即进入 Playing）
	c.send(protocol.CmdRequestStart{})
	c.expectResultOK("RequestStart")

	// 给服务端一点时间推进状态机
	waitFor(t, func() bool {
		state.Mu.Lock()
		room := state.Rooms["room1"]
		state.Mu.Unlock()
		if room == nil {
			return false
		}
		room.Mu.Lock()
		_, playing := room.State.(server.StatePlaying)
		room.Mu.Unlock()
		return playing
	})

	// 交成绩 → 结算回到 SelectChart
	c.send(protocol.CmdPlayed{ID: 1})
	c.expectResultOK("Played")
	waitFor(t, func() bool {
		state.Mu.Lock()
		room := state.Rooms["room1"]
		state.Mu.Unlock()
		if room == nil {
			return false
		}
		room.Mu.Lock()
		_, sel := room.State.(server.StateSelectChart)
		room.Mu.Unlock()
		return sel
	})
}

// TestNetwork_DuplicateOnlineKicksOld 对应 TS「同一玩家重复在线时新连接顶掉旧连接」。
func TestNetwork_DuplicateOnlineKicksOld(t *testing.T) {
	srv, state := newTestServer(t)
	defer srv.Close()

	c1 := dial(t, srv.Addr().String())
	defer c1.close()
	c1.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"})
	c1.expectAuthOK()

	c2 := dial(t, srv.Addr().String())
	defer c2.close()
	c2.send(protocol.CmdAuthenticate{Token: "aaaaaaaa"}) // 同一 token/用户
	c2.expectAuthOK()

	// 新连接 c2 可正常工作。
	c2.send(protocol.CmdPing{})
	for {
		if _, ok := c2.next().(protocol.SrvPong); ok {
			break
		}
	}

	// 旧连接 c1 应被服务端断开。读循环先消费可能缓冲的欢迎消息字节，再命中连接关闭错误。
	_ = c1.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	kicked := false
	for range 100 {
		if _, err := c1.conn.Read(buf); err != nil {
			kicked = true
			break
		}
	}
	if !kicked {
		t.Fatal("old connection should be kicked when same user reconnects")
	}

	// 用户仍在线（绑定到 c2），未被误删。
	state.Mu.Lock()
	_, online := state.Users[100]
	state.Mu.Unlock()
	if !online {
		t.Fatal("user should remain online bound to the new session")
	}
}

// TestNetwork_ConcurrentClients 并发连接多个客户端，各自认证并建房，验证全局锁下
// 对共享状态（rooms/users map）的并发改动无死锁/无 panic（race 检测器不可用时的替代验证）。
func TestNetwork_ConcurrentClients(t *testing.T) {
	// 调短 dangle 窗口，使断开后房间快速回收（默认 10s）。
	old := time.Duration(dangleWindowNonPlaying.Load())
	dangleWindowNonPlaying.Store(int64(50 * time.Millisecond))
	defer dangleWindowNonPlaying.Store(int64(old))

	srv, state := newTestServer(t)
	defer srv.Close()

	const n = 12
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		clients []*testClient
	)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := dial(t, srv.Addr().String())
			c.send(protocol.CmdAuthenticate{Token: fmt.Sprintf("client-%d", i)})
			c.expectAuthOK()
			c.send(protocol.CmdCreateRoom{ID: protocol.RoomID(fmt.Sprintf("room%d", i))})
			c.expectResultOK("CreateRoom")
			mu.Lock()
			clients = append(clients, c)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// 连接仍在线时断言：12 个房间应都已并发创建成功。
	state.Mu.Lock()
	got := len(state.Rooms)
	state.Mu.Unlock()
	if got != n {
		t.Fatalf("expected %d rooms created concurrently, got %d", n, got)
	}

	// 全部断开后，房间应随单人离开被并发回收，最终清空。
	for _, c := range clients {
		c.close()
	}
	waitFor(t, func() bool {
		state.Mu.Lock()
		defer state.Mu.Unlock()
		return len(state.Rooms) == 0
	})
}

// expectResultOK 读取直到某个携带 Unit 结果的响应命令并断言成功。
func (c *testClient) expectResultOK(name string) {
	c.t.Helper()
	for {
		switch cmd := c.next().(type) {
		case protocol.SrvCreateRoom:
			assertResult(c.t, name, "CreateRoom", cmd.Result.Ok, cmd.Result.Error)
			return
		case protocol.SrvSelectChart:
			assertResult(c.t, name, "SelectChart", cmd.Result.Ok, cmd.Result.Error)
			return
		case protocol.SrvRequestStart:
			assertResult(c.t, name, "RequestStart", cmd.Result.Ok, cmd.Result.Error)
			return
		case protocol.SrvPlayed:
			assertResult(c.t, name, "Played", cmd.Result.Ok, cmd.Result.Error)
			return
		default:
			// 跳过广播消息（SrvMessage / SrvChangeState 等）
		}
	}
}

func assertResult(t *testing.T, want, got string, ok bool, errMsg string) {
	t.Helper()
	if want != got {
		t.Fatalf("expected %s result, got %s", want, got)
	}
	if !ok {
		t.Fatalf("%s failed: %s", got, errMsg)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
