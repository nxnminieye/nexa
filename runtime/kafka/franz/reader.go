package franz

import (
	"context"
	"sync"

	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

type reader struct {
	client         client
	maxPollRecords int
	operation      chan struct{}

	mu          sync.Mutex
	closed      bool
	outstanding []*kgo.Record
	closeOnce   sync.Once
}

func newReader(opened client, maxPollRecords int) *reader {
	operation := make(chan struct{}, 1)
	operation <- struct{}{}
	return &reader{client: opened, maxPollRecords: maxPollRecords, operation: operation}
}

func (r *reader) Poll(ctx context.Context) (kafka.Batch, error) {
	if ctx == nil {
		return kafka.Batch{}, adapterError(errorKindContextNil, "/context", "", nil)
	}
	if err := r.enter(ctx, kafka.StagePoll); err != nil {
		return kafka.Batch{}, err
	}
	defer r.leave()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return kafka.Batch{}, adapterError(errorKindReaderClosed, "/state", kafka.StagePoll, nil)
	}
	if len(r.outstanding) != 0 {
		r.mu.Unlock()
		return kafka.Batch{}, adapterError(errorKindPollPending, "/state", "", nil)
	}
	r.mu.Unlock()

	for {
		fetches := r.client.PollRecords(ctx, r.maxPollRecords)
		r.mu.Lock()
		closed := r.closed
		r.mu.Unlock()
		if closed {
			return kafka.Batch{}, adapterError(errorKindReaderClosed, "/state", kafka.StagePoll, nil)
		}
		if err := fetches.Err(); err != nil {
			r.client.AllowRebalance()
			return kafka.Batch{}, operationError(err, kafka.StagePoll, errorKindFetchFailed)
		}
		if err := ctx.Err(); err != nil {
			r.client.AllowRebalance()
			return kafka.Batch{}, operationError(err, kafka.StagePoll, errorKindFetchFailed)
		}
		records := fetches.Records()
		if len(records) == 0 {
			r.client.AllowRebalance()
			continue
		}
		converted := make([]kafka.Record, len(records))
		for index, source := range records {
			record, err := recordFromKGO(source)
			if err != nil {
				r.client.AllowRebalance()
				return kafka.Batch{}, adapterError(errorKindRecordInvalid, "", "", err)
			}
			converted[index] = record
		}
		batch, err := kafka.NewBatch(converted)
		if err != nil {
			r.client.AllowRebalance()
			return kafka.Batch{}, adapterError(errorKindRecordInvalid, "", "", err)
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return kafka.Batch{}, adapterError(errorKindReaderClosed, "/state", kafka.StagePoll, nil)
		}
		r.outstanding = append([]*kgo.Record(nil), records...)
		r.mu.Unlock()
		return batch, nil
	}
}

func (r *reader) Commit(ctx context.Context) error {
	if ctx == nil {
		return adapterError(errorKindContextNil, "/context", "", nil)
	}
	if err := r.enter(ctx, kafka.StageCommit); err != nil {
		return err
	}
	defer r.leave()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return adapterError(errorKindReaderClosed, "/state", kafka.StageCommit, nil)
	}
	if len(r.outstanding) == 0 {
		r.mu.Unlock()
		return adapterError(errorKindCommitWithoutPoll, "/state", "", nil)
	}
	receipt := append([]*kgo.Record(nil), r.outstanding...)
	r.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return operationError(err, kafka.StageCommit, errorKindCommitFailed)
	}
	if err := r.client.CommitRecords(ctx, receipt...); err != nil {
		return operationError(err, kafka.StageCommit, errorKindCommitFailed)
	}
	r.client.AllowRebalance()
	r.mu.Lock()
	if !r.closed {
		r.outstanding = nil
	}
	r.mu.Unlock()
	return nil
}

func (r *reader) Close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.outstanding = nil
		r.mu.Unlock()
		r.client.AllowRebalance()
		r.client.Close()
	})
	return nil
}

func (r *reader) enter(ctx context.Context, stage kafka.Stage) error {
	select {
	case <-ctx.Done():
		return operationError(ctx.Err(), stage, errorKindFetchFailed)
	case <-r.operation:
		return nil
	}
}

func (r *reader) leave() {
	r.operation <- struct{}{}
}
