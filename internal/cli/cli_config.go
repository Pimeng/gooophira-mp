// cli_config.go 把「运行时开关 / 维护模式」类命令从 cli.go 拆出。
// 修改经 config.ParseRuntimeConfigPatch 落盘（保留注释）并由 ServerState.ApplyRuntimePatch
// 热生效（含回放关闭时结束进行中的录制等副作用）。
package cli

import (
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

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