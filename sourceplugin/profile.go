package sourceplugin

import (
	"sort"
	"strconv"
)

type ProfileClosure struct {
	rootProfileID        string
	profileIDs           []string
	files                []File
	requirements         []BundleRequirement
	goModuleRequirements []GoModuleRequirement
	validations          []ValidationRecipe
}

func (c ProfileClosure) RootProfileID() string { return c.rootProfileID }
func (c ProfileClosure) ProfileIDs() []string  { return append([]string(nil), c.profileIDs...) }
func (c ProfileClosure) Files() []File         { return append([]File(nil), c.files...) }
func (c ProfileClosure) BundleRequirements() []BundleRequirement {
	return append([]BundleRequirement(nil), c.requirements...)
}
func (c ProfileClosure) GoModuleRequirements() []GoModuleRequirement {
	return append([]GoModuleRequirement(nil), c.goModuleRequirements...)
}
func (c ProfileClosure) Validations() []ValidationRecipe {
	result := make([]ValidationRecipe, len(c.validations))
	for index, recipe := range c.validations {
		result[index] = cloneValidation(recipe)
	}
	return result
}

func (m Manifest) ResolveProfile(id string) (ProfileClosure, error) {
	if !validStableID(id) {
		return ProfileClosure{}, newSourceError("source_profile_invalid", "profile_id_invalid", "/profile")
	}
	if _, ok := m.profileIndex[id]; !ok {
		return ProfileClosure{}, newSourceError("source_profile_not_found", "profile_not_found", "/profile")
	}
	profileIDs := m.profilePostorder(id)
	closure := ProfileClosure{rootProfileID: id, profileIDs: profileIDs}
	fileSet := make(map[string]File)
	requirementByFullKey := make(map[string]BundleRequirement)
	requirementByIdentity := make(map[string]BundleRequirement)
	goModuleRequirementByPath := make(map[string]GoModuleRequirement)
	for _, profileID := range profileIDs {
		profileIndex := m.profileIndex[profileID]
		profile := m.profiles[profileIndex]
		for _, path := range profile.filePaths {
			fileSet[path] = m.files[m.fileIndex[path]]
		}
		for requirementIndex, requirement := range profile.requirements {
			identityKey := requirementValueIdentityKey(requirement)
			fullKey := requirementValueFullKey(requirement)
			if previous, ok := requirementByIdentity[identityKey]; ok && requirementValueFullKey(previous) != fullKey {
				err := newSourceError(
					"source_bundle_requirement_invalid", "requirement_conflict",
					"/profiles/"+strconv.Itoa(profileIndex)+"/requiresBundles/"+strconv.Itoa(requirementIndex),
				)
				key := profileID + "\x00" + fullKey
				if location, ok := m.diagnostics.requirements[key]; ok {
					err = withLocation(err, m.diagnostics.source, location.line, location.column)
				}
				return ProfileClosure{}, err
			}
			requirementByIdentity[identityKey] = requirement
			requirementByFullKey[fullKey] = requirement
		}
		for requirementIndex, requirement := range profile.goModuleRequirements {
			if previous, ok := goModuleRequirementByPath[requirement.modulePath]; ok && previous.version != requirement.version {
				err := newSourceError(
					"source_go_module_requirement_invalid", "requirement_conflict",
					"/profiles/"+strconv.Itoa(profileIndex)+"/requiresGoModules/"+strconv.Itoa(requirementIndex),
				)
				key := goModuleRequirementDiagnosticKey(profileID, GoModuleRequirementSpec{ModulePath: requirement.modulePath, Version: requirement.version})
				if location, ok := m.diagnostics.goModuleRequirements[key]; ok {
					err = withLocation(err, m.diagnostics.source, location.line, location.column)
				}
				return ProfileClosure{}, err
			}
			goModuleRequirementByPath[requirement.modulePath] = requirement
		}
		for _, recipe := range profile.validations {
			closure.validations = append(closure.validations, cloneValidation(recipe))
		}
	}
	paths := make([]string, 0, len(fileSet))
	for path := range fileSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	closure.files = make([]File, len(paths))
	for index, path := range paths {
		closure.files[index] = fileSet[path]
	}
	requirementKeys := make([]string, 0, len(requirementByFullKey))
	for key := range requirementByFullKey {
		requirementKeys = append(requirementKeys, key)
	}
	sort.Strings(requirementKeys)
	closure.requirements = make([]BundleRequirement, len(requirementKeys))
	for index, key := range requirementKeys {
		closure.requirements[index] = requirementByFullKey[key]
	}
	goModulePaths := make([]string, 0, len(goModuleRequirementByPath))
	for modulePath := range goModuleRequirementByPath {
		goModulePaths = append(goModulePaths, modulePath)
	}
	sort.Strings(goModulePaths)
	closure.goModuleRequirements = make([]GoModuleRequirement, len(goModulePaths))
	for index, modulePath := range goModulePaths {
		closure.goModuleRequirements[index] = goModuleRequirementByPath[modulePath]
	}
	return closure, nil
}

func (m Manifest) profilePostorder(root string) []string {
	visited := make(map[string]bool, len(m.profiles))
	result := make([]string, 0, len(m.profiles))
	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		profile := m.profiles[m.profileIndex[id]]
		for _, dependency := range profile.requiredProfiles {
			visit(dependency)
		}
		result = append(result, id)
	}
	visit(root)
	return result
}

func profileCycle(profiles []ProfileSpec) (cycle []string, pointer, from, to string) {
	indexByID := make(map[string]int, len(profiles))
	for index, profile := range profiles {
		indexByID[profile.ID] = index
	}
	state := make(map[string]uint8, len(profiles))
	positions := make(map[string]int, len(profiles))
	stack := make([]string, 0, len(profiles))
	var visit func(string) bool
	visit = func(id string) bool {
		state[id] = 1
		positions[id] = len(stack)
		stack = append(stack, id)
		profileIndex := indexByID[id]
		for dependencyIndex, dependency := range profiles[profileIndex].RequiresProfiles {
			switch state[dependency] {
			case 0:
				if visit(dependency) {
					return true
				}
			case 1:
				cycle = append([]string(nil), stack[positions[dependency]:]...)
				cycle = append(cycle, dependency)
				cycle = rotateCycleToSmallest(cycle)
				pointer = "/profiles/" + strconv.Itoa(profileIndex) + "/requiresProfiles/" + strconv.Itoa(dependencyIndex)
				from, to = id, dependency
				return true
			}
		}
		stack = stack[:len(stack)-1]
		delete(positions, id)
		state[id] = 2
		return false
	}
	for _, profile := range profiles {
		if state[profile.ID] == 0 && visit(profile.ID) {
			return cycle, pointer, from, to
		}
	}
	return nil, "", "", ""
}

func rotateCycleToSmallest(cycle []string) []string {
	if len(cycle) < 2 {
		return append([]string(nil), cycle...)
	}
	unique := cycle[:len(cycle)-1]
	minimum := 0
	for index := 1; index < len(unique); index++ {
		if unique[index] < unique[minimum] {
			minimum = index
		}
	}
	result := make([]string, 0, len(cycle))
	result = append(result, unique[minimum:]...)
	result = append(result, unique[:minimum]...)
	return append(result, result[0])
}
