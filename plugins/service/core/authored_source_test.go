package core_test

import (
	"embed"
	"io/fs"
	"testing"

	"github.com/nxnminieye/nexa/internal/bundletest"
)

//go:embed _bundle/backend/core/coreapp
var authoredCore embed.FS

func TestAuthoredCoreSource(t *testing.T) {
	t.Parallel()

	source, err := fs.Sub(authoredCore, "_bundle/backend/core")
	if err != nil {
		t.Fatal(err)
	}
	err = bundletest.RunWithOptions(t.Context(), bundletest.Module{
		Path:   "example.com/core-source",
		Source: source,
		Requirements: map[string]string{
			"golang.org/x/crypto": "v0.48.0",
		},
	}, bundletest.Options{Race: bundletest.RaceEnabled()})
	if err != nil {
		t.Fatal(err)
	}
}
