package server

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

func (r *Room) EmitEvent(state *ServerState, ev Event) {
	if state == nil {
		return
	}
	if ev.RoomID == "" {
		ev.RoomID = r.ID.String()
	}
	if ev.UserCount == 0 {
		ev.UserCount = r.UserCount()
	}
	state.EmitEvent(ev)
}

func (r *Room) EmitUserEvent(state *ServerState, typ EventType, user *User) {
	if user == nil {
		return
	}
	r.EmitEvent(state, Event{Type: typ, UserID: user.ID, UserName: user.Name})
}

// BuildScoreRank 从 StatePlaying.Results 构建成绩排行（按 score 降序，平局按玩家名升序）。
// 调用方须持有 room.Mu。
func BuildScoreRank(room *Room, st StatePlaying) []ScoreRankEntry {
	if len(st.Results) == 0 {
		return nil
	}
	rank := make([]ScoreRankEntry, 0, len(st.Results))
	for uid, rec := range st.Results {
		name := ""
		if u, ok := room.UsersMap()[uid]; ok {
			name = u.Name
		}
		if name == "" {
			name = strconv.Itoa(uid)
		}
		stdScore := 0.0
		if rec.StdScore != nil {
			stdScore = *rec.StdScore
		}
		rank = append(rank, ScoreRankEntry{
			Player:   name,
			Score:    rec.Score,
			StdScore: stdScore,
		})
	}
	slices.SortFunc(rank, func(a, b ScoreRankEntry) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Player, b.Player)
	})
	return rank
}

func (r *Room) NotifyState(lc *RoomLifecycle) {
	r.OnStateChange(lc)
	r.NotifyWebSocket(lc)
}

func (r *Room) LogAndBroadcast(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	if key != "" {
		r.logRoomInfo(lc, key, args)
	}
	r.Send(lc, msg)
}

func (r *Room) MarkAndBroadcast(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	if key != "" {
		r.logRoomMark(lc, key, args)
	}
	r.Send(lc, msg)
}

func (r *Room) LogBroadcastAndNotify(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	r.LogAndBroadcast(lc, key, args, msg)
	r.NotifyWebSocket(lc)
}

func (r *Room) MarkBroadcastAndNotify(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	r.MarkAndBroadcast(lc, key, args, msg)
	r.NotifyWebSocket(lc)
}
