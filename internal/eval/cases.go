package eval

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"go.yaml.in/yaml/v3"
)

type Case struct {
	Name      string   `yaml:"name" json:"name"`
	AppRepo   string   `yaml:"appRepo" json:"appRepo"`
	TagPrefix string   `yaml:"tagPrefix" json:"tagPrefix,omitempty"`
	From      string   `yaml:"from" json:"from"`
	To        string   `yaml:"to" json:"to"`
	Keys      []string `yaml:"keys" json:"keys"`
	Affected  []string `yaml:"affected" json:"affected"`
	Source    string   `yaml:"source" json:"source,omitempty"`
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
	for i, c := range f.Cases {
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
	case c.AppRepo == "":
		return errors.New("appRepo is required")
	case c.From == "" || c.To == "":
		return errors.New("from and to are required")
	case len(c.Keys) == 0:
		return errors.New("keys must not be empty")
	}
	for _, a := range c.Affected {
		if !slices.Contains(c.Keys, a) {
			return fmt.Errorf("affected key %q is not listed in keys", a)
		}
	}
	return nil
}
