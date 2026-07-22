package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

type pongResult struct {
	Pong bool `json:"pong"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	privatePlugin, err := newPrivatePlugin()
	if err != nil {
		return 70
	}
	composed, err := host.New(host.Options{Version: "v0.0.0-test"}, privatePlugin)
	if err != nil {
		return 70
	}
	return composed.Execute(context.Background(), args, os.Stdout, os.Stderr)
}

func newPrivatePlugin() (plugin.Plugin, error) {
	return plugin.NewStatic(plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              "private-example",
			Version:         "v0.1.0",
			ContractVersion: plugin.ContractVersion,
			Provides: []plugin.Capability{
				{ID: "private.ping", Version: "v1.0.0"},
			},
		},
		Commands: []plugin.CommandSpec{
			{
				Path:         []string{"private", "ping"},
				Summary:      "return the private plugin readiness state",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				SideEffect:   plugin.SideEffectNone,
				Run: func(context.Context, plugin.Invocation) (any, error) {
					return pongResult{Pong: true}, nil
				},
			},
		},
	})
}
