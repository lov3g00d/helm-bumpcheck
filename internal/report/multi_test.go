package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const hostileVersion = "2.0.0 | x @octo-org/admins <img src=https://evil.example/p.png> [l](https://evil.example)\n\n\x1b]8;;https://evil.example\x07click\x1b]8;;\x07"

func batchFixture() Batch {
	velero := veleroReport()
	velero.Path, velero.Values = "charts/velero", "values_prod.yaml"
	hostile := Report{Chart: "app", From: "1.0.0", To: hostileVersion, Path: "charts/app", Values: "values_a|b.yaml", Report: emptyLists(values.Report{})}
	return Batch{
		Base:    "8387ab7ac96b0f3c1d2e4a5b6c7d8e9f0a1b2c3d",
		Head:    "81b35ce08326aa4b5c6d7e8f9a0b1c2d3e4f5a6b",
		Bumped:  4,
		Reports: []Report{velero, hostile},
		Failed:  []Skip{{Path: "charts/broken", Reason: "helm template charts/broken: exit status 1: Error: \x1b[2Jbad @octo-org/admins <img src=x>\r\nsecond line"}},
		Skipped: []Skip{{Path: "charts/lib", Reason: "no values files match values_*.yaml, values-*.yaml"}},
	}
}

func TestBatchMarkdownNeutralizesUntrustedText(t *testing.T) {
	var b bytes.Buffer
	if err := WriteBatch(&b, "markdown", batchFixture()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"4 charts bumped between 8387ab7ac96b and 81b35ce08326",
		"| `velero 3.2.0 -> 4.0.1` | `values_prod.yaml` | 3 breaking, 0 to check, 1 unverified |\n",
		"| `app 1.0.0 -> 2.0.0 \\| x @octo-org/admins <img src=https://evil.example/p.png> [l](https://evil.example)  click` | `values_a\\|b.yaml` | nothing in this upgrade affects the values you set |\n",
		"### helm bumpcheck: `app 1.0.0 -> 2.0.0 | x @octo-org/admins <img src=https://evil.example/p.png> [l](https://evil.example)  click [values_a|b.yaml]`\n",
		"- `charts/broken`: helm template charts/broken: exit status 1: Error: bad @&#8203;octo-org/admins &lt;img src=x&gt; second line\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "\x1b\x07\r") {
		t.Errorf("markdown contains control characters:\n%q", out)
	}
}

func TestBatchTextNeutralizesUntrustedText(t *testing.T) {
	var b bytes.Buffer
	if err := WriteBatch(&b, "text", batchFixture()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"\napp 1.0.0 -> 2.0.0 | x @octo-org/admins <img src=https://evil.example/p.png> [l](https://evil.example)  click [values_a|b.yaml]\nResult: ",
		"  charts/broken: helm template charts/broken: exit status 1: Error: bad @octo-org/admins <img src=x>\n    second line\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "\x1b\x07\r") {
		t.Errorf("text contains control characters:\n%q", out)
	}
}

func TestBatchMarkdownCompactWhenTooLong(t *testing.T) {
	b := batchFixture()
	long := strings.Repeat("x", 8000)
	for i := range b.Reports[0].Notes[0].Matches {
		b.Reports[0].Notes[0].Matches[i].Text = long + string(rune('a'+i))
		b.Reports[0].Notes[0].Matches[i].Probability = 0.9
	}
	b.Reports = append(b.Reports, b.Reports[0], b.Reports[0], b.Reports[0])
	var out bytes.Buffer
	if err := WriteBatch(&out, "markdown", b); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if out.Len() > maxMarkdownLen {
		t.Errorf("markdown is %d bytes, want at most %d", out.Len(), maxMarkdownLen)
	}
	if strings.Contains(s, "<summary>Evidence</summary>") || strings.Contains(s, "Report truncated") {
		t.Errorf("want the compact rendering without evidence or truncation:\n%s", s)
	}
	if n := strings.Count(s, "| `velero 3.2.0 -> 4.0.1` |"); n != 4 {
		t.Errorf("summary table has %d velero rows, want 4", n)
	}
	if strings.Count(s, "<details>") != strings.Count(s, "</details>") || !strings.Contains(s, "<summary>Skipped</summary>") {
		t.Errorf("details blocks unbalanced or Skipped missing:\n%s", s)
	}
}

func TestBatchMarkdownTruncatesWhenCompactIsTooLong(t *testing.T) {
	b := batchFixture()
	for i := range 2000 {
		b.Skipped = append(b.Skipped, Skip{Path: "charts/lib" + strings.Repeat("x", 40), Reason: strings.Repeat("no values files match ", 2) + string(rune('a'+i%26))})
	}
	var out bytes.Buffer
	if err := WriteBatch(&out, "markdown", b); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if out.Len() > maxMarkdownLen+200 {
		t.Errorf("markdown is %d bytes, want at most about %d", out.Len(), maxMarkdownLen)
	}
	if !strings.HasSuffix(s, "\n\n</details>\n\n_Report truncated. Use -o json for the full result._\n") {
		t.Errorf("want the Skipped block closed before the truncation notice:\n%s", s[len(s)-300:])
	}
	if strings.Count(s, "<details>") != strings.Count(s, "</details>") {
		t.Error("details blocks unbalanced after truncation")
	}
}
