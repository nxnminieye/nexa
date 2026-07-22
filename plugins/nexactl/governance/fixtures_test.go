package governance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

const fixedOperationID = "op_0123456789abcdef0123456789abcdef"

type issueView struct {
	Code    string `json:"code"`
	Object  string `json:"object,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

func writeSkill(t *testing.T, parent, folder, name, description string) string {
	t.Helper()
	root := filepath.Join(parent, folder)
	mustMkdirAll(t, root)
	manifest := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# Skill\n"
	mustWriteFile(t, filepath.Join(root, "SKILL.md"), []byte(manifest))
	return root
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertGovernanceError(t *testing.T, err error, code, issueCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", code)
	}
	payload := protocol.Project(err)
	if payload.Code != code || payload.Domain != "nexactl.governance" || payload.Category != protocol.CategoryInput {
		t.Fatalf("unexpected projected error: %#v", payload)
	}
	issues := decodeIssues(t, payload.Details)
	for _, issue := range issues {
		if issue.Code == issueCode {
			return
		}
	}
	t.Fatalf("issue %q missing from %#v", issueCode, issues)
}

func decodeIssues(t *testing.T, details json.RawMessage) []issueView {
	t.Helper()
	if details == nil {
		t.Fatal("structured error details are missing")
	}
	var document struct {
		Issues []issueView `json:"issues"`
	}
	if err := json.Unmarshal(details, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Issues) == 0 {
		t.Fatal("structured issues are empty")
	}
	return document.Issues
}

func executePlugin(t *testing.T, candidate plugin.Plugin, args ...string) (protocol.Envelope, string, int) {
	t.Helper()
	h, err := host.New(
		host.Options{
			Version: "v0.0.0-test",
			OperationIDs: protocol.OperationIDGeneratorFunc(func() (string, error) {
				return fixedOperationID, nil
			}),
		},
		candidate,
	)
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := h.Execute(context.Background(), args, &stdout, &stderr)
	var envelope protocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\nstdout: %s", err, stdout.String())
	}
	return envelope, stderr.String(), exit
}

func decodeResult[T any](t *testing.T, result any) T {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded T
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
