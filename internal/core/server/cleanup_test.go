package server

import (
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func TestRunCleanupOnce(t *testing.T) {
	st := NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	now := time.Now()
	nowMS := now.UnixMilli()
	oldMS := nowMS - 8*24*60*60*1000 // 8 天前（超过 7 天保留）

	// 上传元数据：user 1 有一条过期 + 一条新；user 2 全过期。
	st.UploadedReplayMeta[1] = map[int][]UploadedReplayMeta{
		10: {{ScoreID: 1, ChartID: 10, Timestamp: oldMS}, {ScoreID: 2, ChartID: 10, Timestamp: nowMS}},
	}
	st.UploadedReplayMeta[2] = map[int][]UploadedReplayMeta{
		20: {{ScoreID: 3, ChartID: 20, Timestamp: oldMS}},
	}

	// 自动上传配置：user 1 在线、user 99 离线。
	st.Users[1] = NewUser(1, "online", "", st)
	st.AutoUploadConfigs[1] = &AutoUploadConfig{Show: true}
	st.AutoUploadConfigs[99] = &AutoUploadConfig{Show: true}

	// CLI 审批会话：一个过期、一个被拒、一个有效。
	st.CLIApprovalSessions["expired"] = &CLIApprovalSession{ExpiresAt: nowMS - 1000, Status: CLIApprovalPending}
	st.CLIApprovalSessions["denied"] = &CLIApprovalSession{ExpiresAt: nowMS + 1_000_000, Status: CLIApprovalDenied}
	st.CLIApprovalSessions["live"] = &CLIApprovalSession{ExpiresAt: nowMS + 1_000_000, Status: CLIApprovalApproved}

	// 临时 admin token：一个过期、一个封禁、一个有效。
	st.TempAdminTokens["exp"] = &TempAdminToken{ExpiresAt: nowMS - 1000}
	st.TempAdminTokens["ban"] = &TempAdminToken{ExpiresAt: nowMS + 1_000_000, Banned: true}
	st.TempAdminTokens["ok"] = &TempAdminToken{ExpiresAt: nowMS + 1_000_000}

	st.RunCleanupOnce(now)

	// 上传元数据：user 1 仅保留新的一条；user 2 整体移除。
	if metas := st.UploadedReplayMeta[1][10]; len(metas) != 1 || metas[0].ScoreID != 2 {
		t.Errorf("user1 chart10 should keep only the fresh meta, got %v", metas)
	}
	if _, ok := st.UploadedReplayMeta[2]; ok {
		t.Error("user2 (all-expired) should be removed from uploadedReplayMeta")
	}

	// 自动上传配置：在线保留，离线移除。
	if _, ok := st.AutoUploadConfigs[1]; !ok {
		t.Error("online user's auto-upload config should be kept")
	}
	if _, ok := st.AutoUploadConfigs[99]; ok {
		t.Error("offline user's auto-upload config should be removed")
	}

	// CLI 审批：过期/拒绝移除，有效保留。
	if _, ok := st.CLIApprovalSessions["expired"]; ok {
		t.Error("expired approval session should be removed")
	}
	if _, ok := st.CLIApprovalSessions["denied"]; ok {
		t.Error("denied approval session should be removed")
	}
	if _, ok := st.CLIApprovalSessions["live"]; !ok {
		t.Error("live approval session should be kept")
	}

	// 临时 token：过期/封禁移除，有效保留。
	if _, ok := st.TempAdminTokens["exp"]; ok {
		t.Error("expired token should be removed")
	}
	if _, ok := st.TempAdminTokens["ban"]; ok {
		t.Error("banned token should be removed")
	}
	if _, ok := st.TempAdminTokens["ok"]; !ok {
		t.Error("valid token should be kept")
	}
}

func TestStartStopCleanup_Idempotent(t *testing.T) {
	st := NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	st.StartCleanup()
	st.StartCleanup() // 二次启动应为 no-op（不泄漏第二个 goroutine）
	st.StopCleanup()
	st.StopCleanup() // 二次停止不应阻塞或 panic
}
