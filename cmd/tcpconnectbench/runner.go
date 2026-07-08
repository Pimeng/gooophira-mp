package main

import (
	"github.com/Pimeng/gooophira-mp/internal/benchmetrics"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"sync"
	"sync/atomic"
	"time"

	"fmt"
)

// runRoomCycleScenario - room lifecycle test (benchmetrics version).
func runRoomCycleScenario(bc benchConfig, mc *benchmetrics.Collector, addr string, vipPool *vipPool) benchmetrics.BenchResult {
	startTime := time.Now()

	// Phase 1: connect + auth all clients
	clients := make([]*tcpClient, 0, bc.Clients)
	{
		var wg sync.WaitGroup
		var clientsMu sync.Mutex
		sem := make(chan struct{}, connCap(bc.Concurrency))

		for i := 0; i < bc.Clients; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				mc.AddConnAttempt()
				vip := vipPool.next()
				cli, connectDur, authDur, err := retryableConnect(id, addr, vip, bc.Verbose)
				if err != nil {
					mc.AddConnFailed()
					mc.RecordError(err)
					return
				}
				mc.AddConnSuccess()
				mc.AddAuthSuccess()
				mc.RecordConnectLatency(connectDur)
				mc.RecordAuthLatency(authDur)
				mc.AddCommands(2)
				mc.AddBytesOut(1024)
				mc.AddPacketOut()
				mc.AddPacketIn()

				clientsMu.Lock()
				clients = append(clients, cli)
				clientsMu.Unlock()
			}(i + 1)
		}
		wg.Wait()
	}

	// Assign clients to rooms
	for r := 0; r < bc.Rooms; r++ {
		roomID := protocol.RoomID(fmt.Sprintf("bench-r%d", r))
		roomClients := assignTCPClients(clients, r, bc.Rooms, len(clients))
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]

		mc.AddRoomCreate()
		_ = host.sendCommand(protocol.CmdCreateRoom{ID: roomID})
		_, _ = host.readFrame()
		for _, cli := range roomClients[1:] {
			mc.AddJoinSuccess()
			_ = cli.sendCommand(protocol.CmdJoinRoom{ID: roomID, Monitor: false})
			_, _ = cli.readFrame()
		}
		_ = host.sendCommand(protocol.CmdSelectChart{ID: 1})
		_, _ = host.readFrame()
		_ = host.sendCommand(protocol.CmdRequestStart{})
		_, _ = host.readFrame()
		for _, cli := range roomClients[1:] {
			_ = cli.sendCommand(protocol.CmdReady{})
			_, _ = cli.readFrame()
		}
		mc.AddCommands(int64(2 + 2*(len(roomClients)-1) + 2))
		mc.AddBytesOut(512 * int64(len(roomClients)))
		mc.SetPeakRooms(int64(r + 1))
	}

	// Phase 2: concurrent Touches -> Played loop
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
					mc.RecordError(err)
					return
				}
				if _, err := c.readFrame(); err != nil {
					mc.RecordError(err)
					return
				}
				if err := c.sendCommand(played); err != nil {
					mc.RecordError(err)
					return
				}
				if _, err := c.readFrame(); err != nil {
					mc.RecordError(err)
					return
				}
				mc.RecordCmdLatency(time.Since(t0))
				mc.AddCommands(2)
				mc.AddBytesOut(256)
				mc.AddBytesIn(256)
				mc.AddPacketOut()
				mc.AddPacketIn()
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

	// Cleanup
	for _, cli := range clients {
		cli.close()
		mc.ConnClose()
	}

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients:     bc.Clients,
		Rooms:       bc.Rooms,
		Duration:    bc.Duration,
		Concurrency: bc.Concurrency,
	}, elapsed)
	result.Name = "room-cycle"

	avgPlayers := float64(len(clients))
	if bc.Rooms > 0 {
		avgPlayers = float64(len(clients)) / float64(bc.Rooms)
	}
	result.Scenario.RoomCycle = &benchmetrics.RoomCycleStats{
		RoomCreateCount:   int64(bc.Rooms),
		JoinSuccess:       int64(len(clients)),
		AvgPlayersPerRoom: avgPlayers,
	}
	return result
}

// runConnectionStormScenario - concurrent connect + auth throughput test.
func runConnectionStormScenario(bc benchConfig, mc *benchmetrics.Collector, addr string, vipPool *vipPool) benchmetrics.BenchResult {
	startTime := time.Now()

	var wg sync.WaitGroup
	sem := make(chan struct{}, connCap(bc.Concurrency))
	var clientsMu sync.Mutex
	clients := make([]*tcpClient, 0, bc.Clients)

	stopCh := make(chan struct{})
	sampleTicker := time.NewTicker(1 * time.Second)
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-sampleTicker.C:
				mc.Sample()
				mc.TimelineTick()
			}
		}
	}()

	for i := 0; i < bc.Clients; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }()

			mc.AddConnAttempt()
			vip := vipPool.next()
			cli, connectDur, authDur, err := retryableConnect(id, addr, vip, false)
			if err != nil {
				mc.AddConnFailed()
				mc.RecordError(err)
				return
			}
			mc.AddConnSuccess()
			mc.AddAuthSuccess()
			mc.RecordConnectLatency(connectDur)
			mc.RecordAuthLatency(authDur)
			mc.AddCommands(2)
			mc.AddBytesOut(1024)

			clientsMu.Lock()
			clients = append(clients, cli)
			clientsMu.Unlock()
		}(i + 1)
	}
	wg.Wait()

	mc.Sample()

	if remaining := bc.Duration - time.Since(startTime); remaining > 0 {
		time.Sleep(remaining)
	}

	close(stopCh)
	sampleTicker.Stop()

	clientsMu.Lock()
	for _, cli := range clients {
		cli.close()
		mc.ConnClose()
	}
	clientsMu.Unlock()

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients:     bc.Clients,
		Rooms:       bc.Rooms,
		Duration:    bc.Duration,
		Concurrency: bc.Concurrency,
	}, elapsed)
	result.Name = "connection-storm"
	return result
}

// runSteadyStateScenario - steady ping stream with fixed connections.
func runSteadyStateScenario(bc benchConfig, mc *benchmetrics.Collector, addr string, vipPool *vipPool) benchmetrics.BenchResult {
	startTime := time.Now()

	clients := make([]*tcpClient, 0, bc.Clients)
	{
		var wg sync.WaitGroup
		var clientsMu sync.Mutex
		sem := make(chan struct{}, connCap(bc.Concurrency))

		for i := 0; i < bc.Clients; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				mc.AddConnAttempt()
				vip := vipPool.next()
				cli, connectDur, _, err := retryableConnect(id, addr, vip, bc.Verbose)
				if err != nil {
					mc.AddConnFailed()
					mc.RecordError(err)
					return
				}
				mc.AddConnSuccess()
				mc.RecordConnectLatency(connectDur)
				mc.AddCommands(2)

				clientsMu.Lock()
				clients = append(clients, cli)
				clientsMu.Unlock()
			}(i + 1)
		}
		wg.Wait()
	}

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
					mc.RecordError(err)
					return
				}
				if _, err := c.readFrame(); err != nil {
					mc.RecordError(err)
					return
				}
				mc.RecordCmdLatency(time.Since(t0))
				mc.AddCommands(1)
				mc.AddBytesOut(64)
				mc.AddBytesIn(64)
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

	for _, cli := range clients {
		cli.close()
		mc.ConnClose()
	}

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients:     bc.Clients,
		Rooms:       bc.Rooms,
		Duration:    bc.Duration,
		Concurrency: bc.Concurrency,
	}, elapsed)
	result.Name = "steady-state"
	return result
}

// runGameplayScenario - high-frequency Touches/Judges frame test.
func runGameplayScenario(bc benchConfig, mc *benchmetrics.Collector, addr string, vipPool *vipPool) benchmetrics.BenchResult {
	startTime := time.Now()

	// Pre-generate frame data
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

	// Phase 1: connect + auth all clients
	clients := make([]*tcpClient, 0, bc.Clients)
	{
		var wg sync.WaitGroup
		var clientsMu sync.Mutex
		sem := make(chan struct{}, connCap(bc.Concurrency))

		for i := 0; i < bc.Clients; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				mc.AddConnAttempt()
				vip := vipPool.next()
				cli, _, _, err := retryableConnect(id, addr, vip, bc.Verbose)
				if err != nil {
					mc.AddConnFailed()
					mc.RecordError(err)
					return
				}
				mc.AddConnSuccess()
				mc.AddCommands(2)

				clientsMu.Lock()
				clients = append(clients, cli)
				clientsMu.Unlock()
			}(i + 1)
		}
		wg.Wait()
	}

	// Phase 2: all rooms enter Playing state
	for r := 0; r < bc.Rooms; r++ {
		roomID := protocol.RoomID(fmt.Sprintf("gm-r%d", r))
		roomClients := assignTCPClients(clients, r, bc.Rooms, len(clients))
		if len(roomClients) == 0 {
			continue
		}
		host := roomClients[0]

		_ = host.sendCommand(protocol.CmdCreateRoom{ID: roomID})
		_, _ = host.readFrame()
		for _, cli := range roomClients[1:] {
			_ = cli.sendCommand(protocol.CmdJoinRoom{ID: roomID, Monitor: false})
			_, _ = cli.readFrame()
		}
		_ = host.sendCommand(protocol.CmdSelectChart{ID: 1})
		_, _ = host.readFrame()
		_ = host.sendCommand(protocol.CmdRequestStart{})
		_, _ = host.readFrame()
		for _, cli := range roomClients {
			_ = cli.sendCommand(protocol.CmdReady{})
			_, _ = cli.readFrame()
		}
		mc.AddCommands(int64(len(roomClients)*2 + 3))
	}

	// Phase 3: concurrent Touches/Judges
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
					mc.RecordError(err)
					return
				}
				if err := c.sendCommand(bundles[idx].judges); err != nil {
					mc.RecordError(err)
					return
				}

				mc.RecordCmdLatency(time.Since(t0))
				mc.AddCommands(2)
				mc.AddBytesOut(512)
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

	// Phase 4: submit results
	for _, cli := range clients {
		_ = cli.sendCommand(protocol.CmdPlayed{ID: int32(cli.id)})
		_, _ = cli.readFrame()
		mc.AddCommands(1)
	}

	for _, cli := range clients {
		cli.close()
		mc.ConnClose()
	}

	elapsed := time.Since(startTime)
	result := mc.Snap(benchmetrics.BenchRunConfig{
		Clients:     bc.Clients,
		Rooms:       bc.Rooms,
		Duration:    bc.Duration,
		Concurrency: bc.Concurrency,
	}, elapsed)
	result.Name = "gameplay"
	return result
}
