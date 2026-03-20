package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reset tears down the global logger between tests.
func reset() {
	globalMu.Lock()
	if global != nil {
		_ = global.out.Close()
		global = nil
	}
	globalMu.Unlock()
}

// ============================================================
// Init and Get
// ============================================================

func TestGet_BeforeInit_ReturnsFallback(t *testing.T) {
	reset()
	l := Get()
	if l == nil {
		t.Fatal("Get() returned nil before Init")
	}
	// Should be safe to call
	l.Info("test", "hello from fallback logger")
}

func TestInit_ToFile(t *testing.T) {
	reset()
	defer reset()

	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

	if err := Init(logFile, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}

	Info("test", "written to file")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(data), "written to file") {
		t.Errorf("log file content = %q, want to contain message", string(data))
	}
}

func TestInit_CreatesParentDir(t *testing.T) {
	reset()
	defer reset()

	dir := t.TempDir()
	logFile := filepath.Join(dir, "subdir", "vmsmith.log")

	if err := Init(logFile, LevelInfo); err != nil {
		t.Fatalf("Init should create parent dir: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(logFile)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}

func TestInit_EmptyPath_UsesStderr(t *testing.T) {
	reset()
	defer reset()

	// Should not error when no file path given.
	if err := Init("", LevelInfo); err != nil {
		t.Fatalf("Init with empty path: %v", err)
	}
	// Calling Info should not panic.
	Info("test", "stderr fallback")
}

func TestClose(t *testing.T) {
	reset()
	dir := t.TempDir()
	Init(filepath.Join(dir, "close.log"), LevelInfo)
	Close()

	globalMu.Lock()
	isNil := global == nil
	globalMu.Unlock()

	if !isNil {
		t.Error("global should be nil after Close()")
	}
}

// ============================================================
// Log levels
// ============================================================

func TestLogLevels_MinLevelFiltersLower(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelWarn)

	l := Get()
	l.Debug("test", "debug msg")
	l.Info("test", "info msg")
	l.Warn("test", "warn msg")
	l.Error("test", "error msg")

	entries := l.Entries("debug", time.Time{}, 0)
	for _, e := range entries {
		if e.Level == "debug" || e.Level == "info" {
			t.Errorf("expected debug/info filtered out at minLevel=warn, got %q", e.Level)
		}
	}
	found := false
	for _, e := range entries {
		if e.Level == "warn" || e.Level == "error" {
			found = true
		}
	}
	if !found {
		t.Error("expected warn/error entries in ring buffer")
	}
}

func TestGlobalHelpers(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	Debug("src", "d")
	Info("src", "i")
	Warn("src", "w")
	Error("src", "e")

	entries := Get().Entries("debug", time.Time{}, 0)
	if len(entries) < 4 {
		t.Errorf("expected at least 4 entries, got %d", len(entries))
	}
}

// ============================================================
// Entries — level filter
// ============================================================

func TestEntries_LevelFilter(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	l.Debug("s", "d1")
	l.Info("s", "i1")
	l.Warn("s", "w1")
	l.Error("s", "e1")

	warnAndAbove := l.Entries("warn", time.Time{}, 0)
	for _, e := range warnAndAbove {
		if e.Level == "debug" || e.Level == "info" {
			t.Errorf("Entries(warn) returned lower-level entry: %q", e.Level)
		}
	}

	all := l.Entries("debug", time.Time{}, 0)
	if len(all) < 4 {
		t.Errorf("Entries(debug) returned %d, want >= 4", len(all))
	}
}

// ============================================================
// Entries — since filter
// ============================================================

func TestEntries_SinceFilter(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	l.Info("s", "before")
	cutoff := time.Now()
	time.Sleep(2 * time.Millisecond) // ensure timestamps differ
	l.Info("s", "after")

	entries := l.Entries("debug", cutoff, 0)
	for _, e := range entries {
		if e.Message == "before" {
			t.Errorf("Entries(since) returned entry logged before cutoff")
		}
	}
	found := false
	for _, e := range entries {
		if e.Message == "after" {
			found = true
		}
	}
	if !found {
		t.Error("Entries(since) should include entry logged after cutoff")
	}
}

// ============================================================
// Entries — limit
// ============================================================

func TestEntries_Limit(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	for i := 0; i < 20; i++ {
		l.Info("s", "msg")
	}

	limited := l.Entries("debug", time.Time{}, 5)
	if len(limited) != 5 {
		t.Errorf("Entries(limit=5) returned %d entries, want 5", len(limited))
	}

	// limit=0 means no cap
	unlimited := l.Entries("debug", time.Time{}, 0)
	if len(unlimited) < 20 {
		t.Errorf("Entries(limit=0) returned %d entries, want >= 20", len(unlimited))
	}
}

// ============================================================
// Entries — source filter (applied at API layer, not ring buffer)
// ============================================================

func TestEntries_SourceVariety(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	l.Info("cli", "cli action")
	l.Info("api", "api request")
	l.Info("daemon", "daemon startup")

	all := l.Entries("debug", time.Time{}, 0)
	sources := map[string]bool{}
	for _, e := range all {
		sources[e.Source] = true
	}
	for _, want := range []string{"cli", "api", "daemon"} {
		if !sources[want] {
			t.Errorf("expected source %q in entries", want)
		}
	}
}

// ============================================================
// Fields
// ============================================================

func TestEntries_Fields(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	l.Info("cli", "vm created", "id", "vm-123", "name", "myvm")

	entries := l.Entries("debug", time.Time{}, 0)
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	e := entries[len(entries)-1]
	if e.Fields["id"] != "vm-123" {
		t.Errorf("fields[id] = %q, want vm-123", e.Fields["id"])
	}
	if e.Fields["name"] != "myvm" {
		t.Errorf("fields[name] = %q, want myvm", e.Fields["name"])
	}
}

func TestEntries_OddFields_Ignored(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	// Odd number of fields — last key has no value, should not panic
	l.Info("s", "msg", "key1", "val1", "orphan-key")

	entries := l.Entries("debug", time.Time{}, 0)
	if len(entries) == 0 {
		t.Fatal("expected entry")
	}
	e := entries[len(entries)-1]
	if e.Fields["key1"] != "val1" {
		t.Errorf("fields[key1] = %q, want val1", e.Fields["key1"])
	}
}

// ============================================================
// Ring buffer overflow
// ============================================================

func TestRingBuffer_Overflow(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	// Write more entries than ring capacity.
	for i := 0; i < ringSize+100; i++ {
		l.Info("s", "msg")
	}

	entries := l.Entries("debug", time.Time{}, 0)
	if len(entries) > ringSize {
		t.Errorf("ring buffer returned %d entries, want <= %d", len(entries), ringSize)
	}
}

// ============================================================
// Entries ordering — oldest first
// ============================================================

func TestEntries_OldestFirst(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	l := Get()
	l.Info("s", "first")
	time.Sleep(time.Millisecond)
	l.Info("s", "second")
	time.Sleep(time.Millisecond)
	l.Info("s", "third")

	entries := l.Entries("debug", time.Time{}, 0)
	n := len(entries)
	if n < 3 {
		t.Fatalf("expected >= 3 entries, got %d", n)
	}
	last3 := entries[n-3:]
	if last3[0].Message != "first" {
		t.Errorf("entries[0].Message = %q, want first", last3[0].Message)
	}
	if last3[2].Message != "third" {
		t.Errorf("entries[2].Message = %q, want third", last3[2].Message)
	}
}

// ============================================================
// File line format
// ============================================================

func TestFileLineFormat(t *testing.T) {
	reset()
	defer reset()

	dir := t.TempDir()
	logFile := filepath.Join(dir, "fmt.log")
	Init(logFile, LevelDebug)

	Info("myservice", "test message", "key", "value")

	data, _ := os.ReadFile(logFile)
	line := string(data)

	if !strings.Contains(line, "[INFO]") {
		t.Errorf("line does not contain [INFO]: %q", line)
	}
	if !strings.Contains(line, "[myservice]") {
		t.Errorf("line does not contain [myservice]: %q", line)
	}
	if !strings.Contains(line, "test message") {
		t.Errorf("line does not contain message: %q", line)
	}
	if !strings.Contains(line, "key=value") {
		t.Errorf("line does not contain field: %q", line)
	}
}

// ============================================================
// parseLevel helper
// ============================================================

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want Level
	}{
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"info", LevelInfo},
		{"INFO", LevelInfo},
		{"warn", LevelWarn},
		{"warning", LevelWarn},
		{"error", LevelError},
		{"ERROR", LevelError},
		{"", LevelInfo},      // default
		{"unknown", LevelInfo}, // default
	}

	for _, tc := range cases {
		got := parseLevel(tc.in)
		if got != tc.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ============================================================
// Concurrency
// ============================================================

func TestConcurrentWrites(t *testing.T) {
	reset()
	defer reset()
	Init("", LevelDebug)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 50; j++ {
				Info("goroutine", "concurrent write")
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not panic and ring buffer should be valid.
	entries := Get().Entries("debug", time.Time{}, 0)
	if len(entries) == 0 {
		t.Error("expected entries after concurrent writes")
	}
}
