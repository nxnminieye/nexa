package directwrite

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerationResultCanonicalStrictRoundTrip(t *testing.T) {
	input := GenerationResult{
		APIVersion:   GenerationResultAPIVersion,
		Kind:         GenerationResultKind,
		Status:       GenerationResultStatusGenerated,
		OutputScopes: []OutputScope{{Path: "z", Mode: OutputModeReplaceTree}, {Path: "a", Mode: OutputModeFileSet}},
	}
	canonical, err := CanonicalGenerationResult(input)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"apiVersion":"nexa.dev/generation-result/v2","kind":"GenerationResult","outputScopes":[{"mode":"file-set","path":"a"},{"mode":"replace-tree","path":"z"}],"status":"generated"}`
	if string(canonical) != want {
		t.Fatalf("canonical = %s", canonical)
	}
	parsed, err := ParseGenerationResult(canonical)
	if err != nil || parsed.OutputScopes[0].Path != "a" {
		t.Fatalf("parsed = %#v, %v", parsed, err)
	}
	for _, invalid := range [][]byte{
		append([]byte(" "), canonical...),
		[]byte(strings.Replace(want, `"status":"generated"`, `"status":"generated","unknown":true`, 1)),
		[]byte(strings.Replace(want, `"file-set","path":"a"},{"mode":"replace-tree","path":"z"`, `"replace-tree","path":"z"},{"mode":"file-set","path":"a"`, 1)),
	} {
		if _, err := ParseGenerationResult(invalid); err == nil {
			t.Fatalf("invalid result accepted: %s", invalid)
		}
	}
}

func TestGenerationErrorDetailsCanonicalRules(t *testing.T) {
	details := GenerationErrorDetails{
		APIVersion:       GenerationErrorDetailsAPIVersion,
		Kind:             GenerationErrorDetailsKind,
		Stage:            FailureStageWrite,
		ScopeState:       ScopeStateResolved,
		OutputScopes:     []OutputScope{{Path: "gen", Mode: OutputModeFileSet}},
		CompletedWrites:  []string{"gen/z.go", "gen/a.go"},
		CompletedDeletes: []string{"gen/stale.go"},
		ChangeEvidence:   ChangeEvidenceComplete,
	}
	canonical, err := CanonicalGenerationErrorDetails(details)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseGenerationErrorDetails(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(parsed.CompletedWrites, ",") != "gen/a.go,gen/z.go" {
		t.Fatalf("writes = %#v", parsed.CompletedWrites)
	}

	unresolved := GenerationErrorDetails{
		APIVersion: GenerationErrorDetailsAPIVersion, Kind: GenerationErrorDetailsKind,
		Stage: FailureStageResolveOutput, ScopeState: ScopeStateUnresolved,
		OutputScopes: []OutputScope{}, CompletedWrites: []string{}, CompletedDeletes: []string{},
		ChangeEvidence: ChangeEvidenceComplete,
	}
	encoded, err := CanonicalGenerationErrorDetails(unresolved)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseGenerationErrorDetails(encoded); err != nil {
		t.Fatal(err)
	}
	unresolved.CompletedWrites = []string{"gen/a.go"}
	if _, err := CanonicalGenerationErrorDetails(unresolved); err == nil {
		t.Fatal("unresolved details accepted completed path")
	}
}

func TestEmbeddedSchemasAreClosedAndIndependent(t *testing.T) {
	resultSchema := GenerationResultSchema()
	errorSchema := GenerationErrorDetailsSchema()
	for name, data := range map[string][]byte{"result": resultSchema, "error": errorSchema} {
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s schema: %v", name, err)
		}
		if closed, ok := document["additionalProperties"].(bool); !ok || closed {
			t.Fatalf("%s schema is not closed", name)
		}
	}
	resultSchema[0] = 'x'
	if bytes.Equal(resultSchema, GenerationResultSchema()) {
		t.Fatal("schema accessor returned mutable owner bytes")
	}
}
