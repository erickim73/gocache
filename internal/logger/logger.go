package logger

import (
	"log/slog"
	"os"
)

// represents minimum log level to display
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo LogLevel = "info"
	LevelWarn LogLevel = "warn"
	LevelError LogLevel = "error"
)

// configures default slog logger without json output
func InitLogger(level LogLevel) {
	// convert LogLevel to slog.Level
	var slogLevel slog.Level
	switch level {
	case LevelDebug:
		slogLevel = slog.LevelDebug // most verbose, shows everything
	case LevelInfo:
		slogLevel = slog.LevelInfo // standard, shows info, warn, error
	case LevelWarn:
		slogLevel = slog.LevelWarn // only warnings and errors
	case LevelError:
		slogLevel = slog.LevelError // only errors
	default:
		slogLevel = slog.LevelInfo // default as info
	}

	// create json handler that writes to stdout
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel, // minimum level to log
		AddSource: false, // adds file:line information to logs
		ReplaceAttr: nil, // allow customizing field names
	})

	// set as default logger for entire application
	slog.SetDefault(slog.New(handler))

	// log that logging is initialized
	slog.Info("Logger initialized",
		"level", level,
		"format", "json",
	)
}

// creates human readable text logger
func InitTextLogger(level LogLevel) {
	var slogLevel slog.Level
	switch level {
	case LevelDebug:
		slogLevel = slog.LevelDebug // most verbose, shows everything
	case LevelInfo:
		slogLevel = slog.LevelInfo // standard, shows info, warn, error
	case LevelWarn:
		slogLevel = slog.LevelWarn // only warnings and errors
	case LevelError:
		slogLevel = slog.LevelError // only errors
	default:
		slogLevel = slog.LevelInfo // default as info
	}

	// TextHandler produces output like:
	// time=2024-02-10T10:00:00.000Z level=INFO msg="Server started" port=7000
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel, // minimum level to log
		AddSource: false, // adds file:line information to logs
	})

	// set as default logger for entire application
	slog.SetDefault(slog.New(handler))

	// log that logging is initialized
	slog.Info("Logger initialized",
		"level", level,
		"format", "text",
	)
}