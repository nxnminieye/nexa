package job

import "github.com/nxnminieye/nexa/sourceplugin"

func New() (sourceplugin.Provider, error) {
	data, err := embedded.ReadFile("bundle.json")
	if err != nil {
		return nil, err
	}
	manifest, err := sourceplugin.Parse("bundle.json", data)
	if err != nil {
		return nil, err
	}
	tree, err := sourceplugin.LoadEmbeddedTree(manifest, embedded, "_bundle", sourceplugin.DefaultTreeLimits())
	if err != nil {
		return nil, err
	}
	return sourceplugin.NewProvider(manifest, tree)
}
