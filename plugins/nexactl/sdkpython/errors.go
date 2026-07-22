package sdkpython

import (
	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/internal/sdkpythonassets"
)

const errorDomain = "nexa.sdkpythonassets"

func inputError(member invocationMember, state string) error {
	detail := sdkpythonassets.DetailDocument{
		APIVersion: "nexa.dev/sdk-python-assets-error-detail/v1",
		Pointer:    member.pointer,
		Reason:     member.reason,
	}
	_ = state
	projected, err := protocol.NewErrorWithDetailsOptions(
		"sdk_python_assets_input_invalid",
		errorDomain,
		protocol.CategoryInput,
		"Python SDK asset input is invalid",
		"",
		detail,
		protocol.ErrorOptions{},
	)
	if err != nil {
		return protocol.NewError(
			"sdk_python_assets_internal",
			errorDomain,
			protocol.CategoryInternal,
			"Python SDK asset operation failed",
			"",
		)
	}
	return projected
}
