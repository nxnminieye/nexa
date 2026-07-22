package franz

import (
	"context"
	"sync"

	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

type producerState uint8

const (
	producerStateOpen producerState = iota
	producerStateClosing
	producerStateClosed
)

// Producer adapts one franz client to the transport-neutral producer port.
type Producer struct {
	client client

	mu           sync.Mutex
	state        producerState
	active       int
	drained      chan struct{}
	flushRunning bool
	flushDone    chan struct{}
}

func newProducer(opened client) *Producer {
	return &Producer{client: opened, state: producerStateOpen}
}

// Produce performs one franz delivery invocation and waits for its callback.
func (p *Producer) Produce(ctx context.Context, message kafka.Message) error {
	if ctx == nil {
		return adapterError(errorKindContextNil, "/context", "", nil)
	}
	record, err := messageToKGO(message)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return operationError(err, "", errorKindDeliveryFailed)
	}
	p.mu.Lock()
	if p.state != producerStateOpen {
		p.mu.Unlock()
		return adapterError(errorKindProducerClosed, "/state", "", nil)
	}
	p.active++
	p.mu.Unlock()
	defer p.finishProduce()

	delivery := make(chan error, 1)
	var callbackOnce sync.Once
	p.client.Produce(ctx, record, func(_ *kgo.Record, err error) {
		callbackOnce.Do(func() { delivery <- err })
	})
	select {
	case err := <-delivery:
		if err != nil {
			return operationError(err, "", errorKindDeliveryFailed)
		}
		return nil
	case <-ctx.Done():
		return operationError(ctx.Err(), "", errorKindDeliveryFailed)
	}
}

// Close blocks new delivery, drains active calls, flushes, and closes once.
func (p *Producer) Close(ctx context.Context) error {
	if ctx == nil {
		return adapterError(errorKindContextNil, "/context", "", nil)
	}
	p.mu.Lock()
	if p.state == producerStateClosed {
		p.mu.Unlock()
		return nil
	}
	if p.state == producerStateOpen {
		p.state = producerStateClosing
		p.drained = make(chan struct{})
		if p.active == 0 {
			close(p.drained)
		}
	}
	drained := p.drained
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return operationError(err, kafka.StageClose, errorKindFlushFailed)
	}
	p.mu.Unlock()

	select {
	case <-drained:
	case <-ctx.Done():
		return operationError(ctx.Err(), kafka.StageClose, errorKindFlushFailed)
	}

	for {
		p.mu.Lock()
		if p.state == producerStateClosed {
			p.mu.Unlock()
			return nil
		}
		if p.flushRunning {
			done := p.flushDone
			p.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return operationError(ctx.Err(), kafka.StageClose, errorKindFlushFailed)
			}
		}
		p.flushRunning = true
		p.flushDone = make(chan struct{})
		done := p.flushDone
		p.mu.Unlock()

		err := p.client.Flush(ctx)
		p.mu.Lock()
		p.flushRunning = false
		close(done)
		if err == nil {
			p.client.Close()
			p.state = producerStateClosed
		}
		p.mu.Unlock()
		if err != nil {
			return operationError(err, kafka.StageClose, errorKindFlushFailed)
		}
		return nil
	}
}

func (p *Producer) finishProduce() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
	if p.state == producerStateClosing && p.active == 0 {
		close(p.drained)
	}
}
