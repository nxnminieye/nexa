package kafka_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestExternalConsumerUsesOnlyPublicKafkaContracts(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot = spacedRepositoryRoot(t, repositoryRoot)
	moduleRoot := t.TempDir()
	module := "module example.com/kafka-consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\n" +
		"replace github.com/nxnminieye/nexa => " + strconv.Quote(filepath.ToSlash(repositoryRoot)) + "\n"
	writeExternalFile(t, filepath.Join(moduleRoot, "go.mod"), module)
	writeExternalFile(t, filepath.Join(moduleRoot, "contract_test.go"), externalConsumerSource)

	environment := externalGoEnvironment(t, t.TempDir())
	runExternalGo(t, moduleRoot, environment, "prepare external consumer", "mod", "tidy")
	runExternalGo(t, moduleRoot, environment, "test external consumer", "test", "-mod=readonly", "./...")
}

func spacedRepositoryRoot(t *testing.T, repositoryRoot string) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "checkout with space")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(parent, "nexa")
	if err := os.Symlink(repositoryRoot, linkedRoot); err != nil {
		t.Fatalf("create spaced checkout alias: %v", err)
	}
	return linkedRoot
}

func writeExternalFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func externalGoEnvironment(t *testing.T, temporary string) []string {
	t.Helper()
	paths := []string{
		filepath.Join(temporary, "build-cache"),
		filepath.Join(temporary, "module-cache"),
		filepath.Join(temporary, "home"),
		filepath.Join(temporary, "xdg-config"),
		filepath.Join(temporary, "xdg-cache"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{
		"GOWORK=off",
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"GOCACHE=" + paths[0],
		"GOMODCACHE=" + paths[1],
		"HOME=" + paths[2],
		"XDG_CONFIG_HOME=" + paths[3],
		"XDG_CACHE_HOME=" + paths[4],
	}
	for _, name := range []string{
		"PATH", "TMPDIR", "GOPROXY", "GONOPROXY", "GOSUMDB", "GONOSUMDB", "GOPRIVATE",
		"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY",
	} {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

func runExternalGo(t *testing.T, directory string, environment []string, stage string, arguments ...string) {
	t.Helper()
	command := exec.Command("go", arguments...)
	command.Dir = directory
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", stage, err, output)
	}
}

const externalConsumerSource = `package consumer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nxnminieye/nexa/runtime/kafka"
)

func TestPublicValuesAndTypedFailure(t *testing.T) {
	record, err := kafka.NewRecord(kafka.RecordSpec{
		Topic: "sample.topic",
		Key: []byte{},
		Value: nil,
		Headers: []kafka.Header{{Key: "x-value", Value: []byte("one")}, {Key: "x-value", Value: []byte("two")}},
	})
	if err != nil { t.Fatal(err) }
	if !record.Valid() || record.Key() == nil || record.Value() != nil || len(record.Headers()) != 2 { t.Fatal("record contract changed") }
	batch, err := kafka.NewBatch([]kafka.Record{record})
	if err != nil || !batch.Valid() || batch.Len() != 1 { t.Fatalf("batch = %#v, %v", batch, err) }

	var calls int
	subscription, err := kafka.NewSubscription(kafka.SubscriptionSpec{
		ID: "sample.worker",
		Group: "sample-group",
		Topics: []string{"sample.topic"},
		Handler: kafka.HandlerFunc(func(_ context.Context, got kafka.Record) error {
			calls++
			if got.Topic() != "sample.topic" { t.Fatal("wrong record") }
			return nil
		}),
	})
	if err != nil { t.Fatal(err) }
	if err := subscription.Handler().Handle(context.Background(), batch.Records()[0]); err != nil || calls != 1 { t.Fatalf("handler = %v, %d", err, calls) }

	message, err := kafka.NewMessage(kafka.MessageSpec{Topic: "sample.topic", Value: []byte{}})
	if err != nil || !message.Valid() || message.Value() == nil { t.Fatalf("message = %#v, %v", message, err) }

	_, err = kafka.NewRecord(kafka.RecordSpec{Topic: "."})
	var typed *kafka.Error
	if !errors.As(err, &typed) || typed.Code() != "configuration_invalid" || typed.Reason() != "topic_invalid" || typed.Pointer() != "/topic" || !errors.Is(err, kafka.ErrConfigurationInvalid) {
		t.Fatalf("typed error = %T %v", err, err)
	}
}
`
