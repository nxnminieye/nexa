package qualityapp

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/quality/readmodel"
)

type projectionSource struct {
	snapshot readmodel.Snapshot
	err      error
}

func (s projectionSource) Load(context.Context) (readmodel.Snapshot, error) {
	return s.snapshot, s.err
}

func TestNilSourceReturnsCanonicalEmptyAndHealthy(t *testing.T) {
	server, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := server.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := readmodel.CanonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := readmodel.CanonicalJSON(readmodel.Empty())
	if !bytes.Equal(got, want) || !server.Health(context.Background()).Ready() {
		t.Fatalf("empty = %s health=%#v", got, server.Health(context.Background()))
	}
}

func TestConfiguredSourceIsImmutableAndConcurrent(t *testing.T) {
	ref, err := provenance.RepositoryRef("requirements/sample.yaml", "requirement:sample")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := readmodel.NewSnapshot(readmodel.SnapshotSpec{
		SourceProfile: "local", ReadModelScope: "workspace", Revision: "r1",
		Requirements: []readmodel.RequirementCoverageSpec{{Requirement: ref, Title: "Sample", Status: "covered", FreezeStatus: readmodel.FreezeNone}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(projectionSource{snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := readmodel.CanonicalJSON(snapshot)

	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			loaded, loadErr := server.Snapshot(context.Background())
			if loadErr != nil {
				t.Errorf("Snapshot() error = %v", loadErr)
				return
			}
			rows := loaded.Requirements()
			rows[0] = readmodel.RequirementCoverage{}
			got, canonicalErr := readmodel.CanonicalJSON(loaded)
			if canonicalErr != nil || !bytes.Equal(got, want) {
				t.Errorf("snapshot = %s, %v", got, canonicalErr)
			}
		}()
	}
	group.Wait()
}

func TestSourceErrorAndCancellationAreStable(t *testing.T) {
	secret := errors.New("private source detail")
	server, err := NewServer(projectionSource{err: secret})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Snapshot(context.Background())
	var projected *Error
	if !errors.As(err, &projected) || projected.Code() != CodeProjectionUnavailable || errors.Is(err, secret) || bytes.Contains([]byte(err.Error()), []byte("private")) {
		t.Fatalf("source error = %#v", err)
	}
	if health := server.Health(context.Background()); health.Ready() || health.Code() != CodeProjectionUnavailable {
		t.Fatalf("health = %#v", health)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = server.Snapshot(ctx)
	if !errors.As(err, &projected) || projected.Code() != CodeOperationCanceled {
		t.Fatalf("cancellation = %#v", err)
	}
}
