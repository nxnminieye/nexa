package coreapp

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCatalogSyncCanonicalizesAndKeepsSourceAtomic(t *testing.T) {
	events := []string{}
	store := &fakeCatalogStore{events: &events}
	reconciler := &recordingReconciler{events: &events}
	service, err := NewCatalogService(store, reconciler)
	if err != nil {
		t.Fatal(err)
	}

	permissions := []PermissionCatalogEntry{{Code: " write ", Description: " Write "}, {Code: "read"}, {Code: "read"}}
	menus := []MenuCatalogEntry{{Code: " settings ", DisplayName: " Settings ", Path: " /settings "}, {Code: "home", DisplayName: "Home"}}
	digest, err := CanonicalCatalogDigest(permissions, menus)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Sync(context.Background(), CatalogSyncInput{
		SourceID: " source-a ", Digest: " " + digest + " ", Permissions: permissions, Menus: menus,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceID != "source-a" || result.Digest != digest {
		t.Fatalf("result = %#v", result)
	}
	if len(store.inputs) != 1 {
		t.Fatalf("sync calls = %d", len(store.inputs))
	}
	input := store.inputs[0]
	if !input.DisableStale {
		t.Fatal("atomic stale disable was not requested")
	}
	if got := input.Permissions; !reflect.DeepEqual(got, []PermissionCatalogEntry{{Code: "read"}, {Code: "write", Description: "Write"}}) {
		t.Fatalf("permissions = %#v", got)
	}
	if got := input.Menus; !reflect.DeepEqual(got, []MenuCatalogEntry{{Code: "home", DisplayName: "Home"}, {Code: "settings", DisplayName: "Settings", Path: "/settings"}}) {
		t.Fatalf("menus = %#v", got)
	}
	if !reflect.DeepEqual(events, []string{"store.catalog.source-a", "reconcile.catalog"}) {
		t.Fatalf("sequencing = %#v", events)
	}
}

func TestCatalogSyncRejectsConflictingDuplicateAndDoesNotReconcileStoreFailure(t *testing.T) {
	store := &fakeCatalogStore{}
	reconciler := &recordingReconciler{}
	service, err := NewCatalogService(store, reconciler)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Sync(context.Background(), CatalogSyncInput{SourceID: "source-a", Digest: "digest", Permissions: []PermissionCatalogEntry{{Code: "read"}, {Code: "read", Description: "different"}}})
	assertCode(t, err, CodeInvalidInput)
	if len(store.inputs) != 0 {
		t.Fatal("invalid catalog reached store")
	}

	digest, digestErr := CanonicalCatalogDigest(nil, nil)
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	store.err = errors.Join(ErrStoreConcurrentWrite, errors.New("private detail"))
	_, err = service.Sync(context.Background(), CatalogSyncInput{SourceID: "source-b", Digest: digest})
	assertIAMCodeAndRedaction(t, err, CodeConcurrentWrite, "private detail")
	if len(reconciler.calls) != 0 {
		t.Fatalf("failed sync reconciled: %#v", reconciler.calls)
	}
	if len(store.inputs) != 1 || store.inputs[0].SourceID != "source-b" {
		t.Fatalf("source isolation lost: %#v", store.inputs)
	}
}

func TestCatalogDigestIsCanonicalAndSyncRejectsDigestPayloadMismatch(t *testing.T) {
	permissions := []PermissionCatalogEntry{{Code: "write"}, {Code: "read"}, {Code: "read"}}
	menus := []MenuCatalogEntry{{Code: "settings", DisplayName: "Settings"}, {Code: "home", DisplayName: "Home"}}
	first, err := CanonicalCatalogDigest(permissions, menus)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalCatalogDigest([]PermissionCatalogEntry{{Code: "read"}, {Code: "write"}}, []MenuCatalogEntry{{Code: "home", DisplayName: "Home"}, {Code: "settings", DisplayName: "Settings"}})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical digest differs: %q != %q", first, second)
	}
	store := &fakeCatalogStore{}
	service, err := NewCatalogService(store, &recordingReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Sync(context.Background(), CatalogSyncInput{SourceID: "source-a", Digest: first, Permissions: []PermissionCatalogEntry{{Code: "different"}}, Menus: menus})
	assertCode(t, err, CodeInvalidInput)
	if len(store.inputs) != 0 {
		t.Fatal("digest/payload mismatch reached store")
	}
}

type fakeCatalogStore struct {
	inputs []CatalogSyncStoreInput
	events *[]string
	err    error
}

func (s *fakeCatalogStore) SyncCatalog(_ context.Context, input CatalogSyncStoreInput) (CatalogSyncResult, error) {
	s.inputs = append(s.inputs, input)
	if s.events != nil {
		*s.events = append(*s.events, "store.catalog."+input.SourceID)
	}
	if s.err != nil {
		return CatalogSyncResult{}, s.err
	}
	return CatalogSyncResult{SourceID: input.SourceID, Digest: input.Digest, PermissionsUpserted: len(input.Permissions), MenusUpserted: len(input.Menus)}, nil
}
