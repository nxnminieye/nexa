package kafka

import "context"

// Handler consumes one immutable record. Handle calls are sequential within a
// subscription and may overlap across subscriptions. The operation-scoped
// context is not retained; Handle honors cancellation and eventually returns.
type Handler interface {
	Handle(context.Context, Record) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, Record) error

func (fn HandlerFunc) Handle(ctx context.Context, record Record) error {
	return fn(ctx, record)
}

// Reader owns broker polling and one private outstanding commit receipt. Poll
// and Commit calls are sequential for one subscription and use operation-scoped
// contexts that are not retained, honor cancellation and eventually return.
// Close is safe concurrently with Poll and Commit, actively unblocks Poll, and
// eventually returns.
type Reader interface {
	Poll(context.Context) (Batch, error)
	Commit(context.Context) error
	Close() error
}

// ReaderFactory opens a Reader for a validated subscription. Open uses an
// operation-scoped context that is not retained, honors cancellation and
// eventually returns. A nonnil error leaves every co-returned resource owned by
// the factory. Only a nil error with a nonnil Reader transfers ownership.
type ReaderFactory interface {
	Open(context.Context, Subscription) (Reader, error)
}

// Producer publishes immutable messages and owns its close lifecycle.
type Producer interface {
	Produce(context.Context, Message) error
	Close(context.Context) error
}
