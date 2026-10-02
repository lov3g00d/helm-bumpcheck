package cli

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

var githubRepo = regexp.MustCompile(`github\.com[/:]([\w.-]+)/([\w.-]+)`)

type notesSource struct {
	kind      string
	repo      string
	tagPrefix string
	from      string
	to        string
}

type fetchedNotes struct {
	src       notesSource
	releases  int
	entries   []notes.Entry
	skipped   string
	truncated string
}

func notesSources(s spec, o options, oldApp, newApp string) []notesSource {
	var out []notesSource
	if s.chartNotes != "" {
		prefix := o.chartTagPrefix
		if prefix == "" {
			prefix = path.Base(s.name) + "-"
		}
		out = append(out, notesSource{kind: "chart", repo: s.chartNotes, tagPrefix: prefix, from: s.from, to: s.to})
	}
	if s.appRepo != "" {
		out = append(out, notesSource{kind: "app", repo: s.appRepo, tagPrefix: o.appTagPrefix, from: oldApp, to: newApp})
	}
	return out
}

func detectRepos(meta helm.Metadata) (appRepo, chartNotes string) {
	for _, src := range append(slices.Clone(meta.Sources), meta.Home) {
		m := githubRepo.FindStringSubmatch(src)
		if m == nil {
			continue
		}
		owner, name := m[1], strings.TrimSuffix(m[2], ".git")
		full := owner + "/" + name
		if strings.Contains(strings.ToLower(name), "chart") {
			if chartNotes == "" {
				chartNotes = full
			}
		} else if appRepo == "" {
			appRepo = full
		}
	}
	return appRepo, chartNotes
}

func fetchNotes(ctx context.Context, d *deps, chart string, src notesSource) *fetchedNotes {
	f := &fetchedNotes{src: src}
	switch {
	case src.from == "" || src.to == "":
		f.skipped = "the chart has no appVersion"
	case notes.Canonical(src.from) == "" || notes.Canonical(src.to) == "":
		f.skipped = fmt.Sprintf("versions %q -> %q are not semver", src.from, src.to)
	case notes.Canonical(src.from) == notes.Canonical(src.to):
		f.skipped = "the version did not change"
	case d.jev == nil:
		f.skipped = d.jevErr.Error()
	}
	if f.skipped != "" {
		return f
	}

	releases, entries, trunc, err := match.Fetch(ctx, d.gh, request(chart, src, nil))
	if err != nil {
		f.skipped = err.Error()
		return f
	}
	if trunc != nil {
		f.truncated = truncationNote(trunc, d.gh.Token != "")
	}
	switch {
	case len(releases) == 0 && trunc != nil:
		f.skipped = fmt.Sprintf("no releases of %s tagged %s<version> in (%s, %s] were reached, %s", src.repo, src.tagPrefix, src.from, src.to, f.truncated)
	case len(releases) == 0:
		f.skipped = fmt.Sprintf("no releases of %s tagged %s<version> in (%s, %s]", src.repo, src.tagPrefix, src.from, src.to)
	default:
		f.releases, f.entries = len(releases), entries
	}
	return f
}

func truncationNote(t *notes.Truncation, authenticated bool) string {
	at := ""
	if t.Oldest != "" {
		at = " at " + t.Oldest
	}
	stopped := fmt.Sprintf("releases may be incomplete: stopped after %d %s%s", t.Pages, plural(t.Pages, "page", "pages"), at)
	switch {
	case t.ResultCap && !authenticated:
		return stopped + ", GitHub lists only the first 1000 releases without a token, set GITHUB_TOKEN to read further"
	case t.ResultCap:
		return stopped + ", GitHub lists only the first 1000 releases"
	default:
		return stopped + ", raise --max-pages to read further"
	}
}

func askNotes(ctx context.Context, d *deps, cs *chartStage, f *fetchedNotes, r values.Report, o options) (*report.Notes, error) {
	src := f.src
	res := &report.Notes{Source: src.kind, Repo: src.repo, TagPrefix: src.tagPrefix, From: src.from, To: src.to, Threshold: o.threshold, Skipped: f.skipped, Truncated: f.truncated}
	if res.Skipped != "" {
		return res, nil
	}
	keys := r.NoteKeys()
	if len(keys) == 0 {
		res.Skipped = "no keys need checking"
		return res, nil
	}

	req := request(cs.spec.name, src, keys)
	matches, stats, err := match.AskMemo(ctx, d.jev, cs.memo, src.repo, req.Candidates(f.entries), match.DefaultConcurrency)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		res.Skipped = err.Error()
		return res, nil
	}
	res.Releases, res.Entries, res.Matches, res.Stats = f.releases, len(f.entries), matches, stats
	return res, nil
}

func request(chart string, src notesSource, keys []string) match.Request {
	return match.Request{
		Repo:      src.repo,
		TagPrefix: src.tagPrefix,
		From:      src.from,
		To:        src.to,
		Chart:     chart,
		Keys:      keys,
	}
}
