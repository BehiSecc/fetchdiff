package extract

import (
	"strings"
	"testing"

	"github.com/BehiSecc/fetchdiff/internal/model"
)

func TestBeautifyMinifiedJavaScript(t *testing.T) {
	formatted, note := Beautify([]byte(`function sum(a,b){return a+b}console.log(sum(1,2));`), model.ResourceJavaScript)
	if note != "" {
		t.Fatal(note)
	}
	if strings.Count(formatted, "\n") < 3 {
		t.Fatalf("JavaScript was not expanded:\n%s", formatted)
	}
}

func TestBeautifyMinifiedHTML(t *testing.T) {
	formatted, _ := Beautify([]byte(`<html><body><main><h1>Hello</h1></main></body></html>`), model.ResourceHTML)
	if strings.Count(formatted, "\n") < 4 {
		t.Fatalf("HTML was not expanded:\n%s", formatted)
	}
}

func TestResourceType(t *testing.T) {
	if got := ResourceType("https://example.com/app", "application/javascript; charset=utf-8"); got != model.ResourceJavaScript {
		t.Fatalf("type = %q", got)
	}
	if got := ResourceType("https://example.com/page.html", "text/plain"); got != model.ResourceHTML {
		t.Fatalf("type = %q", got)
	}
}
