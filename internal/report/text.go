package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const (
	maxValueLen = 60
	maxEntryLen = 120
	maxPathCol  = 48
	kindCol     = 12
)

type style struct{ on bool }

func (s style) wrap(code, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) bold(t string) string   { return s.wrap("1", t) }
func (s style) red(t string) string    { return s.wrap("31", t) }
func (s style) yellow(t string) string { return s.wrap("33", t) }
func (s style) dim(t string) string    { return s.wrap("2", t) }

func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func WriteJSON(w io.Writer, res Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func WriteText(w io.Writer, res Report) error {
	v := build(res)
	st := style{on: colorEnabled(w)}
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n%s\n", st.bold(v.Title), v.Verdict)
	writeSection(&b, st, st.red, "BREAKING", "", v.Breaking)
	writeSection(&b, st, st.yellow, "CHECK", "", v.Check)
	writeSection(&b, st, st.dim, "UNVERIFIED", v.Why, v.Unverified)

	if len(v.Footer) > 0 {
		b.WriteString("\n")
		for _, line := range v.Footer {
			fmt.Fprintf(&b, "%s\n", st.dim(line))
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func writeSection(b *strings.Builder, st style, color func(string) string, name, why string, rows []row) {
	if len(rows) == 0 {
		return
	}
	header := st.bold(name)
	if why != "" {
		header += "  " + st.dim(why)
	}
	fmt.Fprintf(b, "\n%s\n", header)

	width := 0
	for _, r := range rows {
		width = max(width, min(len(r.Path), maxPathCol))
	}
	indent := strings.Repeat(" ", 2+kindCol+2)

	for _, r := range rows {
		line := "  " + color(pad(r.Kind, kindCol))
		switch {
		case r.Path != "" && r.Detail != "":
			line += "  " + pad(r.Path, width) + "  " + r.Detail
		case r.Path != "":
			line += "  " + r.Path
		default:
			line += "  " + r.Detail
		}
		fmt.Fprintf(b, "%s\n", strings.TrimRight(line, " "))

		for _, l := range r.Lines {
			fmt.Fprintf(b, "%s%s\n", indent, l)
		}
		for _, e := range r.Evidence {
			where := e.Release
			if e.Section != "" {
				where += " " + e.Section
			}
			fmt.Fprintf(b, "%s%s\n", indent, st.dim(fmt.Sprintf("note %.2f  %s: %s", e.Probability, where, truncate(e.Text, maxEntryLen))))
		}
		if r.More > 0 {
			fmt.Fprintf(b, "%s%s\n", indent, st.dim(fmt.Sprintf("+ %d more %s", r.More, plural(r.More, "note", "notes"))))
		}
	}
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func describeChange(c values.Change) string {
	oldS, oldOK := c.Old.(string)
	newS, newOK := c.New.(string)
	if oldOK && newOK && (strings.Contains(oldS, "\n") || strings.Contains(newS, "\n")) {
		oldLines, newLines := strings.Split(oldS, "\n"), strings.Split(newS, "\n")
		for i := range max(len(oldLines), len(newLines)) {
			a, b := lineAt(oldLines, i), lineAt(newLines, i)
			if a != b {
				return fmt.Sprintf("line %d: %s -> %s", i+1, render(a), render(b))
			}
		}
	}
	return fmt.Sprintf("%s -> %s", render(c.Old), render(c.New))
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func render(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return truncate(strings.TrimSpace(buf.String()), maxValueLen)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
