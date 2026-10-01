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
	Render *Render `json:"render,omitempty"`
	Notes  *Notes  `json:"notes,omitempty"`
}

type Render struct {
	OldOK    bool   `json:"oldOk"`
	NewOK    bool   `json:"newOk"`
	OldError string `json:"oldError,omitempty"`
	NewError string `json:"newError,omitempty"`
}

func NewRender(oldErr, newErr error) *Render {
	r := &Render{OldOK: oldErr == nil, NewOK: newErr == nil}
	if oldErr != nil {
		r.OldError = oldErr.Error()
	}
	if newErr != nil {
		r.NewError = newErr.Error()
	}
	return r
}

func (r *Render) Breaking() bool {
	return r != nil && r.OldOK && !r.NewOK
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
