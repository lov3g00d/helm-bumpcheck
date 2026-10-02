package gitdiff

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

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
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
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

	var (
		charts []Chart
		skips  []Skip
	)
	for _, file := range splitNUL(out) {
		if path.Base(file) != "Chart.yaml" {
			continue
		}
		dir := path.Dir(file)
		inBase, inHead := baseFiles[file], headFiles[file]
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

		oldMeta, err := r.meta(ctx, baseSHA, file)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, err
			}
			skips = append(skips, Skip{Dir: dir, Reason: err.Error()})
			continue
		}
		newMeta, err := r.meta(ctx, headSHA, file)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, err
			}
			skips = append(skips, Skip{Dir: dir, Reason: err.Error()})
			continue
		}
		if oldMeta.Version == newMeta.Version {
			continue
		}
		charts = append(charts, Chart{
			Dir: dir, Name: newMeta.Name,
			FromVersion: oldMeta.Version, ToVersion: newMeta.Version,
			Base: baseSHA, Head: headSHA,
		})
	}
	slices.SortFunc(charts, func(a, b Chart) int { return cmp.Compare(a.Dir, b.Dir) })
	slices.SortFunc(skips, func(a, b Skip) int { return cmp.Compare(a.Dir, b.Dir) })
	return charts, skips, nil
}

func (r Repo) meta(ctx context.Context, rev, file string) (chartMeta, error) {
	data, err := r.show(ctx, rev, file)
	if err != nil {
		return chartMeta{}, err
	}
	var m chartMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return chartMeta{}, fmt.Errorf("parse %s at %s: %w", file, rev, err)
	}
	return m, nil
}

func (r Repo) Extract(ctx context.Context, rev, dir, dest string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "archive", "--format=tar", rev, "--", dir)
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
