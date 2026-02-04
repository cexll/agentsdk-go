package api

import "testing"

func TestNoopFileSystemPolicy(t *testing.T) {
	p := &noopFileSystemPolicy{}
	if roots := p.Roots(); roots != nil {
		t.Fatalf("expected nil roots")
	}
	p.Allow("/tmp")
	p.root = "/tmp"
	if roots := p.Roots(); len(roots) != 1 || roots[0] != "/tmp" {
		t.Fatalf("unexpected roots %v", roots)
	}
	if err := p.Validate(""); err != nil {
		t.Fatalf("validate should no-op")
	}
}
