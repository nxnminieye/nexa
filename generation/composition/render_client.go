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
		partial := Document{state: &documentState{coreServiceID: document.state.coreServiceID, consumerModulePath: document.state.consumerModulePath, operations: operations}}
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
		artifacts = append(artifacts, artifact("api."+serviceID, path.Join(options.CoreRoot, "desc/generated", apiFile), apiBytes, sourcesFromHTTP(generated)))
		client, err := renderClient(serviceID, operations)
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
		sources := operationSources(operations)
		artifacts = append(artifacts,
			artifact("client."+serviceID, path.Join(base, "client.generated.go"), client, sources),
			artifact("mapper."+serviceID, path.Join(base, "mapper.generated.go"), mapper, sources),
			artifact("errors."+serviceID, path.Join(base, "errors.generated.go"), errors, sources),
		)
		for _, operation := range operations {
			logic, err := renderLogic(document.state.consumerModulePath, options.CoreRoot, operation)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, artifact("logic."+operation.proxy.OperationID(), path.Join(options.CoreRoot, "internal/logic/rpcproxy", fileID(operation.proxy.OperationID())+".generated.go"), logic, operationSources([]*operationState{operation})))
		}
	}
	register, err := renderRegister(document.state.operations)
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, artifact("register", path.Join(options.CoreRoot, "internal/rpcproxy/generated/register.generated.go"), register, operationSources(document.state.operations)))
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

func renderClient(serviceID string, operations []*operationState) ([]byte, error) {
	var source strings.Builder
	source.WriteString("package " + packageName(serviceID) + "\n\nimport \"context\"\n\n")
	source.WriteString("type RequestContext struct { SubjectID string; TenantID int64; RequestID string; TraceID string }\n")
	source.WriteString("type ContextReader interface { Read(context.Context) (RequestContext, error) }\n")
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
	default:
		return "any"
	}
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
	result := make([]provenance.SourceRef, 0, len(set))
	for _, ref := range set {
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
func sourcesFromHTTP(document httpapi.Document) []provenance.SourceRef {
	values := document.Sources()
	result := make([]provenance.SourceRef, len(values))
	for index, source := range values {
		result[index] = source.Ref
	}
	return result
}
