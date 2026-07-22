package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/toolchain"
)

func TestProviderTargetsMaterializedCoreFacts(t *testing.T) {
	tool := toolchain.Tool{ID: "tool", Version: "v1.0.0", Executable: "tool"}
	project, err := (provider{tools: tools{ent: tool, crud: tool, rpc: tool, api: tool}}).Resolve(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var core, account bool
	for _, service := range project.Services {
		switch service.ServiceID {
		case "core":
			core = true
			if service.EntSchemaDir != "backend/core/ent/schema" || service.ProtoEntry != "backend/core/desc/core.proto" || service.APIEntry != "backend/core/desc/core.api" || service.EntGenerateTool.ID == "" || service.RPCGoTool.ID == "" || service.APIGoTool.ID == "" {
				t.Fatalf("Core generation project = %#v", service)
			}
		case "account":
			account = true
			if service.ProtoEntry == "" || service.RPCGoTool.ID == "" {
				t.Fatalf("account proxy project = %#v", service)
			}
		}
	}
	if !core || !account {
		t.Fatalf("generation services core=%t account=%t", core, account)
	}
}

func TestGenerateEntValidatesEachStructuredServiceResult(t *testing.T) {
	services := []string{"core", "account"}
	var calls [][]string
	execute := func(arguments []string) []byte {
		calls = append(calls, append([]string(nil), arguments...))
		service := services[len(calls)-1]
		return []byte(fmt.Sprintf(`{"status":"generated","service":%q}`, service))
	}
	for _, service := range services {
		generateEnt(execute, service, []string{"--repo-root", "/consumer", "--provider", providerID, "--service", service})
	}
	wantCalls := [][]string{
		{"gen", "ent", "--repo-root", "/consumer", "--provider", providerID, "--service", "core"},
		{"gen", "ent", "--repo-root", "/consumer", "--provider", providerID, "--service", "account"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("structured gen ent calls = %#v, want %#v", calls, wantCalls)
	}

	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "expected core") {
			t.Fatalf("service mismatch panic = %v", recovered)
		}
	}()
	generateEnt(func([]string) []byte {
		return []byte(`{"status":"generated","service":"account"}`)
	}, "core", []string{"--service", "core"})
}

func TestGenerationReportCanonicalJSONIsStable(t *testing.T) {
	firstDigest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	secondDigest := "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	first := generationReport{Commands: []commandReport{
		{
			Sequence: 2, CommandID: "generation.rpc.check?provider=consumer&service=core", PlanDigest: secondDigest, Status: "clean",
			ArtifactDigests: []digestReport{{ID: "transport", Digest: secondDigest}, {ID: "manifest", Digest: firstDigest}},
			InputDigests:    []digestReport{{ID: "repo:z", Digest: secondDigest}, {ID: "repo:a", Digest: firstDigest}},
		},
		{Sequence: 1, CommandID: "generation.rpc.plan?provider=consumer&service=core", PlanDigest: firstDigest, Status: "changes-required"},
	}}
	second := generationReport{Commands: []commandReport{
		{Sequence: 1, CommandID: "generation.rpc.plan?provider=consumer&service=core", PlanDigest: firstDigest, Status: "changes-required"},
		{
			Sequence: 2, CommandID: "generation.rpc.check?provider=consumer&service=core", PlanDigest: secondDigest, Status: "clean",
			ArtifactDigests: []digestReport{{ID: "manifest", Digest: firstDigest}, {ID: "transport", Digest: secondDigest}},
			InputDigests:    []digestReport{{ID: "repo:a", Digest: firstDigest}, {ID: "repo:z", Digest: secondDigest}},
		},
	}}

	firstJSON, err := encodeReport(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := encodeReport(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical report changed with insertion order:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
	want := `{"apiVersion":"nexa.dev/core-generation-report/v1","kind":"CoreGenerationReport","commands":[{"sequence":1,"commandId":"generation.rpc.plan?provider=consumer&service=core","planDigest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","status":"changes-required","artifactDigests":[],"inputDigests":[]},{"sequence":2,"commandId":"generation.rpc.check?provider=consumer&service=core","planDigest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","status":"clean","artifactDigests":[{"id":"manifest","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111"},{"id":"transport","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222"}],"inputDigests":[{"id":"repo:a","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111"},{"id":"repo:z","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222"}]}]}` + "\n"
	if string(firstJSON) != want {
		t.Fatalf("canonical report:\n got: %s\nwant: %s", firstJSON, want)
	}
}

func TestReportCommandMergesDuplicateArtifactDigests(t *testing.T) {
	digest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	result := decodeCommandView(t, fmt.Sprintf(`{
		"planDigest": %q,
		"artifacts": [{"id":"transport","digest":%q}],
		"changes": [{"id":"transport","digest":%q}]
	}`, digest, digest, digest))

	command, err := reportCommand(1, []string{"generation", "rpc", "plan"}, result)
	if err != nil {
		t.Fatal(err)
	}
	want := []digestReport{{ID: "transport", Digest: digest}}
	if !reflect.DeepEqual(command.ArtifactDigests, want) {
		t.Fatalf("artifact digests = %#v, want %#v", command.ArtifactDigests, want)
	}
}

func TestReportCommandExcludesControlChanges(t *testing.T) {
	digest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	result := decodeCommandView(t, fmt.Sprintf(`{
		"planDigest": %q,
		"changes": [
			{"id":"transport","digest":%q},
			{"id":"control-lock","digest":%q,"controlRole":"lock"}
		]
	}`, digest, digest, digest))

	command, err := reportCommand(1, []string{"generation", "rpc", "plan"}, result)
	if err != nil {
		t.Fatal(err)
	}
	want := []digestReport{{ID: "transport", Digest: digest}}
	if !reflect.DeepEqual(command.ArtifactDigests, want) {
		t.Fatalf("artifact digests = %#v, want %#v", command.ArtifactDigests, want)
	}
}

func TestReportCommandRejectsConflictingArtifactDigests(t *testing.T) {
	first := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	second := "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	result := decodeCommandView(t, fmt.Sprintf(`{
		"planDigest": %q,
		"artifacts": [{"id":"transport","digest":%q}],
		"changes": [{"id":"transport","digest":%q}]
	}`, first, first, second))

	_, err := reportCommand(1, []string{"generation", "rpc", "plan"}, result)
	if err == nil || !strings.Contains(err.Error(), "transport") || !strings.Contains(err.Error(), "conflicting digests") {
		t.Fatalf("conflicting artifact error = %v", err)
	}
}

func decodeCommandView(t *testing.T, encoded string) view {
	t.Helper()
	var result view
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
