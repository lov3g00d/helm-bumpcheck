package report

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lov3g00d/helm-bumpcheck/internal/match"
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
		"  - note 0.71, velero-4.0.0 What's Changed: \\[velero\\] Add support for multiple backupstoragelocations and volumesnapshotlocations\n",
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

func TestMarkdownNeutralizesUntrustedText(t *testing.T) {
	r := veleroReport()
	r.Notes[0].Matches[0].Text = "Breaking: ping @grafana/team and see [docs](https://evil.example) <img src=x>"
	r.Render.NewError = "Error: bad\n```\n<script>alert(1)</script>"
	var b bytes.Buffer
	if err := WriteMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.HasPrefix(out, Marker+"\n") {
		t.Errorf("missing marker:\n%s", out)
	}
	for _, bad := range []string{"@grafana", "<img", "evil.example"} {
		if strings.Contains(out, bad) {
			t.Errorf("markdown contains %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "  ````\n") {
		t.Errorf("render error fence not lengthened past the backticks inside:\n%s", out)
	}
}

func TestTruncateMarkdownClosesBlocks(t *testing.T) {
	s := "<details><summary>x</summary>\n\n```\n" + strings.Repeat("é line\n", 100)
	got := truncateMarkdown(s, 200)
	if !utf8.ValidString(got) {
		t.Fatal("truncation produced invalid UTF-8")
	}
	if strings.Count(got, "```") != 2 || !strings.Contains(got, "</details>") || !strings.HasSuffix(got, "_Report truncated. Use -o json for the full result._\n") {
		t.Errorf("truncated output not closed:\n%s", got)
	}
}

func TestMarkdownLineBreaksDoNotEscapeInlineSpans(t *testing.T) {
	if got := mdCode("a\r\nb\nc\rd`e"); got != "`a b c d'e`" {
		t.Errorf("mdCode = %q", got)
	}
	if got := mdText("a\r\nb\rc"); got != "a b c" {
		t.Errorf("mdText = %q", got)
	}
	r := Report{Chart: "c", From: "1", To: "2\n\n@octocat please merge <img src=x>"}
	var b bytes.Buffer
	if err := WriteMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	if want := "### helm bumpcheck: `c 1 -> 2  @octocat please merge <img src=x>`\n\n**"; !strings.Contains(b.String(), want) {
		t.Errorf("markdown missing %q:\n%s", want, b.String())
	}
}

func TestMarkdownLinksNoteRefsWithoutBacklinks(t *testing.T) {
	r := veleroReport()
	r.Notes[0].Matches[0].Text = "Fix crash (#1234), see grafana/loki#9 and https://github.com/grafana/tempo/pull/3805"
	var b bytes.Buffer
	if err := WriteMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	want := "What's Changed: Fix crash ([vmware-tanzu/helm-charts#1234](https://redirect.github.com/vmware-tanzu/helm-charts/issues/1234)), " +
		"see [grafana/loki#9](https://redirect.github.com/grafana/loki/issues/9) and [grafana/tempo#3805](https://redirect.github.com/grafana/tempo/issues/3805)\n"
	if !strings.Contains(b.String(), want) {
		t.Errorf("markdown missing %q:\n%s", want, b.String())
	}

	var text bytes.Buffer
	if err := WriteText(&text, r); err != nil {
		t.Fatal(err)
	}
	if want := "What's Changed: Fix crash (vmware-tanzu/helm-charts#1234), see grafana/loki#9 and grafana/tempo#3805\n"; !strings.Contains(text.String(), want) {
		t.Errorf("text missing %q:\n%s", want, text.String())
	}
}

func TestMdIssueLinksLeavesEntitiesAndPathsAlone(t *testing.T) {
	for _, s := range []string{"ping &#64;user", "x/y&#64;z", "a/b/c#3", "see #12"} {
		if got := mdIssueLinks(s); got != s {
			t.Errorf("mdIssueLinks(%q) = %q", s, got)
		}
	}
}

func TestMarkdownNoteRefsUseTheirOwnRepo(t *testing.T) {
	r := veleroReport()
	r.Notes[1] = &Notes{Source: "app", Repo: "vmware-tanzu/velero", Threshold: 0.5, Matches: []match.Match{
		{Key: "configuration.provider", Probability: 0.9, Release: "v1.11.0", Text: "Drop the provider field (#42)"},
	}}
	var b bytes.Buffer
	if err := WriteMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	if want := "Drop the provider field ([vmware-tanzu/velero#42](https://redirect.github.com/vmware-tanzu/velero/issues/42))\n"; !strings.Contains(b.String(), want) {
		t.Errorf("markdown missing %q:\n%s", want, b.String())
	}
}

func TestMdTextBreaksMentions(t *testing.T) {
	got := mdText("(@mapno) ping @grafana/team, mail a@b.example")
	if want := "(@&#8203;mapno) ping @&#8203;grafana/team, mail a@b.example"; got != want {
		t.Errorf("mdText = %q, want %q", got, want)
	}
}

func TestMdTextEscapesBackslashes(t *testing.T) {
	got := mdText(`\[x\](//e.example) a\b`)
	if want := `\\\[x\\\](//e.example) a\\b`; got != want {
		t.Errorf("mdText = %q, want %q", got, want)
	}
	if got := mdText(cleanEntry(`Fix \[docs\](//evil.example)`, "a/b")); got != `Fix \\\[docs\\\](//evil.example)` {
		t.Errorf("mdText(cleanEntry) = %q", got)
	}
}
