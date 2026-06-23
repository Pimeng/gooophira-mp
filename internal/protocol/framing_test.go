package protocol

import (
	"bytes"
	"testing"
)

func TestEncodeLengthPrefixU32(t *testing.T) {
	cases := []struct {
		in   int
		want []byte
	}{
		{0, []byte{0x00}},
		{127, []byte{0x7F}},
		{128, []byte{0x80, 0x01}},
		{16383, []byte{0xFF, 0x7F}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{0xFFFFFFFF, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}},
	}
	for _, c := range cases {
		got, err := EncodeLengthPrefixU32(c.in)
		if err != nil {
			t.Fatalf("EncodeLengthPrefixU32(%d) err %v", c.in, err)
		}
		if !bytes.Equal(got, c.want) {
			t.Fatalf("EncodeLengthPrefixU32(%d) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := EncodeLengthPrefixU32(-1); err != ErrFrameInvalidLength {
		t.Fatalf("negative length should error, got %v", err)
	}
}

func TestFrameWithLengthPrefix_Equivalence(t *testing.T) {
	for _, length := range []int{0, 1, 5, 127, 128, 16383, 16384, 70000} {
		body := make([]byte, length)
		for i := range body {
			body[i] = byte((i * 31) & 0xFF)
		}
		prefix, _ := EncodeLengthPrefixU32(length)
		expected := append(append([]byte{}, prefix...), body...)
		if got := FrameWithLengthPrefix(body); !bytes.Equal(got, expected) {
			t.Fatalf("FrameWithLengthPrefix mismatch for len %d", length)
		}
	}
}

func TestTryDecodeFrame_Basic(t *testing.T) {
	// 空缓冲 → need_more
	if r := TryDecodeFrame(nil, 0); r.Kind != FrameNeedMore {
		t.Fatal("empty should be need_more")
	}
	// 单字节长度帧
	frame := FrameWithLengthPrefix([]byte("hello"))
	r := TryDecodeFrame(frame, 0)
	if r.Kind != FrameOK || string(r.Payload) != "hello" || len(r.Remaining) != 0 {
		t.Fatalf("basic decode failed: %+v", r)
	}
	// 多字节长度帧
	big := make([]byte, 200)
	r = TryDecodeFrame(FrameWithLengthPrefix(big), 0)
	if r.Kind != FrameOK || len(r.Payload) != 200 {
		t.Fatalf("200-byte frame failed: %+v", r)
	}
}

func TestTryDecodeFrame_NeedMore(t *testing.T) {
	frame := FrameWithLengthPrefix([]byte("hello"))
	if r := TryDecodeFrame(frame[:3], 0); r.Kind != FrameNeedMore {
		t.Fatal("truncated body should be need_more")
	}
	if r := TryDecodeFrame([]byte{0x80}, 0); r.Kind != FrameNeedMore {
		t.Fatal("truncated prefix should be need_more")
	}
}

func TestTryDecodeFrame_TooLarge(t *testing.T) {
	frame := FrameWithLengthPrefix(make([]byte, 100))
	r := TryDecodeFrame(frame, 50)
	if r.Kind != FrameError || r.Err != ErrFramePayloadTooLarge {
		t.Fatalf("expected payload-too-large, got %+v", r)
	}
	// 恰好等于上限应通过
	if r := TryDecodeFrame(frame, 100); r.Kind != FrameOK {
		t.Fatalf("exactly-at-limit should be ok, got %+v", r)
	}
}

func TestTryDecodeFrame_InvalidPrefix(t *testing.T) {
	frame := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x80} // 第五字节仍有继续位
	r := TryDecodeFrame(frame, 0)
	if r.Kind != FrameError || r.Err != ErrFrameInvalidLengthPrefix {
		t.Fatalf("expected invalid-length-prefix, got %+v", r)
	}
}

func TestTryDecodeFrame_Multiple(t *testing.T) {
	f1 := FrameWithLengthPrefix([]byte("abc"))
	f2 := FrameWithLengthPrefix([]byte("de"))
	combined := append(append([]byte{}, f1...), f2...)
	r1 := TryDecodeFrame(combined, 0)
	if r1.Kind != FrameOK || string(r1.Payload) != "abc" {
		t.Fatalf("first frame failed: %+v", r1)
	}
	r2 := TryDecodeFrame(r1.Remaining, 0)
	if r2.Kind != FrameOK || string(r2.Payload) != "de" {
		t.Fatalf("second frame failed: %+v", r2)
	}
}

func TestTryDecodeFrame_EmptyPayload(t *testing.T) {
	frame := FrameWithLengthPrefix([]byte{})
	r := TryDecodeFrame(frame, 0)
	if r.Kind != FrameOK || len(r.Payload) != 0 {
		t.Fatalf("empty payload frame failed: %+v", r)
	}
}
