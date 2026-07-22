package sourceplugin

import (
	"errors"
	"testing"

	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
)

func TestIdentityConstructorUsesManifestIdentityContract(t *testing.T) {
	spec := IdentitySpec{
		ProviderID:  "sample.foundation",
		ModulePath:  "example.test/sample/foundation",
		PackagePath: "example.test/sample/foundation/source",
		Version:     "v0.1.0",
	}
	identity, err := NewIdentity(spec)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ProviderID() != spec.ProviderID || identity.ModulePath() != spec.ModulePath ||
		identity.PackagePath() != spec.PackagePath || identity.Version() != spec.Version {
		t.Fatalf("identity = %#v", identity)
	}
	second, err := NewIdentity(spec)
	if err != nil || !identity.Equal(second) || (Identity{}).Equal(identity) || identity.Equal(Identity{}) {
		t.Fatalf("identity equality = %v, second err = %v", identity.Equal(second), err)
	}
}

func TestIdentityConstructorPreservesOwnerValidationOrder(t *testing.T) {
	valid := IdentitySpec{
		ProviderID:  "sample",
		ModulePath:  "example.test/sample",
		PackagePath: "example.test/sample/source",
		Version:     "v0.1.0",
	}
	tests := []struct {
		name    string
		mutate  func(*IdentitySpec)
		reason  string
		pointer string
	}{
		{"provider", func(s *IdentitySpec) { s.ProviderID = "Bad"; s.ModulePath = "bad path" }, "provider_id_invalid", "/identity/providerId"},
		{"module", func(s *IdentitySpec) { s.ModulePath = "bad path"; s.PackagePath = "bad path" }, "module_path_invalid", "/identity/modulePath"},
		{"package", func(s *IdentitySpec) { s.PackagePath = "bad path"; s.Version = "latest" }, "package_path_invalid", "/identity/packagePath"},
		{"relation", func(s *IdentitySpec) { s.PackagePath = "example.test/other"; s.Version = "latest" }, "package_module_mismatch", "/identity/packagePath"},
		{"version", func(s *IdentitySpec) { s.Version = "latest" }, "version_invalid", "/identity/version"},
		{"major", func(s *IdentitySpec) { s.ModulePath += "/v2"; s.PackagePath = s.ModulePath + "/source" }, "module_version_mismatch", "/identity/version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := valid
			test.mutate(&spec)
			_, err := NewIdentity(spec)
			var projected *Error
			if !errors.As(err, &projected) || projected.Reason() != test.reason || projected.Pointer() != test.pointer ||
				projected.Code() != "source_manifest_invalid" || !errors.Is(projected, ErrManifestInvalid) {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestContractIssueProjectionRejectsUnknownReason(t *testing.T) {
	identityErr := projectIdentityIssue(&contract.IdentityIssue{}, "/identity")
	if identityErr == nil || identityErr.Class() != ErrContractInternal || identityErr.Code() != "source_contract_internal" ||
		identityErr.Reason() != "identity_issue_unmapped" || identityErr.Pointer() != "/identity" ||
		identityErr.Error() != "source contract projection failed" || !errors.Is(identityErr, ErrContractInternal) {
		t.Fatalf("identity projection = %#v", identityErr)
	}
	_, pathErr := projectPathIssue(&contract.PathIssue{}, "/files/0/path")
	if pathErr == nil || pathErr.Class() != ErrContractInternal || pathErr.Code() != "source_contract_internal" ||
		pathErr.Reason() != "path_issue_unmapped" || pathErr.Pointer() != "/files/0/path" ||
		pathErr.Error() != "source contract projection failed" || !errors.Is(pathErr, ErrContractInternal) {
		t.Fatalf("path projection = %#v", pathErr)
	}
}

func TestIdentityIssueProjectionRejectsUnknownAndIllegalPairs(t *testing.T) {
	legal := map[contract.IdentityIssue]struct {
		reason  string
		pointer string
	}{
		{Field: contract.IdentityProviderID, Reason: contract.IdentityProviderIDInvalid}:      {reason: "provider_id_invalid", pointer: "/identity/providerId"},
		{Field: contract.IdentityModulePath, Reason: contract.IdentityModulePathInvalid}:      {reason: "module_path_invalid", pointer: "/identity/modulePath"},
		{Field: contract.IdentityPackagePath, Reason: contract.IdentityPackagePathInvalid}:    {reason: "package_path_invalid", pointer: "/identity/packagePath"},
		{Field: contract.IdentityPackagePath, Reason: contract.IdentityPackageModuleMismatch}: {reason: "package_module_mismatch", pointer: "/identity/packagePath"},
		{Field: contract.IdentityVersion, Reason: contract.IdentityVersionInvalid}:            {reason: "version_invalid", pointer: "/identity/version"},
		{Field: contract.IdentityVersion, Reason: contract.IdentityModuleVersionMismatch}:     {reason: "module_version_mismatch", pointer: "/identity/version"},
	}
	fields := []contract.IdentityField{0, contract.IdentityProviderID, contract.IdentityModulePath, contract.IdentityPackagePath, contract.IdentityVersion, 255}
	reasons := []contract.IdentityReason{0, contract.IdentityProviderIDInvalid, contract.IdentityModulePathInvalid, contract.IdentityPackagePathInvalid, contract.IdentityPackageModuleMismatch, contract.IdentityVersionInvalid, contract.IdentityModuleVersionMismatch, 255}
	for _, field := range fields {
		for _, reason := range reasons {
			issue := contract.IdentityIssue{Field: field, Reason: reason}
			err := projectIdentityIssue(&issue, "/identity")
			if expected, ok := legal[issue]; ok {
				if err == nil || err.Class() != ErrManifestInvalid || err.Reason() != expected.reason || err.Pointer() != expected.pointer {
					t.Fatalf("legal issue %#v = %#v", issue, err)
				}
				continue
			}
			if err == nil || err.Class() != ErrContractInternal || err.Reason() != "identity_issue_unmapped" || err.Pointer() != "/identity" {
				t.Fatalf("illegal issue %#v = %#v", issue, err)
			}
		}
	}
}
