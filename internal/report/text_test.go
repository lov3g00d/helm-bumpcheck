package report

import (
	"strings"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

func valuesChange(path string, old, new any) values.Change {
	return values.Change{Path: path, Old: old, New: new}
}

func TestRenderTruncatesByRune(t *testing.T) {
	long := strings.Repeat("ж", 100)
	got := render(long)
	if n := len([]rune(got)); n != maxValueLen {
		t.Errorf("rendered %d runes, want %d", n, maxValueLen)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("rendered %q, want ... suffix", got)
	}
}

func TestDescribeMultilineChange(t *testing.T) {
	got := describeChange(valuesChange("config", "a: 1\nb: 2\nc: 3", "a: 1\nb: 5\nc: 3"))
	if want := `line 2: "b: 2" -> "b: 5"`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = describeChange(valuesChange("config", "a: 1\n", "a: 1\nextra: true\n"))
	if want := `line 2: "" -> "extra: true"`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
