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
	for name, scopes := range map[string][]OutputScope{
		"casefold": {{Path: "Gen", Mode: OutputModeFileSet}, {Path: "gen", Mode: OutputModeFileSet}},
		"overlap":  {{Path: "gen", Mode: OutputModeFileSet}, {Path: "gen/tree", Mode: OutputModeReplaceTree}},
	} {
		invalid, err := canonicalJSON(GenerationResult{
			APIVersion: GenerationResultAPIVersion, Kind: GenerationResultKind,
			Status: GenerationResultStatusGenerated, OutputScopes: scopes,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseGenerationResult(invalid); err == nil {
			t.Fatalf("%s topology accepted", name)
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

	resolvedEmpty := GenerationErrorDetails{
		APIVersion: GenerationErrorDetailsAPIVersion, Kind: GenerationErrorDetailsKind,
		Stage: FailureStageWrite, ScopeState: ScopeStateResolved,
		OutputScopes:    []OutputScope{{Path: "gen", Mode: OutputModeFileSet}},
		CompletedWrites: nil, CompletedDeletes: nil, ChangeEvidence: ChangeEvidenceComplete,
	}
	encoded, err = CanonicalGenerationErrorDetails(resolvedEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"completedWrites":[]`)) || !bytes.Contains(encoded, []byte(`"completedDeletes":[]`)) {
		t.Fatalf("nil arrays were not normalized: %s", encoded)
	}
	if _, err := ParseGenerationErrorDetails(encoded); err != nil {
		t.Fatal(err)
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
		for _, annotation := range []string{"$comment", "x-nexa-unicode-casefold-uniqueness", "x-nexa-path-topology", "x-nexa-canonical-order"} {
			if _, ok := document[annotation]; !ok {
				t.Fatalf("%s schema lacks %s", name, annotation)
			}
		}
	}
	resultSchema[0] = 'x'
	if bytes.Equal(resultSchema, GenerationResultSchema()) {
		t.Fatal("schema accessor returned mutable owner bytes")
	}
}

func TestGenerationResultSchemaDirectlyRejectsNonCleanAndGitPaths(t *testing.T) {
	if err := compileSchemas(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{"../outside", "a/./b", ".GiT/hooks", "bad\x00path"} {
		resultDocument := map[string]any{
			"apiVersion":   GenerationResultAPIVersion,
			"kind":         GenerationResultKind,
			"status":       GenerationResultStatusGenerated,
			"outputScopes": []any{map[string]any{"path": candidate, "mode": string(OutputModeFileSet)}},
		}
		if err := resultSchema.Validate(resultDocument); err == nil {
			t.Fatalf("result schema accepted path %q", candidate)
		}
		errorDocument := map[string]any{
			"apiVersion":       GenerationErrorDetailsAPIVersion,
			"kind":             GenerationErrorDetailsKind,
			"stage":            string(FailureStageWrite),
			"scopeState":       string(ScopeStateResolved),
			"outputScopes":     []any{map[string]any{"path": "gen", "mode": string(OutputModeFileSet)}},
			"completedWrites":  []any{candidate},
			"completedDeletes": []any{},
			"changeEvidence":   string(ChangeEvidenceComplete),
		}
		if err := errorDetailsSchema.Validate(errorDocument); err == nil {
			t.Fatalf("error schema accepted path %q", candidate)
		}
	}
}

func TestWireOwnersRejectNULAndInvalidUTF8Paths(t *testing.T) {
	invalidUTF8 := string([]byte{'g', 'e', 'n', '/', 0xff, '.', 'g', 'o'})
	for _, candidate := range []string{"gen/nu\x00ll.go", invalidUTF8} {
		result := GenerationResult{
			APIVersion: GenerationResultAPIVersion, Kind: GenerationResultKind,
			Status:       GenerationResultStatusGenerated,
			OutputScopes: []OutputScope{{Path: candidate, Mode: OutputModeFileSet}},
		}
		if _, err := CanonicalGenerationResult(result); err == nil {
			t.Fatalf("canonical result accepted path %q", candidate)
		}
		details := GenerationErrorDetails{
			APIVersion: GenerationErrorDetailsAPIVersion, Kind: GenerationErrorDetailsKind,
			Stage: FailureStageWrite, ScopeState: ScopeStateResolved,
			OutputScopes:    []OutputScope{{Path: "gen", Mode: OutputModeFileSet}},
			CompletedWrites: []string{candidate}, CompletedDeletes: []string{},
			ChangeEvidence: ChangeEvidenceComplete,
		}
		if _, err := CanonicalGenerationErrorDetails(details); err == nil {
			t.Fatalf("canonical error details accepted path %q", candidate)
		}
	}

	nulResult := []byte(`{"apiVersion":"nexa.dev/generation-result/v2","kind":"GenerationResult","outputScopes":[{"mode":"file-set","path":"gen/nu\u0000ll.go"}],"status":"generated"}`)
	if _, err := ParseGenerationResult(nulResult); err == nil {
		t.Fatal("result parser accepted NUL path")
	}
	nulDetails := []byte(`{"apiVersion":"nexa.dev/generation-error-details/v2","changeEvidence":"complete","completedDeletes":[],"completedWrites":["gen/nu\u0000ll.go"],"kind":"GenerationErrorDetails","outputScopes":[{"mode":"file-set","path":"gen"}],"scopeState":"resolved","stage":"write"}`)
	if _, err := ParseGenerationErrorDetails(nulDetails); err == nil {
		t.Fatal("error-details parser accepted NUL path")
	}

	rawResult := []byte(`{"apiVersion":"nexa.dev/generation-result/v2","kind":"GenerationResult","outputScopes":[{"mode":"file-set","path":"gen/`)
	rawResult = append(rawResult, 0xff)
	rawResult = append(rawResult, []byte(`.go"}],"status":"generated"}`)...)
	if _, err := ParseGenerationResult(rawResult); err == nil {
		t.Fatal("result parser accepted raw invalid UTF-8")
	}
	rawDetails := []byte(`{"apiVersion":"nexa.dev/generation-error-details/v2","changeEvidence":"complete","completedDeletes":[],"completedWrites":["gen/`)
	rawDetails = append(rawDetails, 0xff)
	rawDetails = append(rawDetails, []byte(`.go"],"kind":"GenerationErrorDetails","outputScopes":[{"mode":"file-set","path":"gen"}],"scopeState":"resolved","stage":"write"}`)...)
	if _, err := ParseGenerationErrorDetails(rawDetails); err == nil {
		t.Fatal("error-details parser accepted raw invalid UTF-8")
	}
}

func TestEmbeddedSchemasDirectlyRejectNULInEveryPathSurface(t *testing.T) {
	if err := compileSchemas(); err != nil {
		t.Fatal(err)
	}
	resultDocument := map[string]any{
		"apiVersion": GenerationResultAPIVersion, "kind": GenerationResultKind,
		"status":       GenerationResultStatusGenerated,
		"outputScopes": []any{map[string]any{"path": "gen/nu\x00ll", "mode": string(OutputModeFileSet)}},
	}
	if err := resultSchema.Validate(resultDocument); err == nil {
		t.Fatal("result schema accepted NUL output scope")
	}
	for _, field := range []string{"outputScopes", "completedWrites", "completedDeletes"} {
		document := map[string]any{
			"apiVersion": GenerationErrorDetailsAPIVersion, "kind": GenerationErrorDetailsKind,
			"stage": string(FailureStageWrite), "scopeState": string(ScopeStateResolved),
			"outputScopes":    []any{map[string]any{"path": "gen", "mode": string(OutputModeFileSet)}},
			"completedWrites": []any{}, "completedDeletes": []any{},
			"changeEvidence": string(ChangeEvidenceComplete),
		}
		switch field {
		case "outputScopes":
			document[field] = []any{map[string]any{"path": "gen/nu\x00ll", "mode": string(OutputModeFileSet)}}
		case "completedWrites", "completedDeletes":
			document[field] = []any{"gen/nu\x00ll"}
		}
		if err := errorDetailsSchema.Validate(document); err == nil {
			t.Fatalf("error schema accepted NUL in %s", field)
		}
	}
}
