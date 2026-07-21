package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/webhookmodel"
	"github.com/Pimeng/gooophira-mp/internal/config"
)

func TestOneBotV11DeliversGroupText(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":1}}`))
	}))
	defer srv.Close()

	target := config.WebhookTarget{
		URL: srv.URL + "/onebot/?source=webhook", AccessToken: "token", MessageType: "group", TargetID: 123456,
	}
	ok, retryable := NewOneBotV11(srv.Client()).Deliver(context.Background(), target, webhookmodel.Event{
		Type: webhookmodel.EventRoomDisband, Server: "Test", RoomID: "ROOM",
	})
	if !ok || retryable {
		t.Fatalf("Deliver()=(%v,%v), want (true,false)", ok, retryable)
	}
	if gotPath != "/onebot/send_group_msg" || gotAuth != "Bearer token" {
		t.Fatalf("unexpected request path=%q auth=%q", gotPath, gotAuth)
	}
	if gotBody["group_id"] != float64(123456) || gotBody["auto_escape"] != true {
		t.Fatalf("unexpected request body: %#v", gotBody)
	}
	if message, _ := gotBody["message"].(string); message == "" {
		t.Fatalf("message must contain rendered event text: %#v", gotBody)
	}
}

func TestOneBotV11DeliversPrivateText(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":2}}`))
	}))
	defer srv.Close()

	ok, retryable := NewOneBotV11(srv.Client()).Deliver(context.Background(), config.WebhookTarget{
		URL: srv.URL, MessageType: "private", TargetID: 654321,
	}, webhookmodel.Event{Type: webhookmodel.EventMaintenance, Enabled: true})
	if !ok || retryable || gotPath != "/send_private_msg" || gotBody["user_id"] != float64(654321) {
		t.Fatalf("unexpected delivery: ok=%v retryable=%v path=%q body=%#v", ok, retryable, gotPath, gotBody)
	}
}

func TestOneBotV11DeliversToTargetIDArray(t *testing.T) {
	var gotIDs []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			GroupID int64 `json:"group_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		gotIDs = append(gotIDs, body.GroupID)
		if body.GroupID == 222 {
			_, _ = w.Write([]byte(`{"status":"failed","retcode":100}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0}`))
	}))
	defer srv.Close()

	ok, retryable := NewOneBotV11(srv.Client()).Deliver(context.Background(), config.WebhookTarget{
		URL: srv.URL, MessageType: "group", TargetIDs: []int64{111, 222, 333},
	}, webhookmodel.Event{Type: webhookmodel.EventGameEnd})
	if ok || retryable {
		t.Fatalf("Deliver()=(%v,%v), want (false,false)", ok, retryable)
	}
	if !slices.Equal(gotIDs, []int64{111, 222, 333}) {
		t.Fatalf("delivered target IDs=%v, want [111 222 333]", gotIDs)
	}
}

func TestOneBotV11ClassifiesFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		retryable bool
	}{
		{name: "business failure", status: http.StatusOK, body: `{"status":"failed","retcode":100}`, retryable: false},
		{name: "unauthorized", status: http.StatusUnauthorized, retryable: false},
		{name: "rate limited", status: http.StatusTooManyRequests, retryable: true},
		{name: "server error", status: http.StatusBadGateway, retryable: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			ok, retryable := NewOneBotV11(srv.Client()).Deliver(context.Background(), config.WebhookTarget{
				URL: srv.URL, MessageType: "group", TargetID: 1,
			}, webhookmodel.Event{Type: webhookmodel.EventGameEnd})
			if ok || retryable != tc.retryable {
				t.Fatalf("Deliver()=(%v,%v), want (false,%v)", ok, retryable, tc.retryable)
			}
		})
	}
}
