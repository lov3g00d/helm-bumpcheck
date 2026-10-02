package gitdiff

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lov3g00d/helm-bumpcheck/internal/helm"
)

const waitDelay = 5 * time.Second

type Repo struct {
	Dir string
}

type Chart struct {
	Dir          string
	Name         string
	FromVersion  string
	ToVersion    string
	Base         string
	Head         string
	Dependencies []string
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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
	out, err := r.git(ctx, "ls-tree", "-r", "-z", "--full-tree", "--name-only", rev)
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
	for f := range strings.SplitSeq(string(out), "\x00") {
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

	out, err := r.git(ctx, "diff-tree", "-r", "-z", "--name-only", "--no-renames", baseSHA, headSHA)
	if err != nil {
		return nil, nil, err
	}
	dirs := map[string]bool{}
	for _, file := range splitNUL(out) {
		switch path.Base(file) {
		case "Chart.yaml", "requirements.yaml", "Chart.lock", "requirements.lock":
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

		before, err := r.chart(ctx, baseSHA, dir, baseFiles)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, err
			}
			skips = append(skips, Skip{Dir: dir, Reason: err.Error()})
			continue
		}
		after, err := r.chart(ctx, headSHA, dir, headFiles)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, err
			}
			skips = append(skips, Skip{Dir: dir, Reason: err.Error()})
			continue
		}
		oldMeta, newMeta := before.meta, after.meta
		changes := slices.Concat(
			dependencyChanges(oldMeta.Dependencies, newMeta.Dependencies),
			lockChanges(oldMeta.Dependencies, newMeta.Dependencies, before.lock, after.lock),
		)
		if oldMeta.Version == newMeta.Version {
			if len(changes) > 0 {
				skips = append(skips, Skip{Dir: dir, Reason: "dependency changes not analyzed: " + strings.Join(changes, "; ")})
			}
			continue
		}
		charts = append(charts, Chart{
			Dir: dir, Name: newMeta.Name,
			FromVersion: oldMeta.Version, ToVersion: newMeta.Version,
			Base: baseSHA, Head: headSHA,
			Dependencies: changes,
		})
	}
	return charts, skips, nil
}

type chartState struct {
	meta helm.Metadata
	lock []helm.Dependency
}

func (r Repo) chart(ctx context.Context, rev, dir string, files map[string]bool) (chartState, error) {
	chartYAML, err := r.show(ctx, rev, path.Join(dir, "Chart.yaml"))
	if err != nil {
		return chartState{}, err
	}
	var requirementsYAML []byte
	if req := path.Join(dir, "requirements.yaml"); files[req] {
		if requirementsYAML, err = r.show(ctx, rev, req); err != nil {
			return chartState{}, err
		}
	}
	m, err := helm.ParseMetadata(chartYAML, requirementsYAML)
	if err != nil {
		return chartState{}, fmt.Errorf("%s at %s: %w", dir, rev, err)
	}
	st := chartState{meta: m}
	for _, name := range []string{"Chart.lock", "requirements.lock"} {
		file := path.Join(dir, name)
		if !files[file] {
			continue
		}
		data, err := r.show(ctx, rev, file)
		if err != nil {
			return chartState{}, err
		}
		if st.lock, err = helm.ParseLock(data); err != nil {
			return chartState{}, fmt.Errorf("%s at %s: %w", file, rev, err)
		}
		break
	}
	return st, nil
}

func dependencyChanges(oldDeps, newDeps []helm.Dependency) []string {
	index := func(deps []helm.Dependency) map[string]helm.Dependency {
		m := make(map[string]helm.Dependency, len(deps))
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
			changes = append(changes, fmt.Sprintf("%s %s added", label(n), orNone(n.Version)))
		case !inNew:
			changes = append(changes, fmt.Sprintf("%s %s removed", label(o), orNone(o.Version)))
		default:
			if o.Name != n.Name {
				changes = append(changes, fmt.Sprintf("%s chart %s -> %s", k, o.Name, n.Name))
			}
			if o.Version != n.Version {
				changes = append(changes, fmt.Sprintf("%s %s -> %s", label(n), orNone(o.Version), orNone(n.Version)))
			}
			if o.Repository != n.Repository {
				changes = append(changes, fmt.Sprintf("%s repository %s -> %s", label(n), orNone(o.Repository), orNone(n.Repository)))
			}
		}
	}
	return changes
}

func lockChanges(oldDeps, newDeps, oldLock, newLock []helm.Dependency) []string {
	versions := func(deps []helm.Dependency) map[string][]string {
		m := map[string][]string{}
		for _, d := range deps {
			m[d.Name] = append(m[d.Name], orNone(d.Version))
		}
		for _, v := range m {
			slices.Sort(v)
		}
		return m
	}
	oldDecl, newDecl, before, after := versions(oldDeps), versions(newDeps), versions(oldLock), versions(newLock)

	var changes []string
	for _, name := range slices.Sorted(maps.Keys(after)) {
		o, ok := before[name]
		n := after[name]
		if !ok || slices.Equal(o, n) {
			continue
		}
		if slices.Equal(oldDecl[name], o) && slices.Equal(newDecl[name], n) {
			continue
		}
		changes = append(changes, fmt.Sprintf("%s %s -> %s locked", name, strings.Join(o, ", "), strings.Join(n, ", ")))
	}
	return changes
}

func label(d helm.Dependency) string {
	if d.Alias != "" && d.Alias != d.Name {
		return d.Alias + " (" + d.Name + ")"
	}
	return d.Name
}

func orNone(s string) string {
	return cmp.Or(s, "(none)")
}

type blob struct {
	oid  string
	path string
	mode os.FileMode
}

func (r Repo) Extract(ctx context.Context, rev, dir, dest string) (string, error) {
	out, err := r.git(ctx, "ls-tree", "-r", "-z", "--full-tree", rev, "--", dir)
	if err != nil {
		return "", err
	}
	var blobs []blob
	for _, entry := range splitNUL(out) {
		info, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(info)
		if !ok || len(fields) != 3 {
			return "", fmt.Errorf("git ls-tree %s: unexpected entry %q", rev, entry)
		}
		var mode os.FileMode
		switch fields[0] {
		case "100644":
			mode = 0o644
		case "100755":
			mode = 0o755
		default:
			continue
		}
		blobs = append(blobs, blob{oid: fields[2], path: name, mode: mode})
	}
	if len(blobs) == 0 {
		return "", fmt.Errorf("no files under %s at %s", dir, rev)
	}
	if err := r.writeBlobs(ctx, blobs, dest); err != nil {
		return "", err
	}
	return filepath.Join(dest, filepath.FromSlash(dir)), nil
}

func (r Repo) writeBlobs(ctx context.Context, blobs []blob, dest string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "cat-file", "--batch")
	cmd.WaitDelay = waitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		w := bufio.NewWriter(stdin)
		for _, b := range blobs {
			if _, err := fmt.Fprintln(w, b.oid); err != nil {
				break
			}
		}
		_ = w.Flush()
		_ = stdin.Close()
	}()

	readErr := readBlobs(bufio.NewReader(stdout), blobs, dest)
	if readErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return readErr
}

func readBlobs(br *bufio.Reader, blobs []blob, dest string) error {
	for _, b := range blobs {
		header, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("git cat-file %s: %w", b.oid, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != b.oid || fields[1] != "blob" {
			return fmt.Errorf("git cat-file %s: unexpected header %q", b.oid, strings.TrimSpace(header))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return fmt.Errorf("git cat-file %s: bad size %q", b.oid, fields[2])
		}
		target, err := safeJoin(dest, b.path)
		if err != nil {
			return err
		}
		body := &io.LimitedReader{R: br, N: size}
		if err := writeFile(target, body, b.mode); err != nil {
			return err
		}
		if lf, err := br.ReadByte(); body.N != 0 || err != nil || lf != '\n' {
			return fmt.Errorf("git cat-file %s: truncated object", b.oid)
		}
	}
	return nil
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
		return "", fmt.Errorf("path %q escapes %s", name, dest)
	}
	return target, nil
}
