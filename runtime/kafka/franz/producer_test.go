package franz

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestProducerFactoryValidatesAndFreezesOptions(t *testing.T) {
	client := &fakeClient{}
	clients := &fakeClientFactory{client: client}
	brokers := []string{"one:9092"}
	options := []kgo.Opt{kgo.ClientID("caller")}
	factory, err := newProducerFactoryWithFactory(ProducerFactoryOptions{Brokers: brokers, ClientOptions: options}, clients)
	if err != nil {
		t.Fatalf("newProducerFactoryWithFactory() error = %v", err)
	}
	if clients.calls != 0 {
		t.Fatalf("constructor client opens = %d", clients.calls)
	}
	brokers[0] = "mutated"
	options[0] = kgo.ClientID("mutated")

	producer, err := factory.Open(context.Background())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if producer == nil || clients.calls != 1 {
		t.Fatalf("Open() producer/calls = %v/%d", producer, clients.calls)
	}
	configured, err := kgo.NewClient(clients.options...)
	if err != nil {
		t.Fatalf("kgo.NewClient(captured options) error = %v", err)
	}
	defer configured.Close()
	if got := configured.OptValue(kgo.SeedBrokers); !reflect.DeepEqual(got, []string{"one:9092"}) {
		t.Fatalf("brokers = %#v", got)
	}
	if got := configured.OptValue(kgo.ClientID); got != "caller" {
		t.Fatalf("client id = %#v", got)
	}
}

func TestProducerFactoryRejectsInvalidInputAndOpenFailure(t *testing.T) {
	failedClient := &fakeClient{}
	clients := &fakeClientFactory{client: failedClient}
	_, err := newProducerFactoryWithFactory(ProducerFactoryOptions{}, clients)
	assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", "brokers_empty", "/brokers", "")
	_, err = newProducerFactoryWithFactory(ProducerFactoryOptions{Brokers: []string{"one:9092"}, ClientOptions: []kgo.Opt{nil}}, clients)
	assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", "client_option_nil", "/clientOptions/0", "")
	if clients.calls != 0 {
		t.Fatalf("invalid constructors opened clients = %d", clients.calls)
	}

	clients.openErr = errors.New("password=secret")
	factory, err := newProducerFactoryWithFactory(ProducerFactoryOptions{Brokers: []string{"one:9092"}}, clients)
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}
	_, err = factory.Open(nil)
	assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", "context_nil", "/context", "")
	_, err = factory.Open(context.Background())
	assertFranzError(t, err, ErrOpenFailed, "franz_open_failed", "client_open_failed", "", kafka.StageOpen)
	if errors.Is(err, clients.openErr) || err.Error() == clients.openErr.Error() {
		t.Fatal("Open() exposed private cause")
	}
	if failedClient.closeCalls != 1 {
		t.Fatalf("failed Open() client Close calls = %d, want 1", failedClient.closeCalls)
	}
}

func TestProducerFactoryRejectsTypedNilClient(t *testing.T) {
	var typedNil *fakeClient
	clients := &fakeClientFactory{client: typedNil}
	factory, err := newProducerFactoryWithFactory(ProducerFactoryOptions{Brokers: []string{"one:9092"}}, clients)
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}
	opened, err := factory.Open(context.Background())
	if opened != nil {
		t.Fatalf("Open() producer = %T, want nil", opened)
	}
	assertFranzError(t, err, ErrOpenFailed, "franz_open_failed", "client_open_failed", "", kafka.StageOpen)
}

func TestProducerProducesOneCopiedMessageAndProjectsFailure(t *testing.T) {
	var calls int
	var captured *kgo.Record
	client := &fakeClient{produceFn: func(_ context.Context, record *kgo.Record, callback func(*kgo.Record, error)) {
		calls++
		captured = record
		callback(record, nil)
	}}
	producer := newProducer(client)
	message := validMessage(t)
	if err := producer.Produce(context.Background(), message); err != nil {
		t.Fatalf("Produce() error = %v", err)
	}
	if calls != 1 || captured.Topic != "events" || len(captured.Headers) != 2 {
		t.Fatalf("Produce() calls/record = %d/%#v", calls, captured)
	}
	captured.Key[0] = 'X'
	captured.Value[0] = 'X'
	captured.Headers[1].Value[0] = 'X'
	assertBytes(t, "message key", message.Key(), []byte("key"))
	assertBytes(t, "message value", message.Value(), []byte("value"))
	assertBytes(t, "message header", message.Headers()[1].Value, []byte("second"))

	secret := errors.New("delivery secret")
	client.produceFn = func(_ context.Context, record *kgo.Record, callback func(*kgo.Record, error)) {
		callback(record, secret)
	}
	err := producer.Produce(context.Background(), validMessage(t))
	assertFranzError(t, err, ErrDeliveryFailed, "franz_delivery_failed", "delivery_failed", "", "")
	if errors.Is(err, secret) || err.Error() == secret.Error() {
		t.Fatal("Produce() exposed delivery cause")
	}

	err = producer.Produce(context.Background(), kafka.Message{})
	assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", "message_required", "/message", "")
}

func TestProducerProjectsActiveContextCancellationExactly(t *testing.T) {
	started := make(chan struct{})
	client := &fakeClient{produceFn: func(_ context.Context, _ *kgo.Record, _ func(*kgo.Record, error)) {
		close(started)
	}}
	producer := newProducer(client)
	message := validMessage(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- producer.Produce(ctx, message) }()
	<-started
	cancel()
	err := <-done
	assertFranzError(t, err, ErrOperationCanceled, "franz_operation_canceled", "context_canceled", "", "")
	if !errors.Is(err, context.Canceled) || !errors.Is(errors.Unwrap(err), context.Canceled) {
		t.Fatalf("Produce cancellation unwrap = %v", errors.Unwrap(err))
	}
}

func TestProducerCloseBlocksNewProduceAndWaitsForInflight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := &fakeClient{produceFn: func(_ context.Context, record *kgo.Record, callback func(*kgo.Record, error)) {
		close(started)
		go func() {
			<-release
			callback(record, nil)
		}()
	}}
	producer := newProducer(client)
	message := validMessage(t)
	produceDone := make(chan error, 1)
	go func() { produceDone <- producer.Produce(context.Background(), message) }()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- producer.Close(context.Background()) }()

	deadline := time.Now().Add(time.Second)
	for producerStateOf(producer) != producerStateClosing && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	err := producer.Produce(context.Background(), validMessage(t))
	assertFranzError(t, err, ErrProducerState, "franz_producer_state", "producer_closed", "/state", "")
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before in-flight delivery: %v", err)
	default:
	}
	close(release)
	if err := <-produceDone; err != nil {
		t.Fatalf("in-flight Produce() error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("client Close calls = %d", client.closeCalls)
	}
}

func TestProducerCloseDeadlineCanContinueLater(t *testing.T) {
	var flushCalls atomic.Int32
	client := &fakeClient{flushFn: func(ctx context.Context) error {
		if flushCalls.Add(1) == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	producer := newProducer(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := producer.Close(ctx)
	assertFranzError(t, err, ErrOperationCanceled, "franz_operation_canceled", "context_deadline", "", kafka.StageClose)
	if client.closeCalls != 0 || producerStateOf(producer) != producerStateClosing {
		t.Fatalf("after deadline close calls/state = %d/%v", client.closeCalls, producerStateOf(producer))
	}
	if err := producer.Close(context.Background()); err != nil {
		t.Fatalf("later Close() error = %v", err)
	}
	if flushCalls.Load() != 2 || client.closeCalls != 1 || producerStateOf(producer) != producerStateClosed {
		t.Fatalf("final flush/close/state = %d/%d/%v", flushCalls.Load(), client.closeCalls, producerStateOf(producer))
	}
	if err := producer.Close(context.Background()); err != nil {
		t.Fatalf("idempotent Close() error = %v", err)
	}
	err = producer.Produce(context.Background(), validMessage(t))
	assertFranzError(t, err, ErrProducerState, "franz_producer_state", "producer_closed", "/state", "")
}

func TestProducerCloseAlreadyCanceledNeverFlushesOrCloses(t *testing.T) {
	for range 64 {
		var flushCalls atomic.Int32
		client := &fakeClient{flushFn: func(context.Context) error {
			flushCalls.Add(1)
			return nil
		}}
		producer := newProducer(client)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := producer.Close(ctx)
		assertFranzError(t, err, ErrOperationCanceled, "franz_operation_canceled", "context_canceled", "", kafka.StageClose)
		if flushCalls.Load() != 0 || client.closeCalls != 0 || producerStateOf(producer) != producerStateClosing {
			t.Fatalf("canceled Close flush/close/state = %d/%d/%v", flushCalls.Load(), client.closeCalls, producerStateOf(producer))
		}
	}
}

func TestProducerCloseProjectsFlushFailureSafelyAndCanRetry(t *testing.T) {
	secret := errors.New("flush broker secret")
	var calls int
	client := &fakeClient{flushFn: func(context.Context) error {
		calls++
		if calls == 1 {
			return secret
		}
		return nil
	}}
	producer := newProducer(client)
	err := producer.Close(context.Background())
	assertFranzError(t, err, ErrFlushFailed, "franz_flush_failed", "flush_failed", "", kafka.StageClose)
	if errors.Is(err, secret) || errors.Unwrap(err) != nil || err.Error() == secret.Error() {
		t.Fatalf("Close() exposed flush cause: %v", err)
	}
	if client.closeCalls != 0 || producerStateOf(producer) != producerStateClosing {
		t.Fatalf("failed flush close/state = %d/%v", client.closeCalls, producerStateOf(producer))
	}
	if err := producer.Close(context.Background()); err != nil {
		t.Fatalf("retry Close() error = %v", err)
	}
	if calls != 2 || client.closeCalls != 1 {
		t.Fatalf("retry flush/close calls = %d/%d", calls, client.closeCalls)
	}
}

func TestProducerConcurrentCloseFlushesAndClosesOnce(t *testing.T) {
	var flushCalls atomic.Int32
	client := &fakeClient{flushFn: func(context.Context) error {
		flushCalls.Add(1)
		time.Sleep(5 * time.Millisecond)
		return nil
	}}
	producer := newProducer(client)
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- producer.Close(context.Background())
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
	if flushCalls.Load() != 1 || client.closeCalls != 1 {
		t.Fatalf("flush/close calls = %d/%d", flushCalls.Load(), client.closeCalls)
	}
}

func validMessage(t *testing.T) kafka.Message {
	t.Helper()
	message, err := kafka.NewMessage(kafka.MessageSpec{
		Topic: "events", Key: []byte("key"), Value: []byte("value"),
		Headers: []kafka.Header{{Key: "trace", Value: nil}, {Key: "trace", Value: []byte("second")}},
	})
	if err != nil {
		t.Fatalf("kafka.NewMessage() error = %v", err)
	}
	return message
}

func producerStateOf(producer *Producer) producerState {
	producer.mu.Lock()
	defer producer.mu.Unlock()
	return producer.state
}
