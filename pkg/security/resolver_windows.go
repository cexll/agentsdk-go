//go:build windows

package security

// supportsNoFollow returns false on Windows because syscall.O_NOFOLLOW is not available.
// Windows symlink detection relies solely on os.Lstat in ensureNoSymlink.
// A TOCTOU gap exists between Lstat and actual file access; consider implementing
// FILE_FLAG_OPEN_REPARSE_POINT via syscall.CreateFile for stronger guarantees.
func supportsNoFollow() bool {
	return false
}
