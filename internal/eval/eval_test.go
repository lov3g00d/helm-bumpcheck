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
	"strings"
	"testing"
	"time"

	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
)

const (
	keyGone  = "app.processor.local_blocks.flush_to_storage"
	keyFine  = "app.server.http_listen_port"
	keyOther = "app.storage.trace.backend"
)

func writeCases(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cases.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCases(t *testing.T) {
	path := writeCases(t, `
cases:
  - name: one
    appRepo: acme/app
    from: 1.0.0
    to: 2.0.0
    keys: [a.b, c.d]
    affected: [a.b]
`)
	cases, err := LoadCases(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Case{{Name: "one", AppRepo: "acme/app", From: "1.0.0", To: "2.0.0", Keys: []string{"a.b", "c.d"}, Affected: []string{"a.b"}}}
	if !reflect.DeepEqual(cases, want) {
		t.Errorf("cases = %+v", cases)
	}
}

func TestLoadCasesRejects(t *testing.T) {
	tests := map[string]string{
		"affected not in keys": "cases:\n  - {name: x, appRepo: a/b, from: 1.0.0, to: 2.0.0, keys: [k], affected: [other]}\n",
		"missing repo":         "cases:\n  - {name: x, from: 1.0.0, to: 2.0.0, keys: [k]}\n",
		"no keys":              "cases:\n  - {name: x, appRepo: a/b, from: 1.0.0, to: 2.0.0}\n",
		"duplicate name":       "cases:\n  - {name: x, appRepo: a/b, from: 1.0.0, to: 2.0.0, keys: [k]}\n  - {name: x, appRepo: a/b, from: 1.0.0, to: 2.0.0, keys: [k]}\n",
		"unknown field":        "cases:\n  - {name: x, appRepo: a/b, from: 1.0.0, to: 2.0.0, keys: [k], affectd: [k]}\n",
		"empty":                "cases: []\n",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadCases(writeCases(t, body)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestExampleCasesLoad(t *testing.T) {
	if _, err := LoadCases("../../eval/cases.example.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestScore(t *testing.T) {
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
	if _, ok := (Score{}).Precision(); ok {
		t.Error("precision with no predictions should be undefined")
	}
	if _, ok := (Score{FP: 2}).Recall(); ok {
		t.Error("recall with no expected keys should be undefined")
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
	resp := &jev.Response{Answers: map[string]jev.Answer{}, Usage: jev.Usage{InputTokens: 200}}
	for id, q := range questions {
		key := q.Instructions.(map[string]any)["config_key"].(string)
		p := 0.05
		switch {
		case strings.Contains(entry, "localblocks") && key == keyGone:
			p = 0.92
		case strings.Contains(entry, "http listen port") && key == keyFine:
			p = 0.4
		}
		resp.Answers[id] = jev.Answer{Type: "noul", Noul: p}
	}
	return resp, nil
}

func TestRunAndSummarize(t *testing.T) {
	cases := []Case{
		{Name: "localblocks", AppRepo: "acme/app", From: "2.9.0", To: "3.0.0", Keys: []string{keyGone, keyFine, keyOther}, Affected: []string{keyGone}},
		{Name: "missing-repo", AppRepo: "acme/missing", From: "1.0.0", To: "2.0.0", Keys: []string{keyFine}},
	}
	r := Runner{GitHub: fakeGitHub(t), Jev: fakeJev{}, Threshold: 0.5}

	results, err := r.Run(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !strings.Contains(results[1].Error, "404") {
		t.Fatalf("results = %+v", results)
	}
	got := results[0]
	if got.Releases != 1 || got.Entries != 2 {
		t.Errorf("releases %d entries %d", got.Releases, got.Entries)
	}
	if !reflect.DeepEqual(got.Keyword.Predicted, []string{keyGone, keyFine}) || got.Keyword.Score != (Score{TP: 1, FP: 1}) {
		t.Errorf("keyword = %+v", got.Keyword)
	}
	if got.Jev.Score != (Score{TP: 1}) {
		t.Errorf("jev = %+v", got.Jev.Score)
	}

	s := Summarize(results, 0.5)
	if s.Scored != 1 || s.Failed != 1 {
		t.Errorf("summary counts = %+v", s)
	}
	if s.Keyword.Score != (Score{TP: 1, FP: 1}) || s.Jev.Score != (Score{TP: 1}) || s.Jev.CostUSD <= 0 {
		t.Errorf("summary keyword %+v jev %+v", s.Keyword, s.Jev)
	}
	if p := s.Sweep[3]; p.Threshold != 0.4 || p.Score != (Score{TP: 1, FP: 1}) {
		t.Errorf("sweep at 0.4 = %+v", p)
	}

	var text bytes.Buffer
	if err := WriteText(&text, results, s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cases: 1 scored, 1 failed", "keyword", "jev@0.50", "1/1/0", "1/0/0", "missing-repo: jev: github releases acme/missing"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text report missing %q:\n%s", want, text.String())
		}
	}

	var js bytes.Buffer
	if err := WriteJSON(&js, results, s); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Summary Summary `json:"summary"`
	}
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil || decoded.Summary.Scored != 1 {
		t.Errorf("json report: %v %+v", err, decoded.Summary)
	}
}

func TestRunKeywordOnly(t *testing.T) {
	cases := []Case{{Name: "c", AppRepo: "acme/app", From: "2.9.0", To: "3.0.0", Keys: []string{keyGone, keyOther}, Affected: []string{keyGone}}}
	results, err := Runner{GitHub: fakeGitHub(t), Threshold: 0.5}.Run(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Jev != nil || results[0].Keyword.Score != (Score{TP: 1}) {
		t.Errorf("result = %+v", results[0])
	}
	if s := Summarize(results, 0.5); s.Jev != nil || len(s.Sweep) != 0 || s.Keyword.Score != (Score{TP: 1}) {
		t.Errorf("summary = %+v", s)
	}
}

func TestRunNoReleasesInRange(t *testing.T) {
	cases := []Case{{Name: "c", AppRepo: "acme/app", From: "5.0.0", To: "6.0.0", Keys: []string{keyGone}}}
	results, err := Runner{GitHub: fakeGitHub(t), Threshold: 0.5}.Run(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(results[0].Error, "no releases of acme/app") {
		t.Errorf("error = %q", results[0].Error)
	}
}
