package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	g := &GitHub{Token: "gh-test", HTTP: &http.Client{Timeout: 5 * time.Second}, PerPage: perPage, MaxPages: 10}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/repos/grafana/tempo/releases" {
			http.NotFound(w, r)
			return
		}
		want := ""
		if g.Token != "" {
			want = "Bearer " + g.Token
		}
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
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
	g.BaseURL = srv.URL
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

	got, _, err := g.Releases(context.Background(), "grafana/tempo", "", "2.9.0", "3.0.3")
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
	got, _, err := g.Releases(context.Background(), "grafana/tempo", "tempo-", "1.10.0", "1.24.4")
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
	got, trunc, err := g.Releases(context.Background(), "grafana/tempo", "", "0.0.1", "9.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tags(got), []string{"v4", "v5"}) {
		t.Errorf("tags = %v, want only the first two pages", tags(got))
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
	if want := (&Truncation{Pages: 2, Oldest: "v4"}); !reflect.DeepEqual(trunc, want) {
		t.Errorf("truncation = %+v, want %+v", trunc, want)
	}
}

func TestReleasesCompleteListsAreNotTruncated(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		from     string
		maxPages int
	}{
		{"short last page", []string{"v5", "v4", "v3"}, "0.0.1", 10},
		{"from reached on the last allowed page", []string{"v5", "v4", "v3", "v2"}, "4.0.0", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := fakeGitHub(t, tt.tags, 1)
			g.MaxPages = tt.maxPages
			_, trunc, err := g.Releases(context.Background(), "grafana/tempo", "", tt.from, "9.0.0")
			if err != nil || trunc != nil {
				t.Errorf("truncation = %+v, err %v, want a complete listing", trunc, err)
			}
		})
	}
}

func TestReleasesClampsUnauthenticatedPaging(t *testing.T) {
	var all []string
	for i := 1200; i > 0; i-- {
		all = append(all, fmt.Sprintf("v1.0.%d", i))
	}

	g, requests := fakeGitHub(t, all, 100)
	g.Token, g.MaxPages = "", 40
	got, trunc, err := g.Releases(context.Background(), "grafana/tempo", "", "1.0.1", "1.0.1200")
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 10 || len(got) != 1000 {
		t.Errorf("%d releases after %d requests, want 1000 after 10", len(got), requests.Load())
	}
	if want := (&Truncation{Pages: 10, Oldest: "v1.0.201", ResultCap: true}); !reflect.DeepEqual(trunc, want) {
		t.Errorf("truncation = %+v, want %+v", trunc, want)
	}

	g, requests = fakeGitHub(t, all, 100)
	g.MaxPages = 40
	got, trunc, err = g.Releases(context.Background(), "grafana/tempo", "", "1.0.1", "1.0.1200")
	if err != nil || trunc != nil || len(got) != 1199 || requests.Load() != 13 {
		t.Errorf("with a token: %d releases after %d requests, truncation %+v, err %v", len(got), requests.Load(), trunc, err)
	}
}

func TestReleasesStopsAtResultCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			json.NewEncoder(w).Encode([]ghRelease{{TagName: "v3.0.0"}, {TagName: "v2.5.0"}})
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"message":"Only the first 1000 results are available.","documentation_url":"https://docs.github.com/rest/releases/releases#list-releases"}`)
	}))
	defer srv.Close()

	g := &GitHub{BaseURL: srv.URL, Token: "t", HTTP: srv.Client(), PerPage: 2, MaxPages: 10, MaxRetries: 2}
	got, trunc, err := g.Releases(context.Background(), "acme/app", "", "1.0.0", "3.0.0")
	if err != nil {
		t.Fatalf("err = %v, want the first page", err)
	}
	if !reflect.DeepEqual(tags(got), []string{"v2.5.0", "v3.0.0"}) {
		t.Errorf("tags = %v", tags(got))
	}
	if want := (&Truncation{Pages: 1, Oldest: "v2.5.0", ResultCap: true}); !reflect.DeepEqual(trunc, want) {
		t.Errorf("truncation = %+v, want %+v", trunc, want)
	}
}

func TestReleasesLaterPageFailureNamesTagReached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			json.NewEncoder(w).Encode([]ghRelease{{TagName: "v3.0.0"}, {TagName: "v2.5.0"}})
			return
		}
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	g := &GitHub{BaseURL: srv.URL, Token: "t", HTTP: srv.Client(), PerPage: 2, MaxPages: 10}
	got, trunc, err := g.Releases(context.Background(), "acme/app", "", "1.0.0", "3.0.0")
	if err == nil || got != nil || trunc != nil {
		t.Fatalf("got %v %+v, err %v, want a failure", got, trunc, err)
	}
	if want := "page 2: 401 Unauthorized"; !strings.Contains(err.Error(), want) || !strings.HasSuffix(err.Error(), "(after page 1, at v2.5.0)") {
		t.Errorf("err = %v", err)
	}
}

func TestReleasesBackoffGrowsWithAttempts(t *testing.T) {
	g := &GitHub{Backoff: time.Second, MaxBackoff: 10 * time.Second}
	resp := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}
	for attempt, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second} {
		got := g.retryWait(resp, attempt)
		if got > want || got < want*3/4 {
			t.Errorf("attempt %d: wait %s, want between %s and %s", attempt, got, want*3/4, want)
		}
	}
}

func TestReleasesSendsUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		io.WriteString(w, "[]")
	}))
	defer srv.Close()

	g := NewGitHubFromEnv()
	g.BaseURL, g.UserAgent = srv.URL, "helm-bumpcheck/1.2.3"
	if _, _, err := g.Releases(context.Background(), "acme/app", "", "1.0.0", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if ua != "helm-bumpcheck/1.2.3" {
		t.Errorf("User-Agent = %q", ua)
	}
}

func TestReleasesErrors(t *testing.T) {
	g, _ := fakeGitHub(t, nil, 100)
	if _, _, err := g.Releases(context.Background(), "tempo", "", "1.0.0", "2.0.0"); err == nil {
		t.Error("expected error for repo without owner")
	}
	if _, _, err := g.Releases(context.Background(), "grafana/tempo", "", "latest", "2.0.0"); err == nil {
		t.Error("expected error for non-semver version")
	}
	_, _, err := g.Releases(context.Background(), "grafana/missing", "", "1.0.0", "2.0.0")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want 404", err)
	}
}

func TestLiveTempoReleases(t *testing.T) {
	if os.Getenv("LIVE") != "1" {
		t.Skip("set LIVE=1 to call the real GitHub API")
	}
	rs, _, err := NewGitHubFromEnv().Releases(context.Background(), "grafana/tempo", "", "2.9.0", "3.0.0")
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

func TestReleasesUnauthenticatedHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	g := &GitHub{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 5 * time.Second}, PerPage: 100, MaxPages: 1}
	_, _, err := g.Releases(context.Background(), "acme/app", "", "1.0.0", "2.0.0")
	if err == nil || !strings.Contains(err.Error(), "set GITHUB_TOKEN") {
		t.Errorf("err = %v, want a GITHUB_TOKEN hint", err)
	}

	g.Token = "t"
	_, _, err = g.Releases(context.Background(), "acme/app", "", "1.0.0", "2.0.0")
	if err == nil || strings.Contains(err.Error(), "set GITHUB_TOKEN") {
		t.Errorf("err = %v, want no hint when a token is set", err)
	}
}

func TestReleasesRetriesRateLimits(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
			t.Errorf("X-GitHub-Api-Version = %q", got)
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"message":"secondary rate limit"}`, http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode([]ghRelease{{TagName: "v2.0.0", Body: "- x"}})
	}))
	defer srv.Close()

	g := &GitHub{BaseURL: srv.URL, Token: "t", HTTP: &http.Client{Timeout: 5 * time.Second}, PerPage: 100, MaxPages: 1, MaxRetries: 2, MaxWait: time.Second}
	got, _, err := g.Releases(context.Background(), "acme/app", "", "1.0.0", "2.0.0")
	if err != nil || len(got) != 1 || calls.Load() != 2 {
		t.Errorf("got %v, err %v, calls %d", got, err, calls.Load())
	}
}

func TestReleasesGivesUpOnLongRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	g := &GitHub{BaseURL: srv.URL, Token: "t", HTTP: &http.Client{Timeout: 5 * time.Second}, PerPage: 100, MaxPages: 1, MaxRetries: 2, MaxWait: time.Second}
	start := time.Now()
	_, _, err := g.Releases(context.Background(), "acme/app", "", "1.0.0", "2.0.0")
	if err == nil || !strings.Contains(err.Error(), "rate limited for") || time.Since(start) > 2*time.Second {
		t.Errorf("err = %v after %s", err, time.Since(start))
	}
}

func TestSplitAllDropsBoilerplate(t *testing.T) {
	rel := Release{Tag: "velero-4.0.0", Body: "A Helm chart for velero\n\n## What's Changed\n* [velero] Add support for multiple locations by @a in #413\n\n## New Contributors\n* @a made their first contribution in #413\n\n**Full Changelog**: https://github.com/x/y/compare/a...b"}
	got := SplitAll([]Release{rel})
	var texts []string
	for _, e := range got {
		texts = append(texts, e.Text)
	}
	want := []string{"A Helm chart for velero", "[velero] Add support for multiple locations by @a in #413"}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("SplitAll texts = %q, want %q", texts, want)
	}
}

func TestReleasesStopsPagingPastFrom(t *testing.T) {
	g, requests := fakeGitHub(t, []string{"v3.0.0", "v2.9.0", "v2.8.0", "v2.7.0", "v2.6.0", "v2.5.0"}, 2)
	got, _, err := g.Releases(context.Background(), "grafana/tempo", "", "2.9.0", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tags(got), []string{"v3.0.0"}) || requests.Load() != 2 {
		t.Errorf("tags %v after %d requests, want [v3.0.0] after 2", tags(got), requests.Load())
	}
}

func TestReleasesEarlyStopNeedsExactFrom(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		prefix   string
		from, to string
		want     []string
	}{
		{
			name:   "backports on a prefix-filtered page",
			tags:   []string{"app-3.1.0", "app-1.9.5", "app-1.9.4", "other-5.0.0", "app-3.0.0", "app-2.5.0", "app-2.0.0", "app-1.0.0"},
			prefix: "app-", from: "2.0.0", to: "3.1.0",
			want: []string{"app-2.5.0", "app-3.0.0", "app-3.1.0"},
		},
		{
			name: "old-line patch releases published after the range",
			tags: []string{"v2.1.0", "v1.9.3", "v1.9.2", "v1.9.1", "v2.0.1", "v2.0.0"},
			from: "2.0.0", to: "2.2.0",
			want: []string{"v2.0.1", "v2.1.0"},
		},
		{
			name:   "monorepo with one matching tag per page",
			tags:   []string{"tempo-1.3.0", "loki-5.0.0", "tempo-1.0.9", "loki-4.9.0", "tempo-1.2.0", "tempo-1.1.0"},
			prefix: "tempo-", from: "1.1.0", to: "1.3.0",
			want: []string{"tempo-1.2.0", "tempo-1.3.0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := fakeGitHub(t, tt.tags, 2)
			got, _, err := g.Releases(context.Background(), "grafana/tempo", tt.prefix, tt.from, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tags(got), tt.want) {
				t.Errorf("tags = %v, want %v", tags(got), tt.want)
			}
		})
	}
}

func countingServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &conns
}

func TestReleasesReusesConnections(t *testing.T) {
	var calls atomic.Int32
	srv, conns := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, strings.Repeat("x", 4<<10), http.StatusBadGateway)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		json.NewEncoder(w).Encode([]ghRelease{{TagName: fmt.Sprintf("v3.0.%d", 9-page)}})
		io.WriteString(w, strings.Repeat(" ", 64<<10))
	})

	g := &GitHub{BaseURL: srv.URL, Token: "t", HTTP: srv.Client(), PerPage: 1, MaxPages: 3, MaxRetries: 1, MaxWait: 5 * time.Second}
	if _, _, err := g.Releases(context.Background(), "acme/app", "", "1.0.0", "4.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := conns.Load(); got != 1 {
		t.Errorf("connections = %d over %d requests, want 1", got, calls.Load())
	}
}
