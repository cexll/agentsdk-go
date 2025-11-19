package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/plugins"
	"github.com/stretchr/testify/require"
)

type stubValidator struct {
	called bool
}

func (s *stubValidator) Validate(cfg *ProjectConfig) error {
	s.called = true
	return nil
}

func baseValidatedConfig() *ProjectConfig {
	root := filepath.Join(os.TempDir(), "cfg-root")
	claude := filepath.Join(root, ".claude")
	return &ProjectConfig{
		Version:     "1.0.0",
		ClaudeDir:   claude,
		Environment: map[string]string{"VALID_KEY": "ok"},
		Plugins:     []PluginRef{{Name: "p1"}},
		Manifests: []*plugins.Manifest{{
			Name:          "p1",
			Version:       "1.0.0",
			EntrypointAbs: filepath.Join(claude, "plugins", "p1", "main"),
		}},
	}
}

func TestNewDefaultValidatorAndValidateSuccess(t *testing.T) {
	cfg := baseValidatedConfig()
	v := NewDefaultValidator(filepath.Dir(cfg.ClaudeDir))
	require.NoError(t, v.Validate(cfg))
}

func TestNewDefaultValidatorRootCanonical(t *testing.T) {
	v := NewDefaultValidator("")
	require.NotNil(t, v)
	require.NotEqual(t, "", v.root)
}

func TestLoaderWithExplicitClaudeDir(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	claude := filepath.Join(workspace, ".claude")
	require.NoError(t, os.MkdirAll(claude, 0o755))

	ts, signer := makeTrustStore(t)
	writePlugin(t, claude, "explicit", signer)
	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yaml"), []byte(`version: 1.0.0
plugins:
  - name: explicit
`), 0o600))

	loader, err := NewLoader(root, WithTrustStore(ts), WithClaudeDir(claude))
	require.NoError(t, err)
	cfg, err := loader.Load()
	require.NoError(t, err)
	require.Equal(t, claude, cfg.ClaudeDir)
}

func TestLoaderRespectsCustomValidator(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claude, 0o755))

	ts, signer := makeTrustStore(t)
	writePlugin(t, claude, "validator", signer)
	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yaml"), []byte(`version: 1.0.0
plugins:
  - name: validator
`), 0o600))

	stub := &stubValidator{}
	loader, err := NewLoader(root, WithTrustStore(ts), WithValidator(stub))
	require.NoError(t, err)
	_, err = loader.Load()
	require.NoError(t, err)
	require.True(t, stub.called)
}

func TestParseProjectConfigErrors(t *testing.T) {
	_, err := ParseProjectConfig([]byte{})
	require.Error(t, err)
}

func TestNormalizeHandlesNilReceiver(t *testing.T) {
	var cfg *ProjectConfig
	require.NotPanics(t, func() { cfg.Normalize() })
}

func TestDecodeMixedYAMLJSONPaths(t *testing.T) {
	// YAML
	var cfg ProjectConfig
	require.NoError(t, decodeMixedYAMLJSON([]byte("version: 1.0.0"), &cfg))
	// JSON (also valid YAML, but exercises helper)
	require.NoError(t, decodeMixedYAMLJSON([]byte(`{"version":"1.0.0"}`), &cfg))
	// Hard invalid for both YAML and JSON to hit error path.
	require.Error(t, decodeMixedYAMLJSON([]byte("["), &cfg))
}

func TestValidatorEnvironmentRules(t *testing.T) {
	cfg := baseValidatedConfig()
	v := NewDefaultValidator(filepath.Dir(cfg.ClaudeDir))
	require.NoError(t, v.Validate(cfg))

	// invalid key
	cfg.Environment = map[string]string{"bad-key": "v"}
	require.Error(t, v.Validate(cfg))
	// newline in value
	cfg.Environment = map[string]string{"VALID_KEY": "bad\nvalue"}
	require.Error(t, v.Validate(cfg))
	// value too long
	cfg.Environment = map[string]string{"VALID_KEY": strings.Repeat("a", 2000)}
	require.Error(t, v.Validate(cfg))
}

func TestValidateNilConfigAndBadVersion(t *testing.T) {
	v := NewDefaultValidator(os.TempDir())
	require.Error(t, v.Validate(nil))
	cfg := baseValidatedConfig()
	cfg.Version = ""
	require.Error(t, v.Validate(cfg))
}

func TestValidatorSandboxPaths(t *testing.T) {
	cfg := baseValidatedConfig()
	cfg.Sandbox.AllowedPaths = []string{"relative/path"}
	v := NewDefaultValidator(filepath.Dir(cfg.ClaudeDir))
	require.NoError(t, v.Validate(cfg))

	cfg.Sandbox.AllowedPaths = []string{"/etc"}
	require.Error(t, v.Validate(cfg))

	cfg.Sandbox.AllowedPaths = []string{"../etc"}
	require.Error(t, v.Validate(cfg))
}

func TestValidatePluginLimitsAndDuplicates(t *testing.T) {
	claude := filepath.Join(os.TempDir(), ".claude")
	cfg := &ProjectConfig{
		Version:   "1.0.0",
		ClaudeDir: claude,
		Plugins: []PluginRef{
			{Name: "p1"},
			{Name: "p1"},
		},
		Manifests: []*plugins.Manifest{{Name: "p1", Version: "1.0.0", EntrypointAbs: filepath.Join(claude, "plugins", "p1", "main")}},
	}
	v := &DefaultValidator{root: filepath.Dir(claude), maxPlugins: 1, maxEnvVars: 64}
	require.Error(t, v.Validate(cfg))

	cfg.Plugins = []PluginRef{{Name: "p1"}, {Name: "p2"}}
	cfg.Manifests = []*plugins.Manifest{
		{Name: "p1", Version: "1.0.0", EntrypointAbs: filepath.Join(claude, "plugins", "p1", "main")},
		{Name: "p2", Version: "1.0.0", EntrypointAbs: filepath.Join(claude, "plugins", "p2", "main")},
	}
	v.maxPlugins = 1
	require.Error(t, v.Validate(cfg))

	cfg.Environment = map[string]string{"K1": "v1", "K2": "v2"}
	v.maxPlugins = 10
	v.maxEnvVars = 1
	require.Error(t, v.Validate(cfg))
}

func TestOptionalPluginSkipping(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claude, 0o755))
	ts := plugins.NewTrustStore()
	ts.AllowUnsigned(true)

	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yaml"), []byte(`version: 1.0.0
plugins:
  - name: optional
    optional: true
`), 0o600))

	loader, err := NewLoader(root, WithTrustStore(ts))
	require.NoError(t, err)
	cfg, err := loader.Load()
	require.NoError(t, err)
	require.Len(t, cfg.Manifests, 0)
}

func TestLoaderMinVersionEnforcement(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claude, 0o755))
	ts, signer := makeTrustStore(t)
	writePlugin(t, claude, "min", signer)

	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yaml"), []byte(`version: 1.0.0
plugins:
  - name: min
    min_version: 2.0.0
`), 0o600))

	loader, err := NewLoader(root, WithTrustStore(ts))
	require.NoError(t, err)
	_, err = loader.Load()
	require.Error(t, err)
}

func TestCompareSemverHelper(t *testing.T) {
	require.Equal(t, 0, compareSemver("1.0.0", "1.0.0"))
	require.Equal(t, 1, compareSemver("2.1.0", "2.0.5"))
	require.Equal(t, -1, compareSemver("1.9.9", "2.0.0"))
	require.Equal(t, 0, compareSemver("v1.2.3", "1.2.3"))
}

func TestReadConfigPayloadVariants(t *testing.T) {
	claude := filepath.Join(t.TempDir(), ".claude")
	require.NoError(t, os.MkdirAll(claude, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yml"), []byte("version: 1"), 0o600))
	path, data, err := readConfigPayload(claude)
	require.NoError(t, err)
	require.Contains(t, path, "config.yml")
	require.NotEmpty(t, data)
	require.NoError(t, os.Remove(filepath.Join(claude, "config.yml")))
	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.json"), []byte(`{"version":"1"}`), 0o600))
	path, data, err = readConfigPayload(claude)
	require.NoError(t, err)
	require.Contains(t, path, "config.json")
	require.NotEmpty(t, data)
}

func TestWithinDirHelper(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "child"), 0o755))
	inside, err := withinDir(base, filepath.Join(base, "child"))
	require.NoError(t, err)
	require.True(t, inside)
	out, err := withinDir(base, filepath.Join(base, "..", "other"))
	require.NoError(t, err)
	require.False(t, out)
}

func TestDiscoverPluginRefsIgnoresHidden(t *testing.T) {
	root := t.TempDir()
	plugDir := filepath.Join(root, "plugins")
	require.NoError(t, os.MkdirAll(filepath.Join(plugDir, "visible"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(plugDir, ".hidden"), 0o755))
	refs, err := discoverPluginRefs(plugDir)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, "visible", refs[0].Name)
}

func TestWatcherAddWatchBranchesAndReloadSkip(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	ts, signer := makeTrustStore(t)
	writePlugin(t, claude, "watch", signer)
	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yaml"), []byte(`version: 1.0.0
plugins:
  - name: watch
	`), 0o600))
	loader, err := NewLoader(root, WithTrustStore(ts))
	require.NoError(t, err)
	w, err := NewWatcher(loader)
	require.NoError(t, err)
	// non-existent path branch
	require.NoError(t, w.addWatch(filepath.Join(root, "missing")))
	// existing path branch
	require.NoError(t, w.addWatch(claude))
	// initialise last hash and ensure reload short-circuits
	cfg, err := loader.Load()
	require.NoError(t, err)
	w.lastHash = cfg.SourceHash
	called := make(chan struct{}, 1)
	w.onChange = func(*ProjectConfig) { called <- struct{}{} }
	w.reload()
	select {
	case <-called:
		t.Fatal("unexpected reload callback for identical hash")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestLoaderAbsolutePluginPathRejected(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claude, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yaml"), []byte(`version: 1.0.0
plugins:
  - name: abs
    path: /tmp/evil
`), 0o644))
	ts := plugins.NewTrustStore()
	ts.AllowUnsigned(true)
	loader, err := NewLoader(root, WithTrustStore(ts))
	require.NoError(t, err)
	_, err = loader.Load()
	require.Error(t, err)
}

func TestLoadPluginRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claude, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(claude, "config.yaml"), []byte(`version: 1.0.0
plugins:
  - name: evil
    path: ../evil
`), 0o644))
	ts := plugins.NewTrustStore()
	ts.AllowUnsigned(true)
	loader, err := NewLoader(root, WithTrustStore(ts))
	require.NoError(t, err)
	_, err = loader.Load()
	require.Error(t, err)
}

func TestNewLoaderInvalidRoot(t *testing.T) {
	_, err := NewLoader("")
	require.Error(t, err)
}

func TestLoaderLastEmpty(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root)
	require.NoError(t, err)
	_, ok := loader.Last()
	require.False(t, ok)
}

func TestLoaderWithMissingClaudeOverride(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root, WithClaudeDir(filepath.Join(root, "missing")))
	require.NoError(t, err)
	_, err = loader.Load()
	require.Error(t, err)
}
