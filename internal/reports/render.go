package reports

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed report.tmpl
var reportTmpl string

// page is what the template renders. Typed blocks pass through as data —
// html/template's contextual escaping handles every model-supplied string —
// and only two things enter as trusted markup: the chart SVG this package
// generates, and freehand sections that ValidateFreehand froze at store time.
type page struct {
	Title   string
	Project string
	Origin  string
	Created string
	ID      string
	CSS     template.CSS
	Blocks  []Block
}

// diffLine is one classified row of a diff block.
type diffLine struct {
	Class string
	Text  string
}

var tmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"chart":      func(b Block) template.HTML { return template.HTML(renderChart(b)) },
	"freehand":   func(s string) template.HTML { return template.HTML(s) },
	"diffLines":  splitDiff,
	"paragraphs": splitParagraphs,
	"seriesClass": func(i int) string {
		return fmt.Sprintf("s%d", i%8+1)
	},
	"treeIndent": func(depth int) int { return depth * 16 },
	// The sources block draws in two runs — what was read, and what was
	// cited and never read — so the split is made here rather than with a
	// condition inside the range: a template that filtered as it drew would
	// walk the list twice to find out whether the second run has anything
	// in it at all.
	"sourcesRead":   func(list []Source) []Source { return sourcesWhere(list, true) },
	"sourcesUnread": func(list []Source) []Source { return sourcesWhere(list, false) },
}).Parse(reportTmpl))

// sourcesWhere is the run of a sources block that was read, or the run that
// was not.
func sourcesWhere(list []Source, read bool) []Source {
	var out []Source
	for _, s := range list {
		if s.Read == read {
			out = append(out, s)
		}
	}
	return out
}

// Render draws one stored report as a complete page under the current
// template and tokens. Freehand blocks must already hold validated markup;
// Render is the trust boundary's inside.
func Render(doc Document, meta Meta, id string) ([]byte, error) {
	p := page{
		Title:   doc.Title,
		Project: meta.Project,
		Origin:  meta.Origin,
		Created: meta.Created.Local().Format("2006-01-02 15:04"),
		ID:      id,
		CSS:     template.CSS(reportCSS),
		Blocks:  doc.Blocks,
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, p); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// splitDiff classifies unified-diff lines for the diff block. It is a
// renderer, not a parser: an odd line is context, never an error.
func splitDiff(d string) []diffLine {
	var out []diffLine
	for _, line := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
		class := "ctx"
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "diff "):
			class = "file"
		case strings.HasPrefix(line, "@@"):
			class = "hunk"
		case strings.HasPrefix(line, "+"):
			class = "add"
		case strings.HasPrefix(line, "-"):
			class = "del"
		}
		out = append(out, diffLine{Class: class, Text: line})
	}
	return out
}

func splitParagraphs(text string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
