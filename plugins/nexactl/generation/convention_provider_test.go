package generation_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

func TestConventionalProviderResolvesStandardServiceLayouts(t *testing.T) {
	repository := t.TempDir()
	writeConventionSource(t, repository, "backend/core/rpc/desc/base.proto", `syntax = "proto3"; package core; message CoreRequest {} service Core { rpc Get(CoreRequest) returns (CoreRequest); }`)
	writeConventionSource(t, repository, "backend/core/api/desc/base.api", "// @nexa $contract: \"nexa.dev/source-comment/v1\"\nsyntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-convention/v1\")\n")
	writeConventionSource(t, repository, "backend/job/desc/base.proto", `syntax = "proto3"; package job; message JobRequest {} service Job { rpc Get(JobRequest) returns (JobRequest); }`)
	writeConventionSource(t, repository, "backend/job/desc/base.api", "// @nexa $contract: \"nexa.dev/source-comment/v1\"\nsyntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-convention/v1\")\n")

	provider := generation.ConventionalProvider{}
	if descriptor := provider.Descriptor(); descriptor.ID != generation.ConventionalProviderID || descriptor.Version != "v1.0.0" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	project, err := provider.Resolve(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Services) != 2 {
		t.Fatalf("services = %#v, want core and job", project.Services)
	}
	if project.Services[0].ServiceID != "core" || project.Services[0].RPC == nil || project.Services[0].API == nil {
		t.Fatalf("core project = %#v", project.Services[0])
	}
	if project.Services[0].API.EntryFile != "backend/core/api/desc/base.api" {
		t.Fatalf("core API entry = %q", project.Services[0].API.EntryFile)
	}
	if project.Services[1].ServiceID != "job" || project.Services[1].RPC == nil || project.Services[1].API == nil {
		t.Fatalf("job project = %#v", project.Services[1])
	}
	if project.Services[1].API.EntryFile != "backend/job/desc/base.api" {
		t.Fatalf("job API entry = %q", project.Services[1].API.EntryFile)
	}
	for _, service := range project.Services {
		if service.RPC.Facts.ServiceID() != service.ServiceID || !service.RPC.Facts.FactGraph().Valid() {
			t.Fatalf("service %q has invalid RPC facts", service.ServiceID)
		}
	}
}

func TestConventionalProviderRejectsNonstandardRPCLayout(t *testing.T) {
	repository := t.TempDir()
	writeConventionSource(t, repository, "backend/job/rpc/desc/base.proto", `syntax = "proto3"; package job;`)

	if _, err := (generation.ConventionalProvider{}).Resolve(context.Background(), repository); err == nil {
		t.Fatal("nonstandard RPC layout accepted")
	}
}

func writeConventionSource(t *testing.T, repository, path, content string) {
	t.Helper()
	target := filepath.Join(repository, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
