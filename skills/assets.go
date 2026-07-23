// Package skills provides the Nexa framework skills embedded in this module version.
package skills

import (
	"embed"
	"io/fs"
)

//go:embed all:nexa-*
var embedded embed.FS

// Files returns the immutable framework skill assets embedded in this module.
func Files() fs.FS {
	return embedded
}
