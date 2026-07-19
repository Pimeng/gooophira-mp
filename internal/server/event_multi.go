package server

// EventSinks 同步扇出到各个接收器，其 Emit 方法必须保持非阻塞。
// 它用于迁移期间，让旧版 Webhook 投递与 Agent outbox 并行运行。
type EventSinks []EventSink

func (sinks EventSinks) Emit(event Event) {
	for _, sink := range sinks {
		if sink != nil {
			sink.Emit(event)
		}
	}
}
