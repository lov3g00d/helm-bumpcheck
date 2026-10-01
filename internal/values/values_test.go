package values

import (
	"reflect"
	"testing"
)

func mustParse(t *testing.T, s string) map[string]any {
	t.Helper()
	m, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return m
}

func TestPath(t *testing.T) {
	tests := []struct {
		segments []string
		want     string
	}{
		{[]string{"a"}, "a"},
		{[]string{"a", "b", "c"}, "a.b.c"},
		{[]string{"podAnnotations", "prometheus.io/scrape"}, `podAnnotations["prometheus.io/scrape"]`},
		{[]string{"a", ""}, `a[""]`},
	}
	for _, tt := range tests {
		if got := Path(tt.segments); got != tt.want {
			t.Errorf("Path(%q) = %q, want %q", tt.segments, got, tt.want)
		}
	}
}

func TestFlatten(t *testing.T) {
	m := mustParse(t, `
image:
  repository: grafana/tempo
  tag: "2.6.0"
resources: {}
tolerations: []
`)
	got := map[string]any{}
	for p, l := range Flatten(m) {
		got[p] = l.Value
	}
	want := map[string]any{
		"image.repository": "grafana/tempo",
		"image.tag":        "2.6.0",
		"resources":        map[string]any{},
		"tolerations":      []any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Flatten = %#v, want %#v", got, want)
	}
}

func TestMergeLaterFileWins(t *testing.T) {
	dst := mustParse(t, "a: {b: 1, c: 2}")
	mergeInto(dst, mustParse(t, "a: {c: 3, d: 4}"))
	want := mustParse(t, "a: {b: 1, c: 3, d: 4}")
	if !reflect.DeepEqual(dst, want) {
		t.Errorf("merged = %#v, want %#v", dst, want)
	}
}

func TestCompare(t *testing.T) {
	oldDefaults := mustParse(t, `
replicas: 1
storage:
  trace:
    backend: local
processor:
  localBlocks:
    flushToStorage: false
podAnnotations: {}
structuredConfig: {}
`)
	newDefaults := mustParse(t, `
replicas: 2
storage:
  trace:
    backend: local
podAnnotations: {}
newFeature: true
`)
	user := mustParse(t, `
storage:
  trace:
    backend: s3
processor:
  localBlocks:
    flushToStorage: true
podAnnotations:
  prometheus.io/scrape: "true"
structuredConfig:
  overrides:
    max_traces: 1000
typoKey: 1
`)

	got := Compare(oldDefaults, newDefaults, user)

	want := Report{
		Removed:        []string{"processor.localBlocks.flushToStorage", "structuredConfig.overrides.max_traces"},
		Freeform:       []string{`podAnnotations["prometheus.io/scrape"]`},
		Undeclared:     []string{"typoKey"},
		DefaultChanged: []Change{{Path: "replicas", Old: 1, New: 2}},
		Added:          []string{"newFeature"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Compare =\n%#v\nwant\n%#v", got, want)
	}
}

func TestCompareFreeformOnlyInNewChart(t *testing.T) {
	oldDefaults := mustParse(t, "metricsGenerator:\n  enabled: false\n")
	newDefaults := mustParse(t, "metricsGenerator:\n  enabled: false\n  processor: {}\n")
	user := mustParse(t, "metricsGenerator:\n  processor:\n    local_blocks:\n      flush_to_storage: true\n")

	got := Compare(oldDefaults, newDefaults, user)
	if want := []string{"metricsGenerator.processor.local_blocks.flush_to_storage"}; !reflect.DeepEqual(got.Freeform, want) {
		t.Errorf("Freeform = %v, want %v", got.Freeform, want)
	}
	if len(got.Undeclared) != 0 {
		t.Errorf("Undeclared = %v, want none", got.Undeclared)
	}
}

func TestCompareTypeChanges(t *testing.T) {
	oldDefaults := mustParse(t, `
configuration:
  provider:
  backupStorageLocation:
    name:
    bucket:
    config: {}
  volumeSnapshotLocation:
    name:
    config: {}
  logLevel: info
tolerations: []
port: "8080"
`)
	newDefaults := mustParse(t, `
configuration:
  backupStorageLocation:
    - name:
      bucket:
      config: {}
  volumeSnapshotLocation:
    - name:
      config: {}
  logLevel: info
tolerations: {}
port: 8080
`)
	user := mustParse(t, `
configuration:
  provider: gcp
  backupStorageLocation:
    name: default
    bucket: backups
  volumeSnapshotLocation:
    provider: openebs
    name: default
    config:
      bucket: snaps
      prefix: cstor
  logLevel: debug
tolerations: []
port: "9090"
`)
	got := Compare(oldDefaults, newDefaults, user)

	want := []TypeChange{
		{Path: "configuration.backupStorageLocation", Old: "map", New: "list", Keys: []string{
			"configuration.backupStorageLocation.bucket", "configuration.backupStorageLocation.name"}},
		{Path: "configuration.volumeSnapshotLocation", Old: "map", New: "list", Keys: []string{
			"configuration.volumeSnapshotLocation.config.bucket", "configuration.volumeSnapshotLocation.config.prefix",
			"configuration.volumeSnapshotLocation.name", "configuration.volumeSnapshotLocation.provider"}},
		{Path: "tolerations", Old: "list", New: "map", Keys: []string{"tolerations"}},
	}
	if !reflect.DeepEqual(got.TypeChanged, want) {
		t.Errorf("TypeChanged =\n%#v\nwant\n%#v", got.TypeChanged, want)
	}
	if !reflect.DeepEqual(got.Removed, []string{"configuration.provider"}) {
		t.Errorf("Removed = %v, want only configuration.provider", got.Removed)
	}
	if len(got.Undeclared) != 0 || len(got.Freeform) != 0 {
		t.Errorf("Undeclared %v Freeform %v, want none", got.Undeclared, got.Freeform)
	}
}

func TestTypeChangeIgnoresNilAndMissing(t *testing.T) {
	oldDefaults := mustParse(t, "a:\n  b:\nc: 1\n")
	newDefaults := mustParse(t, "a:\n  b: [1]\n")
	for _, segs := range [][]string{{"a", "b"}, {"c"}, {"x", "y"}} {
		if _, _, _, ok := typeChange(oldDefaults, newDefaults, segs); ok {
			t.Errorf("typeChange(%v) reported a change", segs)
		}
	}
}
