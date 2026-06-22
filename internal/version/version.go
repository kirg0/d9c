// Package version exposes the application version, following Semantic
// Versioning (https://semver.org): MAJOR.MINOR.PATCH.
//
// Convention for this project: a new feature bumps MINOR, a bug fix bumps
// PATCH, and an incompatible change bumps MAJOR.
package version

// Version is the current d9c release (SemVer, without a leading "v").
//
// It can be overridden at build time, e.g. to stamp a CI build:
//
//	go build -ldflags "-X d9c/internal/version.Version=1.2.3" -o d9c.exe .
var Version = "1.4.0"

// String returns the version prefixed with "v" (e.g. "v1.0.0"), the form
// shown in the UI and printed by the -version flag.
func String() string {
	return "v" + Version
}
