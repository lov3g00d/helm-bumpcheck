package eval

import (
	"slices"

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
