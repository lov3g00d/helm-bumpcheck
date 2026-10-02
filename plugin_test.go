package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

func pluginVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version:\s*(\S+)$`).FindSubmatch(data)
	if m == nil {
		t.Fatal("no version in plugin.yaml")
	}
	return string(m[1])
}

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallBinary(t *testing.T) {
	for _, tool := range []string{"sh", "curl", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("no release archive for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	script, err := filepath.Abs("scripts/install-binary.sh")
	if err != nil {
		t.Fatal(err)
	}
	version := pluginVersion(t)
	archive := fmt.Sprintf("helm-bumpcheck_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	binaryOf := func(v string) string { return fmt.Sprintf("#!/bin/sh\necho 'helm-bumpcheck version %s'\n", v) }
	binary := binaryOf(version)
	hint := "; run 'helm plugin uninstall bumpcheck' before installing again"

	tests := []struct {
		name      string
		archive   []byte
		checksum  string
		installed string
		wantErr   string
	}{
		{name: "installs the release binary", archive: tarGz(t, "helm-bumpcheck", binary)},
		{name: "keeps a binary of the same version", installed: version},
		{name: "replaces an older binary", archive: tarGz(t, "helm-bumpcheck", binary), installed: "0.0.1"},
		{name: "missing release", wantErr: "could not download {url}/" + archive + hint},
		{name: "checksum mismatch", archive: tarGz(t, "helm-bumpcheck", binary), checksum: strings.Repeat("0", 64), wantErr: "checksum mismatch for {url}/" + archive + hint},
		{name: "archive without the binary", archive: tarGz(t, "README.md", "hi"), wantErr: "no helm-bumpcheck binary in {url}/" + archive + hint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginDir := t.TempDir()
			plugin, err := os.ReadFile("plugin.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), plugin, 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.installed != "" {
				writeExecutable(t, filepath.Join(pluginDir, "bin", "helm-bumpcheck"), binaryOf(tt.installed))
			}

			mirror := t.TempDir()
			url := "file://" + mirror + "/v" + version
			if tt.archive != nil {
				release := filepath.Join(mirror, "v"+version)
				if err := os.MkdirAll(release, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(release, archive), tt.archive, 0o644); err != nil {
					t.Fatal(err)
				}
				sum := tt.checksum
				if sum == "" {
					h := sha256.Sum256(tt.archive)
					sum = hex.EncodeToString(h[:])
				}
				checksums := fmt.Sprintf("%s  %s\n", sum, archive)
				if err := os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(checksums), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			cmd := exec.Command("sh", script)
			cmd.Env = append(os.Environ(), "HELM_PLUGIN_DIR="+pluginDir, "HELM_BUMPCHECK_RELEASE_URL=file://"+mirror)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()

			if tt.installed == version {
				if err != nil || stderr.Len() > 0 {
					t.Fatalf("err = %v, stderr = %q, want a silent exit 0", err, stderr.String())
				}
				return
			}
			lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
			if lines[0] != "helm-bumpcheck: warning: HELM_BUMPCHECK_RELEASE_URL file://"+mirror+" does not use https" {
				t.Errorf("first stderr line = %q, want the https warning", lines[0])
			}
			if tt.wantErr != "" {
				want := "helm-bumpcheck: " + strings.ReplaceAll(tt.wantErr, "{url}", url)
				if err == nil || len(lines) != 2 || lines[1] != want {
					t.Errorf("err = %v, stderr = %q, want one error line %q", err, stderr.String(), want)
				}
				if _, statErr := os.Stat(filepath.Join(pluginDir, "bin", "helm-bumpcheck")); statErr == nil {
					t.Error("a binary was installed after a failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("install: %v\n%s", err, stderr.String())
			}
			out, err := exec.Command(filepath.Join(pluginDir, "bin", "helm-bumpcheck"), "--version").Output()
			if err != nil || strings.TrimSpace(string(out)) != "helm-bumpcheck version "+version {
				t.Errorf("installed binary --version = %q, %v", out, err)
			}
		})
	}
}
