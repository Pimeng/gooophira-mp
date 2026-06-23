package protocol

import "errors"

// 帧协议：消息以 LEB128(u32) 长度前缀分帧，默认最大负载 2MB。

// DefaultMaxPayloadBytes 是默认的单帧最大负载字节数。
const DefaultMaxPayloadBytes = 2 * 1024 * 1024

// 帧解码相关错误。
var (
	ErrFrameInvalidLength       = errors.New("frame-invalid-length")
	ErrFramePayloadTooLarge     = errors.New("frame-payload-too-large")
	ErrFrameInvalidLengthPrefix = errors.New("frame-invalid-length-prefix")
)

// EncodeLengthPrefixU32 把长度编码为 LEB128(u32)，返回 1~5 字节。
func EncodeLengthPrefixU32(length int) ([]byte, error) {
	if length < 0 {
		return nil, ErrFrameInvalidLength
	}
	x := uint32(length)
	out := make([]byte, 0, 5)
	for x >= 0x80 {
		out = append(out, byte(x&0x7F)|0x80)
		x >>= 7
	}
	out = append(out, byte(x))
	return out, nil
}

// FrameWithLengthPrefix 给已编码的 body 加上 LEB128(u32) 长度前缀，返回完整帧。
func FrameWithLengthPrefix(body []byte) []byte {
	x := uint32(len(body))
	prefixLen := 1
	for v := x >> 7; v != 0; v >>= 7 {
		prefixLen++
	}
	out := make([]byte, prefixLen+len(body))
	i := 0
	for x >= 0x80 {
		out[i] = byte(x&0x7F) | 0x80
		i++
		x >>= 7
	}
	out[i] = byte(x)
	i++
	copy(out[i:], body)
	return out
}

// FrameKind 是 TryDecodeFrame 的结果类别。
type FrameKind int

const (
	// FrameNeedMore 表示缓冲区数据不足，需要继续累积。
	FrameNeedMore FrameKind = iota
	// FrameOK 表示成功解出一帧。
	FrameOK
	// FrameError 表示帧非法（长度前缀错误或负载超限）。
	FrameError
)

// DecodeFrameResult 是 TryDecodeFrame 的返回结果。
//   - Kind == FrameOK 时 Payload / Remaining 有效（均为入参缓冲区的切片，不拷贝）。
//   - Kind == FrameError 时 Err 有效。
type DecodeFrameResult struct {
	Kind      FrameKind
	Payload   []byte
	Remaining []byte
	Err       error
}

// TryDecodeFrame 尝试从缓冲区头部解出一帧。maxPayloadBytes <= 0 时用默认上限。
func TryDecodeFrame(buf []byte, maxPayloadBytes int) DecodeFrameResult {
	if maxPayloadBytes <= 0 {
		maxPayloadBytes = DefaultMaxPayloadBytes
	}
	if len(buf) == 0 {
		return DecodeFrameResult{Kind: FrameNeedMore}
	}

	var length int
	offset := 0
	shift := 0
	for {
		if offset >= len(buf) {
			return DecodeFrameResult{Kind: FrameNeedMore}
		}
		b := buf[offset]
		offset++
		length |= int(b&0x7F) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
		if shift >= 35 {
			// 第五字节（shift==28）之后仍有继续位 → 非法（u32 LEB128 至多 5 字节）。
			return DecodeFrameResult{Kind: FrameError, Err: ErrFrameInvalidLengthPrefix}
		}
	}

	if length > maxPayloadBytes {
		return DecodeFrameResult{Kind: FrameError, Err: ErrFramePayloadTooLarge}
	}
	if len(buf)-offset < length {
		return DecodeFrameResult{Kind: FrameNeedMore}
	}
	return DecodeFrameResult{
		Kind:      FrameOK,
		Payload:   buf[offset : offset+length],
		Remaining: buf[offset+length:],
	}
}
