// hooks_debug.go provides crash injection hooks for the tx package.
// See internal/kdb/hooks_debug.go for the overall crash harness design.
//
//go:build kdbdebug

package tx

import "os"

// checkCrashHook terminates the process if PCKE_CRASH_AT matches name.
func checkCrashHook(name string) {
	if os.Getenv("PCKE_CRASH_AT") == name {
		os.Exit(137)
	}
}
