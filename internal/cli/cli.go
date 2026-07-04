// Package cli 提供服务端控制台：从 stdin 读命令行并执行管理操作（房间/用户/封禁/广播/
// 维护模式/开关/停止）。输出经 l10n 按服务端语言本地化。对应 TS src/server/cli。
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/logging"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// outputKind 标记一行控制台输出的语义类别，决定终端配色与 GUI 样式（对齐 TS ConsoleOutputLine.kind）。
type outputKind string

const (
	kindOut     outputKind = "out"     // 中性数据/列表
	kindError   outputKind = "error"   // 用法/校验/未找到等错误
	kindSuccess outputKind = "success" // 操作成功确认
	kindInfo    outputKind = "info"    // 状态查询/空列表提示
)

// printer 抽象控制台输出目标：终端实现按级配色写 stdout/stderr，捕获实现收集为 ConsoleOutputLine。
type printer interface {
	emit(kind outputKind, s string)
}

// termPrinter 写终端：error→stderr 红、success→stdout 绿、info→stdout 青、out→stdout 原色
// （对齐 TS cliHelpers.makePrinter）。
type termPrinter struct{ color bool }

func (p termPrinter) emit(kind outputKind, s string) {
	w := os.Stdout
	switch kind {
	case kindError:
		w = os.Stderr
		if p.color {
			s = "\x1b[31m" + s + "\x1b[0m"
		}
	case kindSuccess:
		if p.color {
			s = "\x1b[32m" + s + "\x1b[0m"
		}
	case kindInfo:
		if p.color {
			s = "\x1b[36m" + s + "\x1b[0m"
		}
	}
	fmt.Fprintln(w, s)
}

// capturePrinter 收集输出为 ConsoleOutputLine（GUI 控制台用）。多行文本按行拆分，便于前端逐行渲染。
type capturePrinter struct{ lines []server.ConsoleOutputLine }

func (p *capturePrinter) emit(kind outputKind, s string) {
	for sub := range strings.SplitSeq(s, "\n") {
		p.lines = append(p.lines, server.ConsoleOutputLine{Kind: string(kind), Text: sub})
	}
}

// cliUseColor 判断终端是否启用 ANSI 配色（设置 NO_COLOR、或输出非字符设备时关闭）。
func cliUseColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Console 是控制台命令分发器。
type Console struct {
	state    *server.ServerState
	hub      *server.Hub
	out      printer
	shutdown func()
}

// New 创建控制台（输出到终端，按级配色）。shutdown 为 stop/shutdown 命令的关闭回调（可为 nil）。
func New(state *server.ServerState, hub *server.Hub, shutdown func()) *Console {
	return &Console{state: state, hub: hub, out: termPrinter{color: cliUseColor()}, shutdown: shutdown}
}

// NewExecutor 返回一个可捕获输出的命令执行器（供 GUI 控制台 POST /admin/console/command）。
// 每次调用用独立的 Console + 捕获 printer，命令串行执行（互斥），与 stdin 的 Run 互不干扰。
func NewExecutor(state *server.ServerState, hub *server.Hub, shutdown func()) func(string) ([]server.ConsoleOutputLine, error) {
	var mu sync.Mutex
	return func(line string) ([]server.ConsoleOutputLine, error) {
		mu.Lock()
		defer mu.Unlock()
		cp := &capturePrinter{}
		c := &Console{state: state, hub: hub, out: cp, shutdown: shutdown}
		c.Dispatch(line)
		return cp.lines, nil
	}
}

// Run 阻塞读取 stdin 并逐行分发，直到 EOF。
func (c *Console) Run() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			c.Dispatch(line)
		}
	}
}

func (c *Console) lang() *l10n.Language { return c.state.ServerLang }
func (c *Console) t(key string, args map[string]string) string {
	return l10n.TL(c.lang(), key, args)
}
func (c *Console) print(s string)     { c.out.emit(kindOut, s) }     // 中性输出
func (c *Console) printErr(s string)  { c.out.emit(kindError, s) }   // 错误（红/stderr）
func (c *Console) printOK(s string)   { c.out.emit(kindSuccess, s) } // 成功（绿）
func (c *Console) printInfo(s string) { c.out.emit(kindInfo, s) }    // 提示/状态（青）

func (c *Console) boolYesNo(b bool) string {
	if b {
		return c.t("cli-bool-yes", nil)
	}
	return c.t("cli-bool-no", nil)
}

func (c *Console) stateLabel(st server.InternalRoomState) string {
	switch st.(type) {
	case server.StatePlaying:
		return c.t("cli-room-state-playing", nil)
	case server.StateWaitForReady:
		return c.t("cli-room-state-waiting", nil)
	default:
		return c.t("cli-room-state-select", nil)
	}
}

// Dispatch 解析并执行一行命令。
func (c *Console) Dispatch(line string) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "help":
		c.print(c.t("cli-help", nil))
	case "list", "rooms":
		c.cmdListRooms()
	case "users":
		c.cmdListUsers()
	case "user":
		c.cmdUserInfo(args)
	case "broadcast", "say":
		c.cmdBroadcast(args)
	case "roomsay":
		c.cmdRoomSay(args)
	case "disband":
		c.cmdDisband(args)
	case "maxusers":
		c.cmdMaxUsers(args)
	case "kick":
		c.cmdKick(args)
	case "ban":
		c.cmdBan(args)
	case "unban":
		c.cmdUnban(args)
	case "banroom":
		c.cmdBanRoom(args)
	case "unbanroom":
		c.cmdUnbanRoom(args)
	case "banlist":
		c.cmdBanList()
	case "replay":
		c.cmdConfigToggle(args, "cli-replay", "REPLAY_ENABLED", c.replayEnabled)
	case "roomcreation":
		c.cmdConfigToggle(args, "cli-room-creation", "ROOM_CREATION_ENABLED", c.roomCreationEnabled)
	case "maintenance":
		c.cmdMaintenance(args)
	case "approve":
		c.cmdApprove(args)
	case "deny", "reject":
		c.cmdDeny(args)
	case "pending":
		c.cmdPending()
	case "ipblacklist":
		c.cmdIPBlacklist(args)
	case "contest":
		c.cmdContest(args)
	case "stop", "shutdown":
		c.cmdStop()
	default:
		c.printErr(c.t("cli-unknown-command", map[string]string{"cmd": cmd}))
	}
}

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
		u.Mu.RLock()
		status := c.t("cli-user-status-offline", nil)
		if u.Session != nil {
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
		u.Mu.RUnlock()
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
	u.Mu.RLock()
	connected := u.Session != nil
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
	u.Mu.RUnlock()
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
	if err != nil || n < 1 || n > 64 {
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

// sessionOf 返回用户当前会话（持锁读取后释放，避免持锁调用 Close）。
func (c *Console) userAndSession(id int) (*server.User, server.Session) {
	c.state.Mu.Lock()
	defer c.state.Mu.Unlock()
	u := c.state.Users[id]
	if u == nil {
		return nil, nil
	}
	return u, u.Session
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
		u.Mu.RLock()
		sess = u.Session
		u.Mu.RUnlock()
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

func (c *Console) replayEnabled() bool {
	c.state.Mu.Lock()
	defer c.state.Mu.Unlock()
	return c.state.ReplayEnabled
}

func (c *Console) roomCreationEnabled() bool {
	c.state.Mu.Lock()
	defer c.state.Mu.Unlock()
	return c.state.RoomCreationEnabled
}

// cmdConfigToggle 处理 on/off/status 开关命令：on/off 经 ApplyRuntimePatch 落盘（保留注释）
// 并热生效（含回放关闭时结束进行中的录制等副作用），对齐并改进 TS toggles.ts（连回放也持久化）。
func (c *Console) cmdConfigToggle(args []string, prefix, env string, current func() bool) {
	if len(args) == 0 {
		c.printInfo(c.t(prefix+"-status", map[string]string{"state": c.stateOnOff(current())}))
		return
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "on", "off":
		res := config.ParseRuntimeConfigPatch(map[string]any{env: sub == "on"})
		if !res.OK {
			c.printInfo(c.t(prefix+"-status", map[string]string{"state": c.stateOnOff(current())}))
			return
		}
		c.state.ApplyRuntimePatch(res)
		if sub == "on" {
			c.printOK(c.t(prefix+"-toggled-on", nil))
		} else {
			c.printOK(c.t(prefix+"-toggled-off", nil))
		}
	default:
		c.printInfo(c.t(prefix+"-status", map[string]string{"state": c.stateOnOff(current())}))
	}
}

func (c *Console) cmdMaintenance(args []string) {
	if len(args) == 0 {
		c.printErr(c.t("cli-usage-maintenance", nil))
		return
	}
	switch strings.ToLower(args[0]) {
	case "on":
		msg := ""
		c.state.Mu.Lock()
		c.state.Maintenance = true
		if len(args) > 1 {
			msg = strings.Join(args[1:], " ")
			c.state.MaintenanceMessage = &msg
		}
		c.state.Mu.Unlock()
		c.state.EmitEvent(server.Event{Type: server.EventMaintenance, Enabled: true, Message: msg})
		c.printOK(c.t("cli-maintenance-status", map[string]string{"state": c.stateOnOff(true)}))
	case "off":
		c.state.Mu.Lock()
		c.state.Maintenance = false
		c.state.MaintenanceMessage = nil
		c.state.Mu.Unlock()
		c.state.EmitEvent(server.Event{Type: server.EventMaintenance, Enabled: false})
		c.printOK(c.t("cli-maintenance-status", map[string]string{"state": c.stateOnOff(false)}))
	default:
		c.printInfo(c.t("cli-maintenance-status", map[string]string{"state": c.stateOnOff(c.state.Maintenance)}))
	}
}

// ---------- CLI 提权审批（approve / deny / pending）----------

// findApprovalSsid 通过完整 ssid 或前缀短码唯一定位一个审批会话（调用方持 state.Mu）。
// 返回 (ssid, ambiguous)；ssid=="" 且 !ambiguous 表示未找到。
func (c *Console) findApprovalSsid(input string) (string, bool) {
	if _, ok := c.state.CLIApprovalSessions[input]; ok {
		return input, false
	}
	found := ""
	for ssid := range c.state.CLIApprovalSessions {
		if strings.HasPrefix(ssid, input) {
			if found != "" {
				return "", true // 多个匹配，歧义
			}
			found = ssid
		}
	}
	return found, false
}

func cliShort(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func (c *Console) cmdApprove(args []string) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		c.printErr(c.t("cli-usage-approve", nil))
		return
	}
	input := strings.TrimSpace(args[0])
	c.state.Mu.Lock()
	ssid, ambiguous := c.findApprovalSsid(input)
	if ambiguous {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-approve-ambiguous", map[string]string{"input": input}))
		return
	}
	if ssid == "" {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-approve-not-found", map[string]string{"input": input}))
		return
	}
	sess := c.state.CLIApprovalSessions[ssid]
	now := time.Now().UnixMilli()
	if now > sess.ExpiresAt {
		delete(c.state.CLIApprovalSessions, ssid)
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-approve-expired", map[string]string{"ssid": cliShort(ssid)}))
		return
	}
	if sess.Status != server.CLIApprovalPending {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-approve-already-handled", map[string]string{"ssid": cliShort(ssid), "status": string(sess.Status)}))
		return
	}
	// 签发临时 token，放入 tempAdminTokens（与 OTP 流程产物一致）。
	token := protocol.NewUUID()
	exp := now + server.TempTokenTTLMS
	c.state.TempAdminTokens[token] = &server.TempAdminToken{IP: sess.IP, ExpiresAt: exp}
	sess.Status = server.CLIApprovalApproved
	sess.Token = token
	sess.TokenExpiresAt = exp
	ip := sess.IP
	c.state.Mu.Unlock()

	if c.state.Logger != nil {
		c.state.Logger.Info(fmt.Sprintf("[OTP CLI Approve] 会话 %s 已批准，签发临时TOKEN %s... (IP: %s)", cliShort(ssid), cliShort(token), ip))
	}
	c.printOK(c.t("cli-approve-success", map[string]string{"ssid": cliShort(ssid), "ip": ip}))
}

func (c *Console) cmdDeny(args []string) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		c.printErr(c.t("cli-usage-deny", nil))
		return
	}
	input := strings.TrimSpace(args[0])
	c.state.Mu.Lock()
	ssid, ambiguous := c.findApprovalSsid(input)
	if ambiguous {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-approve-ambiguous", map[string]string{"input": input}))
		return
	}
	if ssid == "" {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-approve-not-found", map[string]string{"input": input}))
		return
	}
	sess := c.state.CLIApprovalSessions[ssid]
	if sess.Status != server.CLIApprovalPending {
		c.state.Mu.Unlock()
		c.printErr(c.t("cli-approve-already-handled", map[string]string{"ssid": cliShort(ssid), "status": string(sess.Status)}))
		return
	}
	sess.Status = server.CLIApprovalDenied
	ip := sess.IP
	c.state.Mu.Unlock()

	if c.state.Logger != nil {
		c.state.Logger.Info(fmt.Sprintf("[OTP CLI Deny] 会话 %s 已被拒绝 (IP: %s)", cliShort(ssid), ip))
	}
	c.printOK(c.t("cli-deny-success", map[string]string{"ssid": cliShort(ssid), "ip": ip}))
}

func (c *Console) cmdPending() {
	now := time.Now().UnixMilli()
	type item struct {
		ssid   string
		ip     string
		remain int64
	}
	c.state.Mu.Lock()
	for ssid, sess := range c.state.CLIApprovalSessions {
		if now > sess.ExpiresAt {
			delete(c.state.CLIApprovalSessions, ssid) // 顺便清过期
		}
	}
	var items []item
	for ssid, sess := range c.state.CLIApprovalSessions {
		if sess.Status == server.CLIApprovalPending {
			remain := max((sess.ExpiresAt-now+999)/1000, 0) // 向上取整秒
			items = append(items, item{ssid: ssid, ip: sess.IP, remain: remain})
		}
	}
	c.state.Mu.Unlock()

	if len(items) == 0 {
		c.printInfo(c.t("cli-pending-empty", nil))
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ssid < items[j].ssid })
	c.print("")
	c.print(c.t("cli-pending-header", map[string]string{"count": strconv.Itoa(len(items))}))
	for _, it := range items {
		c.print(c.t("cli-pending-line", map[string]string{
			"ssid": cliShort(it.ssid), "full": it.ssid, "ip": it.ip, "seconds": strconv.FormatInt(it.remain, 10),
		}))
	}
	c.print("")
}

// ipBlacklister 是可选的「连接日志 IP 黑名单」管理能力（logging.Logger 实现之）。
type ipBlacklister interface {
	GetBlacklistedIPs() []logging.BlacklistedIP
	RemoveFromBlacklist(ip string)
	ClearBlacklist()
}

// cmdIPBlacklist 管理连接日志的 IP 黑名单：list / remove <ip> / clear。
func (c *Console) cmdIPBlacklist(args []string) {
	bl, ok := c.state.Logger.(ipBlacklister)
	if !ok {
		c.printErr(c.t("cli-usage-ipblacklist", nil))
		return
	}
	if len(args) == 0 {
		c.printErr(c.t("cli-usage-ipblacklist", nil))
		return
	}
	switch strings.ToLower(args[0]) {
	case "list":
		items := bl.GetBlacklistedIPs()
		if len(items) == 0 {
			c.printInfo(c.t("cli-blacklist-empty", nil))
			return
		}
		sort.Slice(items, func(i, j int) bool { return items[i].IP < items[j].IP })
		c.print("")
		c.print(c.t("cli-blacklist-header", map[string]string{"count": strconv.Itoa(len(items))}))
		for _, it := range items {
			mins := (int64(it.ExpiresIn/time.Millisecond) + 59999) / 60000 // 向上取整分钟
			c.print(c.t("cli-blacklist-line", map[string]string{"ip": it.IP, "minutes": strconv.FormatInt(mins, 10)}))
		}
		c.print("")
	case "remove":
		if len(args) < 2 {
			c.printErr(c.t("cli-usage-ipblacklist-remove", nil))
			return
		}
		bl.RemoveFromBlacklist(args[1])
		c.printOK(c.t("cli-blacklist-removed", map[string]string{"ip": args[1]}))
	case "clear":
		bl.ClearBlacklist()
		c.printOK(c.t("cli-blacklist-cleared", nil))
	default:
		c.printErr(c.t("cli-ipblacklist-unknown-subcommand", nil))
	}
}

// errCLIContestNotFound 表示房间不存在（开赛时与 TS "contest-room-not-found" 一致）。
var errCLIContestNotFound = errors.New("contest-room-not-found")

// cmdContest 管理比赛模式：contest <roomid> <enable|disable|whitelist|start> [args...]。
func (c *Console) cmdContest(args []string) {
	if len(args) < 2 {
		c.printErr(c.t("cli-usage-contest", nil))
		return
	}
	rid := protocol.RoomID(args[0])
	sub := strings.ToLower(args[1])
	userIDs := parseIntArgs(args[2:])

	switch sub {
	case "enable":
		c.state.Mu.Lock()
		room := c.state.Rooms[rid]
		if room != nil {
			c.hub.EnableContest(room, userIDs)
		}
		c.state.Mu.Unlock()
		if room == nil {
			c.printErr(c.t("cli-room-not-found", nil))
			return
		}
		c.printOK(c.t("cli-contest-enabled", map[string]string{"room": args[0]}))
	case "disable":
		c.state.Mu.Lock()
		room := c.state.Rooms[rid]
		if room != nil {
			c.hub.DisableContest(room)
		}
		c.state.Mu.Unlock()
		if room == nil {
			c.printErr(c.t("cli-room-not-found", nil))
			return
		}
		c.printOK(c.t("cli-contest-disabled", map[string]string{"room": args[0]}))
	case "whitelist":
		if len(userIDs) == 0 {
			c.printErr(c.t("cli-contest-no-user-id", nil))
			return
		}
		c.state.Mu.Lock()
		room := c.state.Rooms[rid]
		applied := room != nil && c.hub.SetContestWhitelist(room, userIDs)
		c.state.Mu.Unlock()
		if !applied {
			c.printErr(c.t("cli-contest-not-enabled", nil))
			return
		}
		c.printOK(c.t("cli-contest-whitelist-updated", map[string]string{"room": args[0]}))
	case "start":
		force := len(args) > 2 && strings.ToLower(args[2]) == "force"
		c.state.Mu.Lock()
		room := c.state.Rooms[rid]
		var err error
		if room == nil {
			err = errCLIContestNotFound
		} else {
			err = c.hub.StartContest(room, force)
		}
		c.state.Mu.Unlock()
		if err != nil {
			c.printErr(c.t("cli-contest-cannot-start", map[string]string{"reason": err.Error()}))
			return
		}
		c.printOK(c.t("cli-contest-started", map[string]string{"room": args[0]}))
	default:
		c.printErr(c.t("cli-contest-unknown-subcommand", nil))
	}
}

// parseIntArgs 解析一组整型参数（跳过非整数项）。
func parseIntArgs(args []string) []int {
	out := make([]int, 0, len(args))
	for _, a := range args {
		if n, err := strconv.Atoi(a); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (c *Console) cmdStop() {
	if c.shutdown == nil {
		c.printInfo(c.t("cli-stop-hint", nil)) // 无关闭回调（如 GUI 控制台触发）时仅提示
		return
	}
	c.printInfo(c.t("cli-stopping", nil))
	c.shutdown()
}

func (c *Console) stateOnOff(b bool) string {
	if b {
		return c.t("cli-state-on", nil)
	}
	return c.t("cli-state-off", nil)
}

func (c *Console) parseUserID(args []string, usageKey string) (int, bool) {
	if len(args) == 0 {
		c.printErr(c.t(usageKey, nil))
		return 0, false
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		c.printErr(c.t("cli-bad-user-id", nil))
		return 0, false
	}
	return id, true
}
