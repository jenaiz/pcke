package onboard

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderText produces a plain-text representation of the walkthrough.
func RenderText(w *Walkthrough) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== %s ===\n", w.Title)
	fmt.Fprintf(&sb, "Generated: %s\n", w.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&sb, "Files: %d | Modules: %d\n\n", w.NodeCount, w.ModuleCount)

	for _, sec := range w.Sections {
		fmt.Fprintf(&sb, "--- [%d] %s ---\n", sec.Order, sec.Title)
		sb.WriteString(sec.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// RenderMarkdown produces a Markdown document from the walkthrough.
func RenderMarkdown(w *Walkthrough) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", w.Title)
	fmt.Fprintf(&sb, "> Generated: %s | Files: %d | Modules: %d\n\n",
		w.GeneratedAt.Format("2006-01-02"), w.NodeCount, w.ModuleCount)

	for _, sec := range w.Sections {
		// Section content already has markdown headers from output renderers.
		sb.WriteString(sec.Content)
		sb.WriteString("\n\n---\n\n")
	}
	return sb.String()
}

// RenderJSON produces a JSON representation of the walkthrough.
func RenderJSON(w *Walkthrough) (string, error) {
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return "", fmt.Errorf("onboard: marshal JSON: %w", err)
	}
	return string(data), nil
}
