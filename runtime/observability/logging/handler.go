package logging

import (
	"context"
	"log/slog"
	"reflect"
)

// HandlerOptions configures the redacting slog handler.
type HandlerOptions struct {
	Next     slog.Handler
	Redactor Redactor
}

type chainOperationKind uint8

const (
	chainOperationAttrs chainOperationKind = iota + 1
	chainOperationGroup
)

type handlerOperation struct {
	kind  chainOperationKind
	attrs []slog.Attr
	group string
}

type handler struct {
	next       slog.Handler
	redactor   Redactor
	operations []handlerOperation
}

// NewHandler wraps a slog handler with leaf attribute redaction.
func NewHandler(options HandlerOptions) (slog.Handler, error) {
	if nilInterface(options.Next) {
		return nil, invalid("handler_nil", "/next")
	}
	if nilInterface(options.Redactor) {
		return nil, invalid("redactor_nil", "/redactor")
	}
	return &handler{next: options.Next, redactor: options.Redactor}, nil
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	next := h.next
	groups := make([]string, 0, len(h.operations))
	for _, operation := range h.operations {
		switch operation.kind {
		case chainOperationAttrs:
			attrs, err := h.redactAttrs(groups, operation.attrs)
			if err != nil {
				return err
			}
			if len(attrs) != 0 {
				next = next.WithAttrs(attrs)
			}
		case chainOperationGroup:
			next = next.WithGroup(operation.group)
			groups = append(groups, operation.group)
		}
	}

	recordAttrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		recordAttrs = append(recordAttrs, attr)
		return true
	})
	redacted, err := h.redactAttrs(groups, recordAttrs)
	if err != nil {
		return err
	}

	result := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	result.AddAttrs(redacted...)
	return next.Handle(ctx, result)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	operations := cloneOperations(h.operations, 1)
	operations = append(operations, handlerOperation{kind: chainOperationAttrs, attrs: cloneAttrs(attrs)})
	return &handler{next: h.next, redactor: h.redactor, operations: operations}
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	operations := cloneOperations(h.operations, 1)
	operations = append(operations, handlerOperation{kind: chainOperationGroup, group: name})
	return &handler{next: h.next, redactor: h.redactor, operations: operations}
}

func (h *handler) redactAttrs(groups []string, attrs []slog.Attr) ([]slog.Attr, error) {
	result := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		attr.Value = attr.Value.Resolve()
		if attr.Value.Kind() == slog.KindGroup {
			childGroups := groups
			if attr.Key != "" {
				childGroups = appendCopy(groups, attr.Key)
			}
			children, err := h.redactAttrs(childGroups, attr.Value.Group())
			if err != nil {
				return nil, err
			}
			if len(children) == 0 {
				continue
			}
			if attr.Key == "" {
				result = append(result, children...)
				continue
			}
			result = append(result, slog.Attr{Key: attr.Key, Value: slog.GroupValue(children...)})
			continue
		}

		redacted, keep, err := h.redact(append([]string(nil), groups...), attr)
		if err != nil {
			return nil, err
		}
		if keep {
			result = append(result, redacted)
		}
	}
	return result, nil
}

func (h *handler) redact(groups []string, attr slog.Attr) (redacted slog.Attr, keep bool, err error) {
	defer func() {
		if recover() != nil {
			redacted = slog.Attr{}
			keep = false
			err = invalid("redactor_panic", "/redactor")
		}
	}()
	redacted, keep = h.redactor.Redact(groups, attr)
	return redacted, keep, nil
}

func cloneOperations(source []handlerOperation, extra int) []handlerOperation {
	result := make([]handlerOperation, len(source), len(source)+extra)
	copy(result, source)
	return result
}

func cloneAttrs(source []slog.Attr) []slog.Attr {
	result := make([]slog.Attr, len(source))
	for index, attr := range source {
		result[index] = attr
		if attr.Value.Kind() == slog.KindGroup {
			result[index].Value = slog.GroupValue(cloneAttrs(attr.Value.Group())...)
		}
	}
	return result
}

func appendCopy(source []string, value string) []string {
	result := make([]string, len(source), len(source)+1)
	copy(result, source)
	return append(result, value)
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
