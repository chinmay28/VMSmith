package version

import (
	"runtime"
	"testing"
)

func TestInfo_DefaultsAreSane(t *testing.T) {
	prevVersion, prevCommit, prevDate := Version, Commit, BuildDate
	defer func() { Version, Commit, BuildDate = prevVersion, prevCommit, prevDate }()

	Version, Commit, BuildDate = "dev", "unknown", "unknown"

	info := Info()
	if info.Version != "dev" {
		t.Errorf("Version = %q, want dev", info.Version)
	}
	if info.Commit != "unknown" {
		t.Errorf("Commit = %q, want unknown", info.Commit)
	}
	if info.BuildDate != "unknown" {
		t.Errorf("BuildDate = %q, want unknown", info.BuildDate)
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", info.OS, runtime.GOOS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", info.Arch, runtime.GOARCH)
	}
}

func TestInfo_ReflectsLDFlagOverrides(t *testing.T) {
	prevVersion, prevCommit, prevDate := Version, Commit, BuildDate
	defer func() { Version, Commit, BuildDate = prevVersion, prevCommit, prevDate }()

	Version, Commit, BuildDate = "v9.9.9", "abc1234", "2026-01-02T03:04:05Z"

	info := Info()
	if info.Version != "v9.9.9" || info.Commit != "abc1234" || info.BuildDate != "2026-01-02T03:04:05Z" {
		t.Errorf("Info = %+v, want overrides applied", info)
	}
}
