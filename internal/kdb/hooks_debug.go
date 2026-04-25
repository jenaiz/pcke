// hooks_debug.go provides crash injection hooks for kdb, compiled only
// with the kdbdebug build tag. Each hook checks the PCKE_CRASH_AT
// environment variable and terminates the process if the hook name matches.
// This enables the crash recovery test harness (internal/kdb/testutil/crashsim)
// to simulate crashes at precise points in the commit path.
//
// Phase 0 — Task T11.
//
//go:build kdbdebug

package kdb

import "os"

// checkCrashHook terminates the process with exit code 137 if the
// PCKE_CRASH_AT environment variable matches the given hook name.
func checkCrashHook(name string) {
	if os.Getenv("PCKE_CRASH_AT") == name {
		os.Exit(137)
	}
}
