package logging

import (
	"log/slog"
	"os"
	"time"

	charmlog "github.com/charmbracelet/log"
)

func Setup(level slog.Level) {
	charmLevel := charmlog.InfoLevel
	switch {
	case level <= slog.LevelDebug:
		charmLevel = charmlog.DebugLevel
	case level >= slog.LevelError:
		charmLevel = charmlog.ErrorLevel
	case level >= slog.LevelWarn:
		charmLevel = charmlog.WarnLevel
	}

	handler := charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		Level:           charmLevel,
		TimeFormat:      time.RFC3339,
		ReportTimestamp: true,
	})
	slog.SetDefault(slog.New(handler))
}
