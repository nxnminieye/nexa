package logging

import "log/slog"

// Redactor filters or replaces resolved non-group slog attributes.
type Redactor interface {
	Redact(groups []string, attr slog.Attr) (slog.Attr, bool)
}

// RedactorFunc adapts a function to Redactor.
type RedactorFunc func(groups []string, attr slog.Attr) (slog.Attr, bool)

// Redact calls fn.
func (fn RedactorFunc) Redact(groups []string, attr slog.Attr) (slog.Attr, bool) {
	return fn(groups, attr)
}
