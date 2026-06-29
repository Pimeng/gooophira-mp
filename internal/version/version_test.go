package version

import (
	"os"
	"strings"
	"testing"
)

func TestGet_EmbeddedDefault(t *testing.T) {
	t.Setenv("PHIRA_MP_VERSION", "") // 确保不受外部环境影响
	// 以 VERSION 文件为唯一事实来源，避免版本号 bump 后测试需要同步改硬编码。
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(raw))
	if got := Get(); got != want {
		t.Errorf("Get() = %q, want embedded %q", got, want)
	}
}

func TestGet_EnvOverride(t *testing.T) {
	t.Setenv("PHIRA_MP_VERSION", "9.9.9-test")
	if got := Get(); got != "9.9.9-test" {
		t.Errorf("env override = %q, want 9.9.9-test", got)
	}
}

func TestGet_InjectedBeatsEmbedded(t *testing.T) {
	t.Setenv("PHIRA_MP_VERSION", "")
	old := injected
	injected = "1.2.3"
	defer func() { injected = old }()
	if got := Get(); got != "1.2.3" {
		t.Errorf("ldflags-injected = %q, want 1.2.3", got)
	}
}
