package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/benchmark/benchmetrics"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
	"github.com/Pimeng/gooophira-mp/internal/server/network"
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

// ---------- pprof 辅助 ----------

type profiler struct {
	dir            string
	profiles       []string
	cpuFile        *os.File
	mutexProfiling bool
	blockProfiling bool
}

func startProfiler(cfg benchConfig) (*profiler, error) {
	p := &profiler{dir: cfg.ProfileDir, profiles: make([]string, 0, 8)}
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
	writeProfile := func(name, path string) string {
		full := dir + "/" + path
		f, err := os.Create(full)
		if err != nil {
			return ""
		}
		defer f.Close()
		if pprof.Lookup(name) != nil {
			_ = pprof.Lookup(name).WriteTo(f, 0)
			return path
		}
		return ""
	}
	profiles := []string{}
	if p.cpuFile != nil {
		profiles = append(profiles, "cpu.pprof")
	}
	if path := writeProfile("goroutine", "goroutine.pprof"); path != "" {
		profiles = append(profiles, path)
	}
	if path := writeProfile("heap", "heap.pprof"); path != "" {
		profiles = append(profiles, path)
	}
	if p.mutexProfiling {
		if path := writeProfile("mutex", "mutex.pprof"); path != "" {
			profiles = append(profiles, path)
		}
	}
	if p.blockProfiling {
		if path := writeProfile("block", "block.pprof"); path != "" {
			profiles = append(profiles, path)
		}
	}
	p.profiles = profiles
}

// ---------- 模拟 Phira API ----------

var clientFrameWriterPool = &sync.Pool{
	New: func() any { return protocol.NewFrameWriter(5) },
}

type mockPhira struct{}

func (m *mockPhira) FetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
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

type vipPool struct {
	base   net.IP
	cursor atomic.Int32
	size   int
}

func newVIPPool(count int) *vipPool {
	return &vipPool{
		base: net.ParseIP("127.0.0.0"),
		size: count,
	}
}

func (p *vipPool) next() net.IP {
	idx := int(p.cursor.Add(1)) % p.size
	ip := make(net.IP, 4)
	copy(ip, p.base.To4())
	host := 1 + idx
	ip[2] = byte(host >> 8)
	ip[3] = byte(host & 0xFF)
	return ip
}

const (
	connectRetryMax  = 8
	connectJitterMax = 200 * time.Millisecond
	connReadTimeout  = 15 * time.Second
)

var emptyPayload = []byte{}

func retryableConnect(id int, addr string, vip net.IP, verbose bool) (*tcpClient, time.Duration, time.Duration, error) {
	for attempt := 0; attempt < connectRetryMax; attempt++ {
		maxJitter := connectJitterMax << max(attempt-1, 0)
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

// ---------- TCP 客户端 ----------

type tcpClient struct {
	id      int
	addr    string
	vip     net.IP
	conn    net.Conn
	reader  *bufio.Reader
	buf     []byte
	mu      sync.Mutex
	authed  bool
	dead    bool
	recvBuf [4096]byte
}

func newTCPClient(id int, addr string, vip net.IP) *tcpClient {
	return &tcpClient{
		id:   id,
		addr: addr,
		vip:  vip,
		buf:  make([]byte, 0, 4096),
	}
}

func (c *tcpClient) connect() error {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, rc syscall.RawConn) error {
			var err error
			_ = rc.Control(func(fd uintptr) {
				err = setSockOptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			})
			return err
		},
	}
	if c.vip != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: c.vip, Port: 0}
	}
	conn, err := dialer.Dial("tcp", c.addr)
	if err != nil {
		return err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	c.conn = conn
	c.reader = bufio.NewReaderSize(conn, 4096)
	if _, err := conn.Write([]byte{1}); err != nil {
		conn.Close()
		return err
	}
	return nil
}

func (c *tcpClient) authenticate() (time.Duration, error) {
	start := time.Now()
	token := fmt.Sprintf("bench-%d", c.id)
	cmd := protocol.CmdAuthenticate{Token: token}
	if err := c.sendCommand(cmd); err != nil {
		return 0, err
	}
	_, err := c.readFrame()
	if err != nil {
		return 0, err
	}
	c.authed = true
	return time.Since(start), nil
}

func (c *tcpClient) sendCommand(cmd protocol.ClientCommand) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := clientFrameWriterPool.Get().(*protocol.BinaryWriter)
	defer clientFrameWriterPool.Put(w)
	w.Reset()
	protocol.EncodeClientCommand(w, cmd)
	fb := w.ToFrameBuffer()
	frame := make([]byte, len(fb))
	copy(frame, fb)
	_, err := c.conn.Write(frame)
	return err
}

func (c *tcpClient) readFrame() ([]byte, error) {
	c.buf = c.buf[:0]
	for {
		res := protocol.TryDecodeFrame(c.buf, 4*1024*1024)
		switch res.Kind {
		case protocol.FrameOK:
			remaining := len(res.Remaining)
			copy(c.buf, res.Remaining)
			c.buf = c.buf[:remaining]
			return emptyPayload, nil
		case protocol.FrameError:
			return nil, res.Err
		case protocol.FrameNeedMore:
			_ = c.conn.SetReadDeadline(time.Now().Add(connReadTimeout))
			n, err := c.reader.Read(c.recvBuf[:])
			if err != nil {
				return nil, err
			}
			c.buf = append(c.buf, c.recvBuf[:n]...)
		}
	}
}

func (c *tcpClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil && !c.dead {
		c.dead = true
		c.conn.Close()
	}
}

// ---------- 辅助函数 ----------

func connCap(userCap int) int {
	c := userCap / 4
	if c < 1 {
		c = 1
	}
	if c > 2000 {
		c = 2000
	}
	return c
}

func assignTCPClients(clients []*tcpClient, r, totalRooms, totalClients int) []*tcpClient {
	if totalClients <= totalRooms {
		if r < totalClients {
			return clients[r : r+1]
		}
		return nil
	}
	perRoom := totalClients / totalRooms
	remainder := totalClients % totalRooms
	start := r*perRoom + min(r, remainder)
	end := start + perRoom
	if r < remainder {
		end++
	}
	if start >= totalClients {
		return nil
	}
	return clients[start:end]
}

// ---------- 内嵌服务器 ----------

func startEmbeddedServer(port int) (*network.Server, string, error) {
	empty := ""
	cfg := &config.ServerConfig{
		Port:                &port,
		Monitors:            []int{999},
		ConnectionRateLimit: new(int),
		CommandRateLimit:    new(bool),
		ChatEnabled:         new(bool),
		HitokotoAPIURL:      &empty,
	}
	*cfg.CommandRateLimit = false
	*cfg.ChatEnabled = false

	state := server.NewServerState(cfg, nil, "realbench", "", "")
	hub := server.NewHub(state, &mockPhira{})

	lc := &net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			_ = c.Control(func(fd uintptr) {
				err = setSockOptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 1024*1024)
			})
			return err
		},
	}
	srv, err := network.ListenConfig("127.0.0.1:0", lc, state, hub)
	if err != nil {
		return nil, "", fmt.Errorf("启动内嵌服务器失败: %w", err)
	}
	return srv, srv.Addr().String(), nil
}

// ---------- 主程序 ----------

func main() {
	bc := parseFlags()

	out := os.Stdout
	if bc.JSONOut {
		out = os.Stderr
	}

	fmt.Fprintf(out, "═══════════════════════════════════════════\n")
	fmt.Fprintf(out, "  Phira MP TCP 压测工具 (tcpconnect)\n")
	fmt.Fprintf(out, "═══════════════════════════════════════════\n")
	fmt.Fprintf(out, "  场景:      %s\n", bc.Scenario)
	fmt.Fprintf(out, "  客户端数:  %d\n", bc.Clients)
	fmt.Fprintf(out, "  房间数:    %d\n", bc.Rooms)
	fmt.Fprintf(out, "  持续时间:  %s\n", bc.Duration)
	fmt.Fprintf(out, "  虚拟 IP:   %d\n", bc.VirtualIPs)
	fmt.Fprintf(out, "  并发度:    %d\n", bc.Concurrency)
	fmt.Fprintf(out, "  pprof:     %s\n", bc.Profile)
	fmt.Fprintf(out, "───────────────────────────────────────────\n")

	prof, err := startProfiler(bc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动 profiler 失败: %v\n", err)
		os.Exit(1)
	}
	defer prof.stop()

	vipPool := newVIPPool(bc.VirtualIPs)

	// 记录各阶段耗时。
	timeStart := time.Now()

	fmt.Fprint(out, "  [1/4] 启动内嵌服务器... ")
	srv, addr, err := startEmbeddedServer(bc.Port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n启动服务器失败: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()
	fmt.Fprintf(out, "OK (监听 %s)\n", addr)
	timeStartup := time.Since(timeStart)

	// 预热。
	warmupStart := time.Now()
	fmt.Fprint(out, "  [2/4] 预热... ")
	{
		warmupMC := benchmetrics.NewCollector()
		wc := benchConfig{Clients: 5, Rooms: 2, Duration: 2 * time.Second, Concurrency: 50, VirtualIPs: bc.VirtualIPs}
		if bc.Scenario == "gameplay" {
			_ = runGameplayScenario(wc, warmupMC, addr, vipPool)
		} else {
			_ = runRoomCycleScenario(wc, warmupMC, addr, vipPool)
		}
	}
	fmt.Fprintln(out, "OK")
	timeWarmup := time.Since(warmupStart)

	// 运行基准测试。
	benchStart := time.Now()
	fmt.Fprintf(out, "  [3/4] 运行 %s 场景...\n", bc.Scenario)
	mc := benchmetrics.NewCollector()
	var result benchmetrics.BenchResult
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
	timeBenchmark := time.Since(benchStart)

	// 写入性能分析文件。
	fmt.Fprint(out, "  [4/4] 生成 pprof 文件... ")
	prof.writeProfiles(bc.ProfileDir)
	fmt.Fprintln(out, "OK")
	timeShutdown := time.Now().Sub(benchStart) - timeBenchmark

	// 汇总阶段耗时。
	result.TimeBreakdown = benchmetrics.TimeBreakdown{
		Startup:   timeStartup,
		Warmup:    timeWarmup,
		Benchmark: timeBenchmark,
		Shutdown:  timeShutdown,
		Total:     time.Since(timeStart),
	}

	report := benchmetrics.BenchReport{
		Title:     "Phira MP Real TCP Bench",
		Timestamp: time.Now().Unix(),
		Results:   []benchmetrics.BenchResult{result},
		Profiles:  prof.profiles,
	}

	if bc.JSONOut {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(report)
	} else {
		renderer := benchmetrics.NewRenderer(os.Stdout, "Phira MP TCP Benchmark Results")
		renderer.Render(&report)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
