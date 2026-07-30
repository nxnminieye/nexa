package sourcecomment_test

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestCanonicalOperationIdentitiesPreserveOwnerBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, got, want string
	}{
		{name: "ungrouped Pascal handler", got: mustAPIOperationID(t, "", "Login"), want: "login"},
		{name: "grouped PDCL handler", got: mustAPIOperationID(t, "asset", "ConfirmAssetReturn"), want: "asset.confirmAssetReturn"},
		{name: "qualified RPC", got: mustRPCOperationID(t, "core-service", "core.v1.CoreService.GetUserInfo"), want: "coreService.core.v1.coreService.getUserInfo"},
	} {
		if test.got != test.want {
			t.Errorf("%s = %q, want %q", test.name, test.got, test.want)
		}
	}
}

func mustAPIOperationID(t *testing.T, group, handler string) string {
	t.Helper()
	value, err := sourcecomment.CanonicalAPIOperationID(group, handler)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustRPCOperationID(t *testing.T, serviceID, method string) string {
	t.Helper()
	value, err := sourcecomment.CanonicalRPCOperationID(serviceID, method)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
