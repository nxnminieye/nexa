package sourceplugin

import (
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

type manifestDocument struct {
	APIVersion *string            `json:"apiVersion,omitempty"`
	Kind       *string            `json:"kind,omitempty"`
	Identity   *identityDocument  `json:"identity,omitempty"`
	Files      *[]fileDocument    `json:"files,omitempty"`
	Profiles   *[]profileDocument `json:"profiles,omitempty"`
}

type identityDocument struct {
	ProviderID  *string `json:"providerId,omitempty"`
	ModulePath  *string `json:"modulePath,omitempty"`
	PackagePath *string `json:"packagePath,omitempty"`
	Version     *string `json:"version,omitempty"`
}

type fileDocument struct {
	Path   *string `json:"path,omitempty"`
	Size   *int64  `json:"size,omitempty"`
	Digest *string `json:"digest,omitempty"`
	Mode   *string `json:"mode,omitempty"`
}

type profileDocument struct {
	ID               *string                      `json:"id,omitempty"`
	Files            *[]string                    `json:"files,omitempty"`
	RequiresProfiles *[]string                    `json:"requiresProfiles,omitempty"`
	RequiresBundles  *[]bundleRequirementDocument `json:"requiresBundles,omitempty"`
	Validations      *[]validationDocument        `json:"validations,omitempty"`
}

type bundleRequirementDocument struct {
	ProviderID     *string `json:"providerId,omitempty"`
	ModulePath     *string `json:"modulePath,omitempty"`
	PackagePath    *string `json:"packagePath,omitempty"`
	Version        *string `json:"version,omitempty"`
	ProfileID      *string `json:"profileId,omitempty"`
	ManifestDigest *string `json:"manifestDigest,omitempty"`
	TreeDigest     *string `json:"treeDigest,omitempty"`
}

type validationDocument struct {
	ID               *string         `json:"id,omitempty"`
	Kind             *ValidationKind `json:"kind,omitempty"`
	WorkingDirectory *string         `json:"workingDirectory,omitempty"`
	Packages         *[]string       `json:"packages,omitempty"`
}

func Parse(source string, data []byte) (Manifest, error) {
	if !validateSourceLabel(source) {
		return Manifest{}, newSourceError("source_manifest_invalid", "source_location_invalid", "")
	}
	var document strictdoc.Document
	var err error
	switch path.Ext(source) {
	case ".json":
		document, err = strictdoc.ParseJSON(source, data)
	case ".yaml", ".yml":
		document, err = strictdoc.ParseYAML(source, data)
	default:
		return Manifest{}, withLocation(newSourceError("source_manifest_invalid", "source_format_unsupported", ""), source, 0, 0)
	}
	if err != nil {
		return Manifest{}, projectStrictDocumentError(source, err)
	}

	normalizedJSON := document.JSON()
	var normalized any
	if err := json.Unmarshal(normalizedJSON, &normalized); err != nil {
		return Manifest{}, withDocumentLocation(document, withLocation(newSourceError("source_manifest_invalid", "document_invalid", ""), source, 0, 0), "")
	}

	var diagnosticWire manifestDocument
	_ = json.Unmarshal(normalizedJSON, &diagnosticWire)
	canonicalToAuthored, authoredToCanonical := semanticPointerMaps(diagnosticWire)
	var failures []schemaFailure
	if schemaErr := validateSchema(normalized); schemaErr != nil {
		failures = append(failures, schemaFailures(schemaErr, normalized)...)
		if value, ok := objectString(normalized, "apiVersion"); ok && value != APIVersion {
			failures = append(failures, schemaFailure{authoredPointer: "/apiVersion", publicPointer: "/apiVersion", reason: "version_unsupported"})
		}
		if value, ok := objectString(normalized, "kind"); ok && value != Kind {
			failures = append(failures, schemaFailure{authoredPointer: "/kind", publicPointer: "/kind", reason: "kind_invalid"})
		}
	}
	var wire manifestDocument
	if decodeErr := document.Decode(&wire); decodeErr != nil {
		failures = append(failures, documentDecodeFailure(decodeErr))
	}
	for index := range failures {
		if failures[index].publicPointer == "" {
			failures[index].publicPointer = failures[index].authoredPointer
		}
		if canonicalPointer, ok := authoredToCanonical[failures[index].authoredPointer]; ok {
			failures[index].publicPointer = canonicalPointer
		}
	}
	if len(failures) > 0 {
		selected := selectDocumentFailure(document, failures)
		pointer := safeDocumentPointer(selected.publicPointer)
		projected := newSourceError(codeForReason(selected.reason), selected.reason, pointer)
		projected.source = source
		line, column := locationForPointer(document, selected.authoredPointer)
		projected.line, projected.column = line, column
		return Manifest{}, projected
	}

	spec, conversionError := manifestSpecFromDocument(wire)
	if conversionError != nil {
		conversionError.source = source
		authoredPointer := conversionError.pointer
		line, column := locationForPointer(document, authoredPointer)
		if canonicalPointer, ok := authoredToCanonical[authoredPointer]; ok {
			conversionError.pointer = canonicalPointer
		}
		conversionError.line, conversionError.column = line, column
		return Manifest{}, conversionError
	}
	diagnostics := collectDiagnostics(source, document, wire, spec)
	manifest, err := newManifest(spec, diagnostics)
	if err != nil {
		projected := err.(*Error)
		if projected.source == "" {
			authoredPointer := projected.pointer
			if mapped, ok := canonicalToAuthored[projected.pointer]; ok {
				authoredPointer = mapped
			}
			line, column := locationForPointer(document, authoredPointer)
			projected = withLocation(projected, source, line, column)
		}
		return Manifest{}, projected
	}
	return manifest, nil
}

func documentDecodeFailure(err error) schemaFailure {
	var strictError *strictdoc.Error
	if !errors.As(err, &strictError) {
		return schemaFailure{reason: "document_invalid"}
	}
	reason := strictError.Code
	switch reason {
	case "document_invalid", "document_unknown_field", "document_trailing_input":
	default:
		reason = "document_invalid"
	}
	return schemaFailure{authoredPointer: strictError.Pointer, publicPointer: strictError.Pointer, reason: reason}
}

func semanticPointerMaps(document manifestDocument) (canonicalToAuthored, authoredToCanonical map[string]string) {
	canonicalToAuthored = make(map[string]string)
	authoredToCanonical = make(map[string]string)
	add := func(canonical, authored string) {
		canonicalToAuthored[canonical] = authored
		authoredToCanonical[authored] = canonical
	}
	if document.Files != nil {
		indices := sortedIndices(len(*document.Files), func(left, right int) bool {
			return valueString((*document.Files)[left].Path) < valueString((*document.Files)[right].Path)
		})
		for canonicalIndex, authoredIndex := range indices {
			canonicalBase := "/files/" + strconv.Itoa(canonicalIndex)
			authoredBase := "/files/" + strconv.Itoa(authoredIndex)
			add(canonicalBase, authoredBase)
			for _, field := range []string{"path", "size", "digest", "mode"} {
				add(canonicalBase+"/"+field, authoredBase+"/"+field)
			}
		}
	}
	if document.Profiles == nil {
		return canonicalToAuthored, authoredToCanonical
	}
	profileIndices := sortedIndices(len(*document.Profiles), func(left, right int) bool {
		return valueString((*document.Profiles)[left].ID) < valueString((*document.Profiles)[right].ID)
	})
	for canonicalProfileIndex, authoredProfileIndex := range profileIndices {
		profile := (*document.Profiles)[authoredProfileIndex]
		canonicalBase := "/profiles/" + strconv.Itoa(canonicalProfileIndex)
		authoredBase := "/profiles/" + strconv.Itoa(authoredProfileIndex)
		add(canonicalBase, authoredBase)
		add(canonicalBase+"/id", authoredBase+"/id")
		add(canonicalBase+"/files", authoredBase+"/files")
		add(canonicalBase+"/requiresProfiles", authoredBase+"/requiresProfiles")
		add(canonicalBase+"/requiresBundles", authoredBase+"/requiresBundles")
		add(canonicalBase+"/validations", authoredBase+"/validations")
		if profile.Files != nil {
			indices := sortedIndices(len(*profile.Files), func(left, right int) bool { return (*profile.Files)[left] < (*profile.Files)[right] })
			for canonicalIndex, authoredIndex := range indices {
				add(canonicalBase+"/files/"+strconv.Itoa(canonicalIndex), authoredBase+"/files/"+strconv.Itoa(authoredIndex))
			}
		}
		if profile.RequiresProfiles != nil {
			indices := sortedIndices(len(*profile.RequiresProfiles), func(left, right int) bool {
				return (*profile.RequiresProfiles)[left] < (*profile.RequiresProfiles)[right]
			})
			for canonicalIndex, authoredIndex := range indices {
				add(canonicalBase+"/requiresProfiles/"+strconv.Itoa(canonicalIndex), authoredBase+"/requiresProfiles/"+strconv.Itoa(authoredIndex))
			}
		}
		if profile.RequiresBundles != nil {
			indices := sortedIndices(len(*profile.RequiresBundles), func(left, right int) bool {
				return rawRequirementKey((*profile.RequiresBundles)[left]) < rawRequirementKey((*profile.RequiresBundles)[right])
			})
			for canonicalIndex, authoredIndex := range indices {
				canonicalRequirement := canonicalBase + "/requiresBundles/" + strconv.Itoa(canonicalIndex)
				authoredRequirement := authoredBase + "/requiresBundles/" + strconv.Itoa(authoredIndex)
				add(canonicalRequirement, authoredRequirement)
				for _, field := range []string{"providerId", "modulePath", "packagePath", "version", "profileId", "manifestDigest", "treeDigest"} {
					add(canonicalRequirement+"/"+field, authoredRequirement+"/"+field)
				}
			}
		}
		if profile.Validations != nil {
			indices := sortedIndices(len(*profile.Validations), func(left, right int) bool {
				return valueString((*profile.Validations)[left].ID) < valueString((*profile.Validations)[right].ID)
			})
			for canonicalIndex, authoredIndex := range indices {
				validation := (*profile.Validations)[authoredIndex]
				canonicalValidation := canonicalBase + "/validations/" + strconv.Itoa(canonicalIndex)
				authoredValidation := authoredBase + "/validations/" + strconv.Itoa(authoredIndex)
				add(canonicalValidation, authoredValidation)
				for _, field := range []string{"id", "kind", "workingDirectory", "packages"} {
					add(canonicalValidation+"/"+field, authoredValidation+"/"+field)
				}
				if validation.Packages != nil {
					packageIndices := sortedIndices(len(*validation.Packages), func(left, right int) bool { return (*validation.Packages)[left] < (*validation.Packages)[right] })
					for canonicalPackageIndex, authoredPackageIndex := range packageIndices {
						add(canonicalValidation+"/packages/"+strconv.Itoa(canonicalPackageIndex), authoredValidation+"/packages/"+strconv.Itoa(authoredPackageIndex))
					}
				}
			}
		}
	}
	return canonicalToAuthored, authoredToCanonical
}

func sortedIndices(length int, less func(left, right int) bool) []int {
	indices := make([]int, length)
	for index := range indices {
		indices[index] = index
	}
	sort.SliceStable(indices, func(left, right int) bool { return less(indices[left], indices[right]) })
	return indices
}

func rawRequirementKey(requirement bundleRequirementDocument) string {
	return strings.Join([]string{
		valueString(requirement.ProviderID), valueString(requirement.ModulePath), valueString(requirement.PackagePath),
		valueString(requirement.Version), valueString(requirement.ProfileID), valueString(requirement.ManifestDigest), valueString(requirement.TreeDigest),
	}, "\x00")
}

func manifestSpecFromDocument(document manifestDocument) (ManifestSpec, *Error) {
	identity := document.Identity
	spec := ManifestSpec{
		Identity: IdentitySpec{
			ProviderID: valueString(identity.ProviderID), ModulePath: valueString(identity.ModulePath),
			PackagePath: valueString(identity.PackagePath), Version: valueString(identity.Version),
		},
		Files: make([]FileSpec, len(*document.Files)), Profiles: make([]ProfileSpec, len(*document.Profiles)),
	}
	for index, file := range *document.Files {
		digest, err := provenance.ParseDigest(valueString(file.Digest))
		if err != nil {
			return ManifestSpec{}, newSourceError("source_file_invalid", "file_digest_invalid", "/files/"+strconv.Itoa(index)+"/digest")
		}
		spec.Files[index] = FileSpec{Path: valueString(file.Path), Size: valueInt64(file.Size), Digest: digest, Mode: FileMode(valueString(file.Mode))}
	}
	for profileIndex, profile := range *document.Profiles {
		converted := ProfileSpec{ID: valueString(profile.ID), Files: append([]string{}, (*profile.Files)...)}
		if profile.RequiresProfiles != nil {
			converted.RequiresProfiles = append([]string(nil), (*profile.RequiresProfiles)...)
		}
		if profile.RequiresBundles != nil {
			converted.RequiresBundles = make([]BundleRequirementSpec, len(*profile.RequiresBundles))
			for index, requirement := range *profile.RequiresBundles {
				manifestDigest, err := provenance.ParseDigest(valueString(requirement.ManifestDigest))
				if err != nil {
					return ManifestSpec{}, newSourceError("source_bundle_requirement_invalid", "requirement_digest_invalid", "/profiles/"+strconv.Itoa(profileIndex)+"/requiresBundles/"+strconv.Itoa(index)+"/manifestDigest")
				}
				treeDigest, err := provenance.ParseDigest(valueString(requirement.TreeDigest))
				if err != nil {
					return ManifestSpec{}, newSourceError("source_bundle_requirement_invalid", "requirement_digest_invalid", "/profiles/"+strconv.Itoa(profileIndex)+"/requiresBundles/"+strconv.Itoa(index)+"/treeDigest")
				}
				converted.RequiresBundles[index] = BundleRequirementSpec{
					ProviderID: valueString(requirement.ProviderID), ModulePath: valueString(requirement.ModulePath), PackagePath: valueString(requirement.PackagePath),
					Version: valueString(requirement.Version), ProfileID: valueString(requirement.ProfileID), ManifestDigest: manifestDigest, TreeDigest: treeDigest,
				}
			}
		}
		if profile.Validations != nil {
			converted.Validations = make([]ValidationRecipeSpec, len(*profile.Validations))
			for index, validation := range *profile.Validations {
				converted.Validations[index] = ValidationRecipeSpec{
					ID: valueString(validation.ID), Kind: valueValidationKind(validation.Kind), WorkingDirectory: valueString(validation.WorkingDirectory), Packages: append([]string(nil), (*validation.Packages)...),
				}
			}
		}
		spec.Profiles[profileIndex] = converted
	}
	return spec, nil
}

func collectDiagnostics(source string, document strictdoc.Document, wire manifestDocument, spec ManifestSpec) diagnosticLocations {
	result := diagnosticLocations{source: source, profileEdges: make(map[string]diagnosticLocation), requirements: make(map[string]diagnosticLocation)}
	for profileIndex, profile := range *wire.Profiles {
		profileID := valueString(profile.ID)
		if profile.RequiresProfiles != nil {
			for dependencyIndex, dependency := range *profile.RequiresProfiles {
				pointer := "/profiles/" + strconv.Itoa(profileIndex) + "/requiresProfiles/" + strconv.Itoa(dependencyIndex)
				if line, column, ok := document.Location(pointer); ok {
					result.profileEdges[profileEdgeKey(profileID, dependency)] = diagnosticLocation{line: line, column: column}
				}
			}
		}
		if profile.RequiresBundles != nil {
			for requirementIndex := range *profile.RequiresBundles {
				pointer := "/profiles/" + strconv.Itoa(profileIndex) + "/requiresBundles/" + strconv.Itoa(requirementIndex)
				if line, column, ok := document.Location(pointer); ok {
					result.requirements[requirementDiagnosticKey(profileID, spec.Profiles[profileIndex].RequiresBundles[requirementIndex])] = diagnosticLocation{line: line, column: column}
				}
			}
		}
	}
	return result
}

func projectStrictDocumentError(source string, err error) *Error {
	var strictError *strictdoc.Error
	if !errors.As(err, &strictError) {
		return withLocation(newSourceError("source_manifest_invalid", "document_invalid", ""), source, 0, 0)
	}
	reason := strictError.Code
	switch reason {
	case "document_invalid", "document_unknown_field", "document_duplicate_key", "document_trailing_input", "document_alias_forbidden", "document_merge_key_forbidden", "document_tag_forbidden":
	default:
		reason = "document_invalid"
	}
	pointer := safeDocumentPointer(strictError.Pointer)
	projected := withLocation(newSourceError("source_manifest_invalid", reason, pointer), source, strictError.Line, strictError.Column)
	return projected
}

func codeForReason(reason string) string {
	switch reason {
	case "profile_id_invalid", "profile_dependency_invalid":
		return "source_profile_invalid"
	case "requirement_profile_invalid":
		return "source_bundle_requirement_invalid"
	case "validation_id_invalid":
		return "source_validation_invalid"
	default:
		return "source_manifest_invalid"
	}
}

func selectDocumentFailure(document strictdoc.Document, failures []schemaFailure) schemaFailure {
	selected := failures[0]
	selectedLine, selectedColumn := locationForPointer(document, selected.authoredPointer)
	for _, failure := range failures[1:] {
		line, column := locationForPointer(document, failure.authoredPointer)
		if beforeLocation(line, column, selectedLine, selectedColumn) || line == selectedLine && column == selectedColumn && failurePriority(failure) < failurePriority(selected) {
			selected, selectedLine, selectedColumn = failure, line, column
		}
	}
	return selected
}

func failurePriority(failure schemaFailure) int {
	switch failure.reason {
	case "version_unsupported", "kind_invalid":
		return 0
	case "profile_id_invalid", "profile_dependency_invalid", "requirement_profile_invalid", "validation_id_invalid":
		return 1
	case "document_unknown_field":
		return 2
	default:
		return 3
	}
}

func beforeLocation(line, column, otherLine, otherColumn int) bool {
	if line == 0 {
		return false
	}
	if otherLine == 0 {
		return true
	}
	return line < otherLine || line == otherLine && column < otherColumn
}

func locationForPointer(document strictdoc.Document, pointer string) (int, int) {
	for candidate := pointer; ; candidate = parentPointer(candidate) {
		if line, column, ok := document.Location(candidate); ok {
			return line, column
		}
		if candidate == "" {
			return 0, 0
		}
	}
}

func parentPointer(pointer string) string {
	index := strings.LastIndex(pointer, "/")
	if index <= 0 {
		return ""
	}
	return pointer[:index]
}

func withDocumentLocation(document strictdoc.Document, err *Error, pointer string) *Error {
	line, column := locationForPointer(document, pointer)
	err.line, err.column = line, column
	return err
}

func objectString(document any, key string) (string, bool) {
	object, ok := document.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := object[key].(string)
	return value, ok
}

func valueString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func valueValidationKind(value *ValidationKind) ValidationKind {
	if value == nil {
		return ""
	}
	return *value
}

func safeDocumentPointer(pointer string) string {
	components, ok := decodePointer(pointer)
	if !ok {
		return ""
	}
	state := "root"
	safe := make([]string, 0, len(components))
	for _, component := range components {
		next, accepted := nextPointerState(state, component)
		if !accepted {
			break
		}
		safe = append(safe, component)
		state = next
	}
	return instancePointer(safe)
}

func decodePointer(pointer string) ([]string, bool) {
	if pointer == "" {
		return nil, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	encoded := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	result := make([]string, len(encoded))
	for index, component := range encoded {
		var decoded strings.Builder
		for position := 0; position < len(component); position++ {
			if component[position] != '~' {
				decoded.WriteByte(component[position])
				continue
			}
			if position+1 >= len(component) || component[position+1] != '0' && component[position+1] != '1' {
				return nil, false
			}
			position++
			if component[position] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
		}
		result[index] = decoded.String()
	}
	return result, true
}

func nextPointerState(state, component string) (string, bool) {
	switch state {
	case "root":
		switch component {
		case "apiVersion", "kind":
			return "scalar", true
		case "identity":
			return "identity", true
		case "files":
			return "files", true
		case "profiles":
			return "profiles", true
		}
	case "identity":
		switch component {
		case "providerId", "modulePath", "packagePath", "version":
			return "scalar", true
		}
	case "files":
		if canonicalIndex(component) {
			return "file", true
		}
	case "file":
		switch component {
		case "path", "size", "digest", "mode":
			return "scalar", true
		}
	case "profiles":
		if canonicalIndex(component) {
			return "profile", true
		}
	case "profile":
		switch component {
		case "id":
			return "scalar", true
		case "files", "requiresProfiles":
			return "scalar-list", true
		case "requiresBundles":
			return "requirements", true
		case "validations":
			return "validations", true
		}
	case "scalar-list":
		if canonicalIndex(component) {
			return "scalar", true
		}
	case "requirements":
		if canonicalIndex(component) {
			return "requirement", true
		}
	case "requirement":
		switch component {
		case "providerId", "modulePath", "packagePath", "version", "profileId", "manifestDigest", "treeDigest":
			return "scalar", true
		}
	case "validations":
		if canonicalIndex(component) {
			return "validation", true
		}
	case "validation":
		switch component {
		case "id", "kind", "workingDirectory":
			return "scalar", true
		case "packages":
			return "scalar-list", true
		}
	}
	return "", false
}

func canonicalIndex(value string) bool {
	const maxProtocolArrayIndex = uint64(1<<31 - 1)
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	if len(value) > 10 {
		return false
	}
	index, err := strconv.ParseUint(value, 10, 32)
	return err == nil && index <= maxProtocolArrayIndex
}
