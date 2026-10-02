package values

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
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

type MeaningChange struct {
	Path     string   `json:"path"`
	Old      any      `json:"old,omitempty"`
	New      any      `json:"new,omitempty"`
	Siblings []string `json:"siblings,omitempty"`
}

type Report struct {
	Removed        []string        `json:"removed"`
	TypeChanged    []TypeChange    `json:"typeChanged"`
	MeaningChanged []MeaningChange `json:"meaningChanged"`
	Freeform       []string        `json:"freeform"`
	Standard       []string        `json:"standard"`
	Undeclared     []string        `json:"undeclared"`
	DefaultChanged []Change        `json:"defaultChanged"`
	Added          []string        `json:"added"`
}

func (r Report) NoteKeys() []string {
	var typed, meaning []string
	for _, tc := range r.TypeChanged {
		typed = append(typed, tc.Keys...)
	}
	for _, mc := range r.MeaningChanged {
		meaning = append(meaning, mc.Path)
	}
	return slices.Concat(r.Removed, typed, meaning, r.Freeform, r.Undeclared)
}

func (r Report) Linked() map[string][]string {
	out := map[string][]string{}
	for _, k := range r.Removed {
		for _, tc := range r.TypeChanged {
			if root(tc.Path) == root(k) {
				out[k] = append(out[k], tc.Keys...)
			}
		}
	}
	return out
}

func root(path string) string {
	if strings.HasPrefix(path, "[") {
		if q, err := strconv.QuotedPrefix(path[1:]); err == nil {
			if s, err := strconv.Unquote(q); err == nil {
				return s
			}
		}
		return path
	}
	if i := strings.IndexAny(path, ".["); i > 0 {
		return path[:i]
	}
	return path
}

func Parse(data []byte) (map[string]any, error) {
	var raw map[any]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return stringMap(raw)
}

func stringMap[K comparable](raw map[K]any) (map[string]any, error) {
	m := make(map[string]any, len(raw))
	for k, v := range raw {
		key, err := keyString(k)
		if err != nil {
			return nil, err
		}
		if _, dup := m[key]; dup {
			return nil, fmt.Errorf("duplicate map key %q", key)
		}
		if m[key], err = normalize(v); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func normalize(v any) (any, error) {
	switch v := v.(type) {
	case map[any]any:
		return stringMap(v)
	case map[string]any:
		return stringMap(v)
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			var err error
			if out[i], err = normalize(x); err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return v, nil
	}
}

func keyString(k any) (string, error) {
	switch k := k.(type) {
	case string:
		return k, nil
	case int:
		return strconv.Itoa(k), nil
	case int64:
		return strconv.FormatInt(k, 10), nil
	case uint64:
		return strconv.FormatUint(k, 10), nil
	case float64:
		switch s := strconv.FormatFloat(k, 'g', -1, 32); s {
		case "+Inf":
			return ".inf", nil
		case "-Inf":
			return "-.inf", nil
		case "NaN":
			return ".nan", nil
		default:
			return s, nil
		}
	case bool:
		return strconv.FormatBool(k), nil
	case nil:
		return "", errors.New("unsupported null map key")
	default:
		return fmt.Sprint(k), nil
	}
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

func isOpen(v any) bool {
	if v == nil {
		return true
	}
	m, ok := v.(map[string]any)
	return ok && len(m) == 0
}

func isMapOrNil(v any) bool {
	if v == nil {
		return true
	}
	_, ok := v.(map[string]any)
	return ok
}

func openBlock(defaults map[string]any, segments []string) ([]string, bool) {
	for i := len(segments) - 1; i > 0; i-- {
		if v, ok := lookup(defaults, segments[:i]); ok {
			return segments[:i], isOpen(v)
		}
	}
	return nil, false
}

func standardAncestor(defaults map[string]any, segments []string) bool {
	for i := 1; i < len(segments); i++ {
		if !isStandardBlock(segments[i-1]) {
			continue
		}
		if v, ok := lookup(defaults, segments[:i]); ok && isMapOrNil(v) {
			return true
		}
	}
	return false
}

func (r *Report) remove(path string, subchart bool) {
	if subchart {
		r.Undeclared = append(r.Undeclared, path)
		return
	}
	r.Removed = append(r.Removed, path)
}

func (r *Report) classify(oldDefaults, newDefaults map[string]any, leaf Leaf, path string, inOld, subchart bool) {
	segs := leaf.Segments
	if isOpen(leaf.Value) {
		_, inNewTree := lookup(newDefaults, segs)
		_, inOldTree := lookup(oldDefaults, segs)
		if inNewTree || inOldTree {
			return
		}
	}
	if standardAncestor(newDefaults, segs) {
		r.Standard = append(r.Standard, path)
		return
	}
	if inOld {
		if _, open := openBlock(newDefaults, segs); open {
			r.Freeform = append(r.Freeform, path)
		} else {
			r.remove(path, subchart)
		}
		return
	}
	if block, open := openBlock(oldDefaults, segs); open {
		if v, ok := lookup(newDefaults, block); ok && isMapOrNil(v) {
			r.Freeform = append(r.Freeform, path)
		} else {
			r.remove(path, subchart)
		}
		return
	}
	if _, open := openBlock(newDefaults, segs); open {
		r.Freeform = append(r.Freeform, path)
		return
	}
	r.Undeclared = append(r.Undeclared, path)
}

func Compare(oldDefaults, newDefaults, user map[string]any, subcharts []string) Report {
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
		o, inOld := oldL[p]
		if n, inNew := newL[p]; inNew {
			if !inOld {
				continue
			}
			if mc, ok := meaningChange(o.Value, n.Value, oldDefaults, newDefaults, user, leaf.Segments); ok {
				mc.Path = p
				r.MeaningChanged = append(r.MeaningChanged, mc)
			}
			continue
		}
		subchart := leaf.Segments[0] == "global" || slices.Contains(subcharts, leaf.Segments[0])
		r.classify(oldDefaults, newDefaults, leaf, p, inOld, subchart)
	}

	for p, o := range oldL {
		n, ok := newL[p]
		if !ok {
			continue
		}
		if overridden(user, o.Segments) {
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
		slices.Sort(tc.Keys)
		r.TypeChanged = append(r.TypeChanged, *tc)
	}
	slices.SortFunc(r.TypeChanged, func(a, b TypeChange) int { return cmp.Compare(a.Path, b.Path) })
	slices.SortFunc(r.MeaningChanged, func(a, b MeaningChange) int { return cmp.Compare(a.Path, b.Path) })
	slices.SortFunc(r.DefaultChanged, func(a, b Change) int { return cmp.Compare(a.Path, b.Path) })
	for _, list := range [][]string{r.Removed, r.Freeform, r.Standard, r.Undeclared, r.Added} {
		slices.Sort(list)
	}
	return r
}

func meaningChange(oldValue, newValue any, oldDefaults, newDefaults, user map[string]any, segments []string) (MeaningChange, bool) {
	var mc MeaningChange
	if kind(oldValue) == "scalar" && kind(newValue) == "scalar" && !reflect.DeepEqual(oldValue, newValue) {
		mc.Old, mc.New = oldValue, newValue
	}
	if len(segments) > 1 {
		parent := segments[:len(segments)-1]
		o, _ := lookup(oldDefaults, parent)
		n, _ := lookup(newDefaults, parent)
		oldParent, _ := o.(map[string]any)
		newParent, _ := n.(map[string]any)
		for k, v := range newParent {
			sibling := append(slices.Clone(parent), k)
			if _, existed := oldParent[k]; existed || !hasValue(v) {
				continue
			}
			if _, set := lookup(user, sibling); set {
				continue
			}
			mc.Siblings = append(mc.Siblings, Path(sibling))
		}
		slices.Sort(mc.Siblings)
	}
	return mc, mc.Old != nil || len(mc.Siblings) > 0
}

func hasValue(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	default:
		return !reflect.ValueOf(v).IsZero()
	}
}

func overridden(user map[string]any, segments []string) bool {
	for i := 1; i <= len(segments); i++ {
		v, ok := lookup(user, segments[:i])
		if !ok {
			return false
		}
		if _, isMap := v.(map[string]any); !isMap || i == len(segments) {
			return true
		}
	}
	return false
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
	slices.Sort(names)
	return names
}
