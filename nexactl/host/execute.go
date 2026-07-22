package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

type executionState struct {
	invoked    bool
	result     any
	diagnostic string
}

func (h *Host) Execute(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if isNil(stderr) {
		stderr = io.Discard
	}
	compact := protocol.CompactJSONRequested(args)
	operationID, ok := generateOperationID(h.operationIDs)
	if !ok {
		operationID = protocol.SentinelOperationID
		return h.writeFailure(
			stdout,
			stderr,
			operationID,
			internalError("operation_id_generation_failed", "operation ID generation failed"),
			compact,
		)
	}

	var humanOutput bytes.Buffer
	state := &executionState{}
	root := h.newCommandTree(state, &humanOutput)
	root.SetArgs(append([]string(nil), args...))
	err := root.ExecuteContext(ctx)
	if state.diagnostic != "" {
		writeDiagnostic(stderr, operationID, state.diagnostic)
	}
	if err != nil {
		return h.writeFailure(stdout, stderr, operationID, stableExecutionError(err, state.invoked), compact)
	}
	if !state.invoked {
		if writeOutput(stdout, humanOutput.Bytes()) {
			return 0
		}
		writeDiagnostic(stderr, operationID, "stdout_write_failed")
		return 70
	}

	envelope := protocol.Success(operationID, state.result)
	payload, err := encodeEnvelope(envelope, compact)
	if err != nil {
		writeDiagnostic(stderr, operationID, "output_encoding_failed")
		return h.writeFailure(
			stdout,
			stderr,
			operationID,
			internalError("output_encoding_failed", "command output could not be encoded"),
			compact,
		)
	}
	if writeOutput(stdout, payload) {
		return 0
	}
	writeDiagnostic(stderr, operationID, "stdout_write_failed")
	return 70
}

func (h *Host) writeFailure(
	stdout io.Writer,
	stderr io.Writer,
	operationID string,
	err error,
	compact bool,
) int {
	payload, encodeErr := encodeEnvelope(protocol.Failure(operationID, err), compact)
	if encodeErr != nil {
		writeDiagnostic(stderr, operationID, "failure_envelope_encoding_failed")
		return 70
	}
	if !writeOutput(stdout, payload) {
		writeDiagnostic(stderr, operationID, "stdout_write_failed")
		return 70
	}
	return protocol.ExitStatus(err)
}

func invokeHandler(
	ctx context.Context,
	handler plugin.Handler,
	invocation plugin.Invocation,
) (result any, err error, diagnostic string) {
	defer func() {
		if recover() != nil {
			result = nil
			err = internalError("internal_error", "command handler failed")
			diagnostic = "handler_panic"
		}
	}()
	result, err = handler(ctx, invocation)
	return result, err, ""
}

func normalizeExecutionError(err error) error {
	if err == nil {
		return nil
	}
	var typed *protocol.Error
	if errors.As(err, &typed) && typed != nil {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return canceledError()
	}
	return err
}

func stableExecutionError(err error, handlerInvoked bool) error {
	err = normalizeExecutionError(err)
	if err == nil || handlerInvoked {
		return err
	}
	var typed *protocol.Error
	if errors.As(err, &typed) && typed != nil {
		return err
	}
	if strings.Contains(err.Error(), "unknown flag:") || strings.Contains(err.Error(), "flag needs an argument:") {
		return usageError("flag_invalid", "command flags are invalid")
	}
	return usageError("command_not_found", "command was not found")
}

func generateOperationID(generator protocol.OperationIDGenerator) (operationID string, ok bool) {
	ok = false
	defer func() {
		if recover() != nil {
			operationID = ""
			ok = false
		}
	}()
	operationID, err := generator.NewOperationID()
	return operationID, err == nil && protocol.IsValidOperationID(operationID)
}

func encodeEnvelope(envelope protocol.Envelope, compact bool) (payload []byte, err error) {
	defer func() {
		if recover() != nil {
			payload = nil
			err = errors.New("envelope encoding failed")
		}
	}()
	var buffer bytes.Buffer
	if err = protocol.Encode(&buffer, envelope, compact); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeOutput(writer io.Writer, payload []byte) bool {
	if isNil(writer) {
		return false
	}
	written, err := writer.Write(payload)
	return err == nil && written == len(payload)
}

func writeDiagnostic(writer io.Writer, operationID, failureClass string) {
	if isNil(writer) {
		return
	}
	_, _ = fmt.Fprintf(
		writer,
		"nexactl.host operation=%s failure=%s\n",
		operationID,
		failureClass,
	)
}

func usageError(code, message string) error {
	return protocol.NewError(code, "nexactl.host", protocol.CategoryUsage, message, "")
}

func internalError(code, message string) error {
	return protocol.NewError(code, "nexactl.host", protocol.CategoryInternal, message, "")
}

func canceledError() error {
	return protocol.NewError(
		"operation_canceled",
		"nexactl.host",
		protocol.CategoryCanceled,
		"operation was canceled",
		"",
	)
}
