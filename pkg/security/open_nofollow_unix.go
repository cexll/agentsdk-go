//go:build !windows

package security

import (
	"errors"
	"fmt"
	"syscall"
)

func openNoFollow(path string) error {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return fmt.Errorf("security: symlink loop detected %s", path)
		}
		// Some files may not allow opening O_RDONLY (e.g. directories on BSD require O_DIRECTORY).
		// Fall back to a metadata-only stat in that case.
		if errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EISDIR) {
			return nil
		}
		return fmt.Errorf("security: O_NOFOLLOW open failed for %s: %w", path, err)
	}
	syscall.Close(fd)
	return nil
}
