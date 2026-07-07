package phira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
		info, err := c.FetchUserInfo(context.Background(), "tok-abc")
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
		rec, err := c.FetchRecord(context.Background(), 7)
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
		if _, err := c.FetchUserInfo(context.Background(), "bad"); err == nil {
			t.Fatal("expected auth error")
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("failed auth must not be cached, expected 2 calls, got %d", got)
	}
}

// TestClient_RetryOnServerError 验证 5xx 时按线性退避重试，最终成功。
func TestClient_RetryOnServerError(t *testing.T) {
	tokenCache.Clear()
	// 调短退避时间避免测试等待 3s。
	oldDelay, oldTimeout := retryBaseDelay, fetchGlobalTimeout
	retryBaseDelay = 2 * time.Millisecond
	fetchGlobalTimeout = 500 * time.Millisecond
	t.Cleanup(func() { retryBaseDelay, fetchGlobalTimeout = oldDelay, oldTimeout })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 3 { // 前 3 次 5xx，第 4 次成功
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"name":"alice","language":""}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	info, err := c.FetchUserInfo(context.Background(), "tok")
	if err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if info.ID != 1 || info.Name != "alice" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if got := calls.Load(); got != 4 { // 初始 1 + 重试 3 = 4 次
		t.Errorf("expected 4 HTTP calls (1 initial + 3 retries), got %d", got)
	}
}

// TestClient_RetryExhausted 验证重试耗尽后返回错误。
func TestClient_RetryExhausted(t *testing.T) {
	tokenCache.Clear()
	oldDelay, oldTimeout := retryBaseDelay, fetchGlobalTimeout
	retryBaseDelay = 1 * time.Millisecond
	fetchGlobalTimeout = 500 * time.Millisecond
	t.Cleanup(func() { retryBaseDelay, fetchGlobalTimeout = oldDelay, oldTimeout })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError) // 始终 5xx
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.FetchUserInfo(context.Background(), "tok"); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if got := calls.Load(); got != 4 { // 初始 1 + 重试 3 = 4 次
		t.Errorf("expected 4 HTTP calls (retries exhausted), got %d", got)
	}
}

// TestClient_RetryOnNetworkError 验证网络错误（连接拒绝）时重试。
func TestClient_RetryOnNetworkError(t *testing.T) {
	tokenCache.Clear()
	oldDelay, oldTimeout := retryBaseDelay, fetchGlobalTimeout
	retryBaseDelay = 1 * time.Millisecond
	fetchGlobalTimeout = 500 * time.Millisecond
	t.Cleanup(func() { retryBaseDelay, fetchGlobalTimeout = oldDelay, oldTimeout })

	// 启动后立即关闭服务器，模拟连接拒绝。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.FetchUserInfo(context.Background(), "tok"); err == nil {
		t.Fatal("expected network error after retries")
	}
}

// TestClient_NoRetryOn4xx 验证 4xx 客户端错误不触发重试。
func TestClient_NoRetryOn4xx(t *testing.T) {
	tokenCache.Clear()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound) // 404
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.FetchUserInfo(context.Background(), "tok"); err == nil {
		t.Fatal("expected error for 404")
	}
	if got := calls.Load(); got != 1 { // 4xx 不重试，只调 1 次
		t.Errorf("expected 1 HTTP call (no retry on 4xx), got %d", got)
	}
}

// TestClient_RetryGlobalTimeout 验证全局超时会在退避窗口中提前中断。
func TestClient_RetryGlobalTimeout(t *testing.T) {
	tokenCache.Clear()
	// 全局超时设得很短，确保在退避等待中触发。
	oldDelay, oldTimeout, oldMax := retryBaseDelay, fetchGlobalTimeout, maxRetries
	retryBaseDelay = 200 * time.Millisecond
	fetchGlobalTimeout = 50 * time.Millisecond // 短于第一次退避 200ms
	maxRetries = 10
	t.Cleanup(func() { retryBaseDelay, fetchGlobalTimeout, maxRetries = oldDelay, oldTimeout, oldMax })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable) // 503 触发重试
	}))
	defer srv.Close()

	start := time.Now()
	c := NewClient(srv.URL)
	_, err := c.FetchUserInfo(context.Background(), "tok")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// 全局超时 50ms 应让总耗时远小于完整退避窗口（200ms+），允许少量误差。
	if elapsed > 300*time.Millisecond {
		t.Errorf("global timeout should cut off retries early, elapsed=%v", elapsed)
	}
}

// mockLogger 捕获 DEBUG 日志用于断言。
type mockLogger struct {
	debugs []string
}

func (m *mockLogger) Debug(msg string)   { m.debugs = append(m.debugs, msg) }
func (m *mockLogger) Info(msg string)    {}
func (m *mockLogger) Mark(msg string)    {}
func (m *mockLogger) Warn(msg string)    {}
func (m *mockLogger) Error(msg string)   {}
func (m *mockLogger) DebugEnabled() bool { return true }

// TestClient_DebugLogOnRetry 验证重试过程输出详细 DEBUG 日志。
func TestClient_DebugLogOnRetry(t *testing.T) {
	tokenCache.Clear()
	oldDelay, oldTimeout := retryBaseDelay, fetchGlobalTimeout
	retryBaseDelay = 1 * time.Millisecond
	fetchGlobalTimeout = 500 * time.Millisecond
	t.Cleanup(func() { retryBaseDelay, fetchGlobalTimeout = oldDelay, oldTimeout })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway) // 前 2 次 502
			return
		}
		_, _ = w.Write([]byte(`{"id":42,"name":"bob","language":""}`))
	}))
	defer srv.Close()

	ml := &mockLogger{}
	c := NewClient(srv.URL)
	c.SetLogger(ml)
	info, err := c.FetchUserInfo(context.Background(), "mytoken123")
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if info.ID != 42 {
		t.Fatalf("unexpected info: %+v", info)
	}

	// 验证 DEBUG 日志覆盖关键环节。
	debugs := ml.debugs
	// 1. 开始日志
	if !contains(debugs, "开始") || !contains(debugs, "/me") {
		t.Errorf("missing start log, got: %v", debugs)
	}
	// 2. 认证请求日志（含 token 脱敏前缀）
	if !contains(debugs, "token 前缀") || !contains(debugs, "mytoken") {
		t.Errorf("missing token prefix log, got: %v", debugs)
	}
	// 3. 502 重试日志
	if !contains(debugs, "502") || !contains(debugs, "重试") {
		t.Errorf("missing 502 retry log, got: %v", debugs)
	}
	// 4. 最终成功日志
	if !contains(debugs, "认证成功") || !contains(debugs, "id=42") {
		t.Errorf("missing success log, got: %v", debugs)
	}
	// 5. 完整 token 不应出现在日志中（脱敏）
	if contains(debugs, "mytoken123") {
		t.Errorf("full token leaked in debug logs: %v", debugs)
	}
}

func contains(lines []string, substr string) bool {
	for _, l := range lines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
