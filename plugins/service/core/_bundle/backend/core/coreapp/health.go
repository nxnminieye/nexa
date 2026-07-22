package coreapp

import "context"

type Health struct {
	Ready bool
}

func CheckHealth(ctx context.Context) (Health, error) {
	if err := ctx.Err(); err != nil {
		return Health{}, canceled("health.check", err)
	}
	return Health{Ready: true}, nil
}
