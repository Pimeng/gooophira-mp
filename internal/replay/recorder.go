package replay

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

const (
	// maxFramesPerInflight 是每玩家单局录制帧数上限，超出后丢弃防止内存无限增长。
	maxFramesPerInflight = 15000
	// fakeMonitorID 是回放假观战者的用户 id。
	fakeMonitorID = 2_000_000_000
)

// Logger 是录制器使用的最小日志接口（可为 nil）。
type Logger interface {
	Debug(msg string)
	Warn(msg string)
}

// Participant 是一局录制中的参与者。
type Participant struct {
	ID   int
	Name string
}

// FileInfo 是一条已完成回放文件的元信息（自动上传去重用）。
type FileInfo struct {
	UserID    int
	ChartID   int
	Timestamp int64
	Path      string
}

type inFlight struct {
	roomKey   string
	userID    int
	userName  string
	chartID   int
	chartName string
	timestamp int64
	recordID  int
	path      string
	touches   []protocol.TouchFrame
	judges    []protocol.JudgeEvent
	overflow  bool
}

func (it *inFlight) info() FileInfo {
	return FileInfo{UserID: it.userID, ChartID: it.chartID, Timestamp: it.timestamp, Path: it.path}
}

// Recorder 录制游戏回放。实现 server.ReplayRecorder（AppendTouches/AppendJudges/
// SetRecordID/SetBaseDir），并提供 StartRoom/EndRoom/ListRoomFiles 等生命周期方法。
//
// 所有 map 操作经内部 mu 保护。dispatch 路径在 ServerState.Mu 下调用 Append*/SetRecordID
// （已串行）；EndRoom 的磁盘写入应由调用方放到 goroutine 中以免阻塞命令处理。
type Recorder struct {
	mu         sync.Mutex
	baseDir    string
	logger     Logger
	inflight   map[string]*inFlight       // key = "roomKey:userID"
	keysByRoom map[string]map[string]bool // roomKey -> set(key)
	completed  map[string][]FileInfo      // roomKey -> 已完成文件
}

// NewRecorder 创建录制器。logger 可为 nil。
func NewRecorder(baseDir string, logger Logger) *Recorder {
	return &Recorder{
		baseDir:    baseDir,
		logger:     logger,
		inflight:   make(map[string]*inFlight),
		keysByRoom: make(map[string]map[string]bool),
		completed:  make(map[string][]FileInfo),
	}
}

func key(roomKey string, userID int) string { return fmt.Sprintf("%s:%d", roomKey, userID) }

// SetBaseDir 设置回放根目录。
func (r *Recorder) SetBaseDir(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseDir = dir
}

func (r *Recorder) log(msg string) {
	if r.logger != nil {
		r.logger.Debug("[Replay] " + msg)
	}
}

// StartRoom 为房间各玩家建立录制条目（纯内存，磁盘写入推迟到 EndRoom）。
// 房间已有进行中的录制时跳过。
func (r *Recorder) StartRoom(roomID protocol.RoomID, chartID int, chartName string, users []Participant) {
	roomKey := string(roomID)
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing := r.keysByRoom[roomKey]; len(existing) > 0 {
		r.log("startRoom skipped: room already recording")
		return
	}
	delete(r.completed, roomKey)

	keys := make(map[string]bool)
	now := time.Now().UnixMilli()
	for _, p := range users {
		if p.ID < 0 {
			continue
		}
		k := key(roomKey, p.ID)
		r.inflight[k] = &inFlight{
			roomKey:   roomKey,
			userID:    p.ID,
			userName:  p.Name,
			chartID:   chartID,
			chartName: chartName,
			timestamp: now,
			path:      FilePath(r.baseDir, p.ID, chartID, now),
		}
		keys[k] = true
	}
	if len(keys) > 0 {
		r.keysByRoom[roomKey] = keys
	}
	r.log(fmt.Sprintf("startRoom: %d recordings", len(keys)))
}

func (r *Recorder) get(roomID protocol.RoomID, userID int) *inFlight {
	return r.inflight[key(string(roomID), userID)]
}

// AppendTouches 追加某玩家触摸帧（达上限后静默丢弃）。
func (r *Recorder) AppendTouches(roomID protocol.RoomID, userID int, frames []protocol.TouchFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.get(roomID, userID)
	if it == nil {
		return
	}
	remaining := maxFramesPerInflight - len(it.touches)
	if remaining <= 0 {
		r.warnOverflow(it)
		return
	}
	it.touches = append(it.touches, frames[:min(len(frames), remaining)]...)
}

// AppendJudges 追加某玩家判定事件（达上限后静默丢弃）。
func (r *Recorder) AppendJudges(roomID protocol.RoomID, userID int, judges []protocol.JudgeEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.get(roomID, userID)
	if it == nil {
		return
	}
	remaining := maxFramesPerInflight - len(it.judges)
	if remaining <= 0 {
		r.warnOverflow(it)
		return
	}
	it.judges = append(it.judges, judges[:min(len(judges), remaining)]...)
}

func (r *Recorder) warnOverflow(it *inFlight) {
	if !it.overflow {
		it.overflow = true
		if r.logger != nil {
			r.logger.Warn(fmt.Sprintf("[Replay] frame overflow for userId=%d, dropping", it.userID))
		}
	}
}

// SetRecordID 记录某玩家本局成绩 id。
func (r *Recorder) SetRecordID(roomID protocol.RoomID, userID, recordID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if it := r.get(roomID, userID); it != nil {
		it.recordID = recordID
	}
}

// EndRoom 关闭房间所有录制并写盘。建议由调用方以 goroutine 调用以免阻塞命令处理。
func (r *Recorder) EndRoom(roomID protocol.RoomID) {
	roomKey := string(roomID)
	r.mu.Lock()
	keys := r.keysByRoom[roomKey]
	delete(r.keysByRoom, roomKey)
	snapshots := make([]*inFlight, 0, len(keys))
	for k := range keys {
		if it := r.inflight[k]; it != nil {
			delete(r.inflight, k)
			snapshots = append(snapshots, it)
		}
	}
	r.mu.Unlock()

	var completed []FileInfo
	for _, it := range snapshots {
		if err := r.writeRecordFile(it); err != nil {
			if r.logger != nil {
				r.logger.Warn(fmt.Sprintf("[Replay] write failed for userId=%d: %v", it.userID, err))
			}
			continue
		}
		completed = append(completed, it.info())
	}
	if len(completed) > 0 {
		r.mu.Lock()
		r.completed[roomKey] = completed
		r.mu.Unlock()
	}
}

// CloseAll 在关闭时刷写所有进行中的录制。
func (r *Recorder) CloseAll() {
	r.mu.Lock()
	roomKeys := make([]protocol.RoomID, 0, len(r.keysByRoom))
	for rk := range r.keysByRoom {
		roomKeys = append(roomKeys, protocol.RoomID(rk))
	}
	r.mu.Unlock()
	for _, rk := range roomKeys {
		r.EndRoom(rk)
	}
}

// ListRoomFiles 返回房间录制文件信息（进行中取实时条目，否则取已完成快照）。
func (r *Recorder) ListRoomFiles(roomID protocol.RoomID) []FileInfo {
	roomKey := string(roomID)
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := r.keysByRoom[roomKey]
	if keys == nil {
		return append([]FileInfo(nil), r.completed[roomKey]...)
	}
	out := make([]FileInfo, 0, len(keys))
	for k := range keys {
		if it := r.inflight[k]; it != nil {
			out = append(out, it.info())
		}
	}
	return out
}

// ClearRoomFiles 清理已完成文件记录（防止已解散房间元数据泄漏）。
func (r *Recorder) ClearRoomFiles(roomID protocol.RoomID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.completed, string(roomID))
}

// FakeMonitorInfo 返回回放假观战者信息（用于让客户端以为有观战者从而上报实时数据）。
func (r *Recorder) FakeMonitorInfo(name string) protocol.UserInfo {
	return protocol.UserInfo{ID: fakeMonitorID, Name: name, Monitor: true}
}

func (r *Recorder) buildContent(it *inFlight) []byte {
	w := protocol.NewBinaryWriter()
	w.WriteI32(int32(it.recordID))
	w.WriteI64(it.timestamp)
	w.WriteI32(int32(it.chartID))
	w.WriteString(it.chartName)
	w.WriteI32(int32(it.userID))
	w.WriteString(it.userName)
	protocol.WriteArray(w, it.touches, protocol.EncodeTouchFrame)
	protocol.WriteArray(w, it.judges, encodeReplayJudgeEvent)
	return w.ToBuffer()
}

func (r *Recorder) writeRecordFile(it *inFlight) error {
	if _, err := ensureDir(r.baseDir, it.userID, it.chartID); err != nil {
		return err
	}
	content := r.buildContent(it)
	payload, err := compressDeflate(content)
	if err != nil {
		return err
	}
	out := append(buildHeader(compressionDeflate), payload...)
	return os.WriteFile(it.path, out, 0o644)
}
