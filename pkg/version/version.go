// Package version exposes build-time identification for the running binary.
//
// Values are populated by the linker via -ldflags
// -X github.com/vmsmith/vmsmith/pkg/version.Version=...
// -X github.com/vmsmith/vmsmith/pkg/version.Commit=...
// -X github.com/vmsmith/vmsmith/pkg/version.BuildDate=...
//
// Defaults are intentionally human-readable so an unconfigured build still
// returns sensible JSON instead of empty strings.
package version

import (
	"runtime"

	"github.com/vmsmith/vmsmith/pkg/types"
)

var (
	// Version is the semver / git-describe identifier (e.g. v1.2.3 or v1.2.3-5-gdeadbeef).
	Version = "dev"
	// Commit is the git commit SHA the binary was built from.
	Commit = "unknown"
	// BuildDate is the build timestamp in RFC 3339 format.
	BuildDate = "unknown"
)

// Info returns the static build identification for this binary.
func Info() types.BuildInfo {
	return types.BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
