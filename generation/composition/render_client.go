package composition

import (
	"go/format"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/provenance"
)

const artifactOwner = "nexa.dev/generator/composition/v1"

func Render(document Document, options RenderOptions) ([]RenderedArtifact, error) {
	if document.state == nil {
		return nil, invalid("document_invalid", "", "/document", "composition document is invalid")
	}
	if !fs.ValidPath(options.CoreRoot) || options.CoreRoot == "." {
		return nil, invalid("core_root_invalid", "", "/options/coreRoot", "Core root is invalid")
	}
	byService := map[string][]*operationState{}
	for _, operation := range document.state.operations {
		byService[operation.serviceID] = append(byService[operation.serviceID], operation)
	}
	serviceIDs := make([]string, 0, len(byService))
	for serviceID := range byService {
		serviceIDs = append(serviceIDs, serviceID)
	}
	sort.Strings(serviceIDs)
	artifacts := make([]RenderedArtifact, 0)
	for _, serviceID := range serviceIDs {
		operations := byService[serviceID]
		types := reachableProjectedTypes(document.state.types, operations)
		partial := Document{state: &documentState{coreServiceID: document.state.coreServiceID, consumerModulePath: document.state.consumerModulePath, operations: operations, types: types}}
		generated, err := GeneratedAPI(partial)
		if err != nil {
			return nil, err
		}
		apiBytes, err := httpapi.RenderGenerated(generated)
		if err != nil {
			return nil, invalid("api_render_failed", "", "", "generated API source is invalid")
		}
		apiFile := serviceID + ".generated.api"
		if serviceID == document.state.coreServiceID {
			apiFile = serviceID + ".proxy.generated.api"
		}
		sources := unionSources(operationSources(operations), typeSources(types))
		artifacts = append(artifacts, artifact("api."+serviceID, path.Join(options.CoreRoot, "desc/generated", apiFile), apiBytes, sources))
		client, err := renderClient(serviceID, operations, types)
		if err != nil {
			return nil, err
		}
		mapper, err := renderMapper(serviceID, operations)
		if err != nil {
			return nil, err
		}
		errors, err := renderErrors(serviceID, operations)
		if err != nil {
			return nil, err
		}
		base := path.Join(options.CoreRoot, "internal/serviceclients", serviceID)
		artifacts = append(artifacts,
			artifact("client."+serviceID, path.Join(base, "client.generated.go"), client, sources),
			artifact("mapper."+serviceID, path.Join(base, "mapper.generated.go"), mapper, sources),
			artifact("errors."+serviceID, path.Join(base, "errors.generated.go"), errors, operationSources(operations)),
		)
		for _, operation := range operations {
			logic, err := renderLogic(document.state.consumerModulePath, options.CoreRoot, operation)
			if err != nil {
				return nil, err
			}
			operationTypes := reachableProjectedTypes(document.state.types, []*operationState{operation})
			artifacts = append(artifacts, artifact("logic."+operation.proxy.OperationID(), path.Join(options.CoreRoot, "internal/logic/rpcproxy", fileID(operation.proxy.OperationID())+".generated.go"), logic, unionSources(operationSources([]*operationState{operation}), typeSources(operationTypes))))
		}
	}
	register, err := renderRegister(document.state.operations)
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, artifact("register", path.Join(options.CoreRoot, "internal/rpcproxy/generated/register.generated.go"), register, unionSources(operationSources(document.state.operations), typeSources(document.state.types))))
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

func artifact(id, artifactPath string, content []byte, sources []provenance.SourceRef) RenderedArtifact {
	return RenderedArtifact{ID: id, Path: artifactPath, Owner: artifactOwner, Content: append([]byte(nil), content...), Sources: append([]provenance.SourceRef(nil), sources...)}
}

func formatted(source string) ([]byte, error) {
	result, err := format.Source([]byte(source))
	if err != nil {
		return nil, invalid("go_render_invalid", "", "", err.Error())
	}
	return result, nil
}

func renderClient(serviceID string, operations []*operationState, types []*projectedTypeState) ([]byte, error) {
	var source strings.Builder
	tenantType := "int64"
	if byService, err := tenantTypesByService(operations); err != nil {
		return nil, err
	} else if value := byService[serviceID]; value != "" {
		tenantType = value
	}
	source.WriteString("package " + packageName(serviceID) + "\n\nimport \"context\"\n\n")
	source.WriteString("type RequestContext struct { SubjectID string; TenantID " + tenantType + "; RequestID string; TraceID string }\n")
	source.WriteString("type ContextReader interface { Read(context.Context) (RequestContext, error) }\n")
	for _, projected := range types {
		source.WriteString("type " + projected.name + " struct {\n")
		for _, field := range projected.fields {
			source.WriteString(exportedIdentifier(field.jsonName) + " " + goType(field.valueType) + " `json:\"" + field.jsonName + "\"`\n")
		}
		source.WriteString("}\n")
	}
	for _, operation := range operations {
		prefix := exportedIdentifier(operation.proxy.OperationID())
		source.WriteString("type " + prefix + "RPCRequest struct {\n")
		for _, binding := range append(append([]resolvedBinding{}, operation.requestFields...), operation.contextFields...) {
			source.WriteString(rpcFieldName(binding) + " " + goType(binding.valueType) + "\n")
		}
		source.WriteString("}\n")
		source.WriteString("type " + prefix + "RPCResponse struct {\n")
		for _, binding := range operation.responseFields {
			source.WriteString(rpcFieldName(binding) + " " + goType(binding.valueType) + "\n")
		}
		source.WriteString("}\n")
	}
	source.WriteString("type RPCClient interface {\n")
	for _, operation := range operations {
		prefix := exportedIdentifier(operation.proxy.OperationID())
		source.WriteString(prefix + "(context.Context, " + prefix + "RPCRequest) (" + prefix + "RPCResponse, error)\n")
	}
	source.WriteString("}\n")
	return formatted(source.String())
}

func packageName(serviceID string) string { return strings.ReplaceAll(serviceID, "-", "") + "client" }
func fileID(value string) string          { return strings.ReplaceAll(value, ".", "-") }
func rpcFieldName(binding resolvedBinding) string {
	var result string
	for _, field := range binding.fields {
		result += exportedIdentifier(field.Name())
	}
	return result
}
func goType(value httpapi.ValueTypeSpec) string {
	switch value.Kind {
	case httpapi.ValueOptional:
		return "*" + goType(*value.Element)
	case httpapi.ValueArray:
		return "[]" + goType(*value.Element)
	case httpapi.ValueScalar:
		if value.Name == "bytes" {
			return "[]byte"
		}
		return value.Name
	case httpapi.ValueRef:
		return value.Name
	default:
		return "any"
	}
}

func reachableProjectedTypes(all []*projectedTypeState, operations []*operationState) []*projectedTypeState {
	index := make(map[string]*projectedTypeState, len(all))
	for _, projected := range all {
		index[projected.name] = projected
	}
	reachable := map[string]bool{}
	var visit func(httpapi.ValueTypeSpec)
	visit = func(value httpapi.ValueTypeSpec) {
		if value.Kind == httpapi.ValueRef && !reachable[value.Name] {
			projected := index[value.Name]
			if projected == nil {
				return
			}
			reachable[value.Name] = true
			for _, field := range projected.fields {
				visit(field.valueType)
			}
		}
		if value.Element != nil {
			visit(*value.Element)
		}
	}
	for _, operation := range operations {
		for _, bindings := range [][]resolvedBinding{operation.requestFields, operation.responseFields} {
			for _, binding := range bindings {
				visit(binding.valueType)
			}
		}
	}
	result := make([]*projectedTypeState, 0, len(reachable))
	for _, projected := range all {
		if reachable[projected.name] {
			result = append(result, projected)
		}
	}
	return result
}

func typeSources(types []*projectedTypeState) []provenance.SourceRef {
	set := map[string]provenance.SourceRef{}
	for _, projected := range types {
		for _, source := range projected.provenance.Sources() {
			set[source.Ref.String()] = source.Ref
		}
		for _, field := range projected.fields {
			for _, source := range field.provenance.Sources() {
				set[source.Ref.String()] = source.Ref
			}
		}
	}
	return sortedSourceSet(set)
}

func unionSources(values ...[]provenance.SourceRef) []provenance.SourceRef {
	set := map[string]provenance.SourceRef{}
	for _, refs := range values {
		for _, ref := range refs {
			set[ref.String()] = ref
		}
	}
	return sortedSourceSet(set)
}

func sortedSourceSet(set map[string]provenance.SourceRef) []provenance.SourceRef {
	result := make([]provenance.SourceRef, 0, len(set))
	for _, ref := range set {
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
func operationSources(operations []*operationState) []provenance.SourceRef {
	set := map[string]provenance.SourceRef{}
	for _, operation := range operations {
		for _, source := range operation.operationProvenance.Sources() {
			set[source.Ref.String()] = source.Ref
		}
		for _, bindings := range [][]resolvedBinding{operation.requestFields, operation.contextFields, operation.responseFields} {
			for _, binding := range bindings {
				for _, field := range binding.fields {
					set[field.Source().Ref.String()] = field.Source().Ref
				}
			}
		}
	}
	return sortedSourceSet(set)
}
