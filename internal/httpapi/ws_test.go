package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/coder/websocket"
)

func dialWS(t *testing.T, svc *Service) (*websocket.Conn, context.Context) {
	t.Helper()
	addr, err := svc.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr.String()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return conn, ctx
}

func wsWrite(t *testing.T, conn *websocket.Conn, ctx context.Context, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

// wsReadUntil 读取直到收到 type==want 的消息，返回其解码后的 map。
func wsReadUntil(t *testing.T, conn *websocket.Conn, want string) map[string]any {
	t.Helper()
	for range 10 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("ws read (waiting %q): %v", want, err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m["type"] == want {
			return m
		}
	}
	t.Fatalf("did not receive %q within 10 messages", want)
	return nil
}

func TestWS_PingPong(t *testing.T) {
	svc, _ := newTestService(t, nil)
	conn, ctx := dialWS(t, svc)
	defer conn.Close(websocket.StatusNormalClosure, "")
	defer svc.Close()

	wsWrite(t, conn, ctx, map[string]any{"type": "ping"})
	wsReadUntil(t, conn, "pong")
}

func TestWS_RoomSubscribeAndUpdate(t *testing.T) {
	svc, state := newTestService(t, nil)
	addRoom(state, "room1", 1, "alice", &config.Chart{ID: 42, Name: "g.r.i.s"})
	conn, ctx := dialWS(t, svc)
	defer conn.Close(websocket.StatusNormalClosure, "")
	defer svc.Close()

	// 订阅不存在的房间 → error。
	wsWrite(t, conn, ctx, map[string]any{"type": "subscribe", "roomId": "nope"})
	if m := wsReadUntil(t, conn, "error"); m["message"] != "room-not-found" {
		t.Fatalf("expected room-not-found, got %v", m)
	}

	// 订阅存在的房间 → subscribed + 首帧 room_update。
	wsWrite(t, conn, ctx, map[string]any{"type": "subscribe", "roomId": "room1"})
	upd := wsReadUntil(t, conn, "room_update")
	data, _ := upd["data"].(map[string]any)
	if data["roomid"] != "room1" {
		t.Fatalf("room_update data = %v", data)
	}

	// 触发一次房间变更广播 → 再收到 room_update。
	state.Mu.Lock()
	state.WSService.BroadcastRoomUpdate("room1")
	state.Mu.Unlock()
	wsReadUntil(t, conn, "room_update")
}

func TestWS_ConsoleSubscribe(t *testing.T) {
	cfg := &config.ServerConfig{AdminToken: sp("secret")}
	svc, state := newTestService(t, cfg)
	state.ConsoleHub.Push("INFO", "before-subscribe")
	conn, ctx := dialWS(t, svc)
	defer conn.Close(websocket.StatusNormalClosure, "")
	defer svc.Close()

	// 错误 token → unauthorized。
	wsWrite(t, conn, ctx, map[string]any{"type": "console_subscribe", "token": "wrong"})
	if m := wsReadUntil(t, conn, "error"); m["message"] != "unauthorized" {
		t.Fatalf("expected unauthorized, got %v", m)
	}

	// 正确 token → console_subscribed，data.lines 含已有快照。
	wsWrite(t, conn, ctx, map[string]any{"type": "console_subscribe", "token": "secret"})
	sub := wsReadUntil(t, conn, "console_subscribed")
	data, _ := sub["data"].(map[string]any)
	lines, _ := data["lines"].([]any)
	if len(lines) == 0 {
		t.Fatalf("console_subscribed should backfill recent logs, got %v", data)
	}
	first, _ := lines[0].(map[string]any)
	if first["message"] != "before-subscribe" || first["level"] != "INFO" {
		t.Fatalf("snapshot line shape wrong: %v", first)
	}

	// 订阅后推送的日志应实时收到 console_log。
	state.ConsoleHub.Push("WARN", "after-subscribe")
	logMsg := wsReadUntil(t, conn, "console_log")
	ld, _ := logMsg["data"].(map[string]any)
	if ld["message"] != "after-subscribe" || ld["level"] != "WARN" {
		t.Fatalf("console_log data wrong: %v", ld)
	}
	if _, ok := ld["timestamp"]; !ok {
		t.Error("console_log line should carry timestamp")
	}

	// 退订 → console_unsubscribed，之后不再收到 console_log。
	wsWrite(t, conn, ctx, map[string]any{"type": "console_unsubscribe"})
	wsReadUntil(t, conn, "console_unsubscribed")
}

func TestWS_AdminSubscribe(t *testing.T) {
	cfg := &config.ServerConfig{AdminToken: sp("secret")}
	svc, state := newTestService(t, cfg)
	addRoom(state, "room1", 1, "alice", nil)
	conn, ctx := dialWS(t, svc)
	defer conn.Close(websocket.StatusNormalClosure, "")
	defer svc.Close()

	// 错误 token → unauthorized。
	wsWrite(t, conn, ctx, map[string]any{"type": "admin_subscribe", "token": "wrong"})
	if m := wsReadUntil(t, conn, "error"); m["message"] != "unauthorized" {
		t.Fatalf("expected unauthorized, got %v", m)
	}

	// 正确 token → admin_subscribed + admin_update 快照。
	wsWrite(t, conn, ctx, map[string]any{"type": "admin_subscribe", "token": "secret"})
	wsReadUntil(t, conn, "admin_subscribed")
	snap := wsReadUntil(t, conn, "admin_update")
	data, _ := snap["data"].(map[string]any)
	changes, _ := data["changes"].(map[string]any)
	if changes["total_rooms"].(float64) != 1 {
		t.Fatalf("admin snapshot total_rooms = %v", changes["total_rooms"])
	}
}
