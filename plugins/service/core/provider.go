package core

import "github.com/nxnminieye/nexa/sourceplugin"

func New() (sourceplugin.Provider, error) {
	manifestBytes, err := bundleAssets.ReadFile("bundle.json")
	if err != nil {
		return nil, err
	}
	manifest, err := sourceplugin.Parse("bundle.json", manifestBytes)
	if err != nil {
		return nil, err
	}
	tree, err := sourceplugin.LoadEmbeddedTree(manifest, bundleAssets, "_bundle", sourceplugin.DefaultTreeLimits())
	if err != nil {
		return nil, err
	}
	return sourceplugin.NewProvider(manifest, tree)
}
