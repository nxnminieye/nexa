package source_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
	sourceadapter "github.com/nxnminieye/nexa/plugins/nexactl/source"
)

const sourceSubprocessMode = "NEXA_SOURCE_ADAPTER_SUBPROCESS"
const sourceOperationID = "op_0123456789abcdef0123456789abcdef"

func TestSourceAdapterSevenCommandSubprocessLifecycle(t *testing.T) {
	repository := t.TempDir()
	_, oldRef := sourceTestProvider(t, "sample", "v0.1.0", "old\n")
	_, newRef := sourceTestProvider(t, "sample", "v0.2.0", "new\n")
	selectionArgs := func(refVersion, manifest, tree string) []string {
		return []string{"--repo-root", repository, "--provider", "sample", "--version", refVersion, "--profile", "default", "--target", "services/sample", "--manifest-digest", manifest, "--tree-digest", tree, "--json"}
	}
	oldArgs := selectionArgs(oldRef.Version(), oldRef.ManifestDigest().String(), oldRef.TreeDigest().String())
	newArgs := selectionArgs(newRef.Version(), newRef.ManifestDigest().String(), newRef.TreeDigest().String())
	managedArgs := []string{"--repo-root", repository, "--provider", "sample", "--target", "services/sample", "--json"}
	steps := []struct {
		name, action string
		flags        []string
	}{
		{"plan", "plan", oldArgs}, {"check", "check", oldArgs}, {"materialize", "materialize", oldArgs},
		{"status", "status", managedArgs}, {"diff", "diff", managedArgs}, {"upgrade", "upgrade", newArgs}, {"detach", "detach", managedArgs},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			envelope := executeSourceSubprocess(t, "linked", append([]string{"source", step.action}, step.flags...))
			if !envelope.OK || envelope.OperationID != sourceOperationID {
				t.Fatalf("envelope = %#v", envelope)
			}
		})
	}
	content, err := os.ReadFile(filepath.Join(repository, "services/sample/value.txt"))
	if err != nil || string(content) != "new\n" {
		t.Fatalf("materialized source=%q err=%v", content, err)
	}
	locks, err := filepath.Glob(filepath.Join(repository, ".nexa/source/locks/*.json"))
	if err != nil || len(locks) != 0 {
		t.Fatalf("detach locks=%v err=%v", locks, err)
	}
}

func TestLinkedSubprocessInspectionAndRepositorySideEffects(t *testing.T) {
	inspection := executeSourceSubprocessOutcome(t, "linked", []string{"inspect", "--json"})
	assertSourceExecution(t, inspection, 0, true, "")
	var projected struct {
		Capabilities []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"capabilities"`
		Commands []struct {
			Path         []string        `json:"path"`
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
			SideEffect   string          `json:"sideEffect"`
		} `json:"commands"`
	}
	decodeResult(t, inspection.envelope, &projected)
	if len(projected.Capabilities) != 1 || projected.Capabilities[0].ID != sourceadapter.CapabilityID || projected.Capabilities[0].Version != sourceadapter.CapabilityVersion {
		t.Fatalf("capabilities = %#v", projected.Capabilities)
	}
	wantSideEffects := map[string]string{
		"source plan": "repository-read", "source check": "repository-read", "source status": "repository-read", "source diff": "repository-read",
		"source materialize": "repository-write", "source upgrade": "repository-write", "source detach": "repository-write",
	}
	seen := make(map[string]bool)
	for _, command := range projected.Commands {
		path := strings.Join(command.Path, " ")
		want, ok := wantSideEffects[path]
		if !ok {
			continue
		}
		seen[path] = true
		if command.SideEffect != want || !jsonObject(command.InputSchema) || !jsonObject(command.OutputSchema) {
			t.Fatalf("inspection command %q = %#v", path, command)
		}
	}
	if len(seen) != len(wantSideEffects) {
		t.Fatalf("source commands seen = %#v", seen)
	}

	repository := t.TempDir()
	target := filepath.Join(repository, "services/sample")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "value.txt"), []byte("private-consumer-bytes-9841\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ref := sourceTestProvider(t, "sample", "v0.1.0", "old\n")
	selection := []string{
		"--repo-root", repository, "--provider", ref.ProviderID(), "--version", ref.Version(), "--profile", "default", "--target", "services/sample",
		"--manifest-digest", ref.ManifestDigest().String(), "--tree-digest", ref.TreeDigest().String(), "--json",
	}
	managed := []string{"--repo-root", repository, "--provider", "sample", "--target", "services/sample", "--json"}
	for _, command := range []struct {
		action   string
		flags    []string
		exit     int
		category protocol.Category
	}{{"plan", selection, 0, ""}, {"check", selection, 0, ""}, {"status", managed, 0, ""}, {"diff", managed, 3, protocol.CategoryInput}} {
		t.Run(command.action+" read", func(t *testing.T) {
			assertRepositoryUnchanged(t, repository, func() {
				result := executeSourceSubprocessOutcome(t, "linked", append([]string{"source", command.action}, command.flags...))
				assertSourceExecution(t, result, command.exit, command.exit == 0, command.category, repository, "private-consumer-bytes-9841")
			})
		})
	}
	t.Run("conflicting write", func(t *testing.T) {
		assertRepositoryUnchanged(t, repository, func() {
			result := executeSourceSubprocessOutcome(t, "linked", append([]string{"source", "materialize"}, selection...))
			assertSourceExecution(t, result, 13, false, protocol.CategoryConflict, repository, "private-consumer-bytes-9841")
		})
	})
}

func TestUnlinkedSubprocessInspectAndVersionRemainAvailable(t *testing.T) {
	for _, args := range [][]string{{"inspect", "--json"}, {"version", "--json"}} {
		envelope := executeSourceSubprocess(t, "unlinked", args)
		if !envelope.OK || envelope.OperationID != sourceOperationID {
			t.Fatalf("args=%v envelope=%#v", args, envelope)
		}
	}
	envelope, exit, stderr := executeSourceSubprocessResult(t, "unlinked", []string{"source", "plan", "--json"})
	if exit != 2 || stderr != "" || envelope.OK || envelope.Error == nil || envelope.Error.Code != "command_not_found" {
		t.Fatalf("unlinked missing command exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
}

func TestSourceAdapterSubprocessHelper(t *testing.T) {
	mode := os.Getenv(sourceSubprocessMode)
	if mode == "" {
		return
	}
	var composed *host.Host
	var err error
	options := host.Options{Version: "v0.0.0-test", OperationIDs: protocol.OperationIDGeneratorFunc(func() (string, error) { return sourceOperationID, nil })}
	if mode == "linked" {
		oldProvider, _ := sourceTestProvider(t, "sample", "v0.1.0", "old\n")
		newProvider, _ := sourceTestProvider(t, "sample", "v0.2.0", "new\n")
		candidate, newErr := sourceadapter.New(sourceOptions(), oldProvider, newProvider)
		if newErr != nil {
			t.Fatal(newErr)
		}
		composed, err = host.New(options, candidate)
	} else {
		composed, err = host.New(options)
	}
	if err != nil {
		t.Fatal(err)
	}
	separator := 0
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index + 1
			break
		}
	}
	if separator == 0 {
		t.Fatal("missing subprocess args")
	}
	os.Exit(composed.Execute(context.Background(), os.Args[separator:], os.Stdout, os.Stderr))
}

func executeSourceSubprocess(t *testing.T, mode string, args []string) protocol.Envelope {
	t.Helper()
	result := executeSourceSubprocessOutcome(t, mode, args)
	if result.exit != 0 || result.stderr != "" {
		t.Fatalf("args=%v exit=%d stderr=%q envelope=%#v", args, result.exit, result.stderr, result.envelope)
	}
	return result.envelope
}

func executeSourceSubprocessResult(t *testing.T, mode string, args []string) (protocol.Envelope, int, string) {
	t.Helper()
	result := executeSourceSubprocessOutcome(t, mode, args)
	return result.envelope, result.exit, result.stderr
}

func executeSourceSubprocessOutcome(t *testing.T, mode string, args []string) sourceHostExecution {
	t.Helper()
	commandArgs := append([]string{"-test.run=^TestSourceAdapterSubprocessHelper$", "--"}, args...)
	command := exec.Command(os.Args[0], commandArgs...)
	command.Env = append(os.Environ(), sourceSubprocessMode+"="+mode)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	exit := 0
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	if exitErr != nil {
		exit = exitErr.ExitCode()
	}
	return sourceHostExecution{envelope: decodeSingleEnvelope(t, stdout.Bytes()), exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}
