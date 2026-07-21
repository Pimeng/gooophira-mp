package main

import (
	"context"
	"fmt"

	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/benchmark/benchmetrics"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// ---------- 模拟 Phira ----------

type benchMockPhira struct{}

func (b *benchMockPhira) FetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
	return server.PhiraUserInfo{}, nil
}
func (b *benchMockPhira) FetchChart(ctx context.Context, id int) (config.Chart, error) {
	return config.Chart{ID: id, Name: fmt.Sprintf("chart-%d", id)}, nil
}
func (b *benchMockPhira) FetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	return config.RecordData{ID: id, Player: id, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}, nil
}

// ---------- 压测会话 ----------

type benchSession struct {
	id       string
	sentCmds []protocol.ServerCommand
}

func (s *benchSession) ID() string                         { return s.id }
func (s *benchSession) TrySend(cmd protocol.ServerCommand) { s.sentCmds = append(s.sentCmds, cmd) }
func (s *benchSession) TrySendFrame(frame []byte)          {}
func (s *benchSession) TrySendFrameOwned(frame []byte)     {}
func (s *benchSession) Close()                             {}

// ---------- 压测客户端 ----------

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

func (c *benchClient) dispatch(cmd protocol.ClientCommand) error {
	switch cmd.(type) {
	case protocol.CmdTouches, protocol.CmdJudges, protocol.CmdPlayed:
		if room := c.user.Room; room != nil {
			room.Mu.Lock()
			c.hub.ProcessClientCommand(c.user, cmd)
			room.Mu.Unlock()
		} else {
			c.hub.State.Mu.Lock()
			c.hub.ProcessClientCommand(c.user, cmd)
			c.hub.State.Mu.Unlock()
		}
	default:
		c.hub.State.Mu.Lock()
		c.hub.ProcessClientCommand(c.user, cmd)
		c.hub.State.Mu.Unlock()
	}
	return nil
}

// ---------- 辅助函数 ----------

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

// ---------- 场景：房间循环 ----------

func runRoomCycleScenario(bc benchConfig, mc *benchmetrics.Collector) benchmetrics.BenchResult {
	startTime := time.Now()
	state := server.NewServerState(&config.ServerConfig{Monitors: []int{999}}, nil, "bench", "", "")
	hub := server.NewHub(state, &benchMockPhira{})

	roomIDs := make([]protocol.RoomID, bc.Rooms)
	for r := 0; r < bc.Rooms; r++ {
		roomIDs[r] = protocol.RoomID(fmt.Sprintf("bench-r%d", r))
	}

	clients := make([]*benchClient, 0, bc.Clients)
	for i := 0; i < bc.Clients; i++ {
		c := newBenchClient(state, hub, i+1, fmt.Sprintf("player-%d", i+1))
		clients = append(clients, c)
	}

	// 阶段 1：串行设置房间。
	for r := 0; r < bc.Rooms; r++ {
		roomClients := assignRoomClients(clients, r, bc.Rooms, bc.Clients)
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]
		rid := roomIDs[r]

		state.Mu.Lock()
		hub.ProcessClientCommand(host.user, protocol.CmdCreateRoom{ID: rid})
		for _, c := range roomClients[1:] {
			hub.ProcessClientCommand(c.user, protocol.CmdJoinRoom{ID: rid, Monitor: false})
		}
		hub.ProcessClientCommand(host.user, protocol.CmdSelectChart{ID: 1})
		hub.ProcessClientCommand(host.user, protocol.CmdRequestStart{})
		for _, c := range roomClients[1:] {
			hub.ProcessClientCommand(c.user, protocol.CmdReady{})
		}
		state.Mu.Unlock()
		mc.AddCommands(int64(2 + 2*(len(roomClients)-1) + 2))
	}

	// 阶段 2：并发游戏循环。
	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	for _, cli := range clients {
		wg.Add(1)
		go func(c *benchClient) {
			defer wg.Done()
			touches := protocol.CmdTouches{
				Frames: []protocol.TouchFrame{{Time: 0.5, Points: []protocol.TouchPoint{
					{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.3}},
				}}},
			}
			played := protocol.CmdPlayed{ID: int32(c.userID)}

			for {
				select {
				case <-stopCh:
					return
				default:
				}
				t0 := time.Now()
				if room := c.user.Room; room != nil {
					room.Mu.Lock()
					hub.ProcessClientCommand(c.user, touches)
					hub.ProcessClientCommand(c.user, played)
					room.Mu.Unlock()
				}
				mc.RecordCmdLatency(time.Since(t0))
				mc.AddCommands(2)
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
				mc.Sample()
				mc.TimelineTick()
			}
		}
	}()

	time.Sleep(bc.Duration)
	close(stopCh)
	sampleTicker.Stop()
	wg.Wait()

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients: bc.Clients, Rooms: bc.Rooms, Duration: bc.Duration, Concurrency: bc.Clients,
	}, elapsed)
	result.Name = "room-cycle"

	avgPlayers := float64(bc.Clients) / float64(max(1, bc.Rooms))
	result.Scenario.RoomCycle = &benchmetrics.RoomCycleStats{
		RoomCreateCount: int64(bc.Rooms), JoinSuccess: int64(bc.Clients), AvgPlayersPerRoom: avgPlayers,
	}
	return result
}

// ---------- 场景：连接风暴 ----------

func runConnectionStormScenario(bc benchConfig, mc *benchmetrics.Collector) benchmetrics.BenchResult {
	startTime := time.Now()
	state := server.NewServerState(&config.ServerConfig{}, nil, "bench", "", "")
	var wg sync.WaitGroup
	sem := make(chan struct{}, 200)

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
			mc.RecordConnectLatency(time.Since(t0))
			mc.AddCommands(1)
		}(i + 1)
	}
	wg.Wait()

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients: bc.Clients, Duration: bc.Duration, Concurrency: bc.Clients,
	}, elapsed)
	result.Name = "connection-storm"
	return result
}

// ---------- 场景：稳定状态 ----------

func runSteadyStateScenario(bc benchConfig, mc *benchmetrics.Collector) benchmetrics.BenchResult {
	startTime := time.Now()
	state := server.NewServerState(&config.ServerConfig{Monitors: []int{999}}, nil, "bench", "", "")
	hub := server.NewHub(state, &benchMockPhira{})

	clients := make([]*benchClient, 0, bc.Clients)
	for i := 0; i < bc.Clients; i++ {
		c := newBenchClient(state, hub, i+1, fmt.Sprintf("p-%d", i+1))
		clients = append(clients, c)
		roomSuffix := i % bc.Rooms
		rid := protocol.RoomID(fmt.Sprintf("ss-r%d", roomSuffix))
		// 快速执行一轮房间流程。
		sess := c.user.Session().(*benchSession)
		_ = sess
		state.Mu.Lock()
		hub.ProcessClientCommand(c.user, protocol.CmdCreateRoom{ID: rid})
		hub.ProcessClientCommand(c.user, protocol.CmdSelectChart{ID: int32(c.userID)})
		hub.ProcessClientCommand(c.user, protocol.CmdRequestStart{})
		hub.ProcessClientCommand(c.user, protocol.CmdReady{})
		hub.ProcessClientCommand(c.user, protocol.CmdTouches{
			Frames: []protocol.TouchFrame{{Time: 0.5, Points: []protocol.TouchPoint{
				{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.3}},
			}}},
		})
		if room := c.user.Room; room != nil {
			room.Mu.Lock()
			hub.ProcessClientCommand(c.user, protocol.CmdPlayed{ID: int32(c.userID)})
			room.Mu.Unlock()
		}
		state.Mu.Unlock()
		mc.AddCommands(6)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 100)
	stopCh := make(chan struct{})

	for _, cli := range clients {
		wg.Add(1)
		go func(c *benchClient) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				case sem <- struct{}{}:
					t0 := time.Now()
					cli.dispatch(protocol.CmdPing{})
					mc.RecordCmdLatency(time.Since(t0))
					mc.AddCommands(1)
					<-sem
				}
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
				mc.Sample()
				mc.TimelineTick()
			}
		}
	}()

	time.Sleep(bc.Duration)
	close(stopCh)
	sampleTicker.Stop()
	wg.Wait()

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients: bc.Clients, Rooms: bc.Rooms, Duration: bc.Duration, Concurrency: bc.Clients,
	}, elapsed)
	result.Name = "steady-state"
	return result
}

// ---------- 场景：游戏过程 ----------

func runGameplayScenario(bc benchConfig, mc *benchmetrics.Collector) benchmetrics.BenchResult {
	startTime := time.Now()
	state := server.NewServerState(&config.ServerConfig{Monitors: []int{999}}, nil, "bench", "", "")
	hub := server.NewHub(state, &benchMockPhira{})

	type frameBundle struct {
		touches protocol.CmdTouches
		judges  protocol.CmdJudges
	}
	bundles := make([]frameBundle, 300)
	for i := range bundles {
		t := float32(i) * 0.016
		bundles[i] = frameBundle{
			touches: protocol.CmdTouches{
				Frames: []protocol.TouchFrame{{Time: t, Points: []protocol.TouchPoint{
					{ID: 0, Pos: protocol.CompactPos{X: 0.5 + float32(i%10)*0.02, Y: 0.3}},
					{ID: 1, Pos: protocol.CompactPos{X: 0.7, Y: 0.4}},
					{ID: 2, Pos: protocol.CompactPos{X: 0.3, Y: 0.6}},
					{ID: 3, Pos: protocol.CompactPos{X: 0.8, Y: 0.1}},
				}}},
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

	clients := make([]*benchClient, 0, bc.Clients)
	for i := 0; i < bc.Clients; i++ {
		c := newBenchClient(state, hub, i+1, fmt.Sprintf("p-%d", i+1))
		clients = append(clients, c)
	}

	// 进入 Playing 状态。
	for r := 0; r < bc.Rooms; r++ {
		roomID := protocol.RoomID(fmt.Sprintf("gm-r%d", r))
		roomClients := assignRoomClients(clients, r, bc.Rooms, bc.Clients)
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]
		state.Mu.Lock()
		hub.ProcessClientCommand(host.user, protocol.CmdCreateRoom{ID: roomID})
		for _, c := range roomClients[1:] {
			hub.ProcessClientCommand(c.user, protocol.CmdJoinRoom{ID: roomID, Monitor: false})
		}
		hub.ProcessClientCommand(host.user, protocol.CmdSelectChart{ID: int32(host.userID)})
		hub.ProcessClientCommand(host.user, protocol.CmdRequestStart{})
		for _, c := range roomClients {
			hub.ProcessClientCommand(c.user, protocol.CmdReady{})
		}
		state.Mu.Unlock()
		mc.AddCommands(int64(len(roomClients)*2 + 3))
	}

	// 并发发送 Touches/Judges。
	var wg sync.WaitGroup
	sem := make(chan struct{}, bc.Clients)
	stopCh := make(chan struct{})
	var frameIdx atomic.Int32

	for _, cli := range clients {
		wg.Add(1)
		go func(c *benchClient) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				case sem <- struct{}{}:
					idx := int(frameIdx.Add(1)) % len(bundles)
					t0 := time.Now()
					if room := c.user.Room; room != nil {
						room.Mu.Lock()
						hub.ProcessClientCommand(c.user, bundles[idx].touches)
						hub.ProcessClientCommand(c.user, bundles[idx].judges)
						room.Mu.Unlock()
					}
					mc.RecordCmdLatency(time.Since(t0))
					mc.AddCommands(2)
					<-sem
				}
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
				mc.Sample()
				mc.TimelineTick()
			}
		}
	}()

	time.Sleep(bc.Duration)
	close(stopCh)
	sampleTicker.Stop()
	wg.Wait()

	// 提交结果。
	for _, cli := range clients {
		cli.dispatch(protocol.CmdPlayed{ID: int32(cli.userID)})
		mc.AddCommands(1)
	}

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients: bc.Clients, Rooms: bc.Rooms, Duration: bc.Duration, Concurrency: bc.Clients,
	}, elapsed)
	result.Name = "gameplay"
	return result
}

// ---------- 场景：混合负载 ----------

func runMixedScenario(bc benchConfig, mc *benchmetrics.Collector) []benchmetrics.BenchResult {
	type step struct {
		name string
		run  func(benchConfig, *benchmetrics.Collector) benchmetrics.BenchResult
	}
	steps := []step{
		{"room-cycle", runRoomCycleScenario},
		{"gameplay", runGameplayScenario},
		{"steady-state", runSteadyStateScenario},
		{"connection-storm", runConnectionStormScenario},
	}
	results := make([]benchmetrics.BenchResult, 0, len(steps))
	for _, s := range steps {
		stepMC := benchmetrics.NewCollector()
		results = append(results, s.run(bc, stepMC))
	}
	return results
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
