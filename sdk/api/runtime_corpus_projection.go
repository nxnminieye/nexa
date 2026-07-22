package api

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
)

// RuntimeCorpusProjection is the immutable generated projection of runtime
// limit values and their parser/boundary semantics.
type RuntimeCorpusProjection struct {
	limits                   RuntimeLimitSet
	parserSemantics          JSONLimitSemantics
	requestRawBytesSemantics RequestRawBytesSemantics
	responseBytesSemantics   ResponseBytesSemantics
}

// BuildRuntimeCorpusProjection derives the conformance projection from the
// typed runtime owners without accepting a second configuration surface.
func BuildRuntimeCorpusProjection() RuntimeCorpusProjection {
	limits := RuntimeLimits()
	return RuntimeCorpusProjection{
		limits:                   limits,
		parserSemantics:          cloneJSONLimitSemantics(limits.JSONSemantics()),
		requestRawBytesSemantics: limits.RequestRawBytesSemantics(),
		responseBytesSemantics:   limits.ResponseBytesSemantics(),
	}
}

// RuntimeLimits returns the generated limit value.
func (p RuntimeCorpusProjection) RuntimeLimits() RuntimeLimitSet { return p.limits }

// ParserDepthAndNodes returns a defensive copy of the generated JSON semantics.
func (p RuntimeCorpusProjection) ParserDepthAndNodes() JSONLimitSemantics {
	return cloneJSONLimitSemantics(p.parserSemantics)
}

// CanonicalJSON generates the exact two-node runtime corpus projection.
func (p RuntimeCorpusProjection) CanonicalJSON() ([]byte, error) {
	document, err := p.document()
	if err != nil {
		return nil, err
	}
	return canonicalRuntimeCorpusProjection(document)
}

// CheckRuntimeCorpusProjection verifies a closed structured projection against
// the current typed owners.
func CheckRuntimeCorpusProjection(data []byte) error {
	var input runtimeCorpusProjectionInput
	if err := strictdoc.DecodeJSON("runtime-corpus-projection.json", data, &input); err != nil {
		return err
	}
	actual, err := input.document()
	if err != nil {
		return err
	}
	actualCanonical, err := canonicalRuntimeCorpusProjection(actual)
	if err != nil {
		return err
	}
	expectedCanonical, err := BuildRuntimeCorpusProjection().CanonicalJSON()
	if err != nil {
		return err
	}
	if !bytes.Equal(actualCanonical, expectedCanonical) {
		return errors.New("runtime corpus projection differs from typed owners")
	}
	return nil
}

type runtimeCorpusProjectionDocument struct {
	RuntimeLimits         RuntimeLimitSet               `json:"runtimeLimits"`
	RuntimeLimitSemantics runtimeLimitSemanticsDocument `json:"runtimeLimitSemantics"`
}

type runtimeCorpusProjectionInput struct {
	RuntimeLimits         *runtimeLimitSetInput       `json:"runtimeLimits"`
	RuntimeLimitSemantics *runtimeLimitSemanticsInput `json:"runtimeLimitSemantics"`
}

type runtimeLimitSetInput struct {
	APIVersion       *string `json:"apiVersion"`
	RequestRawBytes  *int    `json:"requestRawBytes"`
	JSONDepth        *int    `json:"jsonDepth"`
	JSONNodes        *int    `json:"jsonNodes"`
	ResponseBytesMin *int64  `json:"responseBytesMin"`
	ResponseBytesMax *int64  `json:"responseBytesMax"`
}

type runtimeLimitSemanticsInput struct {
	RequestRawBytes     *runtimeRawBytesSemanticsInput `json:"requestRawBytes"`
	ParserDepthAndNodes *runtimeParserSemanticsInput   `json:"parserDepthAndNodes"`
	ResponseBytes       *runtimeResponseSemanticsInput `json:"responseBytes"`
}

type runtimeRawBytesSemanticsInput struct {
	Scope        *RuntimeBoundaryScope `json:"scope"`
	FirstFailure *bool                 `json:"firstFailure"`
}

type runtimeParserSemanticsInput struct {
	Scopes           *[]JSONParserScope `json:"scopes"`
	RootDepth        *int               `json:"rootDepth"`
	DepthInclusive   *bool              `json:"depthInclusive"`
	CountRoot        *bool              `json:"countRoot"`
	CountValues      *bool              `json:"countValues"`
	CountMemberNames *bool              `json:"countMemberNames"`
}

type runtimeResponseSemanticsInput struct {
	Scope                  *RuntimeBoundaryScope `json:"scope"`
	BeforeRemoteErrorParse *bool                 `json:"beforeRemoteErrorParse"`
}

type runtimeLimitSemanticsDocument struct {
	RequestRawBytes     runtimeRawBytesSemanticsDocument `json:"requestRawBytes"`
	ParserDepthAndNodes runtimeParserSemanticsDocument   `json:"parserDepthAndNodes"`
	ResponseBytes       runtimeResponseSemanticsDocument `json:"responseBytes"`
}

type runtimeRawBytesSemanticsDocument struct {
	Scope        RuntimeBoundaryScope `json:"scope"`
	FirstFailure bool                 `json:"firstFailure"`
}

type runtimeParserSemanticsDocument struct {
	Scopes           []JSONParserScope `json:"scopes"`
	RootDepth        int               `json:"rootDepth"`
	DepthInclusive   bool              `json:"depthInclusive"`
	CountRoot        bool              `json:"countRoot"`
	CountValues      bool              `json:"countValues"`
	CountMemberNames bool              `json:"countMemberNames"`
}

type runtimeResponseSemanticsDocument struct {
	Scope                  RuntimeBoundaryScope `json:"scope"`
	BeforeRemoteErrorParse bool                 `json:"beforeRemoteErrorParse"`
}

func (input runtimeCorpusProjectionInput) document() (runtimeCorpusProjectionDocument, error) {
	if input.RuntimeLimits == nil || input.RuntimeLimitSemantics == nil {
		return runtimeCorpusProjectionDocument{}, errors.New("runtime corpus projection is incomplete")
	}
	limits, err := input.RuntimeLimits.document()
	if err != nil {
		return runtimeCorpusProjectionDocument{}, err
	}
	semantics, err := input.RuntimeLimitSemantics.document()
	if err != nil {
		return runtimeCorpusProjectionDocument{}, err
	}
	return runtimeCorpusProjectionDocument{RuntimeLimits: limits, RuntimeLimitSemantics: semantics}, nil
}

func (input runtimeLimitSetInput) document() (RuntimeLimitSet, error) {
	if input.APIVersion == nil || input.RequestRawBytes == nil || input.JSONDepth == nil || input.JSONNodes == nil ||
		input.ResponseBytesMin == nil || input.ResponseBytesMax == nil {
		return RuntimeLimitSet{}, errors.New("runtime corpus projection is incomplete")
	}
	return RuntimeLimitSet{
		APIVersion:       *input.APIVersion,
		RequestRawBytes:  *input.RequestRawBytes,
		JSONDepth:        *input.JSONDepth,
		JSONNodes:        *input.JSONNodes,
		ResponseBytesMin: *input.ResponseBytesMin,
		ResponseBytesMax: *input.ResponseBytesMax,
	}, nil
}

func (input runtimeLimitSemanticsInput) document() (runtimeLimitSemanticsDocument, error) {
	if input.RequestRawBytes == nil || input.ParserDepthAndNodes == nil || input.ResponseBytes == nil {
		return runtimeLimitSemanticsDocument{}, errors.New("runtime corpus projection is incomplete")
	}
	requestRawBytes, err := input.RequestRawBytes.document()
	if err != nil {
		return runtimeLimitSemanticsDocument{}, err
	}
	parserDepthAndNodes, err := input.ParserDepthAndNodes.document()
	if err != nil {
		return runtimeLimitSemanticsDocument{}, err
	}
	responseBytes, err := input.ResponseBytes.document()
	if err != nil {
		return runtimeLimitSemanticsDocument{}, err
	}
	return runtimeLimitSemanticsDocument{
		RequestRawBytes:     requestRawBytes,
		ParserDepthAndNodes: parserDepthAndNodes,
		ResponseBytes:       responseBytes,
	}, nil
}

func (input runtimeRawBytesSemanticsInput) document() (runtimeRawBytesSemanticsDocument, error) {
	if input.Scope == nil || input.FirstFailure == nil {
		return runtimeRawBytesSemanticsDocument{}, errors.New("runtime corpus projection is incomplete")
	}
	return runtimeRawBytesSemanticsDocument{Scope: *input.Scope, FirstFailure: *input.FirstFailure}, nil
}

func (input runtimeParserSemanticsInput) document() (runtimeParserSemanticsDocument, error) {
	if input.Scopes == nil || input.RootDepth == nil || input.DepthInclusive == nil || input.CountRoot == nil ||
		input.CountValues == nil || input.CountMemberNames == nil {
		return runtimeParserSemanticsDocument{}, errors.New("runtime corpus projection is incomplete")
	}
	return runtimeParserSemanticsDocument{
		Scopes:           append([]JSONParserScope(nil), (*input.Scopes)...),
		RootDepth:        *input.RootDepth,
		DepthInclusive:   *input.DepthInclusive,
		CountRoot:        *input.CountRoot,
		CountValues:      *input.CountValues,
		CountMemberNames: *input.CountMemberNames,
	}, nil
}

func (input runtimeResponseSemanticsInput) document() (runtimeResponseSemanticsDocument, error) {
	if input.Scope == nil || input.BeforeRemoteErrorParse == nil {
		return runtimeResponseSemanticsDocument{}, errors.New("runtime corpus projection is incomplete")
	}
	return runtimeResponseSemanticsDocument{Scope: *input.Scope, BeforeRemoteErrorParse: *input.BeforeRemoteErrorParse}, nil
}

func (p RuntimeCorpusProjection) document() (runtimeCorpusProjectionDocument, error) {
	semantics := p.ParserDepthAndNodes()
	scopes := semantics.Scopes()
	requestBoundary := p.requestRawBytesSemantics
	responseBoundary := p.responseBytesSemantics
	if p.limits.APIVersion == "" || requestBoundary.Scope() == "" || responseBoundary.Scope() == "" {
		return runtimeCorpusProjectionDocument{}, errors.New("runtime corpus projection is invalid")
	}
	return runtimeCorpusProjectionDocument{
		RuntimeLimits: p.limits,
		RuntimeLimitSemantics: runtimeLimitSemanticsDocument{
			RequestRawBytes: runtimeRawBytesSemanticsDocument{
				Scope:        requestBoundary.Scope(),
				FirstFailure: requestBoundary.FirstFailure(),
			},
			ParserDepthAndNodes: runtimeParserSemanticsDocument{
				Scopes:           scopes,
				RootDepth:        semantics.RootDepth(),
				DepthInclusive:   semantics.Inclusive(),
				CountRoot:        semantics.CountsRoot(),
				CountValues:      semantics.CountsValues(),
				CountMemberNames: semantics.CountsMemberNames(),
			},
			ResponseBytes: runtimeResponseSemanticsDocument{
				Scope:                  responseBoundary.Scope(),
				BeforeRemoteErrorParse: responseBoundary.BeforeRemoteErrorParse(),
			},
		},
	}, nil
}

func canonicalRuntimeCorpusProjection(document runtimeCorpusProjectionDocument) ([]byte, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, errors.New("runtime corpus projection cannot be encoded")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, errors.New("runtime corpus projection cannot be canonicalized")
	}
	return canonical, nil
}

func cloneJSONLimitSemantics(input JSONLimitSemantics) JSONLimitSemantics {
	input.scopes = append([]JSONParserScope(nil), input.scopes...)
	return input
}
