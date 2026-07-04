// Command bench 是 Phira 多人游戏服务器的性能压测/负载测试工具。
//
// 它在本地启动内嵌房间引擎（Hub + mock Phira），模拟多个客户端进行
// 完整的房间生命周期操作，收集延迟/吞吐量指标，并通过 pprof 生成 CPU、内存、
// goroutine、mutex 和 block 分析文件。
//
// 使用方式:
//
//	# 运行默认场景（50 客户端，5 房间，30 秒）
//	go run ./cmd/bench/
//
//	# 高压测试 + 所有 pprof 分析
//	go run ./cmd/bench/ -clients=200 -rooms=20 -duration=60s -profile=all
//
//	# 仅连接风暴场景 + CPU prof
//	go run ./cmd/bench/ -scenario=connection-storm -clients=500 -profile=cpu
//
//	# JSON 输出 + 自定义 prof 目录
//	go run ./cmd/bench/ -json -profile-dir=./profiles/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// ---------- 命令行参数 ----------

type benchConfig struct {
	Clients    int           `json:"clients"`
	Rooms      int           `json:"rooms"`
	Duration   time.Duration `json:"duration"`
	Scenario   string        `json:"scenario"`
	Profile    string        `json:"profile"`
	ProfileDir string        `json:"profile_dir"`
	JSONOut    bool          `json:"json_out"`
	Verbose    bool          `json:"verbose"`
}

func parseFlags() benchConfig {
	var (
		clients    = flag.Int("clients", 50, "并发客户端数")
		rooms      = flag.Int("rooms", 5, "同时活跃房间数")
		duration   = flag.Duration("duration", 30*time.Second, "测试持续时间 (如 30s, 1m)")
		scenario   = flag.String("scenario", "room-cycle", "测试场景: room-cycle, gameplay, connection-storm, steady-state, mixed")
		profile    = flag.String("profile", "", "pprof 分析类型: cpu, mem, goroutine, mutex, block, all")
		profileDir = flag.String("profile-dir", "./tmp/profiles", "pprof 文件输出目录")
		jsonOut    = flag.Bool("json", false, "输出 JSON 格式结果")
		verbose    = flag.Bool("v", false, "详细输出")
	)
	flag.Parse()
	return benchConfig{
		Clients:    *clients,
		Rooms:      *rooms,
		Duration:   *duration,
		Scenario:   *scenario,
		Profile:    *profile,
		ProfileDir: *profileDir,
		JSONOut:    *jsonOut,
		Verbose:    *verbose,
	}
}

// ---------- 结果指标 ----------

type latencyStats struct {
	Count   int           `json:"count"`
	Min     time.Duration `json:"min"`
	Max     time.Duration `json:"max"`
	Mean    time.Duration `json:"mean"`
	P50     time.Duration `json:"p50"`
	P90     time.Duration `json:"p90"`
	P99     time.Duration `json:"p99"`
	StdDev  time.Duration `json:"stddev"`
	Zero    int64         `json:"zero_count"` // 落在 0ns 的样本数（时钟分辨不足导致）
	Buckets []histoBucket `json:"buckets"`    // 桶分布
}

type histoBucket struct {
	Label string  `json:"label"` // e.g. "1-100ns"
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
	mc.mu.Lock()
	mc.commandsSent += n
	mc.mu.Unlock()
}

func (mc *metricsCollector) addCycle() {
	mc.mu.Lock()
	mc.cyclesCompleted++
	mc.mu.Unlock()
}

func (mc *metricsCollector) addError() {
	mc.mu.Lock()
	mc.errors++
	mc.mu.Unlock()
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

	// 桶分布：按界值统计
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

// formatDur 按值大小自动选择 ns/µs/ms/s 显示。
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

// ---------- 模拟客户端（基于 Hub API，不经过 TCP） ----------

// benchSession 实现 server.Session 接口，捕获服务端响应。
type benchSession struct {
	id       string
	sentCmds []protocol.ServerCommand
}

func (s *benchSession) ID() string                         { return s.id }
func (s *benchSession) TrySend(cmd protocol.ServerCommand) { s.sentCmds = append(s.sentCmds, cmd) }
func (s *benchSession) TrySendFrame(frame []byte)          {} // bench 不依赖帧路径
func (s *benchSession) TrySendFrameOwned(frame []byte)     {} // bench 不依赖帧路径
func (s *benchSession) Close()                             {}

type benchClient struct {
	user   *server.User
	hub    *server.Hub
	userID int
}

func newBenchClient(state *server.ServerState, hub *server.Hub, id int, name string) *benchClient {
	u := server.NewUser(id, name, "zh-CN", state)
	u.SetSession(&benchSession{id: fmt.Sprintf("sess-%d", id)})
	state.Users[id] = u
	return &benchClient{user: u, hub: hub, userID: id}
}

// dispatch 发送命令并等待响应（Hub 内部已串行化）。
func (c *benchClient) dispatch(cmd protocol.ClientCommand) (protocol.ServerCommand, error) {
	sess := c.user.Session.(*benchSession)
	before := len(sess.sentCmds)

	c.hub.ProcessClientCommand(c.user, cmd)

	// 检查是否收到了新响应
	if len(sess.sentCmds) > before {
		return sess.sentCmds[len(sess.sentCmds)-1], nil
	}
	return nil, nil // 无响应（如 Ping、Touches、Judges）
}

// performGameCycle 执行一个完整的房间生命周期并记录耗时。
func (c *benchClient) performGameCycle(roomID protocol.RoomID, mc *metricsCollector) {
	start := time.Now()

	// 1. 创建房间
	c.dispatch(protocol.CmdCreateRoom{ID: roomID})
	// 2. 选谱（需 mock chart 存在——bench 引擎已配置 chart 1）
	c.dispatch(protocol.CmdSelectChart{ID: int32(c.userID)})
	// 3. 请求开始
	c.dispatch(protocol.CmdRequestStart{})
	// 4. 准备
	c.dispatch(protocol.CmdReady{})
	// 5. 发送触摸帧（模拟 Playing 阶段）
	c.dispatch(protocol.CmdTouches{
		Frames: []protocol.TouchFrame{
			{Time: 0.5, Points: []protocol.TouchPoint{
				{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.3}},
			}},
		},
	})
	// 6. 提交成绩（用对应 player ID 的 record）
	c.dispatch(protocol.CmdPlayed{ID: int32(c.userID)})

	mc.recordCycle(time.Since(start))
	mc.addCommands(6)
	mc.addCycle()
}

// ---------- mock Phira ----------

type benchMockPhira struct{}

func (b *benchMockPhira) FetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
	return server.PhiraUserInfo{}, nil
}
func (b *benchMockPhira) FetchChart(ctx context.Context, id int) (config.Chart, error) {
	return config.Chart{ID: id, Name: fmt.Sprintf("chart-%d", id)}, nil
}
func (b *benchMockPhira) FetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	return config.RecordData{ID: id, Player: id, Score: 900000, Accuracy: 0.95, Std: 0.02}, nil
}

// ---------- 测试场景 ----------

// runRoomCycleScenario 执行完整的房间生命周期测试。
// 每个客户端一个独立 goroutine，每条命令单独持 state.Mu 锁，
// 模拟真实 TCP session 的并发模型（readLoop 持锁 → dispatch → 释放 → 等待下一条命令）。
func runRoomCycleScenario(bc benchConfig, mc *metricsCollector) scenarioResult {
	result := scenarioResult{
		Name:    "room-cycle",
		Clients: bc.Clients,
		Rooms:   bc.Rooms,
	}
	startTime := time.Now()

	state := server.NewServerState(&config.ServerConfig{Monitors: []int{999}}, nil, "bench", "", "")
	hub := server.NewHub(state, &benchMockPhira{})

	// 预分配房间 ID
	roomIDs := make([]protocol.RoomID, bc.Rooms)
	for r := 0; r < bc.Rooms; r++ {
		roomIDs[r] = protocol.RoomID(fmt.Sprintf("bench-r%d", r))
	}

	// 创建客户端
	clients := make([]*benchClient, 0, bc.Clients)
	for i := 0; i < bc.Clients; i++ {
		c := newBenchClient(state, hub, i+1, fmt.Sprintf("player-%d", i+1))
		clients = append(clients, c)
	}

	// Phase 1（串行）: 分配客户端到房间，完成 create → join → select → ready → Playing
	// 这一步必须串行以保证房间状态一致（建房东先加入、再其他人加入）。
	for r := 0; r < bc.Rooms; r++ {
		// 确定本房间的客户端切片
		roomClients := assignRoomClients(clients, r, bc.Rooms, bc.Clients)
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]
		rid := roomIDs[r]

		state.Mu.Lock()
		hub.ProcessClientCommand(host.user, protocol.CmdCreateRoom{ID: rid})
		// 其余玩家加入
		for _, c := range roomClients[1:] {
			hub.ProcessClientCommand(c.user, protocol.CmdJoinRoom{ID: rid, Monitor: false})
		}
		// 选谱
		hub.ProcessClientCommand(host.user, protocol.CmdSelectChart{ID: 1})
		// 请求开始 → 自动进入 WaitForReady（host 自动就绪）
		hub.ProcessClientCommand(host.user, protocol.CmdRequestStart{})
		// 其余玩家就绪
		for _, c := range roomClients[1:] {
			hub.ProcessClientCommand(c.user, protocol.CmdReady{})
		}
		state.Mu.Unlock()

		mc.addCommands(int64(2 + 2*(len(roomClients)-1) + 2)) // create+join*(n-1)+select+start+ready*(n-1)
	}

	if bc.Verbose {
		fmt.Fprintf(os.Stderr, "  [INFO] Playing 阶段开始: %d 个客户端, %d 个房间\n", bc.Clients, bc.Rooms)
	}

	// Phase 2（并发）: 每个客户端独立 goroutine，循环发送 Touches → Played，
	// 每条命令单独持 state.Mu（对齐真实 session.readLoop 的逐命令持锁模型）。
	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	for _, c := range clients {
		wg.Add(1)
		go func(cl *benchClient) {
			defer wg.Done()

			touches := protocol.CmdTouches{
				Frames: []protocol.TouchFrame{{
					Time: 0.5,
					Points: []protocol.TouchPoint{
						{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.3}},
					},
				}},
			}
			played := protocol.CmdPlayed{ID: int32(cl.userID)}

			for {
				select {
				case <-stopCh:
					return
				default:
				}

				t0 := time.Now()

				// 持 room.Mu 分段锁（不同房间间并行）
				if room := cl.user.Room; room != nil {
					room.Mu.Lock()
					hub.ProcessClientCommand(cl.user, touches)
					room.Mu.Unlock()

					room.Mu.Lock()
					hub.ProcessClientCommand(cl.user, played)
					room.Mu.Unlock()
				}

				mc.recordCycle(time.Since(t0))
				mc.addCommands(2)
			}
		}(c)
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

	elapsed := time.Since(startTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = mc.commandsSent
	result.CyclesCompleted = mc.cyclesCompleted
	result.CommandsPerSec = float64(mc.commandsSent) / elapsed.Seconds()
	result.CyclesPerSec = float64(mc.cyclesCompleted) / elapsed.Seconds()
	result.CycleLatency = computeLatencyStats(mc.cycleDurs)
	result.Errors = mc.errors
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
}

// assignRoomClients 按均匀分配返回房间 r 的客户端切片。
func assignRoomClients(clients []*benchClient, r, totalRooms, totalClients int) []*benchClient {
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

// runConnectionStormScenario 测量同时连接+认证的吞吐量。
func runConnectionStormScenario(bc benchConfig, mc *metricsCollector) scenarioResult {
	result := scenarioResult{
		Name:    "connection-storm",
		Clients: bc.Clients,
	}
	startTime := time.Now()

	state := server.NewServerState(&config.ServerConfig{}, nil, "bench", "", "")

	// 并发批量创建用户（模拟大量客户端同时连接）
	var wg sync.WaitGroup
	sem := make(chan struct{}, 200) // 并发度

	for i := 0; i < bc.Clients; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }()

			t0 := time.Now()
			u := server.NewUser(id, fmt.Sprintf("u-%d", id), "", state)
			u.SetSession(&benchSession{id: fmt.Sprintf("sess-%d", id)})

			state.Mu.Lock()
			state.Users[id] = u
			state.Mu.Unlock()

			mc.recordConnect(time.Since(t0))
			mc.addCommands(1)
		}(i + 1)
	}
	wg.Wait()

	elapsed := time.Since(startTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = mc.commandsSent
	result.CommandsPerSec = float64(mc.commandsSent) / elapsed.Seconds()
	result.ConnectLatency = computeLatencyStats(mc.connectDurs)
	result.Errors = mc.errors
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
}

// runSteadyStateScenario 保持固定数量客户端的持续命令流。
func runSteadyStateScenario(bc benchConfig, mc *metricsCollector) scenarioResult {
	result := scenarioResult{
		Name:    "steady-state",
		Clients: bc.Clients,
		Rooms:   bc.Rooms,
	}
	startTime := time.Now()

	state := server.NewServerState(&config.ServerConfig{Monitors: []int{999}}, nil, "bench", "", "")
	hub := server.NewHub(state, &benchMockPhira{})

	// 创建客户端 + 进行一次游戏让它们都进入房间
	clients := make([]*benchClient, 0, bc.Clients)
	for i := 0; i < bc.Clients; i++ {
		c := newBenchClient(state, hub, i+1, fmt.Sprintf("p-%d", i+1))
		clients = append(clients, c)
		roomSuffix := i % bc.Rooms
		rid := protocol.RoomID(fmt.Sprintf("ss-r%d", roomSuffix))
		c.performGameCycle(rid, mc)
	}

	// 持续发送心跳/聊天命令
	var wg sync.WaitGroup
	sem := make(chan struct{}, 100)
	stopCh := make(chan struct{})

	for _, c := range clients {
		wg.Add(1)
		go func(cl *benchClient) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				case sem <- struct{}{}:
					t0 := time.Now()
					cl.dispatch(protocol.CmdPing{})
					mc.recordCycle(time.Since(t0))
					mc.addCommands(1)
					<-sem
				}
			}
		}(c)
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
	elapsed := time.Since(startTime)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = mc.commandsSent
	result.CommandsPerSec = float64(mc.commandsSent) / elapsed.Seconds()
	result.CycleLatency = computeLatencyStats(mc.cycleDurs)
	result.Errors = mc.errors
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
}

// runGameplayScenario 模拟真实 Playing 阶段的高频 Judge/Touch 帧收发。
//
// 流程：
//  1. 分配客户端到房间，完成 create → join → select → ready → Playing
//  2. 进入 Playing 后，所有客户端并发持续发送 Touches + Judges 帧
//  3. 持续 bc.Duration 时间，实时采集帧吞吐量和 dispatch 延迟
//  4. 结束时所有玩家提交 CmdPlayed 正常结算
func runGameplayScenario(bc benchConfig, mc *metricsCollector) scenarioResult {
	result := scenarioResult{
		Name:    "gameplay",
		Clients: bc.Clients,
		Rooms:   bc.Rooms,
	}
	startTime := time.Now()

	state := server.NewServerState(&config.ServerConfig{Monitors: []int{999}}, nil, "bench", "", "")
	hub := server.NewHub(state, &benchMockPhira{})

	// 预生成 Touch/Judge 数据（模拟 ~60fps × 4 触摸点 + 3 判定/帧）
	type frameBundle struct {
		touches protocol.CmdTouches
		judges  protocol.CmdJudges
	}
	bundles := make([]frameBundle, 300) // ~5s 的帧（60fps）
	for i := range bundles {
		t := float32(i) * 0.016 // 60fps 时间步长
		bundles[i] = frameBundle{
			touches: protocol.CmdTouches{
				Frames: []protocol.TouchFrame{{
					Time: t,
					Points: []protocol.TouchPoint{
						{ID: 0, Pos: protocol.CompactPos{X: 0.5 + float32(i%10)*0.02, Y: 0.3}},
						{ID: 1, Pos: protocol.CompactPos{X: 0.7, Y: 0.4}},
						{ID: 2, Pos: protocol.CompactPos{X: 0.3, Y: 0.6}},
						{ID: 3, Pos: protocol.CompactPos{X: 0.8, Y: 0.1}},
					},
				}},
			},
			judges: protocol.CmdJudges{
				Judges: []protocol.JudgeEvent{
					{Time: t, LineID: 0, NoteID: uint32(i) % 100, Judgement: protocol.JudgePerfect},
					{Time: t, LineID: 1, NoteID: uint32(i)%100 + 1, Judgement: protocol.JudgeGood},
					{Time: t, LineID: 2, NoteID: uint32(i)%100 + 2, Judgement: protocol.JudgeBad},
				},
			},
		}
	}

	// Phase 1: 创建客户端并让所有房间进入 Playing 状态
	clients := make([]*benchClient, 0, bc.Clients)
	for i := 0; i < bc.Clients; i++ {
		c := newBenchClient(state, hub, i+1, fmt.Sprintf("p-%d", i+1))
		clients = append(clients, c)
	}

	// 按房间分配客户端并进入 Playing 状态
	for r := 0; r < bc.Rooms; r++ {
		roomID := protocol.RoomID(fmt.Sprintf("gm-r%d", r))
		roomClients := assignRoomClients(clients, r, bc.Rooms, bc.Clients)
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]
		// 建房间
		host.dispatch(protocol.CmdCreateRoom{ID: roomID})
		// 其余玩家加入
		for _, c := range roomClients[1:] {
			c.dispatch(protocol.CmdJoinRoom{ID: roomID, Monitor: false})
		}
		// 选谱
		host.dispatch(protocol.CmdSelectChart{ID: int32(host.userID)})
		// 请求开始
		host.dispatch(protocol.CmdRequestStart{})
		// 所有玩家就绪
		for _, c := range roomClients {
			c.dispatch(protocol.CmdReady{})
		}
		mc.addCommands(int64(len(roomClients)*2 + 3)) // create + join*(n-1) + select + start + ready*n
	}

	if bc.Verbose {
		fmt.Fprintf(os.Stderr, "  [INFO] Playing 阶段开始: %d 个客户端, %d 个房间\n", bc.Clients, bc.Rooms)
	}

	// Phase 2: 持续发送 Touches/Judges
	var wg sync.WaitGroup
	sem := make(chan struct{}, bc.Clients) // 允许每客户端一个并发槽位
	stopCh := make(chan struct{})

	// 帧 index 计数器（每个 goroutine 独立递增，模拟时间推进）
	var frameIdx atomic.Int32

	for _, c := range clients {
		wg.Add(1)
		go func(cl *benchClient) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				case sem <- struct{}{}:
					idx := int(frameIdx.Add(1)) % len(bundles)
					t0 := time.Now()

					// 对齐真实 session：Touches/Judges 仅持 room.Mu（分段锁，房间间并行）
					room := cl.user.Room
					if room != nil {
						room.Mu.Lock()
						cl.hub.ProcessClientCommand(cl.user, bundles[idx].touches)
						cl.hub.ProcessClientCommand(cl.user, bundles[idx].judges)
						room.Mu.Unlock()
					}

					mc.recordCycle(time.Since(t0))
					mc.addCommands(2)
					<-sem
				}
			}
		}(c)
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

	// Phase 3: 所有玩家交成绩 → 正常结算
	for _, c := range clients {
		c.dispatch(protocol.CmdPlayed{ID: int32(c.userID)})
		mc.addCommands(1)
	}

	elapsed := time.Since(startTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result.Duration = elapsed.Round(time.Millisecond)
	result.CommandsSent = mc.commandsSent
	result.CommandsPerSec = float64(mc.commandsSent) / elapsed.Seconds()
	result.CyclesCompleted = mc.commandsSent / 2 // 每轮 = 1 Touches + 1 Judges
	result.CyclesPerSec = result.CommandsPerSec / 2
	result.CycleLatency = computeLatencyStats(mc.cycleDurs)
	result.Errors = mc.errors
	result.PeakGoroutines = mc.peakGoroutines
	result.PeakHeapMB = float64(mc.maxHeap) / 1024 / 1024
	result.FinalAllocMB = float64(m.HeapAlloc) / 1024 / 1024
	result.NumGC = m.NumGC

	return result
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
	fmt.Fprintln(out, "  Phira MP 服务器性能压测工具")
	fmt.Fprintln(out, "═══════════════════════════════════════════")
	fmt.Fprintf(out, "  场景:      %s\n", bc.Scenario)
	fmt.Fprintf(out, "  客户端数:  %d\n", bc.Clients)
	fmt.Fprintf(out, "  房间数:    %d\n", bc.Rooms)
	fmt.Fprintf(out, "  持续时间:  %s\n", bc.Duration)
	fmt.Fprintf(out, "  pprof:     %s\n", bc.Profile)
	fmt.Fprintln(out, "───────────────────────────────────────────")

	// 启动 pprof
	prof, err := startProfiler(bc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动 profiler 失败: %v\n", err)
		os.Exit(1)
	}
	defer prof.stop()

	// 预热：跑少量周期让缓存/内存分配稳定
	fmt.Fprint(out, "  [1/2] 预热... ")
	{
		warmupMC := &metricsCollector{}
		wc := benchConfig{Clients: 5, Rooms: 2, Duration: 2 * time.Second}
		if bc.Scenario == "gameplay" {
			_ = runGameplayScenario(wc, warmupMC)
		} else {
			_ = runRoomCycleScenario(wc, warmupMC)
		}
		fmt.Fprintln(out, "OK")
	}

	// 运行测试
	fmt.Fprintln(out, "  [2/2] 运行测试...")
	mc := &metricsCollector{}
	var result scenarioResult
	switch bc.Scenario {
	case "room-cycle", "mixed":
		result = runRoomCycleScenario(bc, mc)
	case "gameplay":
		result = runGameplayScenario(bc, mc)
	case "connection-storm":
		result = runConnectionStormScenario(bc, mc)
	case "steady-state":
		result = runSteadyStateScenario(bc, mc)
	default:
		fmt.Fprintf(os.Stderr, "未知场景: %s\n", bc.Scenario)
		os.Exit(1)
	}

	// 写入 pprof 文件
	prof.writeProfiles(bc.ProfileDir)

	report := benchReport{
		Title:     "Phira MP Bench",
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
		// 桶分布
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

	showIf("命令延迟", r.CycleLatency)
	showIf("连接延迟", r.ConnectLatency)
	showIf("认证延迟", r.AuthLatency)

	fmt.Println("")
	fmt.Printf("  错误数:        %d\n", r.Errors)
	fmt.Printf("  峰值协程数:    %d\n", r.PeakGoroutines)
	fmt.Printf("  峰值堆内存:    %.1f MB\n", r.PeakHeapMB)
	fmt.Printf("  最终堆内存:    %.1f MB\n", r.FinalAllocMB)
	fmt.Printf("  GC 次数:       %d\n", r.NumGC)
	fmt.Println("───────────────────────────────────────────")
}
