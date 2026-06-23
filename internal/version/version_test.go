package version

import "testing"

func TestGet_EmbeddedDefault(t *testing.T) {
	t.Setenv("PHIRA_MP_VERSION", "") // 确保不受外部环境影响
	if got := Get(); got != "0.0.1" {
		t.Errorf("Get() = %q, want embedded 0.0.1", got)
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
