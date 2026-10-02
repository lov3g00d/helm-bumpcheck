package values

import (
	"reflect"
	"slices"
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

	got := Compare(oldDefaults, newDefaults, user, Dependencies{})

	want := Report{
		Removed:        []string{"processor.localBlocks.flushToStorage", "structuredConfig.overrides.max_traces"},
		Standard:       []string{`podAnnotations["prometheus.io/scrape"]`},
		Undeclared:     []string{"typoKey"},
		DefaultChanged: []Change{{Path: "replicas", Old: 1.0, New: 2.0}},
		Added:          []string{"newFeature"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Compare =\n%#v\nwant\n%#v", got, want)
	}
}

func TestCompareIgnoresSpellingsHelmReadsAlike(t *testing.T) {
	got := Compare(mustParse(t, "cpu: 1\nflag: yes\n"), mustParse(t, "cpu: 1.0\nflag: true\n"), map[string]any{}, Dependencies{})
	if !reflect.DeepEqual(got, Report{}) {
		t.Errorf("Compare = %#v, want an empty report", got)
	}
}

func TestCompareFreeformOnlyInNewChart(t *testing.T) {
	oldDefaults := mustParse(t, "metricsGenerator:\n  enabled: false\n")
	newDefaults := mustParse(t, "metricsGenerator:\n  enabled: false\n  processor: {}\n")
	user := mustParse(t, "metricsGenerator:\n  processor:\n    local_blocks:\n      flush_to_storage: true\n")

	got := Compare(oldDefaults, newDefaults, user, Dependencies{})
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
	got := Compare(oldDefaults, newDefaults, user, Dependencies{})

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
	got := Compare(defaults, defaults, user, Dependencies{})

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
		defaultChanged, subcharts   []string
		dropped, removedDeps        []string
		nowDeclared                 []string
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
		{
			name:     "integer key under an open block",
			old:      "tcp: {}\n",
			new:      "tcp: {}\n",
			user:     "tcp:\n  9000: default/svc:8080\n",
			freeform: []string{"tcp.9000"},
		},
		{
			name:    "integer key renamed in the new chart",
			old:     "ports:\n  80: http\n",
			new:     "ports:\n  web: 80\n",
			user:    "ports:\n  80: custom\n",
			removed: []string{"ports.80"},
		},
		{
			name:           "integer and string keys side by side",
			old:            "ports:\n  80: http\n  metrics: 9090\n",
			new:            "ports:\n  80: web\n  metrics: 9090\n",
			user:           "ports:\n  metrics: 9100\n  8080: alt\n",
			undeclared:     []string{"ports.8080"},
			defaultChanged: []string{"ports.80"},
		},
		{
			name:       "subchart override dropped by the parent is undeclared",
			old:        "postgresql:\n  enabled: true\n  auth:\n    username: app\n",
			new:        "postgresql:\n  enabled: true\n",
			user:       "postgresql:\n  auth:\n    username: me\n",
			subcharts:  []string{"postgresql"},
			undeclared: []string{"postgresql.auth.username"},
		},
		{
			name:       "subchart block dropped by the parent is undeclared",
			old:        "db:\n  auth:\n    username: app\n",
			new:        "other: 1\n",
			user:       "db:\n  auth:\n    username: me\n",
			subcharts:  []string{"db"},
			undeclared: []string{"db.auth.username"},
		},
		{
			name:       "open subchart block dropped by the parent is undeclared",
			old:        "db: {}\n",
			new:        "other: 1\n",
			user:       "db:\n  extra: 1\n",
			subcharts:  []string{"db"},
			undeclared: []string{"db.extra"},
		},
		{
			name:     "global key under an open global block is free-form",
			old:      "global:\n  imageRegistry: docker.io\n",
			new:      "global: {}\n",
			user:     "global:\n  imageRegistry: mirror.local\n",
			freeform: []string{"global.imageRegistry"},
		},
		{
			name:       "global key dropped by the parent is undeclared",
			old:        "global:\n  imageRegistry: docker.io\n",
			new:        "replicas: 1\n",
			user:       "global:\n  imageRegistry: mirror.local\n",
			undeclared: []string{"global.imageRegistry"},
		},
		{
			name:      "key under a dropped dependency is removed",
			old:       "redis:\n  auth:\n    enabled: true\n",
			new:       "replicas: 1\n",
			user:      "redis:\n  auth:\n    enabled: false\n",
			subcharts: []string{"postgresql"},
			removed:   []string{"redis.auth.enabled"},
		},
		{
			name:        "keys under a subchart the new chart dropped are removed",
			old:         "redis:\n  enabled: true\n",
			new:         "replicas: 1\n",
			user:        "redis:\n  auth:\n    password: x\n  master:\n    persistence:\n      size: 8Gi\n",
			dropped:     []string{"redis"},
			removed:     []string{"redis.auth.password", "redis.master.persistence.size"},
			removedDeps: []string{"redis"},
		},
		{
			name:        "keys under an aliased subchart the new chart dropped are removed",
			old:         "replicas: 1\n",
			new:         "replicas: 1\n",
			user:        "cache:\n  size: 1\nother: 1\n",
			dropped:     []string{"cache"},
			removed:     []string{"cache.size"},
			undeclared:  []string{"other"},
			removedDeps: []string{"cache"},
		},
		{
			name:       "a subchart in both versions keeps its keys undeclared",
			old:        "redis:\n  enabled: true\n",
			new:        "replicas: 1\n",
			user:       "redis:\n  auth:\n    password: x\n",
			subcharts:  []string{"redis"},
			undeclared: []string{"redis.auth.password"},
		},
		{
			name:        "key the new chart starts declaring",
			old:         "server:\n  replicas: 1\n",
			new:         "server:\n  replicas: 1\n  hostNetwork: false\n",
			user:        "server:\n  hostNetwork: true\n",
			nowDeclared: []string{"server.hostNetwork"},
		},
		{
			name: "newly declared key set to its new default",
			old:  "server:\n  replicas: 1\n",
			new:  "server:\n  replicas: 1\n  hostNetwork: false\n",
			user: "server:\n  hostNetwork: false\n",
		},
		{
			name: "newly declared key under a block that was open",
			old:  "config: {}\n",
			new:  "config:\n  level: info\n",
			user: "config:\n  level: debug\n",
		},
		{
			name: "newly declared key under a standard block",
			old:  "server:\n  podAnnotations:\n    a: b\n",
			new:  "server:\n  podAnnotations:\n    a: b\n    c: d\n",
			user: "server:\n  podAnnotations:\n    c: e\n",
		},
		{
			name:      "newly declared key under an old subchart",
			old:       "replicas: 1\n",
			new:       "redis:\n  auth: true\n",
			user:      "redis:\n  auth: false\n",
			subcharts: []string{"redis"},
		},
		{
			name: "newly declared global key",
			old:  "replicas: 1\n",
			new:  "global:\n  imageRegistry: docker.io\n",
			user: "global:\n  imageRegistry: mirror.local\n",
		},
		{
			name:      "standard block under a subchart stays standard",
			old:       "postgresql:\n  resources: {}\n",
			new:       "postgresql:\n  resources: {}\n",
			user:      "postgresql:\n  resources:\n    limits:\n      cpu: 1\n",
			subcharts: []string{"postgresql"},
			standard:  []string{"postgresql.resources.limits.cpu"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compare(mustParse(t, tt.old), mustParse(t, tt.new), mustParse(t, tt.user), Dependencies{Old: slices.Concat(tt.subcharts, tt.dropped), New: tt.subcharts})
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
			check("RemovedDependencies", got.RemovedDependencies, tt.removedDeps)
			check("NowDeclared", got.NowDeclared, tt.nowDeclared)
		})
	}
}

func TestNoteKeys(t *testing.T) {
	r := Report{
		Removed:        []string{"legacy.enabled"},
		TypeChanged:    []TypeChange{{Path: "ports", Keys: []string{"ports.http", "ports.https"}}},
		MeaningChanged: []MeaningChange{{Path: "image.repository", Siblings: []string{"image.registry"}}},
		NowDeclared:    []string{"server.hostNetwork"},
		Freeform:       []string{"extraConfig.team"},
		Standard:       []string{"resources.limits.cpu"},
		Undeclared:     []string{"typo"},
		DefaultChanged: []Change{{Path: "port"}},
		Added:          []string{"newThing"},
	}
	want := []string{"legacy.enabled", "ports.http", "ports.https", "image.repository", "server.hostNetwork", "extraConfig.team", "typo"}
	if got := r.NoteKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("NoteKeys = %v, want %v", got, want)
	}
}

func TestCompareMeaningChanged(t *testing.T) {
	tests := []struct {
		name           string
		old, new, user string
		want           []MeaningChange
	}{
		{
			name: "scalar default changed under a value you set",
			old:  "image:\n  repository: prom/blackbox-exporter\n",
			new:  "image:\n  repository: prometheus/blackbox-exporter\n",
			user: "image:\n  repository: quay.io/prometheus/blackbox-exporter\n",
			want: []MeaningChange{{Path: "image.repository", Old: "prom/blackbox-exporter", New: "prometheus/blackbox-exporter"}},
		},
		{
			name: "default changed and a sibling with a default appeared",
			old:  "image:\n  repository: prom/blackbox-exporter\n",
			new:  "image:\n  registry: quay.io\n  repository: prometheus/blackbox-exporter\n  digest: \"\"\n",
			user: "image:\n  repository: quay.io/prometheus/blackbox-exporter\n",
			want: []MeaningChange{{Path: "image.repository", Old: "prom/blackbox-exporter", New: "prometheus/blackbox-exporter", Siblings: []string{"image.registry"}}},
		},
		{
			name: "only a sibling appeared",
			old:  "image:\n  repository: grafana/grafana\n",
			new:  "image:\n  repository: grafana/grafana\n  registry: docker.io\n  pullPolicy: IfNotPresent\n",
			user: "image:\n  repository: my/grafana\n",
			want: []MeaningChange{{Path: "image.repository", Siblings: []string{"image.pullPolicy", "image.registry"}}},
		},
		{
			name: "true and non-zero numbers are defaults",
			old:  "probe:\n  path: /health\n",
			new:  "probe:\n  path: /health\n  enabled: true\n  port: 8080\n",
			user: "probe:\n  path: /ready\n",
			want: []MeaningChange{{Path: "probe.path", Siblings: []string{"probe.enabled", "probe.port"}}},
		},
		{
			name: "siblings that are empty to Helm are not defaults",
			old:  "image:\n  repository: grafana/grafana\n",
			new:  "image:\n  repository: grafana/grafana\n  digest: \"\"\n  extra: {}\n  args: []\n  sha: ~\n  verify: false\n  retries: 0\n",
			user: "image:\n  repository: my/grafana\n",
		},
		{
			name: "a sibling you set is not news",
			old:  "image:\n  repository: grafana/grafana\n",
			new:  "image:\n  repository: grafana/grafana\n  registry: docker.io\n",
			user: "image:\n  repository: my/grafana\n  registry: quay.io\n",
		},
		{
			name: "new top-level keys are not siblings",
			old:  "replicas: 1\n",
			new:  "replicas: 1\nregistry: docker.io\n",
			user: "replicas: 3\n",
		},
		{
			name: "list defaults are replaced whole",
			old:  "hosts:\n  - a\n",
			new:  "hosts:\n  - b\n",
			user: "hosts:\n  - c\n",
		},
		{
			name: "default unchanged",
			old:  "image:\n  tag: \"\"\n",
			new:  "image:\n  tag: \"\"\n",
			user: "image:\n  tag: 1.2.3\n",
		},
		{
			name: "key declared only in the new chart",
			old:  "image:\n  repository: a\n",
			new:  "image:\n  repository: a\n  tag: v2\n",
			user: "image:\n  tag: v3\n",
		},
		{
			name: "type change wins",
			old:  "ports:\n  http: 80\n",
			new:  "ports:\n  http:\n    port: 80\n  grpc: 9000\n",
			user: "ports:\n  http: 8080\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compare(mustParse(t, tt.old), mustParse(t, tt.new), mustParse(t, tt.user), Dependencies{})
			if !reflect.DeepEqual(got.MeaningChanged, tt.want) {
				t.Errorf("MeaningChanged = %#v, want %#v", got.MeaningChanged, tt.want)
			}
		})
	}
}

func TestLinked(t *testing.T) {
	r := Report{
		Removed: []string{"configuration.provider", "other.key", `["grafana.ini"].legacy`, `a["x.y"]`},
		TypeChanged: []TypeChange{
			{Path: "configuration.backupStorageLocation", Keys: []string{"configuration.backupStorageLocation.bucket", "configuration.backupStorageLocation.name"}},
			{Path: "configuration.volumeSnapshotLocation", Keys: []string{"configuration.volumeSnapshotLocation.provider"}},
			{Path: `["grafana.ini"].auth`, Keys: []string{`["grafana.ini"].auth.enabled`}},
			{Path: "a.b", Keys: []string{"a.b.c"}},
			{Path: "grafana", Keys: []string{"grafana.x"}},
		},
	}
	want := map[string][]string{
		"configuration.provider": {"configuration.backupStorageLocation.bucket", "configuration.backupStorageLocation.name", "configuration.volumeSnapshotLocation.provider"},
		`["grafana.ini"].legacy`: {`["grafana.ini"].auth.enabled`},
		`a["x.y"]`:               {"a.b.c"},
	}
	if got := r.Linked(); !reflect.DeepEqual(got, want) {
		t.Errorf("Linked = %v, want %v", got, want)
	}
}

func TestRoot(t *testing.T) {
	for path, want := range map[string]string{
		"a":                     "a",
		"a.b.c":                 "a",
		`a["x.y"]`:              "a",
		`["grafana.ini"].auth`:  "grafana.ini",
		`["a\"b"]`:              `a"b`,
		`podAnnotations["k"].v`: "podAnnotations",
	} {
		if got := root(path); got != want {
			t.Errorf("root(%q) = %q, want %q", path, got, want)
		}
	}
}
