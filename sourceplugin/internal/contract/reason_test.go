package contract

import "testing"

func TestIdentityReasonsHaveClosedMachineMappings(t *testing.T) {
	tests := []struct {
		reason IdentityReason
		code   string
	}{
		{IdentityProviderIDInvalid, "provider_id_invalid"},
		{IdentityModulePathInvalid, "module_path_invalid"},
		{IdentityPackagePathInvalid, "package_path_invalid"},
		{IdentityPackageModuleMismatch, "package_module_mismatch"},
		{IdentityVersionInvalid, "version_invalid"},
		{IdentityModuleVersionMismatch, "module_version_mismatch"},
	}
	for _, test := range tests {
		code, ok := test.reason.MachineReason()
		if !ok || code != test.code {
			t.Fatalf("reason %d = %q, %v", test.reason, code, ok)
		}
	}
	if code, ok := (IdentityReason(0)).MachineReason(); ok || code != "" {
		t.Fatalf("zero identity reason = %q, %v", code, ok)
	}
	issue := ValidateIdentity("Bad", "bad path", "bad path", "latest")
	if issue == nil || issue.Field != IdentityProviderID || issue.Reason != IdentityProviderIDInvalid {
		t.Fatalf("identity issue = %#v", issue)
	}
}

func TestPathReasonsHaveClosedMachineMappings(t *testing.T) {
	tests := []struct {
		value  string
		reason PathReason
		code   string
	}{
		{"../bad", PathInvalid, "path_invalid"},
		{"e\u0301.txt", PathNotNFC, "path_not_nfc"},
		{".nexa/source/file", PathReserved, "path_reserved"},
	}
	for _, test := range tests {
		issue := ValidatePortablePath(test.value)
		if issue == nil || issue.Reason != test.reason {
			t.Fatalf("path %q issue = %#v", test.value, issue)
		}
		code, ok := issue.Reason.MachineReason()
		if !ok || code != test.code {
			t.Fatalf("path %q reason = %q, %v", test.value, code, ok)
		}
	}
	if issue := ValidatePortablePath("valid/path"); issue != nil {
		t.Fatalf("valid path issue = %#v", issue)
	}
	if code, ok := (PathReason(0)).MachineReason(); ok || code != "" {
		t.Fatalf("zero path reason = %q, %v", code, ok)
	}
}
