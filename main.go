package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/lov3g00d/helm-bumpcheck/internal/cli"
	"github.com/lov3g00d/helm-bumpcheck/internal/report"
)

var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := cli.NewRootCmd(buildVersion()).ExecuteContext(ctx)
	stop()
	if code := exitCode(os.Stderr, err); code != 0 {
		os.Exit(code)
	}
}

func exitCode(stderr io.Writer, err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, cli.ErrFindings):
		fmt.Fprintln(stderr, report.Sanitize(err.Error()))
		return 2
	default:
		fmt.Fprintln(stderr, "Error:", report.Sanitize(err.Error()))
		return 1
	}
}
