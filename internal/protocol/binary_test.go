package protocol

import (
	"bytes"
	"testing"
)

func TestReader_BasicInts(t *testing.T) {
	r := NewBinaryReader([]byte{0, 255, 128})
	if r.ReadU8() != 0 || r.ReadU8() != 255 || r.ReadU8() != 128 {
		t.Fatal("ReadU8 mismatch")
	}
	r = NewBinaryReader([]byte{0, 255, 128})
	if r.ReadI8() != 0 || r.ReadI8() != -1 || r.ReadI8() != -128 {
		t.Fatal("ReadI8 mismatch")
	}
	r = NewBinaryReader([]byte{0, 1, 2})
	if r.ReadBool() != false || r.ReadBool() != true || r.ReadBool() != false {
		t.Fatal("ReadBool mismatch")
	}
}

func TestReader_WideInts(t *testing.T) {
	w := NewBinaryWriter()
	w.WriteU16(0x1234)
	w.WriteU32(0xDEADBEEF)
	w.WriteI32(-1)
	w.WriteU64(0x123456789ABCDEF0)
	w.WriteI64(-1)
	r := NewBinaryReader(w.ToBuffer())
	if r.ReadU16() != 0x1234 {
		t.Fatal("U16")
	}
	if r.ReadU32() != 0xDEADBEEF {
		t.Fatal("U32")
	}
	if r.ReadI32() != -1 {
		t.Fatal("I32")
	}
	if r.ReadU64() != 0x123456789ABCDEF0 {
		t.Fatal("U64")
	}
	if r.ReadI64() != -1 {
		t.Fatal("I64")
	}
}

func TestReader_F32(t *testing.T) {
	w := NewBinaryWriter()
	w.WriteF32(3.14)
	r := NewBinaryReader(w.ToBuffer())
	if got := r.ReadF32(); got < 3.13 || got > 3.15 {
		t.Fatalf("ReadF32 = %v", got)
	}
}

func TestUleb_Roundtrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 16383, 16384, 1 << 40} {
		w := NewBinaryWriter()
		w.WriteUleb(v)
		r := NewBinaryReader(w.ToBuffer())
		if got := r.ReadUleb(); got != v {
			t.Fatalf("ULEB roundtrip %d -> %d", v, got)
		}
	}
	// 128 编码为 [0x80, 0x01]
	w := NewBinaryWriter()
	w.WriteUleb(128)
	if !bytes.Equal(w.ToBuffer(), []byte{0x80, 0x01}) {
		t.Fatalf("WriteUleb(128) = %v", w.ToBuffer())
	}
}

func TestString_Roundtrip(t *testing.T) {
	w := NewBinaryWriter()
	w.WriteString("hello")
	buf := w.ToBuffer()
	if buf[0] != 5 || string(buf[1:]) != "hello" {
		t.Fatalf("WriteString layout wrong: %v", buf)
	}
	r := NewBinaryReader(buf)
	if r.ReadString() != "hello" {
		t.Fatal("ReadString mismatch")
	}
}

func TestVarchar_TooLong(t *testing.T) {
	// 写入超长应 panic→error
	_, err := encodeWithRecover(func(w *BinaryWriter) { w.WriteVarchar(5, "hello world") })
	if err == nil || err.Error() != "binary-string-too-long" {
		t.Fatalf("WriteVarchar over limit err = %v", err)
	}
	// 读取超长应 error
	w := NewBinaryWriter()
	w.WriteString("hello world")
	_, err = DecodePacket(w.ToBuffer(), func(r *BinaryReader) string { return r.ReadVarchar(5) })
	if err == nil || err.Error() != "binary-string-too-long" {
		t.Fatalf("ReadVarchar over limit err = %v", err)
	}
}

func TestReader_EOF(t *testing.T) {
	_, err := DecodePacket([]byte{1}, func(r *BinaryReader) uint16 { return r.ReadU16() })
	if err == nil || err.Error() != "binary-unexpected-eof" {
		t.Fatalf("expected eof error, got %v", err)
	}
}

func TestReadLen_TooLarge(t *testing.T) {
	w := NewBinaryWriter()
	w.WriteUleb(1 << 53) // 超过 int32 上限的长度前缀
	_, err := DecodePacket(w.ToBuffer(), func(r *BinaryReader) string { return r.ReadString() })
	if err == nil || err.Error() != "binary-length-too-large" {
		t.Fatalf("expected length-too-large, got %v", err)
	}
}

func TestOption_Roundtrip(t *testing.T) {
	w := NewBinaryWriter()
	val := uint32(42)
	WriteOption(w, &val, func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	WriteOption[uint32](w, nil, func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	// 期望: [1, 42,0,0,0, 0]
	if !bytes.Equal(w.ToBuffer(), []byte{1, 42, 0, 0, 0, 0}) {
		t.Fatalf("WriteOption layout = %v", w.ToBuffer())
	}
	r := NewBinaryReader(w.ToBuffer())
	got := ReadOption(r, func(rr *BinaryReader) uint32 { return rr.ReadU32() })
	if got == nil || *got != 42 {
		t.Fatal("ReadOption some mismatch")
	}
	if ReadOption(r, func(rr *BinaryReader) uint32 { return rr.ReadU32() }) != nil {
		t.Fatal("ReadOption none mismatch")
	}
}

func TestArray_Roundtrip(t *testing.T) {
	w := NewBinaryWriter()
	WriteArray(w, []uint32{1, 2, 3}, func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	r := NewBinaryReader(w.ToBuffer())
	got := ReadArray(r, func(rr *BinaryReader) uint32 { return rr.ReadU32() })
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("array roundtrip = %v", got)
	}
}

func TestMap_Roundtrip(t *testing.T) {
	w := NewBinaryWriter()
	WriteSortedMap(w, map[int32]uint32{1: 10, 2: 20},
		func(ww *BinaryWriter, k int32) { ww.WriteI32(k) },
		func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	r := NewBinaryReader(w.ToBuffer())
	got := ReadMap(r,
		func(rr *BinaryReader) int32 { return rr.ReadI32() },
		func(rr *BinaryReader) uint32 { return rr.ReadU32() })
	if got[1] != 10 || got[2] != 20 {
		t.Fatalf("map roundtrip = %v", got)
	}
}

func TestUUID_Roundtrip(t *testing.T) {
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	w := NewBinaryWriter()
	w.WriteUUID(uuid)
	r := NewBinaryReader(w.ToBuffer())
	if got := r.ReadUUID(); got != uuid {
		t.Fatalf("UUID roundtrip %s -> %s", uuid, got)
	}
}

func TestCompactPos_Roundtrip(t *testing.T) {
	w := NewBinaryWriter()
	w.WriteCompactPos(CompactPos{X: 1.5, Y: -2.5})
	r := NewBinaryReader(w.ToBuffer())
	pos := r.ReadCompactPos()
	if pos.X != 1.5 || pos.Y != -2.5 {
		t.Fatalf("CompactPos roundtrip = %+v", pos)
	}
}

func TestStringResult_Roundtrip(t *testing.T) {
	w := NewBinaryWriter()
	EncodeStringResult(w, Ok[uint32](42), func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	EncodeStringResult(w, Errr[uint32]("bad"), func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	r := NewBinaryReader(w.ToBuffer())
	res1 := DecodeStringResult(r, func(rr *BinaryReader) uint32 { return rr.ReadU32() })
	if !res1.Ok || res1.Value != 42 {
		t.Fatalf("StringResult ok = %+v", res1)
	}
	res2 := DecodeStringResult(r, func(rr *BinaryReader) uint32 { return rr.ReadU32() })
	if res2.Ok || res2.Error != "bad" {
		t.Fatalf("StringResult err = %+v", res2)
	}
}

func TestFrameWriter_Equivalence(t *testing.T) {
	for _, bodyLen := range []int{0, 1, 127, 128, 16384} {
		w := NewFrameWriter(5)
		for i := range bodyLen {
			w.WriteU8(uint8((i * 7) & 0xFF))
		}
		frame := w.ToFrameBuffer()
		prefix, _ := EncodeLengthPrefixU32(bodyLen)
		if !bytes.Equal(frame[:len(prefix)], prefix) {
			t.Fatalf("frame prefix mismatch for len %d", bodyLen)
		}
		if len(frame) != len(prefix)+bodyLen {
			t.Fatalf("frame total length mismatch for len %d", bodyLen)
		}
		for i := range bodyLen {
			if frame[len(prefix)+i] != uint8((i*7)&0xFF) {
				t.Fatalf("frame body mismatch at %d", i)
			}
		}
	}
}

func TestFrameWriter_ReserveTooSmall(t *testing.T) {
	w := NewFrameWriter(1)
	for range 200 {
		w.WriteU8(0)
	}
	_, err := encodeWithRecover(func(*BinaryWriter) {})
	_ = err
	// ToFrameBuffer 需要 2 字节前缀但只预留 1
	_, err = recoverFrame(w)
	if err == nil || err.Error() != "frame-reserve-head-too-small" {
		t.Fatalf("expected reserve-head error, got %v", err)
	}
}

// encodeWithRecover 运行编码闭包并把 ProtocolError panic 转为 error。
func encodeWithRecover(fn func(*BinaryWriter)) (buf []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if pe, ok := rec.(ProtocolError); ok {
				err = pe
				return
			}
			panic(rec)
		}
	}()
	w := NewBinaryWriter()
	fn(w)
	return w.ToBuffer(), nil
}

func recoverFrame(w *BinaryWriter) (buf []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if pe, ok := rec.(ProtocolError); ok {
				err = pe
				return
			}
			panic(rec)
		}
	}()
	return w.ToFrameBuffer(), nil
}

// ---------- 基准测试 ----------

func BenchmarkBinaryWriteBasicTypes(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		w := NewBinaryWriter()
		w.WriteU8(255)
		w.WriteU16(0x1234)
		w.WriteU32(0xDEADBEEF)
		w.WriteU64(0x123456789ABCDEF0)
		w.WriteI8(-1)
		w.WriteI32(-42)
		w.WriteF32(3.14159)
		w.WriteBool(true)
		w.WriteUleb(1 << 20)
		w.WriteString("hello-world-benchmark")
		_ = w.ToBuffer()
	}
}

func BenchmarkBinaryReadBasicTypes(b *testing.B) {
	w := NewBinaryWriter()
	w.WriteU8(255)
	w.WriteU16(0x1234)
	w.WriteU32(0xDEADBEEF)
	w.WriteU64(0x123456789ABCDEF0)
	w.WriteI8(-1)
	w.WriteI32(-42)
	w.WriteF32(3.14159)
	w.WriteBool(true)
	w.WriteUleb(1 << 20)
	w.WriteString("hello-world-benchmark")
	data := w.ToBuffer()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := NewBinaryReader(data)
		_ = r.ReadU8()
		_ = r.ReadU16()
		_ = r.ReadU32()
		_ = r.ReadU64()
		_ = r.ReadI8()
		_ = r.ReadI32()
		_ = r.ReadF32()
		_ = r.ReadBool()
		_ = r.ReadUleb()
		_ = r.ReadString()
	}
}

func BenchmarkBinaryWriteArray(b *testing.B) {
	items := make([]uint32, 100)
	for i := range items {
		items[i] = uint32(i * 7)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := NewBinaryWriter()
		WriteArray(w, items, func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
		_ = w.ToBuffer()
	}
}

func BenchmarkBinaryReadArray(b *testing.B) {
	items := make([]uint32, 100)
	for i := range items {
		items[i] = uint32(i * 7)
	}
	w := NewBinaryWriter()
	WriteArray(w, items, func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	data := w.ToBuffer()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := NewBinaryReader(data)
		_ = ReadArray(r, func(rr *BinaryReader) uint32 { return rr.ReadU32() })
	}
}

func BenchmarkBinaryWriteMap(b *testing.B) {
	m := make(map[int32]uint32)
	for i := int32(0); i < 50; i++ {
		m[i] = uint32(i * 10)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := NewBinaryWriter()
		WriteSortedMap(w, m,
			func(ww *BinaryWriter, k int32) { ww.WriteI32(k) },
			func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
		_ = w.ToBuffer()
	}
}

func BenchmarkBinaryReadMap(b *testing.B) {
	m := make(map[int32]uint32)
	for i := int32(0); i < 50; i++ {
		m[i] = uint32(i * 10)
	}
	w := NewBinaryWriter()
	WriteSortedMap(w, m,
		func(ww *BinaryWriter, k int32) { ww.WriteI32(k) },
		func(ww *BinaryWriter, v uint32) { ww.WriteU32(v) })
	data := w.ToBuffer()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := NewBinaryReader(data)
		_ = ReadMap(r,
			func(rr *BinaryReader) int32 { return rr.ReadI32() },
			func(rr *BinaryReader) uint32 { return rr.ReadU32() })
	}
}

func BenchmarkEncodeClientCommand(b *testing.B) {
	cmds := []ClientCommand{
		CmdPing{},
		CmdAuthenticate{Token: "test-token-123"},
		CmdChat{Message: "hello everyone!"},
		CmdCreateRoom{ID: "room-bench"},
		CmdJoinRoom{ID: "room-bench", Monitor: false},
		CmdLeaveRoom{},
		CmdLockRoom{Lock: true},
		CmdCycleRoom{Cycle: true},
		CmdSelectChart{ID: 42},
		CmdRequestStart{},
		CmdReady{},
		CmdCancelReady{},
		CmdPlayed{ID: 100},
		CmdAbort{},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, cmd := range cmds {
			w := NewBinaryWriter()
			EncodeClientCommand(w, cmd)
			_ = w.ToBuffer()
		}
	}
}

func BenchmarkDecodeClientCommand(b *testing.B) {
	// 预编码所有命令
	cmds := []ClientCommand{
		CmdPing{},
		CmdAuthenticate{Token: "test-token-123"},
		CmdChat{Message: "hello everyone!"},
		CmdCreateRoom{ID: "room-bench"},
		CmdJoinRoom{ID: "room-bench", Monitor: false},
		CmdLeaveRoom{},
		CmdLockRoom{Lock: true},
		CmdCycleRoom{Cycle: true},
		CmdSelectChart{ID: 42},
		CmdRequestStart{},
		CmdReady{},
		CmdCancelReady{},
		CmdPlayed{ID: 100},
		CmdAbort{},
	}
	encoded := make([][]byte, len(cmds))
	for i, cmd := range cmds {
		w := NewBinaryWriter()
		EncodeClientCommand(w, cmd)
		encoded[i] = w.ToBuffer()
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, data := range encoded {
			r := NewBinaryReader(data)
			_ = DecodeClientCommand(r)
		}
	}
}

func BenchmarkEncodeServerCommand(b *testing.B) {
	info := AuthInfo{
		Me: UserInfo{ID: 1, Name: "player1", Monitor: false},
		Room: &ClientRoomState{
			ID: "room1", State: RoomStateSelectChart{ID: ptr[int32](1)},
			Live: true, Locked: false, Cycle: true,
			IsHost: true, IsReady: false,
			Users: map[int32]UserInfo{1: {ID: 1, Name: "player1"}, 2: {ID: 2, Name: "player2"}},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := NewBinaryWriter()
		EncodeServerCommand(w, SrvAuthenticate{Result: Ok(info)})
		EncodeServerCommand(w, SrvCreateRoom{Result: Ok(Unit{})})
		EncodeServerCommand(w, SrvJoinRoom{Result: Ok(JoinRoomResponse{
			State: RoomStateSelectChart{}, Users: []UserInfo{{ID: 1, Name: "p1"}}, Live: true,
		})})
		EncodeServerCommand(w, SrvMessage{Message: MsgChat{User: 1, Content: "hello"}})
		EncodeServerCommand(w, SrvChangeState{State: RoomStatePlaying{}})
		EncodeServerCommand(w, SrvReady{Result: Ok(Unit{})})
		EncodeServerCommand(w, SrvPlayed{Result: Ok(Unit{})})
		_ = w.ToBuffer()
	}
}

func BenchmarkDecodeServerCommand(b *testing.B) {
	// 预编码一组命令
	cmds := []ServerCommand{
		SrvAuthenticate{Result: Ok(AuthInfo{
			Me: UserInfo{ID: 1, Name: "player1"},
			Room: &ClientRoomState{
				ID: "room1", State: RoomStateSelectChart{ID: ptr[int32](1)},
				Users: map[int32]UserInfo{1: {ID: 1, Name: "player1"}},
			},
		})},
		SrvCreateRoom{Result: Ok(Unit{})},
		SrvJoinRoom{Result: Ok(JoinRoomResponse{
			State: RoomStateSelectChart{}, Users: []UserInfo{{ID: 1, Name: "p1"}}, Live: true,
		})},
		SrvMessage{Message: MsgChat{User: 1, Content: "hello"}},
		SrvChangeState{State: RoomStatePlaying{}},
		SrvReady{Result: Ok(Unit{})},
		SrvPlayed{Result: Ok(Unit{})},
	}
	w := NewBinaryWriter()
	for _, cmd := range cmds {
		EncodeServerCommand(w, cmd)
	}
	data := w.ToBuffer()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := NewBinaryReader(data)
		for range len(cmds) {
			_ = DecodeServerCommand(r)
		}
	}
}

// Encoding rounds (encode → decode → verify) for client commands.
func BenchmarkClientCommandRoundtrip(b *testing.B) {
	cmd := CmdJoinRoom{ID: "room-bench-123", Monitor: false}
	b.ReportAllocs()
	for b.Loop() {
		w := NewBinaryWriter()
		EncodeClientCommand(w, cmd)
		data := w.ToBuffer()
		r := NewBinaryReader(data)
		_ = DecodeClientCommand(r)
	}
}

// TouchFrame encoding benchmark — simulates heavy in-game traffic.
func BenchmarkEncodeTouchFrames(b *testing.B) {
	frames := make([]TouchFrame, 10)
	for i := range frames {
		pts := make([]TouchPoint, 20)
		for j := range pts {
			pts[j] = TouchPoint{ID: int8(j), Pos: CompactPos{X: float32(j) * 0.1, Y: float32(j) * 0.2}}
		}
		frames[i] = TouchFrame{Time: float32(i) * 0.016, Points: pts}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := NewBinaryWriter()
		EncodeClientCommand(w, CmdTouches{Frames: frames})
		_ = w.ToBuffer()
	}
}

func BenchmarkDecodeTouchFrames(b *testing.B) {
	frames := make([]TouchFrame, 10)
	for i := range frames {
		pts := make([]TouchPoint, 20)
		for j := range pts {
			pts[j] = TouchPoint{ID: int8(j), Pos: CompactPos{X: float32(j) * 0.1, Y: float32(j) * 0.2}}
		}
		frames[i] = TouchFrame{Time: float32(i) * 0.016, Points: pts}
	}
	w := NewBinaryWriter()
	EncodeClientCommand(w, CmdTouches{Frames: frames})
	data := w.ToBuffer()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := NewBinaryReader(data)
		_ = DecodeClientCommand(r)
	}
}

func ptr[T any](v T) *T { return &v }
