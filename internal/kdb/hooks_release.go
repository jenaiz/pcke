// hooks_release.go is the no-op counterpart of hooks_debug.go, compiled
// when the kdbdebug build tag is absent. All crash hooks are inlined away.
//
//go:build !kdbdebug

package kdb

// checkCrashHook is a no-op in release builds.
func checkCrashHook(_ string) {}
