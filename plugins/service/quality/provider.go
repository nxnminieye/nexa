// Package quality publishes the neutral, read-only Quality runtime source bundle.
package quality

import "github.com/nxnminieye/nexa/sourceplugin"

// New constructs an immutable quality-runtime source provider.
func New() (sourceplugin.Provider, error) {
	document, err := bundleFiles.ReadFile("bundle.json")
	if err != nil {
		return nil, err
	}
	manifest, err := sourceplugin.Parse("bundle.json", document)
	if err != nil {
		return nil, err
	}
	tree, err := sourceplugin.LoadEmbeddedTree(
		manifest,
		bundleFiles,
		"_bundle",
		sourceplugin.DefaultTreeLimits(),
	)
	if err != nil {
		return nil, err
	}
	return sourceplugin.NewProvider(manifest, tree)
}
