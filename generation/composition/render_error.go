package composition

import (
	"strconv"
	"strings"
)

func renderErrors(serviceID string, operations []*operationState) ([]byte, error) {
	var source strings.Builder
	source.WriteString("package " + packageName(serviceID) + "\n\nimport (\n\"errors\"\n\"net/http\"\nsdkapi \"github.com/nxnminieye/nexa/sdk/api\"\n)\n\n")
	source.WriteString("type RPCError struct { Domain string; Code string; Message string; DetailsJSON []byte }\n")
	source.WriteString("func (RPCError) Error() string { return \"rpc request failed\" }\n")
	source.WriteString("type HTTPError struct { Status int; ContentType string; Body []byte }\n")
	source.WriteString("func (HTTPError) Error() string { return \"remote request failed\" }\n")
	source.WriteString("func (value HTTPError) WriteHTTP(writer http.ResponseWriter) error { writer.Header().Set(\"Content-Type\", value.ContentType); writer.WriteHeader(value.Status); _, err := writer.Write(value.Body); return err }\n")
	for _, operation := range operations {
		prefix := exportedIdentifier(operation.proxy.OperationID())
		source.WriteString("func Project" + prefix + "Failure(input error, values RequestContext) (HTTPError, error) { var typed RPCError; if !errors.As(input, &typed) { typed = RPCError{} }; return Project" + prefix + "Error(typed, values) }\n")
		source.WriteString("func Project" + prefix + "Error(input RPCError, values RequestContext) (HTTPError, error) {\n")
		source.WriteString("domain, code, message, status := \"internal\", \"internal\", \"internal error\", 500\n")
		for index, projection := range operation.errorProjections {
			keyword := "if"
			if index > 0 {
				keyword = " else if"
			}
			source.WriteString(keyword + " input.Domain == " + strconv.Quote(projection.Match.Domain) + " && input.Code == " + strconv.Quote(projection.Match.Code) + " { domain, code, message, status = " + strconv.Quote(projection.Project.Domain) + ", " + strconv.Quote(projection.Project.Code) + ", \"request failed\", " + strconv.Itoa(projection.Project.HTTPStatus) + " }")
		}
		if len(operation.errorProjections) > 0 {
			source.WriteString("\n")
		}
		source.WriteString("remote, err := sdkapi.NewRemoteError(sdkapi.RemoteErrorSpec{Domain: domain, Code: code, Message: message, RequestID: values.RequestID, TraceID: values.TraceID})\nif err != nil { return HTTPError{}, err }\nbody, err := remote.CanonicalJSON()\nif err != nil { return HTTPError{}, err }\nreturn HTTPError{Status: status, ContentType: \"application/json\", Body: body}, nil\n}\n")
	}
	return formatted(source.String())
}
