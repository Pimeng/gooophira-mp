package server

import "testing"

type countingSink struct{ count int }

func (s *countingSink) Emit(Event) { s.count++ }

func TestEventSinksSkipsNil(t *testing.T) {
	sink := &countingSink{}
	EventSinks{nil, sink}.Emit(Event{Type: EventRoomCreate})
	if sink.count != 1 {
		t.Fatalf("sink count = %d", sink.count)
	}
}
