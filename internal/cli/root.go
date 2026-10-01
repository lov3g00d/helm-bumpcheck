package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

type options struct {
	repo         string
	from         string
	to           string
	valueFiles   []string
	output       string
	appRepo      string
	appTagPrefix string
	threshold    float64
	render       bool
}

type chartInfo struct {
	defaults  map[string]any
	meta      helm.Metadata
	renderErr error
}

type fetchOpts struct {
	meta       bool
	render     bool
	valueFiles []string
}

func NewRootCmd() *cobra.Command {
	var o options
	cmd := &cobra.Command{
		Use:          commandName() + " CHART --from VERSION --to VERSION [-f values.yaml]...",
		Short:        "Show how a chart upgrade affects the values you set",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cmd.OutOrStdout(), args[0], o)
		},
	}
	cmd.CompletionOptions.DisableDefaultCmd = true

	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "chart repository URL")
	f.StringVar(&o.from, "from", "", "chart version you run now")
	f.StringVar(&o.to, "to", "", "chart version you upgrade to")
	f.StringSliceVarP(&o.valueFiles, "values", "f", nil, "values file, repeatable, later files win")
	f.StringVarP(&o.output, "output", "o", "text", "output format: text or json")
	f.StringVar(&o.appRepo, "app-repo", "", "GitHub owner/name of the app the chart ships, to check its release notes")
	f.StringVar(&o.appTagPrefix, "app-tag-prefix", "", "prefix before the version in the app's release tags")
	f.Float64Var(&o.threshold, "threshold", 0.5, "minimum probability for a release-note match to be shown")
	f.BoolVar(&o.render, "render", true, "render both chart versions with your values and report if only the new one fails")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func commandName() string {
	if os.Getenv("HELM_PLUGIN_NAME") != "" {
		return "helm bumpcheck"
	}
	return "helm-bumpcheck"
}

func run(ctx context.Context, w io.Writer, chart string, o options) error {
	if o.output != "text" && o.output != "json" {
		return fmt.Errorf("unknown output format %q, want text or json", o.output)
	}
	if o.threshold < 0 || o.threshold > 1 {
		return fmt.Errorf("threshold must be between 0 and 1, got %v", o.threshold)
	}

	h, err := helm.Detect()
	if err != nil {
		return err
	}

	user, err := values.LoadFiles(o.valueFiles)
	if err != nil {
		return err
	}

	fo := fetchOpts{meta: o.appRepo != "", render: o.render, valueFiles: o.valueFiles}
	var oldChart, newChart chartInfo
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		oldChart, err = fetchChart(gctx, h, helm.Chart{Ref: chart, Version: o.from, Repo: o.repo}, fo)
		return err
	})
	g.Go(func() error {
		var err error
		newChart, err = fetchChart(gctx, h, helm.Chart{Ref: chart, Version: o.to, Repo: o.repo}, fo)
		return err
	})
	if err := g.Wait(); err != nil {
		return err
	}

	res := report.Report{Chart: chart, From: o.from, To: o.to, Report: values.Compare(oldChart.defaults, newChart.defaults, user)}
	if o.render {
		res.Render = report.NewRender(oldChart.renderErr, newChart.renderErr)
	}
	if o.appRepo != "" {
		res.Notes, err = checkNotes(ctx, chart, o, oldChart.meta.AppVersion, newChart.meta.AppVersion, res.Report)
		if err != nil {
			return err
		}
	}

	if o.output == "json" {
		return report.WriteJSON(w, res)
	}
	return report.WriteText(w, res)
}

func fetchChart(ctx context.Context, h helm.Runner, c helm.Chart, fo fetchOpts) (chartInfo, error) {
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
	if fo.meta {
		g.Go(func() error {
			var err error
			info.meta, err = h.ShowChart(gctx, c)
			return err
		})
	}
	if fo.render {
		g.Go(func() error {
			info.renderErr = h.Template(gctx, c, fo.valueFiles)
			return gctx.Err()
		})
	}
	err := g.Wait()
	return info, err
}
