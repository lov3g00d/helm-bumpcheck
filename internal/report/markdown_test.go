package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

func TestWriteMarkdown(t *testing.T) {
	var b bytes.Buffer
	if err := WriteMarkdown(&b, veleroReport()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"### helm bumpcheck: `velero 3.2.0 -> 4.0.1 (app 1.10.0)`\n\n**3 breaking, 0 to check, 1 unverified**\n",
		"#### Breaking\n\n| | Key | Change |\n|---|---|---|\n",
		"| type changed | `configuration.backupStorageLocation` | map -&gt; list, 2 keys you set |\n",
		"| removed | `configuration.provider` | no longer in the chart |\n",
		"| render |  | the new chart rejects your values |\n",
		"<details><summary>Evidence</summary>",
		"  - note 0.71, velero-4.0.0 What's Changed: [velero] Add support for multiple backupstoragelocations and volumesnapshotlocations\n",
		"  ```\n  Error: execution error at (velero/templates/NOTES.txt:78:4):\n",
		"<details><summary>Unverified (1): the chart does not declare these keys and no release note matched them</summary>",
		"- free-form `schedules.daily.schedule`\n",
		"<sub>Release notes (chart): vmware-tanzu/helm-charts 3.2.0 -&gt; 4.0.1, 2 releases, 4 entries, 8 questions</sub><br>\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestMarkdownEscapesTableCells(t *testing.T) {
	r := Report{Chart: "c", From: "1", To: "2", Report: values.Report{
		Removed:        []string{`annotations["a|b"]`},
		DefaultChanged: []values.Change{{Path: "x", Old: "a|b", New: "<c>"}},
	}}
	var b bytes.Buffer
	if err := WriteMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"`annotations[\"a\\|b\"]`", `"a\|b" -&gt; "&lt;c&gt;"`} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestMarkdownCompactWhenTooLong(t *testing.T) {
	r := veleroReport()
	long := strings.Repeat("x", 500)
	for i := range 200 {
		r.Notes[0].Matches = append(r.Notes[0].Matches, r.Notes[0].Matches[0])
		r.Notes[0].Matches[len(r.Notes[0].Matches)-1].Text = long + string(rune('a'+i%26))
		r.Notes[0].Matches[len(r.Notes[0].Matches)-1].Key = r.TypeChanged[0].Keys[0]
		r.Freeform = append(r.Freeform, strings.Repeat("k", 300)+string(rune('a'+i%26)))
	}
	var b bytes.Buffer
	if err := WriteMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	if b.Len() > maxMarkdownLen+200 {
		t.Errorf("markdown is %d bytes, want at most about %d", b.Len(), maxMarkdownLen)
	}
}
