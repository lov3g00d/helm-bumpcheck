package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/lov3g00d/helm-bumpcheck/internal/eval"
	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
)

type config struct {
	cases     string
	threshold float64
	output    string
	skipJev   bool
	maxPages  int
}

func main() {
	var cfg config
	flag.StringVar(&cfg.cases, "cases", "eval/cases.yaml", "labelled cases file")
	flag.Float64Var(&cfg.threshold, "threshold", 0.5, "Jev probability at or above which a key counts as affected")
	flag.StringVar(&cfg.output, "o", "text", "output format: text or json")
	flag.IntVar(&cfg.maxPages, "max-pages", 40, "maximum pages of 100 GitHub releases to read per repo")
	flag.BoolVar(&cfg.skipJev, "skip-jev", false, "score only the keyword baseline, without calling Jev")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Stdout, cfg)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, w io.Writer, cfg config) error {
	if cfg.output != "text" && cfg.output != "json" {
		return fmt.Errorf("unknown output format %q, want text or json", cfg.output)
	}
	cases, err := eval.LoadCases(cfg.cases)
	if err != nil {
		return err
	}

	h, err := helm.Detect()
	if err != nil {
		return err
	}
	gh := notes.NewGitHubFromEnv()
	gh.MaxPages = cfg.maxPages
	r := eval.Runner{Helm: h, GitHub: gh, Threshold: cfg.threshold}
	if !cfg.skipJev {
		client, err := jev.NewFromEnv()
		if err != nil {
			return fmt.Errorf("%w (or pass -skip-jev)", err)
		}
		r.Jev = client
	}

	results, err := r.Run(ctx, cases)
	if err != nil {
		return err
	}
	summary := eval.Summarize(results, cfg.threshold)
	write := eval.WriteText
	if cfg.output == "json" {
		write = eval.WriteJSON
	}
	if err := write(w, results, summary); err != nil {
		return err
	}
	if summary.Scored == 0 {
		return fmt.Errorf("none of the %d cases could be scored", len(cases))
	}
	return nil
}
