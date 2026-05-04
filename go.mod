module github.com/jenaiz/pcke

go 1.25.5

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/fsnotify/fsnotify v1.10.0
	github.com/go-git/go-git/v5 v5.18.0
	github.com/mark3labs/mcp-go v0.49.0
	github.com/smacker/go-tree-sitter v0.0.0-20240827094217-dd81d9e9be82
	github.com/spf13/cobra v1.10.2
)

require (
	dario.cat/mergo v1.0.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.1.6 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/cyphar/filepath-securejoin v0.4.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.8.0 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/pjbgf/sha1cd v0.3.2 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/skeema/knownhosts v1.3.1 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
)

// Pre-pivot tags retracted per ADR-0008 (amended by ADR-0009) and PRD v5.2 §2.4.
// The old releases remain in git history; tooling should treat these versions as
// withdrawn. Use the v0.4.0..v0.9.0 tags published at the same commits instead.
//
// Note: v2.0.0 cannot be retracted here because the module path
// github.com/jenaiz/pcke (no /v2 suffix) is not valid at major version 2 — Go
// rejects retracts for versions outside the module's major-version range. The
// v2.0.0 tag was already unreachable via `go install`; release notes for v0.9.1
// document its supersession by v0.9.0.
retract (
	v1.0.0 // Pre-pivot. Use v0.4.0.
	v1.1.0 // Pre-pivot. Use v0.5.0.
	v1.1.1 // Pre-pivot. Use v0.5.1.
	v1.2.0 // Pre-pivot. Use v0.6.0.
	v1.2.1 // Pre-pivot. Use v0.6.1.
	v1.3.0 // Pre-pivot. Use v0.7.0.
	v1.4.0 // Pre-pivot. Use v0.8.0.
)
