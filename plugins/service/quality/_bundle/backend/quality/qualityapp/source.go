// Package qualityapp serves immutable Quality read-model snapshots.
package qualityapp

import (
	"context"

	"github.com/nxnminieye/nexa/quality/readmodel"
)

// ProjectionSource loads the current consumer-owned Quality projection.
type ProjectionSource interface {
	Load(context.Context) (readmodel.Snapshot, error)
}
