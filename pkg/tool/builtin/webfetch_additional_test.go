package toolbuiltin

import "testing"

func TestHostRedirectError(t *testing.T) {
	err := &hostRedirectError{target: "http://example.com"}
	if err.Error() == "" {
		t.Fatalf("expected error string")
	}
}
