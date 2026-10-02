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
