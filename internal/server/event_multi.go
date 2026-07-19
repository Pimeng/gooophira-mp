package server

// EventSinks fans out synchronously to sinks whose Emit methods must remain
// non-blocking. It is used during migration while legacy Webhook delivery and
// the Agent outbox run in parallel.
type EventSinks []EventSink

func (sinks EventSinks) Emit(event Event) {
	for _, sink := range sinks {
		if sink != nil {
			sink.Emit(event)
		}
	}
}
