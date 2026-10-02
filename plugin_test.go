package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPluginCompleteKeepsStderrOut(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "bin", "helm-bumpcheck"), "#!/bin/sh\necho \"$@\"\necho ':4'\necho 'Completion ended with directive: ShellCompDirectiveNoFileComp' >&2\n")

	cmd := exec.Command("./plugin.complete", "-o", "")
	cmd.Env = append(os.Environ(), "HELM_PLUGIN_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plugin.complete: %v\n%s", err, out)
	}
	if want := "__complete -o \n:4\n"; string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
