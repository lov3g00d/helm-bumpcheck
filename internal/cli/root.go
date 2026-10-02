package cli

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"slices"
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
	detectNotes    bool
	userAgent      string
}

var ErrFindings = errors.New("report has findings")

var writers = map[string]func(io.Writer, report.Report) error{
	"text":     report.WriteText,
	"json":     report.WriteJSON,
	"markdown": report.WriteMarkdown,
}

const rootLong = `Check what a chart version bump does to the values you set.

The report lists keys the new chart removed or changed the type of,
defaults that changed under keys you rely on, keys inside free-form
blocks the chart cannot vouch for, and whether both chart versions
render with your values. With --app-repo or --chart-notes it also asks
TypeSafe Jev which release-note entries concern those keys.

Environment:
  TYPESAFE_API_KEY   enables release-note matching
  TYPESAFE_MODEL     Jev model, default jev-1.13.0
  GITHUB_TOKEN       raises the GitHub rate limit (GH_TOKEN also works)
  HELM_BIN           helm binary to use, set by helm for plugins
  NO_COLOR           disables colour in the text report

Exit codes: 0 no failing findings, 1 error, 2 findings matched --fail-on.`

const rootExample = `  helm bumpcheck tempo --repo https://grafana.github.io/helm-charts \
    --from 1.10.0 --to 1.24.4 -f values.yaml
  helm bumpcheck grafana/tempo --from 1.10.0 --to 1.24.4 -f values.yaml \
    --app-repo grafana/tempo -o markdown --fail-on removed,type,render`

func NewRootCmd(version string) *cobra.Command {
	o := options{userAgent: "helm-bumpcheck/" + version}
	cmd := &cobra.Command{
		Use:           commandName() + " CHART --from VERSION --to VERSION [-f values.yaml]...",
		Short:         "Show how a chart upgrade affects the values you set",
		Long:          rootLong,
		Example:       rootExample,
		Version:       version,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return validate(o)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cmd.OutOrStdout(), args[0], o)
		},
	}

	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "chart repository URL")
	f.StringVar(&o.from, "from", "", "chart version you run now")
	f.StringVar(&o.to, "to", "", "chart version you upgrade to")
	f.StringSliceVarP(&o.valueFiles, "values", "f", nil, "values file, repeatable, later files win")
	f.StringVar(&o.appRepo, "app-repo", "", "GitHub owner/name of the app the chart ships, to check its release notes (needs TYPESAFE_API_KEY)")
	f.StringVar(&o.chartNotes, "chart-notes", "", "GitHub owner/name where the chart publishes its own releases, to check them (needs TYPESAFE_API_KEY)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")

	p := cmd.PersistentFlags()
	p.StringVarP(&o.output, "output", "o", "text", "output format: text, json or markdown")
	p.StringVar(&o.appTagPrefix, "app-tag-prefix", "", "prefix before the version in the app's release tags")
	p.StringVar(&o.chartTagPrefix, "chart-tag-prefix", "", "prefix before the version in the chart's release tags (default CHART-)")
	p.IntVar(&o.maxPages, "max-pages", 40, "maximum pages of 100 GitHub releases to read per repo, at most 10 without a GitHub token")
	p.Float64Var(&o.threshold, "threshold", 0.5, "minimum probability for a release-note match to be shown")
	p.StringSliceVar(&o.failOn, "fail-on", nil, "exit with code 2 if the report has any of: "+strings.Join(report.FailKinds, ", "))
	p.BoolVar(&o.render, "render", true, "render both chart versions with your values and report if only the new one fails")

	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(slices.Sorted(maps.Keys(writers)), cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("fail-on", cobra.FixedCompletions(report.FailKinds, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.MarkFlagFilename("values", "yaml", "yml")
	cmd.SetVersionTemplate("helm-bumpcheck version {{.Version}}\n")

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
	d, cleanup, err := newDeps(o)
	if err != nil {
		return err
	}
	defer cleanup()

	cs, err := prepareChart(ctx, d, spec{
		name:       chart,
		from:       o.from,
		to:         o.to,
		old:        helm.Chart{Ref: chart, Version: o.from, Repo: o.repo},
		new:        helm.Chart{Ref: chart, Version: o.to, Repo: o.repo},
		appRepo:    o.appRepo,
		chartNotes: o.chartNotes,
	}, o)
	if err != nil {
		return err
	}
	res, err := analyzeValues(ctx, d, cs, o.valueFiles, nil, o)
	if err != nil {
		return err
	}
	if err := writers[o.output](w, res); err != nil {
		return err
	}
	return failing(o, res)
}
