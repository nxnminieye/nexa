package main

import (
	"context"
	"embed"
	"os"

	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	sourceadapter "github.com/nxnminieye/nexa/plugins/nexactl/source"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
)

//go:embed provider/manifest.json provider/source/*
var privateSource embed.FS

type privateMergeDriver struct{}

func (privateMergeDriver) Merge(_ context.Context, input engine.TextMergeInput) (engine.TextMergeResult, error) {
	return engine.NewTextMergeResult(input.New, true), nil
}

func privateProvider() (sourceplugin.Provider, error) {
	data, err := privateSource.ReadFile("provider/manifest.json")
	if err != nil {
		return nil, err
	}
	manifest, err := sourceplugin.Parse("provider/manifest.json", data)
	if err != nil {
		return nil, err
	}
	files := manifest.Files()
	inputs := make([]sourceplugin.TreeInput, len(files))
	for index, file := range files {
		assetPath := file.Path()
		if assetPath == "go.mod" {
			assetPath = "go.mod.txt"
		}
		content, readErr := privateSource.ReadFile("provider/source/" + assetPath)
		if readErr != nil {
			return nil, readErr
		}
		inputs[index] = sourceplugin.TreeInput{Path: file.Path(), Content: content}
	}
	tree, err := sourceplugin.NewTree(manifest, inputs, sourceplugin.DefaultTreeLimits())
	if err != nil {
		return nil, err
	}
	return sourceplugin.NewProvider(manifest, tree)
}

func privateSourceAdapter(providers ...sourceplugin.Provider) (plugin.Plugin, error) {
	return sourceadapter.New(sourceadapter.Options{
		Version: "v0.1.0", TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: privateMergeDriver{},
	}, providers...)
}

func privateHost(options host.Options, providers ...sourceplugin.Provider) (*host.Host, error) {
	adapter, err := privateSourceAdapter(providers...)
	if err != nil {
		return nil, err
	}
	return host.New(options, adapter)
}

func main() {
	provider, err := privateProvider()
	if err != nil {
		os.Exit(70)
	}
	composed, err := privateHost(host.Options{Version: "v0.0.0-source-consumer"}, provider)
	if err != nil {
		os.Exit(70)
	}
	os.Exit(composed.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
