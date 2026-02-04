package commands

import (
	"errors"
	"testing"
)

func TestReadFileOverrideOrOS(t *testing.T) {
	t.Parallel()

	fileOpOverridesMu.Lock()
	fileOpOverrides.read = func(string) ([]byte, error) { return []byte("ok"), nil }
	fileOpOverridesMu.Unlock()
	defer func() {
		fileOpOverridesMu.Lock()
		fileOpOverrides.read = nil
		fileOpOverridesMu.Unlock()
	}()

	data, err := readFileOverrideOrOS("any")
	if err != nil || string(data) != "ok" {
		t.Fatalf("unexpected read %q err=%v", data, err)
	}

	fileOpOverridesMu.Lock()
	fileOpOverrides.read = func(string) ([]byte, error) { return nil, errors.New("boom") }
	fileOpOverridesMu.Unlock()
	if _, err := readFileOverrideOrOS("any"); err == nil {
		t.Fatalf("expected override error")
	}
}
