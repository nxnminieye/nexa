package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	api "github.com/nxnminieye/nexa/sdk/api"
	"golang.org/x/sys/unix"
)

func TestRuntimeAdapterExecutesClosedCorpusAgainstOwnerExpected(t *testing.T) {
	contractPath, corpusPath, corpus := runtimeAdapterInputs(t)
	var stdout, stderr bytes.Buffer
	exit := run([]string{"--runtime-contract", contractPath, "--corpus", corpusPath}, &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("run() = exit %d stderr %q", exit, stderr.String())
	}
	actual, err := api.ParseRuntimeAdapterResult(bytes.TrimSuffix(stdout.Bytes(), []byte{'\n'}))
	if err != nil {
		t.Fatalf("adapter stdout is invalid: %v\n%s", err, stdout.Bytes())
	}
	actualJSON, err := actual.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := corpus.ExpectedAdapterResult()
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := expected.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf("adapter result differs from owner expected\nactual=%s\nexpected=%s", actualJSON, expectedJSON)
	}
	if !bytes.Equal(stdout.Bytes(), append(actualJSON, '\n')) {
		t.Fatalf("stdout is not exactly one canonical object plus newline: %q", stdout.Bytes())
	}
	for _, forbidden := range []string{"sample-token", "api.example.test", "/samples/sample-1", `"request":`} {
		if bytes.Contains(stdout.Bytes(), []byte(forbidden)) {
			t.Fatalf("adapter stdout disclosed logical request value %q", forbidden)
		}
	}
}

func TestRuntimeAdapterUsageAndInvalidInputsAreSafe(t *testing.T) {
	contractPath, corpusPath, _ := runtimeAdapterInputs(t)
	vectors := []struct {
		name string
		args []string
	}{
		{name: "missing all"},
		{name: "missing corpus", args: []string{"--runtime-contract", contractPath}},
		{name: "wrong order", args: []string{"--corpus", corpusPath, "--runtime-contract", contractPath}},
		{name: "extra", args: []string{"--runtime-contract", contractPath, "--corpus", corpusPath, "extra"}},
		{name: "missing contract file", args: []string{"--runtime-contract", filepath.Join(t.TempDir(), "missing"), "--corpus", corpusPath}},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if exit := run(vector.args, &stdout, &stderr); exit != 2 {
				t.Fatalf("exit = %d, want 2", exit)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 || !bytes.HasPrefix(stderr.Bytes(), []byte("nexa.runtime-adapter failure=")) {
				t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
			if bytes.Contains(stderr.Bytes(), []byte(t.TempDir())) {
				t.Fatalf("stderr leaked a path: %q", stderr.String())
			}
		})
	}
}

func TestRuntimeAdapterInputFilesAreRegularAndBounded(t *testing.T) {
	contractPath, corpusPath, _ := runtimeAdapterInputs(t)
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "must-not-leak")
	if err := os.Mkdir(secretPath, 0o700); err != nil {
		t.Fatal(err)
	}
	oversizedPath := filepath.Join(directory, "oversized-secret.json")
	if err := os.WriteFile(oversizedPath, bytes.Repeat([]byte{' '}, api.RuntimeCorpusRawBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, vector := range []struct {
		name       string
		contract   string
		corpusPath string
	}{
		{name: "contract non-regular", contract: secretPath, corpusPath: corpusPath},
		{name: "corpus non-regular", contract: contractPath, corpusPath: secretPath},
		{name: "corpus oversized", contract: contractPath, corpusPath: oversizedPath},
	} {
		t.Run(vector.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := run([]string{"--runtime-contract", vector.contract, "--corpus", vector.corpusPath}, &stdout, &stderr)
			if exit != 2 || stdout.Len() != 0 || stderr.String() != "nexa.runtime-adapter failure=input\n" {
				t.Fatalf("run() = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
			if bytes.Contains(stderr.Bytes(), []byte(directory)) || bytes.Contains(stderr.Bytes(), []byte("secret")) {
				t.Fatalf("stderr leaked input path: %q", stderr.String())
			}
		})
	}
}

func TestRuntimeAdapterFIFOInputExitsWithoutBlocking(t *testing.T) {
	contractPath, _, _ := runtimeAdapterInputs(t)
	fifoPath := filepath.Join(t.TempDir(), "runtime-corpus.fifo")
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuntimeAdapterFIFOHelperProcess$")
	command.Env = append(os.Environ(),
		"NEXA_RUNTIME_ADAPTER_FIFO_HELPER=1",
		"NEXA_RUNTIME_ADAPTER_CONTRACT="+contractPath,
		"NEXA_RUNTIME_ADAPTER_CORPUS="+fifoPath,
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("adapter blocked on FIFO: %v", ctx.Err())
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 2 {
		t.Fatalf("helper error = %T %v, stdout %q stderr %q", err, err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || stderr.String() != "nexa.runtime-adapter failure=input\n" {
		t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
}

func TestRuntimeAdapterFIFOHelperProcess(t *testing.T) {
	if os.Getenv("NEXA_RUNTIME_ADAPTER_FIFO_HELPER") != "1" {
		return
	}
	exit := run([]string{
		"--runtime-contract", os.Getenv("NEXA_RUNTIME_ADAPTER_CONTRACT"),
		"--corpus", os.Getenv("NEXA_RUNTIME_ADAPTER_CORPUS"),
	}, os.Stdout, os.Stderr)
	os.Exit(exit)
}

func TestRuntimeAdapterRejectedCorpusEndpointsAreInvalidInput(t *testing.T) {
	for _, vector := range []struct {
		name     string
		endpoint string
	}{
		{name: "malformed", endpoint: "%"},
		{name: "query", endpoint: "https://api.example.test?secret=value"},
		{name: "force query", endpoint: "https://api.example.test?"},
		{name: "raw path", endpoint: "https://api.example.test/%2Fsecret"},
		{name: "invalid prefix", endpoint: "https://api.example.test/a/../b"},
	} {
		t.Run(vector.name, func(t *testing.T) {
			contractPath, corpusPath, _ := runtimeAdapterInputs(t)
			data, err := os.ReadFile(corpusPath)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			var document map[string]any
			if err := decoder.Decode(&document); err != nil {
				t.Fatal(err)
			}
			document["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = vector.endpoint
			data, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(corpusPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			exit := run([]string{"--runtime-contract", contractPath, "--corpus", corpusPath}, &stdout, &stderr)
			if exit != 2 || stdout.Len() != 0 || stderr.String() != "nexa.runtime-adapter failure=input\n" {
				t.Fatalf("run() = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRuntimeAdapterInternalOutputFailureIsSafe(t *testing.T) {
	contractPath, corpusPath, _ := runtimeAdapterInputs(t)
	var stderr bytes.Buffer
	exit := run([]string{"--runtime-contract", contractPath, "--corpus", corpusPath}, failingWriter{}, &stderr)
	if exit != 1 || stderr.String() != "nexa.runtime-adapter failure=output\n" {
		t.Fatalf("run() = exit %d stderr %q", exit, stderr.String())
	}
}

func TestRuntimeAdapterShortOutputWriteIsFailure(t *testing.T) {
	contractPath, corpusPath, _ := runtimeAdapterInputs(t)
	var stderr bytes.Buffer
	exit := run([]string{"--runtime-contract", contractPath, "--corpus", corpusPath}, shortWriter{}, &stderr)
	if exit != 1 || stderr.String() != "nexa.runtime-adapter failure=output\n" {
		t.Fatalf("run() = exit %d stderr %q", exit, stderr.String())
	}
}

func TestRuntimeAdapterBodyReadAllOnlySignalsEOFAfterExhaustion(t *testing.T) {
	body := &adapterBody{
		content: []byte("abcdef"), readBehavior: api.RuntimeAdapterReadAll,
		closeBehavior: api.RuntimeAdapterCloseSuccess, context: newAdapterContext(), state: &adapterCaseState{},
	}
	if n, err := body.Read(nil); n != 0 || err != nil {
		t.Fatalf("zero-length read = %d, %v", n, err)
	}
	buffer := make([]byte, 2)
	for index, want := range []string{"ab", "cd", "ef"} {
		n, err := body.Read(buffer)
		if n != len(want) || string(buffer[:n]) != want {
			t.Fatalf("read %d = %q, %d", index, buffer[:n], n)
		}
		if index < 2 && err != nil {
			t.Fatalf("read %d error = %v before exhaustion", index, err)
		}
		if index == 2 && err != io.EOF {
			t.Fatalf("final read error = %v, want EOF", err)
		}
	}
}

func TestRuntimeAdapterConstructorFailureCleanupPanicPreservesOutcome(t *testing.T) {
	contractPath, _, corpus := runtimeAdapterInputs(t)
	data, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := api.ParseRuntimeContract(data)
	if err != nil {
		t.Fatal(err)
	}
	test := corpus.AdapterCases()[0]
	test.Name = "constructor-failure-close-panic"
	test.Transport.Status = 200
	test.Transport.Headers = []api.RuntimeAdapterHeader{{Name: "X-Invalid", Value: "ok"}}
	test.Transport.CloseBehavior = api.RuntimeAdapterClosePanic
	row, err := executeCase(contract, test)
	if err != nil {
		t.Fatal(err)
	}
	if row.BodyCloseCalls != 1 || row.Outcome.Error == nil {
		t.Fatalf("row = %#v", row)
	}
	if got := row.Outcome.Error; got.Code != "transport_error" || got.Reason != "response_header_name_invalid" || got.Pointer != "/headers/0/name" || got.HTTPStatus != 200 {
		t.Fatalf("error = %#v", got)
	}
}

func runtimeAdapterInputs(t *testing.T) (string, string, api.RuntimeCorpus) {
	t.Helper()
	corpusData, err := os.ReadFile("../../testdata/runtime-api-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := api.ParseRuntimeCorpus(corpusData)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := generationapi.Parse("runtime-api-v1.json#manifest", corpus.ManifestJSON())
	if err != nil {
		t.Fatal(err)
	}
	contract, err := api.BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatal(err)
	}
	contractJSON, err := contract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	contractPath := filepath.Join(directory, "runtime-contract.json")
	corpusPath := filepath.Join(directory, "runtime-corpus.json")
	if err := os.WriteFile(contractPath, contractJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corpusPath, corpusData, 0o600); err != nil {
		t.Fatal(err)
	}
	return contractPath, corpusPath, corpus
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write secret") }

type shortWriter struct{}

func (shortWriter) Write(input []byte) (int, error) { return len(input) - 1, nil }
