package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/report"
)

const dirHelmScript = `#!/bin/sh
if [ "$1" = template ] && [ -n "$FAKE_LOG" ]; then echo "$*" >> "$FAKE_LOG"; fi
case "$3" in *broken*) echo "Error: chart is broken" >&2; exit 1 ;; esac
case "$1 $2" in
  "show values") cat "$3/values.yaml" ;;
  "show chart") cat "$3/Chart.yaml" ;;
  *)
    if [ "$1" = template ] && [ -n "$FAKE_RENDER_FAIL" ] && grep -q '^version: 2' "$3/Chart.yaml"; then
      echo "Error: execution error at (app/templates/NOTES.txt:1:1):" >&2
      echo "ERROR: legacy.enabled was removed" >&2
      exit 1
    fi
    ;;
esac
`

type gitFixture struct {
	t   *testing.T
	dir string
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	bin := filepath.Join(t.TempDir(), "helm")
	if err := os.WriteFile(bin, []byte(dirHelmScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HELM_BIN", bin)
	t.Setenv("TYPESAFE_API_KEY", "")

	g := &gitFixture{t: t, dir: t.TempDir()}
	g.git("init", "-q", "-b", "main")
	return g
}

func (g *gitFixture) git(args ...string) string {
	g.t.Helper()
	base := []string{"-C", g.dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}
	out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (g *gitFixture) write(files map[string]string) {
	g.t.Helper()
	for name, content := range files {
		full := filepath.Join(g.dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			g.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			g.t.Fatal(err)
		}
	}
}

func (g *gitFixture) commit(msg string) string {
	g.t.Helper()
	g.git("add", "-A")
	g.git("commit", "-q", "-m", msg)
	return g.git("rev-parse", "HEAD")
}

func bumpHistory(t *testing.T) (*gitFixture, string, string) {
	g := newGitFixture(t)
	g.write(map[string]string{
		"charts/app/Chart.yaml":            "apiVersion: v2\nname: app\nversion: 1.0.0\nappVersion: \"1.0\"\nsources:\n  - https://github.com/acme/app\n  - https://github.com/acme/helm-charts\n",
		"charts/app/values.yaml":           "replicas: 1\nlegacy:\n  enabled: false\n",
		"charts/app/values_prod.yaml":      "legacy:\n  enabled: true\n",
		"charts/app/values_staging.yaml":   "replicas: 5\n",
		"charts/app/ci/test-values.yaml":   "replicas: 9\n",
		"charts/app/charts/sub/Chart.yaml": "apiVersion: v2\nname: sub\nversion: 1.0.0\n",
		"charts/plain/Chart.yaml":          "apiVersion: v2\nname: plain\nversion: 1.0.0\n",
		"charts/plain/values.yaml":         "a: 1\n",
	})
	base := g.commit("base")
	g.write(map[string]string{
		"charts/app/Chart.yaml":            "apiVersion: v2\nname: app\nversion: 2.0.0\nappVersion: \"2.0\"\nsources:\n  - https://github.com/acme/app\n  - https://github.com/acme/helm-charts\n",
		"charts/app/values.yaml":           "replicas: 2\n",
		"charts/app/charts/sub/Chart.yaml": "apiVersion: v2\nname: sub\nversion: 2.0.0\n",
		"charts/plain/Chart.yaml":          "apiVersion: v2\nname: plain\nversion: 1.1.0\n",
		"charts/new/Chart.yaml":            "apiVersion: v2\nname: new\nversion: 0.1.0\n",
	})
	head := g.commit("bump")
	return g, base, head
}

func TestDiffText(t *testing.T) {
	g, base, head := bumpHistory(t)
	out, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", head)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, want := range []string{
		"2 reports checked between " + base[:12] + " and " + head[:12] + ".",
		"app 1.0.0 -> 2.0.0 (app 1.0 -> 2.0) [values_prod.yaml]\nResult: 1 breaking, 1 to check, 0 unverified\n",
		"  removed       legacy.enabled  no longer in the chart",
		"  default       replicas  1 -> 2",
		"app 1.0.0 -> 2.0.0 (app 1.0 -> 2.0) [values_staging.yaml]\nResult: nothing in this upgrade affects the values you set\n",
		"Skipped\n  charts/new: new chart, nothing to compare\n  charts/plain: no values files match values_*.yaml, values-*.yaml\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "test-values.yaml") || strings.Contains(out, "sub ") {
		t.Errorf("ci values or nested subchart leaked into the report:\n%s", out)
	}
}

func TestDiffJSONAndFailOn(t *testing.T) {
	g, base, head := bumpHistory(t)
	t.Setenv("FAKE_RENDER_FAIL", "1")
	out, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", head, "-o", "json", "--fail-on", "removed,render")
	if !errors.Is(err, ErrFindings) {
		t.Fatalf("err = %v, want findings", err)
	}
	var b report.Batch
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	var got []string
	for _, r := range b.Reports {
		got = append(got, r.Path+" "+r.Values)
	}
	if want := []string{"charts/app values_prod.yaml", "charts/app values_staging.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reports = %v, want %v", got, want)
	}
	if !b.Reports[0].Render.Breaking() {
		t.Errorf("render = %+v, want breaking", b.Reports[0].Render)
	}
	if len(b.Skipped) != 2 {
		t.Errorf("skipped = %+v", b.Skipped)
	}
}

func TestDiffMarkdown(t *testing.T) {
	g, base, head := bumpHistory(t)
	out, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", head, "-o", "markdown")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, want := range []string{
		"## helm bumpcheck\n",
		"| `app` 1.0.0 -&gt; 2.0.0 | `values_prod.yaml` | 1 breaking, 1 to check, 0 unverified |\n",
		"### helm bumpcheck: `app 1.0.0 -> 2.0.0 (app 1.0 -> 2.0) [values_prod.yaml]`",
		"<details><summary>Skipped</summary>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestDiffNothingChanged(t *testing.T) {
	g, base, _ := bumpHistory(t)
	out, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", base)
	if err != nil || !strings.HasPrefix(out, "No chart versions changed between") {
		t.Errorf("err %v, output:\n%s", err, out)
	}
}

func TestDiffNotesDetectsRepos(t *testing.T) {
	g, base, head := bumpHistory(t)
	out, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", head, "--notes")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Release notes (chart): skipped, TYPESAFE_API_KEY is not set",
		"Release notes (app): skipped, TYPESAFE_API_KEY is not set",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDetectRepos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Chart.yaml")
	data := "name: grafana\nhome: https://grafana.com\nsources:\n  - https://github.com/grafana/grafana\n  - https://github.com/grafana/helm-charts.git\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	app, chart := detectRepos(path)
	if app != "grafana/grafana" || chart != "grafana/helm-charts" {
		t.Errorf("detectRepos = %q, %q", app, chart)
	}
}

func TestRootStillRequiresVersions(t *testing.T) {
	setup(t)
	if _, err := execute(t, "mychart", "--from", "1.0.0"); err == nil {
		t.Error("root command without --to should fail")
	}
}

func TestDiffFailedChartExitsWithError(t *testing.T) {
	g, _, _ := bumpHistory(t)
	g.write(map[string]string{
		"charts/broken/Chart.yaml":       "apiVersion: v2\nname: broken\nversion: 1.0.0\n",
		"charts/broken/values.yaml":      "a: 1\n",
		"charts/broken/values_prod.yaml": "a: 2\n",
	})
	base := g.commit("add broken")
	g.write(map[string]string{"charts/broken/Chart.yaml": "apiVersion: v2\nname: broken\nversion: 1.1.0\n"})
	head := g.commit("bump broken")

	out, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", head)
	if err == nil || errors.Is(err, ErrFindings) || !strings.Contains(err.Error(), "1 chart could not be analyzed") {
		t.Fatalf("err = %v, want an analysis error", err)
	}
	if !strings.Contains(out, "Failed\n  charts/broken: values_prod.yaml: ") {
		t.Errorf("failed chart not reported:\n%s", out)
	}
}

func TestDiffRendersOldChartWithBaseValues(t *testing.T) {
	g, base, head := bumpHistory(t)
	logFile := filepath.Join(t.TempDir(), "helm.log")
	t.Setenv("FAKE_LOG", logFile)
	if _, err := execute(t, "diff", "-C", g.dir, "--base", base, "--head", head); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	var oldRender string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "/base/") && strings.Contains(line, "values_prod.yaml") {
			oldRender = line
		}
	}
	if oldRender == "" || strings.Count(oldRender, "/base/") != 2 {
		t.Errorf("old chart not rendered with the base copy of values_prod.yaml:\n%s", data)
	}
}
