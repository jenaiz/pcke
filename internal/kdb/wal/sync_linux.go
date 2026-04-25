//go:build linux

package wal

import "os"

// durableSync on Linux uses plain fsync, which is sufficient on reliable
// hardware with EXT4 data=ordered (default).
func durableSync(f *os.File) error {
	return f.Sync()
}
