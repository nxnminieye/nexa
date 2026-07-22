package crudlogic

import (
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestFrozenErrorTable(t *testing.T) {
	want := []statusProjection{
		{codes.InvalidArgument, "invalid identity"},
		{codes.InvalidArgument, "invalid pagination"},
		{codes.InvalidArgument, "update_mask is required"},
		{codes.InvalidArgument, "update_mask contains unsupported field"},
		{codes.Unauthenticated, "tenant context is required"},
		{codes.NotFound, "entity not found"},
		{codes.InvalidArgument, "invalid field value"},
		{codes.FailedPrecondition, "constraint violation"},
		{codes.Internal, "crud operation failed"},
	}
	if got := frozenErrorTable(); !reflect.DeepEqual(got, want) {
		t.Fatalf("frozenErrorTable() = %#v, want %#v", got, want)
	}
}
