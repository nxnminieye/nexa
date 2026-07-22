package core

import "embed"

//go:embed bundle.json _bundle
var bundleAssets embed.FS
