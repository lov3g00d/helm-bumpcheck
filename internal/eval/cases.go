package eval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const (
	SplitTune    = "tune"
	SplitHoldout = "holdout"
)

const (
	DecisionPromote        = "promote"
	DecisionEvidence       = "evidence"
	DecisionMeaningChanged = "meaning_changed"
)

var (
	splits    = []string{SplitTune, SplitHoldout}
	decisions = []string{DecisionPromote, DecisionEvidence, DecisionMeaningChanged}
)

type Chart struct {
	Repo string `yaml:"repo" json:"repo"`
	Name string `yaml:"name" json:"name"`
}

type Label struct {
	Key      string `yaml:"key" json:"key"`
	Decision string `yaml:"decision" json:"decision"`
}

type Pair struct {
	Release string   `yaml:"release" json:"release"`
	Entry   string   `yaml:"entry" json:"entry"`
	Keys    []string `yaml:"keys" json:"keys"`
	Why     string   `yaml:"why" json:"why,omitempty"`
}

type Case struct {
	Name       string  `yaml:"name" json:"name"`
	Split      string  `yaml:"split" json:"split"`
	Source     string  `yaml:"source" json:"source,omitempty"`
	Chart      Chart   `yaml:"chart" json:"chart"`
	AppRepo    string  `yaml:"appRepo" json:"appRepo"`
	TagPrefix  string  `yaml:"tagPrefix" json:"tagPrefix,omitempty"`
	From       string  `yaml:"from" json:"from"`
	To         string  `yaml:"to" json:"to"`
	ValuesFile string  `yaml:"valuesFile" json:"valuesFile"`
	Affected   []Label `yaml:"affected" json:"affected"`
	Evidence   []Pair  `yaml:"evidence" json:"evidence,omitempty"`
	Traps      []Pair  `yaml:"traps" json:"traps,omitempty"`
}

func (c Case) AffectedKeys(decision ...string) []string {
	var out []string
	for _, a := range c.Affected {
		if len(decision) == 0 || slices.Contains(decision, a.Decision) {
			out = append(out, a.Key)
		}
	}
	return out
}

type caseFile struct {
	Cases []Case `yaml:"cases"`
}

func LoadCases(path string) ([]Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var f caseFile
	dec := yaml.NewDecoder(file)
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Cases) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}

	seen := map[string]bool{}
	var errs []error
	for i := range f.Cases {
		c := &f.Cases[i]
		if c.ValuesFile != "" && !filepath.IsAbs(c.ValuesFile) {
			c.ValuesFile = filepath.Join(filepath.Dir(path), c.ValuesFile)
		}
		if err := c.validate(); err != nil {
			errs = append(errs, fmt.Errorf("case %d (%q): %w", i+1, c.Name, err))
		}
		if seen[c.Name] {
			errs = append(errs, fmt.Errorf("case %d: duplicate name %q", i+1, c.Name))
		}
		seen[c.Name] = true
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f.Cases, nil
}

func (c Case) validate() error {
	switch {
	case c.Name == "":
		return errors.New("name is required")
	case !slices.Contains(splits, c.Split):
		return fmt.Errorf("split %q must be one of %v", c.Split, splits)
	case c.Chart.Repo == "" || c.Chart.Name == "":
		return errors.New("chart.repo and chart.name are required")
	case c.AppRepo == "":
		return errors.New("appRepo is required")
	case c.From == "" || c.To == "":
		return errors.New("from and to are required")
	case c.ValuesFile == "":
		return errors.New("valuesFile is required")
	}

	user, err := values.LoadFiles([]string{c.ValuesFile})
	if err != nil {
		return err
	}
	keys := values.Flatten(user)
	if len(keys) == 0 {
		return fmt.Errorf("%s sets no keys", c.ValuesFile)
	}

	affected := map[string]bool{}
	for _, a := range c.Affected {
		switch {
		case !slices.Contains(decisions, a.Decision):
			return fmt.Errorf("affected key %q: decision %q must be one of %v", a.Key, a.Decision, decisions)
		case affected[a.Key]:
			return fmt.Errorf("affected key %q is listed twice", a.Key)
		}
		if _, ok := keys[a.Key]; !ok {
			return fmt.Errorf("affected key %q is not set in %s", a.Key, c.ValuesFile)
		}
		affected[a.Key] = true
	}
	for _, p := range c.Evidence {
		if err := p.validate(); err != nil {
			return fmt.Errorf("evidence: %w", err)
		}
		for _, k := range p.Keys {
			if !affected[k] {
				return fmt.Errorf("evidence key %q is not listed in affected", k)
			}
		}
	}
	for _, p := range c.Traps {
		if err := p.validate(); err != nil {
			return fmt.Errorf("trap: %w", err)
		}
		for _, k := range p.Keys {
			if _, ok := keys[k]; !ok {
				return fmt.Errorf("trap key %q is not set in %s", k, c.ValuesFile)
			}
			if pairIndex(c.Evidence, p.Release, p.Entry, k) >= 0 {
				return fmt.Errorf("trap key %q on %s %q is also labelled as evidence", k, p.Release, p.Entry)
			}
		}
	}
	return nil
}

func (p Pair) validate() error {
	if p.Release == "" || p.Entry == "" || len(p.Keys) == 0 {
		return errors.New("release, entry and keys are required")
	}
	return nil
}
