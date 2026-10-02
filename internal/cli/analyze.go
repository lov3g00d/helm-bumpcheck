package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

type deps struct {
	helm   helm.Runner
	gh     *notes.GitHub
	jev    match.Evaluator
	jevErr error
	work   string
}

func newDeps(o options) (*deps, func(), error) {
	h, err := helm.Detect()
	if err != nil {
		return nil, nil, err
	}
	h.KubeVersion = o.kubeVersion
	work, err := os.MkdirTemp("", "bumpcheck-")
	if err != nil {
		return nil, nil, err
	}
	gh := notes.NewGitHubFromEnv()
	gh.MaxPages, gh.UserAgent = o.maxPages, o.userAgent
	d := &deps{helm: h, gh: gh, work: work}
	if client, err := jev.NewFromEnv(); err != nil {
		d.jevErr = err
	} else {
		client.UserAgent = o.userAgent
		d.jev = client
	}
	return d, func() { os.RemoveAll(work) }, nil
}

type spec struct {
	name       string
	from       string
	to         string
	old        helm.Chart
	new        helm.Chart
	appRepo    string
	chartNotes string
	buildDeps  bool
}

type chartStage struct {
	spec       spec
	old        helm.Loaded
	new        helm.Loaded
	renderSkip [2]string
	sources    []*fetchedNotes
	memo       *match.Memo
}

func prepareChart(ctx context.Context, d *deps, s spec, o options) (*chartStage, error) {
	cs := &chartStage{spec: s, memo: match.NewMemo()}
	work, err := os.MkdirTemp(d.work, "chart-")
	if err != nil {
		return nil, err
	}

	g, gctx := errgroup.WithContext(ctx)
	for i, side := range []struct {
		chart  helm.Chart
		loaded *helm.Loaded
		dest   string
	}{{s.old, &cs.old, "old"}, {s.new, &cs.new, "new"}} {
		g.Go(func() error {
			var err error
			if *side.loaded, err = d.helm.Load(gctx, side.chart, filepath.Join(work, side.dest)); err != nil {
				return err
			}
			if s.buildDeps && o.render {
				cs.renderSkip[i] = buildDependencies(gctx, d.helm, *side.loaded)
			}
			return gctx.Err()
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	if s.appRepo == "" && s.chartNotes == "" && o.detectNotes {
		cs.spec.appRepo, cs.spec.chartNotes = detectRepos(cs.new.Meta)
	}
	for _, src := range notesSources(cs.spec, o, cs.old.Meta.AppVersion, cs.new.Meta.AppVersion) {
		cs.sources = append(cs.sources, fetchNotes(ctx, d, s.name, src))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cs, nil
}

func buildDependencies(ctx context.Context, h helm.Runner, c helm.Loaded) string {
	missing := helm.MissingDependencies(c.Dir, c.Meta)
	if len(missing) == 0 {
		return ""
	}
	if err := h.BuildDependencies(ctx, c.Dir); err != nil {
		return fmt.Sprintf("the dependencies of %s %s (%s) could not be fetched: %v", c.Meta.Name, c.Meta.Version, strings.Join(missing, ", "), err)
	}
	return ""
}

func analyzeValues(ctx context.Context, d *deps, cs *chartStage, valueFiles, oldValueFiles []string, o options) (report.Report, error) {
	user, err := values.LoadFiles(valueFiles)
	if err != nil {
		return report.Report{}, err
	}
	subcharts := cs.new.Meta.DependencyNames()
	res := report.Report{
		Chart: cs.spec.name, From: cs.spec.from, To: cs.spec.to,
		AppFrom: cs.old.Meta.AppVersion, AppTo: cs.new.Meta.AppVersion,
		Dependencies: subcharts,
		Report:       values.Compare(cs.old.Defaults, cs.new.Defaults, user, values.Dependencies{Old: cs.old.Meta.DependencyNames(), New: subcharts}),
	}

	if skip := cmp.Or(cs.renderSkip[0], cs.renderSkip[1]); o.render && skip != "" {
		res.Render = report.SkippedRender(skip)
	} else if o.render {
		if oldValueFiles == nil {
			oldValueFiles = valueFiles
		}
		var oldErr, newErr error
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			oldErr = d.helm.Template(gctx, helm.Chart{Ref: cs.old.Dir}, oldValueFiles)
			return gctx.Err()
		})
		g.Go(func() error {
			newErr = d.helm.Template(gctx, helm.Chart{Ref: cs.new.Dir}, valueFiles)
			return gctx.Err()
		})
		if err := g.Wait(); err != nil {
			return report.Report{}, err
		}
		res.Render = newRender(oldErr, newErr, o.kubeVersion)
	}

	for _, src := range cs.sources {
		n, err := askNotes(ctx, d, cs, src, res.Report, o)
		if err != nil {
			return report.Report{}, err
		}
		res.Notes = append(res.Notes, n)
	}
	return res, nil
}

func newRender(oldErr, newErr error, kubeVersion string) *report.Render {
	if kv, ok := errors.AsType[*helm.KubeVersionError](newErr); kubeVersion == "" && ok {
		return report.SkippedRender(fmt.Sprintf("the new %s, set --kube-version to your cluster version", kv.Message))
	}
	return report.NewRender(oldErr, newErr)
}

func validate(o options) error {
	if err := report.CheckFormat(o.output); err != nil {
		return err
	}
	for _, k := range o.failOn {
		if !slices.Contains(report.FailKinds, k) {
			return fmt.Errorf("unknown --fail-on kind %q, want any of %s", k, strings.Join(report.FailKinds, ", "))
		}
	}
	if o.maxPages < 1 {
		return fmt.Errorf("--max-pages must be at least 1, got %d", o.maxPages)
	}
	if o.threshold < 0 || o.threshold > 1 {
		return fmt.Errorf("--threshold must be between 0 and 1, got %v", o.threshold)
	}
	for _, f := range []struct{ name, repo string }{{"--app-repo", o.appRepo}, {"--chart-notes", o.chartNotes}} {
		if f.repo == "" {
			continue
		}
		if err := notes.CheckRepo(f.repo); err != nil {
			return fmt.Errorf("%s %w", f.name, err)
		}
	}
	return nil
}

func failing(o options, reports ...report.Report) error {
	var hits []string
	for _, r := range reports {
		for _, k := range r.Failing(o.failOn) {
			if !slices.Contains(hits, k) {
				hits = append(hits, k)
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrFindings, strings.Join(hits, ", "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
