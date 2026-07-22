package rpcgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

var serviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func Plan(ctx context.Context, document protocol.Document, options Options) (artifacts []transaction.ArtifactInput, planErr error) {
	if ctx == nil || !serviceIDPattern.MatchString(options.ServiceID) || document.ServiceID() != options.ServiceID || options.RepositoryRoot == "" || options.StagingRoot == "" || options.Emit == nil || options.Runner == nil || options.Tool.ID == "" || options.Tool.Version == "" || options.Tool.Executable == "" || options.Tool.Probe.ExpectedVersion == "" {
		return nil, failure("input", "request_invalid", options, nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, failure("input", "operation_canceled", options, err)
	}
	repositoryRoot, err := canonicalDirectory(options.RepositoryRoot)
	if err != nil {
		return nil, failure("input", "request_invalid", options, err)
	}
	stagingRoot, err := canonicalDirectory(options.StagingRoot)
	if err != nil {
		return nil, failure("input", "request_invalid", options, err)
	}
	overlap, err := directoriesOverlap(repositoryRoot, stagingRoot)
	if err != nil {
		return nil, failure("input", "request_invalid", options, err)
	}
	if overlap {
		return nil, failure("input", "request_invalid", options, os.ErrInvalid)
	}
	stdin, err := protocol.CanonicalJSON(document)
	if err != nil || len(stdin) > toolchain.MaxStdinBytes {
		return nil, failure("input", "protocol_input_invalid", options, err)
	}
	if err := os.WriteFile(filepath.Join(stagingRoot, "go.mod"), []byte("module example.invalid/nexa/rpc/"+options.ServiceID+"\n\ngo 1.25.0\n"), 0o600); err != nil {
		return nil, failure("staging", "staging_create_failed", options, err)
	}
	request := toolchain.Request{RepositoryRoot: repositoryRoot, StagingRoot: stagingRoot, WorkDir: stagingRoot, Tool: options.Tool, Args: []string{"generate", "--service", options.ServiceID}, Environment: append([]toolchain.EnvVar(nil), options.Environment...), Stdin: append([]byte(nil), stdin...)}
	result, err := options.Runner.Run(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, failure("generate", "operation_canceled", options, err)
		}
		return nil, failure("generate", "tool_failed", options, err)
	}
	if len(result.Stdout) > toolchain.MaxStdoutBytes {
		return nil, failure("result", "result_output_limit", options, nil)
	}
	if !validResultIdentity(result, options) {
		return nil, failure("result", "tool_result_invalid", options, nil)
	}
	if result.ExitCode != 0 {
		return nil, failureWithExit("generate", "tool_failed", options, result.ExitCode)
	}
	inventory, err := parseResult(result.Stdout, options.ServiceID, provenance.SHA256(stdin))
	if err != nil {
		return nil, failure("result", "result_invalid", options, err)
	}
	var verifiedContent map[string][]byte
	artifacts, verifiedContent, err = verifyArtifacts(ctx, stagingRoot, document, inventory)
	if err != nil {
		if ctx.Err() != nil {
			return nil, failure("verify", "operation_canceled", options, err)
		}
		return nil, failure("verify", "artifact_invalid", options, err)
	}
	for _, value := range artifacts {
		if err := options.Emit(value.Path, verifiedContent[value.Path]); err != nil {
			return nil, failure("staging", "staging_create_failed", options, err)
		}
	}
	return artifacts, nil
}

func canonicalDirectory(directoryPath string) (string, error) {
	directory, err := filepath.Abs(directoryPath)
	if err != nil {
		return "", err
	}
	directory, err = filepath.EvalSymlinks(filepath.Clean(directory))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", os.ErrInvalid
	}
	return directory, nil
}

func directoriesOverlap(left, right string) (bool, error) {
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return false, err
		}
		if relative == "." || filepath.IsLocal(relative) {
			return true, nil
		}
	}
	return false, nil
}

func validResultIdentity(result toolchain.Result, options Options) bool {
	return result.ToolID == options.Tool.ID && result.Version == options.Tool.Version && result.ExecutableVersion == options.Tool.Probe.ExpectedVersion
}

func parseResult(data []byte, serviceID string, inputDigest provenance.Digest) (resultDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document resultDocument
	if err := decoder.Decode(&document); err != nil {
		return resultDocument{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return resultDocument{}, errors.New("result has trailing data")
	}
	canonical, err := jcs.Transform(data)
	if err != nil || !bytes.Equal(canonical, data) {
		return resultDocument{}, errors.New("result is not canonical")
	}
	parsedDigest, digestErr := provenance.ParseDigest(document.InputDigest)
	if document.APIVersion != resultAPIVersion || document.Kind != resultKind || document.ServiceID != serviceID || digestErr != nil || parsedDigest != inputDigest || !document.GoTestPassed || len(document.Artifacts) == 0 {
		return resultDocument{}, io.ErrUnexpectedEOF
	}
	return document, nil
}
