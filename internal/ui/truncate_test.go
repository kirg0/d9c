package ui

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short ascii unchanged", "hello", 30, "hello"},
		{"exact length unchanged", "abcde", 5, "abcde"},
		{"ascii clipped", "abcdefghij", 5, "abcd…"},
		{"short cyrillic unchanged", "привет", 30, "привет"},
		// 10 Cyrillic runes (20 bytes): the old byte-slice [:5] would have split a
		// 2-byte rune; rune-based clipping keeps 4 whole runes plus the ellipsis.
		{"cyrillic clipped on rune boundary", "контейнерр", 5, "конт…"},
		{"mixed clipped", "abвгдè", 4, "abв…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.in, tt.max)
			if got != tt.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
			// The result must always be valid UTF-8 (no rune split in the middle).
			if !utf8.ValidString(got) {
				t.Errorf("truncateRunes(%q, %d) = %q is not valid UTF-8", tt.in, tt.max, got)
			}
			// And never exceed max runes.
			if n := utf8.RuneCountInString(got); n > tt.max {
				t.Errorf("truncateRunes(%q, %d) returned %d runes, exceeds max", tt.in, tt.max, n)
			}
		})
	}
}
