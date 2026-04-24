// Package main holds the exit-code mapping for pcke CLI errors.
//
// See PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md §10.2.
package main

// Exit codes for pcke CLI.
const (
	ExitSuccess        = 0
	ExitUserError      = 1
	ExitConfigError    = 2
	ExitLockConflict   = 3
	ExitCorruption     = 4
	ExitInternal       = 5
	ExitSchemaMismatch = 6
)
