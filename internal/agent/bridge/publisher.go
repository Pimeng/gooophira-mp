// agentbridge 包把可变的服务端领域事件映射为稳定的 Agent DTO。
package agentbridge

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agent/outbox"
	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

type Logger interface {
	Warn(string)
}

type queuedEvent struct {
	typeName string
	payload  any
	priority agentoutbox.Priority
	done     chan error
}

// Publisher 实现 server.EventSink，且 Emit 不执行磁盘 I/O。
type Publisher struct {
	store  *agentoutbox.Store
	logger Logger
	queue  chan queuedEvent
	stop   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func New(store *agentoutbox.Store, logger Logger, queueSize int) *Publisher {
	if queueSize < 1 {
		queueSize = 1024
	}
	p := &Publisher{store: store, logger: logger, queue: make(chan queuedEvent, queueSize), stop: make(chan struct{}), done: make(chan struct{})}
	go p.run()
	return p
}

func (p *Publisher) Emit(event server.Event) {
	typeName, payload, ok := mapServerEvent(event)
	if !ok {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	select {
	case p.queue <- queuedEvent{typeName: typeName, payload: payload, priority: agentoutbox.PriorityNormal}:
	default:
		p.store.RecordDroppedNormal()
		p.warn("Agent event queue full; dropping normal event " + typeName)
	}
	p.mu.Unlock()
}

// PublishCritical 在实时锁路径之外运行，并同步执行 fsync。
func (p *Publisher) PublishCritical(typeName string, payload any) error {
	done := make(chan error, 1)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("Agent event publisher is closed")
	}
	p.queue <- queuedEvent{typeName: typeName, payload: payload, priority: agentoutbox.PriorityCritical, done: done}
	p.mu.Unlock()
	return <-done
}

func (p *Publisher) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.stop)
	}
	p.mu.Unlock()
	<-p.done
}

func (p *Publisher) run() {
	defer close(p.done)
	for {
		select {
		case event := <-p.queue:
			err := p.append(event)
			if err != nil && !errors.Is(err, agentoutbox.ErrFull) {
				p.warn("Agent outbox append failed: " + err.Error())
			}
		case <-p.stop:
			for {
				select {
				case event := <-p.queue:
					_ = p.append(event)
				default:
					return
				}
			}
		}
	}
}

func (p *Publisher) append(event queuedEvent) error {
	_, err := p.store.Append(event.typeName, event.payload, event.priority)
	if event.done != nil {
		event.done <- err
	}
	return err
}

func (p *Publisher) warn(message string) {
	if p.logger != nil {
		p.logger.Warn(message)
	}
}

func mapServerEvent(event server.Event) (string, any, bool) {
	base := agentproto.RoomEventV1{Server: event.Server, RoomID: event.RoomID, UserCount: event.UserCount}
	if event.UserID != 0 {
		base.User = &agentproto.PlayerV1{ID: event.UserID, Name: event.UserName}
	}
	switch event.Type {
	case server.EventRoomCreate:
		return agentproto.EventRoomCreatedV1, base, true
	case server.EventRoomDisband:
		return agentproto.EventRoomDisbandedV1, base, true
	case server.EventUserJoin:
		return agentproto.EventUserJoinedV1, base, true
	case server.EventMaintenance:
		return agentproto.EventMaintenanceChangedV1, agentproto.MaintenanceChangedV1{Server: event.Server, Enabled: event.Enabled, Message: event.Message}, true
	case server.EventGameStart:
		players := make([]agentproto.PlayerV1, 0, len(event.Players))
		for _, player := range event.Players {
			players = append(players, agentproto.PlayerV1{ID: player.ID, Name: player.Name})
		}
		return agentproto.EventGameStartedV1, agentproto.GameStartedV1{
			Server: event.Server, RoomID: event.RoomID,
			Chart: agentproto.ChartV1{ID: event.ChartID, Name: event.ChartName, Difficulty: event.ChartDifficulty, Charter: event.ChartCharter, Illustration: event.ImageURL}, Players: players,
		}, true
	case server.EventScoreSubmitted:
		ranks := make([]agentproto.MatchPlayerResultV1, 0, len(event.PlayerScoreRank))
		for i, rank := range event.PlayerScoreRank {
			stdScore := rank.StdScore
			ranks = append(ranks, agentproto.MatchPlayerResultV1{Player: agentproto.PlayerV1{ID: rank.PlayerID, Name: rank.Player}, Score: rank.Score, StdScore: &stdScore, Rank: i + 1})
		}
		return agentproto.EventScoreSubmittedV1, agentproto.ScoreSubmittedV1{Server: event.Server, RoomID: event.RoomID, Chart: agentproto.ChartV1{ID: event.ChartID, Name: event.ChartName}, Ranks: ranks}, true
	default:
		return "", nil, false
	}
}

// CaptureMatchFinished 在调用方持有房间锁时复制全部可变房间状态。
// 回放 ID 会在 recorder.EndRoom 完成后附加。
func CaptureMatchFinished(serverName string, room *server.Room) (agentproto.MatchFinishedV1, bool) {
	state, ok := room.PlayingState()
	if !ok || len(state.Results) == 0 {
		return agentproto.MatchFinishedV1{}, false
	}
	match := agentproto.MatchFinishedV1{
		Server: serverName, RoomID: room.ID.String(), StartedAt: state.StartedAt.UTC(),
		DurationSeconds: time.Since(state.StartedAt).Seconds(),
	}
	if room.Chart != nil {
		match.Chart = agentproto.ChartV1{ID: room.Chart.ID, Name: room.Chart.Name, Difficulty: room.Chart.Level, Charter: room.Chart.Charter, Illustration: room.Chart.Illustration}
	}
	ids := make([]int, 0, len(state.Results))
	for id := range state.Results {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int) int {
		if state.Results[a].Score != state.Results[b].Score {
			return state.Results[b].Score - state.Results[a].Score
		}
		return a - b
	})
	for index, id := range ids {
		record := state.Results[id]
		name := strconv.Itoa(id)
		if user := room.UsersMap()[id]; user != nil && user.Name != "" {
			name = user.Name
		}
		match.Results = append(match.Results, agentproto.MatchPlayerResultV1{
			Player: agentproto.PlayerV1{ID: id, Name: name}, Score: record.Score, Accuracy: record.Accuracy,
			Perfect: record.Perfect, Good: record.Good, Bad: record.Bad, Miss: record.Miss,
			MaxCombo: record.MaxCombo, FullCombo: record.FullCombo, Std: record.Std, StdScore: record.StdScore,
			Rank: index + 1, RecordID: record.ID,
		})
	}
	return match, true
}

func AttachReplayIDs(match *agentproto.MatchFinishedV1, ids map[int]string) {
	for index := range match.Results {
		match.Results[index].ReplayID = ids[match.Results[index].Player.ID]
	}
}
