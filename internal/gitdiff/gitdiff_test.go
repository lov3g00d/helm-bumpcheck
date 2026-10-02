package gitdiff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type testRepo struct {
	t   *testing.T
	dir string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &testRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	base := []string{"-C", r.dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}
	out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(file, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, file)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func chartYAML(name, version string) string {
	return "apiVersion: v2\nname: " + name + "\nversion: " + version + "\nappVersion: \"1.0\"\n"
}

func setupHistory(t *testing.T) (*testRepo, string, string) {
	r := newTestRepo(t)
	r.write("charts/app/Chart.yaml", chartYAML("app", "1.0.0"))
	r.write("charts/app/values.yaml", "replicas: 1\n")
	r.write("charts/app/values_prod.yaml", "replicas: 3\n")
	r.write("charts/app/charts/sub/Chart.yaml", chartYAML("sub", "1.0.0"))
	r.write("charts/old/Chart.yaml", chartYAML("old", "1.0.0"))
	r.write("charts/same/Chart.yaml", chartYAML("same", "1.0.0"))
	base := r.commit("base")

	r.write("charts/app/Chart.yaml", chartYAML("app", "2.0.0"))
	r.write("charts/app/values.yaml", "replicas: 2\n")
	r.write("charts/app/charts/sub/Chart.yaml", chartYAML("sub", "2.0.0"))
	r.write("charts/new/Chart.yaml", chartYAML("new", "1.0.0"))
	r.git("rm", "-q", "-r", "charts/old")
	r.write("charts/same/Chart.yaml", chartYAML("same", "1.0.0")+"description: only metadata changed\n")
	head := r.commit("bump app")
	return r, base, head
}

func TestChangedCharts(t *testing.T) {
	r, base, head := setupHistory(t)
	charts, skips, err := Repo{Dir: r.dir}.ChangedCharts(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}

	want := []Chart{{Dir: "charts/app", Name: "app", FromVersion: "1.0.0", ToVersion: "2.0.0", Base: base, Head: head}}
	if !reflect.DeepEqual(charts, want) {
		t.Errorf("charts = %+v\nwant %+v", charts, want)
	}
	wantSkips := []Skip{
		{Dir: "charts/new", Reason: "new chart, nothing to compare"},
		{Dir: "charts/old", Reason: "chart removed"},
	}
	if !reflect.DeepEqual(skips, wantSkips) {
		t.Errorf("skips = %+v\nwant %+v", skips, wantSkips)
	}
}

func TestChangedChartsUsesMergeBase(t *testing.T) {
	r, base, _ := setupHistory(t)
	r.git("checkout", "-q", "-b", "feature", base)
	r.write("charts/same/Chart.yaml", chartYAML("same", "1.1.0"))
	r.commit("bump same on a branch")

	charts, _, err := Repo{Dir: r.dir}.ChangedCharts(context.Background(), "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) != 1 || charts[0].Dir != "charts/same" || charts[0].Base != base {
		t.Errorf("charts = %+v, want only charts/same compared against the merge base", charts)
	}
}

func TestExtract(t *testing.T) {
	r, base, head := setupHistory(t)
	repo := Repo{Dir: r.dir}

	for rev, want := range map[string]string{base: "replicas: 1\n", head: "replicas: 2\n"} {
		dir, err := repo.Extract(context.Background(), rev, "charts/app", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
		if err != nil || string(got) != want {
			t.Errorf("values.yaml at %s = %q, %v; want %q", rev[:7], got, err, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "charts", "sub", "Chart.yaml")); err != nil {
			t.Errorf("subchart not extracted: %v", err)
		}
	}
}

func TestExtractBadRevision(t *testing.T) {
	r, _, _ := setupHistory(t)
	if _, err := (Repo{Dir: r.dir}).Extract(context.Background(), "nope", "charts/app", t.TempDir()); err == nil {
		t.Error("expected error for unknown revision")
	}
}

func TestSafeJoin(t *testing.T) {
	dest := t.TempDir()
	if _, err := safeJoin(dest, "charts/app/values.yaml"); err != nil {
		t.Errorf("safe path rejected: %v", err)
	}
	for _, bad := range []string{"../escape", "charts/../../escape", "/../../etc/passwd"} {
		if _, err := safeJoin(dest, bad); err == nil {
			t.Errorf("safeJoin(%q) allowed an escape", bad)
		}
	}
}

func TestChangedChartsEdgeCases(t *testing.T) {
	r := newTestRepo(t)
	r.write("Chart.yaml", chartYAML("umbrella", "1.0.0"))
	r.write("charts/dep/Chart.yaml", chartYAML("dep", "1.0.0"))
	r.write("café/Chart.yaml", chartYAML("cafe", "1.0.0"))
	r.write("broken/Chart.yaml", chartYAML("broken", "1.0.0"))
	base := r.commit("base")
	r.write("charts/dep/Chart.yaml", chartYAML("dep", "2.0.0"))
	r.write("café/Chart.yaml", chartYAML("cafe", "2.0.0"))
	r.write("broken/Chart.yaml", "name: [unclosed\n")
	head := r.commit("head")

	charts, skips, err := Repo{Dir: r.dir}.ChangedCharts(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) != 1 || charts[0].Dir != "café" || charts[0].ToVersion != "2.0.0" {
		t.Errorf("charts = %+v, want only the non-ASCII chart; the root chart's subchart must be nested", charts)
	}
	if len(skips) != 1 || skips[0].Dir != "broken" || !strings.Contains(skips[0].Reason, "parse broken/Chart.yaml") {
		t.Errorf("skips = %+v, want the malformed chart skipped", skips)
	}
}

func TestChangedChartsRejectsOptionLikeRefs(t *testing.T) {
	r, _, head := setupHistory(t)
	for _, base := range []string{"--octopus", "-h"} {
		if _, _, err := (Repo{Dir: r.dir}).ChangedCharts(context.Background(), base, head); err == nil {
			t.Errorf("base %q was accepted", base)
		}
	}
}

func TestChangedChartsHonoursCancel(t *testing.T) {
	r, base, head := setupHistory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := (Repo{Dir: r.dir}).ChangedCharts(ctx, base, head); err == nil {
		t.Error("cancelled context returned no error")
	}
}

func TestNested(t *testing.T) {
	files := map[string]bool{"Chart.yaml": true, "charts/a/Chart.yaml": true, "apps/b/Chart.yaml": true}
	for dir, want := range map[string]bool{".": false, "charts/a": true, "apps/b": false} {
		if got := nested(files, dir); got != want {
			t.Errorf("nested(%q) = %v, want %v", dir, got, want)
		}
	}
	if !nested(map[string]bool{"apps/b/Chart.yaml": true}, "apps/b/charts/c") {
		t.Error("vendored subchart not reported as nested")
	}
}

func TestChangedChartsDependencyOnly(t *testing.T) {
	r := newTestRepo(t)
	r.write("umbrella/Chart.yaml", chartYAML("umbrella", "1.0.0")+`dependencies:
  - name: redis
    version: 19.0.0
    repository: https://charts.bitnami.com/bitnami
  - name: redis
    alias: cache
    version: ">=1.2, <2.0"
    repository: https://charts.bitnami.com/bitnami
  - name: postgresql
    version: 12.0.0
    repository: https://charts.bitnami.com/bitnami
`)
	r.write("legacy/Chart.yaml", "apiVersion: v1\nname: legacy\nversion: 1.0.0\n")
	r.write("legacy/requirements.yaml", "dependencies:\n  - name: mysql\n    version: 1.0.0\n    repository: https://old.example\n")
	r.write("both/Chart.yaml", "apiVersion: v1\nname: both\nversion: 1.0.0\n")
	r.write("both/requirements.yaml", "dependencies:\n  - name: nginx\n    version: 1.0.0\n")
	r.write("meta/Chart.yaml", chartYAML("meta", "1.0.0")+"dependencies:\n  - name: redis\n    version: 19.0.0\n    condition: redis.enabled\n")
	base := r.commit("base")

	r.write("umbrella/Chart.yaml", chartYAML("umbrella", "1.0.0")+`dependencies:
  - name: redis
    version: 20.0.0
    repository: https://charts.bitnami.com/bitnami
  - name: redis
    alias: cache
    version: ">=2.0, <3.0"
    repository: oci://registry-1.docker.io/bitnamicharts
  - name: valkey
    version: 1.0.0
    repository: https://valkey.example
`)
	r.write("legacy/requirements.yaml", "dependencies:\n  - name: mysql\n    version: 2.0.0\n    repository: https://old.example\n")
	r.write("both/Chart.yaml", "apiVersion: v1\nname: both\nversion: 1.0.0\ndescription: touched\n")
	r.write("both/requirements.yaml", "dependencies:\n  - name: nginx\n    version: 1.1.0\n")
	r.write("meta/Chart.yaml", chartYAML("meta", "1.0.0")+"dependencies:\n  - name: redis\n    version: 19.0.0\n    condition: redis.on\n")
	head := r.commit("bump dependencies")

	charts, skips, err := Repo{Dir: r.dir}.ChangedCharts(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) != 0 {
		t.Errorf("charts = %+v, want none", charts)
	}
	want := []Skip{
		{Dir: "both", Reason: "dependency changes not analyzed: nginx 1.0.0 -> 1.1.0"},
		{Dir: "legacy", Reason: "dependency changes not analyzed: mysql 1.0.0 -> 2.0.0"},
		{Dir: "umbrella", Reason: "dependency changes not analyzed: cache (redis) >=1.2, <2.0 -> >=2.0, <3.0; " +
			"cache (redis) repository https://charts.bitnami.com/bitnami -> oci://registry-1.docker.io/bitnamicharts; " +
			"postgresql 12.0.0 removed; redis 19.0.0 -> 20.0.0; valkey 1.0.0 added"},
	}
	if !reflect.DeepEqual(skips, want) {
		t.Errorf("skips = %+v\nwant %+v", skips, want)
	}
}

func TestChangedChartsFromSubdirectory(t *testing.T) {
	r, base, head := setupHistory(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "diff.relative")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	repo := Repo{Dir: filepath.Join(r.dir, "charts", "same")}

	charts, skips, err := repo.ChangedCharts(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) != 1 || charts[0].Dir != "charts/app" || len(skips) != 2 {
		t.Fatalf("charts = %+v, skips = %+v; want charts/app and two skips with paths from the repository root", charts, skips)
	}
	dir, err := repo.Extract(context.Background(), head, charts[0].Dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "values.yaml")); err != nil || string(got) != "replicas: 2\n" {
		t.Errorf("values.yaml = %q, %v", got, err)
	}
}

func TestExtractIgnoresExportAttributes(t *testing.T) {
	r := newTestRepo(t)
	r.write("charts/app/Chart.yaml", chartYAML("app", "1.0.0"))
	r.write("charts/app/.gitattributes", "templates/** export-ignore\nvalues.yaml export-subst\n")
	r.write("charts/app/values.yaml", "author: $Format:%an$\n")
	r.write("charts/app/templates/cm.yaml", "{{ fail \"new version breaks\" }}\n")
	r.write("charts/app/hook.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(r.dir, "charts/app/hook.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	rev := r.commit("chart with export attributes")

	dest := t.TempDir()
	dir, err := Repo{Dir: r.dir}.Extract(context.Background(), rev, "charts/app", dest)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{}
	for _, f := range strings.Split(r.git("ls-tree", "-r", "--name-only", rev, "--", "charts/app"), "\n") {
		want[f] = true
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s not extracted: %v", f, err)
			continue
		}
		blob, err := exec.Command("git", "-C", r.dir, "cat-file", "blob", rev+":"+f).Output()
		if err != nil || string(got) != string(blob) {
			t.Errorf("%s = %q, want the committed blob %q (%v)", f, got, blob, err)
		}
	}
	err = filepath.WalkDir(dest, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dest, p)
		if !want[filepath.ToSlash(rel)] {
			t.Errorf("unexpected file %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "hook.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("hook.sh lost its executable bit: %v, %v", info, err)
	}
}

func TestExtractMissingDirectory(t *testing.T) {
	r, base, _ := setupHistory(t)
	if _, err := (Repo{Dir: r.dir}).Extract(context.Background(), base, "charts/nope", t.TempDir()); err == nil {
		t.Error("expected error for a directory with no files")
	}
}

func fakeGit(t *testing.T, sleepOn string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = " + sleepOn + " ] && exec sleep 30; done\nexec '" + gitPath + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGitReportsContextErrors(t *testing.T) {
	r, base, head := setupHistory(t)
	repo := Repo{Dir: r.dir}
	for name, run := range map[string]func(context.Context) error{
		"rev-parse": func(ctx context.Context) error {
			_, _, err := repo.ChangedCharts(ctx, base, head)
			return err
		},
		"cat-file": func(ctx context.Context) error {
			_, err := repo.Extract(ctx, head, "charts/app", t.TempDir())
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			fakeGit(t, name)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if err := run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}
