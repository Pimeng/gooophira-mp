// Command realbench 是 Phira 多人游戏服务器的真实 TCP 压测/负载测试工具。
//
// 与 cmd/bench 不同，本工具启动真实 TCP 服务器并使用真实 TCP 连接（完整协议握手、
// 帧编解码、I/O 读写），模拟真实客户端行为进行压测。同时通过虚拟回环地址绑定
// （127.0.0.2, 127.0.0.3, ...）规避客户端源端口耗尽问题。
//
// 使用方式:
//
//	# 运行默认场景（50 客户端，5 房间，30 秒）
//	go run ./cmd/realbench/
//
//	# 高压测试 + 所有 pprof 分析
//	go run ./cmd/realbench/ -clients=500 -rooms=20 -duration=60s -profile=all
//
//	# 连接风暴场景 + CPU prof
//	go run ./cmd/realbench/ -scenario=connection-storm -clients=2000 -profile=cpu
//
//	# JSON 输出 + 自定义 prof 目录 + 自定义端口
//	go run ./cmd/realbench/ -json -profile-dir=./tmp/profiles/ -port=12346
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/network"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// ---------- 命令行参数 ----------

type benchConfig struct {
	Clients     int           `json:"clients"`
	Rooms       int           `json:"rooms"`
	Duration    time.Duration `json:"duration"`
	Scenario    string        `json:"scenario"`
	Profile     string        `json:"profile"`
	ProfileDir  string        `json:"profile_dir"`
	JSONOut     bool          `json:"json_out"`
	Verbose     bool          `json:"verbose"`
	Port        int           `json:"port"`
	VirtualIPs  int           `json:"virtual_ips"`
	Concurrency int           `json:"concurrency"`
}

// ptr64 把 float64 字面量转为指针，便于填充 RecordData.Std / StdScore 之类的 *float64 字段。
func ptr64(v float64) *float64 { return &v }

func parseFlags() benchConfig {
	var (
		clients     = flag.Int("clients", 50, "并发客户端数")
		rooms       = flag.Int("rooms", 5, "同时活跃房间数")
		duration    = flag.Duration("duration", 30*time.Second, "测试持续时间 (如 30s, 1m)")
		scenario    = flag.String("scenario", "room-cycle", "测试场景: room-cycle, gameplay, connection-storm, steady-state")
		profile     = flag.String("profile", "", "pprof 分析类型: cpu, mem, goroutine, mutex, block, all")
		profileDir  = flag.String("profile-dir", "./tmp/profiles", "pprof 文件输出目录")
		jsonOut     = flag.Bool("json", false, "输出 JSON 格式结果")
		verbose     = flag.Bool("v", false, "详细输出")
		port        = flag.Int("port", 0, "服务端监听端口 (0=自动分配)")
		virtualIPs  = flag.Int("virtual-ips", 100, "虚拟回环 IP 数量 (避免源端口耗尽)")
		concurrency = flag.Int("concurrency", 200, "并发建连/命令的并发度上限")
	)
	flag.Parse()
	return benchConfig{
		Clients:     *clients,
		Rooms:       *rooms,
		Duration:    *duration,
		Scenario:    *scenario,
		Profile:     *profile,
		ProfileDir:  *profileDir,
		JSONOut:     *jsonOut,
		Verbose:     *verbose,
		Port:        *port,
		VirtualIPs:  *virtualIPs,
		Concurrency: *concurrency,
	}
}

// ---------- 结果指标（与 bench 共用结构） ----------

type latencyStats struct {
	Count   int           `json:"count"`
	Min     time.Duration `json:"min"`
	Max     time.Duration `json:"max"`
	Mean    time.Duration `json:"mean"`
	P50     time.Duration `json:"p50"`
	P90     time.Duration `json:"p90"`
	P99     time.Duration `json:"p99"`
	StdDev  time.Duration `json:"stddev"`
	Zero    int64         `json:"zero_count"`
	Buckets []histoBucket `json:"buckets"`
}

type histoBucket struct {
	Label string  `json:"label"`
	Count int64   `json:"count"`
	Pct   float64 `json:"pct"`
}

type scenarioResult struct {
	Name            string        `json:"name"`
	Duration        time.Duration `json:"duration"`
	Clients         int           `json:"clients"`
	Rooms           int           `json:"rooms"`
	CyclesCompleted int64         `json:"cycles_completed"`
	CommandsSent    int64         `json:"commands_sent"`
	CommandsPerSec  float64       `json:"commands_per_sec"`
	CyclesPerSec    float64       `json:"cycles_per_sec"`
	ConnectLatency  latencyStats  `json:"connect_latency,omitempty"`
	AuthLatency     latencyStats  `json:"auth_latency,omitempty"`
	CycleLatency    latencyStats  `json:"cycle_latency"`
	Errors          int64         `json:"errors"`
	ErrorSamples    []string      `json:"error_samples,omitempty"`
	PeakGoroutines  int           `json:"peak_goroutines"`
	PeakHeapMB      float64       `json:"peak_heap_mb"`
	FinalAllocMB    float64       `json:"final_alloc_mb"`
	NumGC           uint32        `json:"num_gc"`
}

type benchReport struct {
	Title     string           `json:"title"`
	Timestamp int64            `json:"timestamp"`
	Config    benchConfig      `json:"config"`
	Results   []scenarioResult `json:"results"`
}

// ---------- pprof 辅助 ----------

type profiler struct {
	dir            string
	cpuFile        *os.File
	mutexProfiling bool
	blockProfiling bool
}

func startProfiler(cfg benchConfig) (*profiler, error) {
	p := &profiler{dir: cfg.ProfileDir}
	if err := os.MkdirAll(p.dir, 0755); err != nil {
		return nil, fmt.Errorf("创建 profile 目录: %w", err)
	}

	switch cfg.Profile {
	case "cpu", "all":
		f, err := os.Create(p.dir + "/cpu.pprof")
		if err != nil {
			return nil, err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return nil, err
		}
		p.cpuFile = f
	}

	p.mutexProfiling = cfg.Profile == "mutex" || cfg.Profile == "all"
	p.blockProfiling = cfg.Profile == "block" || cfg.Profile == "all"

	if p.mutexProfiling {
		runtime.SetMutexProfileFraction(1)
	}
	if p.blockProfiling {
		runtime.SetBlockProfileRate(1)
	}

	return p, nil
}

func (p *profiler) stop() {
	if p.cpuFile != nil {
		pprof.StopCPUProfile()
		p.cpuFile.Close()
	}
}

func (p *profiler) writeProfiles(dir string) {
	writeProfile := func(name, path string) {
		f, err := os.Create(dir + "/" + path)
		if err != nil {
			return
		}
		defer f.Close()
		_ = pprof.Lookup(name).WriteTo(f, 0)
	}

	writeProfile("goroutine", "goroutine.pprof")
	writeProfile("heap", "heap.pprof")

	if p.mutexProfiling {
		writeProfile("mutex", "mutex.pprof")
	}
	if p.blockProfiling {
		writeProfile("block", "block.pprof")
	}
}

// ---------- 指标收集 ----------

type metricsCollector struct {
	mu              sync.Mutex
	cycleDurs       []time.Duration
	connectDurs     []time.Duration
	authDurs        []time.Duration
	commandsSent    int64
	cyclesCompleted int64
	errors          int64
	errMsgs         []string // first 50 unique error messages
	peakGoroutines  int
	maxHeap         uint64
}

func (mc *metricsCollector) recordConnect(d time.Duration) {
	mc.mu.Lock()
	mc.connectDurs = append(mc.connectDurs, d)
	mc.mu.Unlock()
}

func (mc *metricsCollector) recordAuth(d time.Duration) {
	mc.mu.Lock()
	mc.authDurs = append(mc.authDurs, d)
	mc.mu.Unlock()
}

func (mc *metricsCollector) recordCycle(d time.Duration) {
	mc.mu.Lock()
	mc.cycleDurs = append(mc.cycleDurs, d)
	mc.mu.Unlock()
}

func (mc *metricsCollector) addCommands(n int64) {
	atomic.AddInt64(&mc.commandsSent, n)
}

func (mc *metricsCollector) addCycle() {
	atomic.AddInt64(&mc.cyclesCompleted, 1)
}

func (mc *metricsCollector) addError(err error) {
	atomic.AddInt64(&mc.errors, 1)
	if err != nil {
		mc.mu.Lock()
		msg := err.Error()
		// 记录前 50 条不重复错误
		if len(mc.errMsgs) < 50 {
			found := false
			for _, m := range mc.errMsgs {
				if m == msg {
					found = true
					break
				}
			}
			if !found {
				mc.errMsgs = append(mc.errMsgs, msg)
			}
		}
		mc.mu.Unlock()
	}
}

func (mc *metricsCollector) sample() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.HeapAlloc > mc.maxHeap {
		mc.maxHeap = m.HeapAlloc
	}
	if g := runtime.NumGoroutine(); g > mc.peakGoroutines {
		mc.peakGoroutines = g
	}
}

func computeLatencyStats(durs []time.Duration) latencyStats {
	if len(durs) == 0 {
		return latencyStats{}
	}
	sorted := make([]time.Duration, len(durs))
	copy(sorted, durs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum int64
	for _, d := range sorted {
		sum += int64(d)
	}
	n := len(sorted)
	mean := time.Duration(sum / int64(n))

	var variance float64
	for _, d := range sorted {
		diff := float64(d - mean)
		variance += diff * diff
	}
	variance /= float64(n)
	stdDev := time.Duration(math.Sqrt(variance))

	type edge struct {
		hi    time.Duration
		label string
	}
	edges := []edge{
		{0, "0ns"},
		{100 * time.Nanosecond, "1-100ns"},
		{500 * time.Nanosecond, "101-500ns"},
		{1 * time.Microsecond, "501ns-1µs"},
		{10 * time.Microsecond, "1-10µs"},
		{100 * time.Microsecond, "10-100µs"},
		{1 * time.Millisecond, "100µs-1ms"},
		{10 * time.Millisecond, "1-10ms"},
		{100 * time.Millisecond, "10-100ms"},
		{time.Second, "100ms-1s"},
		{time.Hour, ">1s"},
	}

	buckets := make([]histoBucket, len(edges))
	var zero int64
	var idx int
	total := float64(n)
	for i, e := range edges {
		count := int64(0)
		for idx < n && sorted[idx] <= e.hi {
			count++
			idx++
		}
		if i == 0 {
			zero = count
		}
		buckets[i] = histoBucket{Label: e.label, Count: count, Pct: float64(count) / total * 100}
	}

	return latencyStats{
		Count:   n,
		Min:     sorted[0],
		Max:     sorted[n-1],
		Mean:    mean,
		P50:     sorted[int(float64(n-1)*0.50)],
		P90:     sorted[int(float64(n-1)*0.90)],
		P99:     sorted[int(float64(n-1)*0.99)],
		StdDev:  stdDev,
		Zero:    zero,
		Buckets: buckets,
	}
}

func formatDur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return d.String()
	case d >= time.Millisecond:
		return fmt.Sprintf("%.3fms", float64(d)/float64(time.Millisecond))
	case d >= time.Microsecond:
		return fmt.Sprintf("%.1fµs", float64(d)/float64(time.Microsecond))
	default:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
}

// ---------- Mock Phira API ----------

// clientFrameWriterPool 是客户端二进制帧写入器的对象池（预留 5 字节 LEB128(u32) 头部）。
var clientFrameWriterPool = &sync.Pool{
	New: func() any { return protocol.NewFrameWriter(5) },
}

// mockPhira 实现 server.PhiraAPI，将 token "bench-N" 映射到用户 ID=N。
type mockPhira struct{}

func (m *mockPhira) FetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
	// token 格式: "bench-N" → 用户 ID=N
	id := 0
	if strings.HasPrefix(token, "bench-") {
		id, _ = strconv.Atoi(strings.TrimPrefix(token, "bench-"))
	}
	return server.PhiraUserInfo{
		ID:       id,
		Name:     fmt.Sprintf("player-%d", id),
		Language: "zh-CN",
	}, nil
}

func (m *mockPhira) FetchChart(ctx context.Context, id int) (config.Chart, error) {
	return config.Chart{ID: id, Name: fmt.Sprintf("chart-%d", id)}, nil
}

func (m *mockPhira) FetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	return config.RecordData{ID: id, Player: id, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}, nil
}

// ---------- 虚拟 IP 池 ----------

// vipPool 管理虚拟回环地址池，分配给每个 TCP 客户端避免源端口耗尽。
// 127.0.0.0/8 有约 16M 个地址可用。
type vipPool struct {
	base   net.IP // 127.0.0.0
	cursor atomic.Int32
	size   int
}

func newVIPPool(count int) *vipPool {
	return &vipPool{
		base: net.ParseIP("127.0.0.0"),
		size: count,
	}
}

// next 返回下一个虚拟 IP，循环使用。
func (p *vipPool) next() net.IP {
	idx := int(p.cursor.Add(1)) % p.size
	ip := make(net.IP, 4)
	copy(ip, p.base.To4())
	// 从 127.0.0.1 开始（127.0.0.0 是网络地址）
	host := 1 + idx
	ip[2] = byte(host >> 8)
	ip[3] = byte(host & 0xFF)
	return ip
}

// emptyPayload 是 readFrame 返回给调用方的占位切片（调用方仅检查错误，不读内容）。
const (
	// connectRetryMax 是单客户端连接+认证的最大重试次数（应对 TCP 重建突发拥塞）。
	connectRetryMax = 5
	// connectJitterMax 是建连前随机延迟上限，分散 TCP SYN 冲击避免 OS backlog 溢出。
	connectJitterMax = 50 * time.Millisecond
	// connReadTimeout 是客户端读超时，略大于服务器心跳断连阈值（10s）以防止 goroutine 泄露。
	connReadTimeout = 15 * time.Second
)

// retryableConnect 带重试与随机抖动的连接+认证，返回建立成功的客户端及测量时间。
// 重试使用指数级抖动以避免再次冲击 OS TCP backlog。
func retryableConnect(id int, addr string, vip net.IP, verbose bool) (*tcpClient, time.Duration, time.Duration, error) {
	for attempt := 0; attempt < connectRetryMax; attempt++ {
		// 指数级随机抖动：第 N 次重试的上限为 50ms × 2^N，将重试在更宽时间窗摊开。
		maxJitter := connectJitterMax << max(attempt-1, 0) // 50ms, 100ms, 200ms
		jitter := time.Duration(rand.Int63n(int64(maxJitter)))
		if jitter > 0 {
			time.Sleep(jitter)
		}

		cli := newTCPClient(id, addr, vip)

		t0 := time.Now()
		if err := cli.connect(); err != nil {
			if verbose && attempt < connectRetryMax-1 {
				fmt.Fprintf(os.Stderr, "  [RETRY] client %d connect attempt %d failed: %v\n", id, attempt+1, err)
			}
			continue
		}
		connectDur := time.Since(t0)

		authDur, err := cli.authenticate()
		if err != nil {
			cli.close()
			if verbose && attempt < connectRetryMax-1 {
				fmt.Fprintf(os.Stderr, "  [RETRY] client %d auth attempt %d failed: %v\n", id, attempt+1, err)
			}
			continue
		}

		return cli, connectDur, authDur, nil
	}
	return nil, 0, 0, fmt.Errorf("connect+auth failed after %d attempts", connectRetryMax)
}

var emptyPayload = []byte{}

// ---------- TCP 客户端 ----------

// tcpClient 是一个真实 TCP 客户端，管理一个到服务器的连接。
type tcpClient struct {
	id      int
	addr    string // 服务器地址
	vip     net.IP // 绑定的虚拟 IP
	conn    net.Conn
	reader  *bufio.Reader
	buf     []byte
	mu      sync.Mutex
	authed  bool
	dead    bool
	recvBuf [4096]byte
}

// newTCPClient 用虚拟 IP 建立到服务器的 TCP 连接。
func newTCPClient(id int, addr string, vip net.IP) *tcpClient {
	return &tcpClient{
		id:   id,
		addr: addr,
		vip:  vip,
		buf:  make([]byte, 0, 4096),
	}
}

// connect 建立 TCP 连接并发送协议版本字节。
func (c *tcpClient) connect() error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if c.vip != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: c.vip, Port: 0}
	}
	conn, err := dialer.Dial("tcp", c.addr)
	if err != nil {
		return err
	}
	c.conn = conn
	c.reader = bufio.NewReaderSize(conn, 4096)

	// 发送协议版本字节
	if _, err := conn.Write([]byte{1}); err != nil {
		conn.Close()
		return err
	}
	return nil
}

// authenticate 发送认证命令并等待响应。返回认证延迟。
func (c *tcpClient) authenticate() (time.Duration, error) {
	start := time.Now()

	token := fmt.Sprintf("bench-%d", c.id)
	cmd := protocol.CmdAuthenticate{Token: token}
	if err := c.sendCommand(cmd); err != nil {
		return 0, err
	}

	// 读取认证响应
	_, err := c.readFrame()
	if err != nil {
		return 0, err
	}
	c.authed = true
	return time.Since(start), nil
}

// sendCommand 编码并发送一个客户端命令（复用对象池中的编码器）。
func (c *tcpClient) sendCommand(cmd protocol.ClientCommand) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	w := clientFrameWriterPool.Get().(*protocol.BinaryWriter)
	defer clientFrameWriterPool.Put(w)
	w.Reset()
	protocol.EncodeClientCommand(w, cmd)
	fb := w.ToFrameBuffer()
	// fb 引用 w 的内部缓冲区；拷出后再归还。
	frame := make([]byte, len(fb))
	copy(frame, fb)

	_, err := c.conn.Write(frame)
	return err
}

// sendCommandRTT 发送命令并等待响应，返回往返延迟。
func (c *tcpClient) sendCommandRTT(cmd protocol.ClientCommand) (time.Duration, error) {
	start := time.Now()
	if err := c.sendCommand(cmd); err != nil {
		return 0, err
	}
	if _, err := c.readFrame(); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// sendCommandNoResp 发送命令并忽略响应（如 Touches、Judges 等不返回结果的命令）。
func (c *tcpClient) sendCommandNoResp(cmd protocol.ClientCommand) (time.Duration, error) {
	start := time.Now()
	if err := c.sendCommand(cmd); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// readFrame 从连接读取一帧（阻塞）。消耗但不返回 payload 内容（调用方仅检查错误）。
// 读超时由 connReadTimeout 控制，超时后返回 error 以便调用方重试。
func (c *tcpClient) readFrame() ([]byte, error) {
	c.buf = c.buf[:0]
	for {
		res := protocol.TryDecodeFrame(c.buf, 4*1024*1024)
		switch res.Kind {
		case protocol.FrameOK:
			// 复用 buf 的底层数组存储剩余数据，避免分配。
			remaining := len(res.Remaining)
			copy(c.buf, res.Remaining)
			c.buf = c.buf[:remaining]
			return emptyPayload, nil
		case protocol.FrameError:
			return nil, res.Err
		case protocol.FrameNeedMore:
			// 设置读超时防止 goroutine 永久阻塞
			_ = c.conn.SetReadDeadline(time.Now().Add(connReadTimeout))
			n, err := c.reader.Read(c.recvBuf[:])
			if err != nil {
				return nil, err
			}
			c.buf = append(c.buf, c.recvBuf[:n]...)
		}
	}
}

// close 关闭连接。
func (c *tcpClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil && !c.dead {
		c.dead = true
		c.conn.Close()
	}
}

// ---------- 测试场景 ----------

// runRoomCycleScenario 真实 TCP 房间生命周期测试。
func runRoomCycleScenario(bc benchConfig, mc *metricsCollector, addr string, vipPool *vipPool) scenarioResult {
	result := scenarioResult{
		Name:    "room-cycle",
		Clients: bc.Clients,
		Rooms:   bc.Rooms,
	}
	startTime := time.Now()

	// Phase 1: 创建所有客户端并连接 + 认证（带重试与随机抖动）
	clients := make([]*tcpClient, 0, bc.Clients)
	{
		var wg sync.WaitGroup
		var clientsMu sync.Mutex
		sem := make(chan struct{}, bc.Concurrency)

		for i := 0; i < bc.Clients; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				vip := vipPool.next()

				cli, connectDur, authDur, err := retryableConnect(id, addr, vip, bc.Verbose)
				if err != nil {
					mc.addError(err)
					if bc.Verbose {
						fmt.Fprintf(os.Stderr, "  [WARN] client %d failed: %v\n", id, err)
					}
					return
				}
				mc.recordConnect(connectDur)
				mc.recordAuth(authDur)
				mc.addCommands(2) // version byte + authenticate

				clientsMu.Lock()
				clients = append(clients, cli)
				clientsMu.Unlock()
			}(i + 1)
		}
		wg.Wait()
	}

	if bc.Verbose {
		fmt.Fprintf(os.Stderr, "  [INFO] 已连接/认证: %d 个客户端\n", len(clients))
	}

	// 重新分配客户端 ID 以匹配实际成功的连接
	for i, cli := range clients {
		cli.id = i + 1
	}

	// Phase 2: 分配客户端到房间，完成 create → join → select → ready → Playing
	// 这一步必须按房间串行以保证状态一致。
	roomIDs := make([]protocol.RoomID, bc.Rooms)
	for r := 0; r < bc.Rooms; r++ {
		roomIDs[r] = protocol.RoomID(fmt.Sprintf("rb-r%d", r))
		roomClients := assignTCPClients(clients, r, bc.Rooms, len(clients))
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]
		rid := roomIDs[r]

		// 建房间
		if _, err := host.sendCommandRTT(protocol.CmdCreateRoom{ID: rid}); err != nil {
			mc.addError(err)
			continue
		}
		// 其余玩家加入
		for _, cli := range roomClients[1:] {
			if _, err := cli.sendCommandRTT(protocol.CmdJoinRoom{ID: rid, Monitor: false}); err != nil {
				mc.addError(err)
			}
		}
		// 选谱
		if _, err := host.sendCommandRTT(protocol.CmdSelectChart{ID: 1}); err != nil {
			mc.addError(err)
		}
		// 请求开始
		if _, err := host.sendCommandRTT(protocol.CmdRequestStart{}); err != nil {
			mc.addError(err)
		}
		// 其余玩家就绪（host 自动就绪）
		for _, cli := range roomClients[1:] {
			if _, err := cli.sendCommandRTT(protocol.CmdReady{}); err != nil {
				mc.addError(err)
			}
		}
		mc.addCommands(int64(2 + 2*(len(roomClients)-1) + 2)) // create+join*(n-1)+select+start+ready*(n-1)
	}

	if bc.Verbose {
		fmt.Fprintf(os.Stderr, "  [INFO] Playing 阶段开始: %d 个客户端, %d 个房间\n", len(clients), bc.Rooms)
	}

	// Phase 3（并发）: 每客户端循环发送 Touches → Played
	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	for _, cli := range clients {
		wg.Add(1)
		go func(c *tcpClient) {
			defer wg.Done()

			touches := protocol.CmdTouches{
				Frames: []protocol.TouchFrame{{
					Time: 0.5,
					Points: []protocol.TouchPoint{
						{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.3}},
					},
				}},
			}
			played := protocol.CmdPlayed{ID: int32(c.id)}

			for {
				select {
				case <-stopCh:
					return
				default:
				}

				t0 := time.Now()

				if err := c.sendCommand(touches); err != nil {
					mc.addError(err)
					return
				}
				// Touches 无直接响应（广播给其他玩家），跳过 read
				// 但需要消费可能的广播帧
				if _, err := c.sendCommandRTT(played); err != nil {
					mc.addError(err)
					return
				}

				mc.recordCycle(time.Since(t0))
				mc.addCommands(2)
			}
		}(cli)
	}

	sampleTicker := time.NewTicker(1 * time.Second)
	go func() {
		for range sampleTicker.C {
			select {
			case <-stopCh:
				return
			default:
				mc.sample()
			}
		}
	}()

	time.Sleep(bc.Duration)
	close(stopCh)
	sampleTicker.Stop()
	wg.Wait()

	// 清理
	for _, cli := range clients {
		cli.close()
	}

	elapsed := time.Since(startTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = atomic.LoadInt64(&mc.commandsSent)
	result.CyclesCompleted = atomic.LoadInt64(&mc.cyclesCompleted)
	result.CommandsPerSec = float64(result.CommandsSent) / elapsed.Seconds()
	result.CyclesPerSec = float64(result.CyclesCompleted) / elapsed.Seconds()
	result.CycleLatency = computeLatencyStats(mc.cycleDurs)
	result.ConnectLatency = computeLatencyStats(mc.connectDurs)
	result.AuthLatency = computeLatencyStats(mc.authDurs)
	result.Errors = atomic.LoadInt64(&mc.errors)
	result.ErrorSamples = mc.errMsgs
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
}

// assignTCPClients 按均匀分配返回房间 r 的客户端切片。
func assignTCPClients(clients []*tcpClient, r, totalRooms, totalClients int) []*tcpClient {
	perRoom := totalClients / totalRooms
	start := r * perRoom
	end := start + perRoom
	if r == totalRooms-1 {
		end = totalClients
	}
	if start >= totalClients {
		return nil
	}
	return clients[start:end]
}

// runConnectionStormScenario 真实 TCP 连接风暴：并发建连+认证吞吐量测试。
// 连接全部建立后保持到 Duration 结束，期间持续采样峰值 goroutine/heap，
// 用于衡量服务器在持续持有 N 个空闲连接时的资源开销。
func runConnectionStormScenario(bc benchConfig, mc *metricsCollector, addr string, vipPool *vipPool) scenarioResult {
	result := scenarioResult{
		Name:    "connection-storm",
		Clients: bc.Clients,
		Rooms:   bc.Rooms,
	}
	startTime := time.Now()

	var wg sync.WaitGroup
	sem := make(chan struct{}, bc.Concurrency)

	// 跟踪已建立的连接，用于测试结束后统一关闭
	var clientsMu sync.Mutex
	clients := make([]*tcpClient, 0, bc.Clients)

	// 周期采样 goroutine：捕获峰值 goroutine/heap（连接建立期 + 持有期）
	stopCh := make(chan struct{})
	sampleTicker := time.NewTicker(1 * time.Second)
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-sampleTicker.C:
				mc.sample()
			}
		}
	}()

	for i := 0; i < bc.Clients; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }()

			vip := vipPool.next()
			cli, connectDur, authDur, err := retryableConnect(id, addr, vip, false)
			if err != nil {
				mc.addError(err)
				return
			}
			mc.recordConnect(connectDur)
			mc.recordAuth(authDur)
			mc.addCommands(2)

			clientsMu.Lock()
			clients = append(clients, cli)
			clientsMu.Unlock()
		}(i + 1)
	}
	wg.Wait()

	// 风暴建立完毕，立即采样捕获峰值
	mc.sample()

	// 持有所有连接至 Duration 结束，测量稳态资源占用
	if remaining := bc.Duration - time.Since(startTime); remaining > 0 {
		time.Sleep(remaining)
	}

	close(stopCh)
	sampleTicker.Stop()

	// 关闭所有连接
	clientsMu.Lock()
	for _, cli := range clients {
		cli.close()
	}
	clientsMu.Unlock()

	// 最终采样
	mc.sample()

	elapsed := time.Since(startTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = atomic.LoadInt64(&mc.commandsSent)
	result.CommandsPerSec = float64(result.CommandsSent) / elapsed.Seconds()
	result.ConnectLatency = computeLatencyStats(mc.connectDurs)
	result.AuthLatency = computeLatencyStats(mc.authDurs)
	result.Errors = atomic.LoadInt64(&mc.errors)
	result.ErrorSamples = mc.errMsgs
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
}

// runSteadyStateScenario 真实 TCP 稳态压测：保持固定数量连接持续发送心跳。
func runSteadyStateScenario(bc benchConfig, mc *metricsCollector, addr string, vipPool *vipPool) scenarioResult {
	result := scenarioResult{
		Name:    "steady-state",
		Clients: bc.Clients,
		Rooms:   bc.Rooms,
	}
	startTime := time.Now()

	// 创建、连接、认证所有客户端
	clients := make([]*tcpClient, 0, bc.Clients)
	{
		var wg sync.WaitGroup
		var clientsMu sync.Mutex
		sem := make(chan struct{}, bc.Concurrency)

		for i := 0; i < bc.Clients; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				vip := vipPool.next()
				cli, connectDur, _, err := retryableConnect(id, addr, vip, bc.Verbose)
				if err != nil {
					mc.addError(err)
					return
				}
				mc.recordConnect(connectDur)
				mc.addCommands(2)

				clientsMu.Lock()
				clients = append(clients, cli)
				clientsMu.Unlock()
			}(i + 1)
		}
		wg.Wait()
	}

	if bc.Verbose {
		fmt.Fprintf(os.Stderr, "  [INFO] 已连接/认证: %d 个客户端，开始稳态发送\n", len(clients))
	}

	// 持续发送 Ping
	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	for _, cli := range clients {
		wg.Add(1)
		go func(c *tcpClient) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
				}
				t0 := time.Now()
				if err := c.sendCommand(protocol.CmdPing{}); err != nil {
					mc.addError(err)
					return
				}
				if _, err := c.readFrame(); err != nil {
					mc.addError(err)
					return
				}
				mc.recordCycle(time.Since(t0))
				mc.addCommands(1)
			}
		}(cli)
	}

	sampleTicker := time.NewTicker(1 * time.Second)
	go func() {
		for range sampleTicker.C {
			select {
			case <-stopCh:
				return
			default:
				mc.sample()
			}
		}
	}()

	time.Sleep(bc.Duration)
	close(stopCh)
	sampleTicker.Stop()
	wg.Wait()

	for _, cli := range clients {
		cli.close()
	}

	elapsed := time.Since(startTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = atomic.LoadInt64(&mc.commandsSent)
	result.CommandsPerSec = float64(result.CommandsSent) / elapsed.Seconds()
	result.CycleLatency = computeLatencyStats(mc.cycleDurs)
	result.ConnectLatency = computeLatencyStats(mc.connectDurs)
	result.AuthLatency = computeLatencyStats(mc.authDurs)
	result.Errors = atomic.LoadInt64(&mc.errors)
	result.ErrorSamples = mc.errMsgs
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
}

// runGameplayScenario 真实 TCP Playing 阶段高频 Judge/Touch 帧压测。
func runGameplayScenario(bc benchConfig, mc *metricsCollector, addr string, vipPool *vipPool) scenarioResult {
	result := scenarioResult{
		Name:    "gameplay",
		Clients: bc.Clients,
		Rooms:   bc.Rooms,
	}
	startTime := time.Now()

	// 预生成数据
	type frameBundle struct {
		touches protocol.CmdTouches
		judges  protocol.CmdJudges
	}
	bundles := make([]frameBundle, 300)
	for i := range bundles {
		t := float32(i) * 0.016
		bundles[i] = frameBundle{
			touches: protocol.CmdTouches{
				Frames: []protocol.TouchFrame{{
					Time: t,
					Points: []protocol.TouchPoint{
						{ID: 0, Pos: protocol.CompactPos{X: 0.5 + float32(i%10)*0.02, Y: 0.3}},
						{ID: 1, Pos: protocol.CompactPos{X: 0.7, Y: 0.4}},
					},
				}},
			},
			judges: protocol.CmdJudges{
				Judges: []protocol.JudgeEvent{
					{Time: t, LineID: 0, NoteID: uint32(i) % 100, Judgement: protocol.JudgePerfect},
					{Time: t, LineID: 1, NoteID: uint32(i)%100 + 1, Judgement: protocol.JudgeGood},
				},
			},
		}
	}

	// Phase 1: 创建所有客户端并连接+认证
	clients := make([]*tcpClient, 0, bc.Clients)
	{
		var wg sync.WaitGroup
		var clientsMu sync.Mutex
		sem := make(chan struct{}, bc.Concurrency)

		for i := 0; i < bc.Clients; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				vip := vipPool.next()
				cli, _, _, err := retryableConnect(id, addr, vip, bc.Verbose)
				if err != nil {
					mc.addError(err)
					return
				}
				mc.addCommands(2)

				clientsMu.Lock()
				clients = append(clients, cli)
				clientsMu.Unlock()
			}(i + 1)
		}
		wg.Wait()
	}

	if bc.Verbose {
		fmt.Fprintf(os.Stderr, "  [INFO] 已连接/认证: %d 个客户端\n", len(clients))
	}

	// 重新分配 ID
	for i, cli := range clients {
		cli.id = i + 1
	}

	// Phase 2: 让所有房间进入 Playing 状态
	for r := 0; r < bc.Rooms; r++ {
		roomID := protocol.RoomID(fmt.Sprintf("gm-r%d", r))
		roomClients := assignTCPClients(clients, r, bc.Rooms, len(clients))
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]

		host.sendCommand(protocol.CmdCreateRoom{ID: roomID})
		host.readFrame() // 消费响应
		for _, cli := range roomClients[1:] {
			cli.sendCommand(protocol.CmdJoinRoom{ID: roomID, Monitor: false})
			cli.readFrame()
		}
		host.sendCommand(protocol.CmdSelectChart{ID: 1})
		host.readFrame()
		host.sendCommand(protocol.CmdRequestStart{})
		host.readFrame()
		for _, cli := range roomClients[1:] {
			cli.sendCommand(protocol.CmdReady{})
			cli.readFrame()
		}
		mc.addCommands(int64(len(roomClients)*2 + 3))
	}

	if bc.Verbose {
		fmt.Fprintf(os.Stderr, "  [INFO] Playing 阶段开始: %d 个客户端, %d 个房间\n", len(clients), bc.Rooms)
	}

	// Phase 3: 并发发送 Touches/Judges
	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	var frameIdx atomic.Int32

	for _, cli := range clients {
		wg.Add(1)
		go func(c *tcpClient) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
				}
				idx := int(frameIdx.Add(1)) % len(bundles)
				t0 := time.Now()

				if err := c.sendCommand(bundles[idx].touches); err != nil {
					mc.addError(err)
					return
				}
				if err := c.sendCommand(bundles[idx].judges); err != nil {
					mc.addError(err)
					return
				}

				mc.recordCycle(time.Since(t0))
				mc.addCommands(2)
			}
		}(cli)
	}

	sampleTicker := time.NewTicker(1 * time.Second)
	go func() {
		for range sampleTicker.C {
			select {
			case <-stopCh:
				return
			default:
				mc.sample()
			}
		}
	}()

	time.Sleep(bc.Duration)
	close(stopCh)
	sampleTicker.Stop()
	wg.Wait()

	// Phase 4: 提交成绩
	for _, cli := range clients {
		cli.sendCommand(protocol.CmdPlayed{ID: int32(cli.id)})
		cli.readFrame()
		mc.addCommands(1)
	}

	for _, cli := range clients {
		cli.close()
	}

	elapsed := time.Since(startTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = atomic.LoadInt64(&mc.commandsSent)
	result.CyclesCompleted = atomic.LoadInt64(&mc.commandsSent) / 2
	result.CommandsPerSec = float64(result.CommandsSent) / elapsed.Seconds()
	result.CyclesPerSec = result.CommandsPerSec / 2
	result.CycleLatency = computeLatencyStats(mc.cycleDurs)
	result.ConnectLatency = computeLatencyStats(mc.connectDurs)
	result.AuthLatency = computeLatencyStats(mc.authDurs)
	result.Errors = atomic.LoadInt64(&mc.errors)
	result.ErrorSamples = mc.errMsgs
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
}

// ---------- 内嵌服务器 ----------

// startEmbeddedServer 在回环地址上启动内嵌 TCP 服务器（mock Phira），返回监听地址。
func startEmbeddedServer(port int) (*network.Server, string, error) {
	cfg := &config.ServerConfig{
		Port:                &port,
		Monitors:            []int{999},
		ConnectionRateLimit: new(int),  // 0 = 禁用（回环自动豁免，此处显式设 0）
		CommandRateLimit:    new(bool), // 禁用命令限速
		ChatEnabled:         new(bool),
	}
	*cfg.CommandRateLimit = false
	*cfg.ChatEnabled = false

	state := server.NewServerState(cfg, nil, "realbench", "", "")
	hub := server.NewHub(state, &mockPhira{})

	srv, err := network.Listen("127.0.0.1:0", state, hub)
	if err != nil {
		return nil, "", fmt.Errorf("启动内嵌服务器失败: %w", err)
	}
	return srv, srv.Addr().String(), nil
}

// ---------- main ----------

func main() {
	bc := parseFlags()

	// JSON 模式下状态信息走 stderr，stdout 仅输出纯净 JSON
	out := os.Stdout
	if bc.JSONOut {
		out = os.Stderr
	}

	fmt.Fprintln(out, "═══════════════════════════════════════════")
	fmt.Fprintln(out, "  Phira MP TCP 压测工具 (tcpconnect)")
	fmt.Fprintln(out, "═══════════════════════════════════════════")
	fmt.Fprintf(out, "  场景:      %s\n", bc.Scenario)
	fmt.Fprintf(out, "  客户端数:  %d\n", bc.Clients)
	fmt.Fprintf(out, "  房间数:    %d\n", bc.Rooms)
	fmt.Fprintf(out, "  持续时间:  %s\n", bc.Duration)
	fmt.Fprintf(out, "  虚拟 IP:   %d\n", bc.VirtualIPs)
	fmt.Fprintf(out, "  并发度:    %d\n", bc.Concurrency)
	fmt.Fprintf(out, "  pprof:     %s\n", bc.Profile)
	fmt.Fprintln(out, "───────────────────────────────────────────")

	// 启动 pprof
	prof, err := startProfiler(bc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动 profiler 失败: %v\n", err)
		os.Exit(1)
	}
	defer prof.stop()

	// 创建虚拟 IP 池
	vipPool := newVIPPool(bc.VirtualIPs)

	// 启动内嵌服务器
	fmt.Fprint(out, "  [1/3] 启动内嵌服务器... ")
	srv, addr, err := startEmbeddedServer(bc.Port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n启动服务器失败: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()
	fmt.Fprintf(out, "OK (监听 %s)\n", addr)

	// 预热
	fmt.Fprint(out, "  [2/3] 预热... ")
	{
		warmupMC := &metricsCollector{}
		wc := benchConfig{Clients: 5, Rooms: 2, Duration: 2 * time.Second, Concurrency: 50, VirtualIPs: bc.VirtualIPs}
		if bc.Scenario == "gameplay" {
			_ = runGameplayScenario(wc, warmupMC, addr, vipPool)
		} else {
			_ = runRoomCycleScenario(wc, warmupMC, addr, vipPool)
		}
		fmt.Fprintln(out, "OK")
	}

	// 运行测试
	fmt.Fprintf(out, "  [3/3] 运行 %s 场景...\n", bc.Scenario)
	mc := &metricsCollector{}
	var result scenarioResult
	switch bc.Scenario {
	case "room-cycle", "mixed":
		result = runRoomCycleScenario(bc, mc, addr, vipPool)
	case "gameplay":
		result = runGameplayScenario(bc, mc, addr, vipPool)
	case "connection-storm":
		result = runConnectionStormScenario(bc, mc, addr, vipPool)
	case "steady-state":
		result = runSteadyStateScenario(bc, mc, addr, vipPool)
	default:
		fmt.Fprintf(os.Stderr, "未知场景: %s\n", bc.Scenario)
		os.Exit(1)
	}

	// 写入 pprof 文件
	prof.writeProfiles(bc.ProfileDir)

	report := benchReport{
		Title:     "Phira MP Real TCP Bench",
		Timestamp: time.Now().Unix(),
		Config:    bc,
		Results:   []scenarioResult{result},
	}

	if bc.JSONOut {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(report)
	} else {
		printResult(result)
	}

	if bc.Profile != "" && bc.Profile != "none" {
		fmt.Fprintln(out, "\n── pprof 分析提示 ──")
		fmt.Fprintf(out, "  CPU:    go tool pprof -http=:8080 %s/cpu.pprof\n", bc.ProfileDir)
		fmt.Fprintf(out, "  Heap:   go tool pprof -http=:8080 %s/heap.pprof\n", bc.ProfileDir)
		fmt.Fprintf(out, "  Goroutines: go tool pprof -http=:8080 %s/goroutine.pprof\n", bc.ProfileDir)
		if bc.Profile == "mutex" || bc.Profile == "all" {
			fmt.Fprintf(out, "  Mutex:  go tool pprof -http=:8080 %s/mutex.pprof\n", bc.ProfileDir)
		}
		if bc.Profile == "block" || bc.Profile == "all" {
			fmt.Fprintf(out, "  Block:  go tool pprof -http=:8080 %s/block.pprof\n", bc.ProfileDir)
		}
	}
}

func printResult(r scenarioResult) {
	fmt.Println("")
	fmt.Println("── 测试结果 ──────────────────────────────")
	fmt.Printf("  场景:          %s\n", r.Name)
	fmt.Printf("  运行时长:      %s\n", r.Duration.Round(time.Millisecond))
	fmt.Printf("  命令总数:      %d\n", r.CommandsSent)
	fmt.Printf("  吞吐量:        %.0f cmd/s\n", r.CommandsPerSec)
	if r.CyclesCompleted > 0 {
		fmt.Printf("  游戏周期:      %d (%.1f 周期/s)\n", r.CyclesCompleted, r.CyclesPerSec)
	}
	fmt.Println("")

	showIf := func(label string, ls latencyStats) {
		if ls.Count == 0 {
			return
		}
		fmt.Printf("  %s:\n", label)
		fmt.Printf("    样本: %d  平均/最小/最大: %s / %s / %s\n",
			ls.Count, formatDur(ls.Mean), formatDur(ls.Min), formatDur(ls.Max))
		if ls.Zero > 0 {
			pctZero := float64(ls.Zero) / float64(ls.Count) * 100
			fmt.Printf("    零值: %d (%.1f%%)  ← 时钟精度不足，实际耗时 <1 个节拍\n", ls.Zero, pctZero)
		}
		var nonZero int64
		for _, b := range ls.Buckets {
			if b.Count > 0 {
				nonZero += b.Count
			}
		}
		if nonZero > 0 {
			fmt.Println("    分布:")
			for _, b := range ls.Buckets {
				if b.Count > 0 {
					fmt.Printf("      %-12s  %d  (%.1f%%)\n", b.Label, b.Count, b.Pct)
				}
			}
		}
	}

	showIf("连接延迟 (TCP握手)", r.ConnectLatency)
	showIf("认证延迟 (Auth RTT)", r.AuthLatency)
	showIf("命令延迟 (Cmd RTT)", r.CycleLatency)

	fmt.Println("")
	fmt.Printf("  错误数:        %d\n", r.Errors)
	fmt.Printf("  峰值协程数:    %d\n", r.PeakGoroutines)
	fmt.Printf("  峰值堆内存:    %.1f MB\n", r.PeakHeapMB)
	fmt.Printf("  最终堆内存:    %.1f MB\n", r.FinalAllocMB)
	fmt.Printf("  GC 次数:       %d\n", r.NumGC)
	if len(r.ErrorSamples) > 0 {
		fmt.Println("  错误样例:")
		for _, s := range r.ErrorSamples {
			fmt.Printf("    - %s\n", s)
		}
	}
	fmt.Println("───────────────────────────────────────────")
}
