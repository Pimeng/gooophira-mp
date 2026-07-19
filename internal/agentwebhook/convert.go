// agentwebhook 包从 Agent 领域信封派生 Webhook 展示事件，
// 并且只在投递完成后推进持久化处理游标。
package agentwebhook

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/webhookmodel"
)

func Convert(envelope agentproto.Envelope) (webhookmodel.Event, bool, error) {
	event := webhookmodel.Event{Time: envelope.CreatedAt}
	switch envelope.Type {
	case agentproto.EventGameStartedV1:
		var payload agentproto.GameStartedV1
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return event, false, err
		}
		event.Type, event.Server, event.RoomID = webhookmodel.EventGameStart, payload.Server, payload.RoomID
		event.ChartID, event.ChartName = payload.Chart.ID, payload.Chart.Name
		event.ChartDifficulty, event.ChartCharter, event.ImageURL = payload.Chart.Difficulty, payload.Chart.Charter, payload.Chart.Illustration
		event.UserCount = len(payload.Players)
		players := make([]string, 0, len(payload.Players))
		for _, player := range payload.Players {
			players = append(players, fmt.Sprintf("%s(%d)", player.Name, player.ID))
		}
		event.PlayerList = strings.Join(players, "、")
		return event, true, nil
	case agentproto.EventMatchFinishedV1:
		var payload agentproto.MatchFinishedV1
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return event, false, err
		}
		event.Type, event.Server, event.RoomID = webhookmodel.EventGameEnd, payload.Server, payload.RoomID
		event.ChartID, event.ChartName = payload.Chart.ID, payload.Chart.Name
		for _, result := range payload.Results {
			stdScore := 0.0
			if result.StdScore != nil {
				stdScore = *result.StdScore
			}
			event.PlayerScoreRank = append(event.PlayerScoreRank, webhookmodel.ScoreRankEntry{
				PlayerID: result.Player.ID, Player: result.Player.Name, Score: result.Score, StdScore: stdScore,
			})
		}
		return event, true, nil
	case agentproto.EventGameEndedV1:
		var payload agentproto.GameEndedV1
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return event, false, err
		}
		event.Type, event.Server, event.RoomID = webhookmodel.EventGameEnd, payload.Server, payload.RoomID
		event.ChartID, event.ChartName = payload.Chart.ID, payload.Chart.Name
		return event, true, nil
	case agentproto.EventScoreSubmittedV1:
		var payload agentproto.ScoreSubmittedV1
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return event, false, err
		}
		event.Type, event.Server, event.RoomID = webhookmodel.EventScoreSubmitted, payload.Server, payload.RoomID
		event.ChartID, event.ChartName = payload.Chart.ID, payload.Chart.Name
		for _, result := range payload.Ranks {
			stdScore := 0.0
			if result.StdScore != nil {
				stdScore = *result.StdScore
			}
			event.PlayerScoreRank = append(event.PlayerScoreRank, webhookmodel.ScoreRankEntry{PlayerID: result.Player.ID, Player: result.Player.Name, Score: result.Score, StdScore: stdScore})
		}
		return event, true, nil
	case agentproto.EventRoomCreatedV1, agentproto.EventRoomDisbandedV1, agentproto.EventUserJoinedV1:
		var payload agentproto.RoomEventV1
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return event, false, err
		}
		event.Server, event.RoomID, event.UserCount = payload.Server, payload.RoomID, payload.UserCount
		if payload.User != nil {
			event.UserID, event.UserName = payload.User.ID, payload.User.Name
		}
		switch envelope.Type {
		case agentproto.EventRoomCreatedV1:
			event.Type = webhookmodel.EventRoomCreate
		case agentproto.EventRoomDisbandedV1:
			event.Type = webhookmodel.EventRoomDisband
		default:
			event.Type = webhookmodel.EventUserJoin
		}
		return event, true, nil
	case agentproto.EventMaintenanceChangedV1:
		var payload agentproto.MaintenanceChangedV1
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return event, false, err
		}
		event.Type, event.Server, event.Enabled, event.Message = webhookmodel.EventMaintenance, payload.Server, payload.Enabled, payload.Message
		return event, true, nil
	case agentproto.EventReplayCompletedV1:
		return event, false, nil
	default:
		return event, false, nil
	}
}
