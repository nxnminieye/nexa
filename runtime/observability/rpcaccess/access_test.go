package rpcaccess

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nxnminieye/nexa/runtime/observability/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestOptionalDependenciesDoNotBlockRPCHandling(t *testing.T) {
	interceptor, err := UnaryServerInterceptor(Options{})
	if err != nil {
		t.Fatalf("UnaryServerInterceptor() error = %v", err)
	}
	want := &struct{}{}
	got, callErr := interceptor(context.Background(), struct{}{}, nil, func(context.Context, any) (any, error) {
		return want, nil
	})
	if callErr != nil || got != want {
		t.Fatalf("interceptor result = (%p, %v), want (%p, nil)", got, callErr, want)
	}
}

func TestDisabledLoggerSkipsExtraction(t *testing.T) {
	handler := &captureHandler{enabled: false}
	var extractionCalls atomic.Int32
	interceptor, err := UnaryServerInterceptor(Options{
		Logger: slog.New(handler),
		Extractor: ExtractorFunc(func(context.Context, string) (RequestContext, error) {
			extractionCalls.Add(1)
			return RequestContext{}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := interceptor(context.Background(), nil, nil, func(context.Context, any) (any, error) { return "ok", nil })
	if callErr != nil || extractionCalls.Load() != 0 || len(handler.snapshot()) != 0 {
		t.Fatalf("disabled logger performed work: err=%v extraction=%d records=%d", callErr, extractionCalls.Load(), len(handler.snapshot()))
	}
}

func TestUnaryLogsStandardAccessFieldsAndOptionalContext(t *testing.T) {
	fields, err := logging.NewContextFields(logging.ContextFieldsSpec{RequestID: "request-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := &captureHandler{enabled: true}
	interceptor, err := UnaryServerInterceptor(Options{
		Logger: slog.New(handler),
		Extractor: ExtractorFunc(func(_ context.Context, method string) (RequestContext, error) {
			if method != "/sample.Service/Get" {
				t.Fatalf("method = %q", method)
			}
			return NewRequestContext(fields), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantErr := status.Error(codes.PermissionDenied, "private")
	wantResponse := &struct{}{}
	gotResponse, gotErr := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{FullMethod: "/sample.Service/Get"}, func(context.Context, any) (any, error) {
		return wantResponse, wantErr
	})
	if gotResponse != wantResponse || gotErr != wantErr {
		t.Fatalf("result identity changed: (%p, %v)", gotResponse, gotErr)
	}
	records := handler.snapshot()
	if len(records) != 1 || records[0].message != AccessMessage || records[0].level != slog.LevelInfo {
		t.Fatalf("records = %#v", records)
	}
	attrs := records[0].attrs
	if len(attrs) != 5 || attrs[0].Key != FieldMethod || attrs[0].Value.String() != "/sample.Service/Get" || attrs[1].Key != FieldCode || attrs[1].Value.String() != codes.PermissionDenied.String() || attrs[2].Key != FieldDuration || attrs[2].Value.Duration() < 0 || attrs[3].Key != logging.FieldRequestID || attrs[4].Key != logging.FieldTenantID {
		t.Fatalf("attrs = %#v", attrs)
	}
}

func TestExtractorFailureIsSafeAndHandlerStillRuns(t *testing.T) {
	for _, extractor := range []Extractor{
		ExtractorFunc(func(context.Context, string) (RequestContext, error) { return RequestContext{}, errors.New("secret") }),
		ExtractorFunc(func(context.Context, string) (RequestContext, error) { panic("secret") }),
	} {
		handler := &captureHandler{enabled: true}
		interceptor, err := UnaryServerInterceptor(Options{Logger: slog.New(handler), Extractor: extractor})
		if err != nil {
			t.Fatal(err)
		}
		got, callErr := interceptor(context.Background(), nil, nil, func(context.Context, any) (any, error) { return "ok", nil })
		if callErr != nil || got != "ok" {
			t.Fatalf("handler result = (%v, %v)", got, callErr)
		}
		attrs := handler.snapshot()[0].attrs
		if len(attrs) != 4 || attrs[3].Key != FieldExtractionFailed || !attrs[3].Value.Bool() {
			t.Fatalf("attrs = %#v", attrs)
		}
	}
}

func TestHandlerPanicPropagatesWithoutCompletedRecord(t *testing.T) {
	handler := &captureHandler{enabled: true}
	interceptor, err := UnaryServerInterceptor(Options{Logger: slog.New(handler)})
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("panic")
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v, want %v", got, want)
		}
		if len(handler.snapshot()) != 0 {
			t.Fatal("panic emitted completed access record")
		}
	}()
	_, _ = interceptor(context.Background(), nil, nil, func(context.Context, any) (any, error) { panic(want) })
}

func TestRequestContextAttrsAreDefensive(t *testing.T) {
	fields, err := logging.NewContextFields(logging.ContextFieldsSpec{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	requestContext := NewRequestContext(fields)
	attrs := requestContext.Attrs()
	attrs[0] = slog.String("changed", "value")
	if got := requestContext.Attrs(); len(got) != 1 || got[0].Key != logging.FieldTenantID {
		t.Fatalf("Attrs() = %#v", got)
	}
}

type capturedRecord struct {
	level   slog.Level
	message string
	attrs   []slog.Attr
}

type captureHandler struct {
	mu      sync.Mutex
	enabled bool
	records []capturedRecord
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return h.enabled }
func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool { attrs = append(attrs, attr); return true })
	h.mu.Lock()
	h.records = append(h.records, capturedRecord{level: record.Level, message: record.Message, attrs: attrs})
	h.mu.Unlock()
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }
func (h *captureHandler) snapshot() []capturedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]capturedRecord(nil), h.records...)
}
