package report

import (
	"regexp"
	"strings"
)

var (
	mdLink      = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)
	githubRef   = regexp.MustCompile(`https://github\.com/([^/\s]+/[^/\s]+)/(?:pull|issues)/(\d+)\S*`)
	anyURL      = regexp.MustCompile(`https?://\S+`)
	bareRef     = regexp.MustCompile(`(^|[^\w&./-])(\(?)(?i:#|gh-)(\d+)\b(\)?)`)
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

func cleanEntry(text, repo string) string {
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
	text = bareRef.ReplaceAllStringFunc(text, func(s string) string {
		m := bareRef.FindStringSubmatch(s)
		prefix, open, num, closing := m[1], m[2], m[3], m[4]
		if repo != "" {
			return prefix + open + repo + "#" + num + closing
		}
		if open != "" && closing != "" {
			return prefix
		}
		return prefix + open + closing
	})
	text = spaces.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
