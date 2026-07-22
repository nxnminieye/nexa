package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	core "github.com/nxnminieye/nexa/plugins/service/core"
	job "github.com/nxnminieye/nexa/plugins/service/job"
	quality "github.com/nxnminieye/nexa/plugins/service/quality"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestOfficialSourceReferenceCoreExactPlan(t *testing.T) {
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	binary := buildNexactl(t)
	stdout, stderr, exit := runBinary(t, binary,
		"source", "plan",
		"--repo-root", t.TempDir(),
		"--provider", ref.ProviderID(),
		"--version", ref.Version(),
		"--profile", "frontend",
		"--target", "standard/core",
		"--manifest-digest", ref.ManifestDigest().String(),
		"--tree-digest", ref.TreeDigest().String(),
		"--json",
	)
	if exit != 0 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Kind       string `json:"kind"`
		Operation  string `json:"operation"`
		CanApply   bool   `json:"canApply"`
		PlanDigest string `json:"planDigest"`
		Changes    []any  `json:"changes"`
	}
	decodeOfficialSourceResult(t, envelope, &plan)
	if !envelope.OK || plan.Kind != "SourcePlan" || plan.Operation != "materialize" || !plan.CanApply || plan.PlanDigest == "" || len(plan.Changes) == 0 {
		t.Fatalf("envelope=%#v plan=%#v", envelope, plan)
	}
}

func TestOfficialSourceReferenceCoreBackendMaterializeAndCheck(t *testing.T) {
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	target := filepath.Join(repository, "standard", "core")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module example.test/standard-core\n\ngo 1.25.0\n\nrequire (\n\tentgo.io/ent v0.14.5\n\tgithub.com/nxnminieye/nexa v0.0.0\n\tgolang.org/x/crypto v0.48.0\n)\n\nreplace github.com/nxnminieye/nexa => %s\n", filepath.ToSlash(repositoryRoot(t)))
	if err := os.WriteFile(filepath.Join(target, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	rootSum, err := os.ReadFile(filepath.Join(repositoryRoot(t), "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "go.sum"), rootSum, 0o644); err != nil {
		t.Fatal(err)
	}

	binary := buildNexactl(t)
	flags := officialSourceSelection(repository, ref, "backend", "standard/core")
	for _, action := range []string{"materialize", "check"} {
		stdout, stderr, exit := runBinary(t, binary, append([]string{"source", action}, flags...)...)
		if exit != 0 || stderr != "" {
			t.Fatalf("source %s exit=%d stdout=%q stderr=%q", action, exit, stdout, stderr)
		}
		var envelope protocol.Envelope
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || !envelope.OK {
			t.Fatalf("source %s envelope=%#v err=%v", action, envelope, err)
		}
		if action == "materialize" {
			command := exec.Command("go", "mod", "tidy")
			command.Dir = target
			command.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("tidy materialized Core module: %v\n%s", err, output)
			}
		}
	}
	command := exec.Command("go", "test", "-mod=readonly", "./coreapp", "./ent/schema")
	command.Dir = filepath.Join(target, "backend", "core")
	command.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("materialized Core tests: %v\n%s", err, output)
	}
}

func TestOfficialSourceReferenceOnlyPublishesCore(t *testing.T) {
	providers := []struct {
		name string
		ref  release.Ref
	}{
		{name: "job", ref: officialSourceProviderRef(t, job.New)},
		{name: "quality", ref: officialSourceProviderRef(t, quality.New)},
		{name: "kafka", ref: unlinkedKafkaRef(t)},
	}
	binary := buildNexactl(t)
	for _, candidate := range providers {
		t.Run(candidate.name, func(t *testing.T) {
			repository := t.TempDir()
			stdout, stderr, exit := runBinary(t, binary, append([]string{"source", "plan"}, officialSourceSelection(repository, candidate.ref, "backend", "standard/"+candidate.name)...)...)
			if exit != 6 || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			var envelope protocol.Envelope
			if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.OK || envelope.Error == nil || envelope.Error.Code != "capability_unavailable" ||
				envelope.Error.Domain != "nexactl.source" || envelope.Error.Category != protocol.CategoryUnavailable {
				t.Fatalf("envelope = %#v", envelope)
			}
			var details struct {
				Reason  string `json:"reason"`
				Pointer string `json:"pointer"`
				Stage   string `json:"stage"`
			}
			if err := json.Unmarshal(envelope.Error.Details, &details); err != nil ||
				details.Reason != "provider_missing" || details.Pointer != "/provider" || details.Stage != "provider" {
				t.Fatalf("details=%#v err=%v", details, err)
			}
			entries, err := os.ReadDir(repository)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unavailable provider repository entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestOfficialSourceReferenceRejectsCoreDigestConflict(t *testing.T) {
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	conflictingRef, err := release.NewRef(release.RefSpec{
		ProviderID: ref.ProviderID(), ModulePath: ref.ModulePath(), PackagePath: ref.PackagePath(), Version: ref.Version(),
		ManifestDigest: provenance.SHA256([]byte("conflicting Core manifest")), TreeDigest: ref.TreeDigest(),
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	stdout, stderr, exit := runBinary(t, buildNexactl(t), append(
		[]string{"source", "plan"}, officialSourceSelection(repository, conflictingRef, "backend", "standard/core")...,
	)...)
	if exit != 13 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "source_release_conflict" || envelope.Error.Category != protocol.CategoryConflict {
		t.Fatalf("envelope = %#v", envelope)
	}
	var details struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(envelope.Error.Details, &details); err != nil || details.Reason != "immutable_release_conflict" {
		t.Fatalf("details=%#v err=%v", details, err)
	}
	entries, err := os.ReadDir(repository)
	if err != nil || len(entries) != 0 {
		t.Fatalf("conflicting release repository entries=%v err=%v", entries, err)
	}
}

func officialSourceSelection(repository string, ref release.Ref, profile, target string) []string {
	return []string{
		"--repo-root", repository, "--provider", ref.ProviderID(), "--version", ref.Version(), "--profile", profile,
		"--target", target, "--manifest-digest", ref.ManifestDigest().String(), "--tree-digest", ref.TreeDigest().String(), "--json",
	}
}

func decodeOfficialSourceResult(t *testing.T, envelope protocol.Envelope, target any) {
	t.Helper()
	encoded, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
}

func officialSourceProviderRef(t *testing.T, constructor func() (sourceplugin.Provider, error)) release.Ref {
	t.Helper()
	provider, err := constructor()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func unlinkedKafkaRef(t *testing.T) release.Ref {
	t.Helper()
	digest := provenance.SHA256([]byte("unlinked Kafka release"))
	ref, err := release.NewRef(release.RefSpec{
		ProviderID: "kafka-service", ModulePath: "github.com/nxnminieye/nexa",
		PackagePath: "github.com/nxnminieye/nexa/plugins/service/kafka", Version: "v0.1.0",
		ManifestDigest: digest, TreeDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
