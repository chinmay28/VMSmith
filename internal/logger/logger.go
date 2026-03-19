// Package logger provides a structured, leveled logger with an in-memory ring
// buffer so that the REST API can serve recent log entries to the GUI.
package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Level represents a log severity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ringSize is the number of log entries kept in memory for the GUI.
const ringSize = 2000

// Entry is a single structured log entry.
type Entry struct {
	Timestamp time.Time         `json:"ts"`
	Level     string            `json:"level"`
	Source    string            `json:"source"`
	Message   string            `json:"msg"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// Logger writes structured entries to a file and keeps a ring buffer for the
// GUI endpoint.
type Logger struct {
	mu       sync.RWMutex
	out      io.WriteCloser
	ring     [ringSize]Entry
	head     int // next write position
	count    int // number of valid entries (up to ringSize)
	minLevel Level
}

// global is the process-wide logger instance.
var (
	global   *Logger
	globalMu sync.Mutex
)

// Init opens logFile and installs it as the global logger. Safe to call
// multiple times; subsequent calls replace the previous logger.
// If logFile is empty, output goes to stderr only.
func Init(logFile string, minLevel Level) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	var out io.WriteCloser
	if logFile != "" {
		// Ensure parent directory exists.
		dir := logFile[:max(0, strings.LastIndex(logFile, "/"))]
		if dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("logger: creating log dir: %w", err)
			}
		}
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("logger: opening log file %s: %w", logFile, err)
		}
		out = f
	} else {
		out = os.Stderr
	}

	if global != nil {
		// Close the previous file handle (ignore error on close).
		_ = global.out.Close()
	}

	global = &Logger{
		out:      out,
		minLevel: minLevel,
	}
	return nil
}

// Get returns the global logger. If Init has not been called it returns a
// no-op logger that writes to stderr (so callers never need to nil-check).
func Get() *Logger {
	globalMu.Lock()
	defer globalMu.Unlock()
	if global == nil {
		global = &Logger{out: os.Stderr, minLevel: LevelInfo}
	}
	return global
}

// Close flushes and closes the underlying file. Called on daemon shutdown.
func Close() {
	globalMu.Lock()
	defer globalMu.Unlock()
	if global != nil {
		_ = global.out.Close()
		global = nil
	}
}

// ── convenience helpers ──────────────────────────────────────────────────────

func Debug(source, msg string, fields ...string) { Get().log(LevelDebug, source, msg, fields...) }
func Info(source, msg string, fields ...string)  { Get().log(LevelInfo, source, msg, fields...) }
func Warn(source, msg string, fields ...string)  { Get().log(LevelWarn, source, msg, fields...) }
func Error(source, msg string, fields ...string) { Get().log(LevelError, source, msg, fields...) }

// ── Logger methods ────────────────────────────────────────────────────────────

func (l *Logger) Debug(source, msg string, fields ...string) {
	l.log(LevelDebug, source, msg, fields...)
}
func (l *Logger) Info(source, msg string, fields ...string) { l.log(LevelInfo, source, msg, fields...) }
func (l *Logger) Warn(source, msg string, fields ...string) { l.log(LevelWarn, source, msg, fields...) }
func (l *Logger) Error(source, msg string, fields ...string) {
	l.log(LevelError, source, msg, fields...)
}

// log writes a structured entry.  fields must be alternating key, value pairs.
func (l *Logger) log(level Level, source, msg string, fields ...string) {
	if level < l.minLevel {
		return
	}

	entry := Entry{
		Timestamp: time.Now().UTC(),
		Level:     levelName(level),
		Source:    source,
		Message:   msg,
	}

	if len(fields) > 0 {
		entry.Fields = make(map[string]string, len(fields)/2)
		for i := 0; i+1 < len(fields); i += 2 {
			entry.Fields[fields[i]] = fields[i+1]
		}
	}

	// Append to ring buffer.
	l.mu.Lock()
	l.ring[l.head] = entry
	l.head = (l.head + 1) % ringSize
	if l.count < ringSize {
		l.count++
	}
	l.mu.Unlock()

	// Write to file (outside of the ring mutex).
	line := formatLine(entry)
	l.mu.RLock()
	_, _ = fmt.Fprint(l.out, line)
	l.mu.RUnlock()
}

// Entries returns recent log entries from the ring buffer, newest last.
// level filters by minimum level string ("debug", "info", "warn", "error", "").
// since filters to entries strictly after that time (zero = all).
// limit caps the result (0 = return all matching entries up to ringSize).
func (l *Logger) Entries(level string, since time.Time, limit int) []Entry {
	minLvl := parseLevel(level)

	l.mu.RLock()
	total := l.count
	head := l.head
	ring := l.ring // copy
	l.mu.RUnlock()

	// Reconstruct ordered slice (oldest first).
	ordered := make([]Entry, 0, total)
	start := (head - total + ringSize) % ringSize
	for i := 0; i < total; i++ {
		e := ring[(start+i)%ringSize]
		if parseLevel(e.Level) < minLvl {
			continue
		}
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		ordered = append(ordered, e)
	}

	if limit > 0 && len(ordered) > limit {
		ordered = ordered[len(ordered)-limit:]
	}
	return ordered
}

// ── helpers ───────────────────────────────────────────────────────────────────

func levelName(l Level) string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

func parseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func formatLine(e Entry) string {
	var sb strings.Builder
	sb.WriteString(e.Timestamp.Format(time.RFC3339))
	sb.WriteString(" [")
	sb.WriteString(strings.ToUpper(e.Level))
	sb.WriteString("] [")
	sb.WriteString(e.Source)
	sb.WriteString("] ")
	sb.WriteString(e.Message)
	for k, v := range e.Fields {
		sb.WriteString(" ")
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(v)
	}
	sb.WriteString("\n")
	return sb.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
