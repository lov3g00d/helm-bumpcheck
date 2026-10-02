package report

import (
	"bytes"
	"os"
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

func TestRenderDoesNotEscapeHTML(t *testing.T) {
	if got := render("<b>&"); got != `"<b>&"` {
		t.Errorf("render = %s", got)
	}
}

func TestTextStripsTerminalEscapes(t *testing.T) {
	r := veleroReport()
	r.Notes[0].Matches[0].Text = "fix \x1b]8;;https://evil\x07click\x1b]8;;\x07 \x1b[31mred\x1b[0m \u202eevil\u202c done"
	r.Render.NewError = "Error: \x1b[2Jcleared"
	var b strings.Builder
	if err := WriteText(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, bad := range []string{"\x1b", "\u202e", "\x07"} {
		if strings.Contains(out, bad) {
			t.Errorf("text output contains %q", bad)
		}
	}
	if !strings.Contains(out, "fix click red evil done") || !strings.Contains(out, "Error: cleared") {
		t.Errorf("sanitized text lost content:\n%s", out)
	}
}

func TestColorEnabledHonoursNoColor(t *testing.T) {
	tty, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer tty.Close()
	if info, err := tty.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Skip("no character device to stand in for a terminal")
	}

	tests := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"1", false},
		{"false", false},
	}
	for _, tt := range tests {
		t.Setenv("NO_COLOR", tt.value)
		if got := colorEnabled(tty); got != tt.want {
			t.Errorf("NO_COLOR=%q: colour = %v, want %v", tt.value, got, tt.want)
		}
	}
	if colorEnabled(&bytes.Buffer{}) {
		t.Error("colour enabled for a non-terminal writer")
	}
}
