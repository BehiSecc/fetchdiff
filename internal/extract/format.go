package extract

import (
	"bytes"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/BehiSecc/fetchdiff/internal/model"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/yosssi/gohtml"
)

func ResourceType(rawURL, contentType string) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	mediaType = strings.ToLower(mediaType)
	if strings.Contains(mediaType, "javascript") || strings.Contains(mediaType, "ecmascript") {
		return model.ResourceJavaScript
	}
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		return model.ResourceHTML
	}
	parsed, _ := url.Parse(rawURL)
	extension := strings.ToLower(path.Ext(parsed.Path))
	switch extension {
	case ".js", ".mjs", ".cjs", ".jsx":
		return model.ResourceJavaScript
	case ".html", ".htm":
		return model.ResourceHTML
	default:
		return model.ResourceText
	}
}

func Beautify(content []byte, resourceType string) (string, string) {
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return fmt.Sprintf("<binary content: %d bytes>\n", len(content)), "content is not valid text; showing metadata-only representation"
	}
	raw := string(content)
	switch resourceType {
	case model.ResourceJavaScript:
		result := api.Transform(raw, api.TransformOptions{
			Loader:        api.LoaderJS,
			Target:        api.ESNext,
			Charset:       api.CharsetUTF8,
			LegalComments: api.LegalCommentsInline,
			LogLevel:      api.LogLevelSilent,
		})
		if len(result.Errors) == 0 {
			return ensureTrailingNewline(string(result.Code)), ""
		}
		return ensureTrailingNewline(raw), "JavaScript formatting failed; showing the raw diff"
	case model.ResourceHTML:
		return ensureTrailingNewline(gohtml.Format(raw)), ""
	default:
		return ensureTrailingNewline(raw), ""
	}
}

func ensureTrailingNewline(value string) string {
	if value == "" || strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}
