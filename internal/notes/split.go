package notes

import (
	"strings"
)

type Entry struct {
	Release string
	Section string
	Text    string
}

func Split(r Release) []Entry {
	var (
		entries []Entry
		section string
		current []string
		inFence bool
	)

	flush := func() {
		if len(current) > 0 {
			entries = append(entries, Entry{Release: r.Tag, Section: section, Text: strings.Join(current, " ")})
			current = nil
		}
	}

	for _, line := range strings.Split(r.Body, "\n") {
		line = strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if trimmed != "" {
				current = append(current, trimmed)
			}
			continue
		}

		switch {
		case trimmed == "":
			flush()
		case strings.HasPrefix(trimmed, "#"):
			flush()
			section = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		case isBullet(trimmed) && !isIndented(line):
			flush()
			current = append(current, stripBullet(trimmed))
		default:
			current = append(current, stripBullet(trimmed))
		}
	}
	flush()
	return entries
}

func isIndented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func isBullet(s string) bool {
	for _, p := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	dot := strings.Index(s, ". ")
	if dot <= 0 || dot > 3 {
		return false
	}
	for _, c := range s[:dot] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func stripBullet(s string) string {
	if !isBullet(s) {
		return s
	}
	if i := strings.Index(s, " "); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

func SplitAll(releases []Release) []Entry {
	var entries []Entry
	for _, rel := range releases {
		for _, e := range Split(rel) {
			if !boilerplate(e) {
				entries = append(entries, e)
			}
		}
	}
	return entries
}

func boilerplate(e Entry) bool {
	s, t := strings.ToLower(e.Section), strings.ToLower(e.Text)
	return s == "new contributors" ||
		strings.HasPrefix(t, "**full changelog**") ||
		strings.HasPrefix(t, "full changelog") ||
		strings.Contains(t, "made their first contribution")
}
