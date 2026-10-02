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
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	maxRenderLines = 12
	waitDelay      = 5 * time.Second
)

type Runner struct {
	Bin string
}

type Chart struct {
	Ref     string
	Version string
	Repo    string
}

type Metadata struct {
	Name         string       `yaml:"name"`
	Version      string       `yaml:"version"`
	AppVersion   string       `yaml:"appVersion"`
	Dependencies []Dependency `yaml:"dependencies"`
	Home         string       `yaml:"home"`
	Sources      []string     `yaml:"sources"`
}

type Dependency struct {
	Name  string `yaml:"name"`
	Alias string `yaml:"alias"`
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

func LoadDir(dir string) ([]byte, Metadata, error) {
	chartYAML, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if err != nil {
		return nil, Metadata{}, err
	}
	var m Metadata
	if err := yaml.Unmarshal(chartYAML, &m); err != nil {
		return nil, Metadata{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, "Chart.yaml"), err)
	}
	defaults, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, m, nil
	}
	return defaults, m, err
}

func (r Runner) Template(ctx context.Context, c Chart, valueFiles []string) error {
	args := c.args("template", "bumpcheck", c.Ref)
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
		if msg := cleanRenderError(stderr.String()); msg != "" {
			return errors.New(c.redact(msg))
		}
		return err
	}
	return nil
}

func cleanRenderError(stderr string) string {
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
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
