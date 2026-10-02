package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
)

const (
	keyGone = "app.processor.local_blocks.flush_to_storage"
	keyFine = "app.server.http_listen_port"
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
  2.9.0) values='app:\n  processor:\n    local_blocks:\n      flush_to_storage: false\n  server:\n    http_listen_port: 3100\n  storage:\n    trace:\n      backend: local\n' ;;
  3.0.0) values='app:\n  server:\n    http_listen_port: 3100\n  storage:\n    trace:\n      backend: local\n' ;;
  3.2.0) values='app:\n  server: []\n  storage:\n    trace:\n      backend: local\n' ;;
  3.1.0) values='app:\n  server:\n    http_listen_port: 3200\n  storage:\n    trace:\n      backend: local\n' ;;
  *) echo "Error: chart version not found" >&2; exit 1 ;;
esac
printf "name: app\nversion: $version\n" > "$dest/app/Chart.yaml"
printf "$values" > "$dest/app/values.yaml"
`

const userValues = `app:
  processor:
    local_blocks:
      flush_to_storage: true
  server:
    http_listen_port: 3200
  storage:
    trace:
      backend: s3
`

func fakeHelm(t *testing.T) helm.Runner {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "helm")
	if err := os.WriteFile(bin, []byte(fakeHelmScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return helm.Runner{Bin: bin}
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCases(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "values/app.yaml", userValues)
	return writeFile(t, dir, "cases.yaml", body)
}

const validCase = `
cases:
  - name: one
    split: tune
    source: acme/gitops@abc123
    chart: {repo: https://charts.example, name: app}
    appRepo: acme/app
    tagPrefix: v
    from: 2.9.0
    to: 3.0.0
    valuesFile: values/app.yaml
    affected:
      - {key: app.processor.local_blocks.flush_to_storage, decision: evidence}
    evidence:
      - release: v3.0.0
        entry: Removed localblocks
        keys: [app.processor.local_blocks.flush_to_storage]
    traps:
      - release: v3.0.0
        entry: Faster http
        keys: [app.server.http_listen_port]
        why: same words, different setting
`

func TestLoadCases(t *testing.T) {
	path := writeCases(t, validCase)
	cases, err := LoadCases(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Case{{
		Name:       "one",
		Split:      SplitTune,
		Source:     "acme/gitops@abc123",
		Chart:      Chart{Repo: "https://charts.example", Name: "app"},
		AppRepo:    "acme/app",
		TagPrefix:  "v",
		From:       "2.9.0",
		To:         "3.0.0",
		ValuesFile: filepath.Join(filepath.Dir(path), "values/app.yaml"),
		Affected:   []Label{{Key: keyGone, Decision: DecisionEvidence}},
		Evidence:   []Pair{{Release: "v3.0.0", Entry: "Removed localblocks", Keys: []string{keyGone}}},
		Traps:      []Pair{{Release: "v3.0.0", Entry: "Faster http", Keys: []string{keyFine}, Why: "same words, different setting"}},
	}}
	if !reflect.DeepEqual(cases, want) {
		t.Errorf("cases =\n%+v\nwant\n%+v", cases, want)
	}
}

func TestLoadCasesRejects(t *testing.T) {
	tests := map[string]struct{ old, new string }{
		"unknown split":           {"split: tune", "split: train"},
		"missing chart":           {"    chart: {repo: https://charts.example, name: app}\n", ""},
		"missing values file":     {"valuesFile: values/app.yaml", "valuesFile: values/none.yaml"},
		"affected key not set":    {"{key: app.processor.local_blocks.flush_to_storage, decision: evidence}", "{key: app.retention, decision: evidence}"},
		"unknown decision":        {"decision: evidence", "decision: maybe"},
		"evidence not affected":   {"        keys: [app.processor.local_blocks.flush_to_storage]", "        keys: [app.server.http_listen_port]"},
		"trap key not set":        {"keys: [app.server.http_listen_port]", "keys: [app.retention]"},
		"trap labelled evidence":  {"entry: Faster http\n        keys: [app.server.http_listen_port]", "entry: Removed localblocks processor\n        keys: [app.processor.local_blocks.flush_to_storage]"},
		"evidence without keys":   {"        keys: [app.processor.local_blocks.flush_to_storage]\n", "        keys: []\n"},
		"unknown field":           {"split: tune", "split: tune\n    keys: [a]"},
		"duplicate affected key":  {"decision: evidence}\n", "decision: evidence}\n      - {key: app.processor.local_blocks.flush_to_storage, decision: promote}\n"},
		"duplicate name":          {"cases:\n", "cases:\n  - {name: one, split: tune, chart: {repo: r, name: app}, appRepo: a/b, from: 1.0.0, to: 2.0.0, valuesFile: values/app.yaml}\n"},
		"empty":                   {validCase, "cases: []\n"},
		"no repo for the release": {"    appRepo: acme/app\n", ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(validCase, tt.old) {
				t.Fatalf("fixture does not contain %q", tt.old)
			}
			if _, err := LoadCases(writeCases(t, strings.Replace(validCase, tt.old, tt.new, 1))); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestShippedCasesLoad(t *testing.T) {
	for _, path := range []string{"../../eval/cases.yaml", "../../eval/cases.example.yaml"} {
		if _, err := LoadCases(path); err != nil {
			t.Error(err)
		}
	}
}

func TestScoreKeys(t *testing.T) {
	s := ScoreKeys([]string{"a", "b", "x"}, []string{"a", "b", "c"})
	if s != (Score{TP: 2, FP: 1, FN: 1}) {
		t.Fatalf("score = %+v", s)
	}
	if r, ok := s.Recall(); !ok || r != 2.0/3.0 {
		t.Errorf("recall = %v %v", r, ok)
	}
	if p, ok := s.Precision(); !ok || p != 2.0/3.0 {
		t.Errorf("precision = %v %v", p, ok)
	}
	if f, ok := s.F1(); !ok || f != 2.0/3.0 {
		t.Errorf("f1 = %v %v", f, ok)
	}
	if _, ok := (Score{}).Precision(); ok {
		t.Error("precision with no predictions should be undefined")
	}
	if _, ok := (Score{FP: 2}).Recall(); ok {
		t.Error("recall with no expected keys should be undefined")
	}
	if _, ok := (Score{}).F1(); ok {
		t.Error("f1 with nothing predicted or expected should be undefined")
	}
}

func TestScorePairs(t *testing.T) {
	evidence := []Pair{
		{Release: "v2.0.0", Entry: "Remove legacy", Keys: []string{"legacy.enabled", "legacy.mode"}},
		{Release: "v2.1.0", Entry: "Rename port", Keys: []string{"port"}},
	}
	traps := []Pair{{Release: "v2.0.0", Entry: "Faster port", Keys: []string{"port"}}}
	tests := []struct {
		name    string
		matches []match.Match
		want    PairScore
	}{
		{"nothing predicted", nil, PairScore{Score: Score{FN: 3}}},
		{
			"evidence by prefix",
			[]match.Match{
				{Key: "legacy.enabled", Probability: 0.9, Release: "v2.0.0", Text: "Remove legacy mode by @dev in #12"},
				{Key: "port", Probability: 0.7, Release: "v2.1.0", Text: "Rename port to http.port"},
			},
			PairScore{Score: Score{TP: 2, FN: 1}},
		},
		{
			"same text in another release is not evidence",
			[]match.Match{{Key: "port", Probability: 0.9, Release: "v2.0.0", Text: "Rename port to http.port"}},
			PairScore{Score: Score{FN: 3}, Unlabelled: 1},
		},
		{
			"trap and below threshold",
			[]match.Match{
				{Key: "port", Probability: 0.8, Release: "v2.0.0", Text: "Faster port binding"},
				{Key: "legacy.mode", Probability: 0.49, Release: "v2.0.0", Text: "Remove legacy mode"},
			},
			PairScore{Score: Score{FP: 1, FN: 3}},
		},
		{
			"a pair matched twice counts once",
			[]match.Match{
				{Key: "port", Probability: 0.9, Release: "v2.1.0", Text: "Rename port to http.port"},
				{Key: "port", Probability: 0.6, Release: "v2.1.0", Text: "Rename port in the service too"},
			},
			PairScore{Score: Score{TP: 1, FN: 2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScorePairs(tt.matches, 0.5, evidence, traps); got != tt.want {
				t.Errorf("ScorePairs = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPickThreshold(t *testing.T) {
	point := func(t float64, tp, fp, fn int) SweepPoint {
		return SweepPoint{Threshold: t, Score: Score{TP: tp, FP: fp, FN: fn}}
	}
	tests := []struct {
		name  string
		sweep []SweepPoint
		want  int
	}{
		{"best f1", []SweepPoint{point(0.3, 2, 4, 0), point(0.5, 2, 1, 0), point(0.7, 1, 0, 1)}, 1},
		{"ties go to the higher threshold", []SweepPoint{point(0.3, 1, 0, 0), point(0.5, 1, 0, 0), point(0.7, 0, 0, 1)}, 1},
		{"no positives", []SweepPoint{point(0.3, 0, 2, 0), point(0.5, 0, 0, 0)}, -1},
		{"empty", nil, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PickThreshold(tt.sweep); got != tt.want {
				t.Errorf("PickThreshold = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestKeysAbove(t *testing.T) {
	ms := []match.Match{{Key: "b", Probability: 0.9}, {Key: "a", Probability: 0.5}, {Key: "b", Probability: 0.6}, {Key: "c", Probability: 0.49}}
	if got := KeysAbove(ms, 0.5); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("KeysAbove = %v", got)
	}
}

func fakeGitHub(t *testing.T) *notes.GitHub {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/releases" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v3.0.0", "body": "## Breaking Changes\n- Removed localblocks processor\n\n## Features\n- Faster http listen port binding"},
			{"tag_name": "v2.9.0", "body": "## Features\n- Something old"},
		})
	}))
	t.Cleanup(srv.Close)
	return &notes.GitHub{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 5 * time.Second}, PerPage: 100, MaxPages: 1}
}

type fakeJev struct{}

func (fakeJev) Evaluate(ctx context.Context, state any, questions map[string]jev.Question) (*jev.Response, error) {
	entry := state.(map[string]string)["entry"]
	resp := &jev.Response{Model: "jev-1.13.0", Answers: map[string]jev.Answer{}, Usage: jev.Usage{InputTokens: 200}}
	for id, q := range questions {
		key := q.Instructions.(map[string]any)["config_key"].(string)
		p := 0.05
		switch {
		case strings.Contains(entry, "localblocks") && key == keyGone:
			p = 0.92
		case strings.Contains(entry, "http listen port") && key == keyFine:
			p = 0.95
		}
		resp.Answers[id] = jev.Answer{Type: "noul", Noul: p}
	}
	return resp, nil
}

func testCases(t *testing.T) []Case {
	t.Helper()
	valuesFile := writeFile(t, t.TempDir(), "app.yaml", userValues)
	base := Case{
		Chart:      Chart{Repo: "https://charts.example", Name: "app"},
		AppRepo:    "acme/app",
		TagPrefix:  "v",
		From:       "2.9.0",
		To:         "3.0.0",
		ValuesFile: valuesFile,
		Affected:   []Label{{Key: keyGone, Decision: DecisionEvidence}},
		Evidence:   []Pair{{Release: "v3.0.0", Entry: "Removed localblocks", Keys: []string{keyGone}}},
		Traps:      []Pair{{Release: "v3.0.0", Entry: "Faster http", Keys: []string{keyFine}}},
	}
	tune, holdout, missing := base, base, base
	tune.Name, tune.Split = "tune", SplitTune
	holdout.Name, holdout.Split = "holdout", SplitHoldout
	missing.Name, missing.Split, missing.AppRepo = "missing-repo", SplitTune, "acme/missing"
	return []Case{tune, holdout, missing}
}

func TestRunAsksOnlyWhatTheCLIAsks(t *testing.T) {
	r := Runner{Helm: fakeHelm(t), GitHub: fakeGitHub(t), Jev: fakeJev{}, Threshold: 0.5}
	results, err := r.Run(context.Background(), testCases(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || !strings.Contains(results[2].Error, "github releases acme/missing") {
		t.Fatalf("results = %+v", results)
	}
	got := results[0]
	if got.Error != "" {
		t.Fatal(got.Error)
	}
	if got.Keys != 3 || !reflect.DeepEqual(got.Asked, []string{keyGone}) {
		t.Errorf("keys %d asked %v, want 3 keys with only the removed one asked", got.Keys, got.Asked)
	}
	if got.Releases != 1 || got.Entries != 2 {
		t.Errorf("releases %d entries %d", got.Releases, got.Entries)
	}
	if !reflect.DeepEqual(got.Keyword.Predicted, []string{keyGone}) || got.Keyword.Score != (Score{TP: 1}) {
		t.Errorf("keyword = %+v", got.Keyword)
	}
	if got.Jev.Score != (Score{TP: 1}) || got.Jev.Pairs != (PairScore{Score: Score{TP: 1}}) {
		t.Errorf("jev = %+v pairs %+v", got.Jev.Score, got.Jev.Pairs)
	}
	if got.Jev.Stats.Questions != 1 || got.Jev.Stats.Model != "jev-1.13.0" {
		t.Errorf("stats = %+v", got.Jev.Stats)
	}
}

func TestSummarizeAndWrite(t *testing.T) {
	r := Runner{Helm: fakeHelm(t), GitHub: fakeGitHub(t), Jev: fakeJev{}, Threshold: 0.5}
	results, err := r.Run(context.Background(), testCases(t))
	if err != nil {
		t.Fatal(err)
	}

	s := Summarize(results, 0.5)
	if s.Scored != 2 || s.Failed != 1 || s.Keys != 6 || s.Asked != 2 {
		t.Errorf("summary counts = %+v", s)
	}
	if s.Keyword.Score != (Score{TP: 2}) || s.Jev.Score != (Score{TP: 2}) || *s.Jev.Pairs != (PairScore{Score: Score{TP: 2}}) || s.Jev.CostUSD <= 0 {
		t.Errorf("summary keyword %+v jev %+v", s.Keyword, s.Jev)
	}
	if len(s.Sweep) != len(SweepThresholds) || s.Sweep[4].Score != (Score{TP: 1}) {
		t.Errorf("tune sweep = %+v", s.Sweep)
	}
	if s.Pick == nil || s.Pick.Threshold != 0.9 || s.Pick.Holdout != (Score{TP: 1}) || s.Pick.HoldoutPairs != (PairScore{Score: Score{TP: 1}}) {
		t.Errorf("pick = %+v", s.Pick)
	}

	var text bytes.Buffer
	if err := WriteText(&text, results, s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Cases: 2 scored, 1 failed. Jev was asked about 2 of 6 keys",
		"tune     tune     1/3",
		"1/0/0/0",
		"Picked 0.9 on tune by F1, ties going to the higher threshold. Holdout at 0.9: keys 1/0/0, pairs 1/0/0/0.",
		"missing-repo: github releases acme/missing",
	} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text report missing %q:\n%s", want, text.String())
		}
	}

	var js bytes.Buffer
	if err := WriteJSON(&js, results, s); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Summary Summary      `json:"summary"`
		Cases   []CaseResult `json:"cases"`
	}
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil || decoded.Summary.Pick == nil || decoded.Cases[0].Jev.Stats.Model != "jev-1.13.0" {
		t.Errorf("json report: %v %+v", err, decoded.Summary)
	}
}

func TestRunScoresMeaningChangedInCode(t *testing.T) {
	c := testCases(t)[0]
	c.To = "3.1.0"
	c.Affected = append(c.Affected, Label{Key: keyFine, Decision: DecisionMeaningChanged})
	r := Runner{Helm: fakeHelm(t), GitHub: fakeGitHub(t), Jev: fakeJev{}, Threshold: 0.5}
	results, err := r.Run(context.Background(), []Case{c})
	if err != nil {
		t.Fatal(err)
	}
	got := results[0]
	if got.Error != "" {
		t.Fatal(got.Error)
	}
	if !reflect.DeepEqual(got.Asked, []string{keyGone, keyFine}) || !reflect.DeepEqual(got.MeaningChanged, []string{keyFine}) || got.Code != (Score{TP: 1}) {
		t.Errorf("asked %v meaning changed %v code %+v", got.Asked, got.MeaningChanged, got.Code)
	}
	if got.Jev.Score != (Score{TP: 2}) || got.Jev.Pairs != (PairScore{Score: Score{TP: 1, FP: 1}}) {
		t.Errorf("jev = %+v pairs %+v, want the trap pair counted as fp", got.Jev.Score, got.Jev.Pairs)
	}

	s := Summarize(results, 0.5)
	var text bytes.Buffer
	if err := WriteText(&text, results, s); err != nil {
		t.Fatal(err)
	}
	if want := "code meaning_changed: 1/0/0"; s.Code != (Score{TP: 1}) || !strings.Contains(text.String(), want) {
		t.Errorf("code %+v, text missing %q:\n%s", s.Code, want, text.String())
	}
}

func TestRunLinksRemovedKeysToTypeChanges(t *testing.T) {
	c := testCases(t)[0]
	c.To = "3.2.0"
	r := Runner{Helm: fakeHelm(t), GitHub: fakeGitHub(t), Jev: fakeJev{}, Threshold: 0.5}
	results, err := r.Run(context.Background(), []Case{c})
	if err != nil {
		t.Fatal(err)
	}
	got := results[0]
	if got.Error != "" {
		t.Fatal(got.Error)
	}
	var asked []string
	for _, m := range got.Jev.Matches {
		if strings.HasPrefix(m.Text, "Faster http") {
			asked = append(asked, m.Key)
		}
	}
	slices.Sort(asked)
	if want := []string{keyGone, keyFine}; !reflect.DeepEqual(asked, want) || got.Jev.Stats.Questions != 4 {
		t.Errorf("keys asked about the port entry = %v (%d questions), want %v linked through app", asked, got.Jev.Stats.Questions, want)
	}
}

func TestRunKeywordOnly(t *testing.T) {
	results, err := Runner{Helm: fakeHelm(t), GitHub: fakeGitHub(t), Threshold: 0.5}.Run(context.Background(), testCases(t)[:1])
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Jev != nil || results[0].Keyword.Score != (Score{TP: 1}) {
		t.Errorf("result = %+v", results[0])
	}
	if s := Summarize(results, 0.5); s.Jev != nil || len(s.Sweep) != 0 || s.Pick != nil || s.Keyword.Score != (Score{TP: 1}) {
		t.Errorf("summary = %+v", s)
	}
}

func TestRunCaseErrors(t *testing.T) {
	tests := map[string]struct {
		edit func(*Case)
		want string
	}{
		"no releases in range":  {func(c *Case) { c.From, c.To = "2.9.0", "3.0.0"; c.TagPrefix = "x" }, "no releases of acme/app"},
		"chart version missing": {func(c *Case) { c.To = "9.9.9" }, "chart version not found"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := testCases(t)[0]
			tt.edit(&c)
			results, err := Runner{Helm: fakeHelm(t), GitHub: fakeGitHub(t), Threshold: 0.5}.Run(context.Background(), []Case{c})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(results[0].Error, tt.want) {
				t.Errorf("error = %q, want %q", results[0].Error, tt.want)
			}
		})
	}
}
