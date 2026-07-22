package franz

import (
	"context"
	"fmt"
	"reflect"

	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

type client interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
	AllowRebalance()
	Produce(context.Context, *kgo.Record, func(*kgo.Record, error))
	Flush(context.Context) error
	Close()
}

type clientFactory interface {
	Open(context.Context, []kgo.Opt) (client, error)
}

type realClientFactory struct{}

func (realClientFactory) Open(_ context.Context, options []kgo.Opt) (client, error) {
	return kgo.NewClient(options...)
}

// ReaderFactoryOptions contains the closest consumer-owned franz settings.
type ReaderFactoryOptions struct {
	Brokers        []string
	MaxPollRecords int
	ClientOptions  []kgo.Opt
}

type readerFactory struct {
	brokers        []string
	maxPollRecords int
	clientOptions  []kgo.Opt
	clients        clientFactory
}

// NewReaderFactory validates and freezes franz settings without opening a client.
func NewReaderFactory(options ReaderFactoryOptions) (kafka.ReaderFactory, error) {
	return newReaderFactoryWithFactory(options, realClientFactory{})
}

func newReaderFactoryWithFactory(options ReaderFactoryOptions, clients clientFactory) (*readerFactory, error) {
	brokers, clientOptions, err := validateFactoryOptions(options.Brokers, options.ClientOptions)
	if err != nil {
		return nil, err
	}
	if options.MaxPollRecords < 1 {
		return nil, adapterError(errorKindMaxPollRecordsInvalid, "/maxPollRecords", "", nil)
	}
	return &readerFactory{
		brokers: brokers, maxPollRecords: options.MaxPollRecords, clientOptions: clientOptions, clients: clients,
	}, nil
}

func (f *readerFactory) Open(ctx context.Context, subscription kafka.Subscription) (kafka.Reader, error) {
	if ctx == nil {
		return nil, adapterError(errorKindContextNil, "/context", "", nil)
	}
	if !subscription.Valid() {
		return nil, adapterError(errorKindSubscriptionInvalid, "/subscription", "", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, operationError(err, kafka.StageOpen, errorKindClientOpenFailed)
	}
	options := make([]kgo.Opt, 0, len(f.clientOptions)+4)
	options = append(options, f.clientOptions...)
	options = append(options,
		kgo.SeedBrokers(append([]string(nil), f.brokers...)...),
		kgo.ConsumerGroup(subscription.Group()),
		kgo.ConsumeTopics(subscription.Topics()...),
		kgo.DisableAutoCommit(),
	)
	opened, err := f.clients.Open(ctx, options)
	if err != nil {
		if !clientNil(opened) {
			opened.Close()
		}
		return nil, operationError(err, kafka.StageOpen, errorKindClientOpenFailed)
	}
	if clientNil(opened) {
		return nil, adapterError(errorKindClientOpenFailed, "", "", nil)
	}
	if err := ctx.Err(); err != nil {
		opened.Close()
		return nil, operationError(err, kafka.StageOpen, errorKindClientOpenFailed)
	}
	return newReader(opened, f.maxPollRecords), nil
}

func clientNil(opened client) bool {
	if opened == nil {
		return true
	}
	value := reflect.ValueOf(opened)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func validateFactoryOptions(brokers []string, options []kgo.Opt) ([]string, []kgo.Opt, error) {
	if len(brokers) == 0 {
		return nil, nil, adapterError(errorKindBrokersEmpty, "/brokers", "", nil)
	}
	seen := make(map[string]struct{}, len(brokers))
	for index, broker := range brokers {
		if !validBroker(broker) {
			return nil, nil, adapterError(errorKindBrokerInvalid, fmt.Sprintf("/brokers/%d", index), "", nil)
		}
		if _, exists := seen[broker]; exists {
			return nil, nil, adapterError(errorKindBrokerDuplicate, fmt.Sprintf("/brokers/%d", index), "", nil)
		}
		seen[broker] = struct{}{}
	}
	for index, option := range options {
		if option == nil {
			return nil, nil, adapterError(errorKindClientOptionNil, fmt.Sprintf("/clientOptions/%d", index), "", nil)
		}
	}
	return append([]string(nil), brokers...), append([]kgo.Opt(nil), options...), nil
}

func validBroker(broker string) bool {
	if len(broker) < 1 || len(broker) > 1024 {
		return false
	}
	for index := range len(broker) {
		if broker[index] < 0x21 || broker[index] > 0x7e {
			return false
		}
	}
	return true
}
