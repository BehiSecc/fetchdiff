package compare

import (
	"strings"
	"testing"

	"github.com/BehiSecc/fetchdiff/internal/model"
)

func TestBuildBeautifiedJavaScriptDiff(t *testing.T) {
	oldContent := []byte(`function value(){return 1}console.log(value());`)
	newContent := []byte(`function value(){return 2}console.log(value());`)
	diff, err := Build(oldContent, newContent, model.ResourceJavaScript, "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Added == 0 || diff.Removed == 0 {
		t.Fatalf("counts = +%d/-%d\n%s", diff.Added, diff.Removed, diff.Text)
	}
	if !strings.Contains(diff.Text, "return 2") {
		t.Fatalf("missing readable change:\n%s", diff.Text)
	}
}

func TestBuildDoesNotHideCommentOnlyChange(t *testing.T) {
	oldContent := []byte("const value=1;// old comment\n")
	newContent := []byte("const value=1;// new comment\n")
	diff, err := Build(oldContent, newContent, model.ResourceJavaScript, "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Added == 0 || diff.Removed == 0 || !strings.Contains(diff.Text, "new comment") {
		t.Fatalf("comment change was hidden:\n%s", diff.Text)
	}
}

func TestBuildCountsVeryLongLines(t *testing.T) {
	oldContent := []byte(strings.Repeat("a", 70<<10))
	newContent := []byte(strings.Repeat("b", 70<<10))
	diff, err := Build(oldContent, newContent, model.ResourceText, "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Added != 1 || diff.Removed != 1 {
		t.Fatalf("counts = +%d/-%d", diff.Added, diff.Removed)
	}
}
