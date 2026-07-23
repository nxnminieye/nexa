package governance

import (
	"context"
	"encoding/json"

	"github.com/nxnminieye/nexa/nexactl/plugin"
)

// New constructs the official governance Build Plugin.
func New() (plugin.Plugin, error) {
	return plugin.NewStatic(plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              "governance",
			Version:         "v0.1.0",
			ContractVersion: plugin.ContractVersion,
			Provides: []plugin.Capability{
				{ID: "governance.validation", Version: "v1.0.0"},
			},
		},
		Commands: []plugin.CommandSpec{
			{
				Path:    []string{"governance", "skill", "validate"},
				Summary: "validate one skill or an immediate skill root",
				Flags: []plugin.FlagSpec{
					{Name: "root", Type: plugin.FlagString, Summary: "skill directory or skills root", Required: true},
				},
				InputSchema: json.RawMessage(`{
  "type":"object",
  "required":["root"],
  "properties":{"root":{"type":"string"}}
}`),
				OutputSchema: json.RawMessage(`{
  "type":"object",
  "required":["apiVersion","skills"],
  "properties":{
    "apiVersion":{"const":"nexa.dev/governance-skill-report/v1"},
    "skills":{"type":"array"}
  }
}`),
				SideEffect: plugin.SideEffectRepositoryRead,
				Run:        validateSkillCommand,
			},
			{
				Path:    []string{"skills", "sync"},
				Summary: "synchronize embedded Nexa skills into a consumer repository",
				Flags: []plugin.FlagSpec{
					{Name: "repo-root", Type: plugin.FlagString, Summary: "absolute existing consumer repository root", Required: true},
				},
				InputSchema: json.RawMessage(`{
  "type":"object",
  "required":["repo-root"],
  "properties":{"repo-root":{"type":"string"}}
}`),
				OutputSchema: json.RawMessage(`{
  "type":"object",
  "required":["apiVersion","target","skills","fileCount"],
  "properties":{
    "apiVersion":{"const":"nexa.dev/governance-skill-sync-result/v1"},
    "target":{"const":".codex/skills"},
    "skills":{"type":"array","items":{"type":"string"}},
    "fileCount":{"type":"integer","minimum":0}
  }
}`),
				SideEffect: plugin.SideEffectRepositoryWrite,
				Run:        syncSkillsCommand,
			},
		},
	})
}

func syncSkillsCommand(_ context.Context, invocation plugin.Invocation) (any, error) {
	return syncSkills(invocation.Flags["repo-root"].(string))
}

func validateSkillCommand(_ context.Context, invocation plugin.Invocation) (any, error) {
	report, issues := inspectSkills(invocation.Flags["root"].(string))
	if len(issues) != 0 {
		return nil, validationError(
			"skill_manifest_invalid",
			"skill validation failed",
			"fix the reported skill manifest issues",
			issues,
		)
	}
	return report, nil
}
