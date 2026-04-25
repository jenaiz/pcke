// hooks_release.go is the no-op counterpart of hooks_debug.go for the WAL.
//
//go:build !kdbdebug

package wal

// checkCrashHook is a no-op in release builds.
func checkCrashHook(_ string) {}
