// cli_approval.go 把「CLI 提权审批」类命令从 cli.go 拆出：
// pending / approve <ssid> / deny <ssid> / reject <ssid>。
// 在 GUI 浏览器无法直接拿到 ADMIN_TOKEN 的场景下，可由终端审批签发临时 token（4h TTL）。
package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

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