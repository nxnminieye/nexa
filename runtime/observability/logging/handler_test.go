package logging

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerRedactsResolvedLeavesWithGroupSemantics(t *testing.T) {
	next := newRecordingHandler(true)
	var calls []redactionCall
	redactor := RedactorFunc(func(groups []string, attr slog.Attr) (slog.Attr, bool) {
		calls = append(calls, redactionCall{groups: append([]string(nil), groups...), attr: attr})
		switch attr.Key {
		case "drop":
			return slog.Attr{}, false
		case "replace":
			return slog.String("replacement", "redacted"), true
		default:
			return attr, true
		}
	})
	handler, err := NewHandler(HandlerOptions{Next: next, Redactor: redactor})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	handler = handler.WithAttrs([]slog.Attr{
		slog.String("top", "one"),
		slog.Group("outer",
			slog.String("keep", "two"),
			slog.Attr{Value: slog.GroupValue(slog.String("inline", "three"))},
			slog.Group("inner",
				slog.String("drop", "secret fixture"),
				slog.String("replace", "raw"),
			),
			slog.Group("empty"),
		),
	})
	record := slog.NewRecord(time.Unix(1, 2), slog.LevelInfo, "message", 7)
	record.AddAttrs(slog.Group("record", slog.String("leaf", "four")))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	wantCalls := []redactionCall{
		{attr: slog.String("top", "one")},
		{groups: []string{"outer"}, attr: slog.String("keep", "two")},
		{groups: []string{"outer"}, attr: slog.String("inline", "three")},
		{groups: []string{"outer", "inner"}, attr: slog.String("drop", "secret fixture")},
		{groups: []string{"outer", "inner"}, attr: slog.String("replace", "raw")},
		{groups: []string{"record"}, attr: slog.String("leaf", "four")},
	}
	assertRedactionCalls(t, calls, wantCalls)

	entry := next.core.singleEntry(t)
	if len(entry.operations) != 1 || entry.operations[0].kind != operationAttrs {
		t.Fatalf("downstream operations = %#v", entry.operations)
	}
	assertAttrsEqual(t, entry.operations[0].attrs, []slog.Attr{
		slog.String("top", "one"),
		slog.Group("outer",
			slog.String("keep", "two"),
			slog.String("inline", "three"),
			slog.Group("inner", slog.String("replacement", "redacted")),
		),
	})
	assertAttrsEqual(t, entry.attrs, []slog.Attr{
		slog.Group("record", slog.String("leaf", "four")),
	})
}

func TestHandlerPreservesInterleavedOperationScopes(t *testing.T) {
	next := newRecordingHandler(true)
	var calls []redactionCall
	handler, err := NewHandler(HandlerOptions{
		Next: next,
		Redactor: RedactorFunc(func(groups []string, attr slog.Attr) (slog.Attr, bool) {
			calls = append(calls, redactionCall{groups: append([]string(nil), groups...), attr: attr})
			return attr, true
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	stored := []slog.Attr{slog.String("first", "one")}
	handler = handler.WithAttrs(stored).
		WithGroup("").
		WithGroup("outer").
		WithAttrs([]slog.Attr{slog.String("second", "two")}).
		WithGroup("inner").
		WithAttrs([]slog.Attr{slog.String("third", "three")})
	stored[0] = slog.String("mutated", "secret fixture")
	record := slog.NewRecord(time.Unix(2, 3), slog.LevelWarn, "scoped", 8)
	record.AddAttrs(slog.String("fourth", "four"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	assertRedactionCalls(t, calls, []redactionCall{
		{attr: slog.String("first", "one")},
		{groups: []string{"outer"}, attr: slog.String("second", "two")},
		{groups: []string{"outer", "inner"}, attr: slog.String("third", "three")},
		{groups: []string{"outer", "inner"}, attr: slog.String("fourth", "four")},
	})
	entry := next.core.singleEntry(t)
	if len(entry.operations) != 5 {
		t.Fatalf("len(downstream operations) = %d, want 5: %#v", len(entry.operations), entry.operations)
	}
	assertOperationAttrs(t, entry.operations[0], slog.String("first", "one"))
	assertOperationGroup(t, entry.operations[1], "outer")
	assertOperationAttrs(t, entry.operations[2], slog.String("second", "two"))
	assertOperationGroup(t, entry.operations[3], "inner")
	assertOperationAttrs(t, entry.operations[4], slog.String("third", "three"))
	assertAttrsEqual(t, entry.attrs, []slog.Attr{slog.String("fourth", "four")})
}

func TestHandlerRedactorPanicReturnsSafeErrorWithoutDownstreamHandle(t *testing.T) {
	const secret = "secret fixture from panic"
	tests := []struct {
		name  string
		build func(slog.Handler) (slog.Handler, slog.Record)
	}{
		{
			name: "stored attr",
			build: func(handler slog.Handler) (slog.Handler, slog.Record) {
				return handler.WithAttrs([]slog.Attr{slog.String("panic", secret)}), slog.NewRecord(time.Unix(3, 4), slog.LevelInfo, "stored", 9)
			},
		},
		{
			name: "record attr",
			build: func(handler slog.Handler) (slog.Handler, slog.Record) {
				record := slog.NewRecord(time.Unix(4, 5), slog.LevelInfo, "record", 10)
				record.AddAttrs(slog.String("panic", secret))
				return handler, record
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := newRecordingHandler(true)
			handler, err := NewHandler(HandlerOptions{
				Next: next,
				Redactor: RedactorFunc(func(_ []string, attr slog.Attr) (slog.Attr, bool) {
					if attr.Key == "panic" {
						panic(secret)
					}
					return attr, true
				}),
			})
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}
			handler, record := test.build(handler)
			err = handler.Handle(context.Background(), record)
			requireLoggingError(t, err, "redactor_panic", "/redactor")
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("Handle() error leaked secret: %q", err.Error())
			}
			if got := next.core.entryCount(); got != 0 {
				t.Fatalf("downstream Handle calls = %d, want 0", got)
			}
		})
	}
}

func TestHandlerConstructorRejectsNilAndTypedNilDependencies(t *testing.T) {
	next := newRecordingHandler(true)
	keep := RedactorFunc(func(_ []string, attr slog.Attr) (slog.Attr, bool) { return attr, true })
	var typedNilNext *recordingHandler
	var typedNilRedactor RedactorFunc

	tests := []struct {
		name    string
		options HandlerOptions
		reason  string
		pointer string
	}{
		{name: "nil next", options: HandlerOptions{Redactor: keep}, reason: "handler_nil", pointer: "/next"},
		{name: "typed nil next", options: HandlerOptions{Next: typedNilNext, Redactor: keep}, reason: "handler_nil", pointer: "/next"},
		{name: "nil redactor", options: HandlerOptions{Next: next}, reason: "redactor_nil", pointer: "/redactor"},
		{name: "typed nil redactor", options: HandlerOptions{Next: next, Redactor: typedNilRedactor}, reason: "redactor_nil", pointer: "/redactor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewHandler(test.options)
			requireLoggingError(t, err, test.reason, test.pointer)
		})
	}
}

func TestHandlerDelegatesEnabledAndPreservesRecord(t *testing.T) {
	disabledNext := newRecordingHandler(false)
	var redactions atomic.Int32
	disabled, err := NewHandler(HandlerOptions{
		Next: disabledNext,
		Redactor: RedactorFunc(func(_ []string, attr slog.Attr) (slog.Attr, bool) {
			redactions.Add(1)
			return attr, true
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler(disabled) error = %v", err)
	}
	if disabled.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled() = true, want delegated false")
	}
	slog.New(disabled).Info("disabled", "secret", "fixture")
	if redactions.Load() != 0 || disabledNext.core.entryCount() != 0 {
		t.Fatalf("disabled work = redactions %d, entries %d", redactions.Load(), disabledNext.core.entryCount())
	}

	next := newRecordingHandler(true)
	handler, err := NewHandler(HandlerOptions{
		Next: next,
		Redactor: RedactorFunc(func(_ []string, attr slog.Attr) (slog.Attr, bool) {
			return attr, true
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	timestamp := time.Unix(123, 456)
	const pc = uintptr(0x12345)
	record := slog.NewRecord(timestamp, slog.LevelError+2, "exact message", pc)
	record.AddAttrs(slog.Int("answer", 42))
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "exact context")
	if err := handler.Handle(ctx, record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	entry := next.core.singleEntry(t)
	if entry.ctx != ctx || !entry.time.Equal(timestamp) || entry.level != record.Level || entry.message != record.Message || entry.pc != pc {
		t.Fatalf("record projection = ctx:%v time:%v level:%v message:%q pc:%x", entry.ctx == ctx, entry.time, entry.level, entry.message, entry.pc)
	}
	assertAttrsEqual(t, entry.attrs, []slog.Attr{slog.Int("answer", 42)})
	if record.NumAttrs() != 1 {
		t.Fatalf("source record NumAttrs() = %d, want unchanged 1", record.NumAttrs())
	}
}

func TestHandlerResolvesEachLeafOnceAndTrustsReplacement(t *testing.T) {
	next := newRecordingHandler(true)
	value := &countingLogValuer{value: "resolved"}
	var calls []string
	handler, err := NewHandler(HandlerOptions{
		Next: next,
		Redactor: RedactorFunc(func(_ []string, attr slog.Attr) (slog.Attr, bool) {
			calls = append(calls, attr.Key)
			return slog.Any("replacement", &countingLogValuer{value: "trusted"}), true
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	record := slog.NewRecord(time.Unix(5, 6), slog.LevelInfo, "resolve", 11)
	record.AddAttrs(slog.Any("original", value))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := value.calls.Load(); got != 1 {
		t.Fatalf("source LogValue calls = %d, want 1", got)
	}
	if len(calls) != 1 || calls[0] != "original" {
		t.Fatalf("redactor calls = %v, want original exactly once", calls)
	}
	entry := next.core.singleEntry(t)
	if len(entry.attrs) != 1 || entry.attrs[0].Key != "replacement" || entry.attrs[0].Value.Kind() != slog.KindLogValuer {
		t.Fatalf("trusted replacement = %#v", entry.attrs)
	}
}

func TestHandlerConcurrentCallsDoNotShareBuffers(t *testing.T) {
	next := newRecordingHandler(true)
	handler, err := NewHandler(HandlerOptions{
		Next: next,
		Redactor: RedactorFunc(func(groups []string, attr slog.Attr) (slog.Attr, bool) {
			return slog.String(strings.Join(groups, ".")+"."+attr.Key, attr.Value.String()), true
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	handler = handler.WithGroup("scope").WithAttrs([]slog.Attr{slog.String("stored", "value")})

	const count = 64
	var wait sync.WaitGroup
	wait.Add(count)
	for index := 0; index < count; index++ {
		go func(index int) {
			defer wait.Done()
			record := slog.NewRecord(time.Unix(int64(index), 0), slog.LevelInfo, fmt.Sprintf("message-%d", index), uintptr(index+1))
			record.AddAttrs(slog.String("record", fmt.Sprintf("value-%d", index)))
			if err := handler.Handle(context.Background(), record); err != nil {
				t.Errorf("Handle(%d) error = %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	if got := next.core.entryCount(); got != count {
		t.Fatalf("downstream entries = %d, want %d", got, count)
	}
}

type redactionCall struct {
	groups []string
	attr   slog.Attr
}

func assertRedactionCalls(t *testing.T, got, want []redactionCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(redactor calls) = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if strings.Join(got[index].groups, "/") != strings.Join(want[index].groups, "/") || !got[index].attr.Equal(want[index].attr) {
			t.Fatalf("redactor call[%d] = (%v, %#v), want (%v, %#v)", index, got[index].groups, got[index].attr, want[index].groups, want[index].attr)
		}
	}
}

type operationKind uint8

const (
	operationAttrs operationKind = iota + 1
	operationGroup
)

type recordedOperation struct {
	kind  operationKind
	attrs []slog.Attr
	group string
}

type recordedEntry struct {
	ctx        context.Context
	time       time.Time
	level      slog.Level
	message    string
	pc         uintptr
	attrs      []slog.Attr
	operations []recordedOperation
}

type recordingCore struct {
	mu      sync.Mutex
	enabled bool
	entries []recordedEntry
}

func (core *recordingCore) entryCount() int {
	core.mu.Lock()
	defer core.mu.Unlock()
	return len(core.entries)
}

func (core *recordingCore) singleEntry(t *testing.T) recordedEntry {
	t.Helper()
	core.mu.Lock()
	defer core.mu.Unlock()
	if len(core.entries) != 1 {
		t.Fatalf("downstream entries = %d, want 1", len(core.entries))
	}
	return core.entries[0]
}

type recordingHandler struct {
	core       *recordingCore
	operations []recordedOperation
}

func newRecordingHandler(enabled bool) *recordingHandler {
	return &recordingHandler{core: &recordingCore{enabled: enabled}}
}

func (handler *recordingHandler) Enabled(context.Context, slog.Level) bool {
	return handler.core.enabled
}

func (handler *recordingHandler) Handle(ctx context.Context, record slog.Record) error {
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	entry := recordedEntry{
		ctx:        ctx,
		time:       record.Time,
		level:      record.Level,
		message:    record.Message,
		pc:         record.PC,
		attrs:      attrs,
		operations: cloneRecordedOperations(handler.operations),
	}
	handler.core.mu.Lock()
	handler.core.entries = append(handler.core.entries, entry)
	handler.core.mu.Unlock()
	return nil
}

func (handler *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	operations := cloneRecordedOperations(handler.operations)
	operations = append(operations, recordedOperation{kind: operationAttrs, attrs: append([]slog.Attr(nil), attrs...)})
	return &recordingHandler{core: handler.core, operations: operations}
}

func (handler *recordingHandler) WithGroup(name string) slog.Handler {
	operations := cloneRecordedOperations(handler.operations)
	operations = append(operations, recordedOperation{kind: operationGroup, group: name})
	return &recordingHandler{core: handler.core, operations: operations}
}

func cloneRecordedOperations(source []recordedOperation) []recordedOperation {
	result := make([]recordedOperation, len(source))
	for index, operation := range source {
		result[index] = operation
		result[index].attrs = append([]slog.Attr(nil), operation.attrs...)
	}
	return result
}

func assertOperationAttrs(t *testing.T, operation recordedOperation, attrs ...slog.Attr) {
	t.Helper()
	if operation.kind != operationAttrs {
		t.Fatalf("operation kind = %d, want attrs", operation.kind)
	}
	assertAttrsEqual(t, operation.attrs, attrs)
}

func assertOperationGroup(t *testing.T, operation recordedOperation, group string) {
	t.Helper()
	if operation.kind != operationGroup || operation.group != group {
		t.Fatalf("group operation = (%d, %q), want %q", operation.kind, operation.group, group)
	}
}

type countingLogValuer struct {
	calls atomic.Int32
	value string
}

func (value *countingLogValuer) LogValue() slog.Value {
	value.calls.Add(1)
	return slog.StringValue(value.value)
}
