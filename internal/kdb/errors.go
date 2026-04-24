// Package kdb provides the embedded key-value storage engine for pcke.
//
// This file declares sentinel errors shared across kdb sub-packages.
// See PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md §10.
package kdb

import "errors"

// Sentinel errors for kdb operations.
var (
	// ErrDBLocked indicates another process holds the exclusive file lock.
	ErrDBLocked = errors.New("kdb: database is locked by another process")

	// ErrChecksumMismatch indicates a CRC32C verification failed on a page.
	ErrChecksumMismatch = errors.New("kdb: page checksum mismatch")

	// ErrInvalidConfig indicates the configuration is invalid.
	ErrInvalidConfig = errors.New("kdb: invalid configuration")
)
