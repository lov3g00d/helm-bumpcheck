package report

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	ansiEscape = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|[@-Z\\-_])`)
	mention    = regexp.MustCompile(`(^|[^\w@])@([A-Za-z0-9])`)
)

const (
	zwnj = '\u200c'
	zwj  = '\u200d'
)

func Sanitize(s string) string {
	s = ansiEscape.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n':
			return r
		case unicode.IsControl(r):
			return -1
		case r == zwnj || r == zwj:
			return r
		case unicode.Is(unicode.Cf, r):
			return -1
		case unicode.In(r, unicode.Zl, unicode.Zp):
			return ' '
		}
		return r
	}, s)
}

func sanitizeAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = Sanitize(l)
	}
	return out
}

func fenceFor(lines []string) string {
	longest := 0
	for _, l := range lines {
		run := 0
		for _, r := range l {
			if r == '`' {
				run++
				longest = max(longest, run)
			} else {
				run = 0
			}
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func neutralizeMentions(s string) string {
	return mention.ReplaceAllString(s, "${1}&#64;${2}")
}

func truncateMarkdown(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := strings.LastIndexByte(s[:limit], '\n')
	if cut < 0 {
		cut = 0
	}
	s = s[:cut]

	var fence string
	open := 0
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case fence != "" && t == fence:
			fence = ""
		case fence == "" && strings.HasPrefix(t, "```"):
			fence = strings.TrimRight(t, "abcdefghijklmnopqrstuvwxyz")
		case fence == "":
			open += strings.Count(t, "<details>") - strings.Count(t, "</details>")
		}
	}

	var b strings.Builder
	b.WriteString(s)
	if fence != "" {
		b.WriteString("\n" + fence)
	}
	for range max(open, 0) {
		b.WriteString("\n\n</details>")
	}
	b.WriteString("\n\n_Report truncated. Use -o json for the full result._\n")
	return b.String()
}
