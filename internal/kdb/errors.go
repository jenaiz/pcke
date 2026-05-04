// Package kdb provides the embedded key-value storage engine for pcke.
//
// This file declares sentinel errors shared across kdb sub-packages.
package kdb

import "errors"

// Sentinel errors for kdb operations.
var (
	// ErrInvalidConfig indicates the configuration is invalid.
	ErrInvalidConfig = errors.New("kdb: invalid configuration")

	// ErrDBClosed indicates the database has already been closed.
	ErrDBClosed = errors.New("kdb: database is closed")
)
