package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
	"github.com/coder/websocket"
)

// wsHub 管理 WebSocket 客户端与订阅，并实现 server.WebSocketService（房间/管理面板实时推送）。
// 对应 TS network/websocketService.ts。
type wsHub struct {
	svc *Service

	mu       sync.Mutex
	clients  map[*wsClient]struct{}
	roomSubs map[protocol.RoomID]map[*wsClient]struct{}
	admins   map[*wsClient]struct{}

	adminTimer   *time.Timer
	adminPending bool
}

const (
	wsSendBuffer     = 64
	adminDebounceDur = 100 * time.Millisecond
)

func newWSHub(svc *Service) *wsHub {
	return &wsHub{
		svc:      svc,
		clients:  make(map[*wsClient]struct{}),
		roomSubs: make(map[protocol.RoomID]map[*wsClient]struct{}),
		admins:   make(map[*wsClient]struct{}),
	}
}

// 确保 wsHub 满足 server.WebSocketService。
var _ server.WebSocketService = (*wsHub)(nil)

type wsClient struct {
	conn      *websocket.Conn
	hub       *wsHub
	ip        string
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once

	mu           sync.Mutex // 保护订阅字段
	room         protocol.RoomID
	hasRoom      bool
	isAdmin      bool
	consoleUnsub func() // 非 nil 表示已订阅控制台日志频道
}

// ---------- 升级与生命周期 ----------

func (h *wsHub) handle(w http.ResponseWriter, r *http.Request, ip string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c := &wsClient{conn: conn, hub: h, ip: ip, send: make(chan []byte, wsSendBuffer), done: make(chan struct{})}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	ctx := context.Background()
	go c.writeLoop(ctx)
	c.readLoop(ctx) // 阻塞至连接结束
	c.close()
	c.unsubscribeConsole() // 清理控制台日志订阅（在 hub 锁外）
	h.remove(c)
}

// clientCount 返回当前 WebSocket 连接数。
func (h *wsHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *wsHub) remove(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	delete(h.admins, c)
	c.mu.Lock()
	if c.hasRoom {
		if set := h.roomSubs[c.room]; set != nil {
			delete(set, c)
			if len(set) == 0 {
				delete(h.roomSubs, c.room)
			}
		}
	}
	c.mu.Unlock()
}

func (c *wsClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.CloseNow() // 立即关闭，不做关闭握手等待（避免慢客户端/关停时阻塞）
	})
}

func (c *wsClient) enqueue(b []byte) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.send <- b:
	default:
		c.close() // 慢客户端
	}
}

func (c *wsClient) writeLoop(ctx context.Context) {
	for {
		select {
		case <-c.done:
			return
		case b := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.close()
				return
			}
		}
	}
}

// ---------- 入站消息 ----------

type wsMessage struct {
	Type   string `json:"type"`
	RoomID string `json:"roomId"`
	UserID *int   `json:"userId"`
	Token  string `json:"token"`
}

func (c *wsClient) readLoop(ctx context.Context) {
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText || len(data) > 64*1024 {
			continue
		}
		var msg wsMessage
		if json.Unmarshal(data, &msg) != nil {
			c.sendJSON(map[string]any{"type": "error", "message": "invalid-message"})
			continue
		}
		c.handleMessage(msg)
	}
}

func (c *wsClient) handleMessage(msg wsMessage) {
	h := c.hub
	switch msg.Type {
	case "ping":
		c.sendJSON(map[string]any{"type": "pong"})
	case "subscribe":
		c.subscribeRoom(msg.RoomID)
	case "unsubscribe":
		c.unsubscribeRoom()
		c.sendJSON(map[string]any{"type": "unsubscribed"})
	case "admin_subscribe":
		if !h.svc.verifyAdminToken(msg.Token, c.ip) {
			c.sendJSON(map[string]any{"type": "error", "message": "unauthorized"})
			return
		}
		h.mu.Lock()
		h.admins[c] = struct{}{}
		h.mu.Unlock()
		c.mu.Lock()
		c.isAdmin = true
		c.mu.Unlock()
		c.sendJSON(map[string]any{"type": "admin_subscribed"})
		c.svcSendAdminSnapshot()
	case "admin_unsubscribe":
		h.mu.Lock()
		delete(h.admins, c)
		h.mu.Unlock()
		c.mu.Lock()
		c.isAdmin = false
		c.mu.Unlock()
		c.sendJSON(map[string]any{"type": "admin_unsubscribed"})
	case "console_subscribe":
		// 与 admin_subscribe 相同的管理员鉴权（对齐 TS websocketService）。
		if !h.svc.verifyAdminToken(msg.Token, c.ip) {
			c.sendJSON(map[string]any{"type": "error", "message": "unauthorized"})
			return
		}
		c.subscribeConsole()
	case "console_unsubscribe":
		c.unsubscribeConsole()
		c.sendJSON(map[string]any{"type": "console_unsubscribed"})
	}
}

// subscribeConsole 注册控制台日志频道：先回填最近日志快照（先于任何实时行入队），
// 再注册实时订阅，把后续每条日志作为 console_log 推送给本客户端。
func (c *wsClient) subscribeConsole() {
	hub := c.hub.svc.state.ConsoleHub
	if hub == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "console-unavailable"})
		return
	}
	// 回填快照先入队，保证其先于实时行（GUI 收到 console_subscribed 时会清屏重填）。
	c.sendJSON(map[string]any{"type": "console_subscribed", "data": map[string]any{"lines": hub.GetRecent(0)}})

	unsub := hub.Subscribe(func(line server.ConsoleLogLine) {
		c.sendJSON(map[string]any{"type": "console_log", "data": line})
	})
	c.mu.Lock()
	old := c.consoleUnsub // 重复订阅容错：保留新订阅，退订旧的
	c.consoleUnsub = unsub
	c.mu.Unlock()
	if old != nil {
		old()
	}
}

// unsubscribeConsole 退订控制台日志频道（幂等）。
func (c *wsClient) unsubscribeConsole() {
	c.mu.Lock()
	unsub := c.consoleUnsub
	c.consoleUnsub = nil
	c.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

func (c *wsClient) subscribeRoom(roomIDStr string) {
	rid, err := protocol.ParseRoomID(roomIDStr)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "message": "invalid-room-id"})
		return
	}
	h := c.hub
	// 校验房间存在并取首帧数据。
	h.svc.state.Mu.Lock()
	data := h.svc.state.BuildRoomUpdate(rid)
	h.svc.state.Mu.Unlock()
	if data == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "room-not-found"})
		return
	}

	c.unsubscribeRoom()
	h.mu.Lock()
	set := h.roomSubs[rid]
	if set == nil {
		set = make(map[*wsClient]struct{})
		h.roomSubs[rid] = set
	}
	set[c] = struct{}{}
	h.mu.Unlock()
	c.mu.Lock()
	c.room, c.hasRoom = rid, true
	c.mu.Unlock()

	c.sendJSON(map[string]any{"type": "subscribed", "roomId": roomIDStr})
	c.sendJSON(map[string]any{"type": "room_update", "data": data})
}

func (c *wsClient) unsubscribeRoom() {
	c.mu.Lock()
	rid, has := c.room, c.hasRoom
	c.room, c.hasRoom = "", false
	c.mu.Unlock()
	if !has {
		return
	}
	c.hub.mu.Lock()
	if set := c.hub.roomSubs[rid]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(c.hub.roomSubs, rid)
		}
	}
	c.hub.mu.Unlock()
}

func (c *wsClient) sendJSON(v any) {
	if b, err := json.Marshal(v); err == nil {
		c.enqueue(b)
	}
}

func (c *wsClient) svcSendAdminSnapshot() {
	c.hub.svc.state.Mu.Lock()
	rooms := c.hub.svc.state.BuildAdminRooms()
	c.hub.svc.state.Mu.Unlock()
	c.sendJSON(map[string]any{
		"type": "admin_update",
		"data": map[string]any{"timestamp": time.Now().UnixMilli(), "changes": map[string]any{"rooms": rooms, "total_rooms": len(rooms)}},
	})
}

// ---------- server.WebSocketService 实现（dispatch 在 state.Mu 下调用） ----------

// BroadcastRoomUpdate 向订阅了该房间的客户端推送增量。调用方须持 state.Mu。
func (h *wsHub) BroadcastRoomUpdate(roomID protocol.RoomID) {
	h.mu.Lock()
	subs := h.roomSubs[roomID]
	if len(subs) == 0 {
		h.mu.Unlock()
		return
	}
	targets := make([]*wsClient, 0, len(subs))
	for c := range subs {
		targets = append(targets, c)
	}
	h.mu.Unlock()

	data := h.svc.state.BuildRoomUpdate(roomID) // 调用方持有 state.Mu
	if data == nil {
		return
	}
	b, err := json.Marshal(map[string]any{"type": "room_update", "data": data})
	if err != nil {
		return
	}
	for _, c := range targets {
		c.enqueue(b)
	}
}

// BroadcastAdminUpdate 防抖后向管理订阅者推送全量房间数据。调用方须持 state.Mu。
func (h *wsHub) BroadcastAdminUpdate() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.admins) == 0 {
		return
	}
	h.adminPending = true
	if h.adminTimer != nil {
		return
	}
	h.adminTimer = time.AfterFunc(adminDebounceDur, h.flushAdminUpdate)
}

func (h *wsHub) flushAdminUpdate() {
	h.mu.Lock()
	h.adminTimer = nil
	if !h.adminPending || len(h.admins) == 0 {
		h.adminPending = false
		h.mu.Unlock()
		return
	}
	h.adminPending = false
	targets := make([]*wsClient, 0, len(h.admins))
	for c := range h.admins {
		targets = append(targets, c)
	}
	h.mu.Unlock()

	h.svc.state.Mu.Lock()
	rooms := h.svc.state.BuildAdminRooms()
	h.svc.state.Mu.Unlock()
	b, err := json.Marshal(map[string]any{
		"type": "admin_update",
		"data": map[string]any{"timestamp": time.Now().UnixMilli(), "changes": map[string]any{"rooms": rooms, "total_rooms": len(rooms)}},
	})
	if err != nil {
		return
	}
	for _, c := range targets {
		c.enqueue(b)
	}
}

func (h *wsHub) closeAll() {
	h.mu.Lock()
	if h.adminTimer != nil {
		h.adminTimer.Stop()
		h.adminTimer = nil
	}
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.close()
	}
}
