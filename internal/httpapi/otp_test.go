package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// otpCode 读取某 ssid 当前的验证码（测试内省）。
func (s *Service) otpCode(ssid string) string {
	s.otpMu.Lock()
	defer s.otpMu.Unlock()
	return s.otpSessions[ssid].otp
}

func TestOTP_RequestVerifyIssuesToken(t *testing.T) {
	svc, state := newTestService(t, nil) // 无 admin_token → OTP 可用

	w := doReq(svc, http.MethodPost, "/admin/otp/request", `{"mode":"otp"}`)
	if w.Code != 200 {
		t.Fatalf("request status = %d body=%s", w.Code, w.Body.String())
	}
	var req struct {
		OK   bool   `json:"ok"`
		SSID string `json:"ssid"`
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &req)
	if !req.OK || req.SSID == "" || req.Mode != "otp" {
		t.Fatalf("bad request response: %s", w.Body.String())
	}

	code := svc.otpCode(req.SSID)
	if code == "" {
		t.Fatal("otp code not stored")
	}

	// 错误码 → 401。
	if w := doReq(svc, http.MethodPost, "/admin/otp/verify", `{"ssid":"`+req.SSID+`","otp":"wrong"}`); w.Code != 401 {
		t.Errorf("wrong otp should be 401, got %d", w.Code)
	}
	// 正确码 → 200 + token，并写入 TempAdminTokens。
	w = doReq(svc, http.MethodPost, "/admin/otp/verify", `{"ssid":"`+req.SSID+`","otp":"`+code+`"}`)
	if w.Code != 200 {
		t.Fatalf("correct otp verify = %d body=%s", w.Code, w.Body.String())
	}
	var ver struct {
		OK    bool   `json:"ok"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &ver)
	if !ver.OK || ver.Token == "" {
		t.Fatalf("verify should issue a token: %s", w.Body.String())
	}
	state.Mu.Lock()
	_, exists := state.TempAdminTokens[ver.Token]
	state.Mu.Unlock()
	if !exists {
		t.Error("issued token should be registered in TempAdminTokens")
	}
}

func TestOTP_BanAfterMaxAttempts(t *testing.T) {
	svc, _ := newTestService(t, nil)
	w := doReq(svc, http.MethodPost, "/admin/otp/request", `{"mode":"otp"}`)
	var req struct {
		SSID string `json:"ssid"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &req)

	for range otpMaxAttempts { // 3 次错误
		doReq(svc, http.MethodPost, "/admin/otp/verify", `{"ssid":"`+req.SSID+`","otp":"bad"}`)
	}
	// 此后即便提供（曾经的）正确流程也因封禁被拒。
	w = doReq(svc, http.MethodPost, "/admin/otp/verify", `{"ssid":"`+req.SSID+`","otp":"bad"}`)
	if w.Code != 403 {
		t.Errorf("after max attempts should be 403 (banned), got %d body=%s", w.Code, w.Body.String())
	}
}

func TestOTP_CLIModePolling(t *testing.T) {
	svc, state := newTestService(t, nil)

	w := doReq(svc, http.MethodPost, "/admin/otp/request", `{"mode":"cli"}`)
	var req struct {
		SSID string `json:"ssid"`
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &req)
	if req.Mode != "cli" || req.SSID == "" {
		t.Fatalf("cli request response: %s", w.Body.String())
	}

	// 未批准 → 202 pending。
	if w := doReq(svc, http.MethodPost, "/admin/otp/verify", `{"ssid":"`+req.SSID+`","mode":"cli"}`); w.Code != http.StatusAccepted {
		t.Errorf("pending poll should be 202, got %d", w.Code)
	}

	// 模拟控制台批准：状态置为 approved + 签发 token。
	state.Mu.Lock()
	sess := state.CLIApprovalSessions[req.SSID]
	sess.Status = server.CLIApprovalApproved
	sess.Token = "tok-123"
	sess.TokenExpiresAt = sess.ExpiresAt + 1_000_000
	state.Mu.Unlock()

	w = doReq(svc, http.MethodPost, "/admin/otp/verify", `{"ssid":"`+req.SSID+`","mode":"cli"}`)
	if w.Code != 200 {
		t.Fatalf("approved poll should be 200, got %d body=%s", w.Code, w.Body.String())
	}
	var ver struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &ver)
	if ver.Token != "tok-123" {
		t.Errorf("approved poll should return the issued token, got %q", ver.Token)
	}
	// 一次性：取出后会话应被清除。
	state.Mu.Lock()
	_, still := state.CLIApprovalSessions[req.SSID]
	state.Mu.Unlock()
	if still {
		t.Error("approved session should be consumed after retrieval")
	}
}

func TestOTP_DisabledWhenAdminTokenConfigured(t *testing.T) {
	svc, _ := newTestService(t, &config.ServerConfig{AdminToken: sp("secret")})
	if w := doReq(svc, http.MethodPost, "/admin/otp/request", `{"mode":"otp"}`); w.Code != http.StatusForbidden {
		t.Errorf("OTP should be disabled (403) when admin_token set, got %d", w.Code)
	}
}
