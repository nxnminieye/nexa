package api

// RuntimeLimitsAPIVersion identifies the typed runtime API limits projection.
const RuntimeLimitsAPIVersion = "nexa.dev/runtime-api-limits/v1"

// JSONParserScope identifies one parser governed by the shared JSON limits.
type JSONParserScope string

const (
	ScopeParseRequest          JSONParserScope = "ParseRequest"
	ScopeParseRemoteError      JSONParserScope = "ParseRemoteError"
	ScopeClientSuccessResponse JSONParserScope = "ClientSuccessResponse"
)

// RuntimeBoundaryScope identifies one closed byte-limit enforcement boundary.
type RuntimeBoundaryScope string

const (
	RuntimeBoundaryScopeParseRequest RuntimeBoundaryScope = RuntimeBoundaryScope(ScopeParseRequest)
	RuntimeBoundaryScopeClientCall   RuntimeBoundaryScope = "Client.Call"
)

// RequestRawBytesSemantics is the immutable owner of request byte-limit ordering.
type RequestRawBytesSemantics struct {
	scope        RuntimeBoundaryScope
	firstFailure bool
}

func (s RequestRawBytesSemantics) Scope() RuntimeBoundaryScope { return s.scope }
func (s RequestRawBytesSemantics) FirstFailure() bool          { return s.firstFailure }

// ResponseBytesSemantics is the immutable owner of response byte-limit ordering.
type ResponseBytesSemantics struct {
	scope                  RuntimeBoundaryScope
	beforeRemoteErrorParse bool
}

func (s ResponseBytesSemantics) Scope() RuntimeBoundaryScope { return s.scope }
func (s ResponseBytesSemantics) BeforeRemoteErrorParse() bool {
	return s.beforeRemoteErrorParse
}

// JSONLimitSemantics is the immutable owner of shared JSON counting behavior.
type JSONLimitSemantics struct {
	rootDepth         int
	inclusive         bool
	countsRoot        bool
	countsValues      bool
	countsMemberNames bool
	scopes            []JSONParserScope
}

func (s JSONLimitSemantics) RootDepth() int          { return s.rootDepth }
func (s JSONLimitSemantics) Inclusive() bool         { return s.inclusive }
func (s JSONLimitSemantics) CountsRoot() bool        { return s.countsRoot }
func (s JSONLimitSemantics) CountsValues() bool      { return s.countsValues }
func (s JSONLimitSemantics) CountsMemberNames() bool { return s.countsMemberNames }
func (s JSONLimitSemantics) Scopes() []JSONParserScope {
	return append([]JSONParserScope(nil), s.scopes...)
}

// RuntimeLimitSet is the typed cross-language safety and response limit contract.
type RuntimeLimitSet struct {
	APIVersion       string `json:"apiVersion"`
	RequestRawBytes  int    `json:"requestRawBytes"`
	JSONDepth        int    `json:"jsonDepth"`
	JSONNodes        int    `json:"jsonNodes"`
	ResponseBytesMin int64  `json:"responseBytesMin"`
	ResponseBytesMax int64  `json:"responseBytesMax"`
}

// RequestRawBytesSemantics returns the request raw-byte boundary contract.
// A zero or foreign-version limit set returns the zero semantics value.
func (l RuntimeLimitSet) RequestRawBytesSemantics() RequestRawBytesSemantics {
	if l.APIVersion != RuntimeLimitsAPIVersion {
		return RequestRawBytesSemantics{}
	}
	return RequestRawBytesSemantics{
		scope:        RuntimeBoundaryScopeParseRequest,
		firstFailure: true,
	}
}

// ResponseBytesSemantics returns the Client response-byte boundary contract.
// A zero or foreign-version limit set returns the zero semantics value.
func (l RuntimeLimitSet) ResponseBytesSemantics() ResponseBytesSemantics {
	if l.APIVersion != RuntimeLimitsAPIVersion {
		return ResponseBytesSemantics{}
	}
	return ResponseBytesSemantics{
		scope:                  RuntimeBoundaryScopeClientCall,
		beforeRemoteErrorParse: true,
	}
}

// JSONSemantics returns the closed counting contract used by every shared JSON parser.
// A zero or foreign-version limit set returns the zero semantics value.
func (l RuntimeLimitSet) JSONSemantics() JSONLimitSemantics {
	if l.APIVersion != RuntimeLimitsAPIVersion {
		return JSONLimitSemantics{}
	}
	return JSONLimitSemantics{
		rootDepth:         0,
		inclusive:         true,
		countsRoot:        true,
		countsValues:      true,
		countsMemberNames: false,
		scopes:            []JSONParserScope{ScopeParseRequest, ScopeParseRemoteError, ScopeClientSuccessResponse},
	}
}

// RuntimeLimits returns an independent value containing runtime API limits.
func RuntimeLimits() RuntimeLimitSet {
	return RuntimeLimitSet{
		APIVersion:       RuntimeLimitsAPIVersion,
		RequestRawBytes:  1 << 20,
		JSONDepth:        64,
		JSONNodes:        65_536,
		ResponseBytesMin: 1,
		ResponseBytesMax: 64 << 20,
	}
}

// Request is an immutable, normalized JSON request object.
type Request struct {
	root requestObject
	json []byte
}

// JSON returns an independent copy of the normalized request bytes.
func (r Request) JSON() []byte {
	return append([]byte(nil), r.json...)
}

type requestValue interface {
	appendJSON([]byte) []byte
}

type requestMember struct {
	name  string
	value requestValue
	start int
	end   int
}

type requestObject []requestMember
type requestArray []requestValue
type requestString string
type requestNumber string
type requestBool bool
type requestNull struct{}
