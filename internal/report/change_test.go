package report

import (
	"strings"
	"testing"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/compare"
	"github.com/BehiSecc/fetchdiff/internal/model"
)

func TestRenderChangeIsOfflineEscapedAndContinuous(t *testing.T) {
	checked := time.Date(2026, 8, 11, 9, 42, 0, 0, time.UTC)
	diff := compare.Diff{Text: "--- old\n+++ new\n@@ -1,2 +1,2 @@\n-const value = 1;\n+const value = `<script>alert(1)</script>`;\n same();\n@@ -20 +20 @@\n-old();\n+new();\n", Added: 2, Removed: 2}
	content, err := RenderChange(Change{
		Previous: model.Target{Name: "demo", URL: "https://example.com/app.js", SnapshotHash: strings.Repeat("a", 64), SnapshotSize: 100, ResourceType: model.ResourceJavaScript, StatusCode: 200},
		Current:  model.Target{Name: "demo", URL: "https://example.com/app.js", SnapshotHash: strings.Repeat("b", 64), SnapshotSize: 120, ResourceType: model.ResourceJavaScript, StatusCode: 200},
		History:  model.HistoryEntry{CheckedAt: checked}, Diff: diff,
	})
	if err != nil {
		t.Fatal(err)
	}
	html := string(content)
	for _, expected := range []string{"FetchDiff", "JavaScript bundle", "Full changes", `class="context-gap"`, `class="sign">&#43;`, "&lt;script&gt;alert(1)&lt;/script&gt;"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("report is missing %q", expected)
		}
	}
	if strings.Count(html, `class="changes-card"`) != 1 {
		t.Fatalf("report split changes into multiple cards")
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "http://") || strings.Contains(html, "https://fonts") {
		t.Fatal("report contains executable or remote resources")
	}
}

func TestFilenameIsSafe(t *testing.T) {
	got := Filename(model.Target{Name: "Production JS / primary"}, time.Date(2026, 8, 11, 9, 42, 0, 0, time.UTC))
	if got != "fetchdiff-Production-JS---primary-20260811T094200Z.html" {
		t.Fatalf("filename = %q", got)
	}
}
