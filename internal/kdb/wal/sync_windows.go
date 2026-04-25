//go:build windows

package wal

import "os"

// durableSync on Windows uses plain fsync. FlushFileBuffers is the Windows
// equivalent and is what Go's os.File.Sync calls internally.
func durableSync(f *os.File) error {
	return f.Sync()
}
