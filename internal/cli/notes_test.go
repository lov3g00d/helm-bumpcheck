package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
)

func fakeAPIs(t *testing.T) *atomic.Int32 {
	t.Helper()
	var ghCalls atomic.Int32
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ghCalls.Add(1)
		if r.URL.Path != "/repos/acme/app/releases" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v2.0.0", "body": "## Breaking Changes\n- Removed legacy mode\n\n## Features\n- Faster startup"},
			{"tag_name": "v1.5.0", "body": "## Features\n- Team dashboards"},
			{"tag_name": "v1.0.0", "body": "## Features\n- Initial release"},
		})
	}))
	t.Cleanup(gh.Close)

	jv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State     map[string]string `json:"state"`
			Questions map[string]struct {
				Instructions map[string]string `json:"instructions"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode jev request: %v", err)
		}
		resp := jev.Response{Answers: map[string]jev.Answer{}, Usage: jev.Usage{InputTokens: 50}}
		for id, q := range req.Questions {
			p := 0.02
			if strings.Contains(req.State["entry"], "legacy") && strings.Contains(q.Instructions["config_key"], "legacy") {
				p = 0.93
			}
			resp.Answers[id] = jev.Answer{Type: "noul", Noul: p}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(jv.Close)

	t.Setenv("GITHUB_API_URL", gh.URL)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv(jev.BaseURLEnv, jv.URL)
	t.Setenv(jev.APIKeyEnv, "test-key")
	return &ghCalls
}

func TestNotesTextOutput(t *testing.T) {
	userValues := setup(t)
	fakeAPIs(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--app-repo", "acme/app")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Release notes (app): acme/app 1.0.0 -> 2.0.0, 2 releases, 3 entries, 3 questions",
		"  removed       legacy.enabled  no longer in the chart",
		"note 0.93  v2.0.0 Breaking Changes: Removed legacy mode",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0.02") {
		t.Errorf("below-threshold match shown:\n%s", out)
	}
}

func TestNotesJSONKeepsAllMatches(t *testing.T) {
	userValues := setup(t)
	fakeAPIs(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--app-repo", "acme/app", "-o", "json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var res struct {
		Removed []string       `json:"removed"`
		Notes   []report.Notes `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(res.Removed) != 1 {
		t.Errorf("removed = %v", res.Removed)
	}
	if len(res.Notes) != 1 {
		t.Fatalf("notes = %+v", res.Notes)
	}
	if n := res.Notes[0]; n.Releases != 2 || len(n.Matches) != 3 || n.Matches[0].Key != "legacy.enabled" || n.Stats.InputTokens != 100 {
		t.Errorf("notes = %+v", n)
	}
}

func TestNotesSkippedWithoutKey(t *testing.T) {
	userValues := setup(t)
	fakeAPIs(t)
	t.Setenv(jev.APIKeyEnv, "")
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--app-repo", "acme/app")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Release notes (app): skipped, TYPESAFE_API_KEY is not set") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(out, "removed       legacy.enabled") {
		t.Errorf("diff missing when notes are skipped:\n%s", out)
	}
}

func TestNotesSkippedWhenAppVersionUnchanged(t *testing.T) {
	userValues := setup(t)
	fakeAPIs(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "1.0.0", "-f", userValues, "--app-repo", "acme/app")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Release notes (app): skipped, the version did not change") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRejectsBadThreshold(t *testing.T) {
	setup(t)
	_, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "--threshold", "1.5")
	if err == nil || !strings.Contains(err.Error(), "threshold") {
		t.Errorf("err = %v", err)
	}
}

func TestNotesSkippedOnGitHubError(t *testing.T) {
	userValues := setup(t)
	fakeAPIs(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--app-repo", "acme/missing")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Release notes (app): skipped, github releases acme/missing page 1: 404 Not Found") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(out, "removed       legacy.enabled") {
		t.Errorf("diff lost on GitHub error:\n%s", out)
	}
}

func TestChartNotes(t *testing.T) {
	userValues := setup(t)
	fakeAPIs(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--chart-notes", "acme/app", "--chart-tag-prefix", "v")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Release notes (chart): acme/app 1.0.0 -> 2.0.0, 2 releases") {
		t.Errorf("output:\n%s", out)
	}

	out, err = execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--chart-notes", "acme/app")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Release notes (chart): skipped, no releases of acme/app tagged mychart-<version> in (1.0.0, 2.0.0]") {
		t.Errorf("empty range not reported as skipped:\n%s", out)
	}
}

func TestDiffFetchesNotesOncePerChart(t *testing.T) {
	g, base, head := bumpHistory(t)
	ghCalls := fakeAPIs(t)
	out, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", head, "--notes")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if got := ghCalls.Load(); got != 2 {
		t.Errorf("GitHub requests = %d, want 2 (one per notes source, shared by both values files)", got)
	}
}
