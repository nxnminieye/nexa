package sdkpythonassets

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestDefaultTemporaryRepositoryIsAccepted(t *testing.T) {
	repo := newAssetRepo(t)
	if _, err := NewOwner(nil).Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedRootDoesNotEscapeAfterRootPathSwap(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	moved := filepath.Join(base, "moved")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "go.mod"), []byte("module github.com/nxnminieye/nexa\n"))
	root, err := openRepoRoot(repo, "unchanged")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, repo); err != nil {
		t.Fatal(err)
	}
	if err := ensureManagedDirectories(root); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplace(root, bootstrapRelativePath, []byte("pinned")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "sdk")); !os.IsNotExist(err) {
		t.Fatalf("escaped into outside root: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(moved, filepath.FromSlash(bootstrapRelativePath))); err != nil || string(got) != "pinned" {
		t.Fatalf("pinned data=%q err=%v", got, err)
	}
}

func TestWriteRejectsSymlinkParentAndFinal(t *testing.T) {
	t.Run("parent", func(t *testing.T) {
		repo := newAssetRepo(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(repo, "sdk")); err != nil {
			t.Fatal(err)
		}
		if _, err := NewOwner(nil).Write(context.Background(), WriteRequest{RepoRoot: repo}); err == nil {
			t.Fatal("symlink parent accepted")
		}
		entries, _ := os.ReadDir(outside)
		if len(entries) != 0 {
			t.Fatalf("outside modified: %#v", entries)
		}
	})
	t.Run("final", func(t *testing.T) {
		repo := newAssetRepo(t)
		if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside.py")
		mustWrite(t, outside, []byte("keep"))
		if err := os.Symlink(outside, filepath.Join(repo, filepath.FromSlash(bootstrapRelativePath))); err != nil {
			t.Fatal(err)
		}
		if _, err := NewOwner(nil).Write(context.Background(), WriteRequest{RepoRoot: repo}); err == nil {
			t.Fatal("symlink final accepted")
		}
		if got, _ := os.ReadFile(outside); string(got) != "keep" {
			t.Fatalf("outside=%q", got)
		}
	})
}

func TestExclusiveCreateShortWriteAndStrictUmask(t *testing.T) {
	repo := newAssetRepo(t)
	root, err := openRepoRoot(repo, "unchanged")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := createExclusive(root, "collision", 0o644)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := createExclusive(root, "collision", 0o644); !os.IsExist(err) {
		t.Fatalf("collision err=%v", err)
	}
	if err := writeFull(zeroWriter{}, []byte("data")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write err=%v", err)
	}
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	if _, err := NewOwner(nil).Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"sdk", "sdk/python", packageRelativeDir, generatedRelativeDir, objectsRelativeDir} {
		info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("dir %s mode=%v err=%v", rel, info.Mode().Perm(), err)
		}
	}
	for _, rel := range []string{bootstrapRelativePath, indexRelativePath} {
		info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("file %s mode=%v err=%v", rel, info.Mode().Perm(), err)
		}
	}
}

func TestExistingAssetModesAreStrict(t *testing.T) {
	t.Run("managed-directory", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(nil)
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(repo, "sdk"), 0o777); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Check(context.Background(), CheckRequest{RepoRoot: repo}); err == nil {
			t.Fatal("check accepted directory mode drift")
		}
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err == nil {
			t.Fatal("write accepted directory mode drift")
		}
	})
	t.Run("deepest-managed-directory", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(testWheelBuilder{})
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(repo, filepath.FromSlash(objectsRelativeDir)), 0o777); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Check(context.Background(), CheckRequest{RepoRoot: repo}); ErrorReason(err) != ReasonRepoRootInvalid {
			t.Fatalf("check deepest managed directory err=%v reason=%q", err, ErrorReason(err))
		}
		if _, err := owner.Build(context.Background(), newBuildRequest(t, repo)); ErrorReason(err) != ReasonRepoRootInvalid {
			t.Fatalf("build deepest managed directory err=%v reason=%q", err, ErrorReason(err))
		}
	})
	t.Run("immutable-object", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(nil)
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		role := roleByID(t, owner.bundle, "runtime-corpus")
		path := filepath.Join(repo, filepath.FromSlash(generatedRelativeDir+"/"+role.Path))
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Check(context.Background(), CheckRequest{RepoRoot: repo}); err == nil {
			t.Fatal("check accepted object mode drift")
		}
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err == nil {
			t.Fatal("write reused object with mode drift")
		}
	})
	t.Run("replaceable-index", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(nil)
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Check(context.Background(), CheckRequest{RepoRoot: repo}); err == nil {
			t.Fatal("check accepted index mode drift")
		}
		result, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed {
			t.Fatal("mode repair not reported as change")
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("index mode=%v", info.Mode().Perm())
		}
	})
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestAssetBundleIsDeterministicAndDefensive(t *testing.T) {
	first, err := NewAssetBundle()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewAssetBundle()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.IndexBytes(), second.IndexBytes()) || !bytes.Equal(first.BootstrapBytes(), second.BootstrapBytes()) {
		t.Fatal("asset bundle is not deterministic")
	}
	roles := first.Roles()
	if len(roles) != len(closedRoleIDs) {
		t.Fatalf("roles=%d", len(roles))
	}
	roles[0].ID = "changed"
	if first.Roles()[0].ID == "changed" {
		t.Fatal("roles alias owner state")
	}
	data := first.Object(closedRoleIDs[0])
	data[0] ^= 0xff
	if bytes.Equal(data, first.Object(closedRoleIDs[0])) {
		t.Fatal("object aliases owner state")
	}
}

func TestWriteCheckAndConcurrentSameBundleConverge(t *testing.T) {
	repo := newAssetRepo(t)
	owner := NewOwner(nil)
	const writers = 8
	results := make(chan WriteResult, writers)
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo})
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := digestBytes(owner.bundle.IndexBytes())
	for result := range results {
		if result.IndexDigest != want {
			t.Fatalf("index=%q want=%q", result.IndexDigest, want)
		}
	}
	checked, err := owner.Check(context.Background(), CheckRequest{RepoRoot: repo, Mode: SourceTreeMode})
	if err != nil {
		t.Fatal(err)
	}
	if checked.IndexDigest != want || checked.ObjectCount != len(closedRoleIDs) {
		t.Fatalf("check=%#v", checked)
	}
	repeated, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo})
	if err != nil || repeated.Changed || len(repeated.ObjectsWritten) != 0 {
		t.Fatalf("repeat=%#v err=%v", repeated, err)
	}
	assertNoTransactionArtifacts(t, repo)
}

func TestWriteKeepsUserFilesAndUnreferencedObjects(t *testing.T) {
	repo := newAssetRepo(t)
	generated := filepath.Join(repo, filepath.FromSlash(generatedRelativeDir))
	if err := os.MkdirAll(filepath.Join(generated, "objects", "sha256"), 0o755); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(generated, "user-note.txt")
	extraObject := filepath.Join(generated, "objects", "sha256", strings.Repeat("f", 64)+".json")
	if err := os.WriteFile(userPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraObject, []byte("unreferenced"), 0o644); err != nil {
		t.Fatal(err)
	}
	owner := NewOwner(nil)
	if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(userPath); string(got) != "keep" {
		t.Fatalf("user file=%q", got)
	}
	if got, _ := os.ReadFile(extraObject); string(got) != "unreferenced" {
		t.Fatalf("extra object=%q", got)
	}
	if _, err := owner.Check(context.Background(), CheckRequest{RepoRoot: repo}); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledRewriteLeavesCurrentIndexUntouched(t *testing.T) {
	repo := newAssetRepo(t)
	owner := NewOwner(nil)
	first, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(indexRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := owner.Write(ctx, WriteRequest{RepoRoot: repo}); ErrorReason(err) != ReasonOperationCanceled {
		t.Fatalf("reason=%q err=%v", ErrorReason(err), err)
	}
	after, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(indexRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || digestBytes(after) != first.IndexDigest {
		t.Fatal("current index changed after canceled write")
	}
}

func TestCheckRejectsIndexObjectAndSchemaDrift(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		mutate     func(string, AssetBundle)
	}{
		{name: "index", mutate: func(repo string, _ AssetBundle) {
			mustWrite(t, filepath.Join(repo, filepath.FromSlash(indexRelativePath)), []byte("{}"))
		}},
		{name: "object", role: "runtime-corpus", mutate: func(repo string, b AssetBundle) {
			role := roleByID(t, b, "runtime-corpus")
			mustWrite(t, filepath.Join(repo, filepath.FromSlash(generatedRelativeDir+"/"+role.Path)), []byte("{}"))
		}},
		{name: "schema", role: "bundle-index-schema", mutate: func(repo string, b AssetBundle) {
			role := roleByID(t, b, "bundle-index-schema")
			mustWrite(t, filepath.Join(repo, filepath.FromSlash(generatedRelativeDir+"/"+role.Path)), []byte("{}"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newAssetRepo(t)
			owner := NewOwner(nil)
			if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
				t.Fatal(err)
			}
			tc.mutate(repo, owner.bundle)
			if _, err := owner.Check(context.Background(), CheckRequest{RepoRoot: repo}); err == nil {
				t.Fatal("drift accepted")
			}
		})
	}
}

func TestPythonLoaderValidatesBoundsPathsDigestsAndSchema(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	newWritten := func(t *testing.T) (string, *Owner) {
		t.Helper()
		repo := newAssetRepo(t)
		owner := NewOwner(nil)
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		return repo, owner
	}
	t.Run("valid", func(t *testing.T) {
		repo, _ := newWritten(t)
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "")
	})
	t.Run("digest", func(t *testing.T) {
		repo, owner := newWritten(t)
		role := roleByID(t, owner.bundle, "runtime-corpus")
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(generatedRelativeDir+"/"+role.Path)), []byte("{}"))
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_role_drift")
	})
	t.Run("bounds", func(t *testing.T) {
		repo, _ := newWritten(t)
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(indexRelativePath)), bytes.Repeat([]byte("x"), resourceRawBytes+1))
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_index_drift")
	})
	t.Run("path", func(t *testing.T) {
		repo, _ := newWritten(t)
		indexPath := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
		var doc map[string]any
		data, _ := os.ReadFile(indexPath)
		if json.Unmarshal(data, &doc) != nil {
			t.Fatal("index")
		}
		roles := doc["roles"].(map[string]any)
		roles["runtime-corpus"].(map[string]any)["path"] = "../runtime.json"
		encoded, _ := json.Marshal(doc)
		mustWrite(t, indexPath, encoded)
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_role_drift")
	})
	t.Run("schema", func(t *testing.T) {
		repo, _ := newWritten(t)
		indexPath := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
		var doc map[string]any
		data, _ := os.ReadFile(indexPath)
		if json.Unmarshal(data, &doc) != nil {
			t.Fatal("index")
		}
		invalid := []byte(`{"additionalProperties":false,"properties":{"roles":{"required":[],"type":"object"}},"type":"object"}`)
		digest := digestBytes(invalid)
		hex := strings.TrimPrefix(digest, "sha256:")
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(objectsRelativeDir+"/"+hex+".json")), invalid)
		row := doc["roles"].(map[string]any)["bundle-index-schema"].(map[string]any)
		row["digest"] = digest
		row["path"] = "objects/sha256/" + hex + ".json"
		encoded, _ := json.Marshal(doc)
		mustWrite(t, indexPath, encoded)
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_index_schema_drift")
	})
	t.Run("schema-array", func(t *testing.T) {
		repo, _ := newWritten(t)
		indexPath := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
		var doc map[string]any
		data, _ := os.ReadFile(indexPath)
		if json.Unmarshal(data, &doc) != nil {
			t.Fatal("index")
		}
		invalid := []byte(`[]`)
		digest := digestBytes(invalid)
		hex := strings.TrimPrefix(digest, "sha256:")
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(objectsRelativeDir+"/"+hex+".json")), invalid)
		row := doc["roles"].(map[string]any)["bundle-index-schema"].(map[string]any)
		row["digest"] = digest
		row["path"] = "objects/sha256/" + hex + ".json"
		encoded, _ := json.Marshal(doc)
		mustWrite(t, indexPath, encoded)
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_index_schema_drift")
	})
	t.Run("extra-top-level", func(t *testing.T) {
		repo, _ := newWritten(t)
		indexPath := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
		var doc map[string]any
		data, _ := os.ReadFile(indexPath)
		_ = json.Unmarshal(data, &doc)
		doc["unexpected"] = true
		encoded, _ := json.Marshal(doc)
		mustWrite(t, indexPath, encoded)
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_index_drift")
	})
	t.Run("missing-top-level", func(t *testing.T) {
		repo, _ := newWritten(t)
		indexPath := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
		var doc map[string]any
		data, _ := os.ReadFile(indexPath)
		_ = json.Unmarshal(data, &doc)
		delete(doc, "apiVersion")
		encoded, _ := json.Marshal(doc)
		mustWrite(t, indexPath, encoded)
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_index_drift")
	})
	t.Run("wrong-role-type", func(t *testing.T) {
		repo, _ := newWritten(t)
		indexPath := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
		var doc map[string]any
		data, _ := os.ReadFile(indexPath)
		_ = json.Unmarshal(data, &doc)
		doc["roles"].(map[string]any)["runtime-corpus"] = "bad"
		encoded, _ := json.Marshal(doc)
		mustWrite(t, indexPath, encoded)
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_role_drift")
	})
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{{name: "coordinated-extra-role", mutate: func(index, schema map[string]any) {
		roles := index["roles"].(map[string]any)
		data := []byte(`{}`)
		digest := digestBytes(data)
		roles["coordinated-extra"] = map[string]any{"apiVersion": "nexa.dev/extra/v1", "mediaType": "application/json", "path": "objects/sha256/" + strings.TrimPrefix(digest, "sha256:") + ".json", "digest": digest, "schemaRole": "runtime-limits-schema"}
		rolesSchema := schema["properties"].(map[string]any)["roles"].(map[string]any)
		rolesSchema["required"] = append(rolesSchema["required"].([]any), "coordinated-extra")
		rolesSchema["properties"].(map[string]any)["coordinated-extra"] = map[string]any{"$ref": "#/$defs/role"}
	}}, {name: "coordinated-missing-role", mutate: func(index, schema map[string]any) {
		delete(index["roles"].(map[string]any), "runtime-limits")
		rolesSchema := schema["properties"].(map[string]any)["roles"].(map[string]any)
		required := rolesSchema["required"].([]any)
		next := required[:0]
		for _, id := range required {
			if id != "runtime-limits" {
				next = append(next, id)
			}
		}
		rolesSchema["required"] = next
		delete(rolesSchema["properties"].(map[string]any), "runtime-limits")
	}}} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := newWritten(t)
			rewriteIndexAndSchema(t, repo, tc.mutate)
			runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_role_set_drift")
		})
	}
	t.Run("generated-symlink", func(t *testing.T) {
		repo, _ := newWritten(t)
		generated := filepath.Join(repo, filepath.FromSlash(generatedRelativeDir))
		outside := filepath.Join(t.TempDir(), "generated")
		if err := os.Rename(generated, outside); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, generated); err != nil {
			t.Fatal(err)
		}
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_index_drift")
	})
	t.Run("objects-symlink", func(t *testing.T) {
		repo, _ := newWritten(t)
		objects := filepath.Join(repo, filepath.FromSlash(generatedRelativeDir+"/objects"))
		outside := filepath.Join(t.TempDir(), "objects")
		if err := os.Rename(objects, outside); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, objects); err != nil {
			t.Fatal(err)
		}
		runPythonLoader(t, python, filepath.Join(repo, filepath.FromSlash(packageRelativeDir)), "source-tree", "bundle_role_drift")
	})
}

func rewriteIndexAndSchema(t *testing.T, repo string, mutate func(map[string]any, map[string]any)) {
	t.Helper()
	indexPath := filepath.Join(repo, filepath.FromSlash(indexRelativePath))
	indexData, _ := os.ReadFile(indexPath)
	var index map[string]any
	if json.Unmarshal(indexData, &index) != nil {
		t.Fatal("index")
	}
	schemaRow := index["roles"].(map[string]any)["bundle-index-schema"].(map[string]any)
	schemaPath := filepath.Join(repo, filepath.FromSlash(generatedRelativeDir+"/"+schemaRow["path"].(string)))
	schemaData, _ := os.ReadFile(schemaPath)
	var schema map[string]any
	if json.Unmarshal(schemaData, &schema) != nil {
		t.Fatal("schema")
	}
	mutate(index, schema)
	nextSchema, _ := json.Marshal(schema)
	schemaDigest := digestBytes(nextSchema)
	schemaHex := strings.TrimPrefix(schemaDigest, "sha256:")
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(objectsRelativeDir+"/"+schemaHex+".json")), nextSchema)
	schemaRow["digest"] = schemaDigest
	schemaRow["path"] = "objects/sha256/" + schemaHex + ".json"
	nextIndex, _ := json.Marshal(index)
	mustWrite(t, indexPath, nextIndex)
}

func TestBuildRejectsResolvedAliasOverlapAndWheelModeDrift(t *testing.T) {
	t.Run("case-insensitive-repo-alias", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(testWheelBuilder{})
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(repo, "case-out")
		if err := os.Mkdir(out, 0o755); err != nil {
			t.Fatal(err)
		}
		alias := swapASCIIPathCase(out)
		actualInfo, actualErr := os.Stat(out)
		aliasInfo, aliasErr := os.Stat(alias)
		if actualErr != nil || aliasErr != nil || !os.SameFile(actualInfo, aliasInfo) {
			t.Skip("filesystem has no case-insensitive path alias")
		}
		request := newBuildRequest(t, repo)
		request.Out = alias
		if _, err := owner.Build(context.Background(), request); err == nil {
			t.Fatal("case-insensitive out alias inside repo accepted")
		}
	})
	t.Run("repo-alias", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(testWheelBuilder{})
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(repo, "aliased-out"), 0o755); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(t.TempDir(), "repo-alias")
		if err := os.Symlink(repo, alias); err != nil {
			t.Fatal(err)
		}
		request := newBuildRequest(t, repo)
		request.Out = filepath.Join(alias, "aliased-out")
		if _, err := owner.Build(context.Background(), request); err == nil {
			t.Fatal("resolved out inside repo accepted")
		}
	})
	t.Run("mutual-alias", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(testWheelBuilder{})
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		actual := t.TempDir()
		shared := filepath.Join(actual, "shared")
		if err := os.Mkdir(shared, 0o755); err != nil {
			t.Fatal(err)
		}
		aliases := t.TempDir()
		a := filepath.Join(aliases, "a")
		b := filepath.Join(aliases, "b")
		if err := os.Symlink(actual, a); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(actual, b); err != nil {
			t.Fatal(err)
		}
		request := newBuildRequest(t, repo)
		request.Wheelhouse = filepath.Join(a, "shared")
		request.WorkDir = filepath.Join(b, "shared")
		if _, err := owner.Build(context.Background(), request); err == nil {
			t.Fatal("mutually aliased roots accepted")
		}
	})
	t.Run("wheel-mode", func(t *testing.T) {
		repo := newAssetRepo(t)
		owner := NewOwner(modeWheelBuilder{mode: 0o755})
		if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Build(context.Background(), newBuildRequest(t, repo)); err == nil {
			t.Fatal("executable wheel accepted")
		}
	})
}

func swapASCIIPathCase(path string) string {
	var result strings.Builder
	result.Grow(len(path))
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z':
			result.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z':
			result.WriteRune(r - 'A' + 'a')
		default:
			result.WriteRune(r)
		}
	}
	return result.String()
}

func newBuildRequest(t *testing.T, repo string) BuildRequest {
	t.Helper()
	return BuildRequest{RepoRoot: repo, Python: executableForTest(t), MatrixTarget: "darwin-arm64", Wheelhouse: t.TempDir(), WorkDir: t.TempDir(), Out: t.TempDir()}
}

func TestBuildInspectsWheelMembersDigestsAndRecord(t *testing.T) {
	repo := newAssetRepo(t)
	out := t.TempDir()
	wheelhouse := t.TempDir()
	work := t.TempDir()
	python := executableForTest(t)
	owner := NewOwner(testWheelBuilder{})
	if _, err := owner.Write(context.Background(), WriteRequest{RepoRoot: repo}); err != nil {
		t.Fatal(err)
	}
	result, err := owner.Build(context.Background(), BuildRequest{RepoRoot: repo, Python: python, MatrixTarget: "darwin-arm64", Wheelhouse: wheelhouse, WorkDir: work, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if result.WheelDigest == "" || result.RecordDigest == "" || result.WheelSize <= 0 {
		t.Fatalf("build=%#v", result)
	}
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	if output, err := exec.Command(python3, "-m", "pip", "--version").CombinedOutput(); err != nil {
		t.Skipf("pip unavailable: %v: %s", err, output)
	}
	installed := t.TempDir()
	cmd := exec.Command(python3, "-m", "pip", "install", "--no-deps", "--disable-pip-version-check", "--target", installed, filepath.Join(out, result.WheelPath))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pip install: %v: %s", err, output)
	}
	runInstalledLoader(t, python3, installed)
}

type testWheelBuilder struct{}
type modeWheelBuilder struct{ mode os.FileMode }

func (b modeWheelBuilder) Build(ctx context.Context, request WheelBuildRequest) (WheelBuildOutput, error) {
	output, err := testWheelBuilder{}.Build(ctx, request)
	if err == nil {
		err = os.Chmod(filepath.Join(request.Out, output.WheelPath), b.mode)
	}
	return output, err
}

func (testWheelBuilder) Build(_ context.Context, request WheelBuildRequest) (WheelBuildOutput, error) {
	name := "nexa-0.1.0-py3-none-any.whl"
	file, err := os.Create(filepath.Join(request.Out, name))
	if err != nil {
		return WheelBuildOutput{}, err
	}
	zw := zip.NewWriter(file)
	entries := map[string][]byte{"nexa/__init__.py": []byte("from ._bootstrap import open_asset_context\n"), "nexa/_bootstrap.py": request.Snapshot.BootstrapBytes(), "nexa/_generated/bundle-index.json": request.Snapshot.IndexBytes(), "nexa-0.1.0.dist-info/METADATA": []byte("Metadata-Version: 2.1\nName: nexa\nVersion: 0.1.0\n"), "nexa-0.1.0.dist-info/WHEEL": []byte("Wheel-Version: 1.0\nGenerator: nexa-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n")}
	for _, role := range request.Snapshot.Roles() {
		entries["nexa/_generated/"+role.Path] = request.Snapshot.Object(role.ID)
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var record bytes.Buffer
	csvw := csv.NewWriter(&record)
	for _, path := range paths {
		data := entries[path]
		sum := sha256.Sum256(data)
		hash := base64.RawURLEncoding.EncodeToString(sum[:])
		_ = csvw.Write([]string{path, "sha256=" + hash, fmt.Sprint(len(data))})
	}
	_ = csvw.Write([]string{"nexa-0.1.0.dist-info/RECORD", "", ""})
	csvw.Flush()
	entries["nexa-0.1.0.dist-info/RECORD"] = record.Bytes()
	paths = append(paths, "nexa-0.1.0.dist-info/RECORD")
	for _, path := range paths {
		w, e := zw.Create(path)
		if e != nil {
			return WheelBuildOutput{}, e
		}
		if _, e = w.Write(entries[path]); e != nil {
			return WheelBuildOutput{}, e
		}
	}
	if err := zw.Close(); err != nil {
		return WheelBuildOutput{}, err
	}
	if err := file.Close(); err != nil {
		return WheelBuildOutput{}, err
	}
	return WheelBuildOutput{WheelPath: name, PythonVersion: "3.12.1"}, nil
}

func newAssetRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), []byte("module github.com/nxnminieye/nexa\n\ngo 1.24.0\n"))
	return root
}
func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
func roleByID(t *testing.T, b AssetBundle, id string) Role {
	t.Helper()
	for _, r := range b.Roles() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("role %s missing", id)
	return Role{}
}
func assertNoTransactionArtifacts(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && (strings.Contains(d.Name(), ".journal") || strings.Contains(d.Name(), ".lock") || strings.Contains(d.Name(), ".txn-")) {
			t.Errorf("transaction artifact %s", path)
		}
		return nil
	})
}
func runPythonLoader(t *testing.T, python, packageRoot, mode, wantReason string) {
	t.Helper()
	script := `import importlib.util,pathlib,sys
p=pathlib.Path(sys.argv[1]); s=importlib.util.spec_from_file_location("nexa_bootstrap",p/"_bootstrap.py"); m=importlib.util.module_from_spec(s); s.loader.exec_module(m)
try:
  with m.open_asset_context(sys.argv[2], str(p)) as assets: assert assets.read_role("runtime-corpus")
  print("ok")
except m.AssetError as e:
  print(e.reason); raise`
	cmd := exec.Command(python, "-c", script, packageRoot, mode)
	output, err := cmd.CombinedOutput()
	if wantReason == "" {
		if err != nil || string(output) != "ok\n" {
			t.Fatalf("loader: %v: %s", err, output)
		}
		return
	}
	if err == nil || !bytes.Contains(output, []byte(wantReason)) {
		t.Fatalf("loader reason=%s: %v: %s", wantReason, err, output)
	}
}
func runInstalledLoader(t *testing.T, python, installed string) {
	t.Helper()
	script := `import nexa,pathlib
p=pathlib.Path(nexa.__file__).parent
with nexa.open_asset_context("installed-wheel", str(p)) as assets: assert assets.read_role("runtime-corpus")
print("ok")`
	cmd := exec.Command(python, "-c", script)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+installed)
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "ok\n" {
		t.Fatalf("installed import: %v: %s", err, output)
	}
}
func executableForTest(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
