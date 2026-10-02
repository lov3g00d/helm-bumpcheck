package eval

import (
	"slices"
	"strings"

	"github.com/lov3g00d/helm-bumpcheck/internal/match"
)

type Score struct {
	TP int `json:"tp"`
	FP int `json:"fp"`
	FN int `json:"fn"`
}

func ScoreKeys(predicted, expected []string) Score {
	var s Score
	for _, p := range predicted {
		if slices.Contains(expected, p) {
			s.TP++
		} else {
			s.FP++
		}
	}
	for _, e := range expected {
		if !slices.Contains(predicted, e) {
			s.FN++
		}
	}
	return s
}

func (s Score) Add(o Score) Score {
	return Score{TP: s.TP + o.TP, FP: s.FP + o.FP, FN: s.FN + o.FN}
}

func (s Score) Recall() (float64, bool) {
	if s.TP+s.FN == 0 {
		return 0, false
	}
	return float64(s.TP) / float64(s.TP+s.FN), true
}

func (s Score) Precision() (float64, bool) {
	if s.TP+s.FP == 0 {
		return 0, false
	}
	return float64(s.TP) / float64(s.TP+s.FP), true
}

func (s Score) F1() (float64, bool) {
	if s.TP+s.FP+s.FN == 0 {
		return 0, false
	}
	return 2 * float64(s.TP) / float64(2*s.TP+s.FP+s.FN), true
}

type PairScore struct {
	Score
	Unlabelled int `json:"unlabelled"`
}

func (s PairScore) Add(o PairScore) PairScore {
	return PairScore{Score: s.Score.Add(o.Score), Unlabelled: s.Unlabelled + o.Unlabelled}
}

func ScorePairs(matches []match.Match, threshold float64, evidence, traps []Pair) PairScore {
	type labelled struct {
		pair int
		key  string
	}
	var s PairScore
	found := map[labelled]bool{}
	for _, m := range matches {
		if m.Probability < threshold {
			continue
		}
		if i := pairIndex(evidence, m.Release, m.Text, m.Key); i >= 0 {
			found[labelled{i, m.Key}] = true
			continue
		}
		if pairIndex(traps, m.Release, m.Text, m.Key) >= 0 {
			s.FP++
			continue
		}
		s.Unlabelled++
	}
	for i, p := range evidence {
		for _, k := range p.Keys {
			if found[labelled{i, k}] {
				s.TP++
			} else {
				s.FN++
			}
		}
	}
	return s
}

func pairIndex(pairs []Pair, release, text, key string) int {
	for i, p := range pairs {
		if p.Release == release && strings.HasPrefix(text, p.Entry) && slices.Contains(p.Keys, key) {
			return i
		}
	}
	return -1
}

func KeysAbove(matches []match.Match, threshold float64) []string {
	var out []string
	for _, m := range matches {
		if m.Probability >= threshold && !slices.Contains(out, m.Key) {
			out = append(out, m.Key)
		}
	}
	slices.Sort(out)
	return out
}

func PickThreshold(sweep []SweepPoint) int {
	best, bestF1 := -1, 0.0
	for i, p := range sweep {
		if _, positives := p.Score.Recall(); !positives {
			continue
		}
		f1, _ := p.Score.F1()
		if best < 0 || f1 > bestF1 || (f1 == bestF1 && p.Threshold > sweep[best].Threshold) {
			best, bestF1 = i, f1
		}
	}
	return best
}
