package contract

import (
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type IdentityField uint8

const (
	IdentityProviderID IdentityField = iota + 1
	IdentityModulePath
	IdentityPackagePath
	IdentityVersion
)

type IdentityIssue struct {
	Field  IdentityField
	Reason IdentityReason
}

type IdentityReason uint8

const (
	IdentityProviderIDInvalid IdentityReason = iota + 1
	IdentityModulePathInvalid
	IdentityPackagePathInvalid
	IdentityPackageModuleMismatch
	IdentityVersionInvalid
	IdentityModuleVersionMismatch
)

func (i *IdentityIssue) Valid() bool {
	if i == nil {
		return false
	}
	switch i.Field {
	case IdentityProviderID:
		return i.Reason == IdentityProviderIDInvalid
	case IdentityModulePath:
		return i.Reason == IdentityModulePathInvalid
	case IdentityPackagePath:
		return i.Reason == IdentityPackagePathInvalid || i.Reason == IdentityPackageModuleMismatch
	case IdentityVersion:
		return i.Reason == IdentityVersionInvalid || i.Reason == IdentityModuleVersionMismatch
	default:
		return false
	}
}

func (r IdentityReason) MachineReason() (string, bool) {
	switch r {
	case IdentityProviderIDInvalid:
		return "provider_id_invalid", true
	case IdentityModulePathInvalid:
		return "module_path_invalid", true
	case IdentityPackagePathInvalid:
		return "package_path_invalid", true
	case IdentityPackageModuleMismatch:
		return "package_module_mismatch", true
	case IdentityVersionInvalid:
		return "version_invalid", true
	case IdentityModuleVersionMismatch:
		return "module_version_mismatch", true
	default:
		return "", false
	}
}

func ValidateIdentity(providerID, modulePath, packagePath, version string) *IdentityIssue {
	if !ValidStableID(providerID) {
		return &IdentityIssue{Field: IdentityProviderID, Reason: IdentityProviderIDInvalid}
	}
	if module.CheckPath(modulePath) != nil {
		return &IdentityIssue{Field: IdentityModulePath, Reason: IdentityModulePathInvalid}
	}
	if module.CheckImportPath(packagePath) != nil {
		return &IdentityIssue{Field: IdentityPackagePath, Reason: IdentityPackagePathInvalid}
	}
	if packagePath != modulePath && !strings.HasPrefix(packagePath, modulePath+"/") {
		return &IdentityIssue{Field: IdentityPackagePath, Reason: IdentityPackageModuleMismatch}
	}
	if !semver.IsValid(version) || semver.Canonical(version) != version {
		return &IdentityIssue{Field: IdentityVersion, Reason: IdentityVersionInvalid}
	}
	if module.Check(modulePath, version) != nil {
		return &IdentityIssue{Field: IdentityVersion, Reason: IdentityModuleVersionMismatch}
	}
	return nil
}
