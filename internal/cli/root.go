package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
	"github.com/spf13/cobra"
)

type options struct {
	repo           string
	from           string
	to             string
	valueFiles     []string
	output         string
	appRepo        string
	appTagPrefix   string
	threshold      float64
	render         bool
	failOn         []string
	chartNotes     string
	chartTagPrefix string
	maxPages       int
}

var ErrFindings = errors.New("report has findings")

var writers = map[string]func(io.Writer, report.Report) error{
	"text":     report.WriteText,
	"json":     report.WriteJSON,
	"markdown": report.WriteMarkdown,
}

func NewRootCmd() *cobra.Command {
	var o options
	cmd := &cobra.Command{
		Use:           commandName() + " CHART --from VERSION --to VERSION [-f values.yaml]...",
		Short:         "Show how a chart upgrade affects the values you set",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
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
	f.StringVar(&o.appRepo, "app-repo", "", "GitHub owner/name of the app the chart ships, to check its release notes")
	f.StringVar(&o.chartNotes, "chart-notes", "", "GitHub owner/name where the chart publishes its own releases, to check them")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")

	p := cmd.PersistentFlags()
	p.StringVarP(&o.output, "output", "o", "text", "output format: text, json or markdown")
	p.StringVar(&o.appTagPrefix, "app-tag-prefix", "", "prefix before the version in the app's release tags")
	p.StringVar(&o.chartTagPrefix, "chart-tag-prefix", "", "prefix before the version in the chart's release tags (default CHART-)")
	p.IntVar(&o.maxPages, "max-pages", 40, "maximum pages of 100 GitHub releases to read per repo")
	p.Float64Var(&o.threshold, "threshold", 0.5, "minimum probability for a release-note match to be shown")
	p.StringSliceVar(&o.failOn, "fail-on", nil, "exit with code 2 if the report has any of: "+strings.Join(report.FailKinds, ", "))
	p.BoolVar(&o.render, "render", true, "render both chart versions with your values and report if only the new one fails")

	cmd.AddCommand(newDiffCmd(&o))
	return cmd
}

func commandName() string {
	if os.Getenv("HELM_PLUGIN_NAME") != "" {
		return "helm bumpcheck"
	}
	return "helm-bumpcheck"
}

func run(ctx context.Context, w io.Writer, chart string, o options) error {
	write, err := validate(o)
	if err != nil {
		return err
	}
	h, err := helm.Detect()
	if err != nil {
		return err
	}

	res, err := analyze(ctx, h, spec{
		name:       chart,
		from:       o.from,
		to:         o.to,
		old:        helm.Chart{Ref: chart, Version: o.from, Repo: o.repo},
		new:        helm.Chart{Ref: chart, Version: o.to, Repo: o.repo},
		valueFiles: o.valueFiles,
		appRepo:    o.appRepo,
		chartNotes: o.chartNotes,
	}, o)
	if err != nil {
		return err
	}
	if err := write(w, res); err != nil {
		return err
	}
	return failing(o, res)
}
