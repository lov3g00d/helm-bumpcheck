package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
