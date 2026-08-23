// Package version exposes build-time identification for the running binary.
//
// The version scheme is calendar-based: vYEAR.MONTH.PATCH, where the patch
// number is the repository's commit count — every commit is a patch release, so
// `v2026.8.311` is the 311th commit on the 2026.8 line. The month is written as
// a plain number, not zero-padded: that keeps the string valid semver, which
// forbids a leading zero, and nothing here orders versions by sorting text.
//
// Year and Month are declared here in source and bumped by hand when a release
// line opens — deliberately not read from the build clock, which would move the
// version without a commit and make a rebuild of an old tree disagree with what
// it originally shipped. The patch number can only come from git, which a
// compiled binary has no access to, so it is stamped at link time, alongside the
// commit and the build date:
//
//	go build -ldflags "\
//	  -X github.com/vmsmith/vmsmith/pkg/version.Patch=$(git rev-list --count HEAD) \
//	  -X github.com/vmsmith/vmsmith/pkg/version.Commit=... \
//	  -X github.com/vmsmith/vmsmith/pkg/version.BuildDate=..."
//
// `make build` does this for you via scripts/version.sh, which is the one place
// the string is assembled — it reads the two constants below with an anchored
// regex, so keep them one-per-line in `Name = digits` form.
package version

import (
	"runtime"
	"strconv"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// Year and Month of the release line, bumped by hand. Month is a calendar
// month, 1–12.
//
// There is no semantic major/minor: the leading numbers say *when* a release
// line opened, not what it promises about compatibility. What changes for an
// operator on an upgrade is what the release notes are for.
const (
	Year  = 2026
	Month = 8
)

// Patch is the repository's commit count, stamped at link time (see the package
// comment). A bare `go build` leaves it at "0": patch 0 means an unstamped
// development build, never a release.
var Patch = "0"

var (
	// Commit is the git commit SHA the binary was built from.
	Commit = "unknown"
	// BuildDate is the build timestamp in RFC 3339 format.
	BuildDate = "unknown"
)

// String renders the full version, `v`-prefixed to match how the project tags
// releases (v2026.8.0). This is the one rendering — it's what `vmsmith
// --version` and `vmsmith version` print, and what GET /api/version reports.
func String() string {
	return "v" + strconv.Itoa(Year) + "." + strconv.Itoa(Month) + "." + Patch
}

// Stamped reports whether the patch number came from git. It is false for a
// bare `go build`, and for a build made where git could not be asked — from a
// tarball, or from a shallow clone (see scripts/version.sh).
func Stamped() bool { return Patch != "0" }

// Info returns the static build identification for this binary.
func Info() types.BuildInfo {
	return types.BuildInfo{
		Version:   String(),
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
