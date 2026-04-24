package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerDefaultsToInfo(t *testing.T) {
	t.Setenv(envLogLevel, "")
	var buf bytes.Buffer
	SetOutput(&buf)
	l := Logger("kdb.wal")
	l.Debug("debug-line", "k", "v")
	l.Info("info-line", "k", "v")
	out := buf.String()
	if strings.Contains(out, "debug-line") {
		t.Fatalf("debug should be suppressed at default level: %q", out)
	}
	if !strings.Contains(out, "info-line") {
		t.Fatalf("info should appear: %q", out)
	}
	if !strings.Contains(out, "subsystem=kdb.wal") {
		t.Fatalf("expected subsystem tag in output: %q", out)
	}
}

func TestLoggerSubsystemOverride(t *testing.T) {
	t.Setenv(envLogLevel, "info")
	t.Setenv(envLogLevelPrefix+"KDB_WAL", "debug")
	var buf bytes.Buffer
	SetOutput(&buf)
	l := Logger("kdb.wal")
	l.Debug("debug-line")
	if !strings.Contains(buf.String(), "debug-line") {
		t.Fatalf("subsystem-level override should enable debug: %q", buf.String())
	}
}

func TestLoggerRedactsSecretAttrs(t *testing.T) {
	t.Setenv(envLogLevel, "info")
	var buf bytes.Buffer
	SetOutput(&buf)
	l := Logger("pcke.scan")
	l.Info("hit", "api_key", "AKIAEXAMPLE", "Token", "abcd1234", "harmless", "ok")
	out := buf.String()
	if strings.Contains(out, "AKIAEXAMPLE") {
		t.Fatalf("api_key value leaked: %q", out)
	}
	if strings.Contains(out, "abcd1234") {
		t.Fatalf("Token value leaked: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %q", out)
	}
	if !strings.Contains(out, "harmless=ok") {
		t.Fatalf("non-secret attr should pass through: %q", out)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]string{
		"debug":   "DEBUG",
		"info":    "INFO",
		"warn":    "WARN",
		"warning": "WARN",
		"error":   "ERROR",
		"":        "INFO",
		"weird":   "INFO",
	}
	for in, want := range cases {
		if got := parseLevel(in).String(); got != want {
			t.Errorf("parseLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormaliseSubsystem(t *testing.T) {
	if got := normaliseSubsystem("kdb.wal"); got != "KDB_WAL" {
		t.Fatalf("got %q", got)
	}
	if got := normaliseSubsystem("pcke-scan"); got != "PCKE_SCAN" {
		t.Fatalf("got %q", got)
	}
}
