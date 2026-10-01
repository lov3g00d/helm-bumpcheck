package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

func veleroReport() Report {
	note := "[velero] Add support for multiple backupstoragelocations and volumesnapshotlocations by @bsteinm2 in https://github.com/vmware-tanzu/helm-charts/pull/413"
	return Report{
		Chart: "velero", From: "3.2.0", To: "4.0.1", AppFrom: "1.10.0", AppTo: "1.10.0",
		Report: values.Report{
			Removed: []string{"configuration.provider"},
			TypeChanged: []values.TypeChange{
				{Path: "configuration.backupStorageLocation", Old: "map", New: "list", Keys: []string{"configuration.backupStorageLocation.bucket", "configuration.backupStorageLocation.name"}},
			},
			Freeform: []string{"schedules.daily.schedule"},
			Added:    []string{"a", "b"},
		},
		Render: &Render{OldOK: true, NewOK: false, NewError: "Error: execution error at (velero/templates/NOTES.txt:78:4):\nERROR: Please make .configuration.backupStorageLocation from map to slice"},
		Notes: []*Notes{
			{Source: "chart", Repo: "vmware-tanzu/helm-charts", From: "3.2.0", To: "4.0.1", Releases: 2, Entries: 4, Threshold: 0.5,
				Stats: match.Stats{Questions: 8},
				Matches: []match.Match{
					{Key: "configuration.backupStorageLocation.bucket", Probability: 0.71, Release: "velero-4.0.0", Section: "What's Changed", Text: note},
					{Key: "configuration.backupStorageLocation.name", Probability: 0.70, Release: "velero-4.0.0", Section: "What's Changed", Text: note},
					{Key: "schedules.daily.schedule", Probability: 0.2, Release: "velero-4.0.0", Section: "What's Changed", Text: note},
				}},
			{Source: "app", Skipped: "the version did not change"},
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
