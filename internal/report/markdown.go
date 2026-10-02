package report

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

const maxMarkdownLen = 60000

const Marker = "<!-- helm-bumpcheck -->"

var (
	lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")
	issueRef   = regexp.MustCompile(`(^|[^\w./-])([\w.-]+/[\w.-]+)#(\d+)\b`)
)

func WriteMarkdown(w io.Writer, res Report) error {
	v := build(res)
	out := markdown(v, true)
	if len(out) > maxMarkdownLen {
		out = markdown(v, false)
	}
	out = truncateMarkdown(out, maxMarkdownLen)
	_, err := io.WriteString(w, out)
	return err
}

func markdown(v view, details bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n### helm bumpcheck: %s\n\n**%s**\n", Marker, mdCode(v.Title), strings.TrimPrefix(v.Verdict, "Result: "))

	mdSection(&b, "Breaking", v.Breaking, details)
	mdSection(&b, "Check", v.Check, details)
	if len(v.Unverified) > 0 {
		fmt.Fprintf(&b, "\n<details><summary>Unverified (%d): %s</summary>\n\n", len(v.Unverified), v.Why)
		for _, r := range v.Unverified {
			fmt.Fprintf(&b, "- %s %s\n", r.Kind, mdCode(r.Path))
		}
		b.WriteString("\n</details>\n")
	}

	if len(v.Footer) > 0 {
		b.WriteString("\n")
		for _, line := range v.Footer {
			fmt.Fprintf(&b, "<sub>%s</sub><br>\n", mdText(line))
		}
	}
	return b.String()
}

func mdSection(b *strings.Builder, name string, rows []row, details bool) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "\n#### %s\n\n| | Key | Change |\n|---|---|---|\n", name)
	for _, r := range rows {
		path := ""
		if r.Path != "" {
			path = mdCodeCell(r.Path)
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", r.Kind, path, mdCell(r.Detail))
	}
	if !details {
		return
	}

	var more strings.Builder
	for _, r := range rows {
		if len(r.Lines) == 0 && len(r.Evidence) == 0 {
			continue
		}
		label := r.Kind
		if r.Path != "" {
			label = mdCode(r.Path)
		}
		fmt.Fprintf(&more, "- %s\n", label)
		if len(r.Lines) > 0 {
			fence := fenceFor(r.Lines)
			fmt.Fprintf(&more, "  %s\n", fence)
			for _, l := range r.Lines {
				fmt.Fprintf(&more, "  %s\n", l)
			}
			fmt.Fprintf(&more, "  %s\n", fence)
		}
		for _, e := range r.Evidence {
			where := e.Release
			if e.Section != "" {
				where += " " + e.Section
			}
			fmt.Fprintf(&more, "  - note %.2f, %s: %s\n", e.Probability, mdText(where), mdIssueLinks(mdText(e.Text)))
		}
		if r.More > 0 {
			fmt.Fprintf(&more, "  - %d more %s in -o json\n", r.More, plural(r.More, "note", "notes"))
		}
	}
	if more.Len() > 0 {
		fmt.Fprintf(b, "\n<details><summary>Evidence</summary>\n\n%s\n</details>\n", more.String())
	}
}

func mdCode(s string) string {
	return "`" + strings.ReplaceAll(lineBreaks.Replace(s), "`", "'") + "`"
}

func mdCodeCell(s string) string {
	return strings.ReplaceAll(mdCode(s), "|", `\|`)
}

func mdIssueLinks(s string) string {
	return issueRef.ReplaceAllString(s, "${1}[${2}#${3}](https://redirect.github.com/${2}/issues/${3})")
}

func mdCell(s string) string {
	return strings.ReplaceAll(mdText(s), "|", `\|`)
}

func mdText(s string) string {
	s = lineBreaks.Replace(s)
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "[", `\[`)
	s = strings.ReplaceAll(s, "]", `\]`)
	return neutralizeMentions(s)
}
