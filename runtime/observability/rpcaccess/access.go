package rpcaccess

import (
	"context"
	"log/slog"
	"reflect"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

const (
	FieldMethod           = "rpc.method"
	FieldCode             = "rpc.grpc.code"
	FieldDuration         = "rpc.duration"
	FieldExtractionFailed = "rpc.context.extraction_failed"
	AccessMessage         = "rpc access"
)

// Options configures the unary access interceptor.
type Options struct {
	Logger    *slog.Logger
	Extractor Extractor
}

// UnaryServerInterceptor constructs an official gRPC unary access interceptor.
func UnaryServerInterceptor(options Options) (grpc.UnaryServerInterceptor, error) {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if options.Logger == nil || !options.Logger.Enabled(ctx, slog.LevelInfo) {
			return handler(ctx, request)
		}

		started := time.Now()
		method := ""
		if info != nil {
			method = info.FullMethod
		}
		requestContext, extractionFailed := safeExtract(options.Extractor, ctx, method)
		response, handlerErr := handler(ctx, request)
		ended := time.Now()

		duration := ended.Sub(started)
		if duration < 0 {
			duration = 0
		}
		attrs := make([]slog.Attr, 0, 4+len(requestContext.attrs))
		attrs = append(attrs,
			slog.String(FieldMethod, method),
			slog.String(FieldCode, status.Code(handlerErr).String()),
			slog.Duration(FieldDuration, duration),
		)
		if extractionFailed {
			attrs = append(attrs, slog.Bool(FieldExtractionFailed, true))
		}
		attrs = append(attrs, requestContext.Attrs()...)
		options.Logger.LogAttrs(ctx, slog.LevelInfo, AccessMessage, attrs...)
		return response, handlerErr
	}, nil
}

func safeExtract(extractor Extractor, ctx context.Context, method string) (requestContext RequestContext, failed bool) {
	if nilInterface(extractor) {
		return RequestContext{}, false
	}
	defer func() {
		if recover() != nil {
			requestContext = RequestContext{}
			failed = true
		}
	}()
	requestContext, err := extractor.Extract(ctx, method)
	if err != nil {
		return RequestContext{}, true
	}
	return requestContext, false
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
