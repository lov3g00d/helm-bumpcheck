package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Batch struct {
	Base    string   `json:"base"`
	Head    string   `json:"head"`
	Reports []Report `json:"reports"`
	Skipped []Skip   `json:"skipped"`
	Failed  []Skip   `json:"failed"`
}

func WriteBatch(w io.Writer, format string, b Batch) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(b)
	case "markdown":
		return writeBatchMarkdown(w, b)
	case "text":
		return writeBatchText(w, b)
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
}

func batchSummary(b Batch) string {
	if len(b.Reports) == 0 {
		return fmt.Sprintf("No chart versions changed between %s and %s.", short(b.Base), short(b.Head))
	}
	return fmt.Sprintf("%d %s checked between %s and %s.", len(b.Reports), plural(len(b.Reports), "report", "reports"), short(b.Base), short(b.Head))
}

func writeBatchText(w io.Writer, b Batch) error {
	if _, err := fmt.Fprintln(w, batchSummary(b)); err != nil {
		return err
	}
	for _, r := range b.Reports {
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		if err := WriteText(w, r); err != nil {
			return err
		}
	}
	var sb strings.Builder
	for _, group := range []struct {
		name  string
		items []Skip
	}{{"Failed", b.Failed}, {"Skipped", b.Skipped}} {
		if len(group.items) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\n%s\n", group.name)
		for _, s := range group.items {
			fmt.Fprintf(&sb, "  %s: %s\n", sanitize(s.Path), sanitize(s.Reason))
		}
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

func writeBatchMarkdown(w io.Writer, b Batch) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n## helm bumpcheck\n\n%s\n", Marker, mdText(batchSummary(b)))
	if len(b.Reports) > 0 {
		sb.WriteString("\n| Chart | Values | Result |\n|---|---|---|\n")
		for _, r := range b.Reports {
			v := build(r)
			fmt.Fprintf(&sb, "| %s %s -&gt; %s | %s | %s |\n", mdCode(r.Chart), r.From, r.To, mdCode(r.Values), strings.TrimPrefix(v.Verdict, "Result: "))
		}
	}

	details := true
	for {
		var body strings.Builder
		for _, r := range b.Reports {
			body.WriteString("\n---\n\n")
			body.WriteString(strings.TrimPrefix(markdown(build(r), details), Marker+"\n"))
		}
		if len(b.Failed) > 0 {
			body.WriteString("\n**Could not be analyzed**\n\n")
			for _, s := range b.Failed {
				fmt.Fprintf(&body, "- %s: %s\n", mdCode(s.Path), mdText(s.Reason))
			}
		}
		if len(b.Skipped) > 0 {
			body.WriteString("\n<details><summary>Skipped</summary>\n\n")
			for _, s := range b.Skipped {
				fmt.Fprintf(&body, "- %s: %s\n", mdCode(s.Path), mdText(s.Reason))
			}
			body.WriteString("\n</details>\n")
		}
		if sb.Len()+body.Len() <= maxMarkdownLen || !details {
			sb.WriteString(body.String())
			break
		}
		details = false
	}

	_, err := io.WriteString(w, truncateMarkdown(sb.String(), maxMarkdownLen))
	return err
}

func short(rev string) string {
	if len(rev) == 40 {
		return rev[:12]
	}
	return rev
}
