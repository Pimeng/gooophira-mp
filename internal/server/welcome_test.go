package server

import (
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

func (h *testHarness) mkRoom(id string, count, maxU int, locked bool, st InternalRoomState) {
	r := NewRoom(protocol.RoomID(id), 1, maxU, false)
	r.Locked = locked
	r.State = st
	for i := 2; i <= count; i++ {
		r.users = append(r.users, i) // 同包可直接访问，补足玩家数
	}
	h.state.Rooms[protocol.RoomID(id)] = r
}

func TestAvailableRoomsText_Filtering(t *testing.T) {
	h := newHarness()
	h.mkRoom("r1", 1, 8, false, StateSelectChart{})
	h.mkRoom("_priv", 1, 8, false, StateSelectChart{})                               // 私有
	h.mkRoom("locked", 1, 8, true, StateSelectChart{})                               // 锁定
	h.mkRoom("full", 8, 8, false, StateSelectChart{})                                // 满员
	h.mkRoom("waiting", 1, 8, false, StateWaitForReady{Started: map[int]struct{}{}}) // 等待就绪
	h.mkRoom("zplaying", 2, 8, false, StatePlaying{})                                // 游戏中可列

	text := h.state.availableRoomsText(l10n.NewLanguage("en-US"))
	if !strings.Contains(text, "r1 (1/8)") {
		t.Errorf("should list r1: %q", text)
	}
	if !strings.Contains(text, "zplaying (2/8)") {
		t.Errorf("should list playing room: %q", text)
	}
	for _, bad := range []string{"_priv", "locked", "full", "waiting"} {
		if strings.Contains(text, bad) {
			t.Errorf("should NOT list %q room: %q", bad, text)
		}
	}
	// 升序：r1 在 zplaying 前。
	if strings.Index(text, "r1") > strings.Index(text, "zplaying") {
		t.Errorf("rooms should be sorted by id: %q", text)
	}
}

func TestAvailableRoomsText_Empty(t *testing.T) {
	h := newHarness()
	if got := h.state.availableRoomsText(l10n.NewLanguage("en-US")); got != "No available rooms" {
		t.Errorf("empty room list = %q", got)
	}
}

func TestBuildWelcomeText(t *testing.T) {
	h := newHarness()
	h.state.Version = "dev" // 固定版本便于断言（实际版本来自 internal/version）
	h.mkRoom("lobby", 1, 8, false, StateSelectChart{})
	user := NewUser(1, "alice", "en-US", h.state)

	text := h.state.BuildWelcomeText(user, &Hitokoto{Quote: "be water", From: "Bruce Lee"})

	checks := []string{
		`Hello "alice"! Welcome to test!`, // chat-welcome
		"Server is running version dev",   // chat-welcome-version
		"Available rooms:",                // chat-roomlist-title
		"lobby (1/8)",                     // room list item
		"be water — Bruce Lee",            // chat-hitokoto
	}
	for _, want := range checks {
		if !strings.Contains(text, want) {
			t.Errorf("welcome text missing %q\n--- got ---\n%s", want, text)
		}
	}
	if !strings.HasPrefix(text, strings.Repeat("\n", 40)) {
		t.Error("welcome should start with 40 newlines (screen clear)")
	}
}

func TestBuildWelcomeText_NoHitokoto(t *testing.T) {
	h := newHarness()
	user := NewUser(1, "alice", "en-US", h.state)
	text := h.state.BuildWelcomeText(user, nil)
	if strings.Contains(text, "—") {
		t.Error("no hitokoto → no em-dash line")
	}
	if !strings.Contains(text, "No available rooms") {
		t.Error("should show empty room list")
	}
}
