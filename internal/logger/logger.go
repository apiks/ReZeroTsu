package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Init configures slog.Default() with level (debug/info/warn/error) and format (text/json).
func Init(level, format string) {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if strings.ToLower(strings.TrimSpace(format)) == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// PrintAlways writes one line to stderr (not filtered by level). keyValues are slog-style key-value pairs.
func PrintAlways(msg string, keyValues ...any) {
	var b strings.Builder
	b.WriteString(msg)
	for i := 0; i+1 < len(keyValues); i += 2 {
		b.WriteString(" ")
		fmt.Fprint(&b, keyValues[i])
		b.WriteString("=")
		fmt.Fprint(&b, keyValues[i+1])
	}
	b.WriteString("\n")
	os.Stderr.WriteString(b.String())
}

func For(component string) *slog.Logger {
	return slog.Default().With("component", component)
}
