package version

import (
	"regexp"
	"testing"
)

// semverRe is a pragmatic MAJOR.MINOR.PATCH check (the subset this project
// uses); it does not accept the full pre-release/build grammar.
var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func TestVersionIsSemVer(t *testing.T) {
	if !semverRe.MatchString(Version) {
		t.Fatalf("Version %q is not MAJOR.MINOR.PATCH", Version)
	}
}

func TestStringHasVPrefix(t *testing.T) {
	got := String()
	if got != "v"+Version {
		t.Fatalf("String() = %q, want %q", got, "v"+Version)
	}
	if len(got) == 0 || got[0] != 'v' {
		t.Fatalf("String() = %q, want a leading 'v'", got)
	}
}
