package transaction_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

const validGenerationOwner = "nexa.dev/generator/crud-proto/v1"

func TestCompatibilityLockMutationIsClosedSortedAndDefensive(t *testing.T) {
	path := "backend/core/rpc/desc/account.crud.lock.json"
	before := provenance.Source{Ref: mustRepositoryRef(t, path, ""), Digest: provenance.SHA256([]byte("before"))}
	after := []byte(`{"apiVersion":"nexa.dev/crud-protocol-lock/v1"}`)
	sources := []provenance.SourceRef{
		mustRepositoryRef(t, "backend/core/rpc/ent/schema/account.go", "field:id"),
		mustRepositoryRef(t, "backend/core/rpc/ent/schema/account.go", "schema:Account"),
	}
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-crud-lock", Path: path, Owner: validGenerationOwner,
		Before: &before, After: after, AfterDigest: provenance.SHA256(after), Sources: sources,
	})
	if err != nil {
		t.Fatalf("NewCompatibilityLockMutation() error = %v", err)
	}

	after[0] = '!'
	sources[0] = provenance.SourceRef{}
	before = provenance.Source{}
	if mutation.Role() != transaction.ControlSourceCompatibilityLock || mutation.ID() != "account-crud-lock" || mutation.Path() != path || mutation.Owner() != validGenerationOwner {
		t.Fatalf("mutation identity = %q/%q/%q/%q", mutation.Role(), mutation.ID(), mutation.Path(), mutation.Owner())
	}
	gotBefore, ok := mutation.Before()
	if !ok || gotBefore.Ref.String() != "repo:"+path || gotBefore.Digest != provenance.SHA256([]byte("before")) {
		t.Fatalf("Before() = %#v, %v", gotBefore, ok)
	}
	wantAfter := []byte(`{"apiVersion":"nexa.dev/crud-protocol-lock/v1"}`)
	if got := mutation.After(); !bytes.Equal(got, wantAfter) {
		t.Fatalf("After() = %q", got)
	}
	returnedAfter := mutation.After()
	returnedAfter[0] = '!'
	if !bytes.Equal(mutation.After(), wantAfter) {
		t.Fatal("After() returned aliased bytes")
	}
	if mutation.AfterDigest() != provenance.SHA256(wantAfter) {
		t.Fatalf("AfterDigest() = %s", mutation.AfterDigest().String())
	}
	wantSources := []string{
		"repo:backend/core/rpc/ent/schema/account.go#field%3Aid",
		"repo:backend/core/rpc/ent/schema/account.go#schema%3AAccount",
	}
	if got := sourceStrings(mutation.Sources()); !reflect.DeepEqual(got, wantSources) {
		t.Fatalf("Sources() = %#v, want %#v", got, wantSources)
	}
	returnedSources := mutation.Sources()
	returnedSources[0] = provenance.SourceRef{}
	if got := sourceStrings(mutation.Sources()); !reflect.DeepEqual(got, wantSources) {
		t.Fatal("Sources() returned an aliased slice")
	}
}

func TestCompatibilityLockMutationInitialAndNoopStates(t *testing.T) {
	path := "generated/account.crud.lock.json"
	after := []byte("{}")
	initial, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-lock", Path: path, Owner: validGenerationOwner, After: after,
		AfterDigest: provenance.SHA256(after), Sources: []provenance.SourceRef{mustRepositoryRef(t, "schema/account.go", "Account")},
	})
	if err != nil {
		t.Fatalf("initial mutation error = %v", err)
	}
	if _, ok := initial.Before(); ok {
		t.Fatal("initial mutation unexpectedly has Before")
	}

	digest := provenance.SHA256(after)
	before := provenance.Source{Ref: mustRepositoryRef(t, path, ""), Digest: digest}
	noop, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-lock", Path: path, Owner: validGenerationOwner, Before: &before, After: after,
		AfterDigest: digest, Sources: []provenance.SourceRef{mustRepositoryRef(t, "schema/account.go", "Account")},
	})
	if err != nil {
		t.Fatalf("noop mutation error = %v", err)
	}
	if noop.Role() != "" || noop.ID() != "" || noop.Path() != "" || noop.Owner() != "" || len(noop.After()) != 0 || noop.AfterDigest().String() != "" || len(noop.Sources()) != 0 {
		t.Fatalf("noop mutation is not zero: role=%q id=%q path=%q", noop.Role(), noop.ID(), noop.Path())
	}
	if _, ok := noop.Before(); ok {
		t.Fatal("zero noop mutation unexpectedly has Before")
	}
}

func TestCompatibilityLockMutationRejectsInvalidMembers(t *testing.T) {
	path := "generated/account.crud.lock.json"
	after := []byte("{}")
	valid := func() transaction.CompatibilityLockMutationSpec {
		return transaction.CompatibilityLockMutationSpec{
			ID: "account-lock", Path: path, Owner: validGenerationOwner, After: append([]byte(nil), after...),
			AfterDigest: provenance.SHA256(after), Sources: []provenance.SourceRef{mustRepositoryRef(t, "schema/account.go", "Account")},
		}
	}
	tests := []struct {
		name, reason, pointer string
		mutate                func(*transaction.CompatibilityLockMutationSpec)
	}{
		{name: "id", reason: "id_invalid", pointer: "/id", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.ID = "Account_Lock" }},
		{name: "path absolute", reason: "path_invalid", pointer: "/path", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Path = "/generated/lock.json" }},
		{name: "path traversal", reason: "path_invalid", pointer: "/path", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Path = "generated/../lock.json" }},
		{name: "owner plain identifier", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = "crud-proto" }},
		{name: "owner empty segment", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = "nexa.dev/generator//v1" }},
		{name: "owner dot segment", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = "nexa.dev/generator/./v1" }},
		{name: "owner parent segment", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = "nexa.dev/generator/../v1" }},
		{name: "owner zero version", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = "nexa.dev/generator/crud-proto/v0" }},
		{name: "owner negative version", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = "nexa.dev/generator/crud-proto/v-1" }},
		{name: "owner missing version", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = "nexa.dev/generator/crud-proto" }},
		{name: "owner trailing slash", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = validGenerationOwner + "/" }},
		{name: "owner query", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = validGenerationOwner + "?profile=test" }},
		{name: "owner fragment", reason: "owner_invalid", pointer: "/owner", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Owner = validGenerationOwner + "#current" }},
		{name: "before zero ref", reason: "before_source_invalid", pointer: "/before", mutate: func(s *transaction.CompatibilityLockMutationSpec) {
			s.Before = &provenance.Source{Digest: provenance.SHA256([]byte("old"))}
		}},
		{name: "before fragment", reason: "before_source_invalid", pointer: "/before", mutate: func(s *transaction.CompatibilityLockMutationSpec) {
			s.Before = &provenance.Source{Ref: mustRepositoryRef(t, path, "node"), Digest: provenance.SHA256([]byte("old"))}
		}},
		{name: "before wrong path", reason: "before_source_invalid", pointer: "/before", mutate: func(s *transaction.CompatibilityLockMutationSpec) {
			s.Before = &provenance.Source{Ref: mustRepositoryRef(t, "generated/other.lock.json", ""), Digest: provenance.SHA256([]byte("old"))}
		}},
		{name: "before zero digest", reason: "before_source_invalid", pointer: "/before", mutate: func(s *transaction.CompatibilityLockMutationSpec) {
			s.Before = &provenance.Source{Ref: mustRepositoryRef(t, path, "")}
		}},
		{name: "after empty", reason: "after_empty", pointer: "/after", mutate: func(s *transaction.CompatibilityLockMutationSpec) {
			s.After = nil
			s.AfterDigest = provenance.SHA256(nil)
		}},
		{name: "after zero digest", reason: "after_digest_mismatch", pointer: "/afterDigest", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.AfterDigest = provenance.Digest{} }},
		{name: "after digest mismatch", reason: "after_digest_mismatch", pointer: "/afterDigest", mutate: func(s *transaction.CompatibilityLockMutationSpec) {
			s.AfterDigest = provenance.SHA256([]byte("different"))
		}},
		{name: "sources empty", reason: "source_ref_invalid", pointer: "/sources", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Sources = nil }},
		{name: "source zero", reason: "source_ref_invalid", pointer: "/sources/0", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Sources[0] = provenance.SourceRef{} }},
		{name: "source duplicate", reason: "source_ref_duplicate", pointer: "/sources/1", mutate: func(s *transaction.CompatibilityLockMutationSpec) { s.Sources = append(s.Sources, s.Sources[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := valid()
			test.mutate(&spec)
			_, err := transaction.NewCompatibilityLockMutation(spec)
			transactionError := requireTransactionError(t, err, "transaction_control_source_invalid", "input", test.reason, test.pointer)
			if cause := transactionError.Unwrap(); cause == nil || cause == err {
				t.Fatalf("Unwrap() = %v", cause)
			}
		})
	}
}

func TestGenerationTransactionInputConstantsAreClosed(t *testing.T) {
	if transaction.PlanAPIVersion != "nexa.dev/generation-plan/v2" || transaction.ResultAPIVersion != "nexa.dev/generation-result/v1" {
		t.Fatalf("API versions = %q, %q", transaction.PlanAPIVersion, transaction.ResultAPIVersion)
	}
	want := []transaction.ChangeKind{transaction.ChangeCreate, transaction.ChangeUpdate, transaction.ChangeDelete, transaction.ChangeCreateManual}
	if got := []transaction.ChangeKind{"create", "update", "delete", "create-manual"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("change kinds = %#v, want %#v", want, got)
	}
	if transaction.ControlSourceCompatibilityLock != "compatibility-lock" {
		t.Fatalf("control role = %q", transaction.ControlSourceCompatibilityLock)
	}
}

func requireTransactionError(t *testing.T, err error, code, stage, reason, pointer string) *transaction.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	var transactionError *transaction.Error
	if !errors.As(err, &transactionError) {
		t.Fatalf("error type = %T, want *transaction.Error", err)
	}
	if transactionError.Code() != code || transactionError.Stage() != stage || transactionError.Reason() != reason || transactionError.Pointer() != pointer {
		t.Fatalf("error projection = %q/%q/%q/%q", transactionError.Code(), transactionError.Stage(), transactionError.Reason(), transactionError.Pointer())
	}
	return transactionError
}

func mustRepositoryRef(t *testing.T, path, fragment string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatalf("RepositoryRef(%q, %q): %v", path, fragment, err)
	}
	return ref
}

func sourceStrings(values []provenance.SourceRef) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}
