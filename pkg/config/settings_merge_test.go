package config

import "testing"

func TestMergeSettings(t *testing.T) {
	t.Parallel()

	lower := &Settings{
		APIKeyHelper: "low",
		Env:          map[string]string{"A": "1"},
		Permissions:  &PermissionsConfig{Allow: []string{"A"}},
		Sandbox:      &SandboxConfig{Enabled: boolPtr(true)},
		BashOutput:   &BashOutputConfig{SyncThresholdBytes: intPtr(1)},
	}
	higher := &Settings{
		APIKeyHelper: "high",
		Env:          map[string]string{"B": "2"},
		Permissions:  &PermissionsConfig{Allow: []string{"B"}, DefaultMode: "ask"},
		Sandbox:      &SandboxConfig{Enabled: boolPtr(false)},
		BashOutput:   &BashOutputConfig{AsyncThresholdBytes: intPtr(2)},
	}

	merged := MergeSettings(lower, higher)
	if merged.APIKeyHelper != "high" {
		t.Fatalf("unexpected api key helper %q", merged.APIKeyHelper)
	}
	if merged.Env["A"] != "1" || merged.Env["B"] != "2" {
		t.Fatalf("unexpected env %v", merged.Env)
	}
	if merged.Permissions == nil || merged.Permissions.DefaultMode != "ask" {
		t.Fatalf("unexpected permissions %v", merged.Permissions)
	}
	if merged.Sandbox == nil || merged.Sandbox.Enabled == nil || *merged.Sandbox.Enabled {
		t.Fatalf("unexpected sandbox enabled %v", merged.Sandbox)
	}
	if merged.BashOutput == nil || merged.BashOutput.SyncThresholdBytes == nil || merged.BashOutput.AsyncThresholdBytes == nil {
		t.Fatalf("expected bash output merged")
	}
}

func TestMergeSettingsNilInputs(t *testing.T) {
	t.Parallel()

	if MergeSettings(nil, nil) != nil {
		t.Fatalf("expected nil merge")
	}
	if MergeSettings(&Settings{APIKeyHelper: "x"}, nil).APIKeyHelper != "x" {
		t.Fatalf("expected lower preserved")
	}
}
