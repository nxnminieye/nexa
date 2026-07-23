package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRuntimePackagesExternalConsumers(t *testing.T) {
	runRuntimeExternalConsumerMatrix(t)
}

func runRuntimeExternalConsumerMatrix(t *testing.T) {
	t.Helper()
	temporary := t.TempDir()
	moduleRoot := filepath.Join(temporary, "consumer")
	if err := os.MkdirAll(filepath.Join(moduleRoot, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/runtime-consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n")

	programs := []struct {
		name   string
		source string
	}{
		{name: "crud", source: runtimeCRUDConsumerProgram},
		{name: "kafka", source: runtimeKafkaConsumerProgram},
		{name: "franz", source: runtimeFranzConsumerProgram},
		{name: "s3", source: runtimeS3ConsumerProgram},
		{name: "s3aws", source: runtimeS3AWSConsumerProgram},
		{name: "logging", source: runtimeLoggingConsumerProgram},
		{name: "rpcaccess", source: runtimeRPCAccessConsumerProgram},
		{name: "otel", source: runtimeOTelConsumerProgram},
	}
	for _, program := range programs {
		directory := filepath.Join(moduleRoot, "cmd", program.name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, filepath.Join(directory, "main.go"), program.source)
	}

	environment := prepareHermeticExternalModule(t, temporary, moduleRoot)
	t.Cleanup(func() {
		clean := exec.Command("go", "clean", "-modcache")
		clean.Env = environment
		_ = clean.Run()
	})
	for _, program := range programs {
		t.Run(program.name, func(t *testing.T) {
			output := runRuntimeConsumerCommand(t, moduleRoot, environment, "go", "run", "-mod=readonly", "./cmd/"+program.name)
			var result struct {
				Name     string `json:"name"`
				Positive bool   `json:"positive"`
				Failure  string `json:"failure"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("decode %s output: %v\n%s", program.name, err, output)
			}
			if result.Name != program.name || !result.Positive {
				t.Fatalf("%s result = %#v", program.name, result)
			}
		})
	}
}

func runRuntimeConsumerCommand(t *testing.T, directory string, environment []string, name string, args ...string) []byte {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return output
}

const runtimeCRUDConsumerProgram = `package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/nxnminieye/nexa/runtime/crud"
)

func main() {
	object, err := crud.ParseJSONObject([]byte("{\"b\":2,\"a\":1}"))
	if err != nil { panic(err) }
	normalized, err := object.String()
	if err != nil { panic(err) }
	policy, err := crud.NewWindowPolicy(crud.WindowPolicySpec{MinLimit: 1, MaxLimit: 100, MaxOffset: 1000})
	if err != nil { panic(err) }
	window, err := policy.Check(20, 40)
	if err != nil { panic(err) }
	_, failure := crud.ParseJSONObject([]byte("[]"))
	var typed *crud.Error
	if !errors.As(failure, &typed) { panic("missing typed failure") }
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "crud", "positive": normalized == "{\"a\":1,\"b\":2}" && window.Limit() == 20 && window.Offset() == 40,
		"failure": typed.Reason(),
	})
}
`

const runtimeKafkaConsumerProgram = `package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/nxnminieye/nexa/runtime/kafka"
)

type fakeReader struct{}
func (fakeReader) Poll(context.Context) (kafka.Batch, error) { return kafka.Batch{}, errors.New("unused") }
func (fakeReader) Commit(context.Context) error { return nil }
func (fakeReader) Close() error { return nil }
type fakeFactory struct{}
func (fakeFactory) Open(context.Context, kafka.Subscription) (kafka.Reader, error) { return fakeReader{}, nil }

func main() {
	subscription, err := kafka.NewSubscription(kafka.SubscriptionSpec{
		ID: "orders", Group: "orders", Topics: []string{"orders"},
		Handler: kafka.HandlerFunc(func(context.Context, kafka.Record) error { return nil }),
	})
	if err != nil { panic(err) }
	policy := kafka.RetryPolicyFunc(func(kafka.Failure) kafka.RetryDecision { return kafka.RetryDecision{} })
	manager, err := kafka.NewManager(kafka.ManagerOptions{
		Subscriptions: []kafka.Subscription{subscription}, ReaderFactory: fakeFactory{}, RetryPolicy: policy,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil { panic(err) }
	_, failure := kafka.NewManager(kafka.ManagerOptions{})
	var typed *kafka.Error
	if !errors.As(failure, &typed) { panic("missing typed failure") }
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "kafka", "positive": manager.State() == kafka.StateNew && !policy.Decide(kafka.Failure{}).Retry,
		"failure": typed.Reason(),
	})
}
`

const runtimeFranzConsumerProgram = `package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"

	"github.com/nxnminieye/nexa/runtime/kafka/franz"
	"github.com/twmb/franz-go/pkg/kgo"
)

func main() {
	dials := 0
	dialer := kgo.Dialer(func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("dial must not run")
	})
	factory, err := franz.NewReaderFactory(franz.ReaderFactoryOptions{
		Brokers: []string{"127.0.0.1:9092"}, MaxPollRecords: 10, ClientOptions: []kgo.Opt{dialer},
	})
	if err != nil { panic(err) }
	_, failure := franz.NewReaderFactory(franz.ReaderFactoryOptions{MaxPollRecords: 10})
	var typed *franz.Error
	if !errors.As(failure, &typed) { panic("missing typed failure") }
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "franz", "positive": factory != nil && dials == 0, "failure": typed.Reason(),
	})
}
`

const runtimeS3ConsumerProgram = `package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"

	"github.com/nxnminieye/nexa/runtime/s3"
)

func main() {
	ref, err := s3.NewObjectRef("objects", "reports/current.json")
	if err != nil { panic(err) }
	request, err := s3.NewPutRequest(s3.PutRequestSpec{Ref: ref, Body: bytes.NewReader([]byte("{}")), Metadata: map[string]string{"owner": "consumer"}})
	if err != nil { panic(err) }
	_, failure := s3.NewObjectRef("", "key")
	var typed *s3.Error
	if !errors.As(failure, &typed) { panic("missing typed failure") }
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "s3", "positive": request.Valid() && request.Ref() == ref && request.Metadata()["owner"] == "consumer",
		"failure": typed.Reason(),
	})
}
`

const runtimeS3AWSConsumerProgram = `package main

import (
	"encoding/json"
	"errors"
	"os"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	cores3 "github.com/nxnminieye/nexa/runtime/s3"
	s3aws "github.com/nxnminieye/nexa/runtime/s3/aws"
)

func main() {
	client, err := s3aws.NewClient(s3aws.ClientOptions{Config: sdkaws.Config{Region: "us-east-1"}, Endpoint: "https://objects.example.test", UsePathStyle: true})
	if err != nil { panic(err) }
	_, failure := s3aws.NewClient(s3aws.ClientOptions{Config: sdkaws.Config{Region: "us-east-1"}, Endpoint: "http://objects.example.test"})
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "s3aws", "positive": client != nil && errors.Is(failure, cores3.ErrValidation),
	})
}
`

const runtimeLoggingConsumerProgram = `package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"

	"github.com/nxnminieye/nexa/runtime/observability/logging"
)

type recorder struct{ attrs []slog.Attr }
func (*recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(attr slog.Attr) bool { r.attrs = append(r.attrs, attr); return true })
	return nil
}
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler { return r }

func main() {
	fields, err := logging.NewContextFields(logging.ContextFieldsSpec{RequestID: "request-1"})
	if err != nil { panic(err) }
	next := &recorder{}
	handler, err := logging.NewHandler(logging.HandlerOptions{
		Next: next,
		Redactor: logging.RedactorFunc(func(_ []string, attr slog.Attr) (slog.Attr, bool) { return attr, attr.Key != "secret" }),
	})
	if err != nil { panic(err) }
	attrs := append(fields.Attrs(), slog.String("secret", "hidden"))
	slog.New(handler).LogAttrs(context.Background(), slog.LevelInfo, "event", attrs...)
	_, failure := logging.NewContextFields(logging.ContextFieldsSpec{RequestID: " invalid"})
	var typed *logging.Error
	if !errors.As(failure, &typed) { panic("missing typed failure") }
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "logging", "positive": len(next.attrs) == 1 && next.attrs[0].Key == logging.FieldRequestID,
		"failure": typed.Reason(),
	})
}
`

const runtimeRPCAccessConsumerProgram = `package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/nxnminieye/nexa/runtime/observability/rpcaccess"
	"google.golang.org/grpc"
)

type recorder struct{ attrs []slog.Attr }
func (*recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(attr slog.Attr) bool { r.attrs = append(r.attrs, attr); return true })
	return nil
}
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler { return r }

func main() {
	next := &recorder{}
	interceptor, err := rpcaccess.UnaryServerInterceptor(rpcaccess.Options{
		Logger: slog.New(next),
		Extractor: rpcaccess.ExtractorFunc(func(context.Context, string) (rpcaccess.RequestContext, error) { return rpcaccess.RequestContext{}, nil }),
	})
	if err != nil { panic(err) }
	response, callErr := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{FullMethod: "/sample.Service/Get"}, func(context.Context, any) (any, error) { return "response", nil })
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "rpcaccess", "positive": callErr == nil && response == "response" && len(next.attrs) == 3,
	})
}
`

const runtimeOTelConsumerProgram = `package main

import (
	"context"
	"encoding/json"
	"os"

	rpcotel "github.com/nxnminieye/nexa/runtime/observability/rpcaccess/otel"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	extractor := rpcotel.NewExtractor()
	requestContext, err := extractor.Extract(ctx, "/sample.Service/Get")
	if err != nil { panic(err) }
	empty, err := extractor.Extract(context.Background(), "/sample.Service/Get")
	if err != nil { panic(err) }
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"name": "otel", "positive": len(requestContext.Attrs()) == 2 && len(empty.Attrs()) == 0,
	})
}
`
