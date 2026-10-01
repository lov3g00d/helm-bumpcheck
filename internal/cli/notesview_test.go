package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-values-impact/internal/match"
)

func TestCleanEntry(t *testing.T) {
	tests := map[string]string{
		"[BUGFIX] Improved handling of complete blocks [#3805](https://github.com/grafana/tempo/pull/3805) (@mdisibio)":    "[BUGFIX] Improved handling of complete blocks #3805 (@mdisibio)",
		"[velero] Add support for multiple locations by @bsteinm2 in https://github.com/vmware-tanzu/helm-charts/pull/413": "[velero] Add support for multiple locations",
		"Fix crash, see https://github.com/acme/app/issues/12 and https://example.com/docs":                                "Fix crash, see #12 and",
		"Removed metrics-generator localblocks processor by @javiermolinar in #6555":                                       "Removed metrics-generator localblocks processor",
		"Bump chart by @renovate[bot] in https://github.com/x/y/pull/1":                                                    "Bump chart",
		"Plain   text\twith   spaces": "Plain text with spaces",
	}
	for in, want := range tests {
		if got := cleanEntry(in); got != want {
			t.Errorf("cleanEntry(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestGroupByKey(t *testing.T) {
	ms := []match.Match{
		{Key: "a.key", Probability: 0.81, Release: "v2.6.0", Section: "Bugfixes", Text: "fix"},
		{Key: "b.key", Probability: 0.95, Release: "v2.9.0", Section: "Enhancements", Text: "x"},
		{Key: "a.key", Probability: 0.62, Release: "v2.6.0", Section: "Breaking Changes", Text: "removed"},
		{Key: "a.key", Probability: 0.40, Release: "v2.5.0", Section: "Breaking Changes", Text: "below threshold"},
		{Key: "c.key", Probability: 0.70, Release: "v2.7.0", Section: "Deprecations", Text: "deprecated"},
	}
	groups := groupByKey(ms, 0.5)

	var order []string
	for _, g := range groups {
		order = append(order, g.key+":"+g.notable)
	}
	if want := []string{"a.key:BREAKING", "c.key:DEPRECATION", "b.key:"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}

	a := groups[0]
	if a.best != 0.81 || len(a.hits) != 2 || a.hits[0].Section != "Breaking Changes" {
		t.Errorf("a.key group = %+v", a)
	}
}

func TestWriteKeyHitsCollapsesExtra(t *testing.T) {
	g := keyHits{key: "k", best: 0.9}
	for _, rel := range []string{"v1", "v2", "v3", "v4", "v4", "v5"} {
		g.hits = append(g.hits, match.Match{Key: "k", Probability: 0.9, Release: rel, Section: "Features", Text: "t"})
	}
	var b strings.Builder
	writeKeyHits(&b, g)
	out := b.String()
	if strings.Count(out, "0.90  v") != entriesPerKey {
		t.Errorf("expected %d entry lines:\n%s", entriesPerKey, out)
	}
	if !strings.Contains(out, "+ 3 more in v4, v5") {
		t.Errorf("missing collapsed line:\n%s", out)
	}
}
