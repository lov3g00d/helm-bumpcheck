package helm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
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

func TestTemplatePassesValuesAndDiscardsOutput(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, `echo "kind: Deployment"; [ "$*" = "template bumpcheck velero --version 4.0.1 --repo https://x -f a.yaml -f b.yaml" ] || { echo "bad args: $*" >&2; exit 1; }`)}
	if err := r.Template(context.Background(), Chart{Ref: "velero", Version: "4.0.1", Repo: "https://x"}, []string{"a.yaml", "b.yaml"}); err != nil {
		t.Fatalf("Template: %v", err)
	}
}

func TestTemplateFailureIsCleaned(t *testing.T) {
	stderr := "coalesce.go:301: warning: destination for velero.configuration.backupStorageLocation is a table. Ignoring non-table value\n" +
		"Error: execution error at (velero/templates/NOTES.txt:78:4):\n\n" +
		"#################################################################################\n" +
		"######   BREAKING: The config values passed contained no longer accepted    #####\n" +
		"#################################################################################\n\n" +
		"ERROR: Please make .configuration.backupStorageLocation from map to slice\n\n" +
		"REMOVED: .configuration.provider has been removed\n\n" +
		"Use --debug flag to render out invalid YAML\n"
	dir := t.TempDir()
	msgFile := filepath.Join(dir, "stderr.txt")
	if err := os.WriteFile(msgFile, []byte(stderr), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Bin: fakeHelm(t, "cat "+msgFile+" >&2; exit 1")}
	err := r.Template(context.Background(), Chart{Ref: "velero", Version: "4.0.1"}, nil)
	want := "Error: execution error at (velero/templates/NOTES.txt:78:4):\n" +
		"ERROR: Please make .configuration.backupStorageLocation from map to slice\n" +
		"REMOVED: .configuration.provider has been removed"
	if err == nil || err.Error() != want {
		t.Errorf("err =\n%v\nwant\n%s", err, want)
	}
}

func TestCleanRenderErrorCapsLines(t *testing.T) {
	got := cleanRenderError(strings.Repeat("line\n", 40))
	if n := strings.Count(got, "\n") + 1; n != maxRenderLines+1 || !strings.HasSuffix(got, "...") {
		t.Errorf("got %d lines ending %q", n, got[len(got)-3:])
	}
}

func TestErrorsRedactRepoCredentials(t *testing.T) {
	repo := "https://user:s3cret@charts.example.com"
	r := Runner{Bin: fakeHelm(t, `for a in "$@"; do case "$a" in https://*) echo "Error: failed to fetch $a/index.yaml" >&2 ;; esac; done; exit 1`)}
	_, err := r.Pull(context.Background(), Chart{Ref: "app", Repo: repo}, t.TempDir())
	if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "user:xxxxx@charts.example.com") {
		t.Errorf("err = %v", err)
	}
	err = r.Template(context.Background(), Chart{Ref: "app", Repo: repo}, nil)
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("template err = %v", err)
	}
}

const pullScript = `dest=""; prev=""
for a in "$@"; do
  if [ "$prev" = "--untardir" ]; then dest="$a"; fi
  prev="$a"
done
echo "$*" > "$dest/../args"
mkdir -p "$dest/tempo"
printf 'apiVersion: v2\nname: tempo\nversion: 1.24.4\nappVersion: "2.9.0"\nsources:\n  - https://github.com/grafana/tempo\ndependencies:\n  - name: minio\n    alias: store\n' > "$dest/tempo/Chart.yaml"
printf 'replicas: 1\n' > "$dest/tempo/values.yaml"`

func TestPullAndLoadDir(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, pullScript)}
	work := t.TempDir()
	dest := filepath.Join(work, "new")
	dir, err := r.Pull(context.Background(), Chart{Ref: "tempo", Version: "1.24.4", Repo: "https://x"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(dest, "tempo") {
		t.Errorf("dir = %q", dir)
	}
	args, _ := os.ReadFile(filepath.Join(work, "args"))
	if want := "pull tempo --untar --untardir " + dest + " --version 1.24.4 --repo https://x\n"; string(args) != want {
		t.Errorf("args = %q, want %q", args, want)
	}

	defaults, meta, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(defaults) != "replicas: 1\n" || meta.AppVersion != "2.9.0" || meta.Sources[0] != "https://github.com/grafana/tempo" {
		t.Errorf("defaults %q meta %+v", defaults, meta)
	}
	if got := meta.DependencyNames(); len(got) != 1 || got[0] != "store" {
		t.Errorf("dependencies = %v", got)
	}
}

func TestLoad(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, pullScript)}
	got, err := r.Load(context.Background(), Chart{Ref: "tempo", Version: "1.24.4", Repo: "https://x"}, filepath.Join(t.TempDir(), "new"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.Dir, "tempo") || got.Meta.AppVersion != "2.9.0" || !reflect.DeepEqual(got.Defaults, map[string]any{"replicas": 1.0}) {
		t.Errorf("Load = %+v", got)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"Chart.yaml": "apiVersion: v2\nname: app\nversion: 1.0.0\n", "values.yaml": "a: [\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Runner{}.Load(context.Background(), Chart{Ref: dir}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "parse values of "+dir) {
		t.Errorf("err = %v, want a values parse error naming the chart", err)
	}
}

func TestPullUsesLocalDirectory(t *testing.T) {
	dir := t.TempDir()
	r := Runner{Bin: fakeHelm(t, "echo should not run >&2; exit 1")}
	got, err := r.Pull(context.Background(), Chart{Ref: dir}, t.TempDir())
	if err != nil || got != dir {
		t.Errorf("Pull(local) = %q, %v", got, err)
	}
}

func TestPullErrorIncludesStderr(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, `echo 'Error: chart "nope" version "9" not found' >&2; exit 1`)}
	_, err := r.Pull(context.Background(), Chart{Ref: "nope", Version: "9"}, t.TempDir())
	var exitErr *exec.ExitError
	if err == nil || !strings.Contains(err.Error(), `chart "nope" version "9" not found`) || !errors.As(err, &exitErr) {
		t.Errorf("err = %v", err)
	}
}

func TestPullHonoursContext(t *testing.T) {
	r := Runner{Bin: fakeHelm(t, "exec sleep 10")}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := r.Pull(ctx, Chart{Ref: "tempo", Version: "1"}, t.TempDir()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("context did not stop helm")
	}
}

func TestLoadDirWithoutValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: lib\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaults, meta, err := LoadDir(dir)
	if err != nil || defaults != nil || meta.Name != "lib" {
		t.Errorf("LoadDir = %q, %+v, %v", defaults, meta, err)
	}
}

func TestLoadDirDependencies(t *testing.T) {
	tests := []struct {
		name         string
		chart        string
		requirements string
		want         []string
	}{
		{
			name:  "dependencies in Chart.yaml",
			chart: "apiVersion: v2\nname: app\nversion: 1.0.0\ndependencies:\n  - name: redis\n",
			want:  []string{"redis"},
		},
		{
			name:         "v1 dependencies in requirements.yaml",
			chart:        "apiVersion: v1\nname: app\nversion: 1.0.0\n",
			requirements: "dependencies:\n  - name: postgresql\n    alias: db\n  - name: redis\n",
			want:         []string{"db", "redis"},
		},
		{
			name:         "requirements.yaml replaces Chart.yaml dependencies like Helm's loader",
			chart:        "apiVersion: v1\nname: app\nversion: 1.0.0\ndependencies:\n  - name: redis\n",
			requirements: "dependencies:\n  - name: postgresql\n",
			want:         []string{"postgresql"},
		},
		{
			name:         "requirements.yaml without dependencies keeps Chart.yaml ones",
			chart:        "apiVersion: v2\nname: app\nversion: 1.0.0\ndependencies:\n  - name: redis\n",
			requirements: "{}\n",
			want:         []string{"redis"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{"Chart.yaml": tt.chart}
			if tt.requirements != "" {
				files["requirements.yaml"] = tt.requirements
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, meta, err := LoadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := meta.DependencyNames(); !slices.Equal(got, tt.want) {
				t.Errorf("DependencyNames = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMetadataCoercesLikeHelm(t *testing.T) {
	m, err := ParseMetadata([]byte("name: app\nversion: 1.0.0\nappVersion: 1.10\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.AppVersion != "1.1" {
		t.Errorf("AppVersion = %q, want %q as helm reads an unquoted 1.10", m.AppVersion, "1.1")
	}
}

func TestMissingDependencies(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"charts/minio/Chart.yaml":       "name: minio\nversion: 1.0.0\n",
		"charts/renamed/Chart.yaml":     "name: redis\nversion: 1.0.0\n",
		"charts/postgresql-12.1.0.tgz":  "",
		"charts/memcached-cluster.tgz":  "",
		"charts/notachart/values.yaml":  "",
		"charts/valkey-cluster-1.0.tgz": "",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta := Metadata{Dependencies: []Dependency{
		{Name: "minio"}, {Name: "redis", Alias: "cache"}, {Name: "postgresql"},
		{Name: "memcached"}, {Name: "notachart"}, {Name: "valkey"}, {Name: "tempo"},
	}}
	if got, want := MissingDependencies(dir, meta), []string{"memcached", "notachart", "valkey", "tempo"}; !slices.Equal(got, want) {
		t.Errorf("MissingDependencies = %q, want %q", got, want)
	}
	if got := MissingDependencies(t.TempDir(), Metadata{}); got != nil {
		t.Errorf("MissingDependencies without dependencies = %q", got)
	}
}

func TestBuildDependencies(t *testing.T) {
	work := t.TempDir()
	r := Runner{Bin: fakeHelm(t, `echo "$@" > `+filepath.Join(work, "args"))}
	if err := r.BuildDependencies(context.Background(), "/charts/umb"); err != nil {
		t.Fatal(err)
	}
	if args, _ := os.ReadFile(filepath.Join(work, "args")); string(args) != "dependency build /charts/umb\n" {
		t.Errorf("args = %q", args)
	}
}

func TestLoadDirRejectsBadRequirements(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v1\nname: app\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements.yaml"), []byte("dependencies: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "requirements.yaml") {
		t.Errorf("err = %v, want a requirements.yaml parse error", err)
	}
}
