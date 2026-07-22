package quality

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func TestProviderPublishesFrozenProfiles(t *testing.T) {
	provider, err := New()
	if err != nil {
		var projected *sourceplugin.Error
		if errors.As(err, &projected) {
			t.Fatalf("provider: code=%s reason=%s pointer=%s", projected.Code(), projected.Reason(), projected.Pointer())
		}
		t.Fatal(err)
	}
	identity := provider.Manifest().Identity()
	if identity.ProviderID() != "quality-runtime" || identity.Version() != "v0.1.0" {
		t.Fatalf("identity = %s@%s", identity.ProviderID(), identity.Version())
	}
	snapshot, err := sourceplugin.SnapshotProvider(provider)
	if err != nil || snapshot.Manifest().Digest() != provider.Manifest().Digest() || snapshot.Tree().Digest() != provider.Tree().Digest() {
		t.Fatalf("provider snapshot = %#v, %v", snapshot, err)
	}

	backend, err := provider.Manifest().ResolveProfile("backend")
	if err != nil || !reflect.DeepEqual(backend.ProfileIDs(), []string{"backend"}) {
		t.Fatalf("backend closure = %#v, %v", backend.ProfileIDs(), err)
	}
	frontend, err := provider.Manifest().ResolveProfile("frontend")
	if err != nil || !reflect.DeepEqual(frontend.ProfileIDs(), []string{"frontend"}) {
		t.Fatalf("frontend closure = %#v, %v", frontend.ProfileIDs(), err)
	}
	full, err := provider.Manifest().ResolveProfile("full")
	if err != nil || !reflect.DeepEqual(full.ProfileIDs(), []string{"backend", "frontend", "full"}) {
		t.Fatalf("full closure = %#v, %v", full.ProfileIDs(), err)
	}

	first := provider.Tree().Files()
	first[0] = first[len(first)-1]
	if reflect.DeepEqual(first, provider.Tree().Files()) {
		t.Fatal("provider tree aliases caller mutation")
	}
}

func TestProviderManualContractsAreReadOnly(t *testing.T) {
	provider, err := New()
	if err != nil {
		t.Fatal(err)
	}
	root := materializeProfile(t, provider, "backend")

	apiDocument, err := httpapi.Load(context.Background(), httpapi.LoadOptions{
		RepositoryRoot: root,
		EntryFile:      "backend/quality/desc/quality.api",
	})
	if err != nil {
		t.Fatal(err)
	}
	operations := apiDocument.Operations()
	if len(operations) != 2 {
		t.Fatalf("API operations = %d", len(operations))
	}
	for _, operation := range operations {
		if operation.Method() != api.MethodGET {
			t.Fatalf("operation %q method = %s", operation.ID(), operation.Method())
		}
	}

	protocolDocument, err := genprotocol.Compile(context.Background(), genprotocol.CompileOptions{
		ServiceID:  "quality",
		EntryFiles: []string{"backend/quality/desc/quality.proto"},
		Resolver:   directoryResolver{root: root},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, ok := protocolDocument.Service("quality.v1.QualityService")
	if !ok || len(service.Methods()) != 2 {
		t.Fatalf("QualityService methods = %#v", service.Methods())
	}
}

func TestFrontendProfileIsIndependentStructuredReadOnlySource(t *testing.T) {
	provider, err := New()
	if err != nil {
		t.Fatal(err)
	}
	closure, err := provider.Manifest().ResolveProfile("frontend")
	if err != nil {
		t.Fatal(err)
	}
	if len(closure.BundleRequirements()) != 0 || len(closure.Validations()) != 0 {
		t.Fatalf("frontend dependencies = bundles:%d validations:%d", len(closure.BundleRequirements()), len(closure.Validations()))
	}

	type field struct {
		ID       string `json:"id"`
		LabelKey string `json:"labelKey"`
		Items    *struct {
			Fields []field `json:"fields"`
		} `json:"items"`
	}
	objectFound, pageFound := false, false
	locales := map[string]map[string]string{}
	labelKeys := map[string]struct{}{}
	for _, file := range closure.Files() {
		entry, ok := provider.Tree().Lookup(file.Path())
		if !ok {
			t.Fatalf("tree file %q missing", file.Path())
		}
		var header struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
		}
		if err := json.Unmarshal(entry.Bytes(), &header); err != nil {
			t.Fatalf("structured frontend asset: %v", err)
		}
		switch header.Kind {
		case "FrontendObject":
			if header.APIVersion != "nexa.dev/frontend-object/v1" {
				t.Fatalf("object apiVersion = %q", header.APIVersion)
			}
			var object struct {
				ID       string  `json:"id"`
				ReadOnly bool    `json:"readOnly"`
				Fields   []field `json:"fields"`
			}
			if err := json.Unmarshal(entry.Bytes(), &object); err != nil {
				t.Fatal(err)
			}
			if object.ID != "quality-read-model" || !object.ReadOnly {
				t.Fatalf("object = %#v", object)
			}
			var requirementFields []string
			for _, candidate := range object.Fields {
				labelKeys[candidate.LabelKey] = struct{}{}
				if candidate.ID == "requirements" && candidate.Items != nil {
					for _, nested := range candidate.Items.Fields {
						requirementFields = append(requirementFields, nested.ID)
						labelKeys[nested.LabelKey] = struct{}{}
					}
				}
			}
			sort.Strings(requirementFields)
			want := []string{"evidenceRefs", "freezeRefs", "freezeStatus", "gapCodes", "requirement", "status", "testRefs", "title"}
			if !reflect.DeepEqual(requirementFields, want) {
				t.Fatalf("requirement fields = %#v", requirementFields)
			}
			objectFound = true
		case "FrontendLocale":
			if header.APIVersion != "nexa.dev/frontend-locale/v1" {
				t.Fatalf("locale apiVersion = %q", header.APIVersion)
			}
			var locale struct {
				Locale   string            `json:"locale"`
				Messages map[string]string `json:"messages"`
			}
			if err := json.Unmarshal(entry.Bytes(), &locale); err != nil {
				t.Fatal(err)
			}
			locales[locale.Locale] = locale.Messages
		case "FrontendPage":
			if header.APIVersion != "nexa.dev/frontend-page/v1" {
				t.Fatalf("page apiVersion = %q", header.APIVersion)
			}
			var page struct {
				Mode              string `json:"mode"`
				ObjectID          string `json:"objectId"`
				SnapshotOperation string `json:"snapshotOperationId"`
			}
			if err := json.Unmarshal(entry.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Mode != "read-only" || page.ObjectID != "quality-read-model" || page.SnapshotOperation != "quality.snapshot.get" {
				t.Fatalf("page = %#v", page)
			}
			pageFound = true
		default:
			t.Fatalf("unsupported frontend kind %q", header.Kind)
		}
	}
	if !objectFound || !pageFound || len(locales["en-US"]) == 0 || len(locales["zh-CN"]) == 0 {
		t.Fatalf("frontend assets = object:%v page:%v locales:%v", objectFound, pageFound, locales)
	}
	for label := range labelKeys {
		if label == "" || locales["en-US"][label] == "" || locales["zh-CN"][label] == "" {
			t.Fatalf("locale label %q is unresolved", label)
		}
	}
}

func materializeProfile(t *testing.T, provider sourceplugin.Provider, profile string) string {
	t.Helper()
	closure, err := provider.Manifest().ResolveProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, file := range closure.Files() {
		entry, ok := provider.Tree().Lookup(file.Path())
		if !ok {
			t.Fatalf("tree file %q missing", file.Path())
		}
		name := filepath.Join(root, filepath.FromSlash(file.Path()))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, entry.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type directoryResolver struct{ root string }

func (r directoryResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(r.root, filepath.FromSlash(path)))
}
