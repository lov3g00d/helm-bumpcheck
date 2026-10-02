package main

import (
	"os"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/report"
)

func TestExampleWorkflowFindsCommentByMarker(t *testing.T) {
	data, err := os.ReadFile("examples/github-pr-comment.yml")
	if err != nil {
		t.Fatal(err)
	}
	if want := `startswith("` + report.Marker + `")`; !strings.Contains(string(data), want) {
		t.Errorf("examples/github-pr-comment.yml does not look up its comment with %s", want)
	}
}

func TestExampleWorkflowCommentGating(t *testing.T) {
	data, err := os.ReadFile("examples/github-pr-comment.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, want := range []string{
		`if [ -s bumpcheck.md ]; then`,
		`if: env.SAME_REPO == 'true' && steps.check.outputs.report == 'true'`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("examples/github-pr-comment.yml is missing %s", want)
		}
	}

	const empty = "No chart versions changed between"
	if !strings.Contains(workflow, `grep -q '^`+empty+`' bumpcheck.md`) {
		t.Errorf("examples/github-pr-comment.yml does not skip new comments for %q", empty)
	}
	var b strings.Builder
	if err := report.WriteBatch(&b, "markdown", report.Batch{Base: "origin/main", Head: "HEAD"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "\n"+empty+" ") {
		t.Errorf("an empty batch no longer starts a line with %q:\n%s", empty, b.String())
	}
}
