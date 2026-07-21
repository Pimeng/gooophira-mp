package replay

import (
	"bytes"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// TestDecodePayload_Zstd 验证读取侧能解压 ZSTD 压缩的 PHIRAREC 载荷
// （TS 端产出的回放文件使用 ZSTD，Go 端需兼容读取）。
func TestDecodePayload_Zstd(t *testing.T) {
	original := []byte("PHIRAREC payload content for zstd round-trip test")

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("create zstd encoder: %v", err)
	}
	compressed := enc.EncodeAll(original, nil)
	enc.Close()

	header := buildHeader(compressionZstd)
	buf := append(header, compressed...)

	decoded, err := decodePayload(buf)
	if err != nil {
		t.Fatalf("decodePayload zstd: %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("zstd roundtrip mismatch: got %q, want %q", decoded, original)
	}
}

// TestDecodePayload_Deflate 验证 DEFLATE 路径仍正常（回归保护）。
func TestDecodePayload_Deflate(t *testing.T) {
	original := []byte("PHIRAREC payload content for deflate round-trip test")

	compressed, err := compressDeflate(original)
	if err != nil {
		t.Fatalf("compressDeflate: %v", err)
	}

	header := buildHeader(compressionDeflate)
	buf := append(header, compressed...)

	decoded, err := decodePayload(buf)
	if err != nil {
		t.Fatalf("decodePayload deflate: %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("deflate roundtrip mismatch: got %q, want %q", decoded, original)
	}
}

// TestDecodePayload_None 验证无压缩路径。
func TestDecodePayload_None(t *testing.T) {
	original := []byte("uncompressed payload")

	header := buildHeader(compressionNone)
	buf := append(header, original...)

	decoded, err := decodePayload(buf)
	if err != nil {
		t.Fatalf("decodePayload none: %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("none roundtrip mismatch: got %q, want %q", decoded, original)
	}
}

// TestDecodePayload_Unsupported 验证未知压缩算法报错。
func TestDecodePayload_Unsupported(t *testing.T) {
	header := buildHeader(0xFF)
	buf := append(header, []byte("garbage")...)

	if _, err := decodePayload(buf); err == nil {
		t.Error("expected error for unsupported compression byte")
	}
}
