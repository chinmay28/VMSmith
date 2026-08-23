package version

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

func TestString_UnstampedBuildIsPatchZero(t *testing.T) {
	// The zero-configuration build — `go test`, or a bare `go build` — must be
	// visibly a development build rather than claim to be patch release 1.
	if Patch != "0" {
		t.Fatalf("Patch = %q, want the unstamped default", Patch)
	}
	want := fmt.Sprintf("v%d.%d.0", Year, Month)
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if Stamped() {
		t.Error("Stamped() = true on an unstamped build")
	}
}

func TestString_UsesTheStampedPatch(t *testing.T) {
	prev := Patch
	t.Cleanup(func() { Patch = prev })

	Patch = "462"
	want := fmt.Sprintf("v%d.%d.462", Year, Month)
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if !Stamped() {
		t.Error("Stamped() = false with a patch number stamped in")
	}
}

// The version goes over the wire on GET /api/version, so its shape is part of
// the API: three integers behind a `v`, nothing else. Keeping the month
// unpadded is what keeps that valid semver for anything comparing releases.
func TestString_ShapeIsSemver(t *testing.T) {
	prev := Patch
	t.Cleanup(func() { Patch = prev })

	Patch = "462"
	if got := String(); !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(got) {
		t.Errorf("String() = %q, want vYEAR.MONTH.PATCH", got)
	}
}

func TestMonthIsACalendarMonth(t *testing.T) {
	if Month < 1 || Month > 12 {
		t.Errorf("Month = %d, want a calendar month (1-12)", Month)
	}
}

// scripts/version.sh reads Year and Month out of this package's source so the
// Makefile, the .deb build and the .rpm build can't disagree with the binary
// about them — which makes the *shape* of those two lines part of the contract.
// This is the test that notices when a reformat breaks the script's regex.
func TestYearMonthStayReadableToTheBuildScript(t *testing.T) {
	src, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"Year": Year, "Month": Month} {
		// The same pattern scripts/version.sh applies.
		re := regexp.MustCompile(`(?m)^[ \t]*` + name + `[ \t]*=[ \t]*(\d+)[ \t]*$`)
		m := re.FindSubmatch(src)
		if m == nil {
			t.Errorf("scripts/version.sh could not find %s in version.go", name)
			continue
		}
		if got, _ := strconv.Atoi(string(m[1])); got != want {
			t.Errorf("%s reads as %d, but the constant is %d", name, got, want)
		}
	}
}

func TestInfo_DefaultsAreSane(t *testing.T) {
	prevCommit, prevDate := Commit, BuildDate
	defer func() { Commit, BuildDate = prevCommit, prevDate }()

	Commit, BuildDate = "unknown", "unknown"

	info := Info()
	if info.Version != String() {
		t.Errorf("Version = %q, want %q", info.Version, String())
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
	prevPatch, prevCommit, prevDate := Patch, Commit, BuildDate
	defer func() { Patch, Commit, BuildDate = prevPatch, prevCommit, prevDate }()

	Patch, Commit, BuildDate = "462", "abc1234", "2026-01-02T03:04:05Z"

	want := fmt.Sprintf("v%d.%d.462", Year, Month)
	info := Info()
	if info.Version != want || info.Commit != "abc1234" || info.BuildDate != "2026-01-02T03:04:05Z" {
		t.Errorf("Info = %+v, want overrides applied", info)
	}
}
