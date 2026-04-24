// Package kdb provides the embedded key-value storage engine for pcke.
//
// This file declares sentinel errors shared across kdb sub-packages.
// See PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md §10.
package kdb

import "errors"

// Sentinel errors for kdb operations.
var (
	// ErrInvalidConfig indicates the configuration is invalid.
	ErrInvalidConfig = errors.New("kdb: invalid configuration")

	// ErrDBClosed indicates the database has already been closed.
	ErrDBClosed = errors.New("kdb: database is closed")
)
