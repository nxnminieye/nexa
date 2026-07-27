package protocol_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func TestCompileDecodesAndValidatesCompleteHTTPProxy(t *testing.T) {
	document := compileProtocol(t, validProxyProto(""))
	method, ok := document.Method("sample.v1.SampleService.GetSample")
	if !ok {
		t.Fatal("method missing")
	}
	proxy, ok := method.HTTPProxy()
	if !ok || proxy.OperationID() != "sample.get" || proxy.Method() != protocol.MethodGET || proxy.Path() != "/samples/{id}" || proxy.Permission() != "sample.read" {
		t.Fatalf("HTTPProxy() = %#v, %v", proxy, ok)
	}
	auth := proxy.Auth()
	credentials := auth.Credentials()
	if auth.Mode() != protocol.AuthRequired || len(credentials) != 1 || credentials[0].ID() != "primary" || credentials[0].Type() != protocol.CredentialBearer || credentials[0].Location() != protocol.CredentialHeader || credentials[0].Name() != "authorization" {
		t.Fatalf("Auth() = %#v", auth)
	}
	requests := proxy.RequestFields()
	rpcContext := method.RPCContext()
	contexts := rpcContext.ContextFields()
	responses := proxy.ResponseFields()
	errors := proxy.Errors()
	if len(requests) != 1 || requests[0].HTTPField() != "id" || !sameStrings(requests[0].RPCPath(), []string{"sample.v1.GetSampleRequest#1"}) {
		t.Fatalf("RequestFields() = %#v", requests)
	}
	if len(contexts) != 1 || contexts[0].Source() != protocol.ContextTenantID || !sameStrings(contexts[0].RPCPath(), []string{"sample.v1.GetSampleRequest#2"}) {
		t.Fatalf("RPCContext().ContextFields() = %#v", contexts)
	}
	if len(responses) != 1 || responses[0].HTTPField() != "displayName" || !sameStrings(responses[0].RPCPath(), []string{"sample.v1.GetSampleResponse#1"}) {
		t.Fatalf("ResponseFields() = %#v", responses)
	}
	if len(errors) != 1 || errors[0].Match().Domain() != "sample" || errors[0].Match().Code() != "not_found" || errors[0].Project().Domain() != "api" || errors[0].Project().Code() != "sample_not_found" || errors[0].Project().HTTPStatus() != 404 {
		t.Fatalf("Errors() = %#v", errors)
	}
	requests[0] = protocol.RequestFieldBinding{}
	if proxy.RequestFields()[0].HTTPField() != "id" {
		t.Fatal("HTTPProxy collections alias immutable state")
	}
}

func TestCompileMethodRPCContextWithoutHTTPProxy(t *testing.T) {
	source := `syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message GetSampleRequest { int64 tenant_id = 1; }
message GetSampleResponse {}
service SampleService {
  rpc GetSample(GetSampleRequest) returns (GetSampleResponse) {
    option (nexa.protocol.v1.rpc_context) = {
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
    };
  }
}`
	document := compileProtocol(t, source)
	method, ok := document.Method("sample.v1.SampleService.GetSample")
	if !ok {
		t.Fatal("method missing")
	}
	if _, ok := method.HTTPProxy(); ok {
		t.Fatal("HTTPProxy() unexpectedly present")
	}
	fields := method.RPCContext().ContextFields()
	if len(fields) != 1 || fields[0].Source() != protocol.ContextTenantID {
		t.Fatalf("RPCContext().ContextFields() = %#v", fields)
	}
}

func TestCompileRejectsLegacyHTTPProxyContextFields(t *testing.T) {
	source := replaceOnce(validProxyProto(""), `      request_fields: { http_field: "id" rpc_field: "id" }`, `      request_fields: { http_field: "id" rpc_field: "id" }
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }`)
	_, err := protocol.Compile(context.Background(), compileOptions(source))
	if err == nil {
		t.Fatal("Compile() accepted legacy HTTPProxy context_fields")
	}
}

func TestTenantContextRequiresSingularInt64(t *testing.T) {
	for name, declaration := range map[string]string{
		"string":   "string tenant_id = 2;",
		"int32":    "int32 tenant_id = 2;",
		"repeated": "repeated int64 tenant_id = 2;",
		"optional": "optional int64 tenant_id = 2;",
	} {
		t.Run(name, func(t *testing.T) {
			source := replaceOnce(validProxyProto(""), "int64 tenant_id = 2;", declaration)
			_, err := protocol.Compile(context.Background(), compileOptions(source))
			owner, ok := err.(*protocol.Error)
			if !ok || owner.Code() != "protocol_ir_invalid" || owner.Reason() != "context_field_type_invalid" {
				t.Fatalf("Compile() error = %#v", err)
			}
		})
	}
}

func TestNonTenantContextRequiresSingularString(t *testing.T) {
	for name, declaration := range map[string]string{
		"int64":    "int64 request_id = 3;",
		"repeated": "repeated string request_id = 3;",
		"optional": "optional string request_id = 3;",
	} {
		t.Run(name, func(t *testing.T) {
			source := replaceOnce(validProxyProto(""), "int64 tenant_id = 2;", "int64 tenant_id = 2; string request_id = 3;")
			source = replaceOnce(source, "string request_id = 3;", declaration)
			source = replaceOnce(source, `      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }`, `      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
      context_fields: { source: REQUEST_ID rpc_field: "request_id" }`)
			_, err := protocol.Compile(context.Background(), compileOptions(source))
			owner, ok := err.(*protocol.Error)
			if !ok || owner.Code() != "protocol_ir_invalid" || owner.Reason() != "context_field_type_invalid" {
				t.Fatalf("Compile() error = %#v", err)
			}
		})
	}
}

func TestRouteVariablesAllowAdditionalRequestFields(t *testing.T) {
	source := replaceOnce(validProxyProto(""), "int64 tenant_id = 2;", "int64 tenant_id = 2; string filter = 3;")
	source = replaceOnce(source, `request_fields: { http_field: "id" rpc_field: "id" }`, `request_fields: { http_field: "id" rpc_field: "id" }
      request_fields: { http_field: "filter" rpc_field: "filter" }`)
	document := compileProtocol(t, source)
	method, _ := document.Method("sample.v1.SampleService.GetSample")
	proxy, _ := method.HTTPProxy()
	if fields := proxy.RequestFields(); len(fields) != 2 || fields[0].HTTPField() != "filter" || fields[1].HTTPField() != "id" {
		t.Fatalf("RequestFields() = %#v", fields)
	}
	canonical, err := protocol.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := provenance.ParseDomainSource("generated/protocol-extra-request.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.ParseSnapshot(domain, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("Snapshot.CanonicalJSON() = %s, %v", roundTrip, err)
	}
}

func TestCompileAcceptsBodyCollectionsAndObjectTerminals(t *testing.T) {
	source := `syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message Settings { string locale = 1; repeated string groups = 2; }
message Item { string id = 1; repeated string role_codes = 2; Settings settings = 3; }
message Request { repeated string role_codes = 1; Settings settings = 2; repeated Item items = 3; }
message Response { int64 total = 1; repeated Item items = 2; }
service SampleService {
  rpc Replace(Request) returns (Response) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "sample.replace" method: POST path: "/samples"
      auth: { mode: NONE }
      request_fields: { http_field: "roleCodes" rpc_field: "role_codes" }
      request_fields: { http_field: "settings" rpc_field: "settings" }
      request_fields: { http_field: "items" rpc_field: "items" }
      response_fields: { rpc_field: "total" http_field: "total" }
      response_fields: { rpc_field: "items" http_field: "items" }
    };
  }
}`
	document := compileProtocol(t, source)
	method, ok := document.Method("sample.v1.SampleService.Replace")
	if !ok {
		t.Fatal("method missing")
	}
	proxy, ok := method.HTTPProxy()
	if !ok || len(proxy.RequestFields()) != 3 || len(proxy.ResponseFields()) != 2 {
		t.Fatalf("HTTP proxy = %#v, %v", proxy, ok)
	}
}

func TestCompileRejectsCollectionAndObjectOutsideBody(t *testing.T) {
	base := `syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message Item { string id = 1; }
message Request { repeated string values = 1; Item item = 2; }
message Response { string ok = 1; }
service SampleService {
  rpc Get(Request) returns (Response) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "sample.get" method: GET path: "/samples"
      auth: { mode: NONE }
      request_fields: { http_field: "values" rpc_field: "values" }
      request_fields: { http_field: "item" rpc_field: "item" }
      response_fields: { rpc_field: "ok" http_field: "ok" }
    };
  }
}`
	_, err := protocol.Compile(context.Background(), compileOptions(base))
	owner, ok := err.(*protocol.Error)
	if !ok || owner.Reason() != "body_binding_required" {
		t.Fatalf("query collection error = %#v", err)
	}
	pathSource := strings.Replace(base, `method: GET path: "/samples"`, `method: POST path: "/samples/{values}"`, 1)
	_, err = protocol.Compile(context.Background(), compileOptions(pathSource))
	owner, ok = err.(*protocol.Error)
	if !ok || owner.Reason() != "body_binding_required" {
		t.Fatalf("path collection error = %#v", err)
	}
}

func TestCompileRejectsUnsupportedBindingTerminals(t *testing.T) {
	for name, declaration := range map[string]string{
		"map":   "map<string, string> value = 1;",
		"oneof": "oneof choice { string value = 1; int64 other = 2; }",
	} {
		t.Run(name, func(t *testing.T) {
			source := `syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message Request { ` + declaration + ` }
message Response { string ok = 1; }
service SampleService { rpc Put(Request) returns (Response) {
  option (nexa.protocol.v1.http_proxy) = { operation_id: "sample.put" method: POST path: "/samples" auth: { mode: NONE }
    request_fields: { http_field: "value" rpc_field: "value" }
    response_fields: { rpc_field: "ok" http_field: "ok" }
  };
} }`
			_, err := protocol.Compile(context.Background(), compileOptions(source))
			owner, ok := err.(*protocol.Error)
			if !ok || owner.Reason() != "binding_terminal_unsupported" {
				t.Fatalf("Compile() error = %#v", err)
			}
		})
	}
}

func TestCompileRejectsRepeatedIntermediateTraversal(t *testing.T) {
	source := `syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message Item { string id = 1; }
message Request { repeated Item items = 1; }
message Response { string ok = 1; }
service SampleService { rpc Put(Request) returns (Response) {
  option (nexa.protocol.v1.http_proxy) = { operation_id: "sample.put" method: POST path: "/samples" auth: { mode: NONE }
    request_fields: { http_field: "id" rpc_field: "items.id" }
    response_fields: { rpc_field: "ok" http_field: "ok" }
  };
} }`
	_, err := protocol.Compile(context.Background(), compileOptions(source))
	owner, ok := err.(*protocol.Error)
	if !ok || owner.Reason() != "rpc_field_unresolved" {
		t.Fatalf("Compile() error = %#v", err)
	}
}

func TestCompileRejectsInvalidHTTPProxySemantics(t *testing.T) {
	tests := []struct {
		name, source, reason string
	}{
		{name: "streaming", source: validProxyProto("stream "), reason: "streaming_proxy_invalid"},
		{name: "unknown request path", source: replaceOnce(validProxyProto(""), `rpc_field: "id"`, `rpc_field: "missing"`), reason: "rpc_field_unresolved"},
		{name: "required credentials", source: replaceOnce(validProxyProto(""), `mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" }`, `mode: REQUIRED`), reason: "credential_combination_invalid"},
		{name: "permission with none", source: replaceOnce(validProxyProto(""), `mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" }`, `mode: NONE`), reason: "permission_auth_conflict"},
		{name: "bad route", source: replaceOnce(validProxyProto(""), `path: "/samples/{id}"`, `path: "/samples/{id}?x=1"`), reason: "path_invalid"},
		{name: "percent route", source: replaceOnce(validProxyProto(""), `path: "/samples/{id}"`, `path: "/samples/%7Bid%7D"`), reason: "path_invalid"},
		{name: "trailing route segment", source: replaceOnce(validProxyProto(""), `path: "/samples/{id}"`, `path: "/samples/{id}/"`), reason: "path_invalid"},
		{name: "dot route segment", source: replaceOnce(validProxyProto(""), `path: "/samples/{id}"`, `path: "/samples/../{id}"`), reason: "path_invalid"},
		{name: "repeated route variable", source: replaceOnce(validProxyProto(""), `path: "/samples/{id}"`, `path: "/samples/{id}/{id}"`), reason: "path_invalid"},
		{name: "route binding closure", source: replaceOnce(validProxyProto(""), `path: "/samples/{id}"`, `path: "/samples/{other}"`), reason: "path_binding_mismatch"},
		{name: "invalid header credential", source: replaceOnce(validProxyProto(""), `name: "Authorization"`, `name: "bad name"`), reason: "credential_name_invalid"},
		{name: "invalid query credential", source: replaceOnce(validProxyProto(""), `type: BEARER location: HEADER name: "Authorization"`, `type: API_KEY location: QUERY name: "bad-name"`), reason: "credential_name_invalid"},
		{name: "invalid cookie credential", source: replaceOnce(validProxyProto(""), `type: BEARER location: HEADER name: "Authorization"`, `type: SESSION_COOKIE location: COOKIE name: "bad name"`), reason: "credential_name_invalid"},
		{name: "reserved content type credential", source: replaceOnce(validProxyProto(""), `type: BEARER location: HEADER name: "Authorization"`, `type: API_KEY location: HEADER name: "Content-Type"`), reason: "header_name_reserved"},
		{name: "duplicate operation", source: validProxyProto("") + `
service DuplicateService { rpc Duplicate(GetSampleRequest) returns (GetSampleResponse) { option (nexa.protocol.v1.http_proxy) = { operation_id: "sample.get" method: GET path: "/duplicates/{id}" auth: { mode: NONE } request_fields: { http_field: "id" rpc_field: "id" } }; } }
`, reason: "operation_id_duplicate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := protocol.Compile(context.Background(), compileOptions(test.source))
			owner, ok := err.(*protocol.Error)
			if !ok || owner.Code() != "protocol_ir_invalid" || owner.Reason() != test.reason {
				t.Fatalf("Compile() error = %#v", err)
			}
		})
	}
}

func compileProtocol(t *testing.T, source string) protocol.Document {
	t.Helper()
	document, err := protocol.Compile(context.Background(), compileOptions(source))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return document
}

func compileOptions(source string) protocol.CompileOptions {
	return protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{"sample.proto"}, Resolver: &memoryResolver{sources: map[string]string{"sample.proto": source}, opens: map[string]int{}}}
}

func validProxyProto(streaming string) string {
	return `syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message GetSampleRequest { string id = 1; int64 tenant_id = 2; }
message GetSampleResponse { string display_name = 1; }
service SampleService {
  rpc GetSample(` + streaming + `GetSampleRequest) returns (GetSampleResponse) {
    option (nexa.protocol.v1.rpc_context) = {
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
    };
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "sample.get"
      method: GET
      path: "/samples/{id}"
      auth: { mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" } }
      permission: "sample.read"
      request_fields: { http_field: "id" rpc_field: "id" }
      response_fields: { rpc_field: "display_name" http_field: "displayName" }
      errors: { match: { domain: "sample" code: "not_found" } project: { domain: "api" code: "sample_not_found" http_status: 404 } }
    };
  }
}
`
}

func replaceOnce(source, old, replacement string) string {
	return strings.Replace(source, old, replacement, 1)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
