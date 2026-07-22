package crudlogic

import "fmt"

func renderErrorProjection(methodName string) string {
	return fmt.Sprintf(`func %sProjectError(err error) error {
	if err == nil { return nil }
	switch {
	case ent.IsNotFound(err):
		return status.Error(codes.NotFound, "entity not found")
	case ent.IsValidationError(err):
		return status.Error(codes.InvalidArgument, "invalid field value")
	case ent.IsConstraintError(err):
		return status.Error(codes.FailedPrecondition, "constraint violation")
	default:
		_ = errors.Unwrap(err)
		return status.Error(codes.Internal, "crud operation failed")
	}
}
`, methodName)
}
