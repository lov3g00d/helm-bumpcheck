package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata")

func TestGolden(t *testing.T) {
	writers := map[string]func(*bytes.Buffer) error{
		"velero.txt":  func(b *bytes.Buffer) error { return WriteText(b, veleroReport()) },
		"velero.md":   func(b *bytes.Buffer) error { return WriteMarkdown(b, veleroReport()) },
		"velero.json": func(b *bytes.Buffer) error { return WriteJSON(b, veleroReport()) },
		"batch.txt":   func(b *bytes.Buffer) error { return WriteBatch(b, "text", batchFixture()) },
		"batch.md":    func(b *bytes.Buffer) error { return WriteBatch(b, "markdown", batchFixture()) },
		"batch.json":  func(b *bytes.Buffer) error { return WriteBatch(b, "json", batchFixture()) },
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			if err := write(&b); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name)
			if *update {
				if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/report -update to create it)", err)
			}
			if !bytes.Equal(b.Bytes(), want) {
				t.Errorf("%s differs from golden file; rerun with -update if the change is intended\ngot:\n%s", name, b.String())
			}
		})
	}
}

func TestFailing(t *testing.T) {
	r := Report{Render: &Render{OldOK: true, NewOK: false}}
	r.Removed = []string{"a"}
	r.DefaultChanged = nil
	tests := []struct {
		kinds []string
		want  []string
	}{
		{nil, nil},
		{[]string{"removed"}, []string{"removed"}},
		{[]string{"type", "default"}, nil},
		{[]string{"render", "removed", "render"}, []string{"render", "removed", "render"}},
	}
	for _, tt := range tests {
		got := r.Failing(tt.kinds)
		if len(got) != len(tt.want) {
			t.Errorf("Failing(%v) = %v, want %v", tt.kinds, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("Failing(%v) = %v, want %v", tt.kinds, got, tt.want)
			}
		}
	}
	if (Report{}).Failing([]string{"render"}) != nil {
		t.Error("nil render must not fail")
	}
}

func TestRenderOutcomes(t *testing.T) {
	bad := errorString("Error: boom")
	tests := []struct {
		name           string
		oldErr, newErr error
		breaking       bool
		section        string
		footer         string
	}{
		{"both ok", nil, nil, false, "", "Render: both versions render with your values"},
		{"new fails", nil, bad, true, "breaking", ""},
		{"old fails", bad, nil, false, "", "Render: the new version renders with your values, the old one did not"},
		{"both fail", bad, bad, false, "check", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Report{Chart: "c", From: "1", To: "2", Render: NewRender(tt.oldErr, tt.newErr)}
			if r.Render.Breaking() != tt.breaking {
				t.Errorf("Breaking() = %v", r.Render.Breaking())
			}
			v := build(r)
			switch tt.section {
			case "breaking":
				if len(v.Breaking) != 1 || v.Breaking[0].Kind != "render" {
					t.Errorf("breaking = %+v", v.Breaking)
				}
			case "check":
				if len(v.Check) != 1 || v.Check[0].Kind != "render" {
					t.Errorf("check = %+v", v.Check)
				}
			}
			if tt.footer != "" && (len(v.Footer) == 0 || v.Footer[0] != tt.footer) {
				t.Errorf("footer = %q, want %q", v.Footer, tt.footer)
			}
		})
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
