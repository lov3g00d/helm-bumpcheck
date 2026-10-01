package helm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"go.yaml.in/yaml/v3"
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
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
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

func (r Runner) show(ctx context.Context, what string, c Chart) ([]byte, error) {
	args := []string{"show", what, c.Ref}
	if c.Version != "" {
		args = append(args, "--version", c.Version)
	}
	if c.Repo != "" {
		args = append(args, "--repo", c.Repo)
	}

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		cmdline := "helm " + strings.Join(args, " ")
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %w: %s", cmdline, err, msg)
		}
		return nil, fmt.Errorf("%s: %w", cmdline, err)
	}
	return out, nil
}
