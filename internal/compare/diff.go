package compare

import (
	"bufio"
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
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			result.Added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			result.Removed++
		}
	}
	if err := scanner.Err(); err != nil {
		return Diff{}, fmt.Errorf("count diff lines: %w", err)
	}
	return result, nil
}
