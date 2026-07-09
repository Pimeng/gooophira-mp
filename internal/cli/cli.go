// Package cli 提供服务端控制台：从 stdin 读命令行并执行管理操作（房间/用户/封禁/广播/
// 维护模式/开关/停止）。输出经 l10n 按服务端语言本地化。对应 TS src/server/cli。
//
// 命令按职责拆分到同包多个文件：
//   - cli.go             控制台骨架、命令派发与共享辅助
//   - cli_rooms.go       房间与广播
//   - cli_users.go       用户与封禁
//   - cli_config.go      运行时开关与维护模式
//   - cli_contest.go     比赛模式
//   - cli_approval.go    CLI 提权审批
//   - cli_ipblacklist.go 连接日志 IP 黑名单
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
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
	case "lock":
		c.cmdLock(args)
	case "cycle":
		c.cmdCycle(args)
	case "sethost":
		c.cmdSetHost(args)
	case "roominfo":
		c.cmdRoomInfo(args)
	case "nexthost":
		c.cmdNextHost(args)
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

// parseToggle 解析 on/off 开关参数，返回 (value, ok)；非法参数输出错误。
func (c *Console) parseToggle(args []string, usageKey string) (bool, bool) {
	if len(args) < 2 {
		c.printErr(c.t(usageKey, nil))
		return false, false
	}
	switch strings.ToLower(args[1]) {
	case "on", "true", "1":
		return true, true
	case "off", "false", "0":
		return false, true
	default:
		c.printErr(c.t("cli-bad-toggle", nil))
		return false, false
	}
}