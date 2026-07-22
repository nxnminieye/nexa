package servicecatalog_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/project/servicecatalog"
)

func TestLoadMissingCatalog(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	_, err = servicecatalog.Load(root, "backend/services.yaml")
	catalogError := requireCatalogError(t, err, "fact_source_missing", "")
	if catalogError.Source() != "backend/services.yaml" {
		t.Fatalf("Source() = %q, want backend/services.yaml", catalogError.Source())
	}
	if catalogError.Error() != "backend/services.yaml: fact source is missing" {
		t.Fatalf("Error() = %q", catalogError.Error())
	}
	if !errors.Is(catalogError, fs.ErrNotExist) {
		t.Fatalf("Unwrap() does not preserve fs.ErrNotExist: %v", catalogError.Unwrap())
	}
	if catalogError.Unwrap() != fs.ErrNotExist {
		t.Fatalf("Unwrap() = %T %v, want fs.ErrNotExist", catalogError.Unwrap(), catalogError.Unwrap())
	}
}

func TestLoadRejectsOutsideRootSymlink(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(outside, []byte(validCatalogYAML), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "services.yaml")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	_, err = servicecatalog.Load(root, "services.yaml")
	catalogError := requireCatalogError(t, err, "fact_source_read_failed", "")
	if catalogError.Source() != "services.yaml" {
		t.Fatalf("Source() = %q, want services.yaml", catalogError.Source())
	}
	if catalogError.Error() != "services.yaml: fact source could not be read" {
		t.Fatalf("Error() = %q", catalogError.Error())
	}
	if strings.Contains(catalogError.Error(), outside) {
		t.Fatalf("Error() leaks outside path: %q", catalogError.Error())
	}
	assertStableCause(t, catalogError, "fact source read failed")
	if strings.Contains(catalogError.Unwrap().Error(), outside) {
		t.Fatalf("Unwrap() leaks outside path: %q", catalogError.Unwrap().Error())
	}
}

func TestLoadRejectsNilRootAndInvalidSource(t *testing.T) {
	tests := []struct {
		name   string
		root   func(*testing.T) *os.Root
		source string
	}{
		{name: "nil root", source: "services.yaml"},
		{name: "empty source", root: openTemporaryRoot, source: ""},
		{name: "dot source", root: openTemporaryRoot, source: "."},
		{name: "parent source", root: openTemporaryRoot, source: "../services.yaml"},
		{name: "absolute source", root: openTemporaryRoot, source: "/services.yaml"},
		{name: "backslash source", root: openTemporaryRoot, source: `backend\services.yaml`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var root *os.Root
			if test.root != nil {
				root = test.root(t)
			}
			_, err := servicecatalog.Load(root, test.source)
			catalogError := requireCatalogError(t, err, "fact_source_read_failed", "")
			wantMessage := "fact source could not be read"
			if test.source != "" {
				wantMessage = test.source + ": " + wantMessage
			}
			if catalogError.Error() != wantMessage {
				t.Fatalf("Error() = %q", catalogError.Error())
			}
		})
	}
}

func TestLoadCatalogConcurrently(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "services.yaml"), []byte(validCatalogYAML), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	wantCatalog, err := servicecatalog.Load(root, "services.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := catalogProjection(wantCatalog)

	const workers = 100
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			catalog, loadErr := servicecatalog.Load(root, "services.yaml")
			if loadErr != nil {
				errors <- loadErr
				return
			}
			if got := catalogProjection(catalog); !reflect.DeepEqual(got, want) {
				errors <- &projectionMismatch{got: got, want: want}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func openTemporaryRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

type projectionMismatch struct {
	got  projectedCatalog
	want projectedCatalog
}

func (e *projectionMismatch) Error() string {
	return "loaded catalog projection differs"
}
