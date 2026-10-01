package match

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
)

func TestTerms(t *testing.T) {
	ignore := map[string]bool{"tempo": true}
	tests := []struct {
		key  string
		want []string
	}{
		{"tempo.metricsGenerator.processor.local_blocks.flush_to_storage", []string{"metricsgenerator", "processor", "localblocks", "flushtostorage"}},
		{`tempo.podAnnotations["prometheus.io/scrape"]`, []string{"podannotations", "prometheus", "scrape"}},
		{"tempo.server.http_listen_port", []string{"server", "httplistenport"}},
		{"persistence.enabled", []string{"persistence"}},
		{"a.b.cfg", nil},
	}
	for _, tt := range tests {
		if got := Terms(tt.key, ignore); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Terms(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestCandidates(t *testing.T) {
	entries := []notes.Entry{
		{Release: "v3.0.0", Section: "Breaking Changes", Text: "Removed metrics-generator localblocks processor"},
		{Release: "v2.9.0", Section: "Features", Text: "Add `flush_to_storage` option to local blocks"},
		{Release: "v2.9.0", Section: "Bugfixes", Text: "Fix tempo-query crash on startup"},
		{Release: "v2.8.0", Section: "Enhancements", Text: "Expose http listen port metric"},
	}
	keys := []string{
		"tempo.metricsGenerator.processor.local_blocks.flush_to_storage",
		"tempo.server.http_listen_port",
	}
	got := Candidates(entries, keys, []string{"tempo"})

	want := []Candidate{
		{Entry: entries[0], Keys: keys},
		{Entry: entries[1], Keys: keys[:1]},
		{Entry: entries[3], Keys: keys[1:]},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Candidates =\n%+v\nwant\n%+v", got, want)
	}
}

type fakeEvaluator struct {
	mu       sync.Mutex
	inFlight atomic.Int32
	maxSeen  int32
	calls    int
	answer   func(entry string, key string) float64
	err      error
}

func (f *fakeEvaluator) Evaluate(ctx context.Context, state any, questions map[string]jev.Question) (*jev.Response, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	f.mu.Lock()
	f.calls++
	if n > f.maxSeen {
		f.maxSeen = n
	}
	f.mu.Unlock()
	time.Sleep(5 * time.Millisecond)

	if f.err != nil {
		return nil, f.err
	}
	entry := state.(map[string]string)["entry"]
	resp := &jev.Response{Answers: map[string]jev.Answer{}, Usage: jev.Usage{InputTokens: 100}}
	for id, q := range questions {
		key := q.Instructions.(map[string]any)["config_key"].(string)
		resp.Answers[id] = jev.Answer{Type: "noul", Noul: f.answer(entry, key)}
	}
	return resp, nil
}

func TestAsk(t *testing.T) {
	keyA := "tempo.metricsGenerator.processor.local_blocks.flush_to_storage"
	keyB := "tempo.server.http_listen_port"
	candidates := []Candidate{
		{Entry: notes.Entry{Release: "v3.0.0", Section: "Breaking Changes", Text: "Removed localblocks processor"}, Keys: []string{keyA, keyB}},
		{Entry: notes.Entry{Release: "v2.9.0", Section: "Features", Text: "Default http port is now 3200"}, Keys: []string{keyB}},
		{Entry: notes.Entry{Release: "v2.8.0", Section: "Features", Text: "unrelated"}, Keys: []string{keyA}},
	}
	ev := &fakeEvaluator{answer: func(entry, key string) float64 {
		switch {
		case strings.Contains(entry, "localblocks") && key == keyA:
			return 0.9
		case strings.Contains(entry, "3200") && key == keyB:
			return 0.8
		}
		return 0.05
	}}

	got, stats, err := Ask(context.Background(), ev, candidates, 2)
	if err != nil {
		t.Fatal(err)
	}

	var summary []string
	for _, m := range got {
		summary = append(summary, m.Release+" "+m.Key)
	}
	want := []string{
		"v3.0.0 " + keyA,
		"v2.9.0 " + keyB,
		"v2.8.0 " + keyA,
		"v3.0.0 " + keyB,
	}
	if !reflect.DeepEqual(summary, want) {
		t.Errorf("order =\n%q\nwant\n%q", summary, want)
	}
	if got[0].Probability != 0.9 || got[0].Section != "Breaking Changes" {
		t.Errorf("top match = %+v", got[0])
	}
	if stats != (Stats{Requests: 3, Questions: 4, InputTokens: 300}) {
		t.Errorf("stats = %+v", stats)
	}
	if ev.maxSeen > 2 {
		t.Errorf("max concurrent requests = %d, want <= 2", ev.maxSeen)
	}
}

func TestAskPropagatesError(t *testing.T) {
	ev := &fakeEvaluator{err: errors.New("boom")}
	candidates := []Candidate{{Entry: notes.Entry{Release: "v1.0.0", Text: "x"}, Keys: []string{"a.key"}}}
	if _, _, err := Ask(context.Background(), ev, candidates, 4); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

func TestAskNothingToDo(t *testing.T) {
	ev := &fakeEvaluator{}
	got, stats, err := Ask(context.Background(), ev, nil, 4)
	if err != nil || len(got) != 0 || stats != (Stats{}) || ev.calls != 0 {
		t.Errorf("got %v %+v %v calls=%d", got, stats, err, ev.calls)
	}
}

func TestLiveLocalBlocks(t *testing.T) {
	if os.Getenv("LIVE") != "1" || os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("set LIVE=1 and TYPESAFE_API_KEY to call the real API")
	}
	c, err := jev.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []Candidate{{
		Entry: notes.Entry{Release: "v3.0.0", Section: "Breaking Changes", Text: "Removed metrics-generator localblocks processor and related local block storage plumbing by @javiermolinar in #6555"},
		Keys: []string{
			"tempo.metricsGenerator.processor.local_blocks.flush_to_storage",
			"tempo.server.http_listen_port",
		},
	}}
	got, stats, err := Ask(context.Background(), c, candidates, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		t.Logf("%.3f %s", m.Probability, m.Key)
	}
	t.Logf("stats %+v", stats)
}

func TestKeywordKeys(t *testing.T) {
	entries := []notes.Entry{
		{Section: "Breaking Changes", Text: "Removed metrics-generator localblocks processor"},
		{Section: "Features", Text: "Tempo now ships a new UI"},
	}
	req := Request{
		Repo: "grafana/tempo",
		Keys: []string{
			"tempo.server.http_listen_port",
			"tempo.metricsGenerator.processor.local_blocks.flush_to_storage",
			"tempo.retention",
		},
	}
	got := KeywordKeys(entries, req)
	if want := []string{"tempo.metricsGenerator.processor.local_blocks.flush_to_storage"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeywordKeys = %v, want %v", got, want)
	}
}

func TestSplitAllDropsBoilerplate(t *testing.T) {
	rel := notes.Release{Tag: "velero-4.0.0", Body: "A Helm chart for velero\n\n## What's Changed\n* [velero] Add support for multiple locations by @a in #413\n\n## New Contributors\n* @a made their first contribution in #413\n\n**Full Changelog**: https://github.com/x/y/compare/a...b"}
	got := SplitAll([]notes.Release{rel})
	var texts []string
	for _, e := range got {
		texts = append(texts, e.Text)
	}
	want := []string{"A Helm chart for velero", "[velero] Add support for multiple locations by @a in #413"}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("SplitAll texts = %q, want %q", texts, want)
	}
}
