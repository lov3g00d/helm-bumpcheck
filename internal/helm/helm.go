package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"go.yaml.in/yaml/v3"
)

const maxRenderLines = 12

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

func (r Runner) ShowValues(ctx context.Context, c Chart) ([]byte, error) {
	return r.show(ctx, "values", c)
}

func (r Runner) ShowChart(ctx context.Context, c Chart) (Metadata, error) {
	data, err := r.show(ctx, "chart", c)
	if err != nil {
		return Metadata{}, err
	}
	var m Metadata
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Metadata{}, fmt.Errorf("parse Chart.yaml of %s %s: %w", c.Ref, c.Version, err)
	}
	return m, nil
}

func (r Runner) Template(ctx context.Context, c Chart, valueFiles []string) error {
	args := c.args("template", "bumpcheck", c.Ref)
	for _, f := range valueFiles {
		args = append(args, "-f", f)
	}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
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

func (r Runner) show(ctx context.Context, what string, c Chart) ([]byte, error) {
	args := c.args("show", what, c.Ref)

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		cmdline := c.redact("helm " + strings.Join(args, " "))
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %w: %s", cmdline, err, c.redact(msg))
		}
		return nil, fmt.Errorf("%s: %w", cmdline, err)
	}
	return out, nil
}
