package gitdiff

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const waitDelay = 5 * time.Second

type Repo struct {
	Dir string
}

type Chart struct {
	Dir         string
	Name        string
	FromVersion string
	ToVersion   string
	Base        string
	Head        string
}

type Skip struct {
	Dir    string
	Reason string
}

func (r Repo) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir}, args...)...)
	cmd.WaitDelay = waitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (r Repo) Resolve(ctx context.Context, rev string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	return strings.TrimSpace(string(out)), err
}

func (r Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, "merge-base", "--end-of-options", a, b)
	return strings.TrimSpace(string(out)), err
}

func (r Repo) files(ctx context.Context, rev string) (map[string]bool, error) {
	out, err := r.git(ctx, "ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, f := range splitNUL(out) {
		set[f] = true
	}
	return set, nil
}

func splitNUL(out []byte) []string {
	var names []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			names = append(names, f)
		}
	}
	return names
}

func (r Repo) show(ctx context.Context, rev, file string) ([]byte, error) {
	return r.git(ctx, "show", rev+":"+file)
}

func nested(files map[string]bool, dir string) bool {
	parent := path.Dir(dir)
	if dir == "." || path.Base(parent) != "charts" {
		return false
	}
	owner := path.Dir(parent)
	if owner == "." {
		return files["Chart.yaml"]
	}
	return files[owner+"/Chart.yaml"]
}

type chartMeta struct {
	Name         string       `yaml:"name"`
	Version      string       `yaml:"version"`
	Dependencies []dependency `yaml:"dependencies"`
}

type dependency struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Repository string `yaml:"repository"`
	Alias      string `yaml:"alias"`
}

func (r Repo) ChangedCharts(ctx context.Context, base, head string) ([]Chart, []Skip, error) {
	headSHA, err := r.Resolve(ctx, head)
	if err != nil {
		return nil, nil, err
	}
	baseRef, err := r.Resolve(ctx, base)
	if err != nil {
		return nil, nil, err
	}
	baseSHA, err := r.MergeBase(ctx, baseRef, headSHA)
	if err != nil {
		return nil, nil, err
	}
	baseFiles, err := r.files(ctx, baseSHA)
	if err != nil {
		return nil, nil, err
	}
	headFiles, err := r.files(ctx, headSHA)
	if err != nil {
		return nil, nil, err
	}

	out, err := r.git(ctx, "diff", "-z", "--name-only", "--no-renames", baseSHA, headSHA)
	if err != nil {
		return nil, nil, err
	}
	dirs := map[string]bool{}
	for _, file := range splitNUL(out) {
		switch path.Base(file) {
		case "Chart.yaml", "requirements.yaml":
			dirs[path.Dir(file)] = true
		}
	}

	var (
		charts []Chart
		skips  []Skip
	)
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		file := path.Join(dir, "Chart.yaml")
		inBase, inHead := baseFiles[file], headFiles[file]
		if !inBase && !inHead {
			continue
		}
		tree := headFiles
		if !inHead {
			tree = baseFiles
		}
		if nested(tree, dir) {
			continue
		}
		switch {
		case !inBase:
			skips = append(skips, Skip{Dir: dir, Reason: "new chart, nothing to compare"})
			continue
		case !inHead:
			skips = append(skips, Skip{Dir: dir, Reason: "chart removed"})
			continue
		}

		oldMeta, err := r.meta(ctx, baseSHA, dir, baseFiles)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, err
			}
			skips = append(skips, Skip{Dir: dir, Reason: err.Error()})
			continue
		}
		newMeta, err := r.meta(ctx, headSHA, dir, headFiles)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, err
			}
			skips = append(skips, Skip{Dir: dir, Reason: err.Error()})
			continue
		}
		if oldMeta.Version == newMeta.Version {
			if changes := dependencyChanges(oldMeta.Dependencies, newMeta.Dependencies); len(changes) > 0 {
				skips = append(skips, Skip{Dir: dir, Reason: "dependency changes not analyzed: " + strings.Join(changes, "; ")})
			}
			continue
		}
		charts = append(charts, Chart{
			Dir: dir, Name: newMeta.Name,
			FromVersion: oldMeta.Version, ToVersion: newMeta.Version,
			Base: baseSHA, Head: headSHA,
		})
	}
	return charts, skips, nil
}

func (r Repo) meta(ctx context.Context, rev, dir string, files map[string]bool) (chartMeta, error) {
	var m chartMeta
	if err := r.decode(ctx, rev, path.Join(dir, "Chart.yaml"), &m); err != nil {
		return chartMeta{}, err
	}
	if req := path.Join(dir, "requirements.yaml"); files[req] {
		var v1 struct {
			Dependencies []dependency `yaml:"dependencies"`
		}
		if err := r.decode(ctx, rev, req, &v1); err != nil {
			return chartMeta{}, err
		}
		if v1.Dependencies != nil {
			m.Dependencies = v1.Dependencies
		}
	}
	return m, nil
}

func (r Repo) decode(ctx context.Context, rev, file string, v any) error {
	data, err := r.show(ctx, rev, file)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s at %s: %w", file, rev, err)
	}
	return nil
}

func dependencyChanges(oldDeps, newDeps []dependency) []string {
	index := func(deps []dependency) map[string]dependency {
		m := make(map[string]dependency, len(deps))
		for _, d := range deps {
			m[cmp.Or(d.Alias, d.Name)] = d
		}
		return m
	}
	before, after := index(oldDeps), index(newDeps)
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}

	var changes []string
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		o, inOld := before[k]
		n, inNew := after[k]
		switch {
		case !inOld:
			changes = append(changes, fmt.Sprintf("%s %s added", n.label(), orNone(n.Version)))
		case !inNew:
			changes = append(changes, fmt.Sprintf("%s %s removed", o.label(), orNone(o.Version)))
		default:
			if o.Name != n.Name {
				changes = append(changes, fmt.Sprintf("%s chart %s -> %s", k, o.Name, n.Name))
			}
			if o.Version != n.Version {
				changes = append(changes, fmt.Sprintf("%s %s -> %s", n.label(), orNone(o.Version), orNone(n.Version)))
			}
			if o.Repository != n.Repository {
				changes = append(changes, fmt.Sprintf("%s repository %s -> %s", n.label(), orNone(o.Repository), orNone(n.Repository)))
			}
		}
	}
	return changes
}

func (d dependency) label() string {
	if d.Alias != "" && d.Alias != d.Name {
		return d.Alias + " (" + d.Name + ")"
	}
	return d.Name
}

func orNone(s string) string {
	return cmp.Or(s, "(none)")
}

func (r Repo) Extract(ctx context.Context, rev, dir, dest string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "archive", "--format=tar", rev, "--", dir)
	cmd.WaitDelay = waitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	extractErr := untar(stdout, dest)
	if extractErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("git archive %s %s: %w: %s", rev, dir, err, strings.TrimSpace(stderr.String()))
	}
	if extractErr != nil {
		return "", extractErr
	}
	return filepath.Join(dest, filepath.FromSlash(dir)), nil
}

func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		}
	}
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func safeJoin(dest, name string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(name))
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes %s", name, dest)
	}
	return target, nil
}
