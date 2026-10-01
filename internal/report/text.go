package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const (
	maxValueLen = 60
	maxEntryLen = 120
)

func WriteJSON(w io.Writer, res Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func WriteText(w io.Writer, res Report) error {
	r := res.Report
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s -> %s\n", res.Chart, res.From, res.To)

	changed := make([]string, len(r.DefaultChanged))
	for i, c := range r.DefaultChanged {
		changed[i] = describeChange(c)
	}

	typed := make([]string, len(r.TypeChanged))
	for i, tc := range r.TypeChanged {
		typed[i] = fmt.Sprintf("%s: %s -> %s (%d %s you set)", tc.Path, tc.Old, tc.New, len(tc.Keys), plural(len(tc.Keys), "key", "keys"))
	}

	section(&b, "Removed", "keys you set that the new chart no longer declares", r.Removed)
	section(&b, "Type changed", "blocks you set whose type changed in the new chart", typed)
	section(&b, "Default changed", "keys you rely on whose default changed", changed)
	section(&b, "Free-form", "keys inside open blocks, not checked against the chart", r.Freeform)
	section(&b, "Undeclared", "keys the chart never declared", r.Undeclared)

	if len(r.Removed)+len(typed)+len(changed)+len(r.Freeform)+len(r.Undeclared) == 0 {
		b.WriteString("\nNo changes affect the values you set.\n")
	}
	if len(r.Added) > 0 {
		fmt.Fprintf(&b, "\n%d new keys in the target version.\n", len(r.Added))
	}
	if res.Notes != nil {
		writeNotes(&b, res.Notes)
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func writeNotes(b *strings.Builder, n *Notes) {
	if n.Skipped != "" {
		fmt.Fprintf(b, "\nRelease notes: skipped, %s\n", n.Skipped)
		return
	}
	fmt.Fprintf(b, "\nRelease notes: %s %s -> %s (%d releases, %d entries, %d questions)\n",
		n.Repo, n.From, n.To, n.Releases, n.Entries, n.Stats.Questions)

	groups := groupByKey(n.Matches, n.Threshold)
	if len(groups) == 0 {
		fmt.Fprintf(b, "  No entries at or above %.2f.\n", n.Threshold)
		return
	}
	for _, g := range groups {
		writeKeyHits(b, g)
	}
}

func section(b *strings.Builder, name, help string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s (%d): %s\n", name, len(lines), help)
	for _, l := range lines {
		fmt.Fprintf(b, "  %s\n", l)
	}
}

func describeChange(c values.Change) string {
	oldS, oldOK := c.Old.(string)
	newS, newOK := c.New.(string)
	if oldOK && newOK && (strings.Contains(oldS, "\n") || strings.Contains(newS, "\n")) {
		oldLines, newLines := strings.Split(oldS, "\n"), strings.Split(newS, "\n")
		for i := range max(len(oldLines), len(newLines)) {
			a, b := lineAt(oldLines, i), lineAt(newLines, i)
			if a != b {
				return fmt.Sprintf("%s: line %d: %s -> %s", c.Path, i+1, render(a), render(b))
			}
		}
	}
	return fmt.Sprintf("%s: %s -> %s", c.Path, render(c.Old), render(c.New))
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func render(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return truncate(string(data), maxValueLen)
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
