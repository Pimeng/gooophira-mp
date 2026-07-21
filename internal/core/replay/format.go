// Package replay 实现游戏回放录制：PHIRAREC 文件格式、存储路径与录制器。
//
// 压缩写入用 DEFLATE（Go 标准库 compress/flate）而非 TS 端默认的 ZSTD——文件头的压缩字节
// 标明算法，TS 读取侧支持 DEFLATE，故格式完全兼容。读取侧同时支持 DEFLATE 与 ZSTD，
// 以便读取 TS 端产出的 ZSTD 回放文件（zstd 仅用于解压读取，本实现不写 ZSTD）。
package replay

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/klauspost/compress/zstd"
)

// PHIRAREC 文件格式常量。
var phiraRecordMagic = []byte("PHIRAREC")

const (
	phiraRecordVersion    = 1
	phiraRecordHeaderSize = 13

	compressionNone    = 0x00
	compressionZstd    = 0x01
	compressionDeflate = 0x02
)

// buildHeader 构造 13 字节文件头：magic(8) + version(i32le) + compression(1)。
func buildHeader(compression byte) []byte {
	h := make([]byte, phiraRecordHeaderSize)
	copy(h[0:8], phiraRecordMagic)
	binary.LittleEndian.PutUint32(h[8:12], phiraRecordVersion)
	h[12] = compression
	return h
}

func compressDeflate(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressDeflate(data []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	return io.ReadAll(r)
}

// zstdReader 是进程级共享的 zstd 解码器（线程安全，复用以分摊构造开销）。
var zstdReader *zstd.Decoder

func init() {
	zstdReader, _ = zstd.NewReader(nil)
}

func decompressZstd(data []byte) ([]byte, error) {
	if zstdReader == nil {
		return nil, errCompressionUnsupported
	}
	return zstdReader.DecodeAll(data, nil)
}

// isPhiraRecordV2 判断缓冲是否为 PHIRAREC v2 文件。
func isPhiraRecordV2(buf []byte) bool {
	return len(buf) >= phiraRecordHeaderSize && bytes.Equal(buf[0:8], phiraRecordMagic)
}

// decodePayload 按文件头的压缩字节解出原始内容（供读取/测试用）。
func decodePayload(buf []byte) ([]byte, error) {
	compression := buf[12]
	payload := buf[phiraRecordHeaderSize:]
	switch compression {
	case compressionNone:
		return payload, nil
	case compressionDeflate:
		return decompressDeflate(payload)
	case compressionZstd:
		return decompressZstd(payload)
	default:
		return nil, errCompressionUnsupported
	}
}

// encodeReplayJudgeEvent 按回放格式编码判定事件。
//
// ⚠️ 回放中的 line_id/note_id 用 I32（与通信协议的 U32 不同）；为兼容既有回放文件，
// 切勿改用 protocol 的编码。
func encodeReplayJudgeEvent(w *protocol.BinaryWriter, v protocol.JudgeEvent) {
	w.WriteF32(v.Time)
	w.WriteI32(int32(v.LineID))
	w.WriteI32(int32(v.NoteID))
	w.WriteU8(uint8(v.Judgement))
}

// decodeReplayJudgeEvent 按回放格式解码判定事件（读取/测试用）。
func decodeReplayJudgeEvent(r *protocol.BinaryReader) protocol.JudgeEvent {
	time := r.ReadF32()
	lineID := r.ReadI32()
	noteID := r.ReadI32()
	judgement := protocol.Judgement(r.ReadU8())
	return protocol.JudgeEvent{Time: time, LineID: uint32(lineID), NoteID: uint32(noteID), Judgement: judgement}
}
