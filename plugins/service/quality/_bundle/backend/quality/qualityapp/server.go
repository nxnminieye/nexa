package qualityapp

import (
	"context"
	"reflect"

	"github.com/nxnminieye/nexa/quality/readmodel"
)

// Server serves a consumer-owned projection source without retaining mutable state.
type Server struct {
	source ProjectionSource
}

// NewServer constructs a read-only Quality server. A nil-like source selects Empty.
func NewServer(source ProjectionSource) (*Server, error) {
	if nilProjectionSource(source) {
		source = nil
	}
	return &Server{source: source}, nil
}

// Snapshot returns the current immutable projection.
func (s *Server) Snapshot(ctx context.Context) (readmodel.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return readmodel.Snapshot{}, runtimeError(CodeOperationCanceled)
	}
	if s == nil || s.source == nil {
		return readmodel.Empty(), nil
	}
	snapshot, err := s.source.Load(ctx)
	if ctx.Err() != nil {
		return readmodel.Snapshot{}, runtimeError(CodeOperationCanceled)
	}
	if err != nil {
		return readmodel.Snapshot{}, runtimeError(CodeProjectionUnavailable)
	}
	if _, err := readmodel.CanonicalJSON(snapshot); err != nil {
		return readmodel.Snapshot{}, runtimeError(CodeProjectionInvalid)
	}
	return snapshot, nil
}

func nilProjectionSource(source ProjectionSource) bool {
	if source == nil {
		return true
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
