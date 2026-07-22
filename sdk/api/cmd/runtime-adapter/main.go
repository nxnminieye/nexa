package main

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"sync"
	"time"

	api "github.com/nxnminieye/nexa/sdk/api"
	"golang.org/x/sys/unix"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) (exit int) {
	exit = 1
	failureClass := "internal"
	defer func() {
		if recover() != nil {
			writeFailure(stderr, failureClass)
			exit = 1
		}
	}()
	if len(args) != 4 || args[0] != "--runtime-contract" || args[1] == "" || args[2] != "--corpus" || args[3] == "" {
		writeFailure(stderr, "usage")
		return 2
	}
	contractBytes, err := readRegularFile(args[1], api.RuntimeContractLimits().RawBytes)
	if err != nil {
		writeFailure(stderr, "input")
		return 2
	}
	contract, err := api.ParseRuntimeContract(contractBytes)
	if err != nil {
		writeFailure(stderr, "input")
		return 2
	}
	corpusBytes, err := readRegularFile(args[3], api.RuntimeCorpusRawBytes)
	if err != nil {
		writeFailure(stderr, "input")
		return 2
	}
	corpus, err := api.ParseRuntimeCorpus(corpusBytes)
	if err != nil {
		writeFailure(stderr, "input")
		return 2
	}

	failureClass = "execution"
	rows := make([]api.RuntimeAdapterCaseResult, 0, len(corpus.AdapterCases()))
	for _, test := range corpus.AdapterCases() {
		row, executeErr := executeCase(contract, test)
		if executeErr != nil {
			writeFailure(stderr, failureClass)
			return 1
		}
		rows = append(rows, row)
	}
	result, err := api.NewRuntimeAdapterResult(rows)
	if err != nil {
		writeFailure(stderr, "result")
		return 1
	}
	encoded, err := result.CanonicalJSON()
	if err != nil {
		writeFailure(stderr, "result")
		return 1
	}
	failureClass = "output"
	encoded = append(encoded, '\n')
	if written, err := stdout.Write(encoded); err != nil || written != len(encoded) {
		writeFailure(stderr, failureClass)
		return 1
	}
	return 0
}

func readRegularFile(path string, limit int) ([]byte, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("adapter input is invalid")
	}
	file := os.NewFile(uintptr(descriptor), "")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errors.New("adapter input is invalid")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || limit < 0 || !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("adapter input is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("adapter input is invalid")
	}
	return data, nil
}

func executeCase(contract api.RuntimeContract, test api.RuntimeAdapterCase) (api.RuntimeAdapterCaseResult, error) {
	state := &adapterCaseState{}
	ctx := newAdapterContext()
	switch test.ContextBehavior {
	case api.RuntimeAdapterContextActive:
	case api.RuntimeAdapterContextCanceled:
		ctx.fail(context.Canceled)
	case api.RuntimeAdapterContextDeadline:
		ctx.fail(context.DeadlineExceeded)
	default:
		return api.RuntimeAdapterCaseResult{}, errors.New("invalid context behavior")
	}
	endpoint, err := url.Parse(test.Endpoint)
	if err != nil {
		return api.RuntimeAdapterCaseResult{}, err
	}
	provider := api.CredentialProviderFunc(func(context.Context, string) ([]api.CredentialValue, error) {
		state.providerCall()
		switch test.ProviderBehavior {
		case api.RuntimeAdapterProviderValues:
		case api.RuntimeAdapterProviderFailure:
			return nil, errors.New("provider failure")
		case api.RuntimeAdapterProviderPanic:
			panic("provider failure")
		case api.RuntimeAdapterProviderCancel:
			ctx.fail(context.Canceled)
		case api.RuntimeAdapterProviderDeadline:
			ctx.fail(context.DeadlineExceeded)
		default:
			return nil, errors.New("invalid provider behavior")
		}
		values := make([]api.CredentialValue, len(test.Credentials))
		for index, value := range test.Credentials {
			values[index] = api.CredentialValue{ID: value.ID, Value: value.Value}
		}
		return values, nil
	})
	transport := api.TransportFunc(func(_ context.Context, request api.WireRequest) (api.WireResponse, error) {
		state.transportCall(request)
		switch test.Transport.Behavior {
		case api.RuntimeAdapterTransportFailure:
			return api.WireResponse{}, errors.New("transport failure")
		case api.RuntimeAdapterTransportPanic:
			panic("transport failure")
		case api.RuntimeAdapterTransportZero:
			return api.WireResponse{}, nil
		}
		var body *adapterBody
		if test.Transport.ReadBehavior != api.RuntimeAdapterReadAbsent {
			body = &adapterBody{content: []byte(test.Transport.Body), readBehavior: test.Transport.ReadBehavior, closeBehavior: test.Transport.CloseBehavior, context: ctx, state: state}
		}
		headers := make([]api.Header, len(test.Transport.Headers))
		for index, header := range test.Transport.Headers {
			headers[index] = api.Header{Name: header.Name, Value: header.Value}
		}
		response, responseErr := api.NewWireResponse(test.Transport.Status, headers, body)
		if responseErr != nil {
			closeAdapterBody(body)
			return api.WireResponse{}, responseErr
		}
		switch test.Transport.Behavior {
		case api.RuntimeAdapterTransportResponse:
			return response, nil
		case api.RuntimeAdapterTransportResponseAndFailure:
			return response, errors.New("transport failure")
		case api.RuntimeAdapterTransportCancel:
			ctx.fail(context.Canceled)
			return response, nil
		case api.RuntimeAdapterTransportDeadline:
			ctx.fail(context.DeadlineExceeded)
			return response, nil
		default:
			return response, errors.New("invalid transport behavior")
		}
	})
	client, callErr := api.NewRuntime(api.RuntimeOptions{
		RuntimeContract: contract, Endpoint: endpoint, Transport: transport,
		CredentialProvider: provider, MaxResponseBytes: test.MaxResponseBytes,
	})
	if callErr == nil {
		var request api.Request
		request, callErr = api.ParseRequest([]byte(test.Request))
		if callErr == nil {
			var result api.Result
			result, callErr = client.Call(ctx, test.APIOperationID, request)
			if callErr == nil {
				return state.resultRow(test.Name, result, nil)
			}
		}
	}
	return state.resultRow(test.Name, api.Result{}, callErr)
}

func closeAdapterBody(body io.Closer) {
	defer func() { _ = recover() }()
	_ = body.Close()
}

type adapterCaseState struct {
	mu             sync.Mutex
	requestDigest  string
	providerCalls  int
	transportCalls int
	bodyReadCalls  int
	bodyCloseCalls int
}

func (s *adapterCaseState) providerCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerCalls++
}

func (s *adapterCaseState) transportCall(request api.WireRequest) {
	urlValue := request.URL().String()
	headers := request.Headers()
	projectedHeaders := make([]api.RuntimeAdapterHeader, len(headers))
	for index, header := range headers {
		projectedHeaders[index] = api.RuntimeAdapterHeader{Name: header.Name, Value: header.Value}
	}
	var body *string
	if raw := request.Body(); raw != nil {
		value := string(raw)
		body = &value
	}
	digest, err := (api.RuntimeAdapterRequest{Method: string(request.Method()), URL: urlValue, Headers: projectedHeaders, Body: body}).Digest()
	if err != nil {
		panic("logical request digest failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transportCalls++
	s.requestDigest = digest.String()
}

func (s *adapterCaseState) bodyRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodyReadCalls++
}

func (s *adapterCaseState) bodyClose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodyCloseCalls++
}

func (s *adapterCaseState) resultRow(name string, result api.Result, callErr error) (api.RuntimeAdapterCaseResult, error) {
	s.mu.Lock()
	requestDigest := s.requestDigest
	if requestDigest == "" {
		requestDigest = "absent"
	}
	row := api.RuntimeAdapterCaseResult{
		Name: name, RequestDigest: requestDigest, ProviderCalls: s.providerCalls, TransportCalls: s.transportCalls,
		BodyReadCalls: s.bodyReadCalls, BodyCloseCalls: s.bodyCloseCalls,
	}
	s.mu.Unlock()
	if callErr == nil {
		canonical, hasJSON := result.JSON()
		row.Outcome.Success = &api.RuntimeAdapterSuccess{
			APIOperationID: result.APIOperationID(), HTTPStatus: result.HTTPStatus(), ResponseBody: result.ResponseBody(),
			HasJSON: hasJSON, CanonicalJSON: string(canonical),
		}
		return row, nil
	}
	apiError, ok := callErr.(*api.Error)
	if !ok || apiError == nil {
		return api.RuntimeAdapterCaseResult{}, errors.New("unexpected adapter error")
	}
	details := apiError.Details()
	row.Outcome.Error = &api.RuntimeAdapterError{
		Domain: apiError.Domain(), Code: apiError.Code(), Message: apiError.Error(), Category: apiError.Category(),
		Retryable: apiError.Retryable(), APIOperationID: apiError.APIOperationID(), RequestID: apiError.RequestID(),
		TraceID: apiError.TraceID(), Reason: details.Reason(), Pointer: details.Pointer(), HTTPStatus: details.HTTPStatus(),
		RemoteDomain: details.RemoteDomain(), RemoteCode: details.RemoteCode(), RemoteDetailsAbsent: true,
	}
	return row, nil
}

type adapterBody struct {
	content       []byte
	offset        int
	readBehavior  api.RuntimeAdapterReadBehavior
	closeBehavior api.RuntimeAdapterCloseBehavior
	context       *adapterContext
	state         *adapterCaseState
}

func (b *adapterBody) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	b.state.bodyRead()
	switch b.readBehavior {
	case api.RuntimeAdapterReadFailure:
		return 0, errors.New("read failure")
	case api.RuntimeAdapterReadPanic, api.RuntimeAdapterReadForbidden:
		panic("read failure")
	case api.RuntimeAdapterReadCancel:
		b.context.fail(context.Canceled)
		return 0, io.EOF
	case api.RuntimeAdapterReadDeadline:
		b.context.fail(context.DeadlineExceeded)
		return 0, io.EOF
	case api.RuntimeAdapterReadOneByte:
		if b.offset >= len(b.content) {
			return 0, io.EOF
		}
		target[0] = b.content[b.offset]
		b.offset++
		if b.offset == len(b.content) {
			return 1, io.EOF
		}
		return 1, nil
	case api.RuntimeAdapterReadAll:
		if b.offset >= len(b.content) {
			return 0, io.EOF
		}
		n := copy(target, b.content[b.offset:])
		b.offset += n
		if b.offset == len(b.content) {
			return n, io.EOF
		}
		return n, nil
	default:
		return 0, errors.New("invalid read behavior")
	}
}

func (b *adapterBody) Close() error {
	b.state.bodyClose()
	switch b.closeBehavior {
	case api.RuntimeAdapterCloseSuccess:
		return nil
	case api.RuntimeAdapterCloseFailure:
		return errors.New("close failure")
	case api.RuntimeAdapterClosePanic:
		panic("close failure")
	default:
		return errors.New("invalid close behavior")
	}
}

type adapterContext struct {
	mu   sync.RWMutex
	done chan struct{}
	err  error
	once sync.Once
}

func newAdapterContext() *adapterContext { return &adapterContext{done: make(chan struct{})} }

func (c *adapterContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *adapterContext) Done() <-chan struct{}       { return c.done }
func (c *adapterContext) Value(any) any               { return nil }
func (c *adapterContext) Err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}

func (c *adapterContext) fail(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
}

func writeFailure(stderr io.Writer, class string) {
	_, _ = io.WriteString(stderr, "nexa.runtime-adapter failure="+class+"\n")
}
