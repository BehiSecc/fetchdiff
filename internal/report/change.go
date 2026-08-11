package report

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/compare"
	"github.com/BehiSecc/fetchdiff/internal/model"
)

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

type Change struct {
	Previous model.Target
	Current  model.Target
	History  model.HistoryEntry
	Diff     compare.Diff
}

type pageData struct {
	Name, URL, ResourceType, Status, Checked, CheckedISO    string
	FileName, OldHash, NewHash, OldSize, NewSize, SizeDelta string
	Added, Removed, Changed, AddedPercent, RemovedPercent   int
	Rows                                                    []diffRow
}

type diffRow struct {
	Gap, Old, New, Sign, Code, Class string
}

func RenderChange(change Change) ([]byte, error) {
	rows, err := parseDiff(change.Diff.Text)
	if err != nil {
		return nil, err
	}
	checked := change.History.CheckedAt.UTC()
	data := pageData{
		Name: change.Current.Name, URL: change.Current.URL,
		ResourceType: resourceLabel(change.Current.ResourceType),
		Status:       fmt.Sprintf("%d %s", change.Current.StatusCode, http.StatusText(change.Current.StatusCode)),
		Checked:      checked.Format("02 Jan 2006, 15:04 UTC"), CheckedISO: checked.Format(time.RFC3339),
		FileName: resourceName(change.Current.URL), OldHash: shortHash(change.Previous.SnapshotHash),
		NewHash: shortHash(change.Current.SnapshotHash), OldSize: humanBytes(change.Previous.SnapshotSize),
		NewSize: humanBytes(change.Current.SnapshotSize), SizeDelta: signedBytes(change.Current.SnapshotSize - change.Previous.SnapshotSize),
		Added: change.Diff.Added, Removed: change.Diff.Removed, Changed: change.Diff.Added + change.Diff.Removed,
		Rows: rows,
	}
	if data.Changed > 0 {
		data.AddedPercent = change.Diff.Added * 100 / data.Changed
		data.RemovedPercent = 100 - data.AddedPercent
	}
	var output bytes.Buffer
	if err := changeTemplate.Execute(&output, data); err != nil {
		return nil, fmt.Errorf("render HTML change report: %w", err)
	}
	return output.Bytes(), nil
}

func Filename(target model.Target, checked time.Time) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, target.Name)
	name = strings.Trim(name, "-")
	if name == "" {
		name = "target"
	}
	return fmt.Sprintf("fetchdiff-%s-%s.html", name, checked.UTC().Format("20060102T150405Z"))
}

func parseDiff(value string) ([]diffRow, error) {
	var rows []diffRow
	oldLine, newLine := 0, 0
	seenHunk := false
	for _, line := range strings.Split(strings.TrimSuffix(value, "\n"), "\n") {
		if match := hunkHeader.FindStringSubmatch(line); match != nil {
			oldStart, _ := strconv.Atoi(match[1])
			newStart, _ := strconv.Atoi(match[2])
			if seenHunk {
				rows = append(rows, diffRow{Gap: "··· unchanged context ···"})
			}
			oldLine, newLine, seenHunk = oldStart, newStart, true
			continue
		}
		if !seenHunk || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") || line == `\ No newline at end of file` {
			continue
		}
		row := diffRow{}
		switch {
		case strings.HasPrefix(line, "+"):
			row.New, row.Sign, row.Code, row.Class = strconv.Itoa(newLine), "+", strings.TrimPrefix(line, "+"), "addition"
			newLine++
		case strings.HasPrefix(line, "-"):
			row.Old, row.Sign, row.Code, row.Class = strconv.Itoa(oldLine), "−", strings.TrimPrefix(line, "-"), "deletion"
			oldLine++
		default:
			row.Old, row.New, row.Sign, row.Code = strconv.Itoa(oldLine), strconv.Itoa(newLine), " ", strings.TrimPrefix(line, " ")
			oldLine++
			newLine++
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("render HTML change report: diff has no displayable lines")
	}
	return rows, nil
}

func resourceName(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil {
		if value := path.Base(parsed.Path); value != "." && value != "/" && value != "" {
			return value
		}
	}
	return "response"
}
func resourceLabel(value string) string {
	switch value {
	case model.ResourceJavaScript:
		return "JavaScript bundle"
	case model.ResourceHTML:
		return "HTML page"
	default:
		return "Text resource"
	}
}
func shortHash(value string) string {
	if len(value) > 7 {
		return value[:7]
	}
	return value
}
func humanBytes(value int64) string {
	const unit = int64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	size, suffix := float64(value), "KB"
	size /= 1024
	if size >= 1024 {
		size /= 1024
		suffix = "MB"
	}
	return fmt.Sprintf("%.1f %s", size, suffix)
}
func signedBytes(value int64) string {
	if value >= 0 {
		return "+" + humanBytes(value)
	}
	return "−" + humanBytes(-value)
}

var changeTemplate = template.Must(template.New("change").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><title>FetchDiff · {{.Name}}</title>
<style>
:root{--canvas:#f3f1ff;--ink:#191724;--muted:#6c687d;--paper:#fffefd;--line:#ddd9ef;--violet:#6658ed;--violet-dark:#4033c7;--coral:#ff665c;--coral-soft:#ffe8e4;--mint:#18a768;--mint-soft:#dff9e9;--lime:#ddff76;--shadow:0 24px 70px rgba(49,37,109,.10);--display:"Avenir Next","URW Gothic","Segoe UI Variable Display","Helvetica Neue",sans-serif;--sans:"Avenir Next","Segoe UI Variable Text","Helvetica Neue","Nimbus Sans",Arial,sans-serif;--mono:"Noto Sans Mono","SFMono-Regular","Cascadia Code","DejaVu Sans Mono",Consolas,monospace}*{box-sizing:border-box}body{min-height:100vh;margin:0;background:radial-gradient(circle at 10% 2%,rgba(255,213,99,.38),transparent 24%),radial-gradient(circle at 100% 26%,rgba(102,88,237,.18),transparent 30%),var(--canvas);color:var(--ink);font:14px/1.5 var(--sans);-webkit-font-smoothing:antialiased}a{color:inherit}.page{width:min(1240px,calc(100% - 36px));margin:0 auto;padding:28px 0 48px}.nav{display:flex;align-items:center;justify-content:space-between;margin-bottom:24px}.logo{font:850 23px var(--display);letter-spacing:-.045em;text-decoration:none}.logo:hover{color:var(--violet-dark)}.nav-meta{color:var(--muted);font-size:12px}.hero{position:relative;overflow:hidden;display:grid;grid-template-columns:minmax(0,1.45fr) minmax(300px,.55fr);min-height:340px;border:1px solid rgba(64,51,199,.25);border-radius:34px;background:var(--violet);color:#fff;box-shadow:var(--shadow)}.hero:after{position:absolute;width:340px;height:340px;right:-105px;bottom:-170px;border:55px solid rgba(221,255,118,.75);border-radius:50%;content:""}.hero-main{position:relative;z-index:1;display:flex;flex-direction:column;justify-content:space-between;padding:34px 38px}.status-row{display:flex;align-items:center;gap:10px}.pill{display:inline-flex;align-items:center;gap:7px;padding:7px 12px;border:1px solid rgba(255,255,255,.35);border-radius:999px;background:rgba(255,255,255,.13);font-size:11px;font-weight:800;letter-spacing:.075em;text-transform:uppercase}.pill:before{width:7px;height:7px;border-radius:50%;background:var(--lime);content:""}.type{color:rgba(255,255,255,.68);font-size:12px;font-weight:650}h1{max-width:760px;margin:22px 0 10px;font:850 clamp(43px,7vw,78px)/.95 var(--display);letter-spacing:-.065em}.url{max-width:760px;color:rgba(255,255,255,.75);font:14px/1.55 var(--mono);text-decoration:none;overflow-wrap:anywhere}.url:hover{color:#fff;text-decoration:underline}.hero-foot{display:flex;flex-wrap:wrap;gap:22px;margin-top:32px;color:rgba(255,255,255,.78);font-size:14px}.hero-foot strong{color:#fff}.hero-side{position:relative;z-index:1;display:grid;place-items:center;min-height:300px;border-left:1px solid rgba(255,255,255,.19);background:rgba(45,35,159,.28)}.change-orb{display:grid;width:210px;height:210px;place-content:center;border:1px solid rgba(25,23,36,.28);border-radius:48% 52% 58% 42%/45% 44% 56% 55%;background:var(--lime);color:var(--ink);text-align:center;box-shadow:12px 14px 0 rgba(25,23,36,.34);transform:rotate(-4deg)}.change-orb strong{display:block;font-size:58px;line-height:.95;letter-spacing:-.07em}.change-orb span{display:block;margin-top:8px;font-size:11px;font-weight:850;letter-spacing:.09em;text-transform:uppercase}.bento{display:grid;grid-template-columns:1.1fr .9fr .9fr 1.1fr;gap:14px;margin-top:14px}.card{min-width:0;padding:21px 22px;border:1px solid var(--line);border-radius:22px;background:var(--paper)}.card-label{display:block;margin-bottom:9px;color:var(--muted);font-size:10px;font-weight:800;letter-spacing:.09em;text-transform:uppercase}.card-value{display:block;overflow:hidden;font:820 20px var(--display);letter-spacing:-.035em;text-overflow:ellipsis;white-space:nowrap}.card small{display:block;margin-top:5px;color:var(--muted);font-size:11px}.card.coral{background:var(--coral-soft);border-color:#ffc8c2}.card.mint{background:var(--mint-soft);border-color:#bdebcf}.card.yellow{background:#fff5ce;border-color:#f2dfa4}.delta-bar{display:flex;height:7px;margin-top:14px;overflow:hidden;border-radius:999px;background:#e5e0ef}.delta-bar .plus{width:{{.AddedPercent}}%;background:var(--mint)}.delta-bar .minus{width:{{.RemovedPercent}}%;background:var(--coral)}.content{display:grid;grid-template-columns:260px minmax(0,1fr);gap:18px;margin-top:18px;align-items:start}.rail{position:sticky;top:18px;padding:22px;border:1px solid var(--line);border-radius:24px;background:var(--paper)}.rail h2{margin:0 0 18px;font:700 18px var(--display);letter-spacing:-.025em}.timeline{position:relative;display:grid;gap:19px}.timeline:before{position:absolute;inset:7px auto 7px 5px;width:2px;background:var(--line);content:""}.timeline-item{position:relative;padding-left:24px}.timeline-item:before{position:absolute;top:4px;left:0;width:12px;height:12px;border:3px solid var(--paper);border-radius:50%;background:var(--violet);box-shadow:0 0 0 1px var(--line);content:""}.timeline-item.active:before{background:var(--mint)}.timeline-item strong{display:block;font-size:12px}.timeline-item span{display:block;margin-top:2px;color:var(--muted);font:10px/1.4 var(--mono)}.hashes{margin-top:22px;padding-top:18px;border-top:1px solid var(--line)}.hash-line{display:flex;justify-content:space-between;gap:10px;margin-top:8px;color:var(--muted);font-size:10px}.hash-line code{color:var(--ink);font:10px var(--mono)}.changes{min-width:0}.changes-card{overflow:hidden;border:1px solid var(--line);border-radius:26px;background:var(--paper);box-shadow:0 14px 42px rgba(49,37,109,.055)}.changes-head{display:flex;align-items:center;justify-content:space-between;gap:18px;padding:20px 22px;border-bottom:1px solid var(--line)}.changes-title strong{display:block;font:700 18px var(--display);letter-spacing:-.025em}.changes-title span{display:block;margin-top:3px;color:var(--muted);font:11px var(--mono)}.change-count{display:inline-flex;gap:8px;padding:8px 12px;border-radius:999px;background:#f1eef8;font-size:11px;font-weight:800;white-space:nowrap}.change-count .plus{color:var(--mint)}.change-count .minus{color:var(--coral)}.diff-body{overflow:hidden}.diff-row{display:grid;grid-template-columns:48px 48px 28px minmax(0,1fr);min-width:0;border-bottom:1px solid rgba(221,217,239,.65);color:#2d293c;font:12px/1.65 var(--mono)}.diff-row:last-child{border-bottom:0}.line-no{padding:3px 8px;border-right:1px solid var(--line);background:#faf9fd;color:#9a94a8;text-align:right;user-select:none}.sign{padding:3px 6px;font-weight:900;text-align:center;user-select:none}.diff-row code{min-width:0;padding:3px 14px 3px 3px;overflow-wrap:anywhere;white-space:pre-wrap;font:inherit}.addition{background:rgba(24,167,104,.12)}.deletion{background:rgba(255,102,92,.12)}.addition .sign{color:var(--mint)}.deletion .sign{color:var(--coral)}.context-gap{padding:9px 14px;border-bottom:1px solid var(--line);background:#f8f6fd;color:var(--muted);font:10px var(--mono);text-align:center;letter-spacing:.04em}.footer{display:flex;justify-content:space-between;gap:18px;margin-top:20px;padding:4px 6px;color:var(--muted);font-size:11px}.footer strong{color:var(--ink)}.footer a{text-decoration:none}.footer a:hover{color:var(--violet-dark);text-decoration:underline}@media(max-width:930px){.hero{grid-template-columns:1fr}.hero-side{min-height:250px;border-top:1px solid rgba(255,255,255,.19);border-left:0}.bento{grid-template-columns:repeat(2,1fr)}.content{grid-template-columns:1fr}.rail{position:static}.timeline{grid-template-columns:repeat(3,1fr)}.timeline:before{display:none}.timeline-item{padding:0 0 0 19px}.hashes{display:grid;grid-template-columns:1fr 1fr;gap:0 16px}}@media(max-width:700px){.page{width:min(100% - 16px,1240px);padding-top:14px}.nav-meta{font-size:10px}.hero{border-radius:26px}.hero-main{padding:26px 22px}.hero-side{min-height:230px}.change-orb{width:170px;height:170px}.change-orb strong{font-size:48px}.bento{gap:8px;margin-top:8px}.card{padding:16px;border-radius:18px}.card-value{font-size:17px}.content{margin-top:10px;gap:10px}.rail{border-radius:20px}.timeline{grid-template-columns:1fr}.changes-card{border-radius:20px}.changes-head{padding:17px 16px}.diff-row{grid-template-columns:36px 36px 24px minmax(0,1fr);font-size:11px}.line-no{padding-inline:5px}.diff-row code{padding-right:9px}.footer{flex-direction:column}}@media print{body{background:#fff;print-color-adjust:exact;-webkit-print-color-adjust:exact}.page{width:100%;padding:0}.hero,.changes-card{box-shadow:none}.rail{position:static}}
</style></head><body><main class="page"><nav class="nav"><a class="logo" href="https://github.com/BehiSecc/fetchdiff">FetchDiff</a><time class="nav-meta" datetime="{{.CheckedISO}}">{{.Checked}}</time></nav>
<header class="hero"><div class="hero-main"><div><div class="status-row"><span class="pill">Changed</span><span class="type">{{.ResourceType}}</span></div><h1>{{.Name}}</h1><a class="url" href="{{.URL}}">{{.URL}}</a></div><div class="hero-foot"><span><strong>{{.Status}}</strong> response</span><span><strong>{{.Changed}}</strong> changed lines</span><span>complete offline report</span></div></div><div class="hero-side"><div class="change-orb"><strong>+{{.Added}}</strong><span>lines added</span></div></div></header>
<section class="bento"><div class="card coral"><span class="card-label">Removed</span><span class="card-value">−{{.Removed}} lines</span><small>{{.RemovedPercent}}% of changed lines</small><div class="delta-bar"><span class="plus"></span><span class="minus"></span></div></div><div class="card mint"><span class="card-label">New size</span><span class="card-value">{{.NewSize}}</span><small>{{.SizeDelta}} from previous</small></div><div class="card yellow"><span class="card-label">Current hash</span><span class="card-value">{{.NewHash}}</span><small>SHA-256 snapshot</small></div><div class="card"><span class="card-label">Last changed</span><span class="card-value">{{.Checked}}</span><small>Latest verified snapshot</small></div></section>
<div class="content"><aside class="rail"><h2>Change journey</h2><div class="timeline"><div class="timeline-item"><strong>Previous snapshot</strong><span>{{.OldHash}} · {{.OldSize}}</span></div><div class="timeline-item"><strong>Resource fetched</strong><span>{{.Status}}</span></div><div class="timeline-item active"><strong>Change verified</strong><span>{{.NewHash}} · {{.NewSize}}</span></div></div><div class="hashes"><div class="hash-line"><span>Previous</span><code>{{.OldHash}}</code></div><div class="hash-line"><span>Current</span><code>{{.NewHash}}</code></div></div></aside>
<section class="changes"><article class="changes-card"><header class="changes-head"><div class="changes-title"><strong>Full changes</strong><span>{{.FileName}} · {{.OldHash}} → {{.NewHash}}</span></div><span class="change-count"><span class="plus">+{{.Added}}</span><span class="minus">−{{.Removed}}</span></span></header><div class="diff-body" role="table" aria-label="Unified change diff">{{range .Rows}}{{if .Gap}}<div class="context-gap">{{.Gap}}</div>{{else}}<div class="diff-row {{.Class}}" role="row"><span class="line-no">{{.Old}}</span><span class="line-no">{{.New}}</span><span class="sign">{{.Sign}}</span><code>{{.Code}}</code></div>{{end}}{{end}}</div></article></section></div>
<footer class="footer"><span>Generated by <a href="https://github.com/BehiSecc/fetchdiff"><strong>FetchDiff</strong></a> · complete offline report</span><span>Checked {{.Checked}}</span></footer></main></body></html>`))
