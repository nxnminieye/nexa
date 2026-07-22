package qualityapp

import (
	"context"
	"errors"
)

// Health is an immutable readiness projection.
type Health struct {
	ready bool
	code  string
}

func (h Health) Ready() bool  { return h.ready }
func (h Health) Code() string { return h.code }

// Health reports whether the configured source can provide a valid snapshot.
func (s *Server) Health(ctx context.Context) Health {
	_, err := s.Snapshot(ctx)
	if err == nil {
		return Health{ready: true}
	}
	var projected *Error
	if errors.As(err, &projected) {
		return Health{code: projected.Code()}
	}
	return Health{code: CodeProjectionUnavailable}
}
