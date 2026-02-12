package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestBuiltinToolsRespectGitignoreSetting(t *testing.T) {
	dir := t.TempDir()
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		root = dir
	}

	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("HELLO\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("HELLO\n"), 0o600))

	tests := []struct {
		name              string
		respectGitignore  bool
		wantIgnoredInGlob bool
		wantIgnoredInGrep bool
		wantKeepInGlob    bool
		wantKeepInGrep    bool
	}{
		{
			name:              "respect gitignore",
			respectGitignore:  true,
			wantIgnoredInGlob: false,
			wantIgnoredInGrep: false,
			wantKeepInGlob:    true,
			wantKeepInGrep:    true,
		},
		{
			name:              "ignore gitignore",
			respectGitignore:  false,
			wantIgnoredInGlob: true,
			wantIgnoredInGrep: true,
			wantKeepInGlob:    true,
			wantKeepInGrep:    true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			respect := tc.respectGitignore
			settings := &config.Settings{RespectGitignore: &respect}
			factories := builtinToolFactories(root, false, EntryPointCLI, settings, nil, nil, nil)

			globTool := factories["glob"]()
			require.NotNil(t, globTool)

			globRes, err := globTool.Execute(context.Background(), map[string]interface{}{"pattern": "*"})
			require.NoError(t, err)
			require.NotNil(t, globRes)

			globData, ok := globRes.Data.(map[string]interface{})
			require.True(t, ok)
			globMatchesAny, ok := globData["matches"]
			require.True(t, ok)
			globMatches, ok := globMatchesAny.([]string)
			require.True(t, ok)
			require.Equal(t, tc.wantIgnoredInGlob, containsString(globMatches, "ignored.txt"))
			require.Equal(t, tc.wantKeepInGlob, containsString(globMatches, "keep.txt"))

			grepTool := factories["grep"]()
			require.NotNil(t, grepTool)

			grepRes, err := grepTool.Execute(context.Background(), map[string]interface{}{
				"pattern":     "HELLO",
				"path":        ".",
				"output_mode": "files_with_matches",
				"head_limit":  100,
			})
			require.NoError(t, err)
			require.NotNil(t, grepRes)

			grepData, ok := grepRes.Data.(map[string]interface{})
			require.True(t, ok)
			filesAny, ok := grepData["files"]
			if !ok {
				t.Fatalf("expected files in grep output data, got keys %+v", grepData)
			}
			files, ok := filesAny.([]string)
			require.True(t, ok)
			require.Equal(t, tc.wantIgnoredInGrep, containsString(files, "ignored.txt"))
			require.Equal(t, tc.wantKeepInGrep, containsString(files, "keep.txt"))
		})
	}
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
