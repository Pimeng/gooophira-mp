package protocol

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestF16BitsToF32_KnownValues(t *testing.T) {
	cases := []struct {
		bits uint16
		want float32
	}{
		{0x0000, 0},
		{0x3C00, 1},
		{0xBC00, -1},
		{0x4000, 2},
		{0x3800, 0.5},
	}
	for _, c := range cases {
		if got := F16BitsToF32(c.bits); got != c.want {
			t.Errorf("F16BitsToF32(%#04x) = %v, want %v", c.bits, got, c.want)
		}
	}
}

func TestF16BitsToF32_NegativeZero(t *testing.T) {
	got := F16BitsToF32(0x8000)
	if math.Float32bits(got) != 0x80000000 {
		t.Errorf("F16BitsToF32(0x8000) bits = %#08x, want 0x80000000 (-0)", math.Float32bits(got))
	}
}

func TestF16BitsToF32_InfNaN(t *testing.T) {
	if got := F16BitsToF32(0x7C00); !math.IsInf(float64(got), 1) {
		t.Errorf("0x7C00 should be +Inf, got %v", got)
	}
	if got := F16BitsToF32(0xFC00); !math.IsInf(float64(got), -1) {
		t.Errorf("0xFC00 should be -Inf, got %v", got)
	}
	if got := F16BitsToF32(0x7E00); !math.IsNaN(float64(got)) {
		t.Errorf("0x7E00 should be NaN, got %v", got)
	}
	if got := F16BitsToF32(0x7E01); !math.IsNaN(float64(got)) {
		t.Errorf("0x7E01 should be NaN, got %v", got)
	}
}

func TestF16BitsToF32_Subnormal(t *testing.T) {
	want := float32(math.Pow(2, -14) * (1.0 / 1024))
	if got := F16BitsToF32(0x0001); got != want {
		t.Errorf("smallest subnormal = %v, want %v", got, want)
	}
	wantNeg := float32(-math.Pow(2, -14) * (1.0 / 1024))
	if got := F16BitsToF32(0x8001); got != wantNeg {
		t.Errorf("smallest negative subnormal = %v, want %v", got, wantNeg)
	}
}

func TestF32ToF16Bits_KnownValues(t *testing.T) {
	cases := []struct {
		in   float32
		want uint16
	}{
		{0, 0x0000},
		{1, 0x3C00},
		{-1, 0xBC00},
		{2, 0x4000},
		{70000, 0x7C00},  // 上溢到 +Inf
		{-70000, 0xFC00}, // 上溢到 -Inf
	}
	for _, c := range cases {
		if got := F32ToF16Bits(c.in); got != c.want {
			t.Errorf("F32ToF16Bits(%v) = %#04x, want %#04x", c.in, got, c.want)
		}
	}
}

func TestF32ToF16Bits_NegativeZero(t *testing.T) {
	if got := F32ToF16Bits(float32(math.Copysign(0, -1))); got != 0x8000 {
		t.Errorf("F32ToF16Bits(-0) = %#04x, want 0x8000", got)
	}
}

func TestF32ToF16Bits_InfNaN(t *testing.T) {
	if got := F32ToF16Bits(float32(math.Inf(1))); got != 0x7C00 {
		t.Errorf("+Inf = %#04x, want 0x7C00", got)
	}
	if got := F32ToF16Bits(float32(math.Inf(-1))); got != 0xFC00 {
		t.Errorf("-Inf = %#04x, want 0xFC00", got)
	}
	if got := F32ToF16Bits(float32(math.NaN())); got != 0x7E00 {
		t.Errorf("NaN = %#04x, want 0x7E00", got)
	}
}

// refDecodeHalf 是迁移前的纯数学参考解码，用于逐位回归比对。
func refDecodeHalf(bits uint16) float32 {
	sign := 1.0
	if bits&0x8000 != 0 {
		sign = -1.0
	}
	exp := (bits >> 10) & 0x1F
	frac := bits & 0x03FF
	if exp == 0 {
		if frac == 0 {
			return float32(sign * 0.0)
		}
		return float32(sign * math.Pow(2, -14) * (float64(frac) / 1024))
	}
	if exp == 0x1F {
		if frac == 0 {
			return float32(sign * math.Inf(1))
		}
		return float32(math.NaN())
	}
	return float32(sign * math.Pow(2, float64(exp)-15) * (1 + float64(frac)/1024))
}

func isNaNBits(bits uint16) bool {
	return bits&0x7C00 == 0x7C00 && bits&0x03FF != 0
}

// TestHalf_ExhaustiveDecode 验证 F16BitsToF32 对全部 65536 个位模式与参考实现逐位等价。
func TestHalf_ExhaustiveDecode(t *testing.T) {
	for bits := 0; bits <= 0xFFFF; bits++ {
		got := F16BitsToF32(uint16(bits))
		if isNaNBits(uint16(bits)) {
			if !math.IsNaN(float64(got)) {
				t.Fatalf("bits %#04x should decode to NaN, got %v", bits, got)
			}
			continue
		}
		if math.Float32bits(got) != math.Float32bits(refDecodeHalf(uint16(bits))) {
			t.Fatalf("bits %#04x: got %v (%#08x), want %v (%#08x)",
				bits, got, math.Float32bits(got),
				refDecodeHalf(uint16(bits)), math.Float32bits(refDecodeHalf(uint16(bits))))
		}
	}
}

// TestHalf_ExhaustiveRoundtrip 验证 decode→encode 对所有非 NaN 位模式还原自身。
func TestHalf_ExhaustiveRoundtrip(t *testing.T) {
	for bits := 0; bits <= 0xFFFF; bits++ {
		if isNaNBits(uint16(bits)) {
			continue
		}
		got := F32ToF16Bits(F16BitsToF32(uint16(bits)))
		if got != uint16(bits) {
			t.Fatalf("roundtrip bits %#04x -> %#04x", bits, got)
		}
	}
}

// TestHalf_EncodeStable 验证 encode 对量化后的值稳定（再编码不变）。
func TestHalf_EncodeStable(t *testing.T) {
	for _, v := range []float32{0, 1, -1, 0.5, -0.5, 2, 100, -100, 0.1, -0.1} {
		bits := F32ToF16Bits(v)
		if F32ToF16Bits(F16BitsToF32(bits)) != bits {
			t.Errorf("encode not stable for %v", v)
		}
	}
}

// TestHalf_RandomSampleEncodeStable 对触摸坐标典型范围 [-2,2] 的 10 万随机样本验证
// encode→decode→encode 稳定（对应 TS half.test.ts 的随机样本穷举回归）。
func TestHalf_RandomSampleEncodeStable(t *testing.T) {
	for range 100000 {
		v := float32((rand.Float64() - 0.5) * 4)
		bits := F32ToF16Bits(v)
		if F32ToF16Bits(F16BitsToF32(bits)) != bits {
			t.Fatalf("encode not stable for %v (bits %#04x)", v, bits)
		}
	}
}
