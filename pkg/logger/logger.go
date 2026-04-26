package logger

import (
	"log/slog"
	"os"
)

type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
	With(args ...any) Logger
}

type slogLogger struct {
	inner *slog.Logger
}

func New() Logger {
	return &slogLogger{
		inner: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}

func (l *slogLogger) Info(msg string, args ...any) {
	l.inner.Info(msg, args...)
}

func (l *slogLogger) Warn(msg string, args ...any) {
	l.inner.Warn(msg, args...)
}

func (l *slogLogger) Error(msg string, args ...any) {
	l.inner.Error(msg, args...)
}

func (l *slogLogger) Debug(msg string, args ...any) {
	l.inner.Debug(msg, args...)
}

func (l *slogLogger) With(args ...any) Logger {
	return &slogLogger{
		inner: l.inner.With(args...),
	}
}
