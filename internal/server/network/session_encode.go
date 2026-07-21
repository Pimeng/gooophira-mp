// session_encode.go 把服务端命令的帧编码与对象池从 session.go 拆出。errEncode 在
// server.go 中声明，编码失败时由 encodeServerFrame 统一转换 recover 信号为该错误。
package network

import (
	"sync"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

// frameWriterPool 是 BinaryWriter（预留 5 字节 LEB128(u32) 头部）的对象池，
// 用于复用 encodeServerFrame 中的编码缓冲区，减少热路径上的分配。
var frameWriterPool = &sync.Pool{
	New: func() any { return protocol.NewFrameWriter(5) },
}

// encodeServerFrame 把服务端命令编码为「长度前缀 + body」帧（复用对象池中的编码器）。
func encodeServerFrame(cmd protocol.ServerCommand) (frame []byte, err error) {
	w := frameWriterPool.Get().(*protocol.BinaryWriter)
	defer frameWriterPool.Put(w)
	defer func() {
		if rec := recover(); rec != nil {
			err = errEncode
		}
	}()
	w.Reset()
	protocol.EncodeServerCommand(w, cmd)
	fb := w.ToFrameBuffer()
	// fb 引用 w 的内部缓冲区；拷出后再归还，避免 writeLoop 使用时被覆写。
	frame = make([]byte, len(fb))
	copy(frame, fb)
	return frame, nil
}
