package bundletest

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestBundleTestExecutesCopiedModule(t *testing.T) {
	t.Parallel()

	err := Run(t.Context(), Module{
		Path: "example.com/bundle",
		Source: fstest.MapFS{
			"bundle_test.go": &fstest.MapFile{Data: []byte("package bundle\nimport \"testing\"\nfunc TestCopied(t *testing.T) {}\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestModuleTestRunnerReceivesExplicitRaceArgument(t *testing.T) {
	t.Setenv("GOFLAGS", "-race")

	runner := &recordingRunner{}
	_, err := runModuleTests(context.Background(), "/tmp/materialized", []string{"./backend/core/..."}, Options{Race: true}, runner)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"test", "-race", "-mod=mod", "./backend/core/..."}
	if !reflect.DeepEqual(runner.arguments, want) {
		t.Fatalf("go arguments = %#v, want %#v", runner.arguments, want)
	}
	if got := environmentValue(runner.environment, "GOFLAGS"); got != "" {
		t.Fatalf("GOFLAGS = %q, want empty because race must be an explicit argument", got)
	}
}

type recordingRunner struct {
	arguments   []string
	environment []string
}

func (r *recordingRunner) Run(_ context.Context, command moduleTestCommand) ([]byte, error) {
	r.arguments = append([]string(nil), command.Arguments...)
	r.environment = append([]string(nil), command.Environment...)
	return nil, nil
}

func environmentValue(environment []string, key string) string {
	prefix := key + "="
	for _, entry := range environment {
		if len(entry) >= len(prefix) && entry[:len(prefix)] == prefix {
			return entry[len(prefix):]
		}
	}
	return ""
}
