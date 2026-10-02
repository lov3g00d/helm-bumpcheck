package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/cli"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		out  string
	}{
		{"success", nil, 0, ""},
		{"findings", fmt.Errorf("velero: %w", cli.ErrFindings), 2, "velero: " + cli.ErrFindings.Error() + "\n"},
		{"error", errors.New("helm pull: exit status 1"), 1, "Error: helm pull: exit status 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			if code := exitCode(&b, tt.err); code != tt.code {
				t.Errorf("exit code = %d, want %d", code, tt.code)
			}
			if b.String() != tt.out {
				t.Errorf("stderr = %q, want %q", b.String(), tt.out)
			}
		})
	}
}

func TestExitCodeStripsTerminalEscapes(t *testing.T) {
	stderr := "Error: \x1b]8;;http://evil.example\x07click\x1b]8;;\x07 \x1b[2Jcleared \u202eevil\u202c\nline two"
	err := fmt.Errorf("helm template velero: exit status 1: %s", stderr)
	var b strings.Builder
	if code := exitCode(&b, err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	want := "Error: helm template velero: exit status 1: Error: click cleared evil\nline two\n"
	if b.String() != want {
		t.Errorf("stderr = %q, want %q", b.String(), want)
	}
}
