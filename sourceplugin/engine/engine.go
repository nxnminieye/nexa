package engine

import (
	"context"

	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

type Options struct {
	Resolver    *release.ExactResolver
	CacheLimits release.CacheLimits
	TreeLimits  sourceplugin.TreeLimits
	LockLimits  lock.Limits
	MergeDriver MergeDriver
	Executor    Executor
	GoToolchain GoToolchain
}

type Engine struct {
	resolver    *release.ExactResolver
	treeLimits  sourceplugin.TreeLimits
	lockLimits  lock.Limits
	mergeDriver MergeDriver
	executor    Executor
	goToolchain GoToolchain
}

func New(options Options) (*Engine, error) {
	if options.Resolver == nil {
		return nil, newError(ErrInput, "source_engine_invalid", "resolver_required", "/resolver", "new")
	}
	cache, cached := options.Resolver.Cache()
	if cached {
		if !options.CacheLimits.Equal(cache.Limits()) {
			return nil, newError(ErrInput, "source_engine_invalid", "cache_limits_mismatch", "/cacheLimits", "new")
		}
		if options.TreeLimits != options.CacheLimits.Tree {
			return nil, newError(ErrInput, "source_engine_invalid", "tree_limits_mismatch", "/treeLimits", "new")
		}
	} else if options.CacheLimits != (release.CacheLimits{}) {
		return nil, newError(ErrInput, "source_engine_invalid", "cache_limits_without_cache", "/cacheLimits", "new")
	}
	if options.TreeLimits.MaxFiles <= 0 || options.TreeLimits.MaxFileBytes < 0 || options.TreeLimits.MaxTotalBytes < 0 {
		return nil, newError(ErrInput, "source_engine_invalid", "tree_limits_invalid", "/treeLimits", "new")
	}
	if options.LockLimits.MaxDocumentBytes <= 0 || options.LockLimits.MaxProfileClosure <= 0 ||
		options.LockLimits.MaxTrackedFiles <= 0 || options.LockLimits.MaxTargetBytes <= 0 {
		return nil, newError(ErrInput, "source_engine_invalid", "lock_limits_invalid", "/lockLimits", "new")
	}
	if options.MergeDriver == nil {
		return nil, newError(ErrInput, "source_engine_invalid", "merge_driver_required", "/mergeDriver", "new")
	}
	return &Engine{
		resolver: options.Resolver, treeLimits: options.TreeLimits, lockLimits: options.LockLimits,
		mergeDriver: options.MergeDriver, executor: options.Executor, goToolchain: options.GoToolchain,
	}, nil
}

func validateContext(ctx context.Context, stage string) error {
	if ctx == nil {
		return newError(ErrInput, "source_request_invalid", "context_required", "/context", stage)
	}
	if err := ctx.Err(); err != nil {
		return newErrorWithCause(ErrCanceled, "operation_canceled", "context_canceled", "", stage, err)
	}
	return nil
}
