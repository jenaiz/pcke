//go:build darwin

package wal

import (
	"os"
	"syscall"
)

// durableSync performs F_FULLFSYNC on macOS, which flushes the drive's write
// cache (unlike plain fsync which only flushes the OS page cache). Falls back
// to fsync if F_FULLFSYNC returns ENOTSUP (e.g., network filesystems).
func durableSync(f *os.File) error {
	_, _, err := syscall.Syscall(
		syscall.SYS_FCNTL,
		f.Fd(),
		syscall.F_FULLFSYNC,
		0,
	)
	if err == 0 {
		return nil
	}
	if err == syscall.ENOTSUP {
		return f.Sync()
	}
	return err
}
