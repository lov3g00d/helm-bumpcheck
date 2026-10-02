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

type flakyEvaluator struct{}

func (flakyEvaluator) Evaluate(ctx context.Context, state any, questions map[string]jev.Question) (*jev.Response, error) {
	if strings.Contains(state.(map[string]string)["entry"], "flaky") {
		return nil, errors.New("502 bad gateway")
	}
	resp := &jev.Response{Model: "jev-1.13.0", Answers: map[string]jev.Answer{}, Usage: jev.Usage{InputTokens: 10}}
	for id := range questions {
		resp.Answers[id] = jev.Answer{Type: "noul", Noul: 0.9}
	}
	return resp, nil
}

func TestAskKeepsPartialResults(t *testing.T) {
	candidates := []Candidate{
		{Entry: notes.Entry{Release: "v1", Text: "removed thing"}, Keys: []string{"a.key"}},
		{Entry: notes.Entry{Release: "v2", Text: "flaky entry"}, Keys: []string{"a.key", "b.key"}},
	}
	got, stats, err := Ask(context.Background(), flakyEvaluator{}, candidates, 0)
	if err != nil {
		t.Fatalf("err = %v, want partial results", err)
	}
	if len(got) != 1 || got[0].Release != "v1" {
		t.Errorf("matches = %+v", got)
	}
	want := Stats{Requests: 2, Failed: 1, Questions: 1, InputTokens: 10, Model: "jev-1.13.0"}
	if stats != want {
		t.Errorf("stats = %+v, want %+v", stats, want)
	}
}

func TestAskTruncatesLongEntries(t *testing.T) {
	var seen int
	ev := evaluatorFunc(func(state any) {
		seen = len([]rune(state.(map[string]string)["entry"]))
	})
	candidates := []Candidate{{Entry: notes.Entry{Release: "v1", Text: strings.Repeat("x", 10000)}, Keys: []string{"k"}}}
	if _, _, err := Ask(context.Background(), ev, candidates, 1); err != nil {
		t.Fatal(err)
	}
	if seen != maxEntryChars {
		t.Errorf("entry sent with %d chars, want %d", seen, maxEntryChars)
	}
}

type evaluatorFunc func(state any)

func (f evaluatorFunc) Evaluate(ctx context.Context, state any, questions map[string]jev.Question) (*jev.Response, error) {
	f(state)
	resp := &jev.Response{Answers: map[string]jev.Answer{}}
	for id := range questions {
		resp.Answers[id] = jev.Answer{Type: "noul", Noul: 0.1}
	}
	return resp, nil
}

func TestAskMemoReusesAnswers(t *testing.T) {
	ev := &fakeEvaluator{answer: func(entry, key string) float64 { return 0.7 }}
	memo := NewMemo()
	candidates := []Candidate{{Entry: notes.Entry{Release: "v1", Text: "removed x"}, Keys: []string{"a.key", "b.key"}}}

	first, stats, err := AskMemo(context.Background(), ev, memo, "acme/app", candidates, 2)
	if err != nil || len(first) != 2 || stats.Questions != 2 {
		t.Fatalf("first = %v %+v %v", first, stats, err)
	}
	more := []Candidate{{Entry: candidates[0].Entry, Keys: []string{"a.key", "c.key"}}}
	second, stats, err := AskMemo(context.Background(), ev, memo, "acme/app", more, 2)
	if err != nil || len(second) != 2 || stats.Questions != 1 || ev.calls != 2 {
		t.Errorf("second = %v %+v %v calls=%d, want only c.key asked", second, stats, err, ev.calls)
	}
}

type missingAnswerEvaluator struct{}

func (missingAnswerEvaluator) Evaluate(ctx context.Context, state any, questions map[string]jev.Question) (*jev.Response, error) {
	return &jev.Response{Answers: map[string]jev.Answer{}}, nil
}

func TestAskReportsMissingAnswers(t *testing.T) {
	candidates := []Candidate{{Entry: notes.Entry{Release: "v1", Text: "x"}, Keys: []string{"a.key"}}}
	_, stats, err := Ask(context.Background(), missingAnswerEvaluator{}, candidates, 1)
	if err == nil || !strings.Contains(err.Error(), "no answer for key a.key") || stats.Failed != 1 {
		t.Errorf("err = %v stats = %+v", err, stats)
	}
}

func TestAskMemoKeysBySourceAndSection(t *testing.T) {
	ev := &fakeEvaluator{answer: func(entry, key string) float64 { return 0.7 }}
	memo := NewMemo()
	entry := notes.Entry{Release: "v1", Section: "Features", Text: "removed x"}
	ask := func(repo string, e notes.Entry) {
		t.Helper()
		if _, _, err := AskMemo(context.Background(), ev, memo, repo, []Candidate{{Entry: e, Keys: []string{"a.key"}}}, 1); err != nil {
			t.Fatal(err)
		}
	}
	ask("acme/app", entry)
	ask("acme/app", entry)
	ask("acme/charts", entry)
	moved := entry
	moved.Section = "Breaking Changes"
	ask("acme/app", moved)
	if ev.calls != 3 {
		t.Errorf("calls = %d, want 3: one per repo and section, none for the repeat", ev.calls)
	}
}

func TestAskMemoKeepsCachedAnswersWhenRequestsFail(t *testing.T) {
	memo := NewMemo()
	cached := Candidate{Entry: notes.Entry{Release: "v1", Text: "removed thing"}, Keys: []string{"a.key"}}
	if _, _, err := AskMemo(context.Background(), flakyEvaluator{}, memo, "acme/app", []Candidate{cached}, 1); err != nil {
		t.Fatal(err)
	}

	failing := Candidate{Entry: notes.Entry{Release: "v2", Text: "flaky entry"}, Keys: []string{"b.key"}}
	got, stats, err := AskMemo(context.Background(), flakyEvaluator{}, memo, "acme/app", []Candidate{cached, failing}, 1)
	if err != nil {
		t.Fatalf("err = %v, want the cached answer", err)
	}
	if len(got) != 1 || got[0].Key != "a.key" {
		t.Errorf("matches = %+v", got)
	}
	if want := (Stats{Requests: 1, Failed: 1, Cached: 1, Model: "jev-1.13.0"}); stats != want {
		t.Errorf("stats = %+v, want %+v", stats, want)
	}

	_, stats, err = AskMemo(context.Background(), flakyEvaluator{}, memo, "acme/app", []Candidate{cached}, 1)
	if want := (Stats{Cached: 1, Model: "jev-1.13.0"}); err != nil || stats != want {
		t.Errorf("fully cached: stats = %+v, err %v, want %+v", stats, err, want)
	}

	if _, _, err := AskMemo(context.Background(), flakyEvaluator{}, memo, "acme/app", []Candidate{failing}, 1); err == nil {
		t.Error("expected an error when nothing is cached and every request fails")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := AskMemo(ctx, flakyEvaluator{}, memo, "acme/app", []Candidate{cached, failing}, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
