package gitignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewMatcher(t *testing.T) {
	// Create a temp directory with .gitignore
	tmpDir := t.TempDir()
	gitignoreContent := `
# Comment
*.log
node_modules/
!important.log
build/
*.tmp
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0600); err != nil {
		t.Fatal(err)
	}

	m, err := NewMatcher(tmpDir)
	if err != nil {
		t.Fatalf("NewMatcher failed: %v", err)
	}
	if m == nil {
		t.Fatal("Matcher is nil")
	}
	if m.root != tmpDir {
		t.Errorf("root = %q, want %q", m.root, tmpDir)
	}
}

func TestNewMatcherNoGitignore(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewMatcher(tmpDir)
	if err != nil {
		t.Fatalf("NewMatcher failed without .gitignore: %v", err)
	}
	if m == nil {
		t.Fatal("Matcher is nil")
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		relDir  string
		wantOK  bool
		wantPat pattern
	}{
		{
			name:   "empty line",
			line:   "",
			wantOK: false,
		},
		{
			name:   "comment",
			line:   "# this is a comment",
			wantOK: false,
		},
		{
			name:   "simple pattern",
			line:   "*.log",
			wantOK: true,
			wantPat: pattern{
				pattern:  "*.log",
				baseName: true,
			},
		},
		{
			name:   "directory pattern",
			line:   "node_modules/",
			wantOK: true,
			wantPat: pattern{
				pattern:  "node_modules",
				dirOnly:  true,
				baseName: true,
			},
		},
		{
			name:   "negation",
			line:   "!important.log",
			wantOK: true,
			wantPat: pattern{
				pattern:  "important.log",
				negate:   true,
				baseName: true,
			},
		},
		{
			name:   "path with slash",
			line:   "build/output",
			wantOK: true,
			wantPat: pattern{
				pattern:  "build/output",
				baseName: false,
			},
		},
		{
			name:   "leading slash",
			line:   "/root-only.txt",
			wantOK: true,
			wantPat: pattern{
				pattern:  "root-only.txt",
				baseName: false,
			},
		},
		{
			name:   "nested gitignore pattern",
			line:   "dist/",
			relDir: "packages/app",
			wantOK: true,
			wantPat: pattern{
				pattern:  "dist",
				dirOnly:  true,
				baseName: true,
			},
		},
		{
			name:   "nested gitignore path pattern",
			line:   "/local.txt",
			relDir: "packages/app",
			wantOK: true,
			wantPat: pattern{
				pattern:  "packages/app/local.txt",
				baseName: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLine(tt.line, tt.relDir)
			if ok != tt.wantOK {
				t.Errorf("parseLine ok = %v, want %v", ok, tt.wantOK)
				return
			}
			if !ok {
				return
			}
			if got.pattern != tt.wantPat.pattern {
				t.Errorf("pattern = %q, want %q", got.pattern, tt.wantPat.pattern)
			}
			if got.negate != tt.wantPat.negate {
				t.Errorf("negate = %v, want %v", got.negate, tt.wantPat.negate)
			}
			if got.dirOnly != tt.wantPat.dirOnly {
				t.Errorf("dirOnly = %v, want %v", got.dirOnly, tt.wantPat.dirOnly)
			}
			if got.baseName != tt.wantPat.baseName {
				t.Errorf("baseName = %v, want %v", got.baseName, tt.wantPat.baseName)
			}
		})
	}
}

func TestMatcherMatch(t *testing.T) {
	tmpDir := t.TempDir()
	gitignoreContent := `
*.log
node_modules/
!important.log
build/
vendor/
.git
*.tmp
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0600); err != nil {
		t.Fatal(err)
	}

	m, err := NewMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		path  string
		isDir bool
		want  bool
	}{
		// Basic file patterns
		{"log file ignored", "app.log", false, true},
		{"important.log not ignored", "important.log", false, false},
		{"nested log file", "src/debug.log", false, true},
		{"non-log file", "app.txt", false, false},

		// Directory patterns
		{"node_modules dir", "node_modules", true, true},
		{"file in node_modules", "node_modules/package.json", false, true},
		{"build dir", "build", true, true},
		{"build output file", "build/app.js", false, true},
		{"vendor dir", "vendor", true, true},
		{"vendor nested", "vendor/github.com/foo", true, true},

		// .git directory (added by default)
		{".git dir", ".git", true, true},
		{".git/objects", ".git/objects", true, true},

		// Non-ignored paths
		{"src dir", "src", true, false},
		{"main.go", "main.go", false, false},
		{"src/main.go", "src/main.go", false, false},

		// Tmp files
		{"temp file", "cache.tmp", false, true},
		{"nested temp", "tmp/cache.tmp", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Match(tt.path, tt.isDir)
			if got != tt.want {
				t.Errorf("Match(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
			}
		})
	}
}

func TestMatcherNestedGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	// Create root .gitignore
	rootGitignore := `
*.log
node_modules/
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(rootGitignore), 0600); err != nil {
		t.Fatal(err)
	}

	// Create nested directory structure
	subDir := filepath.Join(tmpDir, "packages", "app")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create nested .gitignore
	nestedGitignore := `
dist/
!keep.log
`
	if err := os.WriteFile(filepath.Join(subDir, ".gitignore"), []byte(nestedGitignore), 0600); err != nil {
		t.Fatal(err)
	}

	m, err := NewMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Load nested gitignore
	if err := m.LoadNestedGitignore("packages/app"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		path  string
		isDir bool
		want  bool
	}{
		// Root patterns still apply
		{"root log ignored", "debug.log", false, true},
		{"node_modules", "node_modules", true, true},

		// Nested patterns
		{"nested dist", "packages/app/dist", true, true},
		{"nested dist file", "packages/app/dist/bundle.js", false, true},
		{"nested keep.log", "packages/app/keep.log", false, false},

		// Non-ignored
		{"packages/app/src", "packages/app/src", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Match(tt.path, tt.isDir)
			if got != tt.want {
				t.Errorf("Match(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "main.txt", false},
		{"test_*", "test_foo", true},
		{"test_*", "foo_test", false},
		{"*.tar.gz", "archive.tar.gz", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestMatchDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "src/main.go", true},
		{"**/*.go", "src/pkg/main.go", true},
		{"**/*.txt", "main.go", false},
		{"src/**", "src/main.go", true},
		{"src/**", "src/pkg/main.go", true},
		{"src/**", "other/main.go", false},
		{"**/test", "test", true},
		{"**/test", "foo/test", true},
		{"**/test", "foo/bar/test", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got := matchDoublestar(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchDoublestar(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestShouldTraverse(t *testing.T) {
	tmpDir := t.TempDir()
	gitignoreContent := `
node_modules/
vendor/
.git
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0600); err != nil {
		t.Fatal(err)
	}

	m, err := NewMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		want bool
	}{
		{"src", true},
		{"node_modules", false},
		{"vendor", false},
		{".git", false},
		{"packages", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldTraverse(tt.path)
			if got != tt.want {
				t.Errorf("ShouldTraverse(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchPatternDirOnly(t *testing.T) {
	p := pattern{
		pattern:  "build",
		dirOnly:  true,
		baseName: true,
	}

	// Directory matches
	if !matchPattern(p, "build", true) {
		t.Error("Expected directory 'build' to match")
	}

	// File doesn't match dirOnly pattern
	if matchPattern(p, "build", false) {
		t.Error("Expected file 'build' not to match dirOnly pattern")
	}
}

func TestMatchEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewMatcher(tmpDir)
	if err != nil {
		t.Fatalf("NewMatcher failed: %v", err)
	}

	// Empty path should not match
	if m.Match("", false) {
		t.Error("Empty path should not match")
	}
	if m.Match(".", false) {
		t.Error("Dot path should not match")
	}
}

func TestMatchGlobPrefix(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"build/output", "build/output/file.js", true},
		{"src/lib", "src/lib/utils.go", true},
		{"foo/bar", "foo/bar/baz/qux", true},
		{"other", "something/else", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got := matchGlobPrefix(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchGlobPrefix(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestMatchPatternWithSlash(t *testing.T) {
	tmpDir := t.TempDir()
	gitignoreContent := `
/root-only.txt
build/output/
src/generated/
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0600); err != nil {
		t.Fatal(err)
	}

	m, err := NewMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		path  string
		isDir bool
		want  bool
	}{
		{"root-only at root", "root-only.txt", false, true},
		{"root-only in subdir", "subdir/root-only.txt", false, false},
		{"build/output dir", "build/output", true, true},
		{"build/output file", "build/output/app.js", false, true},
		{"src/generated", "src/generated", true, true},
		{"src/generated file", "src/generated/types.go", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Match(tt.path, tt.isDir)
			if got != tt.want {
				t.Errorf("Match(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
			}
		})
	}
}

func TestMatchGlobError(t *testing.T) {
	// Invalid glob pattern should return false
	got := matchGlob("[invalid", "test")
	if got {
		t.Error("Invalid glob pattern should return false")
	}
}

func TestMatchDoublestarEdgeCases(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		// Empty suffix
		{"foo/**", "foo", true},
		{"foo/**", "foo/bar", true},
		{"foo/**", "foo/bar/baz", true},
		// Prefix patterns
		{"**/test.txt", "test.txt", true},
		{"**/test.txt", "src/test.txt", true},
		{"**/test.txt", "src/pkg/test.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got := matchDoublestar(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchDoublestar(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestNewMatcherReadError(t *testing.T) {
	// Create a directory where .gitignore is also a directory (causes read error)
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	if err := os.MkdirAll(gitignorePath, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := NewMatcher(tmpDir)
	if err == nil {
		t.Error("Expected error when .gitignore is a directory")
	}
}

func TestLoadGitignoreScanError(t *testing.T) {
	tmpDir := t.TempDir()
	m := &Matcher{root: tmpDir}

	// Loading from non-existent subdirectory should work (no error, just no patterns)
	err := m.LoadNestedGitignore("nonexistent")
	if err != nil {
		t.Errorf("LoadNestedGitignore should not error for missing .gitignore: %v", err)
	}
}
