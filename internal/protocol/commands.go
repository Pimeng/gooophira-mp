package protocol

// tag 字节与字段顺序必须与 TS 端（src/common/commands.ts）逐字节一致。

// 心跳相关时间常量。
const (
	HeartbeatIntervalMS          = 3000
	HeartbeatTimeoutMS           = 2000
	HeartbeatDisconnectTimeoutMS = 10000
)

// CompactPos 是触摸点坐标（编码时压缩为两个 f16）。
type CompactPos struct {
	X float32
	Y float32
}

// TouchPoint 是一个触摸点：id + 坐标。TS 用 [number, CompactPos] 元组，这里用结构体。
type TouchPoint struct {
	ID  int8
	Pos CompactPos
}

// TouchFrame 是某一时刻的一组触摸点。
type TouchFrame struct {
	Time   float32
	Points []TouchPoint
}

// Judgement 是判定类型。
type Judgement uint8

// 判定枚举值。
const (
	JudgePerfect     Judgement = 0
	JudgeGood        Judgement = 1
	JudgeBad         Judgement = 2
	JudgeMiss        Judgement = 3
	JudgeHoldPerfect Judgement = 4
	JudgeHoldGood    Judgement = 5
)

// JudgeEvent 是一次判定事件。
type JudgeEvent struct {
	Time      float32
	LineID    uint32
	NoteID    uint32
	Judgement Judgement
}

// UserInfo 是房间内可见的用户信息。
type UserInfo struct {
	ID      int32
	Name    string
	Monitor bool
}

// ---------- RoomState（tagged union） ----------

// RoomState 是房间状态机的一个状态。
type RoomState interface{ isRoomState() }

// RoomStateSelectChart 选谱阶段；ID 为可选的当前所选谱面（nil = 未选）。
type RoomStateSelectChart struct{ ID *int32 }

// RoomStateWaitingForReady 等待准备阶段。
type RoomStateWaitingForReady struct{}

// RoomStatePlaying 游戏进行中。
type RoomStatePlaying struct{}

func (RoomStateSelectChart) isRoomState()     {}
func (RoomStateWaitingForReady) isRoomState() {}
func (RoomStatePlaying) isRoomState()         {}

// ClientRoomState 是发给客户端的完整房间状态视图。
type ClientRoomState struct {
	ID      RoomID
	State   RoomState
	Live    bool
	Locked  bool
	Cycle   bool
	IsHost  bool
	IsReady bool
	Users   map[int32]UserInfo
}

// JoinRoomResponse 是加入房间成功后的响应。
type JoinRoomResponse struct {
	State RoomState
	Users []UserInfo
	Live  bool
}

// AuthInfo 是 Authenticate 成功结果：当前用户 + 可选的当前房间状态。
// 对应 TS 的 [UserInfo, ClientRoomState | null] 元组。
type AuthInfo struct {
	Me   UserInfo
	Room *ClientRoomState
}

// ---------- Message（tagged union，服务端广播给房间） ----------

// Message 是服务端向房间广播的事件消息。
type Message interface{ isMessage() }

type (
	// MsgChat 聊天消息。
	MsgChat struct {
		User    int32
		Content string
	}
	// MsgCreateRoom 创建房间。
	MsgCreateRoom struct{ User int32 }
	// MsgJoinRoom 加入房间。
	MsgJoinRoom struct {
		User int32
		Name string
	}
	// MsgLeaveRoom 离开房间。
	MsgLeaveRoom struct {
		User int32
		Name string
	}
	// MsgNewHost 新房主。
	MsgNewHost struct{ User int32 }
	// MsgSelectChart 选择谱面。
	MsgSelectChart struct {
		User int32
		Name string
		ID   int32
	}
	// MsgGameStart 房主发起开始。
	MsgGameStart struct{ User int32 }
	// MsgReady 玩家已准备。
	MsgReady struct{ User int32 }
	// MsgCancelReady 取消准备。
	MsgCancelReady struct{ User int32 }
	// MsgCancelGame 取消本局。
	MsgCancelGame struct{ User int32 }
	// MsgStartPlaying 全员就绪，开始游戏。
	MsgStartPlaying struct{}
	// MsgPlayed 玩家完成游戏并提交成绩。
	MsgPlayed struct {
		User      int32
		Score     int32
		Accuracy  float32
		FullCombo bool
	}
	// MsgGameEnd 本局结束。
	MsgGameEnd struct{}
	// MsgAbort 玩家中止。
	MsgAbort struct{ User int32 }
	// MsgLockRoom 房间锁定状态变更。
	MsgLockRoom struct{ Lock bool }
	// MsgCycleRoom 房间循环模式变更。
	MsgCycleRoom struct{ Cycle bool }
)

func (MsgChat) isMessage()         {}
func (MsgCreateRoom) isMessage()   {}
func (MsgJoinRoom) isMessage()     {}
func (MsgLeaveRoom) isMessage()    {}
func (MsgNewHost) isMessage()      {}
func (MsgSelectChart) isMessage()  {}
func (MsgGameStart) isMessage()    {}
func (MsgReady) isMessage()        {}
func (MsgCancelReady) isMessage()  {}
func (MsgCancelGame) isMessage()   {}
func (MsgStartPlaying) isMessage() {}
func (MsgPlayed) isMessage()       {}
func (MsgGameEnd) isMessage()      {}
func (MsgAbort) isMessage()        {}
func (MsgLockRoom) isMessage()     {}
func (MsgCycleRoom) isMessage()    {}

// ---------- ClientCommand（tagged union，客户端 -> 服务端） ----------

// ClientCommand 是客户端发往服务端的命令。
type ClientCommand interface{ isClientCommand() }

type (
	// CmdPing 心跳。
	CmdPing struct{}
	// CmdAuthenticate 认证（Phira token，≤32 字节）。
	CmdAuthenticate struct{ Token string }
	// CmdChat 发送聊天（≤200 字节）。
	CmdChat struct{ Message string }
	// CmdTouches 上传触摸帧。
	CmdTouches struct{ Frames []TouchFrame }
	// CmdJudges 上传判定事件。
	CmdJudges struct{ Judges []JudgeEvent }
	// CmdCreateRoom 创建房间。
	CmdCreateRoom struct{ ID RoomID }
	// CmdJoinRoom 加入房间（monitor=是否观战）。
	CmdJoinRoom struct {
		ID      RoomID
		Monitor bool
	}
	// CmdLeaveRoom 离开房间。
	CmdLeaveRoom struct{}
	// CmdLockRoom 锁定/解锁房间。
	CmdLockRoom struct{ Lock bool }
	// CmdCycleRoom 切换循环模式。
	CmdCycleRoom struct{ Cycle bool }
	// CmdSelectChart 选择谱面。
	CmdSelectChart struct{ ID int32 }
	// CmdRequestStart 请求开始。
	CmdRequestStart struct{}
	// CmdReady 准备。
	CmdReady struct{}
	// CmdCancelReady 取消准备。
	CmdCancelReady struct{}
	// CmdPlayed 提交成绩（record id）。
	CmdPlayed struct{ ID int32 }
	// CmdAbort 中止。
	CmdAbort struct{}
)

func (CmdPing) isClientCommand()         {}
func (CmdAuthenticate) isClientCommand() {}
func (CmdChat) isClientCommand()         {}
func (CmdTouches) isClientCommand()      {}
func (CmdJudges) isClientCommand()       {}
func (CmdCreateRoom) isClientCommand()   {}
func (CmdJoinRoom) isClientCommand()     {}
func (CmdLeaveRoom) isClientCommand()    {}
func (CmdLockRoom) isClientCommand()     {}
func (CmdCycleRoom) isClientCommand()    {}
func (CmdSelectChart) isClientCommand()  {}
func (CmdRequestStart) isClientCommand() {}
func (CmdReady) isClientCommand()        {}
func (CmdCancelReady) isClientCommand()  {}
func (CmdPlayed) isClientCommand()       {}
func (CmdAbort) isClientCommand()        {}

// ---------- ServerCommand（tagged union，服务端 -> 客户端） ----------

// ServerCommand 是服务端发往客户端的命令。
type ServerCommand interface{ isServerCommand() }

type (
	// SrvPong 心跳响应。
	SrvPong struct{}
	// SrvAuthenticate 认证结果。
	SrvAuthenticate struct{ Result StringResult[AuthInfo] }
	// SrvChat 聊天结果。
	SrvChat struct{ Result StringResult[Unit] }
	// SrvTouches 广播某玩家的触摸帧。
	SrvTouches struct {
		Player int32
		Frames []TouchFrame
	}
	// SrvJudges 广播某玩家的判定事件。
	SrvJudges struct {
		Player int32
		Judges []JudgeEvent
	}
	// SrvMessage 广播房间事件消息。
	SrvMessage struct{ Message Message }
	// SrvChangeState 房间状态变更。
	SrvChangeState struct{ State RoomState }
	// SrvChangeHost 房主身份变更。
	SrvChangeHost struct{ IsHost bool }
	// SrvCreateRoom 创建房间结果。
	SrvCreateRoom struct{ Result StringResult[Unit] }
	// SrvJoinRoom 加入房间结果。
	SrvJoinRoom struct {
		Result StringResult[JoinRoomResponse]
	}
	// SrvOnJoinRoom 通知有新成员加入。
	SrvOnJoinRoom struct{ Info UserInfo }
	// SrvLeaveRoom 离开房间结果。
	SrvLeaveRoom struct{ Result StringResult[Unit] }
	// SrvLockRoom 锁定房间结果。
	SrvLockRoom struct{ Result StringResult[Unit] }
	// SrvCycleRoom 循环模式结果。
	SrvCycleRoom struct{ Result StringResult[Unit] }
	// SrvSelectChart 选谱结果。
	SrvSelectChart struct{ Result StringResult[Unit] }
	// SrvRequestStart 请求开始结果。
	SrvRequestStart struct{ Result StringResult[Unit] }
	// SrvReady 准备结果。
	SrvReady struct{ Result StringResult[Unit] }
	// SrvCancelReady 取消准备结果。
	SrvCancelReady struct{ Result StringResult[Unit] }
	// SrvPlayed 提交成绩结果。
	SrvPlayed struct{ Result StringResult[Unit] }
	// SrvAbort 中止结果。
	SrvAbort struct{ Result StringResult[Unit] }
)

func (SrvPong) isServerCommand()         {}
func (SrvAuthenticate) isServerCommand() {}
func (SrvChat) isServerCommand()         {}
func (SrvTouches) isServerCommand()      {}
func (SrvJudges) isServerCommand()       {}
func (SrvMessage) isServerCommand()      {}
func (SrvChangeState) isServerCommand()  {}
func (SrvChangeHost) isServerCommand()   {}
func (SrvCreateRoom) isServerCommand()   {}
func (SrvJoinRoom) isServerCommand()     {}
func (SrvOnJoinRoom) isServerCommand()   {}
func (SrvLeaveRoom) isServerCommand()    {}
func (SrvLockRoom) isServerCommand()     {}
func (SrvCycleRoom) isServerCommand()    {}
func (SrvSelectChart) isServerCommand()  {}
func (SrvRequestStart) isServerCommand() {}
func (SrvReady) isServerCommand()        {}
func (SrvCancelReady) isServerCommand()  {}
func (SrvPlayed) isServerCommand()       {}
func (SrvAbort) isServerCommand()        {}

// ---------- 基础类型 codec ----------

func encodeCompactPos(w *BinaryWriter, v CompactPos) { w.WriteCompactPos(v) }
func decodeCompactPos(r *BinaryReader) CompactPos    { return r.ReadCompactPos() }

// EncodeTouchFrame 编码一个触摸帧。
func EncodeTouchFrame(w *BinaryWriter, v TouchFrame) {
	w.WriteF32(v.Time)
	WriteArray(w, v.Points, func(ww *BinaryWriter, p TouchPoint) {
		ww.WriteI8(p.ID)
		encodeCompactPos(ww, p.Pos)
	})
}

// DecodeTouchFrame 解码一个触摸帧。
func DecodeTouchFrame(r *BinaryReader) TouchFrame {
	time := r.ReadF32()
	points := ReadArray(r, func(rr *BinaryReader) TouchPoint {
		id := rr.ReadI8()
		pos := decodeCompactPos(rr)
		return TouchPoint{ID: id, Pos: pos}
	})
	return TouchFrame{Time: time, Points: points}
}

// EncodeJudgeEvent 编码一次判定事件。
func EncodeJudgeEvent(w *BinaryWriter, v JudgeEvent) {
	w.WriteF32(v.Time)
	w.WriteU32(v.LineID)
	w.WriteU32(v.NoteID)
	w.WriteU8(uint8(v.Judgement))
}

// DecodeJudgeEvent 解码一次判定事件。
func DecodeJudgeEvent(r *BinaryReader) JudgeEvent {
	time := r.ReadF32()
	lineID := r.ReadU32()
	noteID := r.ReadU32()
	judgement := Judgement(r.ReadU8())
	return JudgeEvent{Time: time, LineID: lineID, NoteID: noteID, Judgement: judgement}
}

func encodeRoomID(w *BinaryWriter, v RoomID) { w.WriteString(string(v)) }

func decodeRoomID(r *BinaryReader) RoomID {
	id, err := ParseRoomID(r.ReadString())
	if err != nil {
		fail(err.Error())
	}
	return id
}

func encodeUserInfo(w *BinaryWriter, v UserInfo) {
	w.WriteI32(v.ID)
	w.WriteString(v.Name)
	w.WriteBool(v.Monitor)
}

func decodeUserInfo(r *BinaryReader) UserInfo {
	id := r.ReadI32()
	name := r.ReadString()
	monitor := r.ReadBool()
	return UserInfo{ID: id, Name: name, Monitor: monitor}
}

func encodeRoomState(w *BinaryWriter, v RoomState) {
	switch s := v.(type) {
	case RoomStateSelectChart:
		w.WriteU8(0)
		WriteOption(w, s.ID, func(ww *BinaryWriter, id int32) { ww.WriteI32(id) })
	case RoomStateWaitingForReady:
		w.WriteU8(1)
	case RoomStatePlaying:
		w.WriteU8(2)
	default:
		fail("proto-roomstate-invalid")
	}
}

func decodeRoomState(r *BinaryReader) RoomState {
	tag := r.ReadU8()
	switch tag {
	case 0:
		id := ReadOption(r, func(rr *BinaryReader) int32 { return rr.ReadI32() })
		return RoomStateSelectChart{ID: id}
	case 1:
		return RoomStateWaitingForReady{}
	case 2:
		return RoomStatePlaying{}
	default:
		fail("proto-roomstate-tag-invalid")
		return nil
	}
}

func encodeClientRoomState(w *BinaryWriter, v ClientRoomState) {
	encodeRoomID(w, v.ID)
	encodeRoomState(w, v.State)
	w.WriteBool(v.Live)
	w.WriteBool(v.Locked)
	w.WriteBool(v.Cycle)
	w.WriteBool(v.IsHost)
	w.WriteBool(v.IsReady)
	WriteSortedMap(w, v.Users,
		func(ww *BinaryWriter, k int32) { ww.WriteI32(k) },
		encodeUserInfo)
}

func decodeClientRoomState(r *BinaryReader) ClientRoomState {
	id := decodeRoomID(r)
	state := decodeRoomState(r)
	live := r.ReadBool()
	locked := r.ReadBool()
	cycle := r.ReadBool()
	isHost := r.ReadBool()
	isReady := r.ReadBool()
	users := ReadMap(r,
		func(rr *BinaryReader) int32 { return rr.ReadI32() },
		decodeUserInfo)
	return ClientRoomState{
		ID: id, State: state, Live: live, Locked: locked, Cycle: cycle,
		IsHost: isHost, IsReady: isReady, Users: users,
	}
}

func encodeJoinRoomResponse(w *BinaryWriter, v JoinRoomResponse) {
	encodeRoomState(w, v.State)
	WriteArray(w, v.Users, encodeUserInfo)
	w.WriteBool(v.Live)
}

func decodeJoinRoomResponse(r *BinaryReader) JoinRoomResponse {
	state := decodeRoomState(r)
	users := ReadArray(r, decodeUserInfo)
	live := r.ReadBool()
	return JoinRoomResponse{State: state, Users: users, Live: live}
}

// ---------- Message codec ----------

func encodeMessage(w *BinaryWriter, v Message) {
	switch m := v.(type) {
	case MsgChat:
		w.WriteU8(0)
		w.WriteI32(m.User)
		w.WriteString(m.Content)
	case MsgCreateRoom:
		w.WriteU8(1)
		w.WriteI32(m.User)
	case MsgJoinRoom:
		w.WriteU8(2)
		w.WriteI32(m.User)
		w.WriteString(m.Name)
	case MsgLeaveRoom:
		w.WriteU8(3)
		w.WriteI32(m.User)
		w.WriteString(m.Name)
	case MsgNewHost:
		w.WriteU8(4)
		w.WriteI32(m.User)
	case MsgSelectChart:
		w.WriteU8(5)
		w.WriteI32(m.User)
		w.WriteString(m.Name)
		w.WriteI32(m.ID)
	case MsgGameStart:
		w.WriteU8(6)
		w.WriteI32(m.User)
	case MsgReady:
		w.WriteU8(7)
		w.WriteI32(m.User)
	case MsgCancelReady:
		w.WriteU8(8)
		w.WriteI32(m.User)
	case MsgCancelGame:
		w.WriteU8(9)
		w.WriteI32(m.User)
	case MsgStartPlaying:
		w.WriteU8(10)
	case MsgPlayed:
		w.WriteU8(11)
		w.WriteI32(m.User)
		w.WriteI32(m.Score)
		w.WriteF32(m.Accuracy)
		w.WriteBool(m.FullCombo)
	case MsgGameEnd:
		w.WriteU8(12)
	case MsgAbort:
		w.WriteU8(13)
		w.WriteI32(m.User)
	case MsgLockRoom:
		w.WriteU8(14)
		w.WriteBool(m.Lock)
	case MsgCycleRoom:
		w.WriteU8(15)
		w.WriteBool(m.Cycle)
	default:
		fail("proto-message-invalid")
	}
}

func decodeMessage(r *BinaryReader) Message {
	tag := r.ReadU8()
	switch tag {
	case 0:
		return MsgChat{User: r.ReadI32(), Content: r.ReadString()}
	case 1:
		return MsgCreateRoom{User: r.ReadI32()}
	case 2:
		return MsgJoinRoom{User: r.ReadI32(), Name: r.ReadString()}
	case 3:
		return MsgLeaveRoom{User: r.ReadI32(), Name: r.ReadString()}
	case 4:
		return MsgNewHost{User: r.ReadI32()}
	case 5:
		return MsgSelectChart{User: r.ReadI32(), Name: r.ReadString(), ID: r.ReadI32()}
	case 6:
		return MsgGameStart{User: r.ReadI32()}
	case 7:
		return MsgReady{User: r.ReadI32()}
	case 8:
		return MsgCancelReady{User: r.ReadI32()}
	case 9:
		return MsgCancelGame{User: r.ReadI32()}
	case 10:
		return MsgStartPlaying{}
	case 11:
		return MsgPlayed{User: r.ReadI32(), Score: r.ReadI32(), Accuracy: r.ReadF32(), FullCombo: r.ReadBool()}
	case 12:
		return MsgGameEnd{}
	case 13:
		return MsgAbort{User: r.ReadI32()}
	case 14:
		return MsgLockRoom{Lock: r.ReadBool()}
	case 15:
		return MsgCycleRoom{Cycle: r.ReadBool()}
	default:
		fail("proto-message-tag-invalid")
		return nil
	}
}

// ---------- ClientCommand codec ----------

// EncodeClientCommand 编码一个客户端命令。
func EncodeClientCommand(w *BinaryWriter, cmd ClientCommand) {
	switch c := cmd.(type) {
	case CmdPing:
		w.WriteU8(0)
	case CmdAuthenticate:
		w.WriteU8(1)
		w.WriteVarchar(32, c.Token)
	case CmdChat:
		w.WriteU8(2)
		w.WriteVarchar(200, c.Message)
	case CmdTouches:
		w.WriteU8(3)
		WriteArray(w, c.Frames, EncodeTouchFrame)
	case CmdJudges:
		w.WriteU8(4)
		WriteArray(w, c.Judges, EncodeJudgeEvent)
	case CmdCreateRoom:
		w.WriteU8(5)
		encodeRoomID(w, c.ID)
	case CmdJoinRoom:
		w.WriteU8(6)
		encodeRoomID(w, c.ID)
		w.WriteBool(c.Monitor)
	case CmdLeaveRoom:
		w.WriteU8(7)
	case CmdLockRoom:
		w.WriteU8(8)
		w.WriteBool(c.Lock)
	case CmdCycleRoom:
		w.WriteU8(9)
		w.WriteBool(c.Cycle)
	case CmdSelectChart:
		w.WriteU8(10)
		w.WriteI32(c.ID)
	case CmdRequestStart:
		w.WriteU8(11)
	case CmdReady:
		w.WriteU8(12)
	case CmdCancelReady:
		w.WriteU8(13)
	case CmdPlayed:
		w.WriteU8(14)
		w.WriteI32(c.ID)
	case CmdAbort:
		w.WriteU8(15)
	default:
		fail("proto-clientcommand-invalid")
	}
}

// DecodeClientCommand 解码一个客户端命令。
func DecodeClientCommand(r *BinaryReader) ClientCommand {
	tag := r.ReadU8()
	switch tag {
	case 0:
		return CmdPing{}
	case 1:
		return CmdAuthenticate{Token: r.ReadVarchar(32)}
	case 2:
		return CmdChat{Message: r.ReadVarchar(200)}
	case 3:
		return CmdTouches{Frames: ReadArray(r, DecodeTouchFrame)}
	case 4:
		return CmdJudges{Judges: ReadArray(r, DecodeJudgeEvent)}
	case 5:
		return CmdCreateRoom{ID: decodeRoomID(r)}
	case 6:
		return CmdJoinRoom{ID: decodeRoomID(r), Monitor: r.ReadBool()}
	case 7:
		return CmdLeaveRoom{}
	case 8:
		return CmdLockRoom{Lock: r.ReadBool()}
	case 9:
		return CmdCycleRoom{Cycle: r.ReadBool()}
	case 10:
		return CmdSelectChart{ID: r.ReadI32()}
	case 11:
		return CmdRequestStart{}
	case 12:
		return CmdReady{}
	case 13:
		return CmdCancelReady{}
	case 14:
		return CmdPlayed{ID: r.ReadI32()}
	case 15:
		return CmdAbort{}
	default:
		fail("proto-clientcommand-tag-invalid")
		return nil
	}
}

// ---------- ServerCommand codec ----------

func encodeUnitResult(w *BinaryWriter, res StringResult[Unit]) {
	EncodeStringResult(w, res, func(*BinaryWriter, Unit) {})
}

func decodeUnitResult(r *BinaryReader) StringResult[Unit] {
	return DecodeStringResult(r, func(*BinaryReader) Unit { return Unit{} })
}

// EncodeServerCommand 编码一个服务端命令。
func EncodeServerCommand(w *BinaryWriter, cmd ServerCommand) {
	switch c := cmd.(type) {
	case SrvPong:
		w.WriteU8(0)
	case SrvAuthenticate:
		w.WriteU8(1)
		EncodeStringResult(w, c.Result, func(ww *BinaryWriter, info AuthInfo) {
			encodeUserInfo(ww, info.Me)
			WriteOption(ww, info.Room, encodeClientRoomState)
		})
	case SrvChat:
		w.WriteU8(2)
		encodeUnitResult(w, c.Result)
	case SrvTouches:
		w.WriteU8(3)
		w.WriteI32(c.Player)
		WriteArray(w, c.Frames, EncodeTouchFrame)
	case SrvJudges:
		w.WriteU8(4)
		w.WriteI32(c.Player)
		WriteArray(w, c.Judges, EncodeJudgeEvent)
	case SrvMessage:
		w.WriteU8(5)
		encodeMessage(w, c.Message)
	case SrvChangeState:
		w.WriteU8(6)
		encodeRoomState(w, c.State)
	case SrvChangeHost:
		w.WriteU8(7)
		w.WriteBool(c.IsHost)
	case SrvCreateRoom:
		w.WriteU8(8)
		encodeUnitResult(w, c.Result)
	case SrvJoinRoom:
		w.WriteU8(9)
		EncodeStringResult(w, c.Result, encodeJoinRoomResponse)
	case SrvOnJoinRoom:
		w.WriteU8(10)
		encodeUserInfo(w, c.Info)
	case SrvLeaveRoom:
		w.WriteU8(11)
		encodeUnitResult(w, c.Result)
	case SrvLockRoom:
		w.WriteU8(12)
		encodeUnitResult(w, c.Result)
	case SrvCycleRoom:
		w.WriteU8(13)
		encodeUnitResult(w, c.Result)
	case SrvSelectChart:
		w.WriteU8(14)
		encodeUnitResult(w, c.Result)
	case SrvRequestStart:
		w.WriteU8(15)
		encodeUnitResult(w, c.Result)
	case SrvReady:
		w.WriteU8(16)
		encodeUnitResult(w, c.Result)
	case SrvCancelReady:
		w.WriteU8(17)
		encodeUnitResult(w, c.Result)
	case SrvPlayed:
		w.WriteU8(18)
		encodeUnitResult(w, c.Result)
	case SrvAbort:
		w.WriteU8(19)
		encodeUnitResult(w, c.Result)
	default:
		fail("proto-servercommand-invalid")
	}
}

// DecodeServerCommand 解码一个服务端命令。
func DecodeServerCommand(r *BinaryReader) ServerCommand {
	tag := r.ReadU8()
	switch tag {
	case 0:
		return SrvPong{}
	case 1:
		result := DecodeStringResult(r, func(rr *BinaryReader) AuthInfo {
			me := decodeUserInfo(rr)
			room := ReadOption(rr, decodeClientRoomState)
			return AuthInfo{Me: me, Room: room}
		})
		return SrvAuthenticate{Result: result}
	case 2:
		return SrvChat{Result: decodeUnitResult(r)}
	case 3:
		return SrvTouches{Player: r.ReadI32(), Frames: ReadArray(r, DecodeTouchFrame)}
	case 4:
		return SrvJudges{Player: r.ReadI32(), Judges: ReadArray(r, DecodeJudgeEvent)}
	case 5:
		return SrvMessage{Message: decodeMessage(r)}
	case 6:
		return SrvChangeState{State: decodeRoomState(r)}
	case 7:
		return SrvChangeHost{IsHost: r.ReadBool()}
	case 8:
		return SrvCreateRoom{Result: decodeUnitResult(r)}
	case 9:
		return SrvJoinRoom{Result: DecodeStringResult(r, decodeJoinRoomResponse)}
	case 10:
		return SrvOnJoinRoom{Info: decodeUserInfo(r)}
	case 11:
		return SrvLeaveRoom{Result: decodeUnitResult(r)}
	case 12:
		return SrvLockRoom{Result: decodeUnitResult(r)}
	case 13:
		return SrvCycleRoom{Result: decodeUnitResult(r)}
	case 14:
		return SrvSelectChart{Result: decodeUnitResult(r)}
	case 15:
		return SrvRequestStart{Result: decodeUnitResult(r)}
	case 16:
		return SrvReady{Result: decodeUnitResult(r)}
	case 17:
		return SrvCancelReady{Result: decodeUnitResult(r)}
	case 18:
		return SrvPlayed{Result: decodeUnitResult(r)}
	case 19:
		return SrvAbort{Result: decodeUnitResult(r)}
	default:
		fail("proto-servercommand-tag-invalid")
		return nil
	}
}
