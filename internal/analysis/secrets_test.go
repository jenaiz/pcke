package analysis

import (
	"testing"
)

// secretPathFixtures provides 100+ path-based secret detection fixtures.
// Each fixture is a pair: (path, shouldBeSecret).
var secretPathFixtures = []struct {
	path   string
	secret bool
}{
	// .env variants (10)
	{".env", true},
	{".env.local", true},
	{".env.production", true},
	{".env.staging", true},
	{".env.development", true},
	{".env.test", true},
	{"config/.env", true},
	{"deploy/.env.prod", true},
	{"app/.env.docker", true},
	{".env.backup", true},

	// PEM files (10)
	{"server.pem", true},
	{"cert.pem", true},
	{"ca-bundle.pem", true},
	{"tls/server.pem", true},
	{"certs/intermediate.pem", true},
	{"ssl/client.pem", true},
	{"deploy/ca.pem", true},
	{"config/tls.pem", true},
	{"keys/signing.pem", true},
	{"internal/crypto/test.pem", true},

	// Key files (10)
	{"private.key", true},
	{"server.key", true},
	{"id_rsa", true},
	{"ssh/id_rsa", true},
	{"keys/deploy.key", true},
	{"tls/server.key", true},
	{"config/api.key", true},
	{".ssh/id_ed25519", true},
	{"certs/ca.key", true},
	{"deploy/signing.key", true},

	// P12/PFX/JKS (6)
	{"keystore.p12", true},
	{"client.pfx", true},
	{"truststore.jks", true},
	{"deploy/cert.p12", true},
	{"config/client.pfx", true},
	{"java/app.keystore", true},

	// Secrets directories (10)
	{"secrets/database.yml", true},
	{"config/secrets/api.json", true},
	{"deploy/secrets/env.sh", true},
	{"app/secrets/tokens.txt", true},
	{"infra/secrets/passwords", true},
	{"k8s/secrets/sealed.yaml", true},
	{"helm/secrets/values.yaml", true},
	{"ansible/secrets/vault.yml", true},
	{"terraform/secrets/tfvars", true},
	{"ci/secrets/deploy.sh", true},

	// *_secret* patterns (8)
	{"app_secret.yml", true},
	{"db_secret_config.json", true},
	{"api_secret_key.txt", true},
	{"config/client_secret.json", true},
	{"deploy/oauth_secret.env", true},
	{"app/user_secret_store.go", true},
	{"internal/auth_secret.go", true},
	{"pkg/session_secret.go", true},

	// *_token* patterns (8)
	{"refresh_token.txt", true},
	{"api_token_config.json", true},
	{"config/oauth_token.yml", true},
	{"deploy/service_token.env", true},
	{"app/user_token_store.json", true},
	{"internal/jwt_token.go", true},
	{"pkg/auth_token.go", true},
	{"scripts/deploy_token.sh", true},

	// SSH keys (5)
	{".ssh/id_ecdsa", true},
	{".ssh/id_dsa", true},
	{"home/user/.ssh/id_rsa", true},
	{"deploy/id_ed25519", true},
	{"config/id_rsa", true},

	// Other credential files (5)
	{"config/.htpasswd", true},
	{"app/credentials", true},
	{"home/.netrc", true},
	{"config/.npmrc", true},
	{"deploy/.htpasswd", true},

	// More .env variants (10)
	{"backend/.env.ci", true},
	{"frontend/.env.e2e", true},
	{"services/.env.secrets", true},
	{"deploy/.env.aws", true},
	{"ops/.env.gcp", true},
	{"infra/.env.azure", true},
	{"api/.env.private", true},
	{"worker/.env.credentials", true},
	{"scheduler/.env.keys", true},
	{"gateway/.env.tokens", true},

	// More key/cert files (10)
	{"certs/wildcard.pem", true},
	{"ssl/chain.pem", true},
	{"tls/fullchain.pem", true},
	{"deploy/service.key", true},
	{"infra/root-ca.key", true},
	{"config/jwt.key", true},
	{"auth/oauth.key", true},
	{"crypto/encrypt.key", true},
	{"backup/master.key", true},
	{"vault/unseal.key", true},

	// More secret directories (8)
	{"prod/secrets/config.yaml", true},
	{"staging/secrets/db.env", true},
	{"dev/secrets/local.json", true},
	{"shared/secrets/common.yml", true},
	{"platform/secrets/keys.json", true},
	{"service/secrets/certs.pem", true},
	{"cluster/secrets/auth.yaml", true},
	{"cloud/secrets/iam.json", true},

	// NON-SECRET files that should NOT be excluded (28)
	{"main.go", false},
	{"README.md", false},
	{"internal/config/config.go", false},
	{"cmd/app/main.go", false},
	{"pkg/api/handler.go", false},
	{"Makefile", false},
	{"Dockerfile", false},
	{"go.mod", false},
	{"go.sum", false},
	{".github/workflows/ci.yml", false},
	{"docs/architecture.md", false},
	{"internal/kdb/db.go", false},
	{"internal/kdb/btree/btree.go", false},
	{"public/index.html", false},
	{"static/style.css", false},
	{"src/app.ts", false},
	{"lib/utils.py", false},
	{"tests/test_main.py", false},
	{"config.toml", false},
	{"docker-compose.yml", false},
	{"terraform/main.tf", false},
	{"scripts/build.sh", false},
	{"internal/analysis/scanner.go", false},
	{"internal/analysis/secrets.go", false},
	{"CONTRIBUTING.md", false},
	{"LICENSE", false},
	{".golangci.yml", false},
	{"vendor/github.com/pkg/errors/errors.go", false},
}

func TestIsSecretPath_Fixtures(t *testing.T) {
	secrets := 0
	for _, f := range secretPathFixtures {
		got := IsSecretPath(f.path)
		if got != f.secret {
			t.Errorf("IsSecretPath(%q) = %v, want %v", f.path, got, f.secret)
		}
		if f.secret {
			secrets++
		}
	}
	t.Logf("tested %d paths (%d secret, %d clean)", len(secretPathFixtures), secrets, len(secretPathFixtures)-secrets)
	if secrets < 100 {
		t.Errorf("need ≥100 secret fixtures, got %d", secrets)
	}
}

// secretContentFixtures contains content strings that should be redacted.
var secretContentFixtures = []struct {
	name    string
	content string
}{
	{"aws_access_key", "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE"},
	{"aws_secret_key", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
	{"api_key_quoted", `api_key = "sk-proj-1234567890abcdefghijklmnop"`},
	{"api_token_env", `API_TOKEN=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0`},
	{"github_pat", "token = ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"},
	{"github_oauth", "GITHUB_TOKEN=gho_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"},
	{"github_server", "GH_TOKEN=ghs_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"},
	{"github_refresh", "token=ghr_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"},
	{"slack_bot", "SLACK_TOKEN=xoxb-1234567890-abcdefghij"},
	{"slack_user", "token=xoxp-1234567890-abcdefghij"},
	{"pem_rsa", "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKC..."},
	{"pem_ec", "-----BEGIN EC PRIVATE KEY-----\nMHQCAQEE..."},
	{"pem_generic", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBA..."},
	{"pem_openssh", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNza..."},
	{"password_plain", `password = "SuperSecret123!@#$%^&*()"`},
	{"passwd_env", "DB_PASSWD=MyDatabasePassword2024"},
	{"pwd_config", `pwd: "admin_password_here"`},
	{"bearer_token", "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
	{"stripe_live", "STRIPE_KEY=sk_live_1234567890abcdefghijklmnopqrstuv"},
	{"stripe_test", "sk_test_1234567890abcdefghijklmnopqrstuv"},
	{"google_api", "GOOGLE_API_KEY=AIzaSyA1234567890abcdefghijklmnopqrs"},
	{"sendgrid", "SENDGRID_KEY=SG.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefg"},
	{"npm_token", "//registry.npmjs.org/:_authToken=npm_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefgh"},
	{"secret_key_env", "SECRET_KEY=a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6"},
	{"private_key_var", `private_key = "pk_live_abcdefghijklmnopqrstuvwxyz1234567890"`},
	{"access_token_json", `access_token = "ya29a0AfH6SMBxxxxxxxxxxxxxxxxxxxxxxxXXX"`},
	{"auth_token_yaml", "auth_token: tok-1234567890abcdefghij"},
}

func TestRedactSecrets_Fixtures(t *testing.T) {
	for _, f := range secretContentFixtures {
		t.Run(f.name, func(t *testing.T) {
			result, redacted := RedactSecrets(f.content)
			if !redacted {
				t.Errorf("expected redaction for %q", f.name)
			}
			if result == f.content {
				t.Errorf("content unchanged after redaction for %q", f.name)
			}
			t.Logf("  %s: redacted=%v", f.name, redacted)
		})
	}
}

func TestRedactSecrets_CleanContent(t *testing.T) {
	clean := []string{
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }",
		"# README\n\nThis is a project.",
		"const maxRetries = 3",
		"func TestSomething(t *testing.T) {}",
		"version: '3'\nservices:\n  web:\n    image: nginx",
	}
	for _, c := range clean {
		result, redacted := RedactSecrets(c)
		if redacted {
			t.Errorf("false positive redaction: %q -> %q", c, result)
		}
	}
}
