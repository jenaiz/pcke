package onboard

import (
	"strings"
	"testing"
	"time"
)

func TestRenderText(t *testing.T) {
	w := &Walkthrough{
		Title:       "Test Project",
		GeneratedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NodeCount:   10,
		ModuleCount: 3,
		Sections: []Section{
			{Name: "overview", Title: "Project Overview", Content: "Hello world", Order: 1},
			{Name: "arch", Title: "Architecture", Content: "Layered design", Order: 2},
		},
	}

	text := RenderText(w)

	if !strings.Contains(text, "=== Test Project ===") {
		t.Error("missing title in text output")
	}
	if !strings.Contains(text, "Files: 10") {
		t.Error("missing file count")
	}
	if !strings.Contains(text, "[1] Project Overview") {
		t.Error("missing section header")
	}
	if !strings.Contains(text, "Hello world") {
		t.Error("missing section content")
	}
}

func TestRenderMarkdown(t *testing.T) {
	w := &Walkthrough{
		Title:       "Test Project",
		GeneratedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NodeCount:   10,
		ModuleCount: 3,
		Sections: []Section{
			{Name: "overview", Title: "Project Overview", Content: "Hello", Order: 1},
		},
	}

	md := RenderMarkdown(w)

	if !strings.Contains(md, "# Test Project") {
		t.Error("missing title in markdown output")
	}
	if !strings.Contains(md, "> Generated:") {
		t.Error("missing generated line")
	}
	if !strings.Contains(md, "Hello") {
		t.Error("missing section content")
	}
}

func TestRenderJSON(t *testing.T) {
	w := &Walkthrough{
		Title:       "Test",
		GeneratedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NodeCount:   5,
		ModuleCount: 2,
		Sections: []Section{
			{Name: "overview", Title: "Overview", Content: "Content", Order: 1},
		},
	}

	js, err := RenderJSON(w)
	if err != nil {
		t.Fatalf("RenderJSON() error: %v", err)
	}

	if !strings.Contains(js, `"title": "Test"`) {
		t.Error("missing title in JSON")
	}
	if !strings.Contains(js, `"node_count": 5`) {
		t.Error("missing node_count in JSON")
	}
}
