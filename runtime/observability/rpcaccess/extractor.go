package rpcaccess

import (
	"context"
	"log/slog"

	"github.com/nxnminieye/nexa/runtime/observability/logging"
)

// RequestContext contains validated consumer-supplied access attributes.
type RequestContext struct {
	attrs []slog.Attr
}

// NewRequestContext captures validated context fields.
func NewRequestContext(fields logging.ContextFields) RequestContext {
	return RequestContext{attrs: fields.Attrs()}
}

// Attrs returns a defensive copy in logging's stable field order.
func (r RequestContext) Attrs() []slog.Attr {
	return append([]slog.Attr(nil), r.attrs...)
}

// Extractor derives access attributes from an RPC context and full method.
type Extractor interface {
	Extract(ctx context.Context, fullMethod string) (RequestContext, error)
}

// ExtractorFunc adapts a function to Extractor.
type ExtractorFunc func(ctx context.Context, fullMethod string) (RequestContext, error)

// Extract calls fn.
func (fn ExtractorFunc) Extract(ctx context.Context, fullMethod string) (RequestContext, error) {
	return fn(ctx, fullMethod)
}
