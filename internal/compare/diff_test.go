package compare

import (
	"strings"
	"testing"

	"github.com/fetchdiff/fetchdiff/internal/model"
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
