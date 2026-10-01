package cli

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/lov3g00d/helm-bumpcheck/internal/match"
)

const entriesPerKey = 3

var (
	mdLink      = regexp.MustCompile(`\[([^\]]+)\]\(https?://[^)]+\)`)
	githubRef   = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/(?:pull|issues)/(\d+)\S*`)
	anyURL      = regexp.MustCompile(`https?://\S+`)
	attribution = regexp.MustCompile(`\s+by @[\w-]+(?:\[bot\])?(?:\s+in\s+.*)?$`)
	spaces      = regexp.MustCompile(`\s+`)
)

type keyHits struct {
	key     string
	best    float64
	notable string
	hits    []match.Match
}

func notableTag(section string) string {
	s := strings.ToLower(section)
	switch {
	case strings.Contains(s, "breaking"):
		return "BREAKING"
	case strings.Contains(s, "deprecat"):
		return "DEPRECATION"
	}
	return ""
}

func groupByKey(matches []match.Match, threshold float64) []keyHits {
	index := map[string]int{}
	var groups []keyHits
	for _, m := range matches {
		if m.Probability < threshold {
			continue
		}
		i, ok := index[m.Key]
		if !ok {
			i = len(groups)
			index[m.Key] = i
			groups = append(groups, keyHits{key: m.Key})
		}
		g := &groups[i]
		g.hits = append(g.hits, m)
		g.best = max(g.best, m.Probability)
		if g.notable == "" {
			g.notable = notableTag(m.Section)
		}
	}

	for i := range groups {
		slices.SortStableFunc(groups[i].hits, func(a, b match.Match) int {
			if c := cmp.Compare(rankTag(notableTag(b.Section)), rankTag(notableTag(a.Section))); c != 0 {
				return c
			}
			return cmp.Compare(b.Probability, a.Probability)
		})
	}
	slices.SortStableFunc(groups, func(a, b keyHits) int {
		if c := cmp.Compare(rankTag(b.notable), rankTag(a.notable)); c != 0 {
			return c
		}
		if c := cmp.Compare(b.best, a.best); c != 0 {
			return c
		}
		return cmp.Compare(a.key, b.key)
	})
	return groups
}

func rankTag(tag string) int {
	if tag == "" {
		return 0
	}
	return 1
}

func cleanEntry(text string) string {
	text = mdLink.ReplaceAllString(text, "$1")
	text = githubRef.ReplaceAllString(text, "#$1")
	text = anyURL.ReplaceAllString(text, "")
	text = attribution.ReplaceAllString(text, "")
	text = spaces.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func writeKeyHits(b *strings.Builder, g keyHits) {
	fmt.Fprintf(b, "\n  %s  %.2f", g.key, g.best)
	if g.notable != "" {
		fmt.Fprintf(b, "  %s", g.notable)
	}
	b.WriteString("\n")

	for _, m := range g.hits[:min(entriesPerKey, len(g.hits))] {
		where := m.Release
		if m.Section != "" {
			where += " " + m.Section
		}
		fmt.Fprintf(b, "    %.2f  %s: %s\n", m.Probability, where, truncate(cleanEntry(m.Text), maxEntryLen))
	}
	if rest := g.hits[min(entriesPerKey, len(g.hits)):]; len(rest) > 0 {
		var releases []string
		for _, m := range rest {
			if !slices.Contains(releases, m.Release) {
				releases = append(releases, m.Release)
			}
		}
		fmt.Fprintf(b, "    + %d more in %s\n", len(rest), strings.Join(releases, ", "))
	}
}
