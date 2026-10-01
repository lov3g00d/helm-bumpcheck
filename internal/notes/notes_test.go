package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const tempoLikeBody = "Tempo 3.0 removes deprecated components.\r\n" +
	"Review the migration guide, especially if you use localblocks.\r\n" +
	"\r\n" +
	"## Features\r\n" +
	"\r\n" +
	"- Add span profiling support by @a in #1\r\n" +
	"- metrics-generator: Add per-label limiter by @b in #2\r\n" +
	"  - Adds `max_cardinality_per_label` per tenant override.\r\n" +
	"\r\n" +
	"### Breaking Changes\r\n" +
	"\r\n" +
	"* Removed metrics-generator localblocks processor by @c in #3\r\n" +
	"1. Renamed `ingester` block, for example:\r\n" +
	"```yaml\r\n" +
	"# old\r\n" +
	"ingester:\r\n" +
	"  lifecycler: {}\r\n" +
	"```\r\n"

func TestSplit(t *testing.T) {
	got := Split(Release{Tag: "v3.0.0", Body: tempoLikeBody})
	want := []Entry{
		{"v3.0.0", "", "Tempo 3.0 removes deprecated components. Review the migration guide, especially if you use localblocks."},
		{"v3.0.0", "Features", "Add span profiling support by @a in #1"},
		{"v3.0.0", "Features", "metrics-generator: Add per-label limiter by @b in #2 Adds `max_cardinality_per_label` per tenant override."},
		{"v3.0.0", "Breaking Changes", "Removed metrics-generator localblocks processor by @c in #3"},
		{"v3.0.0", "Breaking Changes", "Renamed `ingester` block, for example: # old ingester: lifecycler: {}"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Split =\n%s\nwant\n%s", dump(got), dump(want))
	}
}

func TestSplitEmpty(t *testing.T) {
	if got := Split(Release{Tag: "v1.0.0", Body: ""}); len(got) != 0 {
		t.Errorf("Split(empty) = %v", got)
	}
}

func TestIsBullet(t *testing.T) {
	tests := map[string]bool{
		"- a":        true,
		"* a":        true,
		"+ a":        true,
		"1. a":       true,
		"12. a":      true,
		"-a":         false,
		"v1.2. a":    false,
		"Tempo 3.0.": false,
		"2026. a":    false,
	}
	for in, want := range tests {
		if got := isBullet(in); got != want {
			t.Errorf("isBullet(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCanonical(t *testing.T) {
	tests := map[string]string{
		"3.0.0":       "v3.0.0",
		"v2.9.0":      "v2.9.0",
		"1.24":        "v1.24.0",
		"3.1.0-rc.1":  "v3.1.0-rc.1",
		"latest":      "",
		"tempo-1.0.0": "",
	}
	for in, want := range tests {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func fakeGitHub(t *testing.T, tags []string, perPage int) (*GitHub, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/repos/grafana/tempo/releases" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gh-test" {
			t.Errorf("Authorization = %q", got)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		start := (page - 1) * perPage
		var out []ghRelease
		for i := start; i < start+perPage && i < len(tags); i++ {
			tag := tags[i]
			out = append(out, ghRelease{
				TagName:    tag,
				Body:       "- change in " + tag,
				HTMLURL:    "https://github.com/grafana/tempo/releases/tag/" + tag,
				Prerelease: strings.Contains(tag, "-rc"),
			})
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	g := &GitHub{BaseURL: srv.URL, Token: "gh-test", HTTP: &http.Client{Timeout: 5 * time.Second}, PerPage: perPage, MaxPages: 10}
	return g, &requests
}

func tags(rs []Release) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Tag)
	}
	return out
}

func TestReleasesFiltersRangeAndPaginates(t *testing.T) {
	g, requests := fakeGitHub(t, []string{
		"v3.1.0", "v3.1.0-rc.1", "v3.0.3", "v2.10.8", "v2.9.5", "v3.0.0", "v2.10.0", "v2.9.0", "v2.8.0",
	}, 2)

	got, err := g.Releases(context.Background(), "grafana/tempo", "", "2.9.0", "3.0.3")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"v2.9.5", "v2.10.0", "v2.10.8", "v3.0.0", "v3.0.3"}
	if !reflect.DeepEqual(tags(got), want) {
		t.Errorf("tags = %v, want %v", tags(got), want)
	}
	if got := requests.Load(); got != 5 {
		t.Errorf("requests = %d, want 5 pages", got)
	}
}

func TestReleasesTagPrefix(t *testing.T) {
	g, _ := fakeGitHub(t, []string{"tempo-1.24.4", "tempo-distributed-1.50.0", "tempo-1.23.0", "loki-6.0.0"}, 100)
	got, err := g.Releases(context.Background(), "grafana/tempo", "tempo-", "1.10.0", "1.24.4")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tempo-1.23.0", "tempo-1.24.4"}; !reflect.DeepEqual(tags(got), want) {
		t.Errorf("tags = %v, want %v", tags(got), want)
	}
}

func TestReleasesStopsAtMaxPages(t *testing.T) {
	g, requests := fakeGitHub(t, []string{"v5", "v4", "v3", "v2", "v1"}, 1)
	g.MaxPages = 2
	if _, err := g.Releases(context.Background(), "grafana/tempo", "", "0.0.1", "9.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestReleasesErrors(t *testing.T) {
	g, _ := fakeGitHub(t, nil, 100)
	if _, err := g.Releases(context.Background(), "tempo", "", "1.0.0", "2.0.0"); err == nil {
		t.Error("expected error for repo without owner")
	}
	if _, err := g.Releases(context.Background(), "grafana/tempo", "", "latest", "2.0.0"); err == nil {
		t.Error("expected error for non-semver version")
	}
	_, err := g.Releases(context.Background(), "grafana/missing", "", "1.0.0", "2.0.0")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want 404", err)
	}
}

func TestLiveTempoReleases(t *testing.T) {
	if os.Getenv("LIVE") != "1" {
		t.Skip("set LIVE=1 to call the real GitHub API")
	}
	rs, err := NewGitHubFromEnv().Releases(context.Background(), "grafana/tempo", "", "2.9.0", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rs {
		for _, e := range Split(r) {
			if strings.Contains(e.Text, "Removed metrics-generator localblocks processor") {
				found = true
				t.Logf("%s [%s] %s", e.Release, e.Section, e.Text)
			}
		}
	}
	if !found {
		t.Errorf("localblocks removal not found in %v", tags(rs))
	}
}

func dump(es []Entry) string {
	var b strings.Builder
	for _, e := range es {
		fmt.Fprintf(&b, "  %q [%q] %q\n", e.Release, e.Section, e.Text)
	}
	return b.String()
}
