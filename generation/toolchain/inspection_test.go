package toolchain

import (
	"context"
	"errors"
	"testing"
)

func TestEntityInputInspectionClosesZeroAndInvalidProductionInput(t *testing.T) {
	var zero EntityInputInspection
	_, err := zero.ModuleGraphDigest()
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization")
	_, err = zero.BuildInputDigest()
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization")
	_, err = zero.ModuleSources()
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization")
	_, err = zero.ExecutableVersion()
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization")

	_, err = InspectEntityInputs(context.Background(), EntityInputInspectionSpec{})
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("InspectEntityInputs() error = %#v", err)
	}
}
