package eval

import (
	"context"
	"fmt"

	"github.com/lov3g00d/helm-values-impact/internal/match"
	"github.com/lov3g00d/helm-values-impact/internal/notes"
)

const jevUSDPerInputMTok = 0.042

var SweepThresholds = []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}

type JevCase struct {
	Predicted []string      `json:"predicted"`
	Score     Score         `json:"score"`
	Stats     match.Stats   `json:"stats"`
	Matches   []match.Match `json:"matches"`
}

type KeywordCase struct {
	Predicted []string `json:"predicted"`
	Score     Score    `json:"score"`
}

type CaseResult struct {
	Case     Case        `json:"case"`
	Releases int         `json:"releases"`
	Entries  int         `json:"entries"`
	Keyword  KeywordCase `json:"keyword"`
	Jev      *JevCase    `json:"jev,omitempty"`
	Error    string      `json:"error,omitempty"`
}

type Runner struct {
	GitHub    *notes.GitHub
	Jev       match.Evaluator
	Threshold float64
}

func (r Runner) Run(ctx context.Context, cases []Case) ([]CaseResult, error) {
	out := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		res, err := r.runCase(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			res = CaseResult{Case: c, Error: err.Error()}
		}
		out = append(out, res)
	}
	return out, nil
}

func (r Runner) runCase(ctx context.Context, c Case) (CaseResult, error) {
	res := CaseResult{Case: c}
	req := match.Request{Repo: c.AppRepo, TagPrefix: c.TagPrefix, From: c.From, To: c.To, Keys: c.Keys}

	var (
		releases []notes.Release
		entries  []notes.Entry
	)
	if r.Jev != nil {
		out, err := match.CheckReleaseNotes(ctx, r.GitHub, r.Jev, req)
		if err != nil {
			return res, fmt.Errorf("jev: %w", err)
		}
		releases, entries = out.Releases, out.Entries
		predicted := KeysAbove(out.Matches, r.Threshold)
		res.Jev = &JevCase{Predicted: predicted, Score: ScoreKeys(predicted, c.Affected), Stats: out.Stats, Matches: out.Matches}
	} else {
		var err error
		releases, err = r.GitHub.Releases(ctx, c.AppRepo, c.TagPrefix, c.From, c.To)
		if err != nil {
			return res, err
		}
		entries = match.SplitAll(releases)
	}

	res.Releases, res.Entries = len(releases), len(entries)
	if len(releases) == 0 {
		return res, fmt.Errorf("no releases of %s in (%s, %s]", c.AppRepo, c.From, c.To)
	}

	predicted := match.KeywordKeys(entries, req)
	res.Keyword = KeywordCase{Predicted: predicted, Score: ScoreKeys(predicted, c.Affected)}
	return res, nil
}

type Side struct {
	Score       Score   `json:"score"`
	Requests    int     `json:"requests"`
	InputTokens int64   `json:"inputTokens"`
	CostUSD     float64 `json:"estimatedCostUSD"`
}

type SweepPoint struct {
	Threshold float64 `json:"threshold"`
	Score     Score   `json:"score"`
}

type Summary struct {
	Scored    int          `json:"scored"`
	Failed    int          `json:"failed"`
	Threshold float64      `json:"threshold"`
	Keyword   Side         `json:"keyword"`
	Jev       *Side        `json:"jev,omitempty"`
	Sweep     []SweepPoint `json:"jevSweep,omitempty"`
}

func Summarize(results []CaseResult, threshold float64) Summary {
	s := Summary{Threshold: threshold}
	sweep := make([]Score, len(SweepThresholds))
	for _, r := range results {
		if r.Error != "" {
			s.Failed++
			continue
		}
		s.Scored++
		s.Keyword.Score = s.Keyword.Score.Add(r.Keyword.Score)
		if r.Jev == nil {
			continue
		}
		if s.Jev == nil {
			s.Jev = &Side{}
		}
		s.Jev.Score = s.Jev.Score.Add(r.Jev.Score)
		s.Jev.Requests += r.Jev.Stats.Requests
		s.Jev.InputTokens += int64(r.Jev.Stats.InputTokens)
		for i, t := range SweepThresholds {
			sweep[i] = sweep[i].Add(ScoreKeys(KeysAbove(r.Jev.Matches, t), r.Case.Affected))
		}
	}
	if s.Jev != nil {
		s.Jev.CostUSD = float64(s.Jev.InputTokens) * jevUSDPerInputMTok / 1e6
		for i, t := range SweepThresholds {
			s.Sweep = append(s.Sweep, SweepPoint{Threshold: t, Score: sweep[i]})
		}
	}
	return s
}
