// Package otel adapts an existing OpenTelemetry SpanContext to RPC access fields.
package otel

import (
	"context"

	"github.com/nxnminieye/nexa/runtime/observability/logging"
	"github.com/nxnminieye/nexa/runtime/observability/rpcaccess"
	"go.opentelemetry.io/otel/trace"
)

// NewExtractor returns a stateless reader for an existing SpanContext.
func NewExtractor() rpcaccess.Extractor {
	return rpcaccess.ExtractorFunc(func(ctx context.Context, _ string) (rpcaccess.RequestContext, error) {
		if ctx == nil {
			return rpcaccess.RequestContext{}, nil
		}
		spanContext := trace.SpanContextFromContext(ctx)
		if !spanContext.IsValid() {
			return rpcaccess.RequestContext{}, nil
		}
		fields, err := logging.NewContextFields(logging.ContextFieldsSpec{
			TraceID: spanContext.TraceID().String(),
			SpanID:  spanContext.SpanID().String(),
		})
		if err != nil {
			return rpcaccess.RequestContext{}, nil
		}
		return rpcaccess.NewRequestContext(fields), nil
	})
}
