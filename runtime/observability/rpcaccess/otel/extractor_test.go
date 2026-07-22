package otel

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/nxnminieye/nexa/runtime/observability/logging"
	globalotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestExtractorReadsValidLocalAndRemoteSpanContexts(t *testing.T) {
	traceID := trace.TraceID{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	spanID := trace.SpanID{0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10}
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "remote"}[remote], func(t *testing.T) {
			spanContext := trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID,
				Remote:  remote,
			})
			ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
			requestContext, err := NewExtractor().Extract(ctx, "/sample.v1.Service/Get")
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			assertOTelAttrs(t, requestContext.Attrs(), []slog.Attr{
				slog.String(logging.FieldTraceID, "abcdef0123456789abcdef0123456789"),
				slog.String(logging.FieldSpanID, "fedcba9876543210"),
			})
		})
	}
}

func TestExtractorReturnsEmptyForMissingOrInvalidSpanContext(t *testing.T) {
	validSpanID := trace.SpanID{1}
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{name: "missing", ctx: context.Background()},
		{name: "invalid", ctx: trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{SpanID: validSpanID}))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestContext, err := NewExtractor().Extract(test.ctx, "/sample.v1.Service/Get")
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if attrs := requestContext.Attrs(); len(attrs) != 0 {
				t.Fatalf("Attrs() = %#v, want empty", attrs)
			}
		})
	}
}

func TestExtractorDoesNotUseTracerProviderOrStartSpan(t *testing.T) {
	previous := globalotel.GetTracerProvider()
	provider := &countingTracerProvider{TracerProvider: noop.NewTracerProvider()}
	globalotel.SetTracerProvider(provider)
	t.Cleanup(func() { globalotel.SetTracerProvider(previous) })

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	requestContext, err := NewExtractor().Extract(ctx, "/sample.v1.Service/Get")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("Tracer() calls = %d, want 0", provider.calls.Load())
	}
	if got := trace.SpanContextFromContext(ctx); !got.Equal(spanContext) {
		t.Fatalf("source SpanContext changed: %#v", got)
	}
	if len(requestContext.Attrs()) != 2 {
		t.Fatalf("Attrs() = %#v, want trace and span", requestContext.Attrs())
	}
}

type countingTracerProvider struct {
	trace.TracerProvider
	calls atomic.Int32
}

func (provider *countingTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	provider.calls.Add(1)
	return provider.TracerProvider.Tracer(name, options...)
}

func assertOTelAttrs(t *testing.T, got, want []slog.Attr) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(attrs) = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if !got[index].Equal(want[index]) {
			t.Fatalf("attrs[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}
