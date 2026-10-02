package report

import (
	"regexp"
	"strings"
)

var (
	mdLink      = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)
	githubRef   = regexp.MustCompile(`https://github\.com/([^/\s]+/[^/\s]+)/(?:pull|issues)/(\d+)\S*`)
	anyURL      = regexp.MustCompile(`https?://\S+`)
	attribution = regexp.MustCompile(`\s+by @[\w-]+(?:\[bot\])?(?:\s+in\s+.*)?$`)
	spaces      = regexp.MustCompile(`\s+`)
)

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

func rankTag(tag string) int {
	if tag == "" {
		return 0
	}
	return 1
}

func cleanEntry(text string) string {
	text = mdLink.ReplaceAllStringFunc(text, func(link string) string {
		m := mdLink.FindStringSubmatch(link)
		if ref := githubRef.FindStringSubmatch(m[2]); ref != nil {
			return ref[1] + "#" + ref[2]
		}
		return m[1]
	})
	text = githubRef.ReplaceAllString(text, "$1#$2")
	text = anyURL.ReplaceAllString(text, "")
	text = attribution.ReplaceAllString(text, "")
	text = spaces.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
