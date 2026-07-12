// 载荷格式化：按目标类型把 server.Event 编码为 HTTP POST 请求体与 Content-Type。
package adapter

import (
	"encoding/json"
	"fmt"

	"github.com/Pimeng/gooophira-mp/internal/server"
)

const ctJSON = "application/json; charset=utf-8"

// Format 按目标类型把事件编码为 HTTP 请求体与 Content-Type。
//   - generic：结构化 JSON（含全部字段），便于自定义机器人自行渲染。
//   - discord：{"content": "<文本>"}
//   - feishu及其它不支持的类型：返回 nil（调用方视为跳过 HTTP 通道，由对应适配器处理）。
func Format(typ string, ev server.Event) (body []byte, contentType string) {
	switch typ {
	case "discord":
		b, err := json.Marshal(map[string]any{"content": RenderText(ev)})
		if err != nil {
			return nil, ""
		}
		return b, ctJSON
	case "feishu":
		// 飞书走 SDK（Feishu 适配器），HTTP 通道跳过。
		return nil, ""
	default: // generic（含未知类型）
		b, err := json.Marshal(ev)
		if err != nil {
			return nil, ""
		}
		return b, ctJSON
	}
}

// RenderText 生成事件的人类可读文本（用于 Discord/飞书等纯文本机器人）。
func RenderText(ev server.Event) string {
	srv := ev.Server
	if srv == "" {
		srv = "Phira MP"
	}
	prefix := "[" + srv + "] "
	switch ev.Type {
	case server.EventGameStart:
		if ev.ChartName != "" {
			return fmt.Sprintf("%s🎮 房间 %s 开始游戏：%s（%d 人）", prefix, ev.RoomID, ev.ChartName, ev.UserCount)
		}
		return fmt.Sprintf("%s🎮 房间 %s 开始游戏（%d 人）", prefix, ev.RoomID, ev.UserCount)
	case server.EventGameEnd:
		if ev.ChartName != "" {
			return fmt.Sprintf("%s🏁 房间 %s 本局结束：%s", prefix, ev.RoomID, ev.ChartName)
		}
		return fmt.Sprintf("%s🏁 房间 %s 本局结束", prefix, ev.RoomID)
	case server.EventRoomCreate:
		return fmt.Sprintf("%s➕ %s 创建了房间 %s", prefix, ev.UserName, ev.RoomID)
	case server.EventRoomDisband:
		return fmt.Sprintf("%s➖ 房间 %s 已解散", prefix, ev.RoomID)
	case server.EventUserJoin:
		return fmt.Sprintf("%s👤 %s 加入房间 %s（当前 %d 人）", prefix, ev.UserName, ev.RoomID, ev.UserCount)
	case server.EventMaintenance:
		if ev.Enabled {
			if ev.Message != "" {
				return fmt.Sprintf("%s🛠️ 已进入维护模式：%s", prefix, ev.Message)
			}
			return prefix + "🛠️ 已进入维护模式"
		}
		return prefix + "✅ 已退出维护模式"
	default:
		return fmt.Sprintf("%s事件：%s", prefix, ev.Type)
	}
}
