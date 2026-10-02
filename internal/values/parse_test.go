package values

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseStringifiesKeys(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]any
	}{
		{
			name: "integer key in a nested map",
			in:   "tcp:\n  9000: default/svc:8080\n",
			want: map[string]any{"tcp": map[string]any{"9000": "default/svc:8080"}},
		},
		{
			name: "integer key at the top level",
			in:   "0x10: a\n",
			want: map[string]any{"16": "a"},
		},
		{
			name: "float and bool keys",
			in:   "m:\n  1.5: a\n  true: b\n  .inf: c\n",
			want: map[string]any{"m": map[string]any{"1.5": "a", "true": "b", ".inf": "c"}},
		},
		{
			name: "mixed integer and string keys",
			in:   "ports:\n  80: http\n  metrics: 9090\n",
			want: map[string]any{"ports": map[string]any{"80": "http", "metrics": 9090}},
		},
		{
			name: "map inside a list",
			in:   "rules:\n  - 443: https\n",
			want: map[string]any{"rules": []any{map[string]any{"443": "https"}}},
		},
		{
			name: "empty document",
			in:   "",
			want: map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"sequence at the top level", "- a\n"},
		{"null key", "m:\n  ~: a\n"},
		{"keys equal once stringified", "m:\n  1.0: a\n  1: b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Parse([]byte(tt.in)); err == nil {
				t.Errorf("Parse = %#v, want an error", got)
			}
		})
	}
}

func TestLoadFilesMergesIntegerKeyedMaps(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")
	if err := os.WriteFile(a, []byte("tcp:\n  9000: a/svc:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("tcp:\n  9001: b/svc:2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFiles([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"tcp": map[string]any{"9000": "a/svc:1", "9001": "b/svc:2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadFiles = %#v, want %#v", got, want)
	}
}

func TestCompareReportMarshalsIntegerKeyedDefaults(t *testing.T) {
	r := Compare(mustParse(t, "tcp:\n  9000: a\n"), mustParse(t, "tcp:\n  9000: b\n"), map[string]any{}, nil)
	if _, err := json.Marshal(r); err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := []Change{{Path: "tcp.9000", Old: "a", New: "b"}}
	if !reflect.DeepEqual(r.DefaultChanged, want) {
		t.Errorf("DefaultChanged = %#v, want %#v", r.DefaultChanged, want)
	}
}
