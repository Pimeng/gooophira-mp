package replay

import (
	"path/filepath"
	"testing"
)

func TestReplayIDRoundTripAndTraversalRejection(t *testing.T) {
	id := ID{UserID: 12, ChartID: 34, Timestamp: 56}
	parsed, err := ParseID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("ParseID = %+v, %v", parsed, err)
	}
	path, err := parsed.Path("replays")
	if err != nil || path != filepath.Join("replays", "12", "34", "56.phirarec") {
		t.Fatalf("Path = %q, %v", path, err)
	}
	for _, raw := range []string{"../../secret", "v1:1:../2:3", "v1:-1:2:3", "v1:1:2:0", "v2:1:2:3"} {
		if _, err := ParseID(raw); err == nil {
			t.Errorf("ParseID accepted %q", raw)
		}
	}
}
