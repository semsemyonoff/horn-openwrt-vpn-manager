// Package logx provides colored, timestamped console logging
// matching the output style of the legacy shell scripts.
package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// ANSI color codes.
const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cDim    = "\033[0;90m"
	cRed    = "\033[1;31m"
	cGreen  = "\033[0;32m"
	cYellow = "\033[0;33m"
	cCyan   = "\033[0;36m"
)

// Level controls message visibility.
type Level int

const (
	LevelNormal  Level = 0
	LevelVerbose Level = 1
	LevelDebug   Level = 2
	LevelTrace   Level = 3
)

// Logger is a colored, leveled console logger.
type Logger struct {
	mu    sync.Mutex
	out   io.Writer
	color bool
	level Level
	debug bool // debug mode shows [DEBUG] prefix
}

var std = &Logger{out: os.Stderr, color: true}

// Setup configures the global logger.
func Setup(color bool, verbosity int, debug bool) {
	std.mu.Lock()
	defer std.mu.Unlock()
	std.color = color
	std.level = Level(verbosity)
	std.debug = debug
}

// SetOutput overrides the output writer (for testing).
func SetOutput(w io.Writer) {
	std.mu.Lock()
	defer std.mu.Unlock()
	std.out = w
}

func timestamp() string {
	return time.Now().Format("15:04:05")
}

func (l *Logger) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(l.out, msg)
}

// stateSnapshot returns a consistent snapshot of the mutable logger state.
func (l *Logger) stateSnapshot() (color bool, level Level, debug bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.color, l.level, l.debug
}

// colorize wraps text in ANSI color codes if color is enabled.
func (l *Logger) c(code, text string) string {
	color, _, _ := l.stateSnapshot()
	if !color {
		return text
	}
	return code + text + cReset
}

// Header prints a prominent section header: === text ===.
func Header(text string) {
	std.printf("%s %s", std.c(cBold+cGreen, fmt.Sprintf("[%s]", timestamp())), std.c(cBold, fmt.Sprintf("=== %s ===", text)))
}

// Info prints a normal-level message (always visible): green timestamp.
func Info(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _, debug := std.stateSnapshot()
	prefix := std.c(cGreen, fmt.Sprintf("[%s]", timestamp()))
	if debug {
		prefix = std.c(cYellow, "[DEBUG]") + " " + prefix
	}
	std.printf("%s %s", prefix, msg)
}

// OK prints a success message: green timestamp + green text.
func OK(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _, debug := std.stateSnapshot()
	prefix := std.c(cGreen, fmt.Sprintf("[%s]", timestamp()))
	if debug {
		prefix = std.c(cYellow, "[DEBUG]") + " " + prefix
	}
	std.printf("%s %s", prefix, std.c(cGreen, msg))
}

// Warn prints a warning message: yellow.
func Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	prefix := std.c(cGreen, fmt.Sprintf("[%s]", timestamp()))
	std.printf("%s %s", prefix, std.c(cYellow, msg))
}

// Err prints an error message: red ERROR prefix.
func Err(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	std.printf("%s %s", std.c(cRed, fmt.Sprintf("[%s] ERROR:", timestamp())), msg)
}

// Detail prints a verbose-level message (visible at -v): cyan timestamp.
func Detail(format string, args ...any) {
	_, level, _ := std.stateSnapshot()
	if level < LevelVerbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	std.printf("%s %s", std.c(cCyan, fmt.Sprintf("[%s]", timestamp())), msg)
}

// Debug prints a debug-level message (visible at -vv): dim.
func Debug(format string, args ...any) {
	_, level, _ := std.stateSnapshot()
	if level < LevelDebug {
		return
	}
	msg := fmt.Sprintf(format, args...)
	std.printf("%s", std.c(cDim, fmt.Sprintf("[%s] %s", timestamp(), msg)))
}

// Trace prints a trace-level message (visible at -vvv): dim with [dbg] prefix.
func Trace(format string, args ...any) {
	_, level, _ := std.stateSnapshot()
	if level < LevelTrace {
		return
	}
	msg := fmt.Sprintf(format, args...)
	std.printf("%s", std.c(cDim, fmt.Sprintf("[%s] [dbg] %s", timestamp(), msg)))
}

// Dim prints text in dim/gray (always visible, for secondary info).
func Dim(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	std.printf("  %s", std.c(cDim, msg))
}

// Bold wraps text in bold if color is enabled.
func Bold(s string) string {
	return std.c(cBold, s)
}

// Green wraps text in green if color is enabled.
func Green(s string) string {
	return std.c(cGreen, s)
}

// Yellow wraps text in yellow if color is enabled.
func Yellow(s string) string {
	return std.c(cYellow, s)
}

// Cyan wraps text in cyan if color is enabled.
func Cyan(s string) string {
	return std.c(cCyan, s)
}

// DimText wraps text in dim if color is enabled.
func DimText(s string) string {
	return std.c(cDim, s)
}
