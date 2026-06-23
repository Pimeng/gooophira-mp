package phira

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestClient_TokenCacheHit(t *testing.T) {
	tokenCache.Clear()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me" {
			calls.Add(1)
			_, _ = w.Write([]byte(`{"id":42,"name":"alice","language":"en-US"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	for range 3 {
		info, err := c.FetchUserInfo("tok-abc")
		if err != nil || info.ID != 42 || info.Name != "alice" {
			t.Fatalf("FetchUserInfo = %+v, %v", info, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 HTTP /me call (rest token-cached), got %d", got)
	}
}

func TestClient_RecordCacheHit(t *testing.T) {
	recordCache.Clear()
	t.Cleanup(func() { recordCache.Clear(); _ = os.RemoveAll("cache") }) // record 缓存落盘，清理测试产物
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":7,"player":1,"score":1000000}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	for range 3 {
		rec, err := c.FetchRecord(7)
		if err != nil || rec.ID != 7 || rec.Score != 1000000 {
			t.Fatalf("FetchRecord = %+v, %v", rec, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 HTTP /record call (rest record-cached), got %d", got)
	}
}

func TestClient_TokenCacheErrorNotCached(t *testing.T) {
	tokenCache.Clear()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized) // 始终失败
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	// 两次失败都应真正发起 HTTP（失败结果不缓存）。
	for range 2 {
		if _, err := c.FetchUserInfo("bad"); err == nil {
			t.Fatal("expected auth error")
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("failed auth must not be cached, expected 2 calls, got %d", got)
	}
}
