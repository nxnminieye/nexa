package transaction_test

import (
	"context"
	"testing"

	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

func buildTransactionPlan(t *testing.T, repository string, request transaction.PlanRequest, contents ...[]byte) (transaction.Plan, error) {
	t.Helper()
	for index := range request.Expected {
		if len(contents) == 0 {
			t.Fatal("candidate content is required")
		}
		content := contents[0]
		if len(contents) == len(request.Expected) {
			content = contents[index]
		}
		request.Expected[index].Digest = provenance.SHA256(content)
	}
	if request.RevalidateSources == nil {
		sources := append([]provenance.Source(nil), request.Sources...)
		request.RevalidateSources = func(context.Context) ([]provenance.Source, error) {
			return append([]provenance.Source(nil), sources...), nil
		}
	}
	return transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		emitted := map[string]bool{}
		for index, input := range request.Expected {
			if emitted[input.Path] {
				continue
			}
			content := contents[0]
			if len(contents) == len(request.Expected) {
				content = contents[index]
			}
			if err := emit(input.Path, content); err != nil {
				return transaction.PlanRequest{}, err
			}
			emitted[input.Path] = true
		}
		return request, nil
	})
}
