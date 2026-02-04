package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newIsolatedPaths(t *testing.T) (projectRoot, projectPath, localPath string) {
	t.Helper()

	projectRoot = filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.MkdirAll(projectRoot, 0o755))
	projectPath = getProjectSettingsPath(projectRoot)
	localPath = getLocalSettingsPath(projectRoot)
	return
}

func writeSettingsFile(t *testing.T, path string, cfg Settings) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func loadSettings(t *testing.T, projectRoot string, runtimeOverrides *Settings) *Settings {
	t.Helper()
	loader := SettingsLoader{ProjectRoot: projectRoot, RuntimeOverrides: runtimeOverrides}
	settings, err := loader.Load()
	require.NoError(t, err)
	return settings
}

func TestSettingsLoader_SingleLayer(t *testing.T) {
	testCases := []struct {
		name        string
		targetLayer func(project, local string) string
	}{
		{name: "project only", targetLayer: func(project, _ string) string { return project }},
		{name: "local only", targetLayer: func(_, local string) string { return local }},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			projectRoot, projectPath, localPath := newIsolatedPaths(t)

			cfg := Settings{
				Model:             "claude-3-opus",
				CleanupPeriodDays: intPtr(10),
				Env:               map[string]string{"K": "V"},
				Permissions: &PermissionsConfig{
					Allow:       []string{"Bash(ls:*)"},
					DefaultMode: "acceptEdits",
				},
				Sandbox: &SandboxConfig{
					Enabled:                  boolPtr(true),
					AutoAllowBashIfSandboxed: boolPtr(false),
					Network:                  &SandboxNetworkConfig{AllowUnixSockets: []string{"/tmp/test.sock"}},
				},
			}

			writeSettingsFile(t, tc.targetLayer(projectPath, localPath), cfg)

			loader := SettingsLoader{ProjectRoot: projectRoot}
			got, err := loader.Load()
			require.NoError(t, err)

			require.Equal(t, "claude-3-opus", got.Model)
			require.NotNil(t, got.CleanupPeriodDays)
			require.Equal(t, 10, *got.CleanupPeriodDays)
			require.Equal(t, map[string]string{"K": "V"}, got.Env)
			require.Equal(t, []string{"Bash(ls:*)"}, got.Permissions.Allow)
			require.Equal(t, "acceptEdits", got.Permissions.DefaultMode)
			require.Equal(t, []string{"/tmp/test.sock"}, got.Sandbox.Network.AllowUnixSockets)
			require.True(t, *got.IncludeCoAuthoredBy)               // default preserved
			require.False(t, *got.Sandbox.AutoAllowBashIfSandboxed) // overridden bool respected
		})
	}
}

func TestSettingsLoader_MultiLayerMerge(t *testing.T) {
	projectCfg := Settings{
		Model:             "project-model",
		CleanupPeriodDays: intPtr(20),
		Env:               map[string]string{"A": "2", "B": "p"},
		Permissions: &PermissionsConfig{
			Allow:       []string{"Bash(home:*)", "Bash(proj:*)"},
			DefaultMode: "acceptEdits",
		},
		Sandbox: &SandboxConfig{
			Enabled:          boolPtr(true),
			ExcludedCommands: []string{"sudo"},
			Network: &SandboxNetworkConfig{
				AllowUnixSockets: []string{"/var/run/docker.sock"},
			},
		},
	}
	localCfg := Settings{
		Model: "local-model",
		Env:   map[string]string{"B": "local", "C": "3"},
		Permissions: &PermissionsConfig{
			Deny:        []string{"Delete(*)"},
			DefaultMode: "acceptEdits",
		},
		Sandbox: &SandboxConfig{
			AutoAllowBashIfSandboxed: boolPtr(false),
		},
	}
	runtimeCfg := &Settings{
		Model: "runtime-model",
		Env:   map[string]string{"C": "runtime"},
		Sandbox: &SandboxConfig{
			Enabled: boolPtr(false),
		},
	}
	t.Run("project plus local", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, localPath := newIsolatedPaths(t)
		writeSettingsFile(t, projectPath, projectCfg)
		writeSettingsFile(t, localPath, localCfg)

		got := loadSettings(t, projectRoot, nil)

		require.Equal(t, "local-model", got.Model)
		require.Equal(t, map[string]string{"A": "2", "B": "local", "C": "3"}, got.Env)
		require.Equal(t, []string{"Bash(home:*)", "Bash(proj:*)"}, got.Permissions.Allow)
		require.Equal(t, []string{"Delete(*)"}, got.Permissions.Deny)
		require.False(t, *got.Sandbox.AutoAllowBashIfSandboxed)
	})

	t.Run("project plus runtime overrides", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, _ := newIsolatedPaths(t)
		writeSettingsFile(t, projectPath, projectCfg)

		got := loadSettings(t, projectRoot, runtimeCfg)

		require.Equal(t, "runtime-model", got.Model)
		require.Equal(t, map[string]string{"A": "2", "B": "p", "C": "runtime"}, got.Env)
		require.True(t, got.Sandbox != nil && !*got.Sandbox.Enabled)
	})

}

func TestSettingsLoader_Precedence(t *testing.T) {
	t.Run("local overrides project", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, localPath := newIsolatedPaths(t)
		writeSettingsFile(t, projectPath, Settings{
			Model: "project",
			Env:   map[string]string{"PATH": "project"},
		})
		writeSettingsFile(t, localPath, Settings{
			Env: map[string]string{"PATH": "local"},
		})

		got := loadSettings(t, projectRoot, nil)
		require.Equal(t, "project", got.Model) // unchanged
		require.Equal(t, "local", got.Env["PATH"])
	})

	t.Run("project overrides defaults", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, _ := newIsolatedPaths(t)
		writeSettingsFile(t, projectPath, Settings{Model: "project"})

		got := loadSettings(t, projectRoot, nil)
		require.Equal(t, "project", got.Model)
	})

	t.Run("runtime overrides all", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, localPath := newIsolatedPaths(t)
		writeSettingsFile(t, projectPath, Settings{Model: "project"})
		writeSettingsFile(t, localPath, Settings{Model: "local"})

		got := loadSettings(t, projectRoot, &Settings{Model: "runtime"})
		require.Equal(t, "runtime", got.Model)
	})
}

func TestSettingsLoader_FieldMerging(t *testing.T) {
	t.Parallel()
	projectRoot, projectPath, localPath := newIsolatedPaths(t)

	project := Settings{
		Model: "project",
		Permissions: &PermissionsConfig{
			Allow:                        []string{"Write(logs)", "Exec(*)"},
			Deny:                         []string{"Overwrite(root)"},
			AdditionalDirectories:        []string{"/data/project"},
			DisableBypassPermissionsMode: "disable",
		},
		Env: map[string]string{"A": "2", "B": "p"},
		Sandbox: &SandboxConfig{
			Enabled:          boolPtr(true),
			ExcludedCommands: []string{"sudo"},
			Network: &SandboxNetworkConfig{
				AllowUnixSockets: []string{"/run/docker.sock"},
				HTTPProxyPort:    intPtr(8080),
			},
		},
	}
	local := Settings{
		Model: "local",
		Permissions: &PermissionsConfig{
			Allow:                 []string{"Debug(*)"},
			Deny:                  []string{"Shutdown(*)"},
			AdditionalDirectories: []string{"/data/local"},
			DefaultMode:           "acceptEdits",
		},
		Env: map[string]string{"B": "local"},
		Sandbox: &SandboxConfig{
			AutoAllowBashIfSandboxed: boolPtr(false),
			ExcludedCommands:         []string{"killall"},
			Network: &SandboxNetworkConfig{
				AllowUnixSockets: []string{"/run/local.sock"},
				SocksProxyPort:   intPtr(1080),
			},
		},
	}

	writeSettingsFile(t, projectPath, project)
	writeSettingsFile(t, localPath, local)

	got := loadSettings(t, projectRoot, nil)

	require.Equal(t, []string{"Write(logs)", "Exec(*)", "Debug(*)"}, got.Permissions.Allow)
	require.Equal(t, []string{"Overwrite(root)", "Shutdown(*)"}, got.Permissions.Deny)
	require.Equal(t, []string{"/data/project", "/data/local"}, got.Permissions.AdditionalDirectories)
	require.Equal(t, "acceptEdits", got.Permissions.DefaultMode)
	require.Equal(t, "disable", got.Permissions.DisableBypassPermissionsMode)
	require.Equal(t, map[string]string{"A": "2", "B": "local"}, got.Env)
	require.False(t, *got.Sandbox.AutoAllowBashIfSandboxed)
	require.True(t, *got.Sandbox.Enabled)
	require.Equal(t, []string{"sudo", "killall"}, got.Sandbox.ExcludedCommands)
	require.Equal(t, []string{"/run/docker.sock", "/run/local.sock"}, got.Sandbox.Network.AllowUnixSockets)
	require.Equal(t, 8080, *got.Sandbox.Network.HTTPProxyPort)
	require.Equal(t, 1080, *got.Sandbox.Network.SocksProxyPort)
}

func TestSettingsLoader_ToolOutputConfigMerge(t *testing.T) {
	t.Parallel()
	projectRoot, projectPath, localPath := newIsolatedPaths(t)

	writeSettingsFile(t, projectPath, Settings{
		Model: "project",
		ToolOutput: &ToolOutputConfig{
			DefaultThresholdBytes: 100,
			PerToolThresholdBytes: map[string]int{
				"bash": 10,
				"grep": 20,
			},
		},
	})
	writeSettingsFile(t, localPath, Settings{
		ToolOutput: &ToolOutputConfig{
			PerToolThresholdBytes: map[string]int{
				"grep":      30,
				"file_read": 40,
			},
		},
	})

	got := loadSettings(t, projectRoot, nil)
	require.NotNil(t, got.ToolOutput)
	require.Equal(t, 100, got.ToolOutput.DefaultThresholdBytes)
	require.Equal(t, map[string]int{
		"bash":      10,
		"grep":      30,
		"file_read": 40,
	}, got.ToolOutput.PerToolThresholdBytes)
}

func TestSettingsLoader_MissingFiles(t *testing.T) {
	t.Run("all layers missing returns defaults", func(t *testing.T) {
		t.Parallel()
		projectRoot, _, _ := newIsolatedPaths(t)

		got := loadSettings(t, projectRoot, nil)
		require.NotNil(t, got.CleanupPeriodDays)
		require.Equal(t, 30, *got.CleanupPeriodDays)
		require.True(t, *got.IncludeCoAuthoredBy)
		require.Equal(t, "askBeforeRunningTools", got.Permissions.DefaultMode)
		require.False(t, *got.Sandbox.Enabled)
		require.True(t, *got.Sandbox.AutoAllowBashIfSandboxed)
	})

	t.Run("partial layers merge correctly", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, _ := newIsolatedPaths(t)
		writeSettingsFile(t, projectPath, Settings{Model: "project", Env: map[string]string{"K": "1"}})

		got := loadSettings(t, projectRoot, nil)
		require.Equal(t, "project", got.Model)
		require.Equal(t, map[string]string{"K": "1"}, got.Env)
		require.Equal(t, "askBeforeRunningTools", got.Permissions.DefaultMode)
	})
}

func TestSettingsLoader_InvalidJSON(t *testing.T) {
	t.Run("invalid json format", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, _ := newIsolatedPaths(t)
		require.NoError(t, os.MkdirAll(filepath.Dir(projectPath), 0o755))
		require.NoError(t, os.WriteFile(projectPath, []byte(`{"model":`), 0o600))

		loader := SettingsLoader{ProjectRoot: projectRoot}
		_, err := loader.Load()
		require.Error(t, err)
		require.ErrorContains(t, err, "decode")
	})

	t.Run("missing required fields reported by validator", func(t *testing.T) {
		t.Parallel()
		projectRoot, projectPath, _ := newIsolatedPaths(t)
		writeSettingsFile(t, projectPath, Settings{
			Permissions: &PermissionsConfig{DefaultMode: " "}, // overrides default with blank
		})

		settings := loadSettings(t, projectRoot, nil)
		err := settings.Validate()
		require.Error(t, err)
		require.ErrorContains(t, err, "model is required")
		require.ErrorContains(t, err, "permissions.defaultMode is required")
	})

	t.Run("type mismatch", func(t *testing.T) {
		t.Parallel()
		projectRoot, _, localPath := newIsolatedPaths(t)
		require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0o755))
		require.NoError(t, os.WriteFile(localPath, []byte(`{"permissions": "oops"}`), 0o600))

		loader := SettingsLoader{ProjectRoot: projectRoot}
		_, err := loader.Load()
		require.Error(t, err)
		require.ErrorContains(t, err, "json")
	})
}

func TestSettingsLoaderMissingProjectRoot(t *testing.T) {
	loader := SettingsLoader{}
	_, err := loader.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "project root")
}

func TestLoadJSONFileMissingReturnsNil(t *testing.T) {
	settings, err := loadJSONFile(filepath.Join(t.TempDir(), "missing.json"), nil)
	require.NoError(t, err)
	require.Nil(t, settings)
}
