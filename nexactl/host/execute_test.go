package host_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

const testOperationID = "op_00000000000000000000000000000001"

func TestExecuteSuccessWritesOneCompactEnvelope(t *testing.T) {
	spec := specWithCommand("facts", []string{"facts", "check"})
	var invocation plugin.Invocation
	spec.Commands[0].Run = func(_ context.Context, got plugin.Invocation) (any, error) {
		invocation = got
		return map[string]any{"status": "ok"}, nil
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	var stdout, stderr bytes.Buffer
	exit := h.Execute(context.Background(), []string{"facts", "check", "record-1", "--json"}, &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	envelope := decodeSingleEnvelope(t, stdout.Bytes())
	if !envelope.OK || envelope.OperationID != testOperationID {
		t.Fatalf("envelope = %#v", envelope)
	}
	result, ok := envelope.Result.(map[string]any)
	if !ok || result["status"] != "ok" {
		t.Fatalf("result = %#v", envelope.Result)
	}
	if !reflect.DeepEqual(invocation.Args, []string{"record-1"}) || len(invocation.Flags) != 0 {
		t.Fatalf("invocation = %#v", invocation)
	}
	if bytes.Contains(stdout.Bytes(), []byte("\n  ")) || bytes.Count(stdout.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("compact stdout = %q", stdout.String())
	}
}

func TestExecuteTypedFailureWritesOneEnvelope(t *testing.T) {
	spec := specWithCommand("facts", []string{"facts", "check"})
	spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
		return nil, protocol.NewError(
			"fact_source_missing",
			"nexactl.facts",
			protocol.CategoryInput,
			"fact source is missing",
			"configure the fact source",
		)
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	var stdout, stderr bytes.Buffer
	exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
	if exit != 3 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "fact_source_missing", "nexactl.facts", protocol.CategoryInput)
}

func TestExecuteUsageFailuresAreStableEnvelopes(t *testing.T) {
	spec := executionFlagSpec()
	var handlerCalls int
	spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
		handlerCalls++
		return "unexpected", nil
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)
	tests := []struct {
		name string
		args []string
		code string
	}{
		{name: "unknown command", args: []string{"flgs", "run", "--json"}, code: "command_not_found"},
		{name: "unknown command after flag terminator", args: []string{"--json", "--", "flgs"}, code: "command_not_found"},
		{name: "unknown token before command", args: []string{"--unknown", "flags", "run", "--json"}, code: "command_not_found"},
		{name: "unknown flag inside command path", args: []string{"flags", "--unknown", "run", "--json"}, code: "flag_invalid"},
		{name: "invalid flag", args: []string{"flags", "run", "--name", "orders", "--retries", "not-an-int", "--json"}, code: "flag_invalid"},
		{name: "required flag", args: []string{"flags", "run", "--json"}, code: "flag_required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := h.Execute(context.Background(), tt.args, &stdout, &stderr)
			if exit != 2 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
			}
			assertFailureEnvelope(t, stdout.Bytes(), testOperationID, tt.code, "nexactl.host", protocol.CategoryUsage)
		})
	}
	if handlerCalls != 0 {
		t.Fatalf("handler calls = %d", handlerCalls)
	}
}

func TestExecuteCancellationUsesCanceledTaxonomy(t *testing.T) {
	t.Run("context canceled before handler", func(t *testing.T) {
		called := false
		spec := specWithCommand("facts", []string{"facts", "check"})
		spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
			called = true
			return "unexpected", nil
		}
		h := executionHost(t, fixedOperationIDs(testOperationID), spec)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var stdout, stderr bytes.Buffer
		exit := h.Execute(ctx, []string{"facts", "check", "--json"}, &stdout, &stderr)
		if exit != 130 || stderr.Len() != 0 || called {
			t.Fatalf("exit=%d stderr=%q called=%v", exit, stderr.String(), called)
		}
		assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "operation_canceled", "nexactl.host", protocol.CategoryCanceled)
	})

	t.Run("handler returns cancellation", func(t *testing.T) {
		spec := specWithCommand("facts", []string{"facts", "check"})
		spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
			return nil, context.Canceled
		}
		h := executionHost(t, fixedOperationIDs(testOperationID), spec)

		var stdout, stderr bytes.Buffer
		exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
		if exit != 130 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
		}
		assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "operation_canceled", "nexactl.host", protocol.CategoryCanceled)
	})
}

func TestExecutePrioritizesTypedProtocolErrorOverNestedCancellation(t *testing.T) {
	t.Run("typed external keeps category and hides private cause", func(t *testing.T) {
		const unsafeCause = "D039-host-private /private/staging/source.txt secret=consumer-bytes"
		typed := protocol.NewError(
			"source_transaction_failed",
			"nexactl.source",
			protocol.CategoryExternal,
			"source validation failed",
			"",
		)
		cause := fmt.Errorf("%s: %w", unsafeCause, context.Canceled)
		returned := hostExecutionError{projected: typed, cause: cause}
		spec := specWithCommand("facts", []string{"facts", "check"})
		spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
			return nil, returned
		}
		h := executionHost(t, fixedOperationIDs(testOperationID), spec)

		var stdout, stderr bytes.Buffer
		exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
		if exit != 7 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr.String(), stdout.String())
		}
		assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "source_transaction_failed", "nexactl.source", protocol.CategoryExternal)
		combined := stdout.String() + stderr.String()
		for _, secret := range []string{unsafeCause, "/private/staging", "consumer-bytes"} {
			if strings.Contains(combined, secret) {
				t.Fatalf("private cause escaped: %q", combined)
			}
		}
	})

	t.Run("raw cancellation keeps host taxonomy", func(t *testing.T) {
		spec := specWithCommand("facts", []string{"facts", "check"})
		spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
			return nil, context.Canceled
		}
		h := executionHost(t, fixedOperationIDs(testOperationID), spec)

		var stdout, stderr bytes.Buffer
		exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
		if exit != 130 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
		}
		assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "operation_canceled", "nexactl.host", protocol.CategoryCanceled)
	})

	t.Run("typed cancellation keeps owner details", func(t *testing.T) {
		typed, err := protocol.NewErrorWithDetails(
			"source_operation_canceled",
			"nexactl.source",
			protocol.CategoryCanceled,
			"source command was canceled",
			"",
			hostExecutionDetails{code: "source_operation_canceled", Stage: "transaction"},
		)
		if err != nil {
			t.Fatal(err)
		}
		spec := specWithCommand("facts", []string{"facts", "check"})
		spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
			return nil, typed
		}
		h := executionHost(t, fixedOperationIDs(testOperationID), spec)

		var stdout, stderr bytes.Buffer
		exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
		if exit != 130 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
		}
		envelope := decodeSingleEnvelope(t, stdout.Bytes())
		if envelope.Error == nil || envelope.Error.Code != "source_operation_canceled" || envelope.Error.Domain != "nexactl.source" || envelope.Error.Category != protocol.CategoryCanceled {
			t.Fatalf("error=%#v", envelope.Error)
		}
		var details map[string]any
		if err := json.Unmarshal(envelope.Error.Details, &details); err != nil || details["stage"] != "transaction" {
			t.Fatalf("details=%s err=%v", envelope.Error.Details, err)
		}
	})
}

func TestExecuteHandlerPanicIsProjectedWithoutRawDiagnostic(t *testing.T) {
	spec := specWithCommand("facts", []string{"facts", "check"})
	spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
		panic("secret panic value")
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	var stdout, stderr bytes.Buffer
	exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
	if exit != 70 {
		t.Fatalf("exit = %d", exit)
	}
	assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "internal_error", "nexactl.host", protocol.CategoryInternal)
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "secret panic value") {
		t.Fatalf("raw panic leaked: %q", combined)
	}
	if !strings.Contains(stderr.String(), testOperationID) || !strings.Contains(stderr.String(), "handler_panic") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExecuteInvalidOperationIDGenerationUsesSentinel(t *testing.T) {
	tests := []struct {
		name      string
		generator protocol.OperationIDGenerator
	}{
		{
			name: "generator error",
			generator: protocol.OperationIDGeneratorFunc(func() (string, error) {
				return "", errors.New("secret operation id failure")
			}),
		},
		{name: "empty id", generator: fixedOperationIDs("")},
		{name: "malformed id", generator: fixedOperationIDs("op_injected")},
		{name: "whitespace id", generator: fixedOperationIDs(" op_00000000000000000000000000000001")},
		{name: "newline id", generator: fixedOperationIDs("op_00000000000000000000000000000001\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := executionHost(t, tt.generator)
			var stdout, stderr bytes.Buffer
			exit := h.Execute(context.Background(), []string{"version", "--json"}, &stdout, &stderr)
			if exit != 70 {
				t.Fatalf("exit = %d", exit)
			}
			assertFailureEnvelope(
				t,
				stdout.Bytes(),
				"op_00000000000000000000000000000000",
				"operation_id_generation_failed",
				"nexactl.host",
				protocol.CategoryInternal,
			)
			combined := stdout.String() + stderr.String()
			if strings.Contains(combined, "secret operation id failure") {
				t.Fatalf("raw operation ID error leaked: %q", combined)
			}
		})
	}
}

func TestExecutePluginRootNamedHelpRemainsUnambiguous(t *testing.T) {
	spec := specWithCommand("private-help", []string{"help"})
	spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
		return map[string]any{"owner": "private-help"}, nil
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	inspection := h.Inspect()
	if len(inspection.Commands) != 3 {
		t.Fatalf("commands = %#v", inspection.Commands)
	}
	wantOwners := map[string]string{
		"help":    "private-help",
		"inspect": "nexactl.host",
		"version": "nexactl.host",
	}
	for _, command := range inspection.Commands {
		path := strings.Join(command.Path, " ")
		if want := wantOwners[path]; command.OwnerPluginID != want {
			t.Fatalf("command %q owner = %q, want %q", path, command.OwnerPluginID, want)
		}
		delete(wantOwners, path)
	}
	if len(wantOwners) != 0 {
		t.Fatalf("missing commands = %#v", wantOwners)
	}

	var stdout, stderr bytes.Buffer
	exit := h.Execute(context.Background(), []string{"help", "--json"}, &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr.String(), stdout.String())
	}
	envelope := decodeSingleEnvelope(t, stdout.Bytes())
	result, ok := envelope.Result.(map[string]any)
	if !ok || result["owner"] != "private-help" {
		t.Fatalf("result = %#v", envelope.Result)
	}
}

func TestExecuteFailingStdoutWriterUsesStableDiagnostic(t *testing.T) {
	h := executionHost(t, fixedOperationIDs(testOperationID))
	var stderr bytes.Buffer

	exit := h.Execute(context.Background(), []string{"version", "--json"}, rejectingWriter{}, &stderr)
	if exit != 70 {
		t.Fatalf("exit = %d", exit)
	}
	if strings.Contains(stderr.String(), "secret writer failure") {
		t.Fatalf("raw writer error leaked: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), testOperationID) || !strings.Contains(stderr.String(), "stdout_write_failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExecuteBuiltinsReturnMachineEnvelopes(t *testing.T) {
	h := executionHost(t, fixedOperationIDs(testOperationID))

	t.Run("inspect", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exit := h.Execute(context.Background(), []string{"inspect", "--json"}, &stdout, &stderr)
		if exit != 0 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
		}
		envelope := decodeSingleEnvelope(t, stdout.Bytes())
		result, ok := envelope.Result.(map[string]any)
		if !ok || result["apiVersion"] != "nexa.dev/cli-inspection/v1" {
			t.Fatalf("result = %#v", envelope.Result)
		}
		commands, ok := result["commands"].([]any)
		if !ok || len(commands) != 2 {
			t.Fatalf("commands = %#v", result["commands"])
		}
		flags, ok := result["globalFlags"].([]any)
		if !ok || len(flags) != 2 {
			t.Fatalf("global flags = %#v", result["globalFlags"])
		}
	})

	t.Run("version", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exit := h.Execute(context.Background(), []string{"version", "--json"}, &stdout, &stderr)
		if exit != 0 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
		}
		envelope := decodeSingleEnvelope(t, stdout.Bytes())
		result, ok := envelope.Result.(map[string]any)
		if !ok || result["name"] != "nexactl" || result["version"] != "v0.0.0-test" {
			t.Fatalf("result = %#v", envelope.Result)
		}
	})
}

func TestExecuteDecodesTypedFlagsDefaultsAndArguments(t *testing.T) {
	spec := executionFlagSpec()
	var invocations []plugin.Invocation
	spec.Commands[0].Run = func(_ context.Context, invocation plugin.Invocation) (any, error) {
		invocations = append(invocations, invocation)
		return map[string]any{"accepted": true}, nil
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	var firstOut, firstErr bytes.Buffer
	firstExit := h.Execute(context.Background(), []string{
		"flags", "run",
		"--name", "orders",
		"--force",
		"--retries", "7",
		"--labels", "blue,green",
		"tail",
		"--json",
	}, &firstOut, &firstErr)
	if firstExit != 0 || firstErr.Len() != 0 {
		t.Fatalf("first exit=%d stderr=%q stdout=%q", firstExit, firstErr.String(), firstOut.String())
	}

	var secondOut, secondErr bytes.Buffer
	secondExit := h.Execute(context.Background(), []string{"flags", "run", "--name", "payments", "--json"}, &secondOut, &secondErr)
	if secondExit != 0 || secondErr.Len() != 0 {
		t.Fatalf("second exit=%d stderr=%q stdout=%q", secondExit, secondErr.String(), secondOut.String())
	}
	if len(invocations) != 2 {
		t.Fatalf("invocations = %#v", invocations)
	}
	assertInvocation(t, invocations[0], "orders", true, 7, []string{"blue", "green"}, []string{"tail"})
	assertInvocation(t, invocations[1], "payments", false, 3, []string{"stable", "public"}, nil)
}

func TestExecuteUnencodableResultWritesStableFailureEnvelope(t *testing.T) {
	spec := specWithCommand("facts", []string{"facts", "check"})
	spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
		return secretMarshaler{}, nil
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	var stdout, stderr bytes.Buffer
	exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
	if exit != 70 {
		t.Fatalf("exit = %d", exit)
	}
	assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "output_encoding_failed", "nexactl.host", protocol.CategoryInternal)
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "secret encoding failure") {
		t.Fatalf("raw encoding error leaked: %q", combined)
	}
	if !strings.Contains(stderr.String(), testOperationID) || !strings.Contains(stderr.String(), "output_encoding_failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExecutePanickingJSONMarshalerWritesStableFailureEnvelope(t *testing.T) {
	spec := specWithCommand("facts", []string{"facts", "check"})
	spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
		return panickingMarshaler{}, nil
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	var stdout, stderr bytes.Buffer
	exit := h.Execute(context.Background(), []string{"facts", "check", "--json"}, &stdout, &stderr)
	if exit != 70 {
		t.Fatalf("exit = %d", exit)
	}
	assertFailureEnvelope(t, stdout.Bytes(), testOperationID, "output_encoding_failed", "nexactl.host", protocol.CategoryInternal)
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "secret marshaling panic") {
		t.Fatalf("raw encoding panic leaked: %q", combined)
	}
	wantDiagnostic := "nexactl.host operation=" + testOperationID + " failure=output_encoding_failed\n"
	if stderr.String() != wantDiagnostic {
		t.Fatalf("stderr = %q, want %q", stderr.String(), wantDiagnostic)
	}
}

func TestExecuteHelpUsesHumanStdout(t *testing.T) {
	h := executionHost(t, fixedOperationIDs(testOperationID))
	var stdout, stderr bytes.Buffer

	exit := h.Execute(context.Background(), []string{"--help"}, &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage:") || !strings.Contains(stdout.String(), "inspect") || !strings.Contains(stdout.String(), "version") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	var envelope protocol.Envelope
	if json.Unmarshal(stdout.Bytes(), &envelope) == nil {
		t.Fatalf("help unexpectedly used an envelope: %q", stdout.String())
	}
}

func TestExecuteWithoutJSONUsesIndentedEnvelope(t *testing.T) {
	h := executionHost(t, fixedOperationIDs(testOperationID))
	var stdout, stderr bytes.Buffer

	exit := h.Execute(context.Background(), []string{"version"}, &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	decodeSingleEnvelope(t, stdout.Bytes())
	if !bytes.Contains(stdout.Bytes(), []byte("\n  \"apiVersion\"")) {
		t.Fatalf("stdout is not indented: %q", stdout.String())
	}
}

func TestExecuteFlagTerminatorPreservesPositionalJSONAndIndentedOutput(t *testing.T) {
	spec := specWithCommand("probe", []string{"probe", "run"})
	var invocation plugin.Invocation
	spec.Commands[0].Run = func(_ context.Context, got plugin.Invocation) (any, error) {
		invocation = got
		return map[string]any{"status": "ok"}, nil
	}
	h := executionHost(t, fixedOperationIDs(testOperationID), spec)

	var stdout, stderr bytes.Buffer
	exit := h.Execute(context.Background(), []string{"probe", "run", "--", "--json"}, &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr.String(), stdout.String())
	}
	decodeSingleEnvelope(t, stdout.Bytes())
	if !reflect.DeepEqual(invocation.Args, []string{"--json"}) {
		t.Fatalf("args = %#v", invocation.Args)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("\n  \"apiVersion\"")) {
		t.Fatalf("stdout is not indented: %q", stdout.String())
	}
}

func TestExecuteIndependentHostsRunConcurrently(t *testing.T) {
	newHost := func(t *testing.T, id, value, operationID string) *host.Host {
		t.Helper()
		spec := specWithCommand(id, []string{"shared", "run"})
		spec.Commands[0].Run = func(context.Context, plugin.Invocation) (any, error) {
			return map[string]any{"host": value}, nil
		}
		return executionHost(t, fixedOperationIDs(operationID), spec)
	}
	firstID := "op_00000000000000000000000000000011"
	secondID := "op_00000000000000000000000000000022"
	first := newHost(t, "first", "first", firstID)
	second := newHost(t, "second", "second", secondID)

	type executionResult struct {
		exit   int
		stdout bytes.Buffer
		stderr bytes.Buffer
	}
	results := make([]executionResult, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		results[0].exit = first.Execute(context.Background(), []string{"shared", "run", "--json"}, &results[0].stdout, &results[0].stderr)
	}()
	go func() {
		defer wait.Done()
		results[1].exit = second.Execute(context.Background(), []string{"shared", "run", "--json"}, &results[1].stdout, &results[1].stderr)
	}()
	wait.Wait()

	for i, want := range []struct {
		operationID string
		host        string
	}{{operationID: firstID, host: "first"}, {operationID: secondID, host: "second"}} {
		if results[i].exit != 0 || results[i].stderr.Len() != 0 {
			t.Fatalf("result %d: exit=%d stderr=%q", i, results[i].exit, results[i].stderr.String())
		}
		envelope := decodeSingleEnvelope(t, results[i].stdout.Bytes())
		result, ok := envelope.Result.(map[string]any)
		if !ok || envelope.OperationID != want.operationID || result["host"] != want.host {
			t.Fatalf("result %d envelope = %#v", i, envelope)
		}
	}
}

func executionFlagSpec() plugin.Spec {
	spec := specWithCommand("flags", []string{"flags", "run"})
	spec.Commands[0].Flags = []plugin.FlagSpec{
		{Name: "name", Type: plugin.FlagString, Summary: "object name", Required: true},
		{Name: "force", Type: plugin.FlagBool, Summary: "force operation", Default: json.RawMessage(`false`)},
		{Name: "retries", Type: plugin.FlagInt, Summary: "retry count", Default: json.RawMessage(`3`)},
		{Name: "labels", Type: plugin.FlagStringSlice, Summary: "object labels", Default: json.RawMessage(`["stable","public"]`)},
	}
	return spec
}

func executionHost(t *testing.T, generator protocol.OperationIDGenerator, specs ...plugin.Spec) *host.Host {
	t.Helper()

	plugins := make([]plugin.Plugin, len(specs))
	for i, spec := range specs {
		plugins[i] = mustPlugin(t, spec)
	}
	got, err := host.New(host.Options{Version: "v0.0.0-test", OperationIDs: generator}, plugins...)
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
	return got
}

func fixedOperationIDs(id string) protocol.OperationIDGenerator {
	return protocol.OperationIDGeneratorFunc(func() (string, error) {
		return id, nil
	})
}

func decodeSingleEnvelope(t *testing.T, data []byte) protocol.Envelope {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	var envelope protocol.Envelope
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v; stdout=%q", err, data)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout contains more than one JSON value: err=%v trailing=%#v stdout=%q", err, trailing, data)
	}
	return envelope
}

func assertFailureEnvelope(
	t *testing.T,
	data []byte,
	wantOperationID string,
	wantCode string,
	wantDomain string,
	wantCategory protocol.Category,
) {
	t.Helper()

	envelope := decodeSingleEnvelope(t, data)
	if envelope.OK || envelope.Result != nil || envelope.Error == nil {
		t.Fatalf("failure envelope = %#v", envelope)
	}
	if envelope.OperationID != wantOperationID || envelope.Error.Code != wantCode ||
		envelope.Error.Domain != wantDomain || envelope.Error.Category != wantCategory {
		t.Fatalf("failure = code %q domain %q category %q, want %q %q %q", envelope.Error.Code, envelope.Error.Domain, envelope.Error.Category, wantCode, wantDomain, wantCategory)
	}
}

func assertInvocation(
	t *testing.T,
	got plugin.Invocation,
	wantName string,
	wantForce bool,
	wantRetries int,
	wantLabels []string,
	wantArgs []string,
) {
	t.Helper()

	if got.Flags["name"] != wantName || got.Flags["force"] != wantForce || got.Flags["retries"] != wantRetries {
		t.Fatalf("flags = %#v", got.Flags)
	}
	if !reflect.DeepEqual(got.Flags["labels"], wantLabels) {
		t.Fatalf("labels = %#v, want %#v", got.Flags["labels"], wantLabels)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", got.Args, wantArgs)
	}
}

type rejectingWriter struct{}

type hostExecutionError struct {
	projected error
	cause     error
}

func (e hostExecutionError) Error() string { return e.projected.Error() }

func (e hostExecutionError) Unwrap() []error { return []error{e.projected, e.cause} }

type hostExecutionDetails struct {
	code  string
	Stage string `json:"stage"`
}

func (details hostExecutionDetails) ErrorCode() string { return details.code }

func (details hostExecutionDetails) CanonicalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Stage string `json:"stage"`
	}{Stage: details.Stage})
}

func (rejectingWriter) Write([]byte) (int, error) {
	return 0, errors.New("secret writer failure")
}

type secretMarshaler struct{}

func (secretMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("secret encoding failure")
}

type panickingMarshaler struct{}

func (panickingMarshaler) MarshalJSON() ([]byte, error) {
	panic("secret marshaling panic")
}
