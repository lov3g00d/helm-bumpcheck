package values

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Leaf struct {
	Segments []string
	Value    any
}

type Leaves map[string]Leaf

type Change struct {
	Path string `json:"path"`
	Old  any    `json:"old"`
	New  any    `json:"new"`
}

type Report struct {
	Removed        []string `json:"removed"`
	Freeform       []string `json:"freeform"`
	Undeclared     []string `json:"undeclared"`
	DefaultChanged []Change `json:"defaultChanged"`
	Added          []string `json:"added"`
}

func Parse(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func LoadFiles(paths []string) (map[string]any, error) {
	merged := map[string]any{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		m, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		mergeInto(merged, m)
	}
	return merged, nil
}

func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		sm, srcIsMap := v.(map[string]any)
		dm, dstIsMap := dst[k].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeInto(dm, sm)
			continue
		}
		dst[k] = v
	}
}

func Flatten(m map[string]any) Leaves {
	out := Leaves{}
	flatten(nil, m, out)
	return out
}

func flatten(segments []string, v any, out Leaves) {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		if len(segments) > 0 {
			out[Path(segments)] = Leaf{Segments: segments, Value: v}
		}
		return
	}
	for k, child := range m {
		next := make([]string, len(segments), len(segments)+1)
		copy(next, segments)
		flatten(append(next, k), child, out)
	}
}

func Path(segments []string) string {
	var b strings.Builder
	for i, s := range segments {
		if strings.ContainsAny(s, ".[]\" ") || s == "" {
			b.WriteString("[" + strconv.Quote(s) + "]")
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s)
	}
	return b.String()
}

func freeformAncestor(l Leaves, segments []string) bool {
	for i := len(segments) - 1; i > 0; i-- {
		anc, ok := l[Path(segments[:i])]
		if !ok {
			continue
		}
		m, isMap := anc.Value.(map[string]any)
		return isMap && len(m) == 0
	}
	return false
}

func Compare(oldDefaults, newDefaults, user map[string]any) Report {
	oldL, newL, userL := Flatten(oldDefaults), Flatten(newDefaults), Flatten(user)
	var r Report

	for p, leaf := range userL {
		_, inOld := oldL[p]
		_, inNew := newL[p]
		switch {
		case inNew:
		case inOld:
			if freeformAncestor(newL, leaf.Segments) {
				r.Freeform = append(r.Freeform, p)
			} else {
				r.Removed = append(r.Removed, p)
			}
		case freeformAncestor(oldL, leaf.Segments):
			if freeformAncestor(newL, leaf.Segments) {
				r.Freeform = append(r.Freeform, p)
			} else {
				r.Removed = append(r.Removed, p)
			}
		case freeformAncestor(newL, leaf.Segments):
			r.Freeform = append(r.Freeform, p)
		default:
			r.Undeclared = append(r.Undeclared, p)
		}
	}

	for p, o := range oldL {
		n, ok := newL[p]
		if !ok {
			continue
		}
		if _, set := userL[p]; set {
			continue
		}
		if !reflect.DeepEqual(o.Value, n.Value) {
			r.DefaultChanged = append(r.DefaultChanged, Change{Path: p, Old: o.Value, New: n.Value})
		}
	}

	for p := range newL {
		if _, ok := oldL[p]; !ok {
			r.Added = append(r.Added, p)
		}
	}

	sort.Strings(r.Removed)
	sort.Strings(r.Freeform)
	sort.Strings(r.Undeclared)
	sort.Strings(r.Added)
	sort.Slice(r.DefaultChanged, func(i, j int) bool { return r.DefaultChanged[i].Path < r.DefaultChanged[j].Path })
	return r
}
