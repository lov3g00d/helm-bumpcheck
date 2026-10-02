package report

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

func emptyLists(r values.Report) values.Report {
	v := reflect.ValueOf(&r).Elem()
	for _, f := range v.Fields() {
		if f.Kind() == reflect.Slice && f.IsNil() {
			f.Set(reflect.MakeSlice(f.Type(), 0, 0))
		}
	}
	return r
}

func veleroReport() Report {
	note := "[velero] Add support for multiple backupstoragelocations and volumesnapshotlocations by @bsteinm2 in https://github.com/vmware-tanzu/helm-charts/pull/413"
	return Report{
		Chart: "velero", From: "3.2.0", To: "4.0.1", AppFrom: "1.10.0", AppTo: "1.10.0",
		Report: emptyLists(values.Report{
			Removed: []string{"configuration.provider"},
			TypeChanged: []values.TypeChange{
				{Path: "configuration.backupStorageLocation", Old: "map", New: "list", Keys: []string{"configuration.backupStorageLocation.bucket", "configuration.backupStorageLocation.name"}},
			},
			Freeform: []string{"schedules.daily.schedule"},
			Added:    []string{"a", "b"},
		}),
		Render: &Render{OldOK: true, NewOK: false, NewError: "Error: execution error at (velero/templates/NOTES.txt:78:4):\nERROR: Please make .configuration.backupStorageLocation from map to slice"},
		Notes: []*Notes{
			{Source: "chart", Repo: "vmware-tanzu/helm-charts", From: "3.2.0", To: "4.0.1", Releases: 2, Entries: 4, Threshold: 0.5,
				Stats: match.Stats{Questions: 8},
				Matches: []match.Match{
					{Key: "configuration.backupStorageLocation.bucket", Probability: 0.71, Release: "velero-4.0.0", Section: "What's Changed", Text: note},
					{Key: "configuration.backupStorageLocation.name", Probability: 0.70, Release: "velero-4.0.0", Section: "What's Changed", Text: note},
					{Key: "schedules.daily.schedule", Probability: 0.2, Release: "velero-4.0.0", Section: "What's Changed", Text: note},
				}},
			{Source: "app", Skipped: "the version did not change", Matches: []match.Match{}},
		},
	}
}

func TestBuildPutsDeterministicFindingsInBreaking(t *testing.T) {
	v := build(veleroReport())

	var kinds []string
	for _, r := range v.Breaking {
		kinds = append(kinds, r.Kind+" "+r.Path)
	}
	want := "type changed configuration.backupStorageLocation|removed configuration.provider|render "
	if got := strings.Join(kinds, "|"); got != want {
		t.Errorf("breaking = %q, want %q", got, want)
	}
	if len(v.Check) != 0 || len(v.Unverified) != 1 {
		t.Errorf("check %v unverified %v", v.Check, v.Unverified)
	}
	if v.Verdict != "Result: 3 breaking, 0 to check, 1 unverified" {
		t.Errorf("verdict = %q", v.Verdict)
	}
}

func TestEvidenceIsDedupedAcrossKeys(t *testing.T) {
	tc := build(veleroReport()).Breaking[0]
	if len(tc.Evidence) != 1 || tc.More != 0 {
		t.Fatalf("evidence = %+v more %d, want one deduplicated note", tc.Evidence, tc.More)
	}
	if e := tc.Evidence[0]; e.Probability != 0.71 || e.Text != "[velero] Add support for multiple backupstoragelocations and volumesnapshotlocations" {
		t.Errorf("evidence = %+v", e)
	}
}

func TestBuildMovesMatchedUnverifiedKeysToCheck(t *testing.T) {
	r := veleroReport()
	r.Notes[0].Matches[2].Probability = 0.8
	v := build(r)
	if len(v.Unverified) != 0 || len(v.Check) != 1 || v.Check[0].Kind != "notes" {
		t.Errorf("check %+v unverified %+v", v.Check, v.Unverified)
	}
}

func TestWriteTextLayout(t *testing.T) {
	var b bytes.Buffer
	if err := WriteText(&b, veleroReport()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"velero 3.2.0 -> 4.0.1 (app 1.10.0)\nResult: 3 breaking, 0 to check, 1 unverified\n",
		"\nBREAKING\n  type changed  configuration.backupStorageLocation  map -> list, 2 keys you set\n",
		"                note 0.71  velero-4.0.0 What's Changed: [velero] Add support for multiple backupstoragelocations and volumesnapshotlocations\n",
		"  removed       configuration.provider               no longer in the chart\n",
		"  render        the new chart rejects your values\n                Error: execution error at (velero/templates/NOTES.txt:78:4):\n",
		"\nUNVERIFIED  the chart does not declare these keys and no release note matched them\n  free-form     schedules.daily.schedule\n",
		"Release notes (chart): vmware-tanzu/helm-charts 3.2.0 -> 4.0.1, 2 releases, 4 entries, 8 questions\n",
		"Release notes (app): skipped, the version did not change\n",
		"2 keys added in 4.0.1. Use -o json to list them.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("colour codes written to a non-terminal writer")
	}
}

func TestNothingAffectedVerdict(t *testing.T) {
	v := build(Report{Chart: "c", From: "1", To: "2"})
	if v.Verdict != "Result: nothing in this upgrade affects the values you set" {
		t.Errorf("verdict = %q", v.Verdict)
	}
}

func TestStyle(t *testing.T) {
	if got := (style{on: true}).red("x"); got != "\x1b[31mx\x1b[0m" {
		t.Errorf("red = %q", got)
	}
	if got := (style{}).red("x"); got != "x" {
		t.Errorf("plain red = %q", got)
	}
}

func TestFooterSummarizesStandardKeys(t *testing.T) {
	r := Report{Chart: "c", From: "1", To: "2", Report: values.Report{
		Standard: []string{"resources.limits.cpu", "resources.requests.cpu", `nodeSelector["a"]`},
	}}
	v := build(r)
	want := "3 keys in standard Kubernetes blocks (nodeSelector, resources) not listed. Use -o json to list them."
	if len(v.Footer) != 1 || v.Footer[0] != want {
		t.Errorf("footer = %q", v.Footer)
	}
	if v.Verdict != "Result: nothing in this upgrade affects the values you set" {
		t.Errorf("verdict = %q", v.Verdict)
	}
}

func TestFooterWarnsAboutSubcharts(t *testing.T) {
	v := build(Report{Chart: "c", From: "1", To: "2", Dependencies: []string{"minio", "agent"}})
	want := "The chart has 2 subcharts (minio, agent); keys under them are not compared against the subchart defaults."
	if len(v.Footer) != 1 || v.Footer[0] != want {
		t.Errorf("footer = %q", v.Footer)
	}
}

func TestBuildShowsDroppedSubcharts(t *testing.T) {
	v := build(Report{Chart: "c", From: "1", To: "2", Report: values.Report{
		Removed:             []string{"legacy", "redis.auth.password", "redisx.a"},
		RemovedDependencies: []string{"redis"},
	}})
	var details []string
	for _, r := range v.Breaking {
		details = append(details, r.Path+": "+r.Detail)
	}
	want := []string{"legacy: no longer in the chart", "redis.auth.password: subchart redis dropped from the chart", "redisx.a: no longer in the chart"}
	if !slices.Equal(details, want) {
		t.Errorf("breaking = %q, want %q", details, want)
	}
	if footer := "2 drops 1 subchart (redis); values under it no longer take effect."; !slices.Contains(v.Footer, footer) {
		t.Errorf("footer = %q, want %q", v.Footer, footer)
	}
}

func TestBuildShowsNowDeclaredKeysInCheck(t *testing.T) {
	v := build(Report{Chart: "c", From: "1", To: "2", Report: values.Report{NowDeclared: []string{"server.hostNetwork"}}})
	if len(v.Check) != 1 || v.Check[0].Kind != "declared" || v.Check[0].Path != "server.hostNetwork" {
		t.Fatalf("check = %+v", v.Check)
	}
	if v.Verdict != "Result: 0 breaking, 1 to check, 0 unverified" {
		t.Errorf("verdict = %q", v.Verdict)
	}
}

func TestFooterShowsModelAndFailures(t *testing.T) {
	r := veleroReport()
	r.Notes[0].Stats = match.Stats{Requests: 4, Failed: 1, Questions: 6, Model: "jev-1.13.0"}
	want := "Release notes (chart): vmware-tanzu/helm-charts 3.2.0 -> 4.0.1, 2 releases, 4 entries, 6 questions, jev-1.13.0, 1 of 4 Jev requests failed"
	if got := build(r).Footer[0]; got != want {
		t.Errorf("footer = %q\nwant     %q", got, want)
	}
}

func TestFooterShowsCachedAnswersAndTruncation(t *testing.T) {
	r := veleroReport()
	r.Notes[0].Stats = match.Stats{Cached: 6, Model: "jev-1.13.0"}
	r.Notes[0].Truncated = "releases may be incomplete: stopped after 10 pages at velero-\x1b[31m3.3.0, raise --max-pages to read further"
	footer := build(r).Footer
	want := []string{
		"Release notes (chart): vmware-tanzu/helm-charts 3.2.0 -> 4.0.1, 2 releases, 4 entries, 0 questions, 6 cached answers, jev-1.13.0",
		"Release notes (chart): releases may be incomplete: stopped after 10 pages at velero-3.3.0, raise --max-pages to read further",
	}
	if len(footer) < 2 || footer[0] != want[0] || footer[1] != want[1] {
		t.Errorf("footer = %q\nwant     %q", footer, want)
	}
}

func TestFooterPluralizesCounts(t *testing.T) {
	r := veleroReport()
	r.Notes[0].Releases, r.Notes[0].Entries, r.Notes[0].Stats = 1, 1, match.Stats{Questions: 1}
	want := "Release notes (chart): vmware-tanzu/helm-charts 3.2.0 -> 4.0.1, 1 release, 1 entry, 1 question"
	if got := build(r).Footer[0]; got != want {
		t.Errorf("footer = %q\nwant     %q", got, want)
	}
}

func TestBuildShowsMeaningChangedInCheck(t *testing.T) {
	note := "[prometheus-blackbox-exporter] Refactor image configuration by @jkroepke"
	r := Report{
		Chart: "prometheus-blackbox-exporter", From: "7.12.0", To: "8.0.0",
		Report: values.Report{
			MeaningChanged: []values.MeaningChange{
				{Path: "image.repository", Old: "prom/blackbox-exporter", New: "prometheus/blackbox-exporter", Siblings: []string{"image.registry"}},
				{Path: "probe.path", Siblings: []string{"probe.a", "probe.b", "probe.c", "probe.d"}},
			},
			DefaultChanged: []values.Change{{Path: "replicas", Old: 1, New: 2}},
		},
		Notes: []*Notes{{Source: "chart", Repo: "prometheus-community/helm-charts", Threshold: 0.5, Matches: []match.Match{
			{Key: "image.repository", Probability: 0.77, Release: "prometheus-blackbox-exporter-8.0.0", Text: note},
		}}},
	}
	v := build(r)
	if len(v.Breaking) != 0 || len(v.Check) != 3 || v.Verdict != "Result: 0 breaking, 3 to check, 0 unverified" {
		t.Fatalf("breaking %+v check %+v verdict %q", v.Breaking, v.Check, v.Verdict)
	}
	want := []row{
		{Kind: "meaning", Path: "image.repository", Detail: `you set it, default changed "prom/blackbox-exporter" -> "prometheus/blackbox-exporter", new beside it: image.registry`},
		{Kind: "meaning", Path: "probe.path", Detail: "you set it, new beside it: probe.a, probe.b, probe.c and 1 more"},
		{Kind: "default", Path: "replicas", Detail: "1 -> 2"},
	}
	for i, w := range want {
		got := v.Check[i]
		if got.Kind != w.Kind || got.Path != w.Path || got.Detail != w.Detail {
			t.Errorf("check[%d] = %s %s %q, want %s %s %q", i, got.Kind, got.Path, got.Detail, w.Kind, w.Path, w.Detail)
		}
	}
	if ev := v.Check[0].Evidence; len(ev) != 1 || ev[0].Probability != 0.77 {
		t.Errorf("evidence = %+v, want the release note attached", ev)
	}
}
