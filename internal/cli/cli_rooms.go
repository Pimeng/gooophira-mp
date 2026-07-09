// cli_rooms.go 把「房间 / 广播」类管理命令从 cli.go 拆出：列房、广播、对房发言、
// 解散、设最大人数、锁定/循环、转移房主、查看房间详情、指定下一轮房主。
package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

func (c *Console) cmdListRooms() {
	c.state.Mu.Lock()
	rooms := make(map[protocol.RoomID]*server.Room, len(c.state.Rooms))
	for id, room := range c.state.Rooms {
		rooms[id] = room
	}
	c.state.Mu.Unlock()
	if len(rooms) == 0 {
		c.printInfo(c.t("cli-no-rooms", nil))
		return
	}
	c.print("")
	c.print(c.t("cli-rooms-total", map[string]string{"count": strconv.Itoa(len(rooms))}))
	for id, room := range rooms {
		room.Mu.Lock()
		chart := c.t("cli-none", nil)
		if room.Chart != nil {
			chart = room.Chart.Name
		}
		state := room.State
		userCount := room.UserCount()
		monitorCount := room.MonitorCount()
		maxUsers := room.MaxUsers
		locked := room.Locked
		cycle := room.Cycle
		contest := room.Contest != nil
		room.Mu.Unlock()
		c.print(c.t("cli-room-line", map[string]string{
			"id": string(id), "state": c.stateLabel(state),
			"users": strconv.Itoa(userCount), "maxUsers": strconv.Itoa(maxUsers),
			"monitors": strconv.Itoa(monitorCount), "chart": chart,
			"locked": c.boolYesNo(locked), "cycle": c.boolYesNo(cycle),
			"contest": c.boolYesNo(contest),
		}))
	}
	c.print("")
}

func (c *Console) cmdBroadcast(args []string) {
	if len(args) == 0 {
		c.printErr(c.t("cli-usage-broadcast", nil))
		return
	}
	msg := strings.Join(args, " ")
	c.state.Mu.Lock()
	rooms := make([]*server.Room, 0, len(c.state.Rooms))
	for _, room := range c.state.Rooms {
		rooms = append(rooms, room)
	}
	c.state.Mu.Unlock()
	sysID := c.state.SystemChatUserID()
	for _, room := range rooms {
		room.Mu.Lock()
		c.hub.BroadcastRoomMessage(room, protocol.MsgChat{User: sysID, Content: msg})
		room.Mu.Unlock()
	}
	c.printOK(c.t("cli-broadcast-sent", map[string]string{"count": strconv.Itoa(len(rooms))}))
}

func (c *Console) cmdRoomSay(args []string) {
	if len(args) < 2 {
		c.printErr(c.t("cli-usage-roomsay", nil))
		return
	}
	rid := protocol.RoomID(args[0])
	msg := strings.Join(args[1:], " ")
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	c.state.Mu.Unlock()
	if room == nil {
		c.printErr(c.t("cli-room-not-found-named", map[string]string{"room": args[0]}))
		return
	}
	room.Mu.Lock()
	c.hub.BroadcastRoomMessage(room, protocol.MsgChat{User: c.state.SystemChatUserID(), Content: msg})
	room.Mu.Unlock()
	c.printOK(c.t("cli-room-message-sent", map[string]string{"room": args[0]}))
}

func (c *Console) cmdDisband(args []string) {
	if len(args) == 0 {
		c.printErr(c.t("cli-usage-disband", nil))
		return
	}
	rid := protocol.RoomID(args[0])
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	if room != nil {
		sysID := c.state.SystemChatUserID()
		for _, id := range room.AllParticipantIDs() {
			if u := c.state.Users[id]; u != nil {
				u.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{User: sysID, Content: c.t("room-disbanded-by-admin", nil)}})
			}
		}
		c.hub.DisbandRoom(room)
	}
	c.state.Mu.Unlock()
	if room == nil {
		c.printErr(c.t("cli-room-not-found", nil))
		return
	}
	c.printOK(c.t("cli-room-disbanded", map[string]string{"room": args[0]}))
}

func (c *Console) cmdMaxUsers(args []string) {
	if len(args) < 2 {
		c.printErr(c.t("cli-usage-maxusers", nil))
		return
	}
	n, err := strconv.Atoi(args[1])
	if err != nil || n < 1 || n > 32767 {
		c.printErr(c.t("cli-bad-max-users", nil))
		return
	}
	rid := protocol.RoomID(args[0])
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	c.state.Mu.Unlock()
	if room != nil {
		room.Mu.Lock()
		room.MaxUsers = n
		room.Mu.Unlock()
	}
	if room == nil {
		c.printErr(c.t("cli-room-not-found", nil))
		return
	}
	c.printOK(c.t("cli-room-max-users-set", map[string]string{"room": args[0], "count": strconv.Itoa(n)}))
}

// cmdLock 管理员强制锁定/解锁房间：lock <roomId> <on|off>。
// 绕过房主校验，广播 MsgLockRoom 同步客户端。
func (c *Console) cmdLock(args []string) {
	lock, ok := c.parseToggle(args, "cli-usage-lock")
	if !ok {
		return
	}
	rid := protocol.RoomID(args[0])
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	c.state.Mu.Unlock()
	if room == nil {
		c.printErr(c.t("cli-room-not-found-named", map[string]string{"room": args[0]}))
		return
	}
	c.hub.SetRoomLocked(room, lock)
	if lock {
		c.printOK(c.t("cli-room-locked", map[string]string{"room": args[0]}))
	} else {
		c.printOK(c.t("cli-room-unlocked", map[string]string{"room": args[0]}))
	}
}

// cmdCycle 管理员开关循环模式：cycle <roomId> <on|off>。
func (c *Console) cmdCycle(args []string) {
	cycle, ok := c.parseToggle(args, "cli-usage-cycle")
	if !ok {
		return
	}
	rid := protocol.RoomID(args[0])
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	c.state.Mu.Unlock()
	if room == nil {
		c.printErr(c.t("cli-room-not-found-named", map[string]string{"room": args[0]}))
		return
	}
	c.hub.SetRoomCycle(room, cycle)
	if cycle {
		c.printOK(c.t("cli-room-cycle-on", map[string]string{"room": args[0]}))
	} else {
		c.printOK(c.t("cli-room-cycle-off", map[string]string{"room": args[0]}))
	}
}

// cmdSetHost 即时转移房主：sethost <roomId> <userId>。不限 cycle 模式，目标须在房内。
func (c *Console) cmdSetHost(args []string) {
	if len(args) < 2 {
		c.printErr(c.t("cli-usage-sethost", nil))
		return
	}
	rid := protocol.RoomID(args[0])
	userID, err := strconv.Atoi(args[1])
	if err != nil {
		c.printErr(c.t("cli-bad-user-id", nil))
		return
	}
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	c.state.Mu.Unlock()
	if room == nil {
		c.printErr(c.t("cli-room-not-found-named", map[string]string{"room": args[0]}))
		return
	}
	err = c.hub.TransferHost(room, userID)
	if err != nil {
		switch {
		case errors.Is(err, server.ErrUserNotInRoom):
			c.printErr(c.t("cli-sethost-user-not-in-room", map[string]string{
				"userId": args[1], "room": args[0],
			}))
		case errors.Is(err, server.ErrAlreadyHost):
			c.printErr(c.t("cli-sethost-already-host", map[string]string{
				"userId": args[1], "room": args[0],
			}))
		default:
			c.printErr(err.Error())
		}
		return
	}
	c.printOK(c.t("cli-sethost-set", map[string]string{
		"userId": args[1], "room": args[0],
	}))
}

// cmdRoomInfo 查看房间详情：roominfo <roomId>。显示状态/房主/谱面/标志/成员名单。
func (c *Console) cmdRoomInfo(args []string) {
	if len(args) < 1 {
		c.printErr(c.t("cli-usage-roominfo", nil))
		return
	}
	rid := protocol.RoomID(args[0])
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	if room == nil {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-room-not-found-named", map[string]string{"room": args[0]}))
		return
	}
	room.Mu.Lock()
	state := room.State
	hostID := room.HostID
	maxUsers := room.MaxUsers
	locked := room.Locked
	cycle := room.Cycle
	contest := room.Contest != nil
	chartName := c.t("cli-none", nil)
	if room.Chart != nil {
		chartName = room.Chart.Name
	}
	playerIDs := room.UserIDs()
	monitorIDs := room.MonitorIDs()
	// 同时持 state.Mu + room.Mu（顺序 state→room），state.Users 访问安全。
	playerNames := make([]string, 0, len(playerIDs))
	for _, id := range playerIDs {
		if u := c.state.Users[id]; u != nil {
			playerNames = append(playerNames, u.Name)
		} else {
			playerNames = append(playerNames, strconv.Itoa(id))
		}
	}
	monitorNames := make([]string, 0, len(monitorIDs))
	for _, id := range monitorIDs {
		if u := c.state.Users[id]; u != nil {
			monitorNames = append(monitorNames, u.Name)
		} else {
			monitorNames = append(monitorNames, strconv.Itoa(id))
		}
	}
	room.Mu.Unlock()
	c.state.Mu.Unlock()

	contestStr := c.boolYesNo(contest)
	monitorList := c.t("cli-none", nil)
	if len(monitorNames) > 0 {
		monitorList = strings.Join(monitorNames, ", ")
	}
	c.print("")
	c.print(c.t("cli-roominfo-header", map[string]string{"room": args[0]}))
	c.print(c.t("cli-roominfo-line1", map[string]string{
		"state": c.stateLabel(state), "host": strconv.Itoa(hostID), "maxUsers": strconv.Itoa(maxUsers),
	}))
	c.print(c.t("cli-roominfo-line2", map[string]string{
		"locked": c.boolYesNo(locked), "cycle": c.boolYesNo(cycle), "contest": contestStr,
	}))
	c.print(c.t("cli-roominfo-line3", map[string]string{"chart": chartName}))
	c.print(c.t("cli-roominfo-players", map[string]string{
		"count": strconv.Itoa(len(playerNames)), "list": strings.Join(playerNames, ", "),
	}))
	c.print(c.t("cli-roominfo-monitors", map[string]string{
		"count": strconv.Itoa(len(monitorNames)), "list": monitorList,
	}))
	c.print("")
}

// cmdNextHost 指定房间下一轮房主（仅 cycle 模式生效）：nexthost <roomId> <userId>。
// 非循环模式、用户不存在或不在房间内时返回错误。设置一次性消费，仅影响下一次 rotateCycleHost。
// 反馈仅在 CLI 终端输出，不向客户端发送任何提示。
func (c *Console) cmdNextHost(args []string) {
	if len(args) < 2 {
		c.printErr(c.t("cli-usage-nexthost", nil))
		return
	}
	rid := protocol.RoomID(args[0])
	userID, err := strconv.Atoi(args[1])
	if err != nil {
		c.printErr(c.t("cli-bad-user-id", nil))
		return
	}
	c.state.Mu.Lock()
	room := c.state.Rooms[rid]
	c.state.Mu.Unlock()
	if room == nil {
		c.printErr(c.t("cli-room-not-found-named", map[string]string{"room": args[0]}))
		return
	}
	room.Mu.Lock()
	if !room.Cycle {
		room.Mu.Unlock()
		c.printErr(c.t("cli-nexthost-not-cycle", map[string]string{"room": args[0]}))
		return
	}
	if !room.ContainsUser(userID) {
		room.Mu.Unlock()
		c.printErr(c.t("cli-nexthost-user-not-in-room", map[string]string{
			"userId": args[1], "room": args[0],
		}))
		return
	}
	room.SetNextHost(userID)
	room.Mu.Unlock()
	c.printOK(c.t("cli-nexthost-set", map[string]string{
		"userId": args[1], "room": args[0],
	}))
}