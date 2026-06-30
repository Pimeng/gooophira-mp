package webhook

import (
	"encoding/json"
	"fmt"

	"github.com/Pimeng/gooophira-mp/internal/server"
)

// Format 按目标类型把事件编码为请求体与 Content-Type。
//   - generic：结构化 JSON（含全部字段），便于自定义机器人自行渲染。
//   - discord：{"content": "<文本>"}
//   - feishu ：{"msg_type":"text","content":{"text":"<文本>"}}
//
// 返回 nil body 表示该类型无法编码（跳过该目标）。
func Format(typ string, ev server.Event) (body []byte, contentType string) {
	const ctJSON = "application/json; charset=utf-8"
	switch typ {
	case "discord":
		b, err := json.Marshal(map[string]any{"content": RenderText(ev)})
		if err != nil {
			return nil, ""
		}
		return b, ctJSON
	case "feishu":
		b, err := json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]any{"text": RenderText(ev)},
		})
		if err != nil {
			return nil, ""
		}
		return b, ctJSON
	default: // generic
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
