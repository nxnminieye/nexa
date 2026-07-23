package governance_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/governance"
)

func TestNewDefinesGovernanceValidationCapability(t *testing.T) {
	candidate, err := governance.New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	spec := candidate.Spec()
	if spec.Descriptor.ID != "governance" || spec.Descriptor.Version != "v0.1.0" ||
		spec.Descriptor.ContractVersion != plugin.ContractVersion {
		t.Fatalf("descriptor = %#v", spec.Descriptor)
	}
	if !reflect.DeepEqual(spec.Descriptor.Provides, []plugin.Capability{{ID: "governance.validation", Version: "v1.0.0"}}) {
		t.Fatalf("provides = %#v", spec.Descriptor.Provides)
	}
	if len(spec.Descriptor.Requires) != 0 || len(spec.Commands) != 2 {
		t.Fatalf("unexpected plugin spec: %#v", spec)
	}
	wantPaths := [][]string{
		{"governance", "skill", "validate"},
		{"skills", "sync"},
	}
	for i, command := range spec.Commands {
		if !reflect.DeepEqual(command.Path, wantPaths[i]) {
			t.Fatalf("command %d path = %#v", i, command.Path)
		}
		wantSideEffect := plugin.SideEffectRepositoryRead
		if i == 1 {
			wantSideEffect = plugin.SideEffectRepositoryWrite
		}
		if command.SideEffect != wantSideEffect {
			t.Fatalf("command %d side effect = %q", i, command.SideEffect)
		}
		if !json.Valid(command.InputSchema) || !json.Valid(command.OutputSchema) {
			t.Fatalf("command %d schemas are invalid", i)
		}
	}
	if len(spec.Commands[0].Flags) != 1 || spec.Commands[0].Flags[0].Name != "root" || !spec.Commands[0].Flags[0].Required {
		t.Fatalf("skill flags = %#v", spec.Commands[0].Flags)
	}
	if len(spec.Commands[1].Flags) != 1 || spec.Commands[1].Flags[0].Name != "repo-root" ||
		spec.Commands[1].Flags[0].Type != plugin.FlagString || !spec.Commands[1].Flags[0].Required {
		t.Fatalf("skill sync flags = %#v", spec.Commands[1].Flags)
	}
}

func TestSkillSyncCommandReturnsStructuredResult(t *testing.T) {
	repository := t.TempDir()
	candidate, err := governance.New()
	if err != nil {
		t.Fatal(err)
	}
	envelope, stderr, exit := executePlugin(t, candidate, "skills", "sync", "--repo-root", repository, "--json")
	if exit != 0 || stderr != "" || !envelope.OK {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	result := decodeResult[skillSyncResult](t, envelope.Result)
	if result.APIVersion != "nexa.dev/governance-skill-sync-result/v1" || result.Target != ".codex/skills" || result.FileCount != 8 {
		t.Fatalf("result = %#v", result)
	}
}

func TestSkillCommandReturnsDeterministicStructuredReport(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	writeSkill(t, root, "zulu", "zulu", "Use when zulu is needed")
	writeSkill(t, root, "alpha", "alpha", "Use when alpha is needed")
	candidate, err := governance.New()
	if err != nil {
		t.Fatal(err)
	}

	envelope, stderr, exit := executePlugin(
		t,
		candidate,
		"governance", "skill", "validate", "--root", root, "--json",
	)
	if exit != 0 || stderr != "" || !envelope.OK {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	report := decodeResult[struct {
		APIVersion string `json:"apiVersion"`
		Skills     []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"skills"`
	}](t, envelope.Result)
	if report.APIVersion != "nexa.dev/governance-skill-report/v1" {
		t.Fatalf("apiVersion = %q", report.APIVersion)
	}
	if got := []string{report.Skills[0].Name, report.Skills[1].Name}; !reflect.DeepEqual(got, []string{"alpha", "zulu"}) {
		t.Fatalf("skill order = %#v", got)
	}
	if got := []string{report.Skills[0].Path, report.Skills[1].Path}; !reflect.DeepEqual(got, []string{"alpha", "zulu"}) {
		t.Fatalf("skill paths = %#v", got)
	}
}

func TestGovernanceCommandProjectsStructuredFailure(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "router", "other-name", "Use when routing")
	candidate, err := governance.New()
	if err != nil {
		t.Fatal(err)
	}
	envelope, stderr, exit := executePlugin(
		t,
		candidate,
		"governance", "skill", "validate", "--root", root, "--json",
	)
	if exit != 3 || stderr != "" || envelope.OK || envelope.Error == nil {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	if envelope.Error.Code != "skill_manifest_invalid" || envelope.Error.Domain != "nexactl.governance" {
		t.Fatalf("error = %#v", envelope.Error)
	}
	issues := decodeIssues(t, envelope.Error.Details)
	if issues[0].Code != "skill_name_mismatch" {
		t.Fatalf("issues = %#v", issues)
	}
}
