package protocol

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

// Merge combines independently compiled ProtocolIR documents for one service
// and re-runs the global FactGraph checks across the complete source set.
func Merge(documents ...Document) (Document, error) {
	if len(documents) == 0 {
		return Document{}, protocolError("protocol_merge_failed", "documents_missing", "", "/documents", "at least one Protocol document is required")
	}
	serviceID := ""
	state := &documentState{
		messages: map[string]*messageState{},
		enums:    map[string]*enumState{},
		services: map[string]*serviceState{},
		methods:  map[string]*methodState{},
	}
	files := map[string]*fileState{}
	fileServices := map[string]map[string]*serviceState{}
	messageOwners, enumOwners, methodOwners := map[string]string{}, map[string]string{}, map[string]string{}
	graphs := make([]sourcecomment.FactGraph, 0, len(documents))

	for index, document := range documents {
		if document.state == nil || !document.state.factGraph.Valid() {
			return Document{}, protocolError("protocol_merge_failed", "document_invalid", "", fmt.Sprintf("/documents/%d", index), "merge inputs must be validated Protocol documents")
		}
		if serviceID == "" {
			serviceID = document.state.serviceID
		} else if document.state.serviceID != serviceID {
			return Document{}, protocolError("protocol_merge_failed", "service_id_mismatch", "", fmt.Sprintf("/documents/%d/serviceID", index), "merged Protocol documents must have the same service id")
		}
		graphs = append(graphs, document.state.factGraph)

		for _, file := range document.state.files {
			target := files[file.path]
			if target == nil {
				target = &fileState{path: file.path}
				files[file.path] = target
				fileServices[file.path] = map[string]*serviceState{}
			}
			for _, message := range file.messages {
				if previous := state.messages[message.fullName]; previous != nil {
					if messageOwners[message.fullName] != file.path || !equalMessage(previous, message) {
						return Document{}, mergeConflict("message_conflict", file.path, messageOwners[message.fullName], "Proto message identity is declared by multiple sources")
					}
					continue
				}
				copy := cloneMessage(message)
				messageOwners[copy.fullName], state.messages[copy.fullName] = file.path, copy
				target.messages = append(target.messages, copy)
			}
			for _, enum := range file.enums {
				if previous := state.enums[enum.fullName]; previous != nil {
					if enumOwners[enum.fullName] != file.path || !equalEnum(previous, enum) {
						return Document{}, mergeConflict("enum_conflict", file.path, enumOwners[enum.fullName], "Proto enum identity is declared by multiple sources")
					}
					continue
				}
				copy := cloneEnum(enum)
				enumOwners[copy.fullName], state.enums[copy.fullName] = file.path, copy
				target.enums = append(target.enums, copy)
			}
			for _, service := range file.services {
				fileService := fileServices[file.path][service.fullName]
				if fileService == nil {
					fileService = &serviceState{fullName: service.fullName, filePath: file.path, location: service.location}
					fileServices[file.path][service.fullName], target.services = fileService, append(target.services, fileService)
				}
				globalService := state.services[service.fullName]
				if globalService == nil {
					globalService = &serviceState{fullName: service.fullName, filePath: file.path, location: service.location}
					state.services[service.fullName] = globalService
				} else if file.path < globalService.filePath {
					globalService.filePath, globalService.location = file.path, service.location
				}
				for _, method := range service.methods {
					if previous := state.methods[method.fullName]; previous != nil {
						if methodOwners[method.fullName] != file.path || !equalMethod(previous, method) {
							return Document{}, mergeConflict("method_conflict", file.path, methodOwners[method.fullName], "Proto RPC identity is declared by multiple sources")
						}
						continue
					}
					fileMethod := cloneMethod(method)
					globalMethod := cloneMethod(method)
					methodOwners[method.fullName], state.methods[method.fullName] = file.path, globalMethod
					fileService.methods = append(fileService.methods, fileMethod)
					globalService.methods = append(globalService.methods, globalMethod)
				}
			}
		}
	}

	state.serviceID = serviceID
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		file := files[filePath]
		sort.Slice(file.messages, func(i, j int) bool { return file.messages[i].fullName < file.messages[j].fullName })
		sort.Slice(file.enums, func(i, j int) bool { return file.enums[i].fullName < file.enums[j].fullName })
		for _, service := range file.services {
			sort.Slice(service.methods, func(i, j int) bool { return service.methods[i].fullName < service.methods[j].fullName })
		}
		sort.Slice(file.services, func(i, j int) bool { return file.services[i].fullName < file.services[j].fullName })
		state.files = append(state.files, file)
	}
	for _, service := range state.services {
		sort.Slice(service.methods, func(i, j int) bool { return service.methods[i].fullName < service.methods[j].fullName })
	}
	if err := finalizeSources(state); err != nil {
		return Document{}, err
	}
	facts, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), graphs...)
	if len(diagnostics) > 0 {
		return Document{}, protocolError("protocol_merge_failed", "fact_graph_conflict", diagnostics[0].File, "", diagnostics[0].Suggestion)
	}
	state.factGraph = facts
	return Document{state: state}, nil
}

func mergeConflict(reason, current, previous, message string) *Error {
	source := current
	if previous > source {
		source = previous
	}
	return protocolError("protocol_merge_failed", reason, source, "", message)
}

func equalMessage(left, right *messageState) bool {
	if left.fullName != right.fullName || len(left.fields) != len(right.fields) {
		return false
	}
	for index := range left.fields {
		if !bytes.Equal(left.fields[index].canonicalSource, right.fields[index].canonicalSource) {
			return false
		}
	}
	return true
}

func equalEnum(left, right *enumState) bool {
	if left.fullName != right.fullName || len(left.values) != len(right.values) {
		return false
	}
	for index := range left.values {
		if left.values[index].name != right.values[index].name || left.values[index].number != right.values[index].number {
			return false
		}
	}
	return true
}

func equalMethod(left, right *methodState) bool {
	return bytes.Equal(left.canonicalSource, right.canonicalSource)
}

func cloneMessage(value *messageState) *messageState {
	result := *value
	result.canonicalSource = append([]byte(nil), value.canonicalSource...)
	result.fields = make([]*fieldState, len(value.fields))
	for index, field := range value.fields {
		copy := *field
		copy.canonicalSource = append([]byte(nil), field.canonicalSource...)
		copy.typeValue = cloneType(field.typeValue)
		result.fields[index] = &copy
	}
	return &result
}

func cloneEnum(value *enumState) *enumState {
	result := *value
	result.values = make([]*enumValueState, len(value.values))
	for index, item := range value.values {
		copy := *item
		result.values[index] = &copy
	}
	return &result
}

func cloneMethod(value *methodState) *methodState {
	result := *value
	result.canonicalSource = append([]byte(nil), value.canonicalSource...)
	return &result
}

func cloneType(value *typeState) *typeState {
	if value == nil {
		return nil
	}
	result := *value
	result.key, result.value = cloneType(value.key), cloneType(value.value)
	return &result
}
