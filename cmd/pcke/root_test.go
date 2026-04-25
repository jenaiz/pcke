package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelp(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "pcke") {
		t.Error("--help output missing 'pcke'")
	}

	// All stub commands should appear.
	for _, sub := range []string{"init", "scan", "sync", "rule", "note", "status", "modules", "diagnostics", "config"} {
		if !strings.Contains(out, sub) {
			t.Errorf("--help output missing subcommand %q", sub)
		}
	}
}

func TestRootVersion(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "pcke") {
		t.Error("--version output missing 'pcke'")
	}
}

func TestSubcommandStubs(t *testing.T) {
	t.Parallel()

	subs := []string{"init", "scan", "sync", "rule", "note", "status", "modules"}

	for _, sub := range subs {
		t.Run(sub, func(t *testing.T) {
			t.Parallel()

			cmd := newRootCmd()
			buf := new(bytes.Buffer)
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs([]string{sub})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s: %v", sub, err)
			}
		})
	}
}

func TestConfigSubcommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
	}{
		{[]string{"config", "get", "scan.max_file_bytes"}},
		{[]string{"config", "set", "scan.max_file_bytes", "1048576"}},
		{[]string{"config", "list"}},
	}

	for _, tc := range tests {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			t.Parallel()

			cmd := newRootCmd()
			buf := new(bytes.Buffer)
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs(tc.args)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestExitCodes(t *testing.T) {
	t.Parallel()

	// Verify the constants are distinct and match the spec.
	codes := map[int]string{
		ExitSuccess:        "success",
		ExitUserError:      "user error",
		ExitConfigError:    "config error",
		ExitLockConflict:   "lock conflict",
		ExitCorruption:     "corruption",
		ExitInternal:       "internal",
		ExitSchemaMismatch: "schema mismatch",
	}

	if len(codes) != 7 {
		t.Errorf("expected 7 distinct exit codes, got %d", len(codes))
	}
}
