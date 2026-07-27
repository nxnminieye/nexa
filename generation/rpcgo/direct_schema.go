package rpcgo

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/nxnminieye/nexa/generation/protocol"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	rpcRequestSchemaURL  = "https://nexa.dev/schemas/generation/rpcgo/rpc-go-request-v2.schema.json"
	rpcResultSchemaURL   = "https://nexa.dev/schemas/generation/rpcgo/rpc-go-result-v2.schema.json"
	rpcProtocolSchemaURL = "https://nexa.dev/schemas/generation/protocol/protocol-ir-v2.schema.json"
)

//go:embed rpc-go-request-v2.schema.json
var embeddedRPCGoRequestSchema []byte

//go:embed rpc-go-result-v2.schema.json
var embeddedRPCGoResultSchema []byte

var rpcSchemasOnce sync.Once
var rpcRequestSchema, rpcResultSchema *jsonschema.Schema
var rpcSchemaError error

func RPCGoRequestSchema() []byte { return append([]byte(nil), embeddedRPCGoRequestSchema...) }
func RPCGoResultSchema() []byte  { return append([]byte(nil), embeddedRPCGoResultSchema...) }

func compileRPCSchemas() error {
	rpcSchemasOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		for _, item := range []struct {
			url  string
			data []byte
		}{{rpcRequestSchemaURL, embeddedRPCGoRequestSchema}, {rpcResultSchemaURL, embeddedRPCGoResultSchema}, {rpcProtocolSchemaURL, protocol.Schema()}} {
			var document any
			if rpcSchemaError = json.Unmarshal(item.data, &document); rpcSchemaError != nil {
				return
			}
			if rpcSchemaError = compiler.AddResource(item.url, document); rpcSchemaError != nil {
				return
			}
		}
		if rpcRequestSchema, rpcSchemaError = compiler.Compile(rpcRequestSchemaURL); rpcSchemaError != nil {
			return
		}
		rpcResultSchema, rpcSchemaError = compiler.Compile(rpcResultSchemaURL)
	})
	return rpcSchemaError
}

func validateRPCGoRequestSchema(value any) error {
	if err := compileRPCSchemas(); err != nil {
		return err
	}
	document, err := rpcSchemaDocument(value)
	if err != nil {
		return err
	}
	return rpcRequestSchema.Validate(document)
}
func validateRPCGoResultSchema(value any) error {
	if err := compileRPCSchemas(); err != nil {
		return err
	}
	document, err := rpcSchemaDocument(value)
	if err != nil {
		return err
	}
	return rpcResultSchema.Validate(document)
}
func rpcSchemaDocument(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document any
	err = json.Unmarshal(encoded, &document)
	return document, err
}
