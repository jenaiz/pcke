// hooks_release.go is the no-op counterpart of hooks_debug.go for btree.
//
//go:build !kdbdebug

package btree

// checkCrashHook is a no-op in release builds.
func checkCrashHook(_ string) {}
