package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

type spec struct {
	name       string
	from       string
	to         string
	old        helm.Chart
	new        helm.Chart
	valueFiles []string
	appRepo    string
	chartNotes string
}

type chartInfo struct {
	defaults  map[string]any
	meta      helm.Metadata
	renderErr error
}

func validate(o options) (func(io.Writer, report.Report) error, error) {
	write, ok := writers[o.output]
	if !ok {
		return nil, fmt.Errorf("unknown output format %q, want text, json or markdown", o.output)
	}
	for _, k := range o.failOn {
		if !slices.Contains(report.FailKinds, k) {
			return nil, fmt.Errorf("unknown --fail-on kind %q, want any of %s", k, strings.Join(report.FailKinds, ", "))
		}
	}
	if o.threshold < 0 || o.threshold > 1 {
		return nil, fmt.Errorf("threshold must be between 0 and 1, got %v", o.threshold)
	}
	return write, nil
}

func analyze(ctx context.Context, h helm.Runner, s spec, o options) (report.Report, error) {
	user, err := values.LoadFiles(s.valueFiles)
	if err != nil {
		return report.Report{}, err
	}

	var oldChart, newChart chartInfo
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		oldChart, err = fetchChart(gctx, h, s.old, o.render, s.valueFiles)
		return err
	})
	g.Go(func() error {
		var err error
		newChart, err = fetchChart(gctx, h, s.new, o.render, s.valueFiles)
		return err
	})
	if err := g.Wait(); err != nil {
		return report.Report{}, err
	}

	res := report.Report{
		Chart: s.name, From: s.from, To: s.to,
		AppFrom: oldChart.meta.AppVersion, AppTo: newChart.meta.AppVersion,
		Report: values.Compare(oldChart.defaults, newChart.defaults, user),
	}
	if o.render {
		res.Render = report.NewRender(oldChart.renderErr, newChart.renderErr)
	}
	if sources := notesSources(s, o, oldChart.meta.AppVersion, newChart.meta.AppVersion); len(sources) > 0 {
		res.Notes, err = checkAllNotes(ctx, s.name, o, sources, res.Report)
		if err != nil {
			return report.Report{}, err
		}
	}
	return res, nil
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

func fetchChart(ctx context.Context, h helm.Runner, c helm.Chart, render bool, valueFiles []string) (chartInfo, error) {
	var info chartInfo
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		data, err := h.ShowValues(gctx, c)
		if err != nil {
			return err
		}
		info.defaults, err = values.Parse(data)
		if err != nil {
			return fmt.Errorf("parse values of %s %s: %w", c.Ref, c.Version, err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		info.meta, err = h.ShowChart(gctx, c)
		return err
	})
	if render {
		g.Go(func() error {
			info.renderErr = h.Template(gctx, c, valueFiles)
			return gctx.Err()
		})
	}
	err := g.Wait()
	return info, err
}
