package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
)

const deterministicOperationID = "op_0123456789abcdef0123456789abcdef"

type deterministicOperationIDGenerator struct{}

func (deterministicOperationIDGenerator) NewOperationID() (string, error) {
	return deterministicOperationID, nil
}

func TestPrivatePluginComposition(t *testing.T) {
	privatePlugin, err := newPrivatePlugin()
	if err != nil {
		t.Fatalf("construct private plugin: %v", err)
	}
	composed, err := host.New(
		host.Options{
			Version:      "v0.0.0-test",
			OperationIDs: deterministicOperationIDGenerator{},
		},
		privatePlugin,
	)
	if err != nil {
		t.Fatalf("compose private host: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := composed.Execute(
		context.Background(),
		[]string{"private", "ping", "--json"},
		&stdout,
		&stderr,
	)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}

	var envelope protocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if !envelope.OK || envelope.OperationID != deterministicOperationID {
		t.Fatalf("unexpected success envelope: %#v", envelope)
	}
	var result struct {
		Pong bool `json:"pong"`
	}
	decodeResult(t, envelope.Result, &result)
	if !result.Pong {
		t.Fatalf("unexpected private plugin result: %#v", result)
	}

	inspection := composed.Inspect()
	commandFound := false
	for _, command := range inspection.Commands {
		if len(command.Path) == 2 && command.Path[0] == "private" && command.Path[1] == "ping" {
			commandFound = true
			if command.OwnerPluginID != "private-example" {
				t.Fatalf("unexpected command owner %q", command.OwnerPluginID)
			}
		}
	}
	if !commandFound {
		t.Fatal("private ping command missing from inspection")
	}

	capabilityFound := false
	for _, capability := range inspection.Capabilities {
		if capability.ID == "private.ping" {
			capabilityFound = true
			if capability.Version != "v1.0.0" || capability.ProviderPluginID != "private-example" {
				t.Fatalf("unexpected capability projection: %#v", capability)
			}
		}
	}
	if !capabilityFound {
		t.Fatal("private.ping capability missing from inspection")
	}
}

func decodeResult(t *testing.T, source any, target any) {
	t.Helper()

	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}
