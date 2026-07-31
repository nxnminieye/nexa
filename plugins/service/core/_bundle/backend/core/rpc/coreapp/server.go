package coreapp

import (
	"context"
	"database/sql"
)

// ServerAdapter is the consumer-generated transport boundary. The adapter
// owns the wire server and delegates every method to the source-owned RPC
// service.
type ServerAdapter interface {
	Serve(context.Context, *RPCService) error
}

func Serve(ctx context.Context, database *sql.DB, config Config, clock Clock, adapter ServerAdapter) error {
	if adapter == nil {
		return invalid("rpc-server.adapter")
	}
	service, err := NewServiceContextFromConfig(database, config, clock)
	if err != nil {
		return err
	}
	return adapter.Serve(ctx, service.RPC)
}
