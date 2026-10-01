package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const fakeHelmScript = `#!/bin/sh
case "$*" in
  "show chart "*"--version 1.0.0"*)
    printf 'name: mychart\nversion: 1.0.0\nappVersion: "1.0.0"\n'
    ;;
  "show chart "*"--version 2.0.0"*)
    printf 'name: mychart\nversion: 2.0.0\nappVersion: "2.0.0"\n'
    ;;
  "template "*"--version 2.0.0"*)
    if [ -n "$FAKE_RENDER_FAIL" ]; then
      echo "Error: execution error at (mychart/templates/NOTES.txt:1:1):" >&2
      echo "ERROR: legacy mode was removed, unset legacy.enabled" >&2
      exit 1
    fi
    ;;
  "template "*)
    ;;
  *"--version 1.0.0"*)
    printf 'replicas: 1\nport: 3100\nlegacy:\n  enabled: false\nextraConfig: {}\n'
    ;;
  *"--version 2.0.0"*)
    printf 'replicas: 1\nport: 3200\nextraConfig: {}\nnewThing: true\n'
    ;;
  *)
    echo "Error: chart version not found" >&2
    exit 1
    ;;
esac
`

func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "helm")
	if err := os.WriteFile(bin, []byte(fakeHelmScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HELM_BIN", bin)

	userValues := filepath.Join(dir, "values.yaml")
	data := "legacy:\n  enabled: true\nextraConfig:\n  team: tracing\n"
	if err := os.WriteFile(userValues, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return userValues
}

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestTextOutput(t *testing.T) {
	userValues := setup(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, want := range []string{
		"mychart 1.0.0 -> 2.0.0",
		"  removed       legacy.enabled  no longer in the chart",
		"\nCHECK\n  default       port  3100 -> 3200",
		"  free-form     extraConfig.team",
		"1 key added in 2.0.0. Use -o json to list them.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestJSONOutput(t *testing.T) {
	userValues := setup(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "-o", "json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var r values.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(r.Removed) != 1 || r.Removed[0] != "legacy.enabled" {
		t.Errorf("removed = %v", r.Removed)
	}
	if len(r.DefaultChanged) != 1 || r.DefaultChanged[0].Path != "port" {
		t.Errorf("defaultChanged = %v", r.DefaultChanged)
	}
}

func TestNothingAffected(t *testing.T) {
	setup(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "1.0.0")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Result: nothing in this upgrade affects the values you set") {
		t.Errorf("output:\n%s", out)
	}
}

func TestHelmErrorPropagates(t *testing.T) {
	setup(t)
	_, err := execute(t, "mychart", "--from", "1.0.0", "--to", "9.9.9")
	if err == nil || !strings.Contains(err.Error(), "chart version not found") {
		t.Errorf("err = %v, want helm stderr", err)
	}
}

func TestRejectsUnknownOutput(t *testing.T) {
	setup(t)
	_, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-o", "yaml")
	if err == nil || !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("err = %v", err)
	}
}

func TestRequiresFromAndTo(t *testing.T) {
	setup(t)
	if _, err := execute(t, "mychart", "--from", "1.0.0"); err == nil {
		t.Error("expected error without --to")
	}
}

func TestRequiresChart(t *testing.T) {
	setup(t)
	if _, err := execute(t, "--from", "1.0.0", "--to", "2.0.0"); err == nil {
		t.Error("expected error without CHART")
	}
}

func TestRenderCheck(t *testing.T) {
	userValues := setup(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues)
	if err != nil || !strings.Contains(out, "Render: both versions render with your values") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}

	t.Setenv("FAKE_RENDER_FAIL", "1")
	out, err = execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{
		"  render        the new chart rejects your values",
		"                ERROR: legacy mode was removed, unset legacy.enabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	out, _ = execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--render=false")
	if strings.Contains(out, "Render") {
		t.Errorf("--render=false still rendered:\n%s", out)
	}
}

func TestFailOn(t *testing.T) {
	userValues := setup(t)
	out, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--fail-on", "removed,type,render")
	if !errors.Is(err, ErrFindings) || !strings.Contains(err.Error(), "removed") || strings.Contains(err.Error(), "render") {
		t.Errorf("err = %v, want findings for removed only", err)
	}
	if !strings.Contains(out, "removed       legacy.enabled") {
		t.Errorf("report not written before failing:\n%s", out)
	}

	if _, err := execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "-f", userValues, "--fail-on", "type"); err != nil {
		t.Errorf("err = %v, want none when no type changes", err)
	}

	_, err = execute(t, "mychart", "--from", "1.0.0", "--to", "2.0.0", "--fail-on", "notes")
	if err == nil || errors.Is(err, ErrFindings) || !strings.Contains(err.Error(), "unknown --fail-on kind") {
		t.Errorf("err = %v, want unknown kind error", err)
	}
}
