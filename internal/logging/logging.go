package logging

import (
	"log/slog"
	"os"
)

// Init initializes structured logging with slog as the default logger.
// Output is JSON to stdout for container-friendly log aggregation.
func Init() {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
}
