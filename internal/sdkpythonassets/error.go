package sdkpythonassets

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
)

const (
	ReasonRepoRootInvalid          = "repo_root_invalid"
	ReasonPythonPathInvalid        = "python_path_invalid"
	ReasonPythonContainmentInvalid = "python_containment_invalid"
	ReasonMatrixTargetInvalid      = "matrix_target_invalid"
	ReasonWheelhousePathInvalid    = "wheelhouse_path_invalid"
	ReasonWorkDirInvalid           = "work_dir_invalid"
	ReasonOutPathInvalid           = "out_path_invalid"
	ReasonOperationCanceled        = "operation_canceled"
	ReasonBootstrapProjectionDrift = "bootstrap_projection_drift"
	ReasonBundleIndexMissing       = "bundle_index_missing"
	ReasonBundleIndexDrift         = "bundle_index_drift"
	ReasonBundleRoleSetDrift       = "bundle_role_set_drift"
	ReasonBundleRoleDrift          = "bundle_role_drift"
	ReasonIOFailed                 = "io_failed"
	ReasonPythonMissing            = "python_missing"
	ReasonToolMissing              = "tool_missing"
	ReasonPythonFailed             = "python_failed"
	ReasonWheelBuildFailed         = "wheel_build_failed"
	ReasonWheelInvalid             = "wheel_invalid"
	ReasonRecordInvalid            = "record_invalid"
)

type DetailDocument struct{ APIVersion, Pointer, Reason string }

func (d DetailDocument) ErrorCode() string {
	code, _, _, _ := projectionForReason(d.Reason)
	return code
}
func (d DetailDocument) CanonicalJSON() ([]byte, error) {
	if d.APIVersion != "nexa.dev/sdk-python-assets-error-detail/v1" || !validDetailPointer(d.Pointer) || projectionForReasonKnown(d.Reason) == false {
		return nil, errors.New("detail invalid")
	}
	return jcs.Transform(mustCanonicalValue(map[string]string{"apiVersion": d.APIVersion, "pointer": d.Pointer, "reason": d.Reason}))
}
func validDetailPointer(value string) bool {
	if value == "/repo-root" || value == "/python" || value == "/matrix-target" || value == "/wheelhouse" || value == "/work-dir" || value == "/out" || value == "/context" || value == "/bootstrap" || value == "/bundleIndex" || value == "/roles" || value == "/objects" || value == "/repository" || value == "/tool" || value == "/wheel" || value == "/record" {
		return true
	}
	role := strings.TrimPrefix(value, "/roles/")
	return role != value && role != "" && !strings.ContainsAny(role, "/ ~")
}
func ownerError(reason, pointer, _, _ string) error {
	code, category, message, retryable := projectionForReason(reason)
	detail := DetailDocument{APIVersion: "nexa.dev/sdk-python-assets-error-detail/v1", Pointer: pointer, Reason: reason}
	err, buildErr := protocol.NewErrorWithDetailsOptions(code, "nexa.sdkpythonassets", category, message, "", detail, protocol.ErrorOptions{Retryable: retryable})
	if buildErr != nil {
		return protocol.NewError("sdk_python_assets_internal", "nexa.sdkpythonassets", protocol.CategoryInternal, "Python SDK asset operation failed", "")
	}
	return err
}
func projectionForReasonKnown(reason string) bool {
	switch reason {
	case ReasonRepoRootInvalid, ReasonPythonPathInvalid, ReasonPythonContainmentInvalid, ReasonMatrixTargetInvalid, ReasonWheelhousePathInvalid, ReasonWorkDirInvalid, ReasonOutPathInvalid, ReasonOperationCanceled, ReasonBootstrapProjectionDrift, ReasonBundleIndexMissing, ReasonBundleIndexDrift, ReasonBundleRoleSetDrift, ReasonBundleRoleDrift, ReasonIOFailed, ReasonPythonMissing, ReasonToolMissing, ReasonPythonFailed, ReasonWheelBuildFailed, ReasonWheelInvalid, ReasonRecordInvalid:
		return true
	}
	return false
}
func projectionForReason(reason string) (string, protocol.Category, string, bool) {
	switch reason {
	case ReasonRepoRootInvalid, ReasonPythonPathInvalid, ReasonPythonContainmentInvalid, ReasonMatrixTargetInvalid, ReasonWheelhousePathInvalid, ReasonWorkDirInvalid, ReasonOutPathInvalid:
		return "sdk_python_assets_input_invalid", protocol.CategoryInput, "Python SDK asset input is invalid", false
	case ReasonOperationCanceled:
		return "operation_canceled", protocol.CategoryCanceled, "operation was canceled", false
	case ReasonBootstrapProjectionDrift, ReasonBundleIndexMissing, ReasonBundleIndexDrift, ReasonBundleRoleSetDrift, ReasonBundleRoleDrift:
		return "sdk_python_assets_drift", protocol.CategoryDrift, "Python SDK assets are out of date", false
	case ReasonPythonMissing, ReasonToolMissing:
		return "sdk_python_assets_tool_missing", protocol.CategoryUnavailable, "Python SDK build tool is unavailable", false
	case ReasonPythonFailed, ReasonWheelBuildFailed, ReasonWheelInvalid, ReasonRecordInvalid:
		return "sdk_python_assets_build_failed", protocol.CategoryExternal, "Python SDK asset build failed", false
	default:
		return "sdk_python_assets_internal", protocol.CategoryInternal, "Python SDK asset operation failed", false
	}
}
func ErrorReason(err error) string {
	if err == nil {
		return ""
	}
	payload := protocol.Project(err)
	var detail struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(payload.Details, &detail) == nil {
		return detail.Reason
	}
	return ""
}
