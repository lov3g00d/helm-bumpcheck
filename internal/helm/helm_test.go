package helm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeHelm(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "helm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestShowValuesPassesArgs(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, `printf '%s\n' "$@"`)}
	out, err := r.ShowValues(context.Background(), Chart{Ref: "tempo", Version: "1.10.0", Repo: "https://grafana.github.io/helm-charts"})
	if err != nil {
		t.Fatal(err)
	}
	want := "show\nvalues\ntempo\n--version\n1.10.0\n--repo\nhttps://grafana.github.io/helm-charts\n"
	if string(out) != want {
		t.Errorf("args =\n%s\nwant\n%s", out, want)
	}
}

func TestShowValuesOmitsEmptyFlags(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, `printf '%s\n' "$@"`)}
	out, err := r.ShowValues(context.Background(), Chart{Ref: "oci://ghcr.io/x/chart"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "show\nvalues\noci://ghcr.io/x/chart\n"; string(out) != want {
		t.Errorf("args =\n%s\nwant\n%s", out, want)
	}
}

func TestShowValuesErrorIncludesStderr(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, `echo 'Error: chart "nope" not found' >&2; exit 1`)}
	_, err := r.ShowValues(context.Background(), Chart{Ref: "nope", Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `chart "nope" not found`) {
		t.Errorf("error %q does not include helm stderr", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("error %q does not wrap *exec.ExitError", err)
	}
}

func TestShowValuesHonoursContext(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, `exec sleep 10`)}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := r.ShowValues(ctx, Chart{Ref: "tempo"}); err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %s, context cancel did not stop helm", elapsed)
	}
}

func TestDetectPrefersHelmBin(t *testing.T) {
	t.Setenv("HELM_BIN", "/opt/helm4/helm")
	r, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if r.Bin != "/opt/helm4/helm" {
		t.Errorf("Bin = %q, want HELM_BIN", r.Bin)
	}
}

func TestDetectFallsBackToPath(t *testing.T) {
	bin := fakeHelm(t, "true")
	t.Setenv("HELM_BIN", "")
	t.Setenv("PATH", filepath.Dir(bin))
	r, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if r.Bin != bin {
		t.Errorf("Bin = %q, want %q", r.Bin, bin)
	}
}

func TestDetectErrorsWithoutHelm(t *testing.T) {
	t.Setenv("HELM_BIN", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := Detect(); err == nil {
		t.Fatal("expected error when helm is missing")
	}
}

func TestShowChart(t *testing.T) {
	script := `case "$1 $2" in
  "show chart") printf 'apiVersion: v2\nname: tempo\nversion: 1.24.4\nappVersion: "2.9.0"\n' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac`
	r := Runner{Bin: fakeHelm(t, script)}
	m, err := r.ShowChart(context.Background(), Chart{Ref: "tempo", Version: "1.24.4"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "tempo" || m.Version != "1.24.4" || m.AppVersion != "2.9.0" {
		t.Errorf("metadata = %+v", m)
	}
}

func TestShowChartBadYAML(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, `printf 'name: [unclosed\n'`)}
	if _, err := r.ShowChart(context.Background(), Chart{Ref: "tempo", Version: "1.0.0"}); err == nil || !strings.Contains(err.Error(), "parse Chart.yaml of tempo 1.0.0") {
		t.Errorf("err = %v", err)
	}
}
