// cli_users.go 把「用户 / 封禁」类管理命令从 cli.go 拆出：列用户、用户详情、踢出、
// 全服 / 房间封禁与解封、封禁列表。封禁操作会持久化 admin_data.json。
package cli

import (
	"strconv"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

func (c *Console) cmdListUsers() {
	c.state.Mu.Lock()
	users := make(map[int]*server.User, len(c.state.Users))
	for id, u := range c.state.Users {
		users[id] = u
	}
	c.state.Mu.Unlock()
	if len(users) == 0 {
		c.printInfo(c.t("cli-no-users", nil))
		return
	}
	c.print(c.t("cli-users-total", map[string]string{"count": strconv.Itoa(len(users))}))
	for id, u := range users {
		status := c.t("cli-user-status-offline", nil)
		if u.IsConnected() {
			status = c.t("cli-user-status-online", nil)
		}
		role := c.t("cli-user-role-player", nil)
		if u.Monitor {
			role = c.t("cli-user-role-monitor", nil)
		}
		room := c.t("cli-none", nil)
		if u.Room != nil {
			room = string(u.Room.ID)
		}
		name := u.Name
		bannedTag := ""
		c.state.Mu.Lock()
		if _, banned := c.state.BannedUsers[id]; banned {
			bannedTag = c.t("cli-user-banned-tag", nil)
		}
		c.state.Mu.Unlock()
		c.print(c.t("cli-user-line", map[string]string{
			"id": strconv.Itoa(id), "name": name, "status": status,
			"role": role, "room": room, "bannedTag": bannedTag,
		}))
	}
}

// cmdUserInfo 显示单个用户的详细信息（对应 TS users.ts handleUserInfo）：user <id>。
func (c *Console) cmdUserInfo(args []string) {
	id, ok := c.parseUserID(args, "cli-usage-user")
	if !ok {
		return
	}
	c.state.Mu.Lock()
	u := c.state.Users[id]
	if u == nil {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-user-not-found", map[string]string{"id": args[0]}))
		return
	}
	connected := u.IsConnected()
	monitor := u.Monitor
	room := c.t("cli-none", nil)
	if u.Room != nil {
		room = string(u.Room.ID)
	}
	gameTime := u.GameTime()
	lang := "unknown"
	if u.Lang != nil {
		lang = u.Lang.Tag
	}
	name := u.Name
	_, banned := c.state.BannedUsers[id]
	c.state.Mu.Unlock()

	status := c.t("cli-user-status-offline", nil)
	if connected {
		status = c.t("cli-user-status-online", nil)
	}
	role := c.t("cli-user-role-player", nil)
	if monitor {
		role = c.t("cli-user-role-monitor", nil)
	}
	bannedStr := c.t("cli-no", nil)
	if banned {
		bannedStr = c.t("cli-yes", nil)
	}

	c.print("")
	c.print(c.t("cli-user-info-header", nil))
	c.print(c.t("cli-user-info-id", map[string]string{"id": strconv.Itoa(id)}))
	c.print(c.t("cli-user-info-name", map[string]string{"name": name}))
	c.print(c.t("cli-user-info-status", map[string]string{"status": status}))
	c.print(c.t("cli-user-info-role", map[string]string{"role": role}))
	c.print(c.t("cli-user-info-room", map[string]string{"room": room}))
	c.print(c.t("cli-user-info-banned", map[string]string{"banned": bannedStr}))
	c.print(c.t("cli-user-info-game-time", map[string]string{"time": strconv.FormatFloat(gameTime, 'g', -1, 64)}))
	c.print(c.t("cli-user-info-language", map[string]string{"lang": lang}))
	c.print("")
}

// sessionOf 返回用户当前会话（持锁读取后释放，避免持锁调用 Close）。
func (c *Console) userAndSession(id int) (*server.User, server.Session) {
	c.state.Mu.Lock()
	defer c.state.Mu.Unlock()
	u := c.state.Users[id]
	if u == nil {
		return nil, nil
	}
	return u, u.Session()
}

// adminDisconnecter 是会话的「管理员断开（可保留房间）」可选能力（network.Session 实现之）。
type adminDisconnecter interface {
	AdminDisconnect(preserveRoom bool)
}

func (c *Console) cmdKick(args []string) {
	id, ok := c.parseUserID(args, "cli-usage-kick")
	if !ok {
		return
	}
	// 第二参数 preserve|true：断开但保留该用户在房间内（离线占位、可重连）。
	preserve := len(args) > 1 && (args[1] == "preserve" || args[1] == "true")

	u, sess := c.userAndSession(id)
	if u == nil || sess == nil {
		c.printErr(c.t("cli-user-not-connected", map[string]string{"id": args[0]}))
		return
	}
	// 保留房间且该玩家正在对局：先判退本局并检查房间能否继续结算，避免房间空等已断线的玩家。
	if preserve {
		c.abortPlayingAndCheck(u)
	}
	if ad, ok := sess.(adminDisconnecter); ok {
		ad.AdminDisconnect(preserve)
	} else {
		sess.Close() // 回退：无 preserve 能力的会话按普通断开处理
	}
	c.printOK(c.t("cli-user-kicked", map[string]string{"id": args[0]}))
}

// abortPlayingAndCheck 若用户正在对局中，则将其判退本局并检查房间能否继续结算
// （对应 TS abortPlayingUserAndCheckReady）。
func (c *Console) abortPlayingAndCheck(u *server.User) {
	c.state.Mu.Lock()
	room := u.Room
	c.state.Mu.Unlock()
	if room == nil {
		return
	}
	room.Mu.Lock()
	st, ok := room.State.(server.StatePlaying)
	if !ok {
		room.Mu.Unlock()
		return
	}
	if _, done := st.Results[u.ID]; done {
		room.Mu.Unlock()
		return // 已交成绩
	}
	if _, aborted := st.Aborted[u.ID]; aborted {
		room.Mu.Unlock()
		return // 已判退
	}
	st.Aborted[u.ID] = struct{}{}
	c.hub.BroadcastRoomMessage(room, protocol.MsgAbort{User: int32(u.ID)})
	room.NotifyWebSocket(c.hub.MakeRoomLifecycle(room))
	disband := c.hub.CheckRoomAllReady(room)
	room.Mu.Unlock()
	// 比赛 AutoDisband：room.Mu 释放后再持 state.Mu 调 DisbandRoom（避免重入自死锁）。
	if disband {
		c.state.Mu.Lock()
		c.hub.DisbandRoom(room)
		c.state.Mu.Unlock()
	}
}

func (c *Console) cmdBan(args []string) {
	id, ok := c.parseUserID(args, "cli-usage-ban")
	if !ok {
		return
	}
	c.state.Mu.Lock()
	c.state.BannedUsers[id] = struct{}{}
	var sess server.Session
	if u := c.state.Users[id]; u != nil {
		sess = u.Session()
	}
	c.state.Mu.Unlock()
	if sess != nil {
		sess.Close()
	}
	_ = c.state.SaveAdminData()
	c.printOK(c.t("cli-user-banned", map[string]string{"id": args[0]}))
}

func (c *Console) cmdUnban(args []string) {
	id, ok := c.parseUserID(args, "cli-usage-unban")
	if !ok {
		return
	}
	c.state.Mu.Lock()
	delete(c.state.BannedUsers, id)
	c.state.Mu.Unlock()
	_ = c.state.SaveAdminData()
	c.printOK(c.t("cli-user-unbanned", map[string]string{"id": args[0]}))
}

func (c *Console) cmdBanRoom(args []string) {
	if len(args) < 2 {
		c.printErr(c.t("cli-usage-banroom", nil))
		return
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		c.printErr(c.t("cli-bad-user-id", nil))
		return
	}
	rid := protocol.RoomID(args[1])
	c.state.Mu.Lock()
	set := c.state.BannedRoomUsers[rid]
	if set == nil {
		set = make(map[int]struct{})
		c.state.BannedRoomUsers[rid] = set
	}
	set[id] = struct{}{}
	c.state.Mu.Unlock()
	_ = c.state.SaveAdminData()
	c.printOK(c.t("cli-room-user-banned", map[string]string{"userId": args[0], "room": args[1]}))
}

func (c *Console) cmdUnbanRoom(args []string) {
	if len(args) < 2 {
		c.printErr(c.t("cli-usage-unbanroom", nil))
		return
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		c.printErr(c.t("cli-bad-user-id", nil))
		return
	}
	rid := protocol.RoomID(args[1])
	c.state.Mu.Lock()
	if set := c.state.BannedRoomUsers[rid]; set != nil {
		delete(set, id)
		if len(set) == 0 {
			delete(c.state.BannedRoomUsers, rid)
		}
	}
	c.state.Mu.Unlock()
	_ = c.state.SaveAdminData()
	c.printOK(c.t("cli-room-user-unbanned", map[string]string{"userId": args[0], "room": args[1]}))
}

func (c *Console) cmdBanList() {
	c.state.Mu.Lock()
	ids := make([]int, 0, len(c.state.BannedUsers))
	for id := range c.state.BannedUsers {
		ids = append(ids, id)
	}
	c.state.Mu.Unlock()
	if len(ids) == 0 {
		c.printInfo(c.t("cli-no-banned-users", nil))
		return
	}
	c.print(c.t("cli-banned-list-header", map[string]string{"count": strconv.Itoa(len(ids))}))
	for _, id := range ids {
		c.print("  " + strconv.Itoa(id))
	}
}
