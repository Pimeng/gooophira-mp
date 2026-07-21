// cli_ipblacklist.go 把「连接日志 IP 黑名单」管理命令从 cli.go 拆出：
// ipblacklist <list|remove <ip>|clear>。仅当 logging.Logger 实现 ipBlacklister 时可用。
package cli

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/platform/logging"
)

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
