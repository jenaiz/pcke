// hooks_release.go is the no-op counterpart of hooks_debug.go for tx.
//
//go:build !kdbdebug

package tx

// checkCrashHook is a no-op in release builds.
func checkCrashHook(_ string) {}
