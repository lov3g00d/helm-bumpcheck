package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lov3g00d/helm-bumpcheck/internal/gitdiff"
	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
)

var errNoValues = errors.New("no values files match")

type diffOptions struct {
	base       string
	head       string
	repoDir    string
	valueGlobs []string
	notes      bool
}

func newDiffCmd(o *options) *cobra.Command {
	var d diffOptions
	cmd := &cobra.Command{
		Use:   "diff [--base REF] [--head REF]",
		Short: "Check every chart whose version changed between two git revisions",
		Long: `Find charts whose Chart.yaml version changed between the merge base of
--base and --head, and check each one against every values file in its
directory. Charts whose dependencies changed without a version bump are
listed as skipped. Nothing in the repository is modified; both revisions
are read straight from git objects, so .gitattributes has no effect.`,
		Example: `  helm bumpcheck diff --base origin/main
  helm bumpcheck diff --base origin/main -o markdown --fail-on removed,type,render
  helm bumpcheck diff -C ../deployments --base main --head my-branch --notes`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd.Context(), cmd.OutOrStdout(), *o, d)
		},
	}
	f := cmd.Flags()
	f.StringVar(&d.base, "base", "origin/main", "revision to compare against; its merge base with --head is used")
	f.StringVar(&d.head, "head", "HEAD", "revision with the chart bumps")
	f.StringVarP(&d.repoDir, "repo-dir", "C", ".", "git repository to read")
	f.StringSliceVar(&d.valueGlobs, "values-glob", []string{"values_*.yaml", "values-*.yaml"}, "values files in each chart directory to check, one report per file")
	f.BoolVar(&d.notes, "notes", false, "check release notes of GitHub repos found in each chart's sources (needs TYPESAFE_API_KEY)")
	return cmd
}

func runDiff(ctx context.Context, w io.Writer, o options, d diffOptions) error {
	o.detectNotes = d.notes
	deps, cleanup, err := newDeps(o)
	if err != nil {
		return err
	}
	defer cleanup()

	repo := gitdiff.Repo{Dir: d.repoDir}
	charts, skips, err := repo.ChangedCharts(ctx, d.base, d.head)
	if err != nil {
		return err
	}

	batch := report.Batch{Base: d.base, Head: d.head, Bumped: len(charts), Reports: []report.Report{}, Skipped: []report.Skip{}, Failed: []report.Skip{}}
	for _, s := range skips {
		batch.Skipped = append(batch.Skipped, report.Skip{Path: s.Dir, Reason: s.Reason})
	}

	for i, c := range charts {
		reports, err := diffChart(ctx, deps, repo, c, filepath.Join(deps.work, "git", strconv.Itoa(i)), o, d)
		switch {
		case err == nil:
			batch.Reports = append(batch.Reports, reports...)
		case ctx.Err() != nil:
			return err
		case errors.Is(err, errNoValues):
			batch.Skipped = append(batch.Skipped, report.Skip{Path: c.Dir, Reason: err.Error()})
		default:
			batch.Failed = append(batch.Failed, report.Skip{Path: c.Dir, Reason: err.Error()})
		}
	}

	if err := report.WriteBatch(w, o.output, batch); err != nil {
		return err
	}
	if n := len(batch.Failed); n > 0 {
		return fmt.Errorf("%d %s could not be analyzed", n, plural(n, "chart", "charts"))
	}
	return failing(o, batch.Reports...)
}

func diffChart(ctx context.Context, deps *deps, repo gitdiff.Repo, c gitdiff.Chart, work string, o options, d diffOptions) ([]report.Report, error) {
	oldDir, err := repo.Extract(ctx, c.Base, c.Dir, filepath.Join(work, "base"))
	if err != nil {
		return nil, err
	}
	newDir, err := repo.Extract(ctx, c.Head, c.Dir, filepath.Join(work, "head"))
	if err != nil {
		return nil, err
	}

	files, err := matchValues(newDir, d.valueGlobs)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w %s", errNoValues, strings.Join(d.valueGlobs, ", "))
	}

	cs, err := prepareChart(ctx, deps, spec{
		name: c.Name,
		from: c.FromVersion,
		to:   c.ToVersion,
		old:  helm.Chart{Ref: oldDir},
		new:  helm.Chart{Ref: newDir},
	}, o)
	if err != nil {
		return nil, err
	}

	var out []report.Report
	for _, file := range files {
		oldValues := filepath.Join(oldDir, file)
		if _, err := os.Stat(oldValues); err != nil {
			oldValues = filepath.Join(newDir, file)
		}
		res, err := analyzeValues(ctx, deps, cs, []string{filepath.Join(newDir, file)}, []string{oldValues}, o)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		res.Path, res.Values = c.Dir, file
		out = append(out, res)
	}
	return out, nil
}

func matchValues(dir string, globs []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.Type().IsRegular() || e.Name() == "values.yaml" {
			continue
		}
		for _, g := range globs {
			if ok, err := filepath.Match(g, e.Name()); err != nil {
				return nil, fmt.Errorf("bad --values-glob %q: %w", g, err)
			} else if ok {
				out = append(out, e.Name())
				break
			}
		}
	}
	slices.Sort(out)
	return out, nil
}
