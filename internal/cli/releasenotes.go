package cli

import (
	"context"
	"fmt"
	"path"
	"slices"

	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

type notesSource struct {
	kind      string
	repo      string
	tagPrefix string
	from      string
	to        string
}

func notesSources(chart string, o options, oldMeta, newMeta string) []notesSource {
	var out []notesSource
	if o.chartNotes != "" {
		prefix := o.chartTagPrefix
		if prefix == "" {
			prefix = path.Base(chart) + "-"
		}
		out = append(out, notesSource{kind: "chart", repo: o.chartNotes, tagPrefix: prefix, from: o.from, to: o.to})
	}
	if o.appRepo != "" {
		out = append(out, notesSource{kind: "app", repo: o.appRepo, tagPrefix: o.appTagPrefix, from: oldMeta, to: newMeta})
	}
	return out
}

func checkAllNotes(ctx context.Context, chart string, o options, sources []notesSource, r values.Report) ([]*report.Notes, error) {
	keys := slices.Concat(r.Removed, typedKeys(r.TypeChanged), r.Freeform, r.Undeclared)
	client, keyErr := jev.NewFromEnv()
	gh := notes.NewGitHubFromEnv()
	gh.MaxPages = o.maxPages

	var out []*report.Notes
	for _, src := range sources {
		res := &report.Notes{Source: src.kind, Repo: src.repo, TagPrefix: src.tagPrefix, From: src.from, To: src.to, Threshold: o.threshold}
		switch {
		case src.from == "" || src.to == "":
			res.Skipped = "the chart has no appVersion"
		case notes.Canonical(src.from) == "" || notes.Canonical(src.to) == "":
			res.Skipped = fmt.Sprintf("versions %q -> %q are not semver", src.from, src.to)
		case notes.Canonical(src.from) == notes.Canonical(src.to):
			res.Skipped = "the version did not change"
		case len(keys) == 0:
			res.Skipped = "no keys need checking"
		case keyErr != nil:
			res.Skipped = keyErr.Error()
		}
		if res.Skipped == "" {
			if err := checkNotes(ctx, gh, client, chart, src, keys, res); err != nil {
				return nil, err
			}
		}
		out = append(out, res)
	}
	return out, nil
}

func checkNotes(ctx context.Context, gh *notes.GitHub, client *jev.Client, chart string, src notesSource, keys []string, res *report.Notes) error {
	out, err := match.CheckReleaseNotes(ctx, gh, client, match.Request{
		Repo:      src.repo,
		TagPrefix: src.tagPrefix,
		From:      src.from,
		To:        src.to,
		Keys:      keys,
		Ignore:    []string{path.Base(chart)},
	})
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		res.Skipped = err.Error()
		return nil
	}
	if len(out.Releases) == 0 {
		res.Skipped = fmt.Sprintf("no releases of %s tagged %s<version> in (%s, %s]", src.repo, src.tagPrefix, src.from, src.to)
		return nil
	}
	res.Releases, res.Entries, res.Matches, res.Stats = len(out.Releases), len(out.Entries), out.Matches, out.Stats
	return nil
}

func typedKeys(tcs []values.TypeChange) []string {
	var out []string
	for _, tc := range tcs {
		out = append(out, tc.Keys...)
	}
	return out
}
