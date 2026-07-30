// Package composition projects minimal unary RPC identity into canonical .api.
package composition

import (
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

const CapabilityID = "nexa.dev/generation-api-proxy"
const CapabilityVersion = "nexa.dev/generation-api-proxy/v1"

type BuildOptions struct {
	CoreServiceID      string
	ConsumerModulePath string
}

type Document struct{ state *documentState }
type RenderedArtifact struct {
	ID, Path, Owner string
	Content         []byte
	Sources         []provenance.SourceRef
}
type RenderOptions struct{ CoreRoot string }

type documentState struct {
	coreServiceID, consumerModulePath string
	operations                        []*operationState
	types                             []*projectedTypeState
	facts                             sourcecomment.FactGraph
}

func (d Document) FactGraph() sourcecomment.FactGraph {
	if d.state == nil {
		return sourcecomment.FactGraph{}
	}
	return d.state.facts
}

type operationState struct {
	serviceID      string
	methodFullName string
	inputName      string
	outputName     string
	requestType    string
	responseType   string
	operationID    string
	httpMethod     api.HTTPMethod
	path           string
	auth           api.AuthMode
	permission     string
	firstSource    sourcecomment.SourceRef
	method         protocol.Method
	bindingSource  provenance.Source
	request        *projectedTypeState
	response       *projectedTypeState
	provenance     httpapi.NodeProvenance
}

type projectedTypeState struct {
	name, serviceID, messageFullName string
	semanticID                       string
	firstSource                      sourcecomment.SourceRef
	message                          protocol.Message
	fields                           []*projectedFieldState
	provenance                       httpapi.NodeProvenance
}

type projectedFieldState struct {
	protoName, jsonName string
	semanticID          string
	firstSource         sourcecomment.SourceRef
	number              int
	valueType           httpapi.ValueTypeSpec
	required            bool
	field               protocol.Field
	provenance          httpapi.NodeProvenance
}
