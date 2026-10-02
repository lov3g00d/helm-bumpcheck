package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const (
	maxRenderLines = 12
	waitDelay      = 5 * time.Second
)

type Runner struct {
	Bin         string
	KubeVersion string
}

type KubeVersionError struct {
	Message string
}

func (e *KubeVersionError) Error() string {
	return e.Message
}

type Chart struct {
	Ref     string
	Version string
	Repo    string
}

type Metadata struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	AppVersion   string       `json:"appVersion"`
	Dependencies []Dependency `json:"dependencies"`
	Home         string       `json:"home"`
	Sources      []string     `json:"sources"`
}

type Dependency struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Repository string `json:"repository"`
	Alias      string `json:"alias"`
}

func (m Metadata) DependencyNames() []string {
	var names []string
	for _, d := range m.Dependencies {
		if d.Alias != "" {
			names = append(names, d.Alias)
		} else {
			names = append(names, d.Name)
		}
	}
	return names
}

func Detect() (Runner, error) {
	if bin := os.Getenv("HELM_BIN"); bin != "" {
		return Runner{Bin: bin}, nil
	}
	bin, err := exec.LookPath("helm")
	if err != nil {
		return Runner{}, fmt.Errorf("helm not found, set HELM_BIN or add helm to PATH: %w", err)
	}
	return Runner{Bin: bin}, nil
}

func (r Runner) Pull(ctx context.Context, c Chart, dest string) (string, error) {
	if c.Version == "" && c.Repo == "" {
		if info, err := os.Stat(c.Ref); err == nil && info.IsDir() {
			return c.Ref, nil
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	if _, err := r.run(ctx, c, c.args("pull", c.Ref, "--untar", "--untardir", dest)...); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(dest, e.Name()), nil
		}
	}
	return "", fmt.Errorf("helm pull %s %s left no chart directory in %s", c.Ref, c.Version, dest)
}

type Loaded struct {
	Dir      string
	Defaults map[string]any
	Meta     Metadata
}

func (r Runner) Load(ctx context.Context, c Chart, dest string) (Loaded, error) {
	dir, err := r.Pull(ctx, c, dest)
	if err != nil {
		return Loaded{}, err
	}
	data, meta, err := LoadDir(dir)
	if err != nil {
		return Loaded{}, err
	}
	defaults, err := values.Parse(data)
	if err != nil {
		return Loaded{}, fmt.Errorf("parse values of %s %s: %w", c.Ref, c.Version, err)
	}
	return Loaded{Dir: dir, Defaults: defaults, Meta: meta}, nil
}

func LoadDir(dir string) ([]byte, Metadata, error) {
	chartYAML, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if err != nil {
		return nil, Metadata{}, err
	}
	requirementsYAML, err := os.ReadFile(filepath.Join(dir, "requirements.yaml"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, Metadata{}, err
	}
	m, err := ParseMetadata(chartYAML, requirementsYAML)
	if err != nil {
		return nil, Metadata{}, fmt.Errorf("%s: %w", dir, err)
	}
	defaults, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, m, nil
	}
	return defaults, m, err
}

func ParseMetadata(chartYAML, requirementsYAML []byte) (Metadata, error) {
	var m Metadata
	if err := yaml.Unmarshal(chartYAML, &m); err != nil {
		return Metadata{}, fmt.Errorf("parse Chart.yaml: %w", err)
	}
	if requirementsYAML == nil {
		return m, nil
	}
	var v1 struct {
		Dependencies []Dependency `json:"dependencies"`
	}
	if err := yaml.Unmarshal(requirementsYAML, &v1); err != nil {
		return Metadata{}, fmt.Errorf("parse requirements.yaml: %w", err)
	}
	if v1.Dependencies != nil {
		m.Dependencies = v1.Dependencies
	}
	return m, nil
}

func ParseLock(data []byte) ([]Dependency, error) {
	var lock struct {
		Dependencies []Dependency `json:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parse lock file: %w", err)
	}
	return lock.Dependencies, nil
}

func MissingDependencies(dir string, meta Metadata) []string {
	entries, _ := os.ReadDir(filepath.Join(dir, "charts"))
	var missing []string
	for _, d := range meta.Dependencies {
		if !slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return vendors(dir, e, d.Name) }) {
			missing = append(missing, d.Name)
		}
	}
	return missing
}

func vendors(dir string, e fs.DirEntry, name string) bool {
	if e.IsDir() {
		data, err := os.ReadFile(filepath.Join(dir, "charts", e.Name(), "Chart.yaml"))
		if err != nil {
			return false
		}
		m, err := ParseMetadata(data, nil)
		return err == nil && m.Name == name
	}
	version, ok := strings.CutPrefix(strings.TrimSuffix(e.Name(), ".tgz"), name+"-")
	return ok && strings.HasSuffix(e.Name(), ".tgz") && version != "" && version[0] >= '0' && version[0] <= '9'
}

func (r Runner) BuildDependencies(ctx context.Context, dir string) error {
	_, err := r.run(ctx, Chart{}, "dependency", "build", dir)
	return err
}

func (r Runner) Template(ctx context.Context, c Chart, valueFiles []string) error {
	args := c.args("template", "bumpcheck", c.Ref)
	if r.KubeVersion != "" {
		args = append(args, "--kube-version", r.KubeVersion)
	}
	for _, f := range valueFiles {
		args = append(args, "-f", f)
	}
	cmd := r.command(ctx, args...)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := c.redact(cleanRenderError(stderr.String()))
		if _, req, ok := strings.Cut(msg, "chart requires kubeVersion:"); ok {
			req, _, _ = strings.Cut(req, "\n")
			return &KubeVersionError{Message: "chart requires kubeVersion:" + req}
		}
		if msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

func cleanRenderError(stderr string) string {
	var kept []string
	for line := range strings.SplitSeq(stderr, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "",
			strings.HasPrefix(t, "###") && strings.HasSuffix(t, "###"),
			strings.Contains(t, "warning:"),
			strings.HasPrefix(t, "Use --debug flag"):
			continue
		}
		if len(kept) == maxRenderLines {
			kept = append(kept, "...")
			break
		}
		kept = append(kept, t)
	}
	return strings.Join(kept, "\n")
}

func (c Chart) redact(s string) string {
	if c.Repo == "" {
		return s
	}
	u, err := url.Parse(c.Repo)
	if err != nil || u.User == nil {
		return s
	}
	return strings.ReplaceAll(s, c.Repo, u.Redacted())
}

func (c Chart) args(cmd ...string) []string {
	args := append([]string{}, cmd...)
	if c.Version != "" {
		args = append(args, "--version", c.Version)
	}
	if c.Repo != "" {
		args = append(args, "--repo", c.Repo)
	}
	return args
}

func (r Runner) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.WaitDelay = waitDelay
	return cmd
}

func (r Runner) run(ctx context.Context, c Chart, args ...string) ([]byte, error) {
	cmd := r.command(ctx, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		cmdline := c.redact("helm " + strings.Join(args, " "))
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %w: %s", cmdline, err, c.redact(msg))
		}
		return nil, fmt.Errorf("%s: %w", cmdline, err)
	}
	return out, nil
}
