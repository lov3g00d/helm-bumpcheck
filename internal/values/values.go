package values

import (
	"fmt"
	"os"
	"reflect"
	"slices"
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

type TypeChange struct {
	Path string   `json:"path"`
	Old  string   `json:"old"`
	New  string   `json:"new"`
	Keys []string `json:"keys"`
}

type Report struct {
	Removed        []string     `json:"removed"`
	TypeChanged    []TypeChange `json:"typeChanged"`
	Freeform       []string     `json:"freeform"`
	Standard       []string     `json:"standard"`
	Undeclared     []string     `json:"undeclared"`
	DefaultChanged []Change     `json:"defaultChanged"`
	Added          []string     `json:"added"`
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

var standardBlocks = map[string]bool{
	"resources":                 true,
	"nodeselector":              true,
	"tolerations":               true,
	"affinity":                  true,
	"securitycontext":           true,
	"podsecuritycontext":        true,
	"containersecuritycontext":  true,
	"topologyspreadconstraints": true,
}

func isStandardBlock(name string) bool {
	n := strings.ToLower(name)
	return standardBlocks[n] || strings.HasSuffix(n, "annotations") || strings.HasSuffix(n, "labels")
}

func freeformBlock(l Leaves, segments []string) ([]string, bool) {
	for i := len(segments) - 1; i > 0; i-- {
		anc, ok := l[Path(segments[:i])]
		if !ok {
			continue
		}
		m, isMap := anc.Value.(map[string]any)
		return segments[:i], isMap && len(m) == 0
	}
	return nil, false
}

func freeformAncestor(l Leaves, segments []string) bool {
	_, ok := freeformBlock(l, segments)
	return ok
}

func (r *Report) addFreeform(newL Leaves, leaf Leaf, path string) {
	block, _ := freeformBlock(newL, leaf.Segments)
	if len(block) > 0 && isStandardBlock(block[len(block)-1]) {
		r.Standard = append(r.Standard, path)
		return
	}
	r.Freeform = append(r.Freeform, path)
}

func Compare(oldDefaults, newDefaults, user map[string]any) Report {
	oldL, newL, userL := Flatten(oldDefaults), Flatten(newDefaults), Flatten(user)
	var r Report
	typed := map[string]*TypeChange{}

	for p, leaf := range userL {
		if at, oldKind, newKind, ok := typeChange(oldDefaults, newDefaults, leaf.Segments); ok {
			path := Path(at)
			if typed[path] == nil {
				typed[path] = &TypeChange{Path: path, Old: oldKind, New: newKind}
			}
			typed[path].Keys = append(typed[path].Keys, p)
			continue
		}
		_, inOld := oldL[p]
		_, inNew := newL[p]
		switch {
		case inNew:
		case inOld:
			if freeformAncestor(newL, leaf.Segments) {
				r.addFreeform(newL, leaf, p)
			} else {
				r.Removed = append(r.Removed, p)
			}
		case freeformAncestor(oldL, leaf.Segments):
			if freeformAncestor(newL, leaf.Segments) {
				r.addFreeform(newL, leaf, p)
			} else {
				r.Removed = append(r.Removed, p)
			}
		case freeformAncestor(newL, leaf.Segments):
			r.addFreeform(newL, leaf, p)
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

	for _, tc := range typed {
		sort.Strings(tc.Keys)
		r.TypeChanged = append(r.TypeChanged, *tc)
	}
	sort.Slice(r.TypeChanged, func(i, j int) bool { return r.TypeChanged[i].Path < r.TypeChanged[j].Path })
	sort.Strings(r.Removed)
	sort.Strings(r.Freeform)
	sort.Strings(r.Standard)
	sort.Strings(r.Undeclared)
	sort.Strings(r.Added)
	sort.Slice(r.DefaultChanged, func(i, j int) bool { return r.DefaultChanged[i].Path < r.DefaultChanged[j].Path })
	return r
}

func kind(v any) string {
	switch v.(type) {
	case nil:
		return ""
	case map[string]any:
		return "map"
	case []any:
		return "list"
	default:
		return "scalar"
	}
}

func lookup(m map[string]any, segments []string) (any, bool) {
	var cur any = m
	for _, s := range segments {
		node, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = node[s]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func typeChange(oldDefaults, newDefaults map[string]any, segments []string) ([]string, string, string, bool) {
	for i := 1; i <= len(segments); i++ {
		o, inOld := lookup(oldDefaults, segments[:i])
		n, inNew := lookup(newDefaults, segments[:i])
		if !inOld || !inNew {
			return nil, "", "", false
		}
		oldKind, newKind := kind(o), kind(n)
		if oldKind == "" || newKind == "" {
			return nil, "", "", false
		}
		if oldKind != newKind {
			return segments[:i], oldKind, newKind, true
		}
	}
	return nil, "", "", false
}

func StandardBlocks(paths []string) []string {
	var names []string
	for _, p := range paths {
		name := strings.FieldsFunc(p, func(r rune) bool { return r == '.' || r == '[' })
		for i := len(name) - 1; i >= 0; i-- {
			if isStandardBlock(name[i]) {
				if !slices.Contains(names, name[i]) {
					names = append(names, name[i])
				}
				break
			}
		}
	}
	sort.Strings(names)
	return names
}
