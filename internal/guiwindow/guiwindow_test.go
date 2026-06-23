package guiwindow

import (
	"runtime"
	"strings"
	"testing"
)

func TestAppModeArgs(t *testing.T) {
	args := appModeArgs("http://127.0.0.1:8080/gui#token=abc")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--app=http://127.0.0.1:8080/gui#token=abc", "--user-data-dir=", "--window-size=1380,860", "--no-first-run"} {
		if !strings.Contains(joined, want) {
			t.Errorf("app-mode args missing %q: %v", want, args)
		}
	}
}

func TestTryLaunch_NonexistentBinary(t *testing.T) {
	// 不存在的可执行文件应返回 false（而非 panic 或挂起）。
	if tryLaunch("phira-mp-no-such-binary-xyz-12345") {
		t.Error("launching a nonexistent binary should return false")
	}
}

func TestEnvOrFallback(t *testing.T) {
	if got := envOr("PHIRA_MP_DEFINITELY_UNSET_VAR", "fallback"); got != "fallback" {
		t.Errorf("envOr fallback = %q, want fallback", got)
	}
	t.Setenv("PHIRA_MP_SET_VAR", "value")
	if got := envOr("PHIRA_MP_SET_VAR", "fallback"); got != "value" {
		t.Errorf("envOr set = %q, want value", got)
	}
}

func TestWindowsCandidates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only path probing")
	}
	cands := windowsCandidates()
	if len(cands) == 0 {
		t.Fatal("expected some Windows browser candidates")
	}
	joined := strings.Join(cands, ";")
	if !strings.Contains(joined, "msedge.exe") || !strings.Contains(joined, "chrome.exe") {
		t.Errorf("candidates should include Edge and Chrome: %v", cands)
	}
}
