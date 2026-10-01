package match

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/sync/errgroup"

	"github.com/lov3g00d/helm-values-impact/internal/jev"
	"github.com/lov3g00d/helm-values-impact/internal/notes"
)

const minTermLen = 4

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
	Requests    int `json:"requests"`
	Questions   int `json:"questions"`
	InputTokens int `json:"inputTokens"`
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
	results := make([][]Match, len(candidates))
	usage := make([]int, len(candidates))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for i, c := range candidates {
		g.Go(func() error {
			ms, tokens, err := askOne(gctx, ev, c)
			if err != nil {
				return fmt.Errorf("%s entry %q: %w", c.Entry.Release, truncate(c.Entry.Text, 60), err)
			}
			results[i], usage[i] = ms, tokens
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, Stats{}, err
	}

	var (
		out   []Match
		stats Stats
	)
	for i, ms := range results {
		out = append(out, ms...)
		stats.Requests++
		stats.Questions += len(candidates[i].Keys)
		stats.InputTokens += usage[i]
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Probability != out[j].Probability {
			return out[i].Probability > out[j].Probability
		}
		return out[i].Key < out[j].Key
	})
	return out, stats, nil
}

func askOne(ctx context.Context, ev Evaluator, c Candidate) ([]Match, int, error) {
	questions := make(map[string]jev.Question, len(c.Keys))
	for i, k := range c.Keys {
		questions[fmt.Sprintf("k%d", i)] = question(k)
	}
	state := map[string]string{
		"release": c.Entry.Release,
		"section": c.Entry.Section,
		"entry":   c.Entry.Text,
	}

	resp, err := ev.Evaluate(ctx, state, questions)
	if err != nil {
		return nil, 0, err
	}

	out := make([]Match, 0, len(c.Keys))
	for i, k := range c.Keys {
		a, ok := resp.Answers[fmt.Sprintf("k%d", i)]
		if !ok {
			return nil, 0, fmt.Errorf("no answer for key %s", k)
		}
		out = append(out, Match{
			Key:         k,
			Probability: a.Noul,
			Release:     c.Entry.Release,
			Section:     c.Entry.Section,
			Text:        c.Entry.Text,
		})
	}
	return out, resp.Usage.InputTokens, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}
