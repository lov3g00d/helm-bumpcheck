package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/lov3g00d/helm-bumpcheck/internal/gitdiff"
	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
)

var githubRepo = regexp.MustCompile(`github\.com[/:]([\w.-]+)/([\w.-]+)`)

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
		Use:           "diff [--base REF] [--head REF]",
		Short:         "Check every chart whose version changed between two git revisions",
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
	if _, err := validate(o); err != nil {
		return err
	}
	h, err := helm.Detect()
	if err != nil {
		return err
	}
	repo := gitdiff.Repo{Dir: d.repoDir}
	charts, skips, err := repo.ChangedCharts(ctx, d.base, d.head)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "bumpcheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	batch := report.Batch{Base: d.base, Head: d.head, Reports: []report.Report{}, Skipped: []report.Skip{}}
	for _, s := range skips {
		batch.Skipped = append(batch.Skipped, report.Skip{Path: s.Dir, Reason: s.Reason})
	}

	for i, c := range charts {
		reports, err := diffChart(ctx, h, repo, c, filepath.Join(tmp, strconv.Itoa(i)), o, d)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			batch.Skipped = append(batch.Skipped, report.Skip{Path: c.Dir, Reason: err.Error()})
			continue
		}
		batch.Reports = append(batch.Reports, reports...)
	}

	if err := report.WriteBatch(w, o.output, batch); err != nil {
		return err
	}
	return failing(o, batch.Reports...)
}

func diffChart(ctx context.Context, h helm.Runner, repo gitdiff.Repo, c gitdiff.Chart, work string, o options, d diffOptions) ([]report.Report, error) {
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
		return nil, fmt.Errorf("no values files match %s", strings.Join(d.valueGlobs, ", "))
	}

	var appRepo, chartNotes string
	if d.notes {
		appRepo, chartNotes = detectRepos(filepath.Join(newDir, "Chart.yaml"))
	}

	var out []report.Report
	for _, file := range files {
		res, err := analyze(ctx, h, spec{
			name:       c.Name,
			from:       c.FromVersion,
			to:         c.ToVersion,
			old:        helm.Chart{Ref: oldDir},
			new:        helm.Chart{Ref: newDir},
			valueFiles: []string{filepath.Join(newDir, file)},
			appRepo:    appRepo,
			chartNotes: chartNotes,
		}, o)
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

func detectRepos(chartYAML string) (appRepo, chartNotes string) {
	data, err := os.ReadFile(chartYAML)
	if err != nil {
		return "", ""
	}
	var meta struct {
		Home    string   `yaml:"home"`
		Sources []string `yaml:"sources"`
	}
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return "", ""
	}
	for _, src := range append(meta.Sources, meta.Home) {
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
