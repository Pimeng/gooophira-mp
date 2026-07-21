package server

import (
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

func touchFrame(tm float32) protocol.TouchFrame {
	return protocol.TouchFrame{Time: tm, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.5}}}}
}

func TestMonitorBuffer_MergesFramesPerPlayer(t *testing.T) {
	h := newHarness(200)
	room := NewRoom("room1", 1, 8, false)
	mon := h.addUser(200, "mon")
	if !room.AddUser(mon, true) {
		t.Fatal("monitor should be addable")
	}

	b := NewMonitorBuffer()
	// 同一玩家(7)两批帧 → 应合并为一条 SrvTouches。
	b.BufferTouches(room, 7, []protocol.TouchFrame{touchFrame(1)})
	b.BufferTouches(room, 7, []protocol.TouchFrame{touchFrame(2), touchFrame(3)})
	b.Flush()

	sent := sentTo(mon)
	var touches []protocol.SrvTouches
	for _, c := range sent {
		if tc, ok := c.(protocol.SrvTouches); ok {
			touches = append(touches, tc)
		}
	}
	if len(touches) != 1 {
		t.Fatalf("expected 1 merged SrvTouches, got %d (%v)", len(touches), sent)
	}
	if touches[0].Player != 7 || len(touches[0].Frames) != 3 {
		t.Errorf("merged touches = player %d, %d frames; want player 7, 3 frames", touches[0].Player, len(touches[0].Frames))
	}
}

func TestMonitorBuffer_NoMonitorsNoSend(t *testing.T) {
	_ = newHarness()
	room := NewRoom("room1", 1, 8, false)
	b := NewMonitorBuffer()
	// 调用方已在 room.Mu 外检查 monitor count，缓冲层不再重复检查。
	b.BufferTouches(room, 7, []protocol.TouchFrame{touchFrame(1)})
	b.Flush()
	b.mu.Lock()
	n := len(b.touch)
	b.mu.Unlock()
	if n != 0 {
		t.Errorf("flush should drain buffered frames, got %d remaining", n)
	}
}

func TestMonitorBuffer_SeparatePlayersSeparateCommands(t *testing.T) {
	h := newHarness(200)
	room := NewRoom("room1", 1, 8, false)
	mon := h.addUser(200, "mon")
	room.AddUser(mon, true)

	b := NewMonitorBuffer()
	b.BufferTouches(room, 7, []protocol.TouchFrame{touchFrame(1)})
	b.BufferTouches(room, 8, []protocol.TouchFrame{touchFrame(2)})
	b.Flush()

	players := map[int32]bool{}
	for _, c := range sentTo(mon) {
		if tc, ok := c.(protocol.SrvTouches); ok {
			players[tc.Player] = true
		}
	}
	if !players[7] || !players[8] {
		t.Errorf("expected separate commands for players 7 and 8, got %v", players)
	}
}

func TestMonitorBuffer_JudgesMerge(t *testing.T) {
	h := newHarness(200)
	room := NewRoom("room1", 1, 8, false)
	mon := h.addUser(200, "mon")
	room.AddUser(mon, true)

	b := NewMonitorBuffer()
	b.BufferJudges(room, 7, []protocol.JudgeEvent{{Time: 1, LineID: 0, NoteID: 0, Judgement: 1}})
	b.BufferJudges(room, 7, []protocol.JudgeEvent{{Time: 2, LineID: 0, NoteID: 1, Judgement: 1}})
	b.Flush()

	var judges []protocol.SrvJudges
	for _, c := range sentTo(mon) {
		if jc, ok := c.(protocol.SrvJudges); ok {
			judges = append(judges, jc)
		}
	}
	if len(judges) != 1 || len(judges[0].Judges) != 2 {
		t.Fatalf("expected 1 merged SrvJudges with 2 events, got %v", judges)
	}
}

func TestMonitorBuffer_StopFlushesAndIsIdempotent(t *testing.T) {
	h := newHarness(200)
	room := NewRoom("room1", 1, 8, false)
	mon := h.addUser(200, "mon")
	room.AddUser(mon, true)

	b := NewMonitorBuffer()
	b.BufferTouches(room, 7, []protocol.TouchFrame{touchFrame(1)})
	b.Stop() // 应刷写残留
	b.Stop() // 二次调用不应 panic

	got := 0
	for _, c := range sentTo(mon) {
		if _, ok := c.(protocol.SrvTouches); ok {
			got++
		}
	}
	if got != 1 {
		t.Errorf("Stop should flush pending frames, got %d touches", got)
	}
}
