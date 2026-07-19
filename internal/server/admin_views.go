package server

import (
	"math"
	"sort"
	"strconv"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// 管理员视图数据：供 HTTP /admin/rooms 与（后续）WebSocket admin 推送共用。
// 带 JSON 标签，httpapi 直接序列化。对应 TS game/adminViews.ts。

// AdminRoomState 是房间状态的详细视图。
type AdminRoomState struct {
	Type          string `json:"type"`
	ReadyUsers    []int  `json:"ready_users,omitempty"`
	ReadyCount    *int   `json:"ready_count,omitempty"`
	ResultsCount  *int   `json:"results_count,omitempty"`
	AbortedCount  *int   `json:"aborted_count,omitempty"`
	FinishedUsers []int  `json:"finished_users,omitempty"`
	AbortedUsers  []int  `json:"aborted_users,omitempty"`
}

// AdminUserView 是房间内一名玩家的详细视图。
type AdminUserView struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	IsHost    bool   `json:"is_host"`
	GameTime  any    `json:"game_time"` // 负无穷转换为 null。
	Language  string `json:"language"`
	Finished  *bool  `json:"finished,omitempty"`
	Aborted   *bool  `json:"aborted,omitempty"`
	RecordID  *int   `json:"record_id,omitempty"`
}

// AdminMonitorView 是房间内一名观战者的视图。
type AdminMonitorView struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Language  string `json:"language"`
}

// AdminChart 是谱面视图。
type AdminChart struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// AdminHost 是房主视图。
type AdminHost struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// AdminContest 是比赛配置视图。
type AdminContest struct {
	WhitelistCount int   `json:"whitelist_count"`
	Whitelist      []int `json:"whitelist"`
	ManualStart    bool  `json:"manual_start"`
	AutoDisband    bool  `json:"auto_disband"`
}

// AdminLog 是一条房间日志视图。
type AdminLog struct {
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// AdminRoomData 是单个房间的完整管理视图。
type AdminRoomData struct {
	RoomID          string             `json:"roomid"`
	MaxUsers        int                `json:"max_users"`
	CurrentUsers    int                `json:"current_users"`
	CurrentMonitors int                `json:"current_monitors"`
	ReplayEligible  bool               `json:"replay_eligible"`
	Live            bool               `json:"live"`
	Locked          bool               `json:"locked"`
	Cycle           bool               `json:"cycle"`
	Host            AdminHost          `json:"host"`
	State           AdminRoomState     `json:"state"`
	Chart           *AdminChart        `json:"chart"`
	Contest         *AdminContest      `json:"contest"`
	Users           []AdminUserView    `json:"users"`
	Monitors        []AdminMonitorView `json:"monitors"`
	RecentLogs      []AdminLog         `json:"recent_logs"`
}

func adminStateString(st InternalRoomState) string {
	switch st.(type) {
	case StatePlaying:
		return "playing"
	case StateWaitForReady:
		return "waiting_for_ready"
	default:
		return "select_chart"
	}
}

func sortedKeys[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func gameTimeJSON(t float64) any {
	if math.IsInf(t, -1) || math.IsNaN(t) {
		return nil
	}
	return t
}

func (s *ServerState) lang(u *User) string {
	if u != nil && u.Lang != nil {
		return u.Lang.Tag
	}
	return "unknown"
}

// buildAdminRoom 组装单房间管理视图（调用方须持 Mu）。
func (s *ServerState) buildAdminRoom(id protocol.RoomID, room *Room) AdminRoomData {
	host := s.Users[room.HostID]
	hostName := strconv.Itoa(room.HostID)
	hostConnected := false
	if host != nil {
		hostName = host.Name
		hostConnected = host.IsConnected()
	}

	stateView := AdminRoomState{Type: adminStateString(room.State)}
	switch st := room.State.(type) {
	case StateWaitForReady:
		ready := sortedKeys(st.Started)
		cnt := len(ready)
		stateView.ReadyUsers = ready
		stateView.ReadyCount = &cnt
	case StatePlaying:
		rc, ac := len(st.Results), len(st.Aborted)
		stateView.ResultsCount = &rc
		stateView.AbortedCount = &ac
		stateView.FinishedUsers = sortedKeys(st.Results)
		stateView.AbortedUsers = sortedKeys(st.Aborted)
	}

	users := make([]AdminUserView, 0, room.UserCount())
	for _, uid := range room.UserIDs() {
		u := s.Users[uid]
		connected := false
		if u != nil {
			connected = u.IsConnected()
		}
		uv := AdminUserView{
			ID: uid, Name: nameOrID(u, uid), Connected: connected,
			IsHost: uid == room.HostID, GameTime: gameTimeJSON(gameTimeOf(u)), Language: s.lang(u),
		}
		if st, ok := room.State.(StatePlaying); ok {
			_, fin := st.Results[uid]
			_, ab := st.Aborted[uid]
			done := fin || ab
			uv.Finished = &done
			uv.Aborted = &ab
			if fin {
				rid := st.Results[uid].ID
				uv.RecordID = &rid
			}
		}
		users = append(users, uv)
	}

	monitors := make([]AdminMonitorView, 0, room.MonitorCount())
	for _, mid := range room.MonitorIDs() {
		u := s.Users[mid]
		connected := false
		if u != nil {
			connected = u.IsConnected()
		}
		monitors = append(monitors, AdminMonitorView{
			ID: mid, Name: nameOrID(u, mid), Connected: connected, Language: s.lang(u),
		})
	}

	var chart *AdminChart
	if room.Chart != nil {
		chart = &AdminChart{Name: room.Chart.Name, ID: room.Chart.ID}
	}
	var contest *AdminContest
	if room.Contest != nil {
		contest = &AdminContest{
			WhitelistCount: len(room.Contest.Whitelist), Whitelist: sortedKeys(room.Contest.Whitelist),
			ManualStart: room.Contest.ManualStart, AutoDisband: room.Contest.AutoDisband,
		}
	}

	logs := room.GetRecentLogs()
	recent := make([]AdminLog, len(logs))
	for i, l := range logs {
		recent[i] = AdminLog{Message: l.Message, Timestamp: l.Timestamp}
	}

	return AdminRoomData{
		RoomID: string(id), MaxUsers: room.MaxUsers, CurrentUsers: len(users), CurrentMonitors: len(monitors),
		ReplayEligible: room.ReplayEligible, Live: room.Live, Locked: room.Locked, Cycle: room.Cycle,
		Host:  AdminHost{ID: room.HostID, Name: hostName, Connected: hostConnected},
		State: stateView, Chart: chart, Contest: contest, Users: users, Monitors: monitors, RecentLogs: recent,
	}
}

// BuildAdminRooms 返回所有房间的管理视图（按 roomid 升序）。调用方须持 Mu。
func (s *ServerState) BuildAdminRooms() []AdminRoomData {
	out := make([]AdminRoomData, 0, len(s.Rooms))
	for id, room := range s.Rooms {
		out = append(out, s.buildAdminRoom(id, room))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RoomID < out[j].RoomID })
	return out
}

// ---------- WebSocket 房间增量推送视图（RoomUpdateData） ----------

// RoomUpdateUser 是房间增量推送里的玩家。
type RoomUpdateUser struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IsReady   bool   `json:"is_ready"`
	Connected bool   `json:"connected"`
}

// RoomUpdateMonitor 是房间增量推送里的观战者。
type RoomUpdateMonitor struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// RoomUpdateHost 是房间增量推送里的房主。
type RoomUpdateHost struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// RoomUpdateData 是 WebSocket room_update 推送的数据。
type RoomUpdateData struct {
	RoomID     string              `json:"roomid"`
	State      string              `json:"state"`
	Locked     bool                `json:"locked"`
	Cycle      bool                `json:"cycle"`
	Live       bool                `json:"live"`
	Chart      *AdminChart         `json:"chart"`
	Host       RoomUpdateHost      `json:"host"`
	Users      []RoomUpdateUser    `json:"users"`
	Monitors   []RoomUpdateMonitor `json:"monitors"`
	RecentLogs []AdminLog          `json:"recent_logs"`
}

// BuildRoomUpdate 组装某房间的增量推送视图；房间不存在返回 nil。调用方须持 Mu。
func (s *ServerState) BuildRoomUpdate(id protocol.RoomID) *RoomUpdateData {
	room := s.Rooms[id]
	if room == nil {
		return nil
	}
	host := s.Users[room.HostID]
	hostName := nameOrID(host, room.HostID)
	hostConnected := false
	if host != nil {
		hostConnected = host.IsConnected()
	}
	// 拷贝 Started 集合：原集合是 StateWaitForReady 的内部字段（map 引用语义），
	// 直接别名会让外部观察者修改到房间内部状态。
	started := map[int]struct{}{}
	if st, ok := room.State.(StateWaitForReady); ok {
		for id := range st.Started {
			started[id] = struct{}{}
		}
	}
	users := make([]RoomUpdateUser, 0, room.UserCount())
	for _, uid := range room.UserIDs() {
		u := s.Users[uid]
		connected := false
		if u != nil {
			connected = u.IsConnected()
		}
		_, ready := started[uid]
		users = append(users, RoomUpdateUser{ID: uid, Name: nameOrID(u, uid), IsReady: ready, Connected: connected})
	}
	monitors := make([]RoomUpdateMonitor, 0, room.MonitorCount())
	for _, mid := range room.MonitorIDs() {
		u := s.Users[mid]
		connected := false
		if u != nil {
			connected = u.IsConnected()
		}
		monitors = append(monitors, RoomUpdateMonitor{ID: mid, Name: nameOrID(u, mid), Connected: connected})
	}
	var chart *AdminChart
	if room.Chart != nil {
		chart = &AdminChart{Name: room.Chart.Name, ID: room.Chart.ID}
	}
	logs := room.GetRecentLogs()
	recent := make([]AdminLog, len(logs))
	for i, l := range logs {
		recent[i] = AdminLog{Message: l.Message, Timestamp: l.Timestamp}
	}
	return &RoomUpdateData{
		RoomID: string(id), State: adminStateString(room.State), Locked: room.Locked,
		Cycle: room.Cycle, Live: room.Live, Chart: chart,
		Host:  RoomUpdateHost{ID: room.HostID, Name: hostName, Connected: hostConnected},
		Users: users, Monitors: monitors, RecentLogs: recent,
	}
}

// AdminOnlineUser 是在线用户的管理视图。
type AdminOnlineUser struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Monitor   bool   `json:"monitor"`
	Room      string `json:"room"`
	Banned    bool   `json:"banned"`
	Language  string `json:"language"`
}

// BuildOnlineUsers 返回所有在线用户的管理视图（按 id 升序）。调用方须持 Mu。
func (s *ServerState) BuildOnlineUsers() []AdminOnlineUser {
	out := make([]AdminOnlineUser, 0, len(s.Users))
	for id, u := range s.Users {
		room := ""
		if u.Room != nil {
			room = string(u.Room.ID)
		}
		connected := false
		connected = u.IsConnected()
		_, banned := s.BannedUsers[id]
		out = append(out, AdminOnlineUser{
			ID: id, Name: u.Name, Connected: connected, Monitor: u.Monitor,
			Room: room, Banned: banned, Language: s.lang(u),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func gameTimeOf(u *User) float64 {
	if u == nil {
		return math.Inf(-1)
	}
	return u.GameTime()
}

func nameOrID(u *User, id int) string {
	if u != nil {
		return u.Name
	}
	return strconv.Itoa(id)
}
