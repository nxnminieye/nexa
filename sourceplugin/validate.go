package sourceplugin

import (
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
)

const MaxStableIDBytes = contract.MaxStableIDBytes

type diagnosticLocation struct {
	line   int
	column int
}

type diagnosticLocations struct {
	source       string
	profileEdges map[string]diagnosticLocation
	requirements map[string]diagnosticLocation
}

func (d diagnosticLocations) clone() diagnosticLocations {
	result := diagnosticLocations{source: d.source}
	if d.profileEdges != nil {
		result.profileEdges = make(map[string]diagnosticLocation, len(d.profileEdges))
		for key, location := range d.profileEdges {
			result.profileEdges[key] = location
		}
	}
	if d.requirements != nil {
		result.requirements = make(map[string]diagnosticLocation, len(d.requirements))
		for key, location := range d.requirements {
			result.requirements[key] = location
		}
	}
	return result
}

func newManifest(spec ManifestSpec, diagnostics diagnosticLocations) (Manifest, error) {
	normalized := normalizeManifestSpec(spec)
	if err := validateIdentity(normalized.Identity); err != nil {
		return Manifest{}, err
	}
	if spec.Files == nil {
		return Manifest{}, newSourceError("source_manifest_invalid", "document_invalid", "/files")
	}
	if err := validateFiles(normalized.Files); err != nil {
		return Manifest{}, err
	}
	if spec.Profiles == nil {
		return Manifest{}, newSourceError("source_manifest_invalid", "document_invalid", "/profiles")
	}
	if err := validateProfiles(normalized.Profiles, normalized.Files); err != nil {
		return Manifest{}, err
	}
	if cycle, pointer, from, to := profileCycle(normalized.Profiles); len(cycle) > 0 {
		err := newSourceError("source_profile_cycle", "profile_cycle", pointer)
		err.cycle = append([]string(nil), cycle...)
		if location, ok := diagnostics.profileEdges[profileEdgeKey(from, to)]; ok {
			err = withLocation(err, diagnostics.source, location.line, location.column)
		}
		return Manifest{}, err
	}

	manifest := manifestFromSpec(normalized, diagnostics)
	canonical, err := canonicalManifestJSON(manifest)
	if err != nil {
		return Manifest{}, newSourceError("source_manifest_invalid", "document_invalid", "")
	}
	manifest.canonical = canonical
	manifest.digest = provenance.SHA256(canonical[:len(canonical)-1])
	return manifest, nil
}

func normalizeManifestSpec(spec ManifestSpec) ManifestSpec {
	result := ManifestSpec{Identity: spec.Identity, Files: append([]FileSpec(nil), spec.Files...), Profiles: make([]ProfileSpec, len(spec.Profiles))}
	sort.SliceStable(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	for index, profile := range spec.Profiles {
		result.Profiles[index] = ProfileSpec{
			ID:               profile.ID,
			Files:            cloneStringsPreservingPresence(profile.Files),
			RequiresProfiles: append([]string(nil), profile.RequiresProfiles...),
			RequiresBundles:  append([]BundleRequirementSpec(nil), profile.RequiresBundles...),
			Validations:      make([]ValidationRecipeSpec, len(profile.Validations)),
		}
		if result.Profiles[index].RequiresProfiles == nil {
			result.Profiles[index].RequiresProfiles = []string{}
		}
		if result.Profiles[index].RequiresBundles == nil {
			result.Profiles[index].RequiresBundles = []BundleRequirementSpec{}
		}
		for validationIndex, recipe := range profile.Validations {
			result.Profiles[index].Validations[validationIndex] = ValidationRecipeSpec{
				ID: recipe.ID, Kind: recipe.Kind, WorkingDirectory: recipe.WorkingDirectory,
				Packages: append([]string(nil), recipe.Packages...),
			}
		}
		if result.Profiles[index].Validations == nil {
			result.Profiles[index].Validations = []ValidationRecipeSpec{}
		}
		sort.Strings(result.Profiles[index].Files)
		sort.Strings(result.Profiles[index].RequiresProfiles)
		sort.SliceStable(result.Profiles[index].RequiresBundles, func(i, j int) bool {
			return requirementFullKey(result.Profiles[index].RequiresBundles[i]) < requirementFullKey(result.Profiles[index].RequiresBundles[j])
		})
		for validationIndex := range result.Profiles[index].Validations {
			sort.Strings(result.Profiles[index].Validations[validationIndex].Packages)
		}
		sort.SliceStable(result.Profiles[index].Validations, func(i, j int) bool {
			return result.Profiles[index].Validations[i].ID < result.Profiles[index].Validations[j].ID
		})
	}
	sort.SliceStable(result.Profiles, func(i, j int) bool { return result.Profiles[i].ID < result.Profiles[j].ID })
	return result
}

func validateIdentity(identity IdentitySpec) *Error {
	issue := contract.ValidateIdentity(identity.ProviderID, identity.ModulePath, identity.PackagePath, identity.Version)
	if issue == nil {
		return nil
	}
	return projectIdentityIssue(issue, "/identity")
}

func projectIdentityIssue(issue *contract.IdentityIssue, pointer string) *Error {
	if issue == nil {
		return nil
	}
	if !issue.Valid() {
		return newContractInternal("identity_issue_unmapped", pointer)
	}
	field := ""
	switch issue.Field {
	case contract.IdentityProviderID:
		field = "providerId"
	case contract.IdentityModulePath:
		field = "modulePath"
	case contract.IdentityPackagePath:
		field = "packagePath"
	case contract.IdentityVersion:
		field = "version"
	default:
		return newContractInternal("identity_issue_unmapped", pointer)
	}
	reason, ok := issue.Reason.MachineReason()
	if !ok {
		return newContractInternal("identity_issue_unmapped", pointer)
	}
	return newSourceError("source_manifest_invalid", reason, pointer+"/"+field)
}

func validateFiles(files []FileSpec) *Error {
	for index, file := range files {
		base := "/files/" + strconv.Itoa(index)
		reason, internal := validatePortablePath(file.Path, base+"/path")
		if internal != nil {
			return internal
		}
		if reason != "" {
			return newSourceError("source_path_invalid", reason, base+"/path")
		}
		if file.Size < 0 {
			return newSourceError("source_file_invalid", "file_size_invalid", base+"/size")
		}
		if !validDigest(file.Digest) {
			return newSourceError("source_file_invalid", "file_digest_invalid", base+"/digest")
		}
		if file.Mode != Mode0644 && file.Mode != Mode0755 {
			return newSourceError("source_file_invalid", "file_mode_invalid", base+"/mode")
		}
		if index > 0 && file.Path == files[index-1].Path {
			return newSourceError("source_file_invalid", "file_duplicate", base+"/path")
		}
	}
	seenFolded := make(map[string]int, len(files))
	for index, file := range files {
		key := foldPortablePath(file.Path)
		if previous, ok := seenFolded[key]; ok && files[previous].Path != file.Path {
			return newSourceError("source_path_invalid", "path_collision", "/files/"+strconv.Itoa(index)+"/path")
		}
		seenFolded[key] = index
	}
	for left := range files {
		for right := left + 1; right < len(files); right++ {
			leftFolded := foldPortablePath(files[left].Path)
			rightFolded := foldPortablePath(files[right].Path)
			if strings.HasPrefix(rightFolded, leftFolded+"/") || strings.HasPrefix(leftFolded, rightFolded+"/") {
				return newSourceError("source_path_invalid", "path_prefix_collision", "/files/"+strconv.Itoa(right)+"/path")
			}
		}
	}
	return nil
}

func validateProfiles(profiles []ProfileSpec, files []FileSpec) *Error {
	fileSet := make(map[string]struct{}, len(files))
	for _, file := range files {
		fileSet[file.Path] = struct{}{}
	}
	profileSet := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		profileSet[profile.ID] = struct{}{}
	}
	seenProfileIDs := make(map[string]struct{}, len(profiles))
	validationIDs := make(map[string]struct{})
	for profileIndex, profile := range profiles {
		base := "/profiles/" + strconv.Itoa(profileIndex)
		if !validStableID(profile.ID) {
			return newSourceError("source_profile_invalid", "profile_id_invalid", base+"/id")
		}
		if _, duplicate := seenProfileIDs[profile.ID]; duplicate {
			return newSourceError("source_profile_invalid", "profile_id_duplicate", base+"/id")
		}
		seenProfileIDs[profile.ID] = struct{}{}
		if profile.Files == nil {
			return newSourceError("source_manifest_invalid", "document_invalid", base+"/files")
		}
		for index, file := range profile.Files {
			pointer := base + "/files/" + strconv.Itoa(index)
			if index > 0 && file == profile.Files[index-1] {
				return newSourceError("source_profile_invalid", "profile_file_duplicate", pointer)
			}
			if _, ok := fileSet[file]; !ok {
				return newSourceError("source_profile_invalid", "profile_file_unknown", pointer)
			}
		}
		for index, dependency := range profile.RequiresProfiles {
			pointer := base + "/requiresProfiles/" + strconv.Itoa(index)
			if !validStableID(dependency) {
				return newSourceError("source_profile_invalid", "profile_dependency_invalid", pointer)
			}
			if index > 0 && dependency == profile.RequiresProfiles[index-1] {
				return newSourceError("source_profile_invalid", "profile_dependency_duplicate", pointer)
			}
			if _, ok := profileSet[dependency]; !ok {
				return newSourceError("source_profile_invalid", "profile_dependency_unknown", pointer)
			}
		}
		for index, requirement := range profile.RequiresBundles {
			pointer := base + "/requiresBundles/" + strconv.Itoa(index)
			if err := validateRequirement(requirement, pointer); err != nil {
				return err
			}
			if index > 0 && requirementFullKey(requirement) == requirementFullKey(profile.RequiresBundles[index-1]) {
				return newSourceError("source_bundle_requirement_invalid", "requirement_duplicate", pointer)
			}
		}
		for index, validation := range profile.Validations {
			pointer := base + "/validations/" + strconv.Itoa(index)
			if !validStableID(validation.ID) {
				return newSourceError("source_validation_invalid", "validation_id_invalid", pointer+"/id")
			}
			if _, duplicate := validationIDs[validation.ID]; duplicate {
				return newSourceError("source_validation_invalid", "validation_id_duplicate", pointer+"/id")
			}
			validationIDs[validation.ID] = struct{}{}
			if validation.Kind != ValidationGoTest && validation.Kind != ValidationGoBuild {
				return newSourceError("source_validation_invalid", "validation_kind_invalid", pointer+"/kind")
			}
			if validation.WorkingDirectory != "." {
				reason, internal := validatePortablePath(validation.WorkingDirectory, pointer+"/workingDirectory")
				if internal != nil {
					return internal
				}
				if reason != "" {
					return newSourceError("source_validation_invalid", "validation_workdir_invalid", pointer+"/workingDirectory")
				}
			}
			if len(validation.Packages) == 0 {
				return newSourceError("source_validation_invalid", "validation_package_invalid", pointer+"/packages")
			}
			for packageIndex, packagePath := range validation.Packages {
				packagePointer := pointer + "/packages/" + strconv.Itoa(packageIndex)
				valid, internal := validRecipePackage(packagePath, packagePointer)
				if internal != nil {
					return internal
				}
				if !valid {
					return newSourceError("source_validation_invalid", "validation_package_invalid", packagePointer)
				}
				if packageIndex > 0 && packagePath == validation.Packages[packageIndex-1] {
					return newSourceError("source_validation_invalid", "validation_package_duplicate", packagePointer)
				}
			}
		}
	}
	return nil
}

func validateRequirement(requirement BundleRequirementSpec, pointer string) *Error {
	if issue := contract.ValidateIdentity(requirement.ProviderID, requirement.ModulePath, requirement.PackagePath, requirement.Version); issue != nil {
		if err := projectIdentityIssue(issue, pointer); err.Class() == ErrContractInternal {
			return err
		}
		return newSourceError("source_bundle_requirement_invalid", "requirement_identity_invalid", pointer)
	}
	if !validStableID(requirement.ProfileID) {
		return newSourceError("source_bundle_requirement_invalid", "requirement_profile_invalid", pointer+"/profileId")
	}
	if !validDigest(requirement.ManifestDigest) || !validDigest(requirement.TreeDigest) {
		return newSourceError("source_bundle_requirement_invalid", "requirement_digest_invalid", pointer)
	}
	return nil
}

func validStableID(value string) bool {
	return contract.ValidStableID(value)
}

func validDigest(value provenance.Digest) bool {
	_, err := provenance.ParseDigest(value.String())
	return err == nil
}

func validRecipePackage(value, pointer string) (bool, *Error) {
	if value == "." {
		return true, nil
	}
	if !strings.HasPrefix(value, "./") || strings.Contains(value, "@") || strings.HasPrefix(value, "-") {
		return false, nil
	}
	relative := strings.TrimPrefix(value, "./")
	if relative == "..." {
		return false, nil
	}
	if strings.HasSuffix(relative, "/...") {
		relative = strings.TrimSuffix(relative, "/...")
	}
	if relative == "" {
		return false, nil
	}
	reason, internal := validatePortablePath(relative, pointer)
	if internal != nil {
		return false, internal
	}
	return reason == "", nil
}

func requirementIdentityKey(spec BundleRequirementSpec) string {
	return strings.Join([]string{spec.ProviderID, spec.ModulePath, spec.PackagePath, spec.Version, spec.ProfileID}, "\x00")
}

func requirementFullKey(spec BundleRequirementSpec) string {
	return requirementIdentityKey(spec) + "\x00" + spec.ManifestDigest.String() + "\x00" + spec.TreeDigest.String()
}

func requirementValueFullKey(requirement BundleRequirement) string {
	return strings.Join([]string{requirement.providerID, requirement.modulePath, requirement.packagePath, requirement.version, requirement.profileID, requirement.manifestDigest.String(), requirement.treeDigest.String()}, "\x00")
}

func requirementValueIdentityKey(requirement BundleRequirement) string {
	return strings.Join([]string{requirement.providerID, requirement.modulePath, requirement.packagePath, requirement.version, requirement.profileID}, "\x00")
}

func profileEdgeKey(profileID, dependencyID string) string {
	return profileID + "\x00" + dependencyID
}

func requirementDiagnosticKey(profileID string, requirement BundleRequirementSpec) string {
	return profileID + "\x00" + requirementFullKey(requirement)
}

func manifestFromSpec(spec ManifestSpec, diagnostics diagnosticLocations) Manifest {
	manifest := Manifest{
		identity: Identity{providerID: spec.Identity.ProviderID, modulePath: spec.Identity.ModulePath, packagePath: spec.Identity.PackagePath, version: spec.Identity.Version},
		files:    make([]File, len(spec.Files)), profiles: make([]Profile, len(spec.Profiles)),
		fileIndex: make(map[string]int, len(spec.Files)), profileIndex: make(map[string]int, len(spec.Profiles)), diagnostics: diagnostics.clone(),
	}
	for index, file := range spec.Files {
		manifest.files[index] = File{path: file.Path, size: file.Size, digest: file.Digest, mode: file.Mode}
		manifest.fileIndex[file.Path] = index
	}
	for index, profile := range spec.Profiles {
		converted := Profile{id: profile.ID, filePaths: append([]string{}, profile.Files...), requiredProfiles: append([]string{}, profile.RequiresProfiles...), requirements: make([]BundleRequirement, len(profile.RequiresBundles)), validations: make([]ValidationRecipe, len(profile.Validations))}
		for requirementIndex, requirement := range profile.RequiresBundles {
			converted.requirements[requirementIndex] = BundleRequirement{providerID: requirement.ProviderID, modulePath: requirement.ModulePath, packagePath: requirement.PackagePath, version: requirement.Version, profileID: requirement.ProfileID, manifestDigest: requirement.ManifestDigest, treeDigest: requirement.TreeDigest}
		}
		for validationIndex, recipe := range profile.Validations {
			converted.validations[validationIndex] = ValidationRecipe{id: recipe.ID, kind: recipe.Kind, workingDirectory: recipe.WorkingDirectory, packages: append([]string(nil), recipe.Packages...)}
		}
		manifest.profiles[index] = converted
		manifest.profileIndex[profile.ID] = index
	}
	return manifest
}

func cloneStringsPreservingPresence(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
