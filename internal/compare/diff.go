package compare

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/fetchdiff/fetchdiff/internal/extract"
	"github.com/pmezard/go-difflib/difflib"
)

type Diff struct {
	Text       string
	Added      int
	Removed    int
	FormatNote string
}

func Build(oldContent, newContent []byte, resourceType, oldLabel, newLabel string) (Diff, error) {
	oldText, oldNote := extract.Beautify(oldContent, resourceType)
	newText, newNote := extract.Beautify(newContent, resourceType)
	note := oldNote
	if note == "" {
		note = newNote
	}
	if !bytes.Equal(oldContent, newContent) && oldText == newText {
		oldText = trailingNewline(string(oldContent))
		newText = trailingNewline(string(newContent))
		note = "Beautification normalized the only change; showing the raw diff"
	}
	text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldText),
		B:        difflib.SplitLines(newText),
		FromFile: oldLabel,
		ToFile:   newLabel,
		Context:  3,
	})
	if err != nil {
		return Diff{}, fmt.Errorf("build unified diff: %w", err)
	}
	result := Diff{Text: text, FormatNote: note}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			result.Added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			result.Removed++
		}
	}
	return result, nil
}

func trailingNewline(value string) string {
	if value == "" || strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}
