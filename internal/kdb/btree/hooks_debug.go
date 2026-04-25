// hooks_debug.go provides crash injection hooks for the B+tree package.
// See internal/kdb/hooks_debug.go for the overall crash harness design.
//
//go:build kdbdebug

package btree

import "os"

// checkCrashHook terminates the process if PCKE_CRASH_AT matches name.
func checkCrashHook(name string) {
	if os.Getenv("PCKE_CRASH_AT") == name {
		os.Exit(137)
	}
}
