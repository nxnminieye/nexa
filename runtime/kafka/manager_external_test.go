package kafka_test

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestManagerExternalConsumer(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot = spacedRepositoryRoot(t, repositoryRoot)
	moduleRoot := t.TempDir()
	module := "module example.com/kafka-manager-consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\n" +
		"replace github.com/nxnminieye/nexa => " + strconv.Quote(filepath.ToSlash(repositoryRoot)) + "\n"
	writeExternalFile(t, filepath.Join(moduleRoot, "go.mod"), module)
	writeExternalFile(t, filepath.Join(moduleRoot, "manager_test.go"), externalManagerConsumerSource)

	environment := externalGoEnvironment(t, t.TempDir())
	runExternalGo(t, moduleRoot, environment, "prepare external Manager consumer", "mod", "tidy")
	runExternalGo(t, moduleRoot, environment, "test external Manager consumer", "test", "-mod=readonly", "./...")
}

const externalManagerConsumerSource = `package consumer_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/runtime/kafka"
)

type reader struct {
	batch kafka.Batch
	raw error
	committed chan struct{}
	closeOnce sync.Once
	closed chan struct{}
}

func (r *reader) Poll(ctx context.Context) (kafka.Batch, error) {
	if r.raw != nil { return kafka.Batch{}, r.raw }
	if r.batch.Valid() {
		batch := r.batch
		r.batch = kafka.Batch{}
		return batch, nil
	}
	<-ctx.Done()
	return kafka.Batch{}, ctx.Err()
}

func (r *reader) Commit(context.Context) error {
	select { case r.committed <- struct{}{}: default: }
	return nil
}

func (r *reader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

type factory struct { reader kafka.Reader }
func (f factory) Open(context.Context, kafka.Subscription) (kafka.Reader, error) { return f.reader, nil }

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func subscription(t *testing.T, id string, handled chan struct{}) kafka.Subscription {
	t.Helper()
	subscription, err := kafka.NewSubscription(kafka.SubscriptionSpec{
		ID: id, Group: "external-manager", Topics: []string{"external.manager"},
		Handler: kafka.HandlerFunc(func(context.Context, kafka.Record) error {
			if handled != nil { handled <- struct{}{} }
			return nil
		}),
	})
	if err != nil { t.Fatal(err) }
	return subscription
}

func TestPublicManagerSuccessAndTerminalFailure(t *testing.T) {
	record, err := kafka.NewRecord(kafka.RecordSpec{Topic: "external.manager"})
	if err != nil { t.Fatal(err) }
	batch, err := kafka.NewBatch([]kafka.Record{record})
	if err != nil { t.Fatal(err) }
	committed := make(chan struct{}, 1)
	successReader := &reader{batch: batch, committed: committed, closed: make(chan struct{})}
	manager, err := kafka.NewManager(kafka.ManagerOptions{
		Subscriptions: []kafka.Subscription{subscription(t, "external.success", nil)},
		ReaderFactory: factory{reader: successReader}, RetryPolicy: kafka.NoRetry(), Logger: logger(),
	})
	if err != nil || manager.State() != kafka.StateNew { t.Fatalf("NewManager = %v state %q", err, manager.State()) }
	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := manager.Start(startCtx); err != nil { t.Fatal(err) }
	<-committed
	cancelStart()
	if manager.State() != kafka.StateRunning { t.Fatalf("successful Start context retained: %q", manager.State()) }
	if err := manager.Close(context.Background()); err != nil { t.Fatal(err) }
	if manager.State() != kafka.StateClosed { t.Fatalf("closed state = %q", manager.State()) }
	<-successReader.closed

	raw := errors.New("external adapter secret")
	failingReader := &reader{raw: raw, committed: make(chan struct{}, 1), closed: make(chan struct{})}
	failed, err := kafka.NewManager(kafka.ManagerOptions{
		Subscriptions: []kafka.Subscription{subscription(t, "external.failure", nil)},
		ReaderFactory: factory{reader: failingReader}, RetryPolicy: kafka.NoRetry(), Logger: logger(),
	})
	if err != nil { t.Fatal(err) }
	if err := failed.Start(context.Background()); err != nil { t.Fatal(err) }
	terminal := failed.Wait(context.Background())
	var typed *kafka.Error
	if !errors.As(terminal, &typed) || typed.Code() != "reader_failed" || typed.Reason() != "poll_failed" || typed.SubscriptionID() != "external.failure" {
		t.Fatalf("terminal = %T %v", terminal, terminal)
	}
	if errors.Is(terminal, raw) || errors.Unwrap(terminal) != nil { t.Fatal("raw cause escaped public Manager") }
	if err := failed.Close(context.Background()); err == nil { t.Fatal("failed Close lost terminal result") }
}
`
