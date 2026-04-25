// hooks_release.go is the no-op counterpart of hooks_debug.go for the pool.
//
//go:build !kdbdebug

package bufpool

// checkCrashHook is a no-op in release builds.
func checkCrashHook(_ string) {}
