// cli_contest.go 把「比赛模式」管理命令从 cli.go 拆出：
// contest <roomid> <enable|disable|whitelist|start> [args...]。
package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

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