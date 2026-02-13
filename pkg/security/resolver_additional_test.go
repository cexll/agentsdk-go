package security

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSplitPathForWalk(t *testing.T) {
	sep := string(filepath.Separator)
	tests := []struct {
		name      string
		input     string
		wantRoot  string
		wantParts []string
	}{
		{
			name:      "relative path",
			input:     filepath.Join("a", "b", "c"),
			wantRoot:  "",
			wantParts: []string{"a", "b", "c"},
		},
	}

	// Absolute path tests differ by platform: on Windows, bare "\" is not
	// considered absolute by filepath.IsAbs (it's root-relative without a
	// volume). Use a volume-qualified root instead.
	if runtime.GOOS == "windows" {
		tests = append(tests,
			struct {
				name      string
				input     string
				wantRoot  string
				wantParts []string
			}{
				name:      "absolute root only",
				input:     `C:\`,
				wantRoot:  `C:\`,
				wantParts: nil,
			},
			struct {
				name      string
				input     string
				wantRoot  string
				wantParts []string
			}{
				name:      "absolute with parts",
				input:     `C:\usr\local\bin`,
				wantRoot:  `C:\`,
				wantParts: []string{"usr", "local", "bin"},
			},
		)
	} else {
		tests = append(tests,
			struct {
				name      string
				input     string
				wantRoot  string
				wantParts []string
			}{
				name:      "absolute root only",
				input:     sep,
				wantRoot:  sep,
				wantParts: nil,
			},
			struct {
				name      string
				input     string
				wantRoot  string
				wantParts []string
			}{
				name:      "absolute with parts",
				input:     sep + filepath.Join("usr", "local", "bin"),
				wantRoot:  sep,
				wantParts: []string{"usr", "local", "bin"},
			},
		)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, parts := splitPathForWalk(tt.input)
			if root != tt.wantRoot {
				t.Errorf("root = %q, want %q", root, tt.wantRoot)
			}
			if len(parts) != len(tt.wantParts) {
				t.Fatalf("parts = %v, want %v", parts, tt.wantParts)
			}
			for i := range parts {
				if parts[i] != tt.wantParts[i] {
					t.Errorf("parts[%d] = %q, want %q", i, parts[i], tt.wantParts[i])
				}
			}
		})
	}
}

func TestOpenNoFollowSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no O_NOFOLLOW on windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := openNoFollow(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestOpenNoFollowRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no O_NOFOLLOW on windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := openNoFollow(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected error: %v", err)
	}
}
