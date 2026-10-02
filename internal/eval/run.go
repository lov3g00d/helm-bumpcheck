package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const jevUSDPerInputMTok = 0.042

var SweepThresholds = []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}

type JevCase struct {
	Predicted []string      `json:"predicted"`
	Score     Score         `json:"score"`
	Pairs     PairScore     `json:"pairs"`
	Stats     match.Stats   `json:"stats"`
	Matches   []match.Match `json:"matches"`
}

type KeywordCase struct {
	Predicted []string `json:"predicted"`
	Score     Score    `json:"score"`
}

type CaseResult struct {
	Case           Case        `json:"case"`
	Keys           int         `json:"keys"`
	Asked          []string    `json:"asked"`
	MeaningChanged []string    `json:"meaningChanged"`
	Code           Score       `json:"code"`
	Releases       int         `json:"releases"`
	Entries        int         `json:"entries"`
	Keyword        KeywordCase `json:"keyword"`
	Jev            *JevCase    `json:"jev,omitempty"`
	Error          string      `json:"error,omitempty"`
}

type Runner struct {
	Helm      helm.Runner
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
	user, err := values.LoadFiles([]string{c.ValuesFile})
	if err != nil {
		return res, err
	}
	rep, err := r.compare(ctx, c, user)
	if err != nil {
		return res, err
	}
	req := match.Request{Repo: c.AppRepo, TagPrefix: c.TagPrefix, From: c.From, To: c.To, Chart: c.Chart.Name, Keys: rep.NoteKeys(), Linked: rep.Linked()}
	res.Keys, res.Asked = len(values.Flatten(user)), req.Keys
	for _, mc := range rep.MeaningChanged {
		res.MeaningChanged = append(res.MeaningChanged, mc.Path)
	}
	res.Code = ScoreKeys(res.MeaningChanged, c.AffectedKeys(DecisionMeaningChanged))

	releases, entries, _, err := match.Fetch(ctx, r.GitHub, req)
	if err != nil {
		return res, err
	}
	res.Releases, res.Entries = len(releases), len(entries)
	if len(releases) == 0 {
		return res, fmt.Errorf("no releases of %s in (%s, %s]", c.AppRepo, c.From, c.To)
	}

	affected := c.AffectedKeys()
	predicted := match.KeywordKeys(entries, req)
	res.Keyword = KeywordCase{Predicted: predicted, Score: ScoreKeys(predicted, affected)}

	if r.Jev != nil {
		matches, stats, err := match.Ask(ctx, r.Jev, req.Candidates(entries), match.DefaultConcurrency)
		if err != nil {
			return res, fmt.Errorf("jev: %w", err)
		}
		predicted := KeysAbove(matches, r.Threshold)
		res.Jev = &JevCase{
			Predicted: predicted,
			Score:     ScoreKeys(predicted, affected),
			Pairs:     ScorePairs(matches, r.Threshold, c.Evidence, c.Traps),
			Stats:     stats,
			Matches:   matches,
		}
	}
	return res, nil
}

func (r Runner) compare(ctx context.Context, c Case, user map[string]any) (values.Report, error) {
	work, err := os.MkdirTemp("", "bumpcheck-eval-")
	if err != nil {
		return values.Report{}, err
	}
	defer os.RemoveAll(work)

	oldDefaults, _, err := r.defaults(ctx, c, c.From, filepath.Join(work, "old"))
	if err != nil {
		return values.Report{}, err
	}
	newDefaults, meta, err := r.defaults(ctx, c, c.To, filepath.Join(work, "new"))
	if err != nil {
		return values.Report{}, err
	}
	return values.Compare(oldDefaults, newDefaults, user, meta.DependencyNames()), nil
}

func (r Runner) defaults(ctx context.Context, c Case, version, dest string) (map[string]any, helm.Metadata, error) {
	dir, err := r.Helm.Pull(ctx, helm.Chart{Ref: c.Chart.Name, Version: version, Repo: c.Chart.Repo}, dest)
	if err != nil {
		return nil, helm.Metadata{}, err
	}
	data, meta, err := helm.LoadDir(dir)
	if err != nil {
		return nil, helm.Metadata{}, err
	}
	defaults, err := values.Parse(data)
	if err != nil {
		return nil, helm.Metadata{}, fmt.Errorf("parse values of %s %s: %w", c.Chart.Name, version, err)
	}
	return defaults, meta, nil
}

type Side struct {
	Score       Score      `json:"score"`
	Pairs       *PairScore `json:"pairs,omitempty"`
	Questions   int        `json:"questions"`
	Requests    int        `json:"requests"`
	InputTokens int64      `json:"inputTokens"`
	CostUSD     float64    `json:"estimatedCostUSD"`
}

type SweepPoint struct {
	Threshold float64   `json:"threshold"`
	Score     Score     `json:"score"`
	Pairs     PairScore `json:"pairs"`
}

type Pick struct {
	Threshold    float64   `json:"threshold"`
	Tune         Score     `json:"tune"`
	Holdout      Score     `json:"holdout"`
	HoldoutPairs PairScore `json:"holdoutPairs"`
}

type Summary struct {
	Scored    int          `json:"scored"`
	Failed    int          `json:"failed"`
	Threshold float64      `json:"threshold"`
	Keys      int          `json:"keys"`
	Asked     int          `json:"asked"`
	Code      Score        `json:"code"`
	Keyword   Side         `json:"keyword"`
	Jev       *Side        `json:"jev,omitempty"`
	Sweep     []SweepPoint `json:"tuneSweep,omitempty"`
	Pick      *Pick        `json:"pick,omitempty"`
}

func Summarize(results []CaseResult, threshold float64) Summary {
	s := Summary{Threshold: threshold}
	tune := make([]SweepPoint, len(SweepThresholds))
	holdout := make([]SweepPoint, len(SweepThresholds))
	for i, t := range SweepThresholds {
		tune[i].Threshold, holdout[i].Threshold = t, t
	}
	for _, r := range results {
		if r.Error != "" {
			s.Failed++
			continue
		}
		s.Scored++
		s.Keys += r.Keys
		s.Asked += len(r.Asked)
		s.Code = s.Code.Add(r.Code)
		s.Keyword.Score = s.Keyword.Score.Add(r.Keyword.Score)
		if r.Jev == nil {
			continue
		}
		if s.Jev == nil {
			s.Jev = &Side{Pairs: &PairScore{}}
		}
		s.Jev.Score = s.Jev.Score.Add(r.Jev.Score)
		*s.Jev.Pairs = s.Jev.Pairs.Add(r.Jev.Pairs)
		s.Jev.Questions += r.Jev.Stats.Questions
		s.Jev.Requests += r.Jev.Stats.Requests
		s.Jev.InputTokens += int64(r.Jev.Stats.InputTokens)

		sweep := tune
		if r.Case.Split == SplitHoldout {
			sweep = holdout
		}
		for i, t := range SweepThresholds {
			sweep[i].Score = sweep[i].Score.Add(ScoreKeys(KeysAbove(r.Jev.Matches, t), r.Case.AffectedKeys()))
			sweep[i].Pairs = sweep[i].Pairs.Add(ScorePairs(r.Jev.Matches, t, r.Case.Evidence, r.Case.Traps))
		}
	}
	if s.Jev == nil {
		return s
	}
	s.Jev.CostUSD = float64(s.Jev.InputTokens) * jevUSDPerInputMTok / 1e6
	s.Sweep = tune
	if i := PickThreshold(tune); i >= 0 {
		s.Pick = &Pick{Threshold: tune[i].Threshold, Tune: tune[i].Score, Holdout: holdout[i].Score, HoldoutPairs: holdout[i].Pairs}
	}
	return s
}
