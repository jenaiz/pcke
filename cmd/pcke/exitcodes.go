// Package main holds the exit-code mapping for pcke CLI errors.
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
