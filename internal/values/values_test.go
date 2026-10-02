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
		Standard:       []string{`podAnnotations["prometheus.io/scrape"]`},
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

func TestCompareStandardBlocks(t *testing.T) {
	defaults := mustParse(t, `
resources: {}
nodeSelector: {}
ingress:
  annotations: {}
sidecar:
  resources: {}
structuredConfig: {}
`)
	user := mustParse(t, `
resources:
  limits:
    cpu: 100m
nodeSelector:
  cloud.google.com/gke-spot: "true"
ingress:
  annotations:
    kubernetes.io/ingress.class: nginx
sidecar:
  limits:
    cpu: 100m
structuredConfig:
  overrides:
    max: 1
`)
	got := Compare(defaults, defaults, user)

	wantStandard := []string{`ingress.annotations["kubernetes.io/ingress.class"]`, `nodeSelector["cloud.google.com/gke-spot"]`, "resources.limits.cpu"}
	if !reflect.DeepEqual(got.Standard, wantStandard) {
		t.Errorf("Standard = %q, want %q", got.Standard, wantStandard)
	}
	if !reflect.DeepEqual(got.Freeform, []string{"structuredConfig.overrides.max"}) {
		t.Errorf("Freeform = %q", got.Freeform)
	}
	if !reflect.DeepEqual(got.Undeclared, []string{"sidecar.limits.cpu"}) {
		t.Errorf("Undeclared = %q, a misplaced key must stay visible", got.Undeclared)
	}
	if blocks := StandardBlocks(got.Standard); !reflect.DeepEqual(blocks, []string{"annotations", "nodeSelector", "resources"}) {
		t.Errorf("StandardBlocks = %q", blocks)
	}
}

func TestCompareClassificationEdgeCases(t *testing.T) {
	tests := []struct {
		name                        string
		old, new, user              string
		removed, freeform, standard []string
		undeclared, typeChanged     []string
		defaultChanged              []string
	}{
		{
			name:     "free-form block that gains defaults is not removed",
			old:      "config: {}\n",
			new:      "config:\n  a: 1\n",
			user:     "config:\n  b: 2\n",
			freeform: []string{"config.b"},
		},
		{
			name:     "empty security context that gains defaults stays standard",
			old:      "podSecurityContext: {}\n",
			new:      "podSecurityContext:\n  fsGroup: 1001\n",
			user:     "podSecurityContext:\n  runAsUser: 1000\n",
			standard: []string{"podSecurityContext.runAsUser"},
		},
		{
			name: "null deletes a declared key",
			old:  "metrics:\n  enabled: true\n",
			new:  "metrics:\n  enabled: true\n",
			user: "metrics: null\n",
		},
		{
			name: "empty map over a declared map is an override",
			old:  "resources:\n  limits:\n    cpu: 1\n",
			new:  "resources:\n  limits:\n    cpu: 1\n",
			user: "resources: {}\n",
		},
		{
			name:     "null default block is open",
			old:      "extra:\n",
			new:      "extra:\n",
			user:     "extra:\n  a: 1\n",
			freeform: []string{"extra.a"},
		},
		{
			name:     "open block that becomes null stays open",
			old:      "extra: {}\n",
			new:      "extra: ~\n",
			user:     "extra:\n  a: 1\n",
			freeform: []string{"extra.a"},
		},
		{
			name:     "partially declared standard block",
			old:      "resources:\n  requests:\n    cpu: 100m\n",
			new:      "resources:\n  requests:\n    cpu: 100m\n",
			user:     "resources:\n  limits:\n    memory: 1Gi\n",
			standard: []string{"resources.limits.memory"},
		},
		{
			name:    "open block removed from the chart",
			old:     "extra: {}\n",
			new:     "other: 1\n",
			user:    "extra:\n  a: 1\n",
			removed: []string{"extra.a"},
		},
		{
			name:       "misplaced key next to a standard block stays undeclared",
			old:        "sidecar:\n  resources: {}\n",
			new:        "sidecar:\n  resources: {}\n",
			user:       "sidecar:\n  limits:\n    cpu: 1\n",
			undeclared: []string{"sidecar.limits.cpu"},
		},
		{
			name: "null ancestor drops changed defaults under it",
			old:  "probe:\n  path: /a\n  port: 80\n",
			new:  "probe:\n  path: /b\n  port: 81\n",
			user: "probe: null\n",
		},
		{
			name:           "null deep ancestor",
			old:            "a:\n  b:\n    c: 1\nd: 1\n",
			new:            "a:\n  b:\n    c: 2\nd: 2\n",
			user:           "a:\n  b: null\n",
			defaultChanged: []string{"d"},
		},
		{
			name:       "user map over a scalar default hides its change",
			old:        "extra: 1\n",
			new:        "extra: 2\n",
			user:       "extra:\n  a: 1\n",
			undeclared: []string{"extra.a"},
		},
		{
			name:       "scalar over a declared block hides changes under it",
			old:        "probe:\n  path: /a\n",
			new:        "probe:\n  path: /b\n",
			user:       "probe: false\n",
			undeclared: []string{"probe"},
		},
		{
			name:           "null sibling does not hide a change",
			old:            "a:\n  b: 1\n  c: 1\n",
			new:            "a:\n  b: 2\n  c: 2\n",
			user:           "a:\n  b: null\n",
			defaultChanged: []string{"a.c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compare(mustParse(t, tt.old), mustParse(t, tt.new), mustParse(t, tt.user))
			check := func(field string, got, want []string) {
				if len(got) == 0 && len(want) == 0 {
					return
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %q, want %q", field, got, want)
				}
			}
			check("Removed", got.Removed, tt.removed)
			check("Freeform", got.Freeform, tt.freeform)
			check("Standard", got.Standard, tt.standard)
			check("Undeclared", got.Undeclared, tt.undeclared)
			var typed, changed []string
			for _, tc := range got.TypeChanged {
				typed = append(typed, tc.Path)
			}
			for _, c := range got.DefaultChanged {
				changed = append(changed, c.Path)
			}
			check("TypeChanged", typed, tt.typeChanged)
			check("DefaultChanged", changed, tt.defaultChanged)
		})
	}
}
