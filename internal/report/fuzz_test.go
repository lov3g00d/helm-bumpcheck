package report

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func FuzzSanitize(f *testing.F) {
	for _, s := range []string{
		"zero\u200bwidth", "\ufeffbom", "a\u202eb\u202cc", "\U0001F469\u200d\U0001F4BB", "a\u2028b", "a\tb\nc",
		"a\x00b\x7fc\u0085d", "fix \x1b]8;;https://evil\x07click\x1b]8;;\x07 \x1b[31mred\x1b[0m", "\x1b\x1b[[31m", "\xff\xfe",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Sanitize(s)
		if !utf8.ValidString(got) {
			t.Fatalf("Sanitize(%q) = %q, not valid UTF-8", s, got)
		}
		if again := Sanitize(got); again != got {
			t.Fatalf("Sanitize is not idempotent on %q: %q then %q", s, got, again)
		}
		for _, r := range got {
			switch {
			case r == '\t' || r == '\n' || r == zwnj || r == zwj:
			case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.In(r, unicode.Zl, unicode.Zp):
				t.Fatalf("Sanitize(%q) = %q, keeps %U", s, got, r)
			}
		}
	})
}

func FuzzTruncateMarkdown(f *testing.F) {
	f.Add("<details><summary>x</summary>\n\n```\n"+strings.Repeat("é line\n", 100), 200)
	f.Add("````go\n```\nstill code\n````\n<details>\n<details>\ntext\n</details>\n", 30)
	f.Add("short", 100)
	f.Fuzz(func(t *testing.T, s string, limit int) {
		if limit < 0 || limit > 1<<16 {
			t.Skip()
		}
		got := truncateMarkdown(s, limit)
		if len(s) <= limit {
			if got != s {
				t.Fatalf("truncateMarkdown changed input within the limit: %q -> %q", s, got)
			}
			return
		}
		cut := max(strings.LastIndexByte(s[:limit], '\n'), 0)
		if !strings.HasPrefix(got, s[:cut]) {
			t.Fatalf("truncateMarkdown(%q, %d) = %q, does not keep the input up to the last line break", s, limit, got)
		}
		if utf8.ValidString(s) && !utf8.ValidString(got) {
			t.Fatalf("truncateMarkdown(%q, %d) = %q, not valid UTF-8", s, limit, got)
		}
		if fence, open := markdownBlocks(got); fence != "" || open > 0 {
			t.Fatalf("truncateMarkdown(%q, %d) = %q leaves fence %q and %d <details> open", s, limit, got, fence, open)
		}
	})
}

func markdownBlocks(s string) (string, int) {
	var fence string
	open := 0
	for line := range strings.SplitSeq(s, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case fence != "" && len(t) >= len(fence) && strings.Trim(t, "`") == "":
			fence = ""
		case fence == "" && strings.HasPrefix(t, "```"):
			fence = t[:len(t)-len(strings.TrimLeft(t, "`"))]
		case fence == "":
			open += strings.Count(t, "<details>") - strings.Count(t, "</details>")
		}
	}
	return fence, open
}
