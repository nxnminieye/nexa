package sourcecomment_test

import (
	"bytes"
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestBuildGraphAllowsDownstreamLocalOperationFacts(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Record", "ent://schema/record.go#Record", "record")
	ent.Facts = []sourcecomment.Directive{directive(t, `// @nexa label.zh-CN: "记录"`, 2)}
	rpc := node(t, sourcecomment.StageProto, sourcecomment.NodeRPC, "Record.List", "proto://rpc/record.proto#records.v1.RecordService.List", "rpc List")
	rpc.Facts = []sourcecomment.Directive{directive(t, `// @nexa permission: "records.read"`, 8), directive(t, `// @nexa auth: "required"`, 9)}
	rpc.SourceDirective = refPointer(ent.Source)
	projection := sourcecomment.ProjectionExpectation{Downstream: rpc.Source, Upstream: ent.Source, SemanticID: "Record.List", Kind: sourcecomment.NodeRPC, ExpectedNativeCanonical: []byte("rpc List")}
	graph, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{rpc, ent}, Projections: []sourcecomment.ProjectionExpectation{projection}})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, id := range []sourcecomment.FactID{{SemanticID: "Record", Key: "label.zh-CN"}, {SemanticID: "Record.List", Key: "permission"}, {SemanticID: "Record.List", Key: "auth"}} {
		if _, ok := graph.Fact(id); !ok {
			t.Errorf("fact %q missing", id.String())
		}
	}
}

func TestBuildGraphBuildsOneGraphFromMultipleAuthoringRoots(t *testing.T) {
	// The graph is assembled from all available roots in one pass. Ent is not
	// required for the independent Proto RPC branch.
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Account", "ent://schema/account.go#Account", "schema Account")
	ent.Facts = []sourcecomment.Directive{directive(t, `// @nexa scope: "tenant"`, 2)}
	protoAccount := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "Account", "proto://rpc/account.proto#iam.v1.Account", "message Account")
	protoAccount.SourceDirective = refPointer(ent.Source)
	protoRoot := node(t, sourcecomment.StageProto, sourcecomment.NodeRPC, "Health", "proto://rpc/health.proto#health.v1.HealthService.Health", "rpc Health")
	protoRoot.Facts = []sourcecomment.Directive{directive(t, `// @nexa auth: "none"`, 2), directive(t, `// @nexa permission: "health.read"`, 3)}
	apiRoot := node(t, sourcecomment.StageAPI, sourcecomment.NodeAPIOperation, "Audit", "api://desc/audit.api#Audit", "GET /audit")
	apiRoot.Facts = []sourcecomment.Directive{directive(t, `// @nexa auth: "required"`, 2), directive(t, `// @nexa permission: "audit.read"`, 3)}
	pageRoot := node(t, sourcecomment.StagePage, sourcecomment.NodePage, "home", "page://pages/home.yaml#home", "page home")
	pageRoot.Facts = []sourcecomment.Directive{directive(t, `// @nexa route.path: "/home"`, 2)}
	apiAccount := node(t, sourcecomment.StageAPI, sourcecomment.NodeAPIType, "Account", "api://desc/account.api#Account", "type Account")
	apiAccount.SourceDirective = refPointer(protoAccount.Source)
	apiHealth := node(t, sourcecomment.StageAPI, sourcecomment.NodeAPIOperation, "Health", "api://desc/health.api#Health", "GET /health")
	apiHealth.SourceDirective = refPointer(protoRoot.Source)

	accountProjection := sourcecomment.ProjectionExpectation{
		Downstream: apiAccount.Source, Upstream: protoAccount.Source,
		SemanticID: "Account", Kind: sourcecomment.NodeAPIType,
		ExpectedNativeCanonical: []byte("type Account"),
	}
	protoProjection := sourcecomment.ProjectionExpectation{
		Downstream: protoAccount.Source, Upstream: ent.Source,
		SemanticID: "Account", Kind: sourcecomment.NodeMessage,
		ExpectedNativeCanonical: []byte("message Account"),
	}
	healthProjection := sourcecomment.ProjectionExpectation{
		Downstream: apiHealth.Source, Upstream: protoRoot.Source,
		SemanticID: "Health", Kind: sourcecomment.NodeAPIOperation,
		ExpectedNativeCanonical: []byte("GET /health"),
	}
	lock, err := sourcecomment.NewProjectionLock([]sourcecomment.ProjectionExpectation{accountProjection, protoProjection, healthProjection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	graph, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{
		Nodes:       []sourcecomment.NodeInput{apiHealth, protoAccount, ent, apiAccount, protoRoot, apiRoot, pageRoot},
		Projections: []sourcecomment.ProjectionExpectation{accountProjection, protoProjection, healthProjection},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if err := lock.ValidateFactGraph(graph); err != nil {
		t.Fatal(err)
	}
	if fact, ok := graph.Fact(sourcecomment.FactID{SemanticID: "Account", Key: "scope"}); !ok || fact.FirstSource().String() != ent.Source.String() {
		t.Fatalf("Account scope first source = %#v, present=%v", fact.FirstSource(), ok)
	}
	if fact, ok := graph.Fact(sourcecomment.FactID{SemanticID: "Health", Key: "permission"}); !ok || fact.FirstSource().String() != protoRoot.Source.String() {
		t.Fatalf("Health permission first source = %#v, present=%v", fact.FirstSource(), ok)
	}
	if fact, ok := graph.Fact(sourcecomment.FactID{SemanticID: "Audit", Key: "permission"}); !ok || fact.FirstSource().String() != apiRoot.Source.String() {
		t.Fatalf("Audit permission first source = %#v, present=%v", fact.FirstSource(), ok)
	}
	if fact, ok := graph.Fact(sourcecomment.FactID{SemanticID: "home", Key: "route.path"}); !ok || fact.FirstSource().String() != pageRoot.Source.String() {
		t.Fatalf("home route first source = %#v, present=%v", fact.FirstSource(), ok)
	}
}

func TestBuildGraphRejectsInvalidProjectionTopology(t *testing.T) {
	proto := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "Account", "proto://rpc/account.proto#iam.v1.Account", "message Account")
	api := node(t, sourcecomment.StageAPI, sourcecomment.NodeAPIType, "Account", "api://desc/account.api#Account", "type Account")
	api.SourceDirective = refPointer(proto.Source)
	valid := sourcecomment.ProjectionExpectation{Downstream: api.Source, Upstream: proto.Source, SemanticID: "Account", Kind: sourcecomment.NodeAPIType, ExpectedNativeCanonical: []byte("type Account")}
	if _, err := sourcecomment.NewProjectionLock([]sourcecomment.ProjectionExpectation{valid, valid}, nil); err == nil {
		t.Fatal("duplicate projections accepted by projection lock")
	}
	protoPeer := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "AccountView", "proto://rpc/account.proto#iam.v1.AccountView", "message AccountView")
	protoPeer.SourceDirective = refPointer(proto.Source)
	sameStage := sourcecomment.ProjectionExpectation{Downstream: protoPeer.Source, Upstream: proto.Source, SemanticID: "AccountView", Kind: sourcecomment.NodeMessage, ExpectedNativeCanonical: []byte("message AccountView")}
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{proto, protoPeer}, Projections: []sourcecomment.ProjectionExpectation{sameStage}})
	assertContainsCode(t, diagnostics, sourcecomment.CodeInvalidTarget)

	_, diagnostics = sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{proto, api}, Projections: []sourcecomment.ProjectionExpectation{valid, valid}})
	assertContainsCode(t, diagnostics, sourcecomment.CodeSemanticCollision)
}

func TestBuildGraphRejectsConflictingFactsAcrossAuthoringRoots(t *testing.T) {
	protoRoot := node(t, sourcecomment.StageProto, sourcecomment.NodeRPC, "Health", "proto://rpc/health.proto#health.v1.HealthService.Health", "rpc Health")
	protoRoot.Facts = []sourcecomment.Directive{directive(t, `// @nexa permission: "health.read"`, 2)}
	apiRoot := node(t, sourcecomment.StageAPI, sourcecomment.NodeAPIOperation, "Health", "api://desc/health.api#Health", "GET /health")
	apiRoot.Facts = []sourcecomment.Directive{directive(t, `// @nexa permission: "health.admin"`, 2)}
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{apiRoot, protoRoot}})
	assertContainsCode(t, diagnostics, sourcecomment.CodeInheritedFactChanged)
}

func TestBuildGraphRequiresHTTPMethodAndPathTogether(t *testing.T) {
	rpc := node(t, sourcecomment.StageProto, sourcecomment.NodeRPC, "Record.List", "proto://rpc/record.proto#records.v1.RecordService.List", "rpc")
	rpc.Facts = []sourcecomment.Directive{directive(t, `// @nexa http.method: "GET"`, 4)}
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{rpc}})
	assertCodes(t, diagnostics, sourcecomment.CodeInvalidValue)
	if diagnostics[0].FactID != "Record.List:http.path" || diagnostics[0].Expected != "http.path" || diagnostics[0].Actual != "<missing>" {
		t.Fatalf("diagnostic = %#v", diagnostics[0])
	}
}

func TestInvalidGraphCannotBeCanonicalized(t *testing.T) {
	node := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Record", "ent://schema/record.go#Record", "schema")
	node.Facts = []sourcecomment.Directive{directive(t, `// @nexa unknown.fact: "x"`, 2)}
	graph, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{node}})
	if len(diagnostics) != 1 || diagnostics[0].Code != sourcecomment.CodeUnknownKey {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if _, err := graph.CanonicalJSON(); err == nil {
		t.Fatal("invalid graph was canonicalized")
	}
	if len(graph.Facts()) != 0 {
		t.Fatal("invalid graph exposed partial facts")
	}
}

func TestBuildGraphRejectsInheritedFactModificationAndMisplacement(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "Record.name", "ent://schema/record.go#Record.name", "string name")
	ent.Facts = []sourcecomment.Directive{directive(t, `// @nexa label.zh-CN: "名称"`, 2)}
	proto := node(t, sourcecomment.StageProto, sourcecomment.NodeProtoField, "Record.name", "proto://rpc/record.proto#records.v1.Record.name", "string name = 1")
	proto.Facts = []sourcecomment.Directive{directive(t, `// @nexa label.zh-CN: "记录名称"`, 12), directive(t, `// @nexa ui.control: "text"`, 13)}
	proto.SourceDirective = refPointer(ent.Source)
	expectation := sourcecomment.ProjectionExpectation{Downstream: proto.Source, Upstream: ent.Source, SemanticID: "Record.name", Kind: sourcecomment.NodeProtoField, ExpectedNativeCanonical: []byte("string name = 1")}
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{proto, ent}, Projections: []sourcecomment.ProjectionExpectation{expectation}})
	assertCodes(t, diagnostics, sourcecomment.CodeInheritedFactChanged, sourcecomment.CodeMisplacedFact)
	for _, item := range diagnostics {
		if item.FactID == "" || item.EarliestSource != ent.Source.String() || item.Suggestion == "" {
			t.Fatalf("diagnostic = %#v", item)
		}
	}
}

func TestBuildGraphRejectsProjectedNodeChangeDeletionAndSourceForgery(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "Record.name", "ent://schema/record.go#Record.name", "string")
	proto := node(t, sourcecomment.StageProto, sourcecomment.NodeProtoField, "Record.name", "proto://rpc/record.proto#records.v1.Record.name", "int64 name = 1")
	forged := mustRef(t, "ent://schema/other.go#Other.name")
	proto.SourceDirective = &forged
	expectation := sourcecomment.ProjectionExpectation{Downstream: proto.Source, Upstream: ent.Source, SemanticID: "Record.name", Kind: sourcecomment.NodeProtoField, ExpectedNativeCanonical: []byte("string name = 1")}
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{ent, proto}, Projections: []sourcecomment.ProjectionExpectation{expectation}})
	assertCodes(t, diagnostics, sourcecomment.CodeInheritedNodeChanged, sourcecomment.CodeSourceMismatch)

	_, deleted := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{ent}, Projections: []sourcecomment.ProjectionExpectation{expectation}})
	assertCodes(t, deleted, sourcecomment.CodeInheritedNodeChanged)
}

func TestBuildGraphRejectsInheritedFactDeletion(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Record", "ent://schema/record.go#Record", "schema")
	expected := sourcecomment.InheritedFactExpectation{ID: sourcecomment.FactID{SemanticID: "Record", Key: "scope"}, Value: sourcecomment.StringValue("tenant"), FirstSource: ent.Source, Location: sourcecomment.Location{File: "detached/metadata.json", Line: 1, Column: 1}}
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{ent}, InheritedFacts: []sourcecomment.InheritedFactExpectation{expected}})
	assertCodes(t, diagnostics, sourcecomment.CodeInheritedFactChanged)
	if diagnostics[0].Actual != "<missing>" || diagnostics[0].FactID != "Record:scope" || diagnostics[0].EarliestSource != ent.Source.String() {
		t.Fatalf("diagnostic = %#v", diagnostics[0])
	}
}

func TestBuildGraphRejectsDuplicateAndSemanticCollisions(t *testing.T) {
	first := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "RecordItem", "proto://rpc/record.proto#records.v1.RecordItem", "message")
	first.TransformedIdentifiers = []string{"record_item"}
	first.Facts = []sourcecomment.Directive{directive(t, `// @nexa scope: "tenant"`, 4), directive(t, `// @nexa scope: "tenant"`, 5)}
	second := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "recorditem", "proto://rpc/other.proto#records.v1.OtherItem", "message")
	third := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "Different", "proto://rpc/third.proto#records.v1.Third", "message")
	third.TransformedIdentifiers = []string{"record_item"}
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{first, second, third}})
	assertContainsCode(t, diagnostics, sourcecomment.CodeDuplicateFact)
	if countCode(diagnostics, sourcecomment.CodeSemanticCollision) < 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestFactGraphCanonicalFormAndDigestAreDeterministic(t *testing.T) {
	first := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Record", "ent://schema/record.go#Record", "schema")
	first.Facts = []sourcecomment.Directive{directive(t, `// @nexa scope: "tenant"`, 2)}
	second := node(t, sourcecomment.StageProto, sourcecomment.NodeRPC, "Record.List", "proto://rpc/record.proto#records.v1.RecordService.List", "rpc")
	second.Facts = []sourcecomment.Directive{directive(t, `// @nexa auth: "required"`, 3)}
	one, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{first, second}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	two, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{second, first}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	oneJSON, _ := one.CanonicalJSON()
	twoJSON, _ := two.CanonicalJSON()
	if !bytes.Equal(oneJSON, twoJSON) {
		t.Fatalf("canonical differs\n%s\n%s", oneJSON, twoJSON)
	}
	oneDigest, _ := one.Digest()
	twoDigest, _ := two.Digest()
	if oneDigest != twoDigest || len(oneDigest) != len("sha256:")+64 {
		t.Fatalf("digests = %q %q", oneDigest, twoDigest)
	}
	copyJSON, _ := one.CanonicalJSON()
	copyJSON[0] = '!'
	again, _ := one.CanonicalJSON()
	if again[0] == '!' {
		t.Fatal("canonical bytes were aliased")
	}
}

func TestFactGraphCanonicalizesSetValuedFacts(t *testing.T) {
	first := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Record", "ent://schema/record.go#Record", "schema")
	first.Facts = []sourcecomment.Directive{directive(t, `// @nexa crud.operations: ["delete","list","create"]`, 2)}
	second := first
	second.Facts = []sourcecomment.Directive{directive(t, `// @nexa crud.operations: ["list","create","delete"]`, 2)}
	one, oneDiagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{first}})
	two, twoDiagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{second}})
	if len(oneDiagnostics) != 0 || len(twoDiagnostics) != 0 {
		t.Fatalf("diagnostics = %#v %#v", oneDiagnostics, twoDiagnostics)
	}
	oneJSON, _ := one.CanonicalJSON()
	twoJSON, _ := two.CanonicalJSON()
	if !bytes.Equal(oneJSON, twoJSON) {
		t.Fatalf("set fact canonical differs\n%s\n%s", oneJSON, twoJSON)
	}
}

func TestBuildGraphRejectsCaseFoldedSourceCollision(t *testing.T) {
	first := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "First", "proto://rpc/Record.proto#records.v1.First", "message")
	second := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "Second", "proto://rpc/record.proto#records.v1.Second", "message")
	_, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{first, second}})
	assertCodes(t, diagnostics, sourcecomment.CodeSemanticCollision)
}

func TestBuildGraphAllowsMultipleSemanticNodesInOneSourceFile(t *testing.T) {
	schema := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Record", "ent://schema/record.go#Record", "schema")
	field := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "Record.name", "ent://schema/record.go#Record.name", "field")
	graph, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{schema, field}})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if _, err := graph.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildGraphScopesTransformedFieldIdentifiersToTheirOwner(t *testing.T) {
	account := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "Account.source", "ent://schema/account.go#Account.source", "field")
	account.TransformedIdentifiers = []string{"source"}
	audit := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "AuditEntry.source", "ent://schema/audit_entry.go#AuditEntry.source", "field")
	audit.TransformedIdentifiers = []string{"source"}
	graph, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{account, audit}})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if _, err := graph.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}

	colliding := account
	colliding.SemanticID = "Account.Source"
	colliding.Source = mustRef(t, "ent://schema/account.go#Account.Source")
	colliding.Location.File = colliding.Source.Path()
	colliding.SourceLocation.File = colliding.Source.Path()
	_, diagnostics = sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{account, colliding}})
	assertContainsCode(t, diagnostics, sourcecomment.CodeSemanticCollision)
}

func node(t *testing.T, stage sourcecomment.Stage, kind sourcecomment.NodeKind, semanticID, rawRef, native string) sourcecomment.NodeInput {
	t.Helper()
	ref := mustRef(t, rawRef)
	return sourcecomment.NodeInput{SemanticID: semanticID, Kind: kind, Stage: stage, Source: ref, Location: sourcecomment.Location{File: ref.Path(), Line: 1, Column: 1}, SourceLocation: sourcecomment.Location{File: ref.Path(), Line: 1, Column: 1}, NativeCanonical: []byte(native)}
}
func directive(t *testing.T, raw string, line int) sourcecomment.Directive {
	t.Helper()
	value, selected, failure := sourcecomment.ParseLine(sourcecomment.Line{Text: raw, CommentPrefix: "//", Location: sourcecomment.Location{File: "facts", Line: line, Column: 1}})
	if failure != nil || !selected {
		t.Fatalf("parse %q = %v, %#v", raw, selected, failure)
	}
	return value
}
func refPointer(value sourcecomment.SourceRef) *sourcecomment.SourceRef { copy := value; return &copy }
func assertCodes(t *testing.T, diagnostics []sourcecomment.Diagnostic, codes ...sourcecomment.Code) {
	t.Helper()
	if len(diagnostics) != len(codes) {
		t.Fatalf("diagnostics = %#v, want %v", diagnostics, codes)
	}
	for _, code := range codes {
		assertContainsCode(t, diagnostics, code)
	}
}
func assertContainsCode(t *testing.T, diagnostics []sourcecomment.Diagnostic, code sourcecomment.Code) {
	t.Helper()
	if countCode(diagnostics, code) == 0 {
		t.Fatalf("code %s missing from %#v", code, diagnostics)
	}
}
func countCode(diagnostics []sourcecomment.Diagnostic, code sourcecomment.Code) int {
	count := 0
	for _, item := range diagnostics {
		if item.Code == code {
			count++
		}
	}
	return count
}
