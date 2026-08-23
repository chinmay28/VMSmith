package cli

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
	"github.com/vmsmith/vmsmith/pkg/version"
)

func TestCLI_Version_HumanReadable(t *testing.T) {
	prevPatch, prevCommit, prevDate := version.Patch, version.Commit, version.BuildDate
	defer func() { version.Patch, version.Commit, version.BuildDate = prevPatch, prevCommit, prevDate }()
	version.Patch = "462"
	version.Commit = "deadbee"
	version.BuildDate = "2026-05-06T12:00:00Z"

	out, err := runCLI("version")
	if err != nil {
		t.Fatalf("vmsmith version failed: %v", err)
	}
	if !strings.Contains(out, "vmsmith "+version.String()) {
		t.Errorf("output missing version line: %q", out)
	}
	if !strings.Contains(out, "deadbee") {
		t.Errorf("output missing commit: %q", out)
	}
	if !strings.Contains(out, "2026-05-06T12:00:00Z") {
		t.Errorf("output missing build date: %q", out)
	}
}

func TestCLI_Version_JSON(t *testing.T) {
	prevPatch, prevCommit, prevDate := version.Patch, version.Commit, version.BuildDate
	defer func() { version.Patch, version.Commit, version.BuildDate = prevPatch, prevCommit, prevDate }()
	version.Patch = "999"
	version.Commit = "abc1234"
	version.BuildDate = "2026-05-06T00:00:00Z"

	out, err := runCLI("version", "--json")
	if err != nil {
		t.Fatalf("vmsmith version --json failed: %v", err)
	}
	var info types.BuildInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("output is not JSON: %v\noutput: %s", err, out)
	}
	if info.Version != version.String() || info.Commit != "abc1234" || info.BuildDate != "2026-05-06T00:00:00Z" {
		t.Errorf("BuildInfo = %+v, want overrides applied", info)
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
}
