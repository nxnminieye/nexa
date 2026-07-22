package composition

import "strings"

func renderMapper(serviceID string, operations []*operationState) ([]byte, error) {
	var source strings.Builder
	source.WriteString("package " + packageName(serviceID) + "\n\n")
	for _, operation := range operations {
		prefix := exportedIdentifier(operation.proxy.OperationID())
		source.WriteString("type " + prefix + "HTTPRequest struct {\n")
		for _, binding := range operation.requestFields {
			source.WriteString(exportedIdentifier(binding.httpField) + " " + goType(binding.valueType) + "\n")
		}
		source.WriteString("}\n")
		source.WriteString("type " + prefix + "HTTPResponse struct {\n")
		for _, binding := range operation.responseFields {
			source.WriteString(exportedIdentifier(binding.httpField) + " " + goType(binding.valueType) + "\n")
		}
		source.WriteString("}\n")
		source.WriteString("func Map" + prefix + "Request(input " + prefix + "HTTPRequest, values RequestContext) " + prefix + "RPCRequest {\nreturn " + prefix + "RPCRequest{\n")
		for _, binding := range operation.requestFields {
			source.WriteString(rpcFieldName(binding) + ": input." + exportedIdentifier(binding.httpField) + ",\n")
		}
		for _, binding := range operation.contextFields {
			source.WriteString(rpcFieldName(binding) + ": values." + contextField(binding) + ",\n")
		}
		source.WriteString("}\n}\n")
		source.WriteString("func Map" + prefix + "Response(input " + prefix + "RPCResponse) " + prefix + "HTTPResponse {\nreturn " + prefix + "HTTPResponse{\n")
		for _, binding := range operation.responseFields {
			source.WriteString(exportedIdentifier(binding.httpField) + ": input." + rpcFieldName(binding) + ",\n")
		}
		source.WriteString("}\n}\n")
	}
	return formatted(source.String())
}

func contextField(binding resolvedBinding) string {
	switch binding.context {
	case "subject-id":
		return "SubjectID"
	case "tenant-id":
		return "TenantID"
	case "request-id":
		return "RequestID"
	case "trace-id":
		return "TraceID"
	default:
		return ""
	}
}
