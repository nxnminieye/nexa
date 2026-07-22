package servicecatalog

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

const dnsLikeExpression = `[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*`

func validateCatalog(source string, document catalogDocument) []*Error {
	var failures []*Error
	allServiceIDs := make(map[string]struct{}, len(document.Services))
	for _, service := range document.Services {
		allServiceIDs[stringValue(service.ID)] = struct{}{}
	}

	seenIDs := make(map[string]struct{}, len(document.Services))
	seenRoots := make(map[string]struct{}, len(document.Services))
	for serviceIndex, service := range document.Services {
		servicePointer := "/services/" + strconv.Itoa(serviceIndex)
		serviceID := stringValue(service.ID)
		serviceRoot := stringValue(service.Root)
		if !matches(`^`+dnsLikeExpression+`$`, serviceID) {
			failures = append(failures, semanticError("service_id_invalid", source, servicePointer+"/id"))
		}
		if _, duplicate := seenIDs[serviceID]; duplicate {
			failures = append(failures, semanticError("service_id_duplicate", source, servicePointer+"/id"))
		}
		seenIDs[serviceID] = struct{}{}

		if !validServiceRoot(serviceRoot) {
			failures = append(failures, semanticError("service_root_invalid", source, servicePointer+"/root"))
		}
		if _, duplicate := seenRoots[serviceRoot]; duplicate {
			failures = append(failures, semanticError("service_root_duplicate", source, servicePointer+"/root"))
		}
		seenRoots[serviceRoot] = struct{}{}

		seenDependencies := make(map[string]struct{}, len(service.DependsOn))
		for dependencyIndex, dependency := range service.DependsOn {
			pointer := servicePointer + "/dependsOn/" + strconv.Itoa(dependencyIndex)
			if dependency == serviceID {
				failures = append(failures, semanticError("service_dependency_self", source, pointer))
			}
			if _, duplicate := seenDependencies[dependency]; duplicate {
				failures = append(failures, semanticError("service_dependency_duplicate", source, pointer))
			}
			seenDependencies[dependency] = struct{}{}
			if _, exists := allServiceIDs[dependency]; !exists {
				failures = append(failures, semanticError("service_dependency_unknown", source, pointer))
			}
		}

		seenBindings := make(map[string]struct{}, len(service.CapabilityBindings))
		for bindingIndex, binding := range service.CapabilityBindings {
			bindingPointer := servicePointer + "/capabilityBindings/" + strconv.Itoa(bindingIndex)
			bindingID := stringValue(binding.ID)
			bindingAPIVersion := stringValue(binding.APIVersion)
			bindingIDValid := matches(`^`+dnsLikeExpression+`/`+dnsLikeExpression+`$`, bindingID)
			if !bindingIDValid {
				failures = append(failures, semanticError("service_binding_id_invalid", source, bindingPointer+"/id"))
			}
			if _, duplicate := seenBindings[bindingID]; duplicate {
				failures = append(failures, semanticError("service_binding_duplicate", source, bindingPointer+"/id"))
			}
			seenBindings[bindingID] = struct{}{}
			bindingVersionValid := matches(`^`+dnsLikeExpression+`/`+dnsLikeExpression+`/v[1-9][0-9]*$`, bindingAPIVersion)
			if !bindingVersionValid || bindingIDValid && !strings.HasPrefix(bindingAPIVersion, bindingID+"/v") {
				failures = append(failures, semanticError("service_binding_version_invalid", source, bindingPointer+"/apiVersion"))
			}
		}
	}

	cycle := dependencyCycle(document.Services)
	if len(cycle) > 0 {
		err := semanticError("service_dependency_cycle", source, cyclePointer(document.Services, cycle))
		err.cycle = append([]string(nil), cycle...)
		failures = append(failures, err)
	}
	return failures
}

func validServiceRoot(root string) bool {
	if root == "." || !fs.ValidPath(root) || path.Clean(root) != root || strings.ContainsAny(root, "\\\x00") {
		return false
	}
	_, err := provenance.RepositoryRef(root, "service-root")
	return err == nil
}

func matches(expression, value string) bool {
	matched, err := regexp.MatchString(expression, value)
	return err == nil && matched
}

func semanticError(code, source, pointer string) *Error {
	messages := map[string]string{
		"service_id_invalid":              "service catalog contains an invalid service identifier",
		"service_id_duplicate":            "service catalog contains a duplicate service identifier",
		"service_root_invalid":            "service catalog contains an invalid service root",
		"service_root_duplicate":          "service catalog contains a duplicate service root",
		"service_dependency_unknown":      "service catalog contains an unknown dependency",
		"service_dependency_duplicate":    "service catalog contains a duplicate dependency",
		"service_dependency_self":         "service catalog contains a self dependency",
		"service_dependency_cycle":        "service catalog contains a dependency cycle",
		"service_binding_id_invalid":      "service catalog contains an invalid capability binding identifier",
		"service_binding_version_invalid": "service catalog contains an invalid capability binding version",
		"service_binding_duplicate":       "service catalog contains a duplicate capability binding",
	}
	return newError(code, "", source, pointer, messages[code])
}

func dependencyCycle(services []serviceDocument) []string {
	dependencies := make(map[string][]string, len(services))
	ids := make([]string, len(services))
	for index, service := range services {
		serviceID := stringValue(service.ID)
		ids[index] = serviceID
		dependencies[serviceID] = append([]string(nil), service.DependsOn...)
		sort.Strings(dependencies[serviceID])
	}
	sort.Strings(ids)

	state := make(map[string]uint8, len(services))
	positions := make(map[string]int, len(services))
	stack := make([]string, 0, len(services))
	var visit func(string) []string
	visit = func(id string) []string {
		state[id] = 1
		positions[id] = len(stack)
		stack = append(stack, id)
		for _, dependency := range dependencies[id] {
			switch state[dependency] {
			case 0:
				if cycle := visit(dependency); len(cycle) > 0 {
					return cycle
				}
			case 1:
				cycle := append([]string(nil), stack[positions[dependency]:]...)
				return append(cycle, dependency)
			}
		}
		stack = stack[:len(stack)-1]
		delete(positions, id)
		state[id] = 2
		return nil
	}
	for _, id := range ids {
		if state[id] == 0 {
			if cycle := visit(id); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

func cyclePointer(services []serviceDocument, cycle []string) string {
	if len(cycle) < 2 {
		return ""
	}
	for serviceIndex, service := range services {
		if stringValue(service.ID) != cycle[0] {
			continue
		}
		for dependencyIndex, dependency := range service.DependsOn {
			if dependency == cycle[1] {
				return "/services/" + strconv.Itoa(serviceIndex) + "/dependsOn/" + strconv.Itoa(dependencyIndex)
			}
		}
	}
	return ""
}
