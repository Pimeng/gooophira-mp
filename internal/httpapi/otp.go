package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// OTP / CLI 提权路由（对应 TS routes/otpRoutes.ts）：在未配置静态 admin_token 时，
// 通过终端验证码（otp）或控制台审批（cli）签发 IP 绑定的临时管理员 token。
//
//   - POST /admin/otp/request  申请提权：otp 模式终端打印验证码；cli 模式控制台等待 approve/deny
//   - POST /admin/otp/verify   验证：otp 模式校验码并签发临时 token；cli 模式轮询审批结果取 token

const otpMaxAttempts = 3
const otpBanDuration = 10 * 60 * 1000 // 失败封禁 10 分钟（ms）

type otpSession struct {
	otp       string
	expiresAt int64
}

// routeOTP 分发 OTP 提权路由。配置了 admin_token 时整体禁用（403）。
func (s *Service) routeOTP(w http.ResponseWriter, r *http.Request, lang *l10n.Language, ip string) {
	if strings.TrimSpace(strOrEmpty(s.state.Config.AdminToken)) != "" {
		s.adminErr(w, lang, http.StatusForbidden, "otp-disabled-when-token-configured", "otp-disabled-when-token-configured")
		return
	}
	switch r.URL.Path {
	case "/admin/otp/request":
		s.handleOTPRequest(w, r, lang, ip)
	case "/admin/otp/verify":
		s.handleOTPVerify(w, r, lang, ip)
	}
}

func (s *Service) handleOTPRequest(w http.ResponseWriter, r *http.Request, _ *l10n.Language, ip string) {
	mode := "otp"
	if body, ok := decodeJSONObject(r); ok {
		if m := strings.ToLower(strings.TrimSpace(jsonString(body["mode"]))); m == "cli" || m == "otp" {
			mode = m
		}
	}
	ssid := protocol.NewUUID()
	expiresAt := time.Now().UnixMilli() + server.OTPTTLMS

	if mode == "cli" {
		s.state.Mu.Lock()
		s.state.CLIApprovalSessions[ssid] = &server.CLIApprovalSession{
			IP: ip, ExpiresAt: expiresAt, Status: server.CLIApprovalPending, RequestedAt: time.Now().UnixMilli(),
		}
		s.state.Mu.Unlock()
		short := shortID(ssid)
		if s.state.Logger != nil {
			s.state.Logger.Info(fmt.Sprintf("[OTP CLI Request] 收到管理员提权申请，请求IP: %s，会话ID: %s（短码: %s），1分钟内有效。使用 'approve %s' 批准或 'deny %s' 拒绝", ip, ssid, short, short, short))
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ssid": ssid, "expiresIn": server.OTPTTLMS, "mode": "cli"})
		return
	}

	otp := shortID(protocol.NewUUID()) // 8 位验证码
	s.otpMu.Lock()
	s.pruneOTPLocked()
	s.otpSessions[ssid] = otpSession{otp: otp, expiresAt: expiresAt}
	s.otpMu.Unlock()
	// 验证码仅打印到终端，不经 logger 写入日志文件。
	otpStdout(fmt.Sprintf("[OTP Request] 您正在请求验证码登录管理员后台，验证码: %s，会话ID: %s，1分钟内有效", otp, ssid))
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ssid": ssid, "expiresIn": server.OTPTTLMS, "mode": "otp"})
}

func (s *Service) handleOTPVerify(w http.ResponseWriter, r *http.Request, lang *l10n.Language, ip string) {
	body, _ := decodeJSONObject(r)
	ssid := strings.TrimSpace(jsonString(body["ssid"]))
	mode := "otp"
	if m := strings.ToLower(strings.TrimSpace(jsonString(body["mode"]))); m == "cli" || m == "otp" {
		mode = m
	}
	if ssid == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}

	if mode == "cli" {
		s.verifyCLIApproval(w, lang, ssid, ip)
		return
	}

	otp := strings.TrimSpace(jsonString(body["otp"]))
	if otp == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	s.verifyOTP(w, lang, ssid, otp, ip)
}

// verifyCLIApproval 轮询 CLI 审批会话的状态（pending/denied/approved）。
func (s *Service) verifyCLIApproval(w http.ResponseWriter, lang *l10n.Language, ssid, ip string) {
	now := time.Now().UnixMilli()
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	sess := s.state.CLIApprovalSessions[ssid]
	if sess == nil || now > sess.ExpiresAt {
		delete(s.state.CLIApprovalSessions, ssid)
		s.adminErr(w, lang, http.StatusUnauthorized, "invalid-or-expired-session", "invalid-or-expired-session")
		return
	}
	if sess.IP != ip {
		s.adminErr(w, lang, http.StatusForbidden, "ip-mismatch", "ip-mismatch")
		return
	}
	switch sess.Status {
	case server.CLIApprovalPending:
		s.writeJSON(w, http.StatusAccepted, map[string]any{"ok": false, "error": "pending-approval", "status": "pending", "message": l10n.TL(lang, "pending-approval", nil)})
	case server.CLIApprovalDenied:
		delete(s.state.CLIApprovalSessions, ssid)
		s.writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "approval-denied", "status": "denied", "message": l10n.TL(lang, "approval-denied", nil)})
	default: // approved
		if sess.Token == "" || sess.TokenExpiresAt == 0 {
			delete(s.state.CLIApprovalSessions, ssid)
			s.adminErr(w, lang, http.StatusInternalServerError, "token-not-issued", "token-not-issued")
			return
		}
		token, exp := sess.Token, sess.TokenExpiresAt
		delete(s.state.CLIApprovalSessions, ssid) // 一次性，取出即清
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": token, "expiresAt": exp, "expiresIn": max64(0, exp-now), "mode": "cli"})
	}
}

// verifyOTP 校验终端验证码；失败累计达上限封禁 IP/会话；成功签发临时 token。
func (s *Service) verifyOTP(w http.ResponseWriter, lang *l10n.Language, ssid, otp, ip string) {
	now := time.Now().UnixMilli()
	s.otpMu.Lock()
	s.pruneOTPLocked()
	if until, ok := s.otpBanIP[ip]; ok && now < until {
		s.otpMu.Unlock()
		s.adminErr(w, lang, http.StatusForbidden, "ip-banned-too-many-attempts", "ip-banned-too-many-attempts")
		return
	}
	if until, ok := s.otpBanSSID[ssid]; ok && now < until {
		s.otpMu.Unlock()
		s.adminErr(w, lang, http.StatusForbidden, "ssid-banned-too-many-attempts", "ssid-banned-too-many-attempts")
		return
	}
	sess, ok := s.otpSessions[ssid]
	if !ok || now > sess.expiresAt {
		s.otpMu.Unlock()
		s.adminErr(w, lang, http.StatusUnauthorized, "invalid-or-expired-otp", "invalid-or-expired-otp")
		return
	}
	if sess.otp != otp {
		s.otpFailIP[ip]++
		s.otpFailSSID[ssid]++
		if s.otpFailIP[ip] >= otpMaxAttempts {
			s.otpBanIP[ip] = now + otpBanDuration
			s.warnOTP(fmt.Sprintf("[OTP] IP %s 因验证失败次数过多已被封禁", ip))
		}
		if s.otpFailSSID[ssid] >= otpMaxAttempts {
			s.otpBanSSID[ssid] = now + otpBanDuration
			delete(s.otpSessions, ssid)
			s.warnOTP(fmt.Sprintf("[OTP] 会话 %s 因验证失败次数过多已被封禁", ssid))
		}
		s.otpMu.Unlock()
		s.adminErr(w, lang, http.StatusUnauthorized, "invalid-or-expired-otp", "invalid-or-expired-otp")
		return
	}
	// 成功：清理尝试记录与会话。
	delete(s.otpFailIP, ip)
	delete(s.otpFailSSID, ssid)
	delete(s.otpSessions, ssid)
	s.otpMu.Unlock()

	token := protocol.NewUUID()
	exp := now + server.TempTokenTTLMS
	s.state.Mu.Lock()
	s.state.TempAdminTokens[token] = &server.TempAdminToken{IP: ip, ExpiresAt: exp}
	s.state.Mu.Unlock()
	otpStdout(fmt.Sprintf("[OTP] 临时管理员TOKEN已生成，IP: %s，Token: %s...，4小时内有效", ip, shortID(token)))
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": token, "expiresAt": exp, "expiresIn": server.TempTokenTTLMS})
}

// pruneOTPLocked 清理过期的 OTP 会话与封禁（调用方持 otpMu）。
func (s *Service) pruneOTPLocked() {
	now := time.Now().UnixMilli()
	for k, v := range s.otpSessions {
		if now > v.expiresAt {
			delete(s.otpSessions, k)
		}
	}
	for k, until := range s.otpBanIP {
		if now >= until {
			delete(s.otpBanIP, k)
			delete(s.otpFailIP, k)
		}
	}
	for k, until := range s.otpBanSSID {
		if now >= until {
			delete(s.otpBanSSID, k)
			delete(s.otpFailSSID, k)
		}
	}
}

func (s *Service) warnOTP(msg string) {
	if s.state.Logger != nil {
		s.state.Logger.Warn(msg)
	}
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// otpStdout 把含敏感信息（验证码/token）的行直接打印到终端，不经 logger（避免写入日志文件）。
func otpStdout(msg string) {
	fmt.Fprintf(os.Stdout, "[%s] [INFO] %s\n", time.Now().Format(time.RFC3339), msg)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
