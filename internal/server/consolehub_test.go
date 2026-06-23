package server

import "testing"

func TestConsoleHub_GetRecent(t *testing.T) {
	h := NewConsoleHub()
	if len(h.GetRecent(10)) != 0 {
		t.Error("empty hub should return no lines")
	}
	for i := range 5 {
		h.Push("INFO", string(rune('a'+i)))
	}
	all := h.GetRecent(0) // 0 = 全部
	if len(all) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(all))
	}
	if all[0].Message != "a" || all[4].Message != "e" {
		t.Errorf("order should be oldest→newest, got %q..%q", all[0].Message, all[4].Message)
	}
	last2 := h.GetRecent(2)
	if len(last2) != 2 || last2[0].Message != "d" || last2[1].Message != "e" {
		t.Errorf("GetRecent(2) should return the last two, got %v", last2)
	}
}

func TestConsoleHub_Subscribe(t *testing.T) {
	h := NewConsoleHub()
	var got []ConsoleLogLine
	unsub := h.Subscribe(func(l ConsoleLogLine) { got = append(got, l) })

	h.Push("INFO", "one")
	h.Push("WARN", "two")
	if len(got) != 2 || got[0].Message != "one" || got[1].Level != "WARN" {
		t.Fatalf("subscriber should receive both lines in order, got %v", got)
	}
	if got[0].Timestamp == 0 {
		t.Error("pushed line should carry a timestamp")
	}

	unsub()
	h.Push("INFO", "three") // 退订后不应再收到
	if len(got) != 2 {
		t.Errorf("after unsubscribe no more lines should arrive, got %d", len(got))
	}
}

func TestConsoleHub_RingCap(t *testing.T) {
	h := NewConsoleHub()
	for i := range consoleLogCap + 50 {
		h.Push("INFO", string(rune(i)))
	}
	all := h.GetRecent(0)
	if len(all) != consoleLogCap {
		t.Errorf("buffer should cap at %d, got %d", consoleLogCap, len(all))
	}
	// 最旧的 50 条应被丢弃：首条应是第 50 条（rune(50)）。
	if all[0].Message != string(rune(50)) {
		t.Errorf("oldest entries should be evicted")
	}
}
