package protocol

import (
	"reflect"
	"testing"
)

func i32p(v int32) *int32 { return &v }

func roundtripClient(t *testing.T, cmd ClientCommand) {
	t.Helper()
	w := NewBinaryWriter()
	EncodeClientCommand(w, cmd)
	got, err := DecodePacket(w.ToBuffer(), DecodeClientCommand)
	if err != nil {
		t.Fatalf("decode %T: %v", cmd, err)
	}
	if !reflect.DeepEqual(got, cmd) {
		t.Fatalf("client roundtrip %T:\n got  %#v\n want %#v", cmd, got, cmd)
	}
}

func roundtripServer(t *testing.T, cmd ServerCommand) {
	t.Helper()
	w := NewBinaryWriter()
	EncodeServerCommand(w, cmd)
	got, err := DecodePacket(w.ToBuffer(), DecodeServerCommand)
	if err != nil {
		t.Fatalf("decode %T: %v", cmd, err)
	}
	if !reflect.DeepEqual(got, cmd) {
		t.Fatalf("server roundtrip %T:\n got  %#v\n want %#v", cmd, got, cmd)
	}
}

func roundtripMessage(t *testing.T, m Message) {
	t.Helper()
	w := NewBinaryWriter()
	encodeMessage(w, m)
	got, err := DecodePacket(w.ToBuffer(), decodeMessage)
	if err != nil {
		t.Fatalf("decode %T: %v", m, err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("message roundtrip %T:\n got  %#v\n want %#v", m, got, m)
	}
}

func TestClientCommand_Roundtrip(t *testing.T) {
	frames := []TouchFrame{{Time: 1.5, Points: []TouchPoint{{ID: 3, Pos: CompactPos{X: 0.5, Y: -2.5}}}}}
	judges := []JudgeEvent{{Time: 2.5, LineID: 1, NoteID: 2, Judgement: JudgeGood}}
	cmds := []ClientCommand{
		CmdPing{},
		CmdAuthenticate{Token: "abc"},
		CmdChat{Message: "hello"},
		CmdTouches{Frames: frames},
		CmdJudges{Judges: judges},
		CmdCreateRoom{ID: RoomID("room1")},
		CmdJoinRoom{ID: RoomID("room1"), Monitor: true},
		CmdLeaveRoom{},
		CmdLockRoom{Lock: true},
		CmdCycleRoom{Cycle: true},
		CmdSelectChart{ID: 42},
		CmdRequestStart{},
		CmdReady{},
		CmdCancelReady{},
		CmdPlayed{ID: 7},
		CmdAbort{},
	}
	for _, c := range cmds {
		roundtripClient(t, c)
	}
}

func TestMessage_Roundtrip(t *testing.T) {
	msgs := []Message{
		MsgChat{User: 1, Content: "hi"},
		MsgCreateRoom{User: 1},
		MsgJoinRoom{User: 1, Name: "alice"},
		MsgLeaveRoom{User: 1, Name: "alice"},
		MsgNewHost{User: 2},
		MsgSelectChart{User: 1, Name: "chart", ID: 5},
		MsgGameStart{User: 1},
		MsgReady{User: 1},
		MsgCancelReady{User: 1},
		MsgCancelGame{User: 1},
		MsgStartPlaying{},
		MsgPlayed{User: 1, Score: 1000000, Accuracy: 0.5, FullCombo: true},
		MsgGameEnd{},
		MsgAbort{User: 1},
		MsgLockRoom{Lock: true},
		MsgCycleRoom{Cycle: true},
	}
	for _, m := range msgs {
		roundtripMessage(t, m)
	}
}

func TestServerCommand_Roundtrip(t *testing.T) {
	users := map[int32]UserInfo{1: {ID: 1, Name: "alice", Monitor: false}}
	room := &ClientRoomState{
		ID: RoomID("room1"), State: RoomStateSelectChart{ID: i32p(3)},
		Live: true, Locked: false, Cycle: true, IsHost: true, IsReady: false, Users: users,
	}
	cmds := []ServerCommand{
		SrvPong{},
		SrvAuthenticate{Result: Ok(AuthInfo{Me: UserInfo{ID: 1, Name: "a"}, Room: room})},
		SrvAuthenticate{Result: Ok(AuthInfo{Me: UserInfo{ID: 1, Name: "a"}, Room: nil})},
		SrvAuthenticate{Result: Errr[AuthInfo]("bad-token")},
		SrvChat{Result: Ok(Unit{})},
		SrvChat{Result: Errr[Unit]("muted")},
		SrvTouches{Player: 1, Frames: []TouchFrame{{Time: 1, Points: []TouchPoint{{ID: 0, Pos: CompactPos{X: 0, Y: 0}}}}}},
		SrvJudges{Player: 1, Judges: []JudgeEvent{{Time: 1, LineID: 0, NoteID: 0, Judgement: JudgeMiss}}},
		SrvMessage{Message: MsgChat{User: 1, Content: "hi"}},
		SrvChangeState{State: RoomStateWaitingForReady{}},
		SrvChangeState{State: RoomStatePlaying{}},
		SrvChangeState{State: RoomStateSelectChart{ID: nil}},
		SrvChangeHost{IsHost: true},
		SrvCreateRoom{Result: Ok(Unit{})},
		SrvJoinRoom{Result: Ok(JoinRoomResponse{State: RoomStatePlaying{}, Users: []UserInfo{{ID: 1, Name: "a"}}, Live: true})},
		SrvOnJoinRoom{Info: UserInfo{ID: 2, Name: "bob", Monitor: true}},
		SrvLeaveRoom{Result: Ok(Unit{})},
		SrvLockRoom{Result: Ok(Unit{})},
		SrvCycleRoom{Result: Ok(Unit{})},
		SrvSelectChart{Result: Ok(Unit{})},
		SrvRequestStart{Result: Ok(Unit{})},
		SrvReady{Result: Ok(Unit{})},
		SrvCancelReady{Result: Ok(Unit{})},
		SrvPlayed{Result: Ok(Unit{})},
		SrvAbort{Result: Ok(Unit{})},
	}
	for _, c := range cmds {
		roundtripServer(t, c)
	}
}

// TestRoomState_SelectChartNilVsSome 确保 Option<i32> 的 nil/some 都正确往返。
func TestRoomState_SelectChartNilVsSome(t *testing.T) {
	roundtripServer(t, SrvChangeState{State: RoomStateSelectChart{ID: nil}})
	roundtripServer(t, SrvChangeState{State: RoomStateSelectChart{ID: i32p(0)}})
	roundtripServer(t, SrvChangeState{State: RoomStateSelectChart{ID: i32p(-1)}})
}

// TestClientRoomState_MapSortDeterministic 确保多 key map 编码确定（按 key 升序）。
func TestClientRoomState_MapSortDeterministic(t *testing.T) {
	users := map[int32]UserInfo{
		3: {ID: 3, Name: "c"},
		1: {ID: 1, Name: "a"},
		2: {ID: 2, Name: "b"},
	}
	state := ClientRoomState{ID: RoomID("r"), State: RoomStatePlaying{}, Users: users}
	w1 := NewBinaryWriter()
	encodeClientRoomState(w1, state)
	w2 := NewBinaryWriter()
	encodeClientRoomState(w2, state)
	if !reflect.DeepEqual(w1.ToBuffer(), w2.ToBuffer()) {
		t.Fatal("map encoding not deterministic")
	}
	got, err := DecodePacket(w1.ToBuffer(), decodeClientRoomState)
	if err != nil || len(got.Users) != 3 {
		t.Fatalf("decode client room state: %v %+v", err, got)
	}
}
