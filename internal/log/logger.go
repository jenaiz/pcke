// Package log centralises slog wiring for pcke and kdb subsystems.
//
// Phase −1 bootstrap. Provides:
//   - Logger(subsystem) returns a *slog.Logger tagged with the subsystem.
//   - Level resolution from PCKE_LOG_LEVEL with optional per-subsystem
//     override via PCKE_LOG_LEVEL_<NORMALISED_SUBSYSTEM>.
//   - A handler that redacts string values whose attribute key matches
//     /(?i)(secret|token|key|password|credential)/ before they reach the
//     output. Defence-in-depth for diagnostics & log surfaces (Plan §8.3).
//
// The logger is NOT a goal in itself: real usage starts in Phase 0.
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
)

// envLogLevel is the global level env var. Values: debug | info | warn | error.
const envLogLevel = "PCKE_LOG_LEVEL"

// envLogLevelPrefix lets callers override per-subsystem, e.g.
// PCKE_LOG_LEVEL_KDB_WAL=debug.
const envLogLevelPrefix = "PCKE_LOG_LEVEL_"

var (
	mu      sync.Mutex
	output  io.Writer = os.Stderr
	loggers           = map[string]*slog.Logger{}
)

// secretKeyRe matches attribute keys whose values must be redacted.
// Conservative on purpose; false positives are preferable to leaks.
var secretKeyRe = regexp.MustCompile(`(?i)(secret|token|key|password|credential)`)

// redactedPlaceholder replaces matched string values in log attributes.
const redactedPlaceholder = "[REDACTED]"

// Logger returns a *slog.Logger for the given subsystem, e.g. "kdb.wal" or
// "pcke.scan". Loggers are cached; concurrent calls are safe.
//
// Level resolution (highest precedence first):
//  1. PCKE_LOG_LEVEL_<SUBSYSTEM_NORMALISED> (e.g. KDB_WAL for "kdb.wal").
//  2. PCKE_LOG_LEVEL.
//  3. Default: info.
func Logger(subsystem string) *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	if l, ok := loggers[subsystem]; ok {
		return l
	}
	level := resolveLevel(subsystem)
	handler := newRedactingHandler(output, level)
	l := slog.New(handler).With(slog.String("subsystem", subsystem))
	loggers[subsystem] = l
	return l
}

// SetOutput is intended for tests. Resets the cached loggers so the new sink
// takes effect.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	output = w
	loggers = map[string]*slog.Logger{}
}

func resolveLevel(subsystem string) slog.Level {
	if v := os.Getenv(envLogLevelPrefix + normaliseSubsystem(subsystem)); v != "" {
		return parseLevel(v)
	}
	if v := os.Getenv(envLogLevel); v != "" {
		return parseLevel(v)
	}
	return slog.LevelInfo
}

func normaliseSubsystem(s string) string {
	return strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(s))
}

func parseLevel(v string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// redactingHandler wraps a slog.Handler and rewrites attributes whose key
// suggests they carry secrets.
type redactingHandler struct{ inner slog.Handler }

func newRedactingHandler(w io.Writer, lvl slog.Level) *redactingHandler {
	return &redactingHandler{
		inner: slog.NewTextHandler(w, &slog.HandlerOptions{
			Level:       lvl,
			ReplaceAttr: redactReplaceAttr,
		}),
	}
}

func redactReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	if secretKeyRe.MatchString(a.Key) && a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, redactedPlaceholder)
	}
	return a
}

func (h *redactingHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &redactingHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name)}
}
