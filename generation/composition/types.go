// Package composition composes typed unary RPC proxy metadata into consumer-owned HTTP API projections.
package composition

import (
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

const APIVersion = "nexa.dev/composition-ir/v1"
const Kind = "CompositionIR"
const CapabilityID = "nexa.dev/generation-api-proxy"
const CapabilityVersion = "nexa.dev/generation-api-proxy/v1"

type BuildOptions struct {
	CoreServiceID      string
	ConsumerModulePath string
}

type Document struct{ state *documentState }
type Snapshot struct {
	state  *snapshotState
	marker snapshotMarker
}
type RenderedArtifact struct {
	ID, Path, Owner string
	Content         []byte
	Sources         []provenance.SourceRef
}
type RenderOptions struct{ CoreRoot string }

type documentState struct {
	coreServiceID, consumerModulePath string
	operations                        []*operationState
}
type snapshotState struct{ canonical []byte }
type snapshotMarker struct{ _ [0]func() }

type operationState struct {
	serviceID, methodName, inputName, outputName string
	requestType, responseType                    string
	proxy                                        protocol.HTTPProxy
	method                                       protocol.Method
	bindingSource                                provenance.Source
	requestMessage, responseMessage              protocol.Message
	requestFields, contextFields, responseFields []resolvedBinding
	operationProvenance                          httpapi.NodeProvenance
	requestProvenance, responseProvenance        httpapi.NodeProvenance
	errorProjections                             []api.ErrorProjectionSpec
}

type resolvedBinding struct {
	httpField string
	context   protocol.ContextValue
	typedPath []string
	fields    []protocol.Field
	valueType httpapi.ValueTypeSpec
	required  bool
}
