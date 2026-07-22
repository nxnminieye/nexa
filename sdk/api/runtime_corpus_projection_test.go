package api

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

type runtimeCorpusProjectionFixture struct {
	RuntimeLimits         json.RawMessage `json:"runtimeLimits"`
	RuntimeLimitSemantics json.RawMessage `json:"runtimeLimitSemantics"`
}

func TestRuntimeCorpusProjectionGenerateAndCheck(t *testing.T) {
	data, err := os.ReadFile("testdata/runtime-api-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	document, err := strictdoc.ParseJSON("runtime-api-v1.json", data)
	if err != nil {
		t.Fatal(err)
	}
	var fixture runtimeCorpusProjectionFixture
	if err := json.Unmarshal(document.JSON(), &fixture); err != nil {
		t.Fatal(err)
	}
	fixtureJSON, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fixtureCanonical, err := jcs.Transform(fixtureJSON)
	if err != nil {
		t.Fatal(err)
	}

	projection := BuildRuntimeCorpusProjection()
	generated, err := projection.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, fixtureCanonical) {
		t.Fatalf(
			"runtime corpus projection drift\ngenerated=%s\ngeneratedDigest=%s\nfixture=%s\nfixtureDigest=%s",
			generated,
			provenance.SHA256(generated).String(),
			fixtureCanonical,
			provenance.SHA256(fixtureCanonical).String(),
		)
	}
	if provenance.SHA256(generated) != provenance.SHA256(fixtureCanonical) {
		t.Fatal("runtime corpus projection digest differs")
	}
	if err := CheckRuntimeCorpusProjection(fixtureCanonical); err != nil {
		t.Fatalf("CheckRuntimeCorpusProjection() error = %v", err)
	}
}

func TestRuntimeCorpusProjectionAccessorsAreOwnerDerivedAndDefensive(t *testing.T) {
	projection := BuildRuntimeCorpusProjection()
	ownerLimits := RuntimeLimits()
	if got := projection.RuntimeLimits(); got != ownerLimits {
		t.Fatalf("RuntimeLimits() = %#v, want owner %#v", got, ownerLimits)
	}
	mutatedLimits := projection.RuntimeLimits()
	mutatedLimits.JSONDepth = 0
	if got := projection.RuntimeLimits(); got != ownerLimits {
		t.Fatalf("runtime limit mutation escaped: %#v", got)
	}

	projectedSemantics := projection.ParserDepthAndNodes()
	ownerSemantics := ownerLimits.JSONSemantics()
	if projectedSemantics.RootDepth() != ownerSemantics.RootDepth() ||
		projectedSemantics.Inclusive() != ownerSemantics.Inclusive() ||
		projectedSemantics.CountsRoot() != ownerSemantics.CountsRoot() ||
		projectedSemantics.CountsValues() != ownerSemantics.CountsValues() ||
		projectedSemantics.CountsMemberNames() != ownerSemantics.CountsMemberNames() ||
		!reflect.DeepEqual(projectedSemantics.Scopes(), ownerSemantics.Scopes()) {
		t.Fatal("parser depth/node projection differs from JSONSemantics owner")
	}
	mutatedScopes := projectedSemantics.Scopes()
	mutatedScopes[0] = "mutated"
	if !reflect.DeepEqual(projection.ParserDepthAndNodes().Scopes(), ownerSemantics.Scopes()) {
		t.Fatal("projection scope mutation escaped")
	}
	document, err := projection.document()
	if err != nil {
		t.Fatal(err)
	}
	requestBoundary := ownerLimits.RequestRawBytesSemantics()
	if document.RuntimeLimitSemantics.RequestRawBytes.Scope != requestBoundary.Scope() ||
		document.RuntimeLimitSemantics.RequestRawBytes.FirstFailure != requestBoundary.FirstFailure() {
		t.Fatal("request raw-byte projection differs from typed boundary owner")
	}
	responseBoundary := ownerLimits.ResponseBytesSemantics()
	if document.RuntimeLimitSemantics.ResponseBytes.Scope != responseBoundary.Scope() ||
		document.RuntimeLimitSemantics.ResponseBytes.BeforeRemoteErrorParse != responseBoundary.BeforeRemoteErrorParse() {
		t.Fatal("response-byte projection differs from typed boundary owner")
	}
}

func TestRuntimeCorpusProjectionCheckRejectsUnknownAndDriftedFacts(t *testing.T) {
	projection := BuildRuntimeCorpusProjection()
	generated, err := projection.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	var document runtimeCorpusProjectionDocument
	if err := json.Unmarshal(generated, &document); err != nil {
		t.Fatal(err)
	}
	document.RuntimeLimits.JSONDepth++
	drifted, err := canonicalRuntimeCorpusProjection(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeCorpusProjection(drifted); err == nil {
		t.Fatal("CheckRuntimeCorpusProjection accepted drifted owner facts")
	}

	var raw runtimeCorpusProjectionFixture
	if err := json.Unmarshal(generated, &raw); err != nil {
		t.Fatal(err)
	}
	withUnknown, err := json.Marshal(struct {
		RuntimeLimits         json.RawMessage `json:"runtimeLimits"`
		RuntimeLimitSemantics json.RawMessage `json:"runtimeLimitSemantics"`
		Unknown               bool            `json:"unknown"`
	}{RuntimeLimits: raw.RuntimeLimits, RuntimeLimitSemantics: raw.RuntimeLimitSemantics, Unknown: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeCorpusProjection(withUnknown); err == nil {
		t.Fatal("CheckRuntimeCorpusProjection accepted an unknown field")
	}

	var incomplete runtimeCorpusProjectionInput
	if err := json.Unmarshal(generated, &incomplete); err != nil {
		t.Fatal(err)
	}
	incomplete.RuntimeLimitSemantics.ParserDepthAndNodes.CountMemberNames = nil
	incompleteJSON, err := json.Marshal(incomplete)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeCorpusProjection(incompleteJSON); err == nil {
		t.Fatal("CheckRuntimeCorpusProjection accepted an incomplete false-valued field")
	}

	generated[0] = '['
	again, err := projection.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) == 0 || again[0] != '{' {
		t.Fatal("canonical projection byte mutation escaped")
	}
}

func TestRuntimeCorpusProjectionCheckRejectsClosedDocumentAndBoundaryDriftMatrix(t *testing.T) {
	generated, err := BuildRuntimeCorpusProjection().CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	replace := func(old, replacement string) []byte {
		t.Helper()
		mutated := bytes.Replace(generated, []byte(old), []byte(replacement), 1)
		if bytes.Equal(mutated, generated) {
			t.Fatalf("projection mutation token not found: %s", old)
		}
		return mutated
	}
	vectors := []struct {
		name string
		data []byte
	}{
		{name: "nested unknown", data: replace(`"responseBytes":{`, `"responseBytes":{"unknown":true,`)},
		{name: "missing", data: replace(`"requestRawBytes":{"firstFailure":true,`, `"requestRawBytes":{`)},
		{name: "null", data: replace(`"beforeRemoteErrorParse":true`, `"beforeRemoteErrorParse":null`)},
		{name: "duplicate", data: replace(`"rootDepth":0`, `"rootDepth":0,"rootDepth":0`)},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			if err := CheckRuntimeCorpusProjection(vector.data); err == nil {
				t.Fatal("CheckRuntimeCorpusProjection accepted a closed-document violation")
			}
		})
	}

	ownerLimits := RuntimeLimits()
	var document runtimeCorpusProjectionDocument
	if err := json.Unmarshal(generated, &document); err != nil {
		t.Fatal(err)
	}
	document.RuntimeLimitSemantics.RequestRawBytes.FirstFailure = !ownerLimits.RequestRawBytesSemantics().FirstFailure()
	drifted, err := canonicalRuntimeCorpusProjection(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeCorpusProjection(drifted); err == nil {
		t.Fatal("CheckRuntimeCorpusProjection accepted request boundary owner drift")
	}

	if err := json.Unmarshal(generated, &document); err != nil {
		t.Fatal(err)
	}
	document.RuntimeLimitSemantics.ResponseBytes.Scope = ownerLimits.RequestRawBytesSemantics().Scope()
	drifted, err = canonicalRuntimeCorpusProjection(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeCorpusProjection(drifted); err == nil {
		t.Fatal("CheckRuntimeCorpusProjection accepted response boundary owner drift")
	}
}
