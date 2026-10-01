package report

import (
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

type Report struct {
	Chart string `json:"chart"`
	From  string `json:"from"`
	To    string `json:"to"`
	values.Report
	Notes *Notes `json:"notes,omitempty"`
}

type Notes struct {
	Repo      string        `json:"repo"`
	From      string        `json:"from"`
	To        string        `json:"to"`
	Releases  int           `json:"releases"`
	Entries   int           `json:"entries"`
	Threshold float64       `json:"threshold"`
	Stats     match.Stats   `json:"stats"`
	Matches   []match.Match `json:"matches"`
	Skipped   string        `json:"skipped,omitempty"`
}
