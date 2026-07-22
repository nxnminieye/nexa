package crudproto_test

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/crudproto"
)

func TestProjectProtoDestination(t *testing.T) {
	destination, err := crudproto.ProjectProtoDestination("accounts", "api/accounts.proto")
	if err != nil {
		t.Fatal(err)
	}
	if destination.ServiceID() != "accounts" || destination.EntryPath() != "api/accounts.proto" {
		t.Fatalf("destination input = %q %q", destination.ServiceID(), destination.EntryPath())
	}
	if destination.ArtifactPath() != "api/accounts.crud.generated.proto" || destination.LockPath() != "api/accounts.crud-protocol.lock.json" {
		t.Fatalf("destination outputs = %q %q", destination.ArtifactPath(), destination.LockPath())
	}
	if destination.ManifestPath() != ".nexa/generation/crud-proto.accounts.manifest.json" {
		t.Fatalf("manifest path = %q", destination.ManifestPath())
	}
	if destination.ArtifactID() != "crud-proto.accounts" {
		t.Fatalf("artifact ID = %q", destination.ArtifactID())
	}
}

func TestProjectProtoDestinationRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name, serviceID, entryPath, reason, pointer string
	}{
		{"absolute", "accounts", "/api/accounts.proto", "proto_destination_invalid", "/entryPath"},
		{"traversal", "accounts", "api/../accounts.proto", "proto_destination_invalid", "/entryPath"},
		{"non proto", "accounts", "api/accounts.json", "proto_destination_invalid", "/entryPath"},
		{"already generated", "accounts", "api/accounts.crud.generated.proto", "proto_destination_invalid", "/entryPath"},
		{"invalid service", "Accounts", "api/accounts.proto", "service_id_invalid", "/serviceId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := crudproto.ProjectProtoDestination(test.serviceID, test.entryPath)
			typed, ok := err.(*crudproto.Error)
			if !ok || typed.Code() != "crud_host_invalid" || typed.Stage() != "project" || typed.Reason() != test.reason || typed.Pointer() != test.pointer {
				t.Fatalf("projection error = %#v", err)
			}
		})
	}
}
