package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

const (
	DefaultLevel  = "info"
	DefaultFormat = "json"
)

func New(out io.Writer, levelText, formatText string) (*slog.Logger, error) {
	level, err := parseLevel(levelText)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: level}
	switch normalize(formatText, DefaultFormat) {
	case "json":
		return slog.New(slog.NewJSONHandler(out, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(out, opts)), nil
	default:
		return nil, fmt.Errorf("invalid log format %q", formatText)
	}
}

func parseLevel(text string) (slog.Level, error) {
	switch normalize(text, DefaultLevel) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q", text)
	}
}

func normalize(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}
