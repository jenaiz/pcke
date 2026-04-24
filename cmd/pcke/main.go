// Package main is the pcke CLI entry point.
//
// Phase −1 bootstrap: this binary exists only to validate the build and release
// pipeline. It prints the version injected at build time and exits.
// Real CLI surface (Cobra) lands in Phase 0.
package main

import (
	"fmt"
	"os"
)

// version is injected at build time via `-ldflags "-X main.version=..."`.
// Default is "dev" for local builds.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Printf("pcke %s (pre-alpha)\n", version)
			return
		case "--help", "-h", "help":
			fmt.Println("pcke — Project Context & Knowledge Engine (pre-alpha)")
			fmt.Println()
			fmt.Println("Usage: pcke [--version|--help]")
			fmt.Println()
			fmt.Println("Phase −1 bootstrap binary. Real commands ship in Phase 0.")
			return
		}
	}
	fmt.Printf("pcke %s (pre-alpha) — no commands implemented yet\n", version)
}
