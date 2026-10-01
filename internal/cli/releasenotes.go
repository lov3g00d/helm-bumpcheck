package cli

import (
	"context"
	"fmt"
	"path"
	"slices"

	"github.com/lov3g00d/helm-values-impact/internal/jev"
	"github.com/lov3g00d/helm-values-impact/internal/match"
	"github.com/lov3g00d/helm-values-impact/internal/notes"
	"github.com/lov3g00d/helm-values-impact/internal/values"
)

const jevConcurrency = 8

type notesResult struct {
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

func checkNotes(ctx context.Context, chart string, o options, fromApp, toApp string, r values.Report) (*notesResult, error) {
	res := &notesResult{Repo: o.appRepo, From: fromApp, To: toApp, Threshold: o.threshold}
	keys := slices.Concat(r.Removed, r.Freeform, r.Undeclared)

	switch {
	case fromApp == "" || toApp == "":
		res.Skipped = "the chart has no appVersion"
	case notes.Canonical(fromApp) == "" || notes.Canonical(toApp) == "":
		res.Skipped = fmt.Sprintf("appVersion %q -> %q is not semver", fromApp, toApp)
	case notes.Canonical(fromApp) == notes.Canonical(toApp):
		res.Skipped = "appVersion did not change"
	case len(keys) == 0:
		res.Skipped = "no keys need checking"
	}
	if res.Skipped != "" {
		return res, nil
	}

	client, err := jev.NewFromEnv()
	if err != nil {
		res.Skipped = err.Error()
		return res, nil
	}

	releases, err := notes.NewGitHubFromEnv().Releases(ctx, o.appRepo, o.appTagPrefix, fromApp, toApp)
	if err != nil {
		return skipOnError(ctx, res, err)
	}
	var entries []notes.Entry
	for _, rel := range releases {
		entries = append(entries, notes.Split(rel)...)
	}
	res.Releases, res.Entries = len(releases), len(entries)

	candidates := match.Candidates(entries, keys, []string{path.Base(chart), path.Base(o.appRepo)})
	res.Matches, res.Stats, err = match.Ask(ctx, client, candidates, jevConcurrency)
	if err != nil {
		return skipOnError(ctx, res, err)
	}
	return res, nil
}

func skipOnError(ctx context.Context, res *notesResult, err error) (*notesResult, error) {
	if ctx.Err() != nil {
		return nil, err
	}
	res.Skipped = err.Error()
	return res, nil
}
