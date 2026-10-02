package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCommandPathUnderHelm(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		want       []string
	}{
		{"standalone", "", []string{"helm-bumpcheck CHART --from VERSION", "helm-bumpcheck diff [--base REF]"}},
		{"helm plugin", "bumpcheck", []string{"helm bumpcheck CHART --from VERSION", "helm bumpcheck diff [--base REF]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HELM_PLUGIN_NAME", tt.pluginName)
			root, err := execute(t, "--help")
			if err != nil {
				t.Fatal(err)
			}
			diff, err := execute(t, "diff", "--help")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(root, "Usage:\n  "+tt.want[0]) {
				t.Errorf("root usage missing %q:\n%s", tt.want[0], root)
			}
			if !strings.Contains(diff, "Usage:\n  "+tt.want[1]) {
				t.Errorf("diff usage missing %q:\n%s", tt.want[1], diff)
			}
		})
	}
}

func TestVersionOutput(t *testing.T) {
	for _, pluginName := range []string{"", "bumpcheck"} {
		t.Setenv("HELM_PLUGIN_NAME", pluginName)
		out, err := execute(t, "--version")
		if err != nil {
			t.Fatal(err)
		}
		if out != "helm-bumpcheck version test\n" {
			t.Errorf("HELM_PLUGIN_NAME=%q: --version = %q, install-binary.sh reads the last field", pluginName, out)
		}
	}
}

func TestHelpListsEnvironment(t *testing.T) {
	out, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"TYPESAFE_API_KEY", "TYPESAFE_MODEL", "TYPESAFE_BASE_URL", "GITHUB_TOKEN", "GITHUB_API_URL", "HELM_BIN", "NO_COLOR"} {
		if !strings.Contains(out, "\n  "+env+" ") {
			t.Errorf("--help does not list %s:\n%s", env, out)
		}
	}
}

func TestHelpListsEveryFindingKind(t *testing.T) {
	out, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out), " ")
	for _, kind := range []string{"removed", "changed the type", "defaults that changed", "free-form", "render", "meaning may have changed", "starts declaring", "undeclared keys under a subchart or global", "release-note"} {
		if !strings.Contains(help, kind) {
			t.Errorf("--help does not mention %q:\n%s", kind, out)
		}
	}
}

func TestCompletion(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-o", ""}, "json\nmarkdown\ntext\n:4\n"},
		{[]string{"--fail-on", ""}, "removed\ntype\nrender\ndefault\n:4\n"},
		{[]string{"diff", "-o", ""}, "json\nmarkdown\ntext\n:4\n"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := NewRootCmd("test")
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append([]string{"__complete"}, tt.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.want {
				t.Errorf("completions = %q, want %q", out.String(), tt.want)
			}
		})
	}
}
