package report

import "testing"

func TestSanitizeUnicodeFormatCharacters(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"zero width space", "zero\u200bwidth", "zerowidth"},
		{"byte order mark", "\ufeffbom", "bom"},
		{"arabic letter mark", "a\u061cb", "ab"},
		{"soft hyphen", "soft\u00adhyphen", "softhyphen"},
		{"bidi override", "a\u202eb\u202cc", "abc"},
		{"bidi isolate", "a\u2066b\u2069c", "abc"},
		{"directional marks", "a\u200eb\u200fc", "abc"},
		{"zero width joiner kept", "\U0001F469\u200d\U0001F4BB", "\U0001F469\u200d\U0001F4BB"},
		{"zero width non-joiner kept", "\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645", "\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645"},
		{"line separator", "a\u2028b", "a b"},
		{"paragraph separator", "a\u2029b", "a b"},
		{"tab and newline kept", "a\tb\nc", "a\tb\nc"},
		{"c0 and c1 controls", "a\x00b\x7fc\u0085d", "abcd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitize(tt.in); got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
