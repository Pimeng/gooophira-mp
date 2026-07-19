// Package protocol 实现 Phira 多人客户端与服务端之间的二进制线路协议：长度前缀帧
// 编解码、带类型的二进制读写器、IEEE-754 半精度浮点转换、UUID 处理，以及完整的
// ClientCommand/ServerCommand/Message 编解码。
package protocol

import "math"

// 半精度浮点（IEEE-754 binary16）与单精度浮点的互转。
//
// Go 标准库没有 float16，这里手写转换，遵循 round-to-nearest-even，与原版
// Phira（Rust half crate）以及 TS 端的原生 Float16Array 逐位一致。触摸点坐标
// 的编解码是高频热路径，但单点转换足够廉价，无需查表。

// F16BitsToF32 将 16 位半精度位模式解码为单精度浮点数。
// 通过直接构造 float32 的位模式保证精确性（含 ±0、次正规数、Inf/NaN）。
func F16BitsToF32(bits uint16) float32 {
	sign := uint32(bits&0x8000) << 16 // 移到 float32 的符号位
	exp := uint32(bits>>10) & 0x1F
	frac := uint32(bits & 0x03FF)

	switch exp {
	case 0:
		if frac == 0 {
			return math.Float32frombits(sign) // ±0
		}
		// 次正规数：归一化为 float32 的正规数（float32 范围远大于 float16）。
		// 值 = frac * 2^-24，左移 frac 直到隐含位 0x400 置位。
		e := uint32(127 - 15 + 1) // = 113
		for frac&0x0400 == 0 {
			frac <<= 1
			e--
		}
		frac &= 0x03FF // 丢弃隐含的前导 1
		return math.Float32frombits(sign | (e << 23) | (frac << 13))
	case 0x1F:
		if frac == 0 {
			return math.Float32frombits(sign | 0x7F800000) // 正负无穷。
		}
		return math.Float32frombits(sign | 0x7F800000 | (frac << 13)) // 非数值 NaN。
	default:
		e := exp + (127 - 15) // 重新偏置指数
		return math.Float32frombits(sign | (e << 23) | (frac << 13))
	}
}

// F32ToF16Bits 将单精度浮点数编码为 16 位半精度位模式，采用 round-to-nearest-even。
// NaN 统一编码为 0x7E00（与 TS 原生实现一致）。
func F32ToF16Bits(value float32) uint16 {
	b := math.Float32bits(value)
	sign := uint16((b >> 16) & 0x8000)
	exp := int32((b >> 23) & 0xFF)
	mant := b & 0x007FFFFF

	if exp == 0xFF {
		if mant != 0 {
			return 0x7E00 // 规范化 NaN
		}
		return sign | 0x7C00 // 正负无穷。
	}

	e := exp - 127 + 15 // 去偏置后重新按 float16 偏置

	if e >= 0x1F {
		return sign | 0x7C00 // 上溢到 Inf
	}

	if e <= 0 {
		// float16 次正规数或零
		if e < -10 {
			return sign // 太小，归零（保留符号）
		}
		mant |= 0x00800000 // 恢复隐含前导 1
		shift := uint32(14 - e)
		rem := mant & ((1 << shift) - 1)
		res := mant >> shift
		halfway := uint32(1) << (shift - 1)
		if rem > halfway || (rem == halfway && res&1 == 1) {
			res++
		}
		return sign | uint16(res)
	}

	// 正规数
	half := sign | uint16(e<<10) | uint16(mant>>13)
	rem := mant & 0x1FFF
	if rem > 0x1000 || (rem == 0x1000 && half&1 == 1) {
		half++ // 进位可能溢入指数位，符合 IEEE 行为
	}
	return half
}
