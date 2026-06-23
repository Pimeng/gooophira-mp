package protocol

import (
	"encoding/binary"
	"math"
	"slices"
)

// 二进制数据读写：BinaryReader / BinaryWriter，对 []byte 做类型安全的顺序读写。
// 所有多字节数值均为小端序（Little Endian）。
//
// 错误处理：读取越界等错误以 panic(ProtocolError) 抛出，由 DecodePacket 在包
// 边界 recover 成普通 error。这样每个字段的解码函数无需逐个返回 error，保持与
// TS 端「抛异常」结构一致、代码紧凑。编码路径不会失败。

// ProtocolError 表示协议编解码过程中的错误。
type ProtocolError struct{ Msg string }

func (e ProtocolError) Error() string { return e.Msg }

func fail(msg string) { panic(ProtocolError{Msg: msg}) }

// BinaryReader 从底层缓冲区顺序读取各种数据类型，内部维护读偏移。
type BinaryReader struct {
	Buf    []byte
	Offset int
}

// NewBinaryReader 在给定缓冲区上创建读取器。
func NewBinaryReader(buf []byte) *BinaryReader { return &BinaryReader{Buf: buf} }

func (r *BinaryReader) ensure(need int) {
	if r.Offset+need > len(r.Buf) {
		fail("binary-unexpected-eof")
	}
}

// Take 读取并返回接下来的 n 个字节（底层缓冲区的切片，不拷贝）。
func (r *BinaryReader) Take(n int) []byte {
	r.ensure(n)
	out := r.Buf[r.Offset : r.Offset+n]
	r.Offset += n
	return out
}

func (r *BinaryReader) ReadU8() uint8 {
	r.ensure(1)
	v := r.Buf[r.Offset]
	r.Offset++
	return v
}

func (r *BinaryReader) ReadI8() int8 { return int8(r.ReadU8()) }

func (r *BinaryReader) ReadBool() bool { return r.ReadU8() == 1 }

func (r *BinaryReader) ReadU16() uint16 {
	r.ensure(2)
	v := binary.LittleEndian.Uint16(r.Buf[r.Offset:])
	r.Offset += 2
	return v
}

func (r *BinaryReader) ReadU32() uint32 {
	r.ensure(4)
	v := binary.LittleEndian.Uint32(r.Buf[r.Offset:])
	r.Offset += 4
	return v
}

func (r *BinaryReader) ReadI32() int32 { return int32(r.ReadU32()) }

func (r *BinaryReader) ReadU64() uint64 {
	r.ensure(8)
	v := binary.LittleEndian.Uint64(r.Buf[r.Offset:])
	r.Offset += 8
	return v
}

func (r *BinaryReader) ReadI64() int64 { return int64(r.ReadU64()) }

func (r *BinaryReader) ReadF32() float32 {
	return math.Float32frombits(r.ReadU32())
}

// ReadUleb 读取 LEB128 无符号变长整数（最多到 64 位）。
func (r *BinaryReader) ReadUleb() uint64 {
	var result uint64
	var shift uint
	for {
		b := r.ReadU8()
		result |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return result
		}
		shift += 7
	}
}

// readLen 读取一个用作长度前缀的 LEB128 值，并校验其落在 int 可表示范围内。
func (r *BinaryReader) readLen() int {
	v := r.ReadUleb()
	if v > math.MaxInt32 {
		fail("binary-length-too-large")
	}
	return int(v)
}

// ReadString 读取 LEB128 长度前缀的 UTF-8 字符串。
func (r *BinaryReader) ReadString() string {
	n := r.readLen()
	return string(r.Take(n))
}

// ReadVarchar 读取带最大长度限制的字符串，超限则 fail。
func (r *BinaryReader) ReadVarchar(maxLen int) string {
	n := r.readLen()
	if n > maxLen {
		fail("binary-string-too-long")
	}
	return string(r.Take(n))
}

// ReadUUID 读取 UUID（线上为 low, high 两个 u64）。
func (r *BinaryReader) ReadUUID() string {
	low := r.ReadU64()
	high := r.ReadU64()
	return U64PairToUUID(high, low)
}

// ReadCompactPos 读取压缩坐标（两个 f16 位模式）。
func (r *BinaryReader) ReadCompactPos() CompactPos {
	x := F16BitsToF32(r.ReadU16())
	y := F16BitsToF32(r.ReadU16())
	return CompactPos{X: x, Y: y}
}

// ReadOption 读取 Option<T>：false → nil，true → 指向解码值的指针。
func ReadOption[T any](r *BinaryReader, decode func(*BinaryReader) T) *T {
	if r.ReadBool() {
		v := decode(r)
		return &v
	}
	return nil
}

// ReadArray 读取 LEB128 长度前缀的数组。
func ReadArray[T any](r *BinaryReader, decode func(*BinaryReader) T) []T {
	n := r.readLen()
	out := make([]T, n)
	for i := range n {
		out[i] = decode(r)
	}
	return out
}

// ReadMap 读取 LEB128 大小前缀的 Map。
func ReadMap[K comparable, V any](r *BinaryReader, decodeK func(*BinaryReader) K, decodeV func(*BinaryReader) V) map[K]V {
	n := r.readLen()
	out := make(map[K]V, n)
	for range n {
		k := decodeK(r)
		v := decodeV(r)
		out[k] = v
	}
	return out
}

// BinaryWriter 顺序写入各种数据类型。内部 buf 的 len 即当前写位置；body 从
// reserveHead 偏移开始，预留头部供 ToFrameBuffer 原地回填长度前缀。
type BinaryWriter struct {
	buf         []byte
	reserveHead int
}

// NewBinaryWriter 创建普通写入器。
func NewBinaryWriter() *BinaryWriter {
	return &BinaryWriter{buf: make([]byte, 0, 512)}
}

// NewFrameWriter 创建预留头部的写入器，配合 ToFrameBuffer 一次性产出带长度前缀
// 的完整帧。reserveHead 至少为 5（u32 LEB128 最多 5 字节）。
func NewFrameWriter(reserveHead int) *BinaryWriter {
	return &BinaryWriter{buf: make([]byte, reserveHead, 512), reserveHead: reserveHead}
}

// Reset 重置写位置以复用缓冲区，避免重复分配。
func (w *BinaryWriter) Reset() { w.buf = w.buf[:w.reserveHead] }

// ToBuffer 返回已写入的 body（不含预留头部）。
func (w *BinaryWriter) ToBuffer() []byte { return w.buf[w.reserveHead:] }

// ToFrameBuffer 把 body 长度的 LEB128(u32) 前缀原地回填进预留头部，返回
// 「长度前缀 + body」的完整帧。要求构造时 reserveHead 足够容纳前缀。
func (w *BinaryWriter) ToFrameBuffer() []byte {
	bodyLen := len(w.buf) - w.reserveHead
	prefixLen := 1
	for v := uint32(bodyLen) >> 7; v != 0; v >>= 7 {
		prefixLen++
	}
	if prefixLen > w.reserveHead {
		fail("frame-reserve-head-too-small")
	}
	start := w.reserveHead - prefixLen
	x := uint32(bodyLen)
	i := start
	for x >= 0x80 {
		w.buf[i] = byte(x&0x7F) | 0x80
		i++
		x >>= 7
	}
	w.buf[i] = byte(x)
	return w.buf[start:]
}

// WriteBuffer 追加原始字节。
func (w *BinaryWriter) WriteBuffer(b []byte) { w.buf = append(w.buf, b...) }

func (w *BinaryWriter) WriteU8(v uint8) { w.buf = append(w.buf, v) }

func (w *BinaryWriter) WriteI8(v int8) { w.buf = append(w.buf, byte(v)) }

func (w *BinaryWriter) WriteBool(v bool) {
	if v {
		w.buf = append(w.buf, 1)
	} else {
		w.buf = append(w.buf, 0)
	}
}

func (w *BinaryWriter) WriteU16(v uint16) { w.buf = binary.LittleEndian.AppendUint16(w.buf, v) }

func (w *BinaryWriter) WriteU32(v uint32) { w.buf = binary.LittleEndian.AppendUint32(w.buf, v) }

func (w *BinaryWriter) WriteI32(v int32) { w.buf = binary.LittleEndian.AppendUint32(w.buf, uint32(v)) }

func (w *BinaryWriter) WriteU64(v uint64) { w.buf = binary.LittleEndian.AppendUint64(w.buf, v) }

func (w *BinaryWriter) WriteI64(v int64) { w.buf = binary.LittleEndian.AppendUint64(w.buf, uint64(v)) }

func (w *BinaryWriter) WriteF32(v float32) {
	w.buf = binary.LittleEndian.AppendUint32(w.buf, math.Float32bits(v))
}

// WriteUleb 写入 LEB128 无符号变长整数。
func (w *BinaryWriter) WriteUleb(v uint64) {
	for v >= 0x80 {
		w.buf = append(w.buf, byte(v&0x7F)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
}

// WriteString 写入 UTF-8 字符串（LEB128 长度前缀 + 内容）。
func (w *BinaryWriter) WriteString(s string) {
	w.WriteUleb(uint64(len(s)))
	w.buf = append(w.buf, s...)
}

// WriteVarchar 写入带最大字节数限制的字符串，超限则 fail。
func (w *BinaryWriter) WriteVarchar(maxLen int, s string) {
	if len(s) > maxLen {
		fail("binary-string-too-long")
	}
	w.WriteUleb(uint64(len(s)))
	w.buf = append(w.buf, s...)
}

// WriteUUID 写入 UUID（line 上为 low, high 两个 u64）。
func (w *BinaryWriter) WriteUUID(uuid string) {
	high, low, err := UUIDToU64Pair(uuid)
	if err != nil {
		fail("binary-invalid-uuid")
	}
	w.WriteU64(low)
	w.WriteU64(high)
}

// WriteCompactPos 写入压缩坐标（两个 f16 位模式）。
func (w *BinaryWriter) WriteCompactPos(p CompactPos) {
	w.WriteU16(F32ToF16Bits(p.X))
	w.WriteU16(F32ToF16Bits(p.Y))
}

// WriteOption 写入 Option<T>：nil → false，非 nil → true + 值。
func WriteOption[T any](w *BinaryWriter, value *T, encode func(*BinaryWriter, T)) {
	if value == nil {
		w.WriteBool(false)
		return
	}
	w.WriteBool(true)
	encode(w, *value)
}

// WriteArray 写入数组（LEB128 长度前缀 + 逐元素编码）。
func WriteArray[T any](w *BinaryWriter, arr []T, encode func(*BinaryWriter, T)) {
	w.WriteUleb(uint64(len(arr)))
	for i := range arr {
		encode(w, arr[i])
	}
}

// WriteSortedMap 写入 map，按 key 升序编码以保证确定性输出（与 TS 端排序行为一致）。
func WriteSortedMap[V any](w *BinaryWriter, m map[int32]V, encodeK func(*BinaryWriter, int32), encodeV func(*BinaryWriter, V)) {
	keys := make([]int32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	w.WriteUleb(uint64(len(keys)))
	for _, k := range keys {
		encodeK(w, k)
		encodeV(w, m[k])
	}
}

// DecodePacket 在包边界解码：把解码过程中的 ProtocolError panic 转为返回值 error。
func DecodePacket[T any](data []byte, decode func(*BinaryReader) T) (result T, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if pe, ok := rec.(ProtocolError); ok {
				err = pe
				return
			}
			panic(rec)
		}
	}()
	result = decode(NewBinaryReader(data))
	return
}

// EncodePacket 编码一个值为字节切片。
func EncodePacket[T any](value T, encode func(*BinaryWriter, T)) []byte {
	w := NewBinaryWriter()
	encode(w, value)
	return w.ToBuffer()
}

// StringResult 是错误类型固定为 string 的 Rust 风格 Result，用于线上协议。
type StringResult[T any] struct {
	Ok    bool
	Value T
	Error string
}

// Ok 构造成功的 StringResult。
func Ok[T any](value T) StringResult[T] { return StringResult[T]{Ok: true, Value: value} }

// Errr 构造失败的 StringResult。（命名避让内置 error 习惯用名。）
func Errr[T any](msg string) StringResult[T] { return StringResult[T]{Ok: false, Error: msg} }

// EncodeStringResult 编码 StringResult（错误固定为 string）。
func EncodeStringResult[T any](w *BinaryWriter, value StringResult[T], encodeOk func(*BinaryWriter, T)) {
	if value.Ok {
		w.WriteBool(true)
		encodeOk(w, value.Value)
	} else {
		w.WriteBool(false)
		w.WriteString(value.Error)
	}
}

// DecodeStringResult 解码 StringResult（错误固定为 string）。
func DecodeStringResult[T any](r *BinaryReader, decodeOk func(*BinaryReader) T) StringResult[T] {
	if r.ReadBool() {
		return StringResult[T]{Ok: true, Value: decodeOk(r)}
	}
	return StringResult[T]{Ok: false, Error: r.ReadString()}
}

// Unit 表示空负载（对应 TS 的 Record<never, never>），用于无数据的 Result.Ok。
type Unit struct{}
