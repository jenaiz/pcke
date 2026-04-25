package analysis // secrets.go — path-based exclusion and content-based redaction.

import (
	"regexp"
	"strings"
)

// secretPathPatterns contains glob-style path patterns that indicate a
// file should be excluded from analysis entirely.
var secretPathPatterns = []string{
	".env",
	"*.pem",
	"*.key",
	"*.p12",
	"*.pfx",
	"*.jks",
	"*.keystore",
	"**/secrets/**",
	"**/*_secret*",
	"**/*_token*",
	"**/.htpasswd",
	"**/credentials",
	"**/.netrc",
	"**/.npmrc",
	"**/id_rsa",
	"**/id_ed25519",
	"**/id_ecdsa",
	"**/id_dsa",
}

// secretContentPatterns matches high-value secrets in file content.
var secretContentPatterns = []*regexp.Regexp{
	// AWS access key IDs.
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// AWS secret access keys (40 base64 chars).
	regexp.MustCompile(`(?i)(?:aws_secret_access_key|secret_key)\s*[:=]\s*[A-Za-z0-9/+=]{40}`),
	// Generic API key/token assignments.
	regexp.MustCompile(`(?i)(?:api[_-]?key|api[_-]?token|auth[_-]?token|access[_-]?token|secret[_-]?key|private[_-]?key)\s*[:=]\s*["']?[A-Za-z0-9_\-/.+=]{20,}["']?`),
	// GitHub personal access tokens.
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`ghs_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`ghr_[A-Za-z0-9]{36}`),
	// Slack tokens.
	regexp.MustCompile(`xox[bprs]-[A-Za-z0-9\-]{10,}`),
	// PEM private key blocks.
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`),
	// Generic high-entropy strings in key/secret context.
	regexp.MustCompile(`(?i)(?:password|passwd|pwd)\s*[:=]\s*["']?[^\s"']{8,}["']?`),
	// Bearer tokens.
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_\-/.+=]{20,}`),
	// Stripe keys.
	regexp.MustCompile(`sk_(?:live|test)_[A-Za-z0-9]{24,}`),
	// Google API keys.
	regexp.MustCompile(`AIza[A-Za-z0-9_\-]{35}`),
	// Heroku API key.
	regexp.MustCompile(`(?i)heroku.*[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}`),
	// Twilio.
	regexp.MustCompile(`SK[A-Fa-f0-9]{32}`),
	// SendGrid.
	regexp.MustCompile(`SG\.[A-Za-z0-9_\-]{22}\.[A-Za-z0-9_\-]{43}`),
	// npm tokens.
	regexp.MustCompile(`npm_[A-Za-z0-9]{36}`),
}

// IsSecretPath reports whether the given relative path matches a
// secret-file pattern and should be excluded from analysis.
func IsSecretPath(relPath string) bool {
	lower := strings.ToLower(relPath)
	base := lastPathComponent(lower)

	for _, pat := range secretPathPatterns {
		if matchSecretPattern(pat, lower, base) {
			return true
		}
	}

	// .env variants: .env, .env.local, .env.production, etc.
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}

	return false
}

// RedactSecrets replaces secret values in content with "[REDACTED]".
// It returns the redacted content and true if any redaction was applied.
func RedactSecrets(content string) (string, bool) {
	redacted := false
	result := content
	for _, re := range secretContentPatterns {
		if re.MatchString(result) {
			result = re.ReplaceAllString(result, "[REDACTED]")
			redacted = true
		}
	}
	return result, redacted
}

// matchSecretPattern matches a single pattern against a path.
func matchSecretPattern(pattern, lower, base string) bool {
	switch {
	case strings.HasPrefix(pattern, "**/"):
		// Match anywhere in path.
		suffix := pattern[3:]
		if strings.Contains(suffix, "/") {
			// Directory pattern like **/secrets/**
			dir := strings.TrimSuffix(suffix, "/**")
			return strings.Contains(lower, "/"+dir+"/") || strings.HasPrefix(lower, dir+"/")
		}
		// File pattern like **/*_secret*
		return matchWildcard(suffix, base)

	case strings.HasPrefix(pattern, "*."):
		// Extension match.
		ext := pattern[1:] // e.g. ".pem"
		return strings.HasSuffix(base, ext)

	default:
		// Exact base match.
		return base == pattern
	}
}

// matchWildcard matches simple patterns with * wildcards against a string.
func matchWildcard(pattern, s string) bool {
	// Split pattern on * and check that all parts appear in order.
	parts := strings.Split(pattern, "*")
	idx := 0
	for _, part := range parts {
		if part == "" {
			continue
		}
		pos := strings.Index(s[idx:], part)
		if pos < 0 {
			return false
		}
		idx += pos + len(part)
	}
	return true
}

// lastPathComponent returns the file name from a slash-separated path.
func lastPathComponent(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
