package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type countingHandler struct {
	calls  int
	record Record
}

func (h *countingHandler) Handle(_ context.Context, record Record) error {
	h.calls++
	h.record = record
	return nil
}

func TestSubscriptionImmutableValuesAndHandlerFuncDelegation(t *testing.T) {
	record, err := NewRecord(RecordSpec{Topic: "sample", Offset: 7})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), subscriptionContextKey{}, "context-value")
	var calls int
	var receivedContext context.Context
	var receivedRecord Record
	handler := HandlerFunc(func(callContext context.Context, callRecord Record) error {
		calls++
		receivedContext = callContext
		receivedRecord = callRecord
		return errors.New("handler-result")
	})
	topics := []string{"first", "second"}
	subscription, err := NewSubscription(SubscriptionSpec{ID: "sample.worker-1", Group: "sample-group", Topics: topics, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	if !subscription.Valid() || (Subscription{}).Valid() || subscription.ID() != "sample.worker-1" || subscription.Group() != "sample-group" {
		t.Fatalf("subscription values = %t/%t/%q/%q", subscription.Valid(), (Subscription{}).Valid(), subscription.ID(), subscription.Group())
	}
	topics[0] = "changed"
	if got := subscription.Topics(); got[0] != "first" || got[1] != "second" {
		t.Fatalf("topics = %v", got)
	}
	readTopics := subscription.Topics()
	readTopics[1] = "changed"
	if subscription.Topics()[1] != "second" {
		t.Fatal("topics accessor exposed mutable state")
	}

	returned := subscription.Handler().Handle(ctx, record)
	if returned == nil || returned.Error() != "handler-result" || calls != 1 || receivedContext != ctx || receivedRecord.Topic() != "sample" || receivedRecord.Offset() != 7 {
		t.Fatalf("handler delegation = err %v calls %d record %#v", returned, calls, receivedRecord)
	}
}

type subscriptionContextKey struct{}

func TestSubscriptionValidationOrderAndPointers(t *testing.T) {
	validHandler := HandlerFunc(func(context.Context, Record) error { return nil })
	vectors := []struct {
		name    string
		spec    SubscriptionSpec
		reason  string
		pointer string
	}{
		{name: "id first", spec: SubscriptionSpec{Group: "", Handler: nil}, reason: "subscription_id_invalid", pointer: "/id"},
		{name: "group second", spec: SubscriptionSpec{ID: "sample", Handler: nil}, reason: "group_invalid", pointer: "/group"},
		{name: "topics empty", spec: SubscriptionSpec{ID: "sample", Group: "group", Handler: nil}, reason: "topics_empty", pointer: "/topics"},
		{name: "topic validity before duplicates", spec: SubscriptionSpec{ID: "sample", Group: "group", Topics: []string{"same", "same", ""}, Handler: nil}, reason: "topic_invalid", pointer: "/topics/2"},
		{name: "duplicate second index", spec: SubscriptionSpec{ID: "sample", Group: "group", Topics: []string{"same", "other", "same"}, Handler: nil}, reason: "topic_duplicate", pointer: "/topics/2"},
		{name: "handler last", spec: SubscriptionSpec{ID: "sample", Group: "group", Topics: []string{"topic"}}, reason: "handler_nil", pointer: "/handler"},
		{name: "nil HandlerFunc", spec: SubscriptionSpec{ID: "sample", Group: "group", Topics: []string{"topic"}, Handler: HandlerFunc(nil)}, reason: "handler_nil", pointer: "/handler"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			subscription, err := NewSubscription(vector.spec)
			if subscription.Valid() {
				t.Fatal("failed constructor returned valid subscription")
			}
			requireConfigurationError(t, err, vector.reason, vector.pointer)
		})
	}

	var typedNil *countingHandler
	_, err := NewSubscription(SubscriptionSpec{ID: "sample", Group: "group", Topics: []string{"topic"}, Handler: typedNil})
	requireConfigurationError(t, err, "handler_nil", "/handler")

	constructed, err := NewSubscription(SubscriptionSpec{ID: "sample", Group: "group", Topics: []string{"topic"}, Handler: validHandler})
	if err != nil || !constructed.Valid() {
		t.Fatalf("valid subscription = %#v, %v", constructed, err)
	}
}

func TestSubscriptionIdentifierBoundaries(t *testing.T) {
	handler := HandlerFunc(func(context.Context, Record) error { return nil })
	validIDs := []string{
		"a",
		"a0",
		"sample.worker-1_v2",
		"a" + strings.Repeat("b", 127),
	}
	for _, id := range validIDs {
		if _, err := NewSubscription(SubscriptionSpec{ID: id, Group: "group", Topics: []string{"topic"}, Handler: handler}); err != nil {
			t.Fatalf("valid id %q rejected: %v", id, err)
		}
	}
	invalidIDs := []string{
		"",
		"A",
		"1sample",
		"sample.",
		"sample-",
		"sample_",
		"sample..worker",
		"sample--worker",
		"sample__worker",
		"sample.Worker",
		"sämple",
		"sample/worker",
		"a" + strings.Repeat("b", 128),
	}
	for _, id := range invalidIDs {
		_, err := NewSubscription(SubscriptionSpec{ID: id, Group: "group", Topics: []string{"topic"}, Handler: handler})
		requireConfigurationError(t, err, "subscription_id_invalid", "/id")
	}
}

func TestSubscriptionGroupBoundaries(t *testing.T) {
	handler := HandlerFunc(func(context.Context, Record) error { return nil })
	validGroups := []string{"g", strings.Repeat("g", 255), strings.Repeat("é", 127) + "a"}
	for _, group := range validGroups {
		if _, err := NewSubscription(SubscriptionSpec{ID: "sample", Group: group, Topics: []string{"topic"}, Handler: handler}); err != nil {
			t.Fatalf("valid group length %d rejected: %v", len(group), err)
		}
	}
	invalidGroups := []string{"", strings.Repeat("g", 256), strings.Repeat("é", 128), "has\ncontrol", "has\u0085control", string([]byte{0xff})}
	for _, group := range invalidGroups {
		_, err := NewSubscription(SubscriptionSpec{ID: "sample", Group: group, Topics: []string{"topic"}, Handler: handler})
		requireConfigurationError(t, err, "group_invalid", "/group")
	}
}

type portReader struct {
	batch       Batch
	pollCalls   int
	commitCalls int
	closeCalls  int
}

func (r *portReader) Poll(context.Context) (Batch, error) {
	r.pollCalls++
	return r.batch, nil
}

func (r *portReader) Commit(context.Context) error {
	r.commitCalls++
	return nil
}

func (r *portReader) Close() error {
	r.closeCalls++
	return nil
}

type portFactory struct {
	reader       Reader
	openCalls    int
	subscription Subscription
}

func (f *portFactory) Open(_ context.Context, subscription Subscription) (Reader, error) {
	f.openCalls++
	f.subscription = subscription
	return f.reader, nil
}

type portProducer struct {
	produceCalls int
	closeCalls   int
	message      Message
}

func (p *portProducer) Produce(_ context.Context, message Message) error {
	p.produceCalls++
	p.message = message
	return nil
}

func (p *portProducer) Close(context.Context) error {
	p.closeCalls++
	return nil
}

func TestPortsCarryValidatedValuesThroughConsumerImplementations(t *testing.T) {
	record, err := NewRecord(RecordSpec{Topic: "sample"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewBatch([]Record{record})
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := NewSubscription(SubscriptionSpec{
		ID: "sample", Group: "group", Topics: []string{"sample"},
		Handler: HandlerFunc(func(context.Context, Record) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := NewMessage(MessageSpec{Topic: "sample"})
	if err != nil {
		t.Fatal(err)
	}

	readerImpl := &portReader{batch: batch}
	factoryImpl := &portFactory{reader: readerImpl}
	var factory ReaderFactory = factoryImpl
	reader, err := factory.Open(context.Background(), subscription)
	if err != nil {
		t.Fatal(err)
	}
	polled, err := reader.Poll(context.Background())
	if err != nil || !polled.Valid() || polled.Len() != 1 {
		t.Fatalf("Poll() = %#v, %v", polled, err)
	}
	if err := reader.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if factoryImpl.openCalls != 1 || factoryImpl.subscription.ID() != "sample" || readerImpl.pollCalls != 1 || readerImpl.commitCalls != 1 || readerImpl.closeCalls != 1 {
		t.Fatalf("reader port calls = open %d poll %d commit %d close %d", factoryImpl.openCalls, readerImpl.pollCalls, readerImpl.commitCalls, readerImpl.closeCalls)
	}

	producerImpl := &portProducer{}
	var producer Producer = producerImpl
	if err := producer.Produce(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := producer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if producerImpl.produceCalls != 1 || producerImpl.closeCalls != 1 || producerImpl.message.Topic() != "sample" {
		t.Fatalf("producer calls = produce %d close %d", producerImpl.produceCalls, producerImpl.closeCalls)
	}
}

var (
	_ Handler       = HandlerFunc(nil)
	_ Reader        = (*portReader)(nil)
	_ ReaderFactory = (*portFactory)(nil)
	_ Producer      = (*portProducer)(nil)
)
