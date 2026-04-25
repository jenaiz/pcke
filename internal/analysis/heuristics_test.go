package analysis

import (
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		path string
		want FileClass
	}{
		// Test files.
		{"internal/kdb/db_test.go", ClassTest},
		{"src/app.spec.ts", ClassTest},
		{"src/app.test.tsx", ClassTest},
		{"tests/test_main.py", ClassTest},
		{"lib/utils_test.py", ClassTest},
		{"src/MainTest.java", ClassTest},

		// Entry points.
		{"cmd/pcke/main.go", ClassEntryPoint},
		{"cli/root.go", ClassEntryPoint},
		{"bin/server.js", ClassEntryPoint},

		// API layer.
		{"api/handler.go", ClassAPI},
		{"routes/users.js", ClassAPI},
		{"handlers/auth.go", ClassAPI},
		{"endpoints/v1.py", ClassAPI},
		{"controllers/home.rb", ClassAPI},

		// Data layer.
		{"models/user.go", ClassDataLayer},
		{"entities/order.java", ClassDataLayer},
		{"schema/migrations.sql", ClassDataLayer},
		{"domain/product.ts", ClassDataLayer},

		// Infrastructure.
		{"Dockerfile", ClassInfra},
		{"Dockerfile.prod", ClassInfra},
		{"deploy/main.tf", ClassInfra},
		{"infra/network.hcl", ClassInfra},
		{"docker-compose.yml", ClassInfra},
		{"kubernetes/deployment.yaml", ClassInfra},
		{"k8s/service.yml", ClassInfra},

		// Config.
		{"Makefile", ClassConfig},
		{"go.mod", ClassConfig},
		{"package.json", ClassConfig},
		{".golangci.yml", ClassConfig},
		{"pyproject.toml", ClassConfig},
		{".gitignore", ClassConfig},

		// Documentation.
		{"README.md", ClassDoc},
		{"docs/architecture.md", ClassDoc},
		{"CONTRIBUTING.md", ClassDoc},
		{"LICENSE", ClassDoc},
		{"CHANGELOG", ClassDoc},
		{"guide.rst", ClassDoc},

		// Assets.
		{"logo.png", ClassAsset},
		{"fonts/inter.woff2", ClassAsset},
		{"video.mp4", ClassAsset},
		{"archive.tar.gz", ClassAsset},

		// Source.
		{"internal/kdb/db.go", ClassSource},
		{"src/utils.rs", ClassSource},
		{"lib/helpers.py", ClassSource},
		{"app/service.java", ClassSource},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := Classify(tt.path)
			if got != tt.want {
				t.Errorf("Classify(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestLanguage(t *testing.T) {
	tests := []struct {
		ext  string
		want string
	}{
		{".go", "Go"},
		{".py", "Python"},
		{".js", "JavaScript"},
		{".ts", "TypeScript"},
		{".rs", "Rust"},
		{".java", "Java"},
		{".rb", "Ruby"},
		{".c", "C"},
		{".cpp", "C++"},
		{".cs", "C#"},
		{".swift", "Swift"},
		{".kt", "Kotlin"},
		{".sh", "Shell"},
		{".sql", "SQL"},
		{".tf", "Terraform"},
		{".md", "Markdown"},
		{".unknown", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := Language(tt.ext)
			if got != tt.want {
				t.Errorf("Language(%q) = %q, want %q", tt.ext, got, tt.want)
			}
		})
	}
}

func TestDetectModule(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"internal/kdb/db.go", "internal/kdb"},
		{"pkg/utils/helper.go", "pkg/utils"},
		{"cmd/pcke/main.go", "cmd/pcke"},
		{"src/app/service.ts", "src/app"},
		{"lib/core/engine.py", "lib/core"},
		{"main.go", "(root)"},
		{"README.md", "(root)"},
		{"config/settings.toml", "config"},
		{"scripts/build.sh", "scripts"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := DetectModule(tt.path)
			if got != tt.want {
				t.Errorf("DetectModule(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestFileClassString(t *testing.T) {
	tests := []struct {
		class FileClass
		want  string
	}{
		{ClassUnknown, "unknown"},
		{ClassSource, "source"},
		{ClassTest, "test"},
		{ClassEntryPoint, "entry_point"},
		{ClassAPI, "api"},
		{ClassDataLayer, "data_layer"},
		{ClassInfra, "infra"},
		{ClassConfig, "config"},
		{ClassDoc, "doc"},
		{ClassAsset, "asset"},
	}

	for _, tt := range tests {
		got := tt.class.String()
		if got != tt.want {
			t.Errorf("FileClass(%d).String() = %q, want %q", tt.class, got, tt.want)
		}
	}
}
