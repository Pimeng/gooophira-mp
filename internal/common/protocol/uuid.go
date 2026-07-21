package protocol

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// UUID 与 (high, low) u64 对的互转，以及 v4 生成。
//
// 线上把 UUID 编码为两个 64 位整数（先 low 后 high，见 binary.go 的
// WriteUUID）。这里自实现 parse/format/v4，避免引入外部依赖。
// high = 大端解释的 bytes[0:8]，low = 大端解释的 bytes[8:16]。

// ErrInvalidUUID 表示 UUID 字符串格式非法。
var ErrInvalidUUID = errors.New("uuid-invalid")

// UUIDToU64Pair 解析规范格式 UUID 字符串，返回 (high, low)。
func UUIDToU64Pair(uuid string) (high, low uint64, err error) {
	b, err := parseUUID(uuid)
	if err != nil {
		return 0, 0, err
	}
	high = binary.BigEndian.Uint64(b[0:8])
	low = binary.BigEndian.Uint64(b[8:16])
	return high, low, nil
}

// U64PairToUUID 由 (high, low) 还原规范格式 UUID 字符串。
func U64PairToUUID(high, low uint64) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], high)
	binary.BigEndian.PutUint64(b[8:16], low)
	return formatUUID(b)
}

// NewUUID 生成一个 v4（随机）UUID 字符串。
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand 失败属于不可恢复的环境问题
	}
	b[6] = (b[6] & 0x0F) | 0x40 // 版本 4
	b[8] = (b[8] & 0x3F) | 0x80 // 变体 RFC 4122
	return formatUUID(b)
}

// hexPairPositions 为规范 UUID（8-4-4-4-12，带连字符）中 16 个字节各自高位
// 十六进制字符的下标。
var hexPairPositions = [16]int{0, 2, 4, 6, 9, 11, 14, 16, 19, 21, 24, 26, 28, 30, 32, 34}

func parseUUID(s string) ([16]byte, error) {
	var b [16]byte
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return b, ErrInvalidUUID
	}
	for i, p := range hexPairPositions {
		hi := fromHexDigit(s[p])
		lo := fromHexDigit(s[p+1])
		if hi == 0xFF || lo == 0xFF {
			return b, ErrInvalidUUID
		}
		b[i] = hi<<4 | lo
	}
	return b, nil
}

func formatUUID(b [16]byte) string {
	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf[:])
}

func fromHexDigit(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0xFF
	}
}
