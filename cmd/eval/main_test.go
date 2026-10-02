package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
)

const fakeHelmScript = `#!/bin/sh
[ "$1" = pull ] || exit 1
dest=""; version=""; prev=""
for a in "$@"; do
  case "$prev" in
    --untardir) dest="$a" ;;
    --version) version="$a" ;;
  esac
  prev="$a"
done
mkdir -p "$dest/app"
case "$version" in
  1.0.0) values='legacy:\n  enabled: false\nport: 3100\n' ;;
  2.0.0) values='port: 3100\n' ;;
  *) echo "Error: chart version not found" >&2; exit 1 ;;
esac
printf "name: app\nversion: $version\n" > "$dest/app/Chart.yaml"
printf "$values" > "$dest/app/values.yaml"
`

const casesFile = `cases:
  - name: legacy
    split: tune
    chart: {repo: https://charts.example, name: app}
    appRepo: acme/app
    tagPrefix: v
    from: 1.0.0
    to: 2.0.0
    valuesFile: values.yaml
    affected:
      - {key: legacy.enabled, decision: evidence}
    evidence:
      - {release: v2.0.0, entry: Removed legacy mode, keys: [legacy.enabled]}
`

func setup(t *testing.T) config {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"helm":        fakeHelmScript,
		"values.yaml": "legacy:\n  enabled: true\nport: 3200\n",
		"cases.yaml":  casesFile,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HELM_BIN", filepath.Join(dir, "helm"))

	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/releases" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v2.0.0", "body": "## Breaking Changes\n- Removed legacy mode\n\n## Features\n- Faster port binding"},
			{"tag_name": "v1.0.0", "body": "## Features\n- Initial release"},
		})
	}))
	t.Cleanup(gh.Close)
	t.Setenv("GITHUB_API_URL", gh.URL)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

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
		resp := jev.Response{Model: "jev-1.13.0", Answers: map[string]jev.Answer{}, Usage: jev.Usage{InputTokens: 50}}
		for id, q := range req.Questions {
			p := 0.1
			if strings.Contains(req.State["entry"], "legacy") && q.Instructions["config_key"] == "legacy.enabled" {
				p = 0.85
			}
			resp.Answers[id] = jev.Answer{Type: "noul", Noul: p}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(jv.Close)
	t.Setenv(jev.BaseURLEnv, jv.URL)
	t.Setenv(jev.APIKeyEnv, "test-key")

	return config{cases: filepath.Join(dir, "cases.yaml"), threshold: 0.5, output: "text", maxPages: 1}
}

func TestRunText(t *testing.T) {
	cfg := setup(t)
	var out bytes.Buffer
	if err := run(context.Background(), &out, cfg); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"Cases: 1 scored, 0 failed. Jev was asked about 1 of 2 keys",
		"legacy  tune   1/2",
		"Picked 0.8 on tune by F1",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunJSONSkipJev(t *testing.T) {
	cfg := setup(t)
	t.Setenv(jev.APIKeyEnv, "")
	cfg.output, cfg.skipJev = "json", true
	var out bytes.Buffer
	if err := run(context.Background(), &out, cfg); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	var got struct {
		Summary struct {
			Scored  int `json:"scored"`
			Keyword struct {
				Score struct{ TP, FP, FN int } `json:"score"`
			} `json:"keyword"`
			Jev *struct{} `json:"jev"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary.Scored != 1 || got.Summary.Jev != nil || got.Summary.Keyword.Score.TP != 1 {
		t.Errorf("summary = %+v", got.Summary)
	}
}

func notFound(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRunErrors(t *testing.T) {
	tests := map[string]struct {
		edit func(*testing.T, *config)
		want string
	}{
		"unknown output": {func(t *testing.T, c *config) { c.output = "yaml" }, "unknown output format"},
		"missing cases":  {func(t *testing.T, c *config) { c.cases = filepath.Join(t.TempDir(), "none.yaml") }, "no such file"},
		"no key":         {func(t *testing.T, c *config) { t.Setenv(jev.APIKeyEnv, "") }, "or pass -skip-jev"},
		"nothing scored": {func(t *testing.T, c *config) { c.skipJev = true; t.Setenv("GITHUB_API_URL", notFound(t)) }, "none of the 1 cases could be scored"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := setup(t)
			tt.edit(t, &cfg)
			err := run(context.Background(), &bytes.Buffer{}, cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
