package main

import "testing"

func TestResolveAgentConfigPath(t *testing.T) {
	tests := []struct {
		name       string
		configPath string
		legacyFlag string
		existing   map[string]bool
		want       string
	}{
		{name: "new default", configPath: "config/agent.yaml", existing: map[string]bool{"config/agent.yaml": true, "agent_config.yml": true}, want: "config/agent.yaml"},
		{name: "legacy fallback", configPath: "config/agent.yaml", existing: map[string]bool{"agent_config.yml": true}, want: "agent_config.yml"},
		{name: "missing default", configPath: "config/agent.yaml", existing: map[string]bool{}, want: "config/agent.yaml"},
		{name: "explicit config", configPath: "other/agent.yaml", existing: map[string]bool{}, want: "other/agent.yaml"},
		{name: "legacy flag", configPath: "config/agent.yaml", legacyFlag: "old.yml", existing: map[string]bool{"config/agent.yaml": true}, want: "old.yml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAgentConfigPathWithStat(tt.configPath, tt.legacyFlag, func(path string) bool { return tt.existing[path] })
			if got != tt.want {
				t.Fatalf("resolved path = %q, want %q", got, tt.want)
			}
		})
	}
}
