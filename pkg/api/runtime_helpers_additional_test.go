package api

import "testing"

func TestRuntimeOutputDirHelpers(t *testing.T) {
	if bashOutputBaseDir() == "" || toolOutputBaseDir() == "" {
		t.Fatalf("expected base dirs")
	}
	if bashOutputSessionDir("sess") == "" || toolOutputSessionDir("sess") == "" {
		t.Fatalf("expected session dirs")
	}
	if err := cleanupBashOutputSessionDir("sess"); err != nil {
		t.Fatalf("cleanup bash output: %v", err)
	}
	if err := cleanupToolOutputSessionDir("sess"); err != nil {
		t.Fatalf("cleanup tool output: %v", err)
	}
}
