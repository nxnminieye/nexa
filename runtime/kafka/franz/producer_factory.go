package franz

import (
	"context"

	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ProducerFactoryOptions contains the closest producer-owned franz settings.
type ProducerFactoryOptions struct {
	Brokers       []string
	ClientOptions []kgo.Opt
}

// ProducerFactory freezes client settings and opens independently owned producers.
type ProducerFactory struct {
	brokers       []string
	clientOptions []kgo.Opt
	clients       clientFactory
}

// NewProducerFactory validates and freezes franz settings without opening a client.
func NewProducerFactory(options ProducerFactoryOptions) (*ProducerFactory, error) {
	return newProducerFactoryWithFactory(options, realClientFactory{})
}

func newProducerFactoryWithFactory(options ProducerFactoryOptions, clients clientFactory) (*ProducerFactory, error) {
	brokers, clientOptions, err := validateFactoryOptions(options.Brokers, options.ClientOptions)
	if err != nil {
		return nil, err
	}
	return &ProducerFactory{brokers: brokers, clientOptions: clientOptions, clients: clients}, nil
}

// Open creates one producer. Client lifecycle begins at this method boundary.
func (f *ProducerFactory) Open(ctx context.Context) (kafka.Producer, error) {
	if ctx == nil {
		return nil, adapterError(errorKindContextNil, "/context", "", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, operationError(err, kafka.StageOpen, errorKindClientOpenFailed)
	}
	options := make([]kgo.Opt, 0, len(f.clientOptions)+1)
	options = append(options, f.clientOptions...)
	options = append(options, kgo.SeedBrokers(append([]string(nil), f.brokers...)...))
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
	return newProducer(opened), nil
}
