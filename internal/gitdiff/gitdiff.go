package gitdiff

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
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
	out, err := r.git(ctx, "rev-parse", "--verify", rev+"^{commit}")
	return strings.TrimSpace(string(out)), err
}

func (r Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, "merge-base", a, b)
	return strings.TrimSpace(string(out)), err
}

func (r Repo) exists(ctx context.Context, rev, file string) bool {
	_, err := r.git(ctx, "cat-file", "-e", rev+":"+file)
	return err == nil
}

func (r Repo) show(ctx context.Context, rev, file string) ([]byte, error) {
	return r.git(ctx, "show", rev+":"+file)
}

func (r Repo) nested(ctx context.Context, rev, dir string) bool {
	for d := path.Dir(dir); d != "." && d != "/"; d = path.Dir(d) {
		if r.exists(ctx, rev, d+"/Chart.yaml") {
			return true
		}
	}
	return false
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
	baseSHA, err := r.MergeBase(ctx, base, headSHA)
	if err != nil {
		return nil, nil, err
	}

	out, err := r.git(ctx, "diff", "--name-only", "--no-renames", baseSHA, headSHA)
	if err != nil {
		return nil, nil, err
	}

	var (
		charts []Chart
		skips  []Skip
	)
	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if path.Base(file) != "Chart.yaml" {
			continue
		}
		dir := path.Dir(file)
		inBase, inHead := r.exists(ctx, baseSHA, file), r.exists(ctx, headSHA, file)
		rev := headSHA
		if !inHead {
			rev = baseSHA
		}
		if r.nested(ctx, rev, dir) {
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
			return nil, nil, err
		}
		newMeta, err := r.meta(ctx, headSHA, file)
		if err != nil {
			return nil, nil, err
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
	sort.Slice(charts, func(i, j int) bool { return charts[i].Dir < charts[j].Dir })
	sort.Slice(skips, func(i, j int) bool { return skips[i].Dir < skips[j].Dir })
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
