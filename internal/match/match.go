package match

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/sync/errgroup"

	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
)

const (
	minTermLen         = 4
	DefaultConcurrency = 8
	maxEntryChars      = 4000
)

var genericTerms = map[string]bool{
	"enabled":          true,
	"config":           true,
	"structuredconfig": true,
	"values":           true,
	"global":           true,
	"annotations":      true,
	"labels":           true,
}

var alwaysAskSections = []string{"breaking", "deprecat", "remov", "upgrade", "migrat"}

type Candidate struct {
	Entry notes.Entry
	Keys  []string
}

type Match struct {
	Key         string  `json:"key"`
	Probability float64 `json:"probability"`
	Release     string  `json:"release"`
	Section     string  `json:"section"`
	Text        string  `json:"text"`
}

type Stats struct {
	Requests    int    `json:"requests"`
	Failed      int    `json:"failed"`
	Questions   int    `json:"questions"`
	InputTokens int    `json:"inputTokens"`
	Cached      int    `json:"cached,omitempty"`
	Model       string `json:"model,omitempty"`
}

type Evaluator interface {
	Evaluate(ctx context.Context, state any, questions map[string]jev.Question) (*jev.Response, error)
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func Terms(key string, ignore map[string]bool) []string {
	fields := strings.FieldsFunc(key, func(r rune) bool {
		return r == '.' || r == '[' || r == ']' || r == '"' || r == '/'
	})
	var out []string
	seen := map[string]bool{}
	for _, f := range fields {
		t := normalize(f)
		if len(t) < minTermLen || genericTerms[t] || ignore[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func alwaysAsk(section string) bool {
	s := strings.ToLower(section)
	for _, p := range alwaysAskSections {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func Candidates(entries []notes.Entry, keys []string, ignore []string) []Candidate {
	ignored := map[string]bool{}
	for _, i := range ignore {
		ignored[normalize(i)] = true
	}
	terms := make(map[string][]string, len(keys))
	for _, k := range keys {
		terms[k] = Terms(k, ignored)
	}

	var out []Candidate
	for _, e := range entries {
		text := normalize(e.Text)
		all := alwaysAsk(e.Section)
		var matched []string
		for _, k := range keys {
			if all || containsAny(text, terms[k]) {
				matched = append(matched, k)
			}
		}
		if len(matched) > 0 {
			out = append(out, Candidate{Entry: e, Keys: matched})
		}
	}
	return out
}

func containsAny(text string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(text, t) {
			return true
		}
	}
	return false
}

func question(key string) jev.Question {
	return jev.NoulWithCriteria(
		map[string]any{
			"config_key": key,
			"question":   "Does the release-note `entry` describe a change that affects the setting `config_key`, or the component that setting configures?",
		},
		"The entry removes, renames, moves or changes the default or meaning of `config_key`, or removes or changes the component it configures.",
		"The entry is about something else, or only shares a similar word without affecting `config_key`.",
	)
}

func Ask(ctx context.Context, ev Evaluator, candidates []Candidate, concurrency int) ([]Match, Stats, error) {
	type outcome struct {
		matches []Match
		tokens  int
		model   string
		err     error
	}
	results := make([]outcome, len(candidates))

	var g errgroup.Group
	g.SetLimit(max(concurrency, 1))
	for i, c := range candidates {
		g.Go(func() error {
			ms, resp, err := askOne(ctx, ev, c)
			if err != nil {
				results[i].err = fmt.Errorf("%s entry %q: %w", c.Entry.Release, truncate(c.Entry.Text, 60), err)
				return nil
			}
			results[i] = outcome{matches: ms, tokens: resp.Usage.InputTokens, model: resp.Model}
			return nil
		})
	}
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, Stats{}, err
	}

	var (
		out      []Match
		stats    Stats
		firstErr error
	)
	for i, r := range results {
		stats.Requests++
		if r.err != nil {
			stats.Failed++
			firstErr = cmp.Or(firstErr, r.err)
			continue
		}
		out = append(out, r.matches...)
		stats.Questions += len(candidates[i].Keys)
		stats.InputTokens += r.tokens
		stats.Model = cmp.Or(stats.Model, r.model)
	}
	if stats.Failed > 0 && stats.Failed == stats.Requests {
		return nil, stats, fmt.Errorf("all %d Jev requests failed: %w", stats.Failed, firstErr)
	}
	slices.SortStableFunc(out, func(a, b Match) int {
		return cmp.Or(cmp.Compare(b.Probability, a.Probability), cmp.Compare(a.Key, b.Key))
	})
	return out, stats, nil
}

func askOne(ctx context.Context, ev Evaluator, c Candidate) ([]Match, *jev.Response, error) {
	questions := make(map[string]jev.Question, len(c.Keys))
	for i, k := range c.Keys {
		questions[fmt.Sprintf("k%d", i)] = question(k)
	}
	state := map[string]string{
		"release": c.Entry.Release,
		"section": c.Entry.Section,
		"entry":   truncate(c.Entry.Text, maxEntryChars),
	}

	resp, err := ev.Evaluate(ctx, state, questions)
	if err != nil {
		return nil, nil, err
	}

	out := make([]Match, 0, len(c.Keys))
	for i, k := range c.Keys {
		a, ok := resp.Answers[fmt.Sprintf("k%d", i)]
		if !ok {
			return nil, nil, fmt.Errorf("no answer for key %s", k)
		}
		out = append(out, Match{
			Key:         k,
			Probability: a.Noul,
			Release:     c.Entry.Release,
			Section:     c.Entry.Section,
			Text:        c.Entry.Text,
		})
	}
	return out, resp, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

type Request struct {
	Repo      string
	TagPrefix string
	From      string
	To        string
	Chart     string
	Keys      []string
	Linked    map[string][]string
}

func Fetch(ctx context.Context, gh *notes.GitHub, req Request) ([]notes.Release, []notes.Entry, *notes.Truncation, error) {
	releases, trunc, err := gh.Releases(ctx, req.Repo, req.TagPrefix, req.From, req.To)
	if err != nil {
		return nil, nil, nil, err
	}
	return releases, notes.SplitAll(releases), trunc, nil
}

func (r Request) ignore() []string {
	out := []string{path.Base(r.Repo)}
	if r.Chart != "" {
		out = append(out, path.Base(r.Chart))
	}
	return out
}

func (r Request) Candidates(entries []notes.Entry) []Candidate {
	out := Candidates(entries, r.Keys, r.ignore())
	if len(r.Linked) == 0 {
		return out
	}
	for i, c := range out {
		var keys []string
		for _, k := range r.Keys {
			if slices.Contains(c.Keys, k) || slices.ContainsFunc(r.Linked[k], func(l string) bool { return slices.Contains(c.Keys, l) }) {
				keys = append(keys, k)
			}
		}
		out[i].Keys = keys
	}
	return out
}

type Memo struct {
	answers map[memoKey]Match
	model   string
}

type memoKey struct {
	repo, release, section, text, key string
}

func NewMemo() *Memo {
	return &Memo{answers: map[memoKey]Match{}}
}

func AskMemo(ctx context.Context, ev Evaluator, memo *Memo, repo string, candidates []Candidate, concurrency int) ([]Match, Stats, error) {
	var (
		known   []Match
		pending []Candidate
	)
	for _, c := range candidates {
		var missing []string
		for _, k := range c.Keys {
			if m, ok := memo.answers[memoKey{repo, c.Entry.Release, c.Entry.Section, c.Entry.Text, k}]; ok {
				known = append(known, m)
			} else {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			pending = append(pending, Candidate{Entry: c.Entry, Keys: missing})
		}
	}
	fresh, stats, err := Ask(ctx, ev, pending, concurrency)
	if err != nil && (len(known) == 0 || ctx.Err() != nil) {
		return nil, stats, err
	}
	for _, m := range fresh {
		memo.answers[memoKey{repo, m.Release, m.Section, m.Text, m.Key}] = m
	}
	memo.model = cmp.Or(stats.Model, memo.model)
	stats.Cached = len(known)
	if len(known) > 0 {
		stats.Model = cmp.Or(stats.Model, memo.model)
	}
	out := append(known, fresh...)
	slices.SortStableFunc(out, func(a, b Match) int {
		return cmp.Or(cmp.Compare(b.Probability, a.Probability), cmp.Compare(a.Key, b.Key))
	})
	return out, stats, nil
}

func KeywordKeys(entries []notes.Entry, req Request) []string {
	ignored := map[string]bool{}
	for _, i := range req.ignore() {
		ignored[normalize(i)] = true
	}
	var out []string
	for _, k := range req.Keys {
		terms := Terms(k, ignored)
		for _, e := range entries {
			if containsAny(normalize(e.Text), terms) {
				out = append(out, k)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}
