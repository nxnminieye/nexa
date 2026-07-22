package franz

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestReaderFactoryValidatesWithoutOpeningClient(t *testing.T) {
	tests := []struct {
		name    string
		options ReaderFactoryOptions
		reason  string
		pointer string
	}{
		{name: "empty brokers", options: ReaderFactoryOptions{MaxPollRecords: 1}, reason: "brokers_empty", pointer: "/brokers"},
		{name: "invalid broker", options: ReaderFactoryOptions{Brokers: []string{"broker one"}, MaxPollRecords: 1}, reason: "broker_invalid", pointer: "/brokers/0"},
		{name: "duplicate broker", options: ReaderFactoryOptions{Brokers: []string{"a:1", "a:1"}, MaxPollRecords: 1}, reason: "broker_duplicate", pointer: "/brokers/1"},
		{name: "invalid max", options: ReaderFactoryOptions{Brokers: []string{"a:1"}}, reason: "max_poll_records_invalid", pointer: "/maxPollRecords"},
		{name: "nil option", options: ReaderFactoryOptions{Brokers: []string{"a:1"}, MaxPollRecords: 1, ClientOptions: []kgo.Opt{nil}}, reason: "client_option_nil", pointer: "/clientOptions/0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factory := &fakeClientFactory{}
			_, err := newReaderFactoryWithFactory(test.options, factory)
			assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", test.reason, test.pointer, "")
			if factory.calls != 0 {
				t.Fatalf("client opens = %d, want 0", factory.calls)
			}
		})
	}
}

func TestReaderFactoryOpenUsesExplicitFrozenFacts(t *testing.T) {
	client := &fakeClient{}
	factory := &fakeClientFactory{client: client}
	brokers := []string{"one:9092", "two:9092"}
	options := []kgo.Opt{kgo.ClientID("caller")}
	readerFactory, err := newReaderFactoryWithFactory(ReaderFactoryOptions{
		Brokers: brokers, MaxPollRecords: 7, ClientOptions: options,
	}, factory)
	if err != nil {
		t.Fatalf("newReaderFactoryWithFactory() error = %v", err)
	}
	brokers[0] = "mutated"
	options[0] = kgo.ClientID("mutated")

	subscription := validSubscription(t)
	opened, err := readerFactory.Open(context.Background(), subscription)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if factory.calls != 1 || opened == nil {
		t.Fatalf("Open() calls/reader = %d/%v", factory.calls, opened)
	}
	configured, err := kgo.NewClient(factory.options...)
	if err != nil {
		t.Fatalf("kgo.NewClient(captured options) error = %v", err)
	}
	defer configured.Close()
	if got := configured.OptValue(kgo.SeedBrokers); !reflect.DeepEqual(got, []string{"one:9092", "two:9092"}) {
		t.Fatalf("brokers = %#v", got)
	}
	if got := configured.OptValue(kgo.ClientID); got != "caller" {
		t.Fatalf("client id = %#v", got)
	}
	if got := configured.OptValue(kgo.ConsumerGroup); got != subscription.Group() {
		t.Fatalf("group = %#v", got)
	}
	gotTopics, ok := configured.OptValue("ConsumeTopics").(map[string]*regexp.Regexp)
	if !ok || len(gotTopics) != 2 || gotTopics["orders"] != nil || gotTopics["events"] != nil {
		t.Fatalf("topics = %#v", gotTopics)
	}

	client.polls = []kgo.Fetches{fetches(record("orders", 0, 1))}
	if _, err := opened.Poll(context.Background()); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if client.maxPollRecords != 7 {
		t.Fatalf("PollRecords max = %d, want 7", client.maxPollRecords)
	}
}

func TestReaderFactoryOpenProjectsContextAndClientFailure(t *testing.T) {
	failedClient := &fakeClient{}
	factory := &fakeClientFactory{client: failedClient, openErr: errors.New("sasl=secret")}
	readerFactory, err := newReaderFactoryWithFactory(ReaderFactoryOptions{Brokers: []string{"one:9092"}, MaxPollRecords: 1}, factory)
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}
	_, err = readerFactory.Open(nil, validSubscription(t))
	assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", "context_nil", "/context", "")
	_, err = readerFactory.Open(context.Background(), kafka.Subscription{})
	assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", "subscription_invalid", "/subscription", "")
	_, err = readerFactory.Open(context.Background(), validSubscription(t))
	assertFranzError(t, err, ErrOpenFailed, "franz_open_failed", "client_open_failed", "", kafka.StageOpen)
	if err.Error() == "sasl=secret" {
		t.Fatal("Open() exposed client cause")
	}
	if failedClient.closeCalls != 1 {
		t.Fatalf("failed Open() client Close calls = %d, want 1", failedClient.closeCalls)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = readerFactory.Open(canceled, validSubscription(t))
	assertFranzError(t, err, ErrOperationCanceled, "franz_operation_canceled", "context_canceled", "", kafka.StageOpen)
}

func TestReaderFactoryRejectsTypedNilClient(t *testing.T) {
	var typedNil *fakeClient
	clients := &fakeClientFactory{client: typedNil}
	factory, err := newReaderFactoryWithFactory(ReaderFactoryOptions{Brokers: []string{"one:9092"}, MaxPollRecords: 1}, clients)
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}
	opened, err := factory.Open(context.Background(), validSubscription(t))
	if opened != nil {
		t.Fatalf("Open() reader = %T, want nil", opened)
	}
	assertFranzError(t, err, ErrOpenFailed, "franz_open_failed", "client_open_failed", "", kafka.StageOpen)
}

func TestReaderPollAndCommitExactReceipt(t *testing.T) {
	first := record("orders", 0, 1)
	second := record("orders", 0, 2)
	client := &fakeClient{polls: []kgo.Fetches{nil, fetches(first, second)}, commitErrs: []error{errors.New("commit secret"), nil}}
	reader := newReader(client, 10)

	batch, err := reader.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if batch.Len() != 2 || client.pollCalls != 2 {
		t.Fatalf("batch/polls = %d/%d", batch.Len(), client.pollCalls)
	}
	first.Value[0] = 'X'
	if got := batch.Records()[0].Value(); string(got) != "value" {
		t.Fatalf("batch aliases franz record: %q", got)
	}

	_, err = reader.Poll(context.Background())
	assertFranzError(t, err, ErrReaderState, "franz_reader_state", "poll_pending", "/state", kafka.StagePoll)
	err = reader.Commit(context.Background())
	assertFranzError(t, err, ErrCommitFailed, "franz_commit_failed", "commit_failed", "", kafka.StageCommit)
	if !sameRecords(client.commits[0], []*kgo.Record{first, second}) {
		t.Fatal("Commit() did not submit exact polled record pointers")
	}
	err = reader.Commit(context.Background())
	if err != nil {
		t.Fatalf("second Commit() error = %v", err)
	}
	if !sameRecords(client.commits[1], []*kgo.Record{first, second}) {
		t.Fatal("failed Commit() did not retain exact receipt")
	}
	if client.allowRebalanceCalls != 2 {
		t.Fatalf("AllowRebalance calls = %d, want one after empty poll and one after successful commit", client.allowRebalanceCalls)
	}
	err = reader.Commit(context.Background())
	assertFranzError(t, err, ErrReaderState, "franz_reader_state", "commit_without_poll", "/state", kafka.StageCommit)
}

func TestReaderCloseAllowsBlockedRebalanceBeforeClosingClient(t *testing.T) {
	client := &fakeClient{polls: []kgo.Fetches{fetches(record("orders", 0, 1))}}
	reader := newReader(client, 1)
	if _, err := reader.Poll(context.Background()); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if client.allowRebalanceCalls != 1 || !client.closeSawAllowedRebalance {
		t.Fatalf("allow/close ordering = %d/%v", client.allowRebalanceCalls, client.closeSawAllowedRebalance)
	}
}

func TestReaderFetchErrorWinsAndCancellationIsExact(t *testing.T) {
	secret := errors.New("broker secret")
	withError := fetches(record("orders", 0, 1))
	withError[0].Topics[0].Partitions[0].Err = secret
	reader := newReader(&fakeClient{polls: []kgo.Fetches{withError}}, 1)
	batch, err := reader.Poll(context.Background())
	if batch.Valid() {
		t.Fatal("Poll() returned batch with fetch error")
	}
	assertFranzError(t, err, ErrFetchFailed, "franz_fetch_failed", "fetch_failed", "", kafka.StagePoll)
	if errors.Is(err, secret) || err.Error() == secret.Error() {
		t.Fatal("Poll() exposed fetch cause")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	reader = newReader(&fakeClient{polls: []kgo.Fetches{fetchError(context.Canceled)}}, 1)
	_, err = reader.Poll(canceled)
	assertFranzError(t, err, ErrOperationCanceled, "franz_operation_canceled", "context_canceled", "", kafka.StagePoll)
	if !errors.Is(err, context.Canceled) || !errors.Is(errors.Unwrap(err), context.Canceled) {
		t.Fatalf("Poll cancellation unwrap = %v", errors.Unwrap(err))
	}
}

func TestReaderCloseUnblocksPollAndIsIdempotent(t *testing.T) {
	client := newBlockingFakeClient()
	reader := newReader(client, 1)
	pollDone := make(chan error, 1)
	go func() {
		_, err := reader.Poll(context.Background())
		pollDone <- err
	}()
	<-client.pollStarted

	if err := reader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	select {
	case err := <-pollDone:
		assertFranzError(t, err, ErrReaderState, "franz_reader_state", "reader_closed", "/state", kafka.StagePoll)
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock Poll()")
	}
	if client.closeCalls != 1 {
		t.Fatalf("client Close calls = %d", client.closeCalls)
	}
	_, err := reader.Poll(context.Background())
	assertFranzError(t, err, ErrReaderState, "franz_reader_state", "reader_closed", "/state", kafka.StagePoll)
	err = reader.Commit(context.Background())
	assertFranzError(t, err, ErrReaderState, "franz_reader_state", "reader_closed", "/state", kafka.StageCommit)
}

func validSubscription(t *testing.T) kafka.Subscription {
	t.Helper()
	subscription, err := kafka.NewSubscription(kafka.SubscriptionSpec{
		ID: "orders", Group: "workers", Topics: []string{"orders", "events"},
		Handler: kafka.HandlerFunc(func(context.Context, kafka.Record) error { return nil }),
	})
	if err != nil {
		t.Fatalf("kafka.NewSubscription() error = %v", err)
	}
	return subscription
}

func record(topic string, partition int32, offset int64) *kgo.Record {
	return &kgo.Record{Topic: topic, Partition: partition, Offset: offset, Timestamp: time.Unix(offset, 0), Key: []byte("key"), Value: []byte("value")}
}

func fetches(records ...*kgo.Record) kgo.Fetches {
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "orders", Partitions: []kgo.FetchPartition{{Partition: 0, Records: records}}}}}}
}

func fetchError(err error) kgo.Fetches {
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "orders", Partitions: []kgo.FetchPartition{{Partition: 0, Err: err}}}}}}
}

func sameRecords(left, right []*kgo.Record) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type fakeClientFactory struct {
	client  client
	openErr error
	calls   int
	options []kgo.Opt
}

func (f *fakeClientFactory) Open(_ context.Context, options []kgo.Opt) (client, error) {
	f.calls++
	f.options = append([]kgo.Opt(nil), options...)
	return f.client, f.openErr
}

type fakeClient struct {
	mu                       sync.Mutex
	polls                    []kgo.Fetches
	pollCalls                int
	maxPollRecords           int
	commitErrs               []error
	commits                  [][]*kgo.Record
	produceFn                func(context.Context, *kgo.Record, func(*kgo.Record, error))
	flushFn                  func(context.Context) error
	closeCalls               int
	allowRebalanceCalls      int
	closeSawAllowedRebalance bool
	pollStarted              chan struct{}
	closed                   chan struct{}
	closeOnce                sync.Once
}

func newBlockingFakeClient() *fakeClient {
	return &fakeClient{pollStarted: make(chan struct{}), closed: make(chan struct{})}
}

func (f *fakeClient) PollRecords(ctx context.Context, max int) kgo.Fetches {
	f.mu.Lock()
	f.pollCalls++
	f.maxPollRecords = max
	if len(f.polls) > 0 {
		result := f.polls[0]
		f.polls = f.polls[1:]
		f.mu.Unlock()
		return result
	}
	started, closed := f.pollStarted, f.closed
	f.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	select {
	case <-ctx.Done():
		return fetchError(ctx.Err())
	case <-closed:
		return fetchError(kgo.ErrClientClosed)
	}
}

func (f *fakeClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits = append(f.commits, append([]*kgo.Record(nil), records...))
	if len(f.commitErrs) == 0 {
		return nil
	}
	err := f.commitErrs[0]
	f.commitErrs = f.commitErrs[1:]
	return err
}

func (f *fakeClient) Produce(ctx context.Context, record *kgo.Record, callback func(*kgo.Record, error)) {
	if f.produceFn != nil {
		f.produceFn(ctx, record, callback)
		return
	}
	callback(record, nil)
}

func (f *fakeClient) Flush(ctx context.Context) error {
	if f.flushFn != nil {
		return f.flushFn(ctx)
	}
	return nil
}

func (f *fakeClient) Close() {
	f.mu.Lock()
	f.closeCalls++
	f.closeSawAllowedRebalance = f.allowRebalanceCalls > 0
	closed := f.closed
	f.mu.Unlock()
	if closed != nil {
		f.closeOnce.Do(func() { close(closed) })
	}
}

func (f *fakeClient) AllowRebalance() {
	f.mu.Lock()
	f.allowRebalanceCalls++
	f.mu.Unlock()
}
