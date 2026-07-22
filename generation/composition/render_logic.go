package composition

import "strings"

func renderLogic(modulePath, coreRoot string, operation *operationState) ([]byte, error) {
	prefix := exportedIdentifier(operation.proxy.OperationID())
	alias := packageName(operation.serviceID)
	importPath := strings.TrimSuffix(modulePath, "/") + "/" + strings.Trim(coreRoot, "/") + "/internal/serviceclients/" + operation.serviceID
	source := "package rpcproxy\n\nimport (\n\"context\"\n" + alias + " \"" + importPath + "\"\n)\n" +
		"type " + prefix + "Logic struct { client " + alias + ".RPCClient; contexts " + alias + ".ContextReader }\n" +
		"func New" + prefix + "Logic(client " + alias + ".RPCClient, contexts " + alias + ".ContextReader) *" + prefix + "Logic { return &" + prefix + "Logic{client: client, contexts: contexts} }\n" +
		"func (logic *" + prefix + "Logic) Execute(ctx context.Context, input " + alias + "." + prefix + "HTTPRequest) (" + alias + "." + prefix + "HTTPResponse, error) { values, err := logic.contexts.Read(ctx); if err != nil { return " + alias + "." + prefix + "HTTPResponse{}, err }; request := " + alias + ".Map" + prefix + "Request(input, values); response, err := logic.client." + prefix + "(ctx, request); if err != nil { projected, projectionErr := " + alias + ".Project" + prefix + "Failure(err, values); if projectionErr != nil { return " + alias + "." + prefix + "HTTPResponse{}, projectionErr }; return " + alias + "." + prefix + "HTTPResponse{}, projected }; return " + alias + ".Map" + prefix + "Response(response), nil }\n"
	return formatted(source)
}
