package lock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const nonEmptyLockPayload = `{"apiVersion":"nexa.dev/source-lock/v1","kind":"SourceLock","profileClosure":["base","default"],"profileId":"default","release":{"manifestDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","modulePath":"example.test/source","packagePath":"example.test/source/provider","providerId":"sample","treeDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","version":"v0.1.0"},"target":"services/sample","trackedFiles":[{"digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","mode":"0644","path":"go.mod","size":12}]}`
const emptyLockPayload = `{"apiVersion":"nexa.dev/source-lock/v1","kind":"SourceLock","profileClosure":["empty"],"profileId":"empty","release":{"manifestDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","modulePath":"example.test/source","packagePath":"example.test/source/provider","providerId":"sample","treeDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","version":"v0.1.0"},"target":"services/sample","trackedFiles":[]}`

func TestKeyGoldenAccessorsAndValidation(t *testing.T) {
	key, err := lock.NewKey("sample", "services/sample")
	if err != nil {
		t.Fatal(err)
	}
	if key.ProviderID() != "sample" || key.Target() != "services/sample" ||
		key.RepositoryPath() != ".nexa/source/locks/sample-c389587a38879d3002d290606e11954e94c3a554e5a1b0579573387262a37cfc.json" {
		t.Fatalf("key = %#v path=%q", key, key.RepositoryPath())
	}
	copyKey, err := lock.NewKey("sample", "services/sample")
	if err != nil || !key.Equal(copyKey) || (lock.Key{}).Equal(key) || key.Equal(lock.Key{}) {
		t.Fatalf("key equality = %v, err=%v", key.Equal(copyKey), err)
	}
	_, err = lock.NewKey("Bad", ".nexa/source")
	assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "key_provider_invalid", "/providerId", lock.StageKey)
	_, err = lock.NewKey("sample", ".nexa/source")
	assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "key_target_invalid", "/target", lock.StageKey)
}

func TestLockGoldenDocumentsSchemaDigestAndDefensiveSnapshot(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	for _, test := range []struct {
		name    string
		payload string
		digest  string
		files   int
	}{
		{"non-empty", nonEmptyLockPayload, "sha256:bdaaf850a99422e07df702869799a5205c6a83d8ed6cec001afd7d76d5c73195", 1},
		{"empty", emptyLockPayload, "sha256:06d398a5fbae32ecb3145b6b4d7880117fd335672420183d5fc1454f6e5a46c6", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := lock.Parse(key.RepositoryPath(), []byte(test.payload+"\n"), lock.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := snapshot.CanonicalJSON()
			if err != nil || string(canonical) != test.payload+"\n" || snapshot.Digest().String() != test.digest {
				t.Fatalf("canonical=%s digest=%s err=%v", canonical, snapshot.Digest().String(), err)
			}
			if snapshot.APIVersion() != lock.APIVersion || snapshot.Kind() != lock.Kind || !snapshot.Key().Equal(key) ||
				snapshot.Target() != key.Target() || snapshot.Source() != key.RepositoryPath() || len(snapshot.TrackedFiles()) != test.files {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			closure := snapshot.ProfileClosure()
			closure[0] = "changed"
			files := snapshot.TrackedFiles()
			if len(files) > 0 {
				files[0] = lock.BaselineFile{}
			}
			canonical[0] = 'x'
			if snapshot.ProfileClosure()[0] == "changed" || !bytes.Equal(mustCanonical(t, snapshot), []byte(test.payload+"\n")) {
				t.Fatal("snapshot accessor mutated internal state")
			}
		})
	}
	compiler := jsonschema.NewCompiler()
	var schemaDocument any
	if err := json.Unmarshal(lock.Schema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	if err := compiler.AddResource("lock.json", schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lockDocument any
	if err := json.Unmarshal([]byte(nonEmptyLockPayload), &lockDocument); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(lockDocument); err != nil {
		t.Fatal(err)
	}
}

func TestLockSafeIntegerParseSchemaAndCanonical(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	const maxSafe = int64(1<<53 - 1)
	validPayload := strings.Replace(nonEmptyLockPayload, `"size":12`, `"size":9007199254740991`, 1)
	trusted, err := jcs.Transform([]byte(validPayload))
	if err != nil || string(trusted) != validPayload {
		t.Fatalf("trusted JCS max-safe payload = %s, err=%v", trusted, err)
	}
	snapshot, err := lock.Parse(key.RepositoryPath(), []byte(validPayload+"\n"), lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := snapshot.CanonicalJSON()
	if err != nil || string(canonical) != validPayload+"\n" || snapshot.TrackedFiles()[0].Size() != maxSafe {
		t.Fatalf("max-safe snapshot size=%d canonical=%s err=%v", snapshot.TrackedFiles()[0].Size(), canonical, err)
	}
	if snapshot.Digest() != provenance.SHA256(trusted) {
		t.Fatalf("max-safe digest=%s want=%s", snapshot.Digest(), provenance.SHA256(trusted))
	}

	for _, value := range []string{"9007199254740992", "9007199254740993", "9223372036854775807"} {
		payload := strings.Replace(nonEmptyLockPayload, `"size":12`, `"size":`+value, 1)
		_, err := lock.Parse(key.RepositoryPath(), []byte(payload+"\n"), lock.DefaultLimits())
		assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "tracked_file_size_invalid", "/trackedFiles/0/size", lock.StageParse)
	}

	compiler := jsonschema.NewCompiler()
	var schemaDocument any
	if err := json.Unmarshal(lock.Schema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	if err := compiler.AddResource("safe-integer-lock.json", schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("safe-integer-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	decodeExact := func(payload string) any {
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.UseNumber()
		var document any
		if err := decoder.Decode(&document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	if err := schema.Validate(decodeExact(validPayload)); err != nil {
		t.Fatalf("schema rejected max-safe size: %v", err)
	}
	unsafePayload := strings.Replace(nonEmptyLockPayload, `"size":12`, `"size":9007199254740992`, 1)
	if err := schema.Validate(decodeExact(unsafePayload)); err == nil {
		t.Fatal("schema accepted size above the JCS safe-integer domain")
	}
}

func TestLockParseStrictCanonicalAndSourceIdentity(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	tests := []struct {
		name    string
		source  string
		data    string
		class   lock.ErrorClass
		reason  string
		pointer string
	}{
		{"unsafe source", "/secret/lock.json", nonEmptyLockPayload + "\n", lock.ErrLockInput, "source_location_invalid", ""},
		{"volume source", "C:/secret/lock.json", nonEmptyLockPayload + "\n", lock.ErrLockInput, "source_location_invalid", ""},
		{"unknown", key.RepositoryPath(), strings.Replace(nonEmptyLockPayload, `"kind":"SourceLock"`, `"kind":"SourceLock","extra":true`, 1) + "\n", lock.ErrLockInput, "document_unknown_field", ""},
		{"duplicate", key.RepositoryPath(), strings.Replace(nonEmptyLockPayload, `"kind":"SourceLock"`, `"kind":"SourceLock","kind":"SourceLock"`, 1) + "\n", lock.ErrLockInput, "document_duplicate_key", "/kind"},
		{"non-canonical", key.RepositoryPath(), nonEmptyLockPayload + " \n", lock.ErrLockInput, "document_not_canonical", ""},
		{"trailing", key.RepositoryPath(), nonEmptyLockPayload + "\n{}", lock.ErrLockInput, "document_trailing_input", ""},
		{"wrong source", "other/lock.json", nonEmptyLockPayload + "\n", lock.ErrLockConflict, "source_key_mismatch", "/source"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := lock.Parse(test.source, []byte(test.data), lock.DefaultLimits())
			assertLockError(t, err, test.class, codeForLockClass(test.class), test.reason, test.pointer, lock.StageParse)
		})
	}
}

func TestLockStrictSafePointerHostileKeys(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	hostileKeys := []struct {
		name string
		key  string
	}{
		{name: "newline credential", key: "credential/token\nsecret"},
		{name: "absolute path", key: "/etc/credential"},
		{name: "escaped slash", key: "a/b"},
		{name: "control", key: "\x01token"},
		{name: "format", key: "token\u202esecret"},
		{name: "document-limit-sized", key: strings.Repeat("x", 64<<10)},
	}
	quote := func(value string) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	for _, hostile := range hostileKeys {
		t.Run(hostile.name, func(t *testing.T) {
			q := quote(hostile.key)
			limits := lock.DefaultLimits()
			cases := []struct {
				name    string
				data    string
				reason  string
				pointer string
			}{
				{name: "duplicate root", data: "{" + q + ":1," + q + ":2}", reason: "document_duplicate_key", pointer: ""},
				{name: "duplicate release", data: "{\"release\":{" + q + ":1," + q + ":2}}", reason: "document_duplicate_key", pointer: "/release"},
				{name: "duplicate tracked", data: "{\"trackedFiles\":[{" + q + ":1," + q + ":2}]}", reason: "document_duplicate_key", pointer: "/trackedFiles/0"},
				{name: "unknown root", data: "{" + q + ":1}", reason: "document_unknown_field", pointer: ""},
				{name: "unknown release", data: "{\"release\":{" + q + ":1}}", reason: "document_unknown_field", pointer: "/release"},
				{name: "unknown tracked", data: "{\"trackedFiles\":[{" + q + ":1}]}", reason: "document_unknown_field", pointer: "/trackedFiles/0"},
				{name: "syntax root", data: "{" + q + ":[}", reason: "document_invalid", pointer: ""},
				{name: "syntax release", data: "{\"release\":{" + q + ":[}}", reason: "document_invalid", pointer: "/release"},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					limits.MaxDocumentBytes = int64(len(test.data) + 1)
					_, err := lock.Parse(key.RepositoryPath(), []byte(test.data), limits)
					pointer := test.pointer
					if test.reason == "document_unknown_field" {
						var candidate *lock.Error
						if errors.As(err, &candidate) && candidate.Pointer() == "" {
							pointer = ""
						}
					}
					projected := assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", test.reason, pointer, lock.StageParse)
					if projected.Source() != key.RepositoryPath() {
						t.Fatalf("strict location = %q:%d:%d", projected.Source(), projected.Line(), projected.Column())
					}
					for _, public := range []string{projected.Error(), projected.Class().Error(), projected.Code(), projected.Reason(), projected.Pointer(), projected.Source()} {
						if strings.Contains(public, hostile.key) {
							t.Fatalf("hostile key reached public error accessor: %q", public)
						}
					}
				})
			}
		})
	}

	for _, test := range []struct {
		name    string
		data    string
		reason  string
		pointer string
	}{
		{name: "known root duplicate", data: `{"kind":"SourceLock","kind":"SourceLock"}`, reason: "document_duplicate_key", pointer: "/kind"},
		{name: "known release duplicate", data: `{"release":{"providerId":"sample","providerId":"sample"}}`, reason: "document_duplicate_key", pointer: "/release/providerId"},
		{name: "known tracked duplicate", data: `{"trackedFiles":[{"path":"a","path":"a"}]}`, reason: "document_duplicate_key", pointer: "/trackedFiles/0/path"},
		{name: "known type", data: `{"release":"credential/token"}`, reason: "document_invalid", pointer: "/release"},
		{name: "trailing", data: `{} {"credential/token":1}`, reason: "document_trailing_input", pointer: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := lock.Parse(key.RepositoryPath(), []byte(test.data), lock.DefaultLimits())
			assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", test.reason, test.pointer, lock.StageParse)
		})
	}
}

func TestLockParseRejectsNonAdjacentFoldedPrefixCollision(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	var document map[string]any
	if err := json.Unmarshal([]byte(nonEmptyLockPayload), &document); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	document["trackedFiles"] = []any{
		map[string]any{"path": "A", "mode": "0644", "size": 1, "digest": digest},
		map[string]any{"path": "B", "mode": "0644", "size": 1, "digest": digest},
		map[string]any{"path": "a/x", "mode": "0644", "size": 1, "digest": digest},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lock.Parse(key.RepositoryPath(), append(canonical, '\n'), lock.DefaultLimits())
	assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "tracked_file_collision", "/trackedFiles/2/path", lock.StageParse)
}

func TestLockParseUnicodeFoldedEqualityChoosesGlobalCollisionPair(t *testing.T) {
	tests := []struct {
		name    string
		paths   []string
		pointer string
	}{
		{name: "simple fold groups", paths: []string{"S", "T", "t", "ſ"}, pointer: "/trackedFiles/3/path"},
		{name: "full fold expansion", paths: []string{"ss", "ß"}, pointer: "/trackedFiles/1/path"},
		{name: "equality before prefix", paths: []string{"S", "T", "s/x", "t", "ſ"}, pointer: "/trackedFiles/4/path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, data, limits := canonicalLockWithPaths(t, test.paths)
			_, err := lock.Parse(source, data, limits)
			assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "tracked_file_collision", test.pointer, lock.StageParse)
		})
	}
}

func TestLockParseTrackedFileResourceGrowthIsBounded(t *testing.T) {
	measure := func(count int) uint64 {
		t.Helper()
		source, data, limits := canonicalLockWithTrackedFiles(t, count)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		snapshot, err := lock.Parse(source, data, limits)
		runtime.ReadMemStats(&after)
		if err != nil || len(snapshot.TrackedFiles()) != count {
			t.Fatalf("Parse(%d) files = %d, err=%v", count, len(snapshot.TrackedFiles()), err)
		}
		return after.TotalAlloc - before.TotalAlloc
	}

	small := measure(512)
	large := measure(1024)
	const allocationSlack = 2 << 20
	if large > small*3+allocationSlack {
		t.Fatalf("2x tracked files allocated %d bytes after %d bytes; growth exceeds bounded parse contract", large, small)
	}
}

func TestLockParseSingleDeepPathUsesBoundedMemory(t *testing.T) {
	const pathBytes = (512 << 10) - 1
	deepPath := strings.Repeat("a/", pathBytes/2) + "z"
	if len(deepPath) != pathBytes {
		t.Fatalf("deep path bytes = %d, want %d", len(deepPath), pathBytes)
	}
	source, data, limits := canonicalLockWithPaths(t, []string{deepPath})

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	snapshot, err := lock.Parse(source, data, limits)
	runtime.ReadMemStats(&after)
	if err != nil || len(snapshot.TrackedFiles()) != 1 || snapshot.TrackedFiles()[0].Path() != deepPath {
		t.Fatalf("deep path Parse = %#v, err=%v", snapshot.TrackedFiles(), err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("deep path bytes=%d document bytes=%d allocated=%d", len(deepPath), len(data), allocated)
	const maxAllocation = 32 << 20
	if allocated > maxAllocation {
		t.Fatalf("deep path Parse allocated %d bytes, want at most %d", allocated, maxAllocation)
	}
	runtime.KeepAlive(snapshot)
}

func TestLockParseDeepPrefixCollisionKeepsCanonicalPairAndAuthoredLocation(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	deep := strings.Repeat("a/", 16<<10) + "leaf"
	digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	document := "{\n" +
		`  "apiVersion":"nexa.dev/source-lock/v1",` + "\n" +
		`  "kind":"SourceLock",` + "\n" +
		`  "profileClosure":["base","default"],` + "\n" +
		`  "profileId":"default",` + "\n" +
		`  "release":{"treeDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","providerId":"sample","version":"v0.1.0","packagePath":"example.test/source/provider","modulePath":"example.test/source","manifestDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},` + "\n" +
		`  "target":"services/sample",` + "\n" +
		`  "trackedFiles":[` + "\n" +
		`    {"digest":"` + digest + `","mode":"0644","path":` + strconv.Quote(deep) + `,"size":1},` + "\n" +
		`    {"digest":"` + digest + `","mode":"0644","path":` + strconv.Quote(deep+"-sibling") + `,"size":1},` + "\n" +
		`    {"digest":"` + digest + `","mode":"0644","path":"b/x","size":1},` + "\n" +
		`    {"digest":"` + digest + `","mode":"0644","path":` + strconv.Quote("A/"+strings.TrimPrefix(deep, "a/")+"/child") + `,"size":1},` + "\n" +
		`    {"digest":"` + digest + `","mode":"0644","path":"B","size":1}` + "\n" +
		`  ]` + "\n}"

	_, err := lock.Parse(key.RepositoryPath(), []byte(document), lock.DefaultLimits())
	projected := assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "tracked_file_collision", "/trackedFiles/2/path", lock.StageParse)
	if projected.Source() != key.RepositoryPath() || projected.Line() != 9 || projected.Column() <= 0 {
		t.Fatalf("deep authored diagnostics = source=%q line=%d column=%d", projected.Source(), projected.Line(), projected.Column())
	}
}

func TestLockParseRefOrderAndAuthoredCollisionCoordinates(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	t.Run("identity precedes digest", func(t *testing.T) {
		compound := strings.Replace(nonEmptyLockPayload, `"providerId":"sample"`, `"providerId":"Bad"`, 1)
		compound = strings.Replace(compound, `"manifestDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"manifestDigest":"BAD"`, 1)
		_, err := lock.Parse(key.RepositoryPath(), []byte(compound+"\n"), lock.DefaultLimits())
		assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "provider_id_invalid", "/release/providerId", lock.StageParse)
	})

	t.Run("canonical pointer uses authored location", func(t *testing.T) {
		digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		document := "{\n" +
			`  "apiVersion":"nexa.dev/source-lock/v1",` + "\n" +
			`  "kind":"SourceLock",` + "\n" +
			`  "profileClosure":["base","default"],` + "\n" +
			`  "profileId":"default",` + "\n" +
			`  "release":{"treeDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","providerId":"sample","version":"v0.1.0","packagePath":"example.test/source/provider","modulePath":"example.test/source","manifestDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},` + "\n" +
			`  "target":"services/sample",` + "\n" +
			`  "trackedFiles":[` + "\n" +
			`    {"digest":"` + digest + `","mode":"0644","path":"a/x","size":1},` + "\n" +
			`    {"digest":"` + digest + `","mode":"0644","path":"B","size":1},` + "\n" +
			`    {"digest":"` + digest + `","mode":"0644","path":"A","size":1}` + "\n" +
			`  ]` + "\n}"
		_, err := lock.Parse(key.RepositoryPath(), []byte(document), lock.DefaultLimits())
		projected := assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "tracked_file_collision", "/trackedFiles/2/path", lock.StageParse)
		if projected.Source() != key.RepositoryPath() || projected.Line() != 9 || projected.Column() <= 0 {
			t.Fatalf("authored diagnostics = source=%q line=%d column=%d", projected.Source(), projected.Line(), projected.Column())
		}
	})
}

func TestLockParseClosedSemanticErrorMatrix(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		reason  string
		pointer string
	}{
		{"version", func(document map[string]any) { document["apiVersion"] = "nexa.dev/source-lock/v2" }, "version_unsupported", "/apiVersion"},
		{"kind", func(document map[string]any) { document["kind"] = "Other" }, "kind_invalid", "/kind"},
		{"provider", func(document map[string]any) { document["release"].(map[string]any)["providerId"] = "Bad" }, "provider_id_invalid", "/release/providerId"},
		{"module", func(document map[string]any) { document["release"].(map[string]any)["modulePath"] = "bad path" }, "module_path_invalid", "/release/modulePath"},
		{"package relation", func(document map[string]any) {
			document["release"].(map[string]any)["packagePath"] = "example.test/other"
		}, "package_module_mismatch", "/release/packagePath"},
		{"release version", func(document map[string]any) { document["release"].(map[string]any)["version"] = "latest" }, "version_invalid", "/release/version"},
		{"manifest digest", func(document map[string]any) { document["release"].(map[string]any)["manifestDigest"] = "bad" }, "manifest_digest_invalid", "/release/manifestDigest"},
		{"tree digest", func(document map[string]any) { document["release"].(map[string]any)["treeDigest"] = "bad" }, "tree_digest_invalid", "/release/treeDigest"},
		{"profile", func(document map[string]any) { document["profileId"] = "Bad" }, "profile_id_invalid", "/profileId"},
		{"closure empty", func(document map[string]any) { document["profileClosure"] = []any{} }, "profile_closure_invalid", "/profileClosure"},
		{"closure member", func(document map[string]any) { document["profileClosure"] = []any{"Bad", "default"} }, "profile_closure_invalid", "/profileClosure/0"},
		{"closure duplicate", func(document map[string]any) { document["profileClosure"] = []any{"base", "base", "default"} }, "profile_closure_duplicate", "/profileClosure/1"},
		{"closure root", func(document map[string]any) { document["profileClosure"] = []any{"base"} }, "profile_closure_root_mismatch", "/profileClosure"},
		{"target", func(document map[string]any) { document["target"] = ".nexa/source" }, "key_target_invalid", "/target"},
		{"tracked path", func(document map[string]any) { firstLockFile(document)["path"] = "../bad" }, "tracked_file_path_invalid", "/trackedFiles/0/path"},
		{"tracked duplicate", func(document map[string]any) {
			first := firstLockFile(document)
			document["trackedFiles"] = []any{first, cloneLockMap(first)}
		}, "tracked_file_duplicate", "/trackedFiles/1/path"},
		{"tracked mode", func(document map[string]any) { firstLockFile(document)["mode"] = "0600" }, "tracked_file_mode_invalid", "/trackedFiles/0/mode"},
		{"tracked size", func(document map[string]any) { firstLockFile(document)["size"] = float64(-1) }, "tracked_file_size_invalid", "/trackedFiles/0/size"},
		{"tracked digest", func(document map[string]any) { firstLockFile(document)["digest"] = "bad" }, "tracked_file_digest_invalid", "/trackedFiles/0/digest"},
		{"tracked order", func(document map[string]any) {
			first := firstLockFile(document)
			left, right := cloneLockMap(first), cloneLockMap(first)
			left["path"], right["path"] = "z.txt", "a.txt"
			document["trackedFiles"] = []any{left, right}
		}, "tracked_file_order_invalid", "/trackedFiles/0/path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := mutateCanonicalLock(t, nonEmptyLockPayload, test.mutate)
			_, err := lock.Parse(key.RepositoryPath(), data, lock.DefaultLimits())
			projected := assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", test.reason, test.pointer, lock.StageParse)
			if projected.Source() != key.RepositoryPath() || projected.Line() <= 0 || projected.Column() <= 0 {
				t.Fatalf("location = %q:%d:%d", projected.Source(), projected.Line(), projected.Column())
			}
		})
	}
}

func TestLockParseCompoundOrderIsAuthoredOrderIndependent(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	makeFile := func(path, mode string, size float64, digest string) map[string]any {
		return map[string]any{"path": path, "mode": mode, "size": size, "digest": digest}
	}
	tests := []struct {
		name    string
		files   []any
		reason  string
		pointer string
	}{
		{"mode before size", []any{makeFile("z", "0644", -1, digest), makeFile("a", "0600", 1, digest)}, "tracked_file_mode_invalid", "/trackedFiles/0/mode"},
		{"size before digest", []any{makeFile("z", "0644", 1, "bad"), makeFile("a", "0644", -1, digest)}, "tracked_file_size_invalid", "/trackedFiles/0/size"},
	}
	for _, test := range tests {
		for _, reverse := range []bool{false, true} {
			name := test.name + "/forward"
			files := append([]any(nil), test.files...)
			if reverse {
				name = test.name + "/reverse"
				files[0], files[1] = files[1], files[0]
			}
			t.Run(name, func(t *testing.T) {
				data := mutateCanonicalLock(t, nonEmptyLockPayload, func(document map[string]any) { document["trackedFiles"] = files })
				_, err := lock.Parse(key.RepositoryPath(), data, lock.DefaultLimits())
				assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", test.reason, test.pointer, lock.StageParse)
			})
		}
	}
}

func TestLockParseExplicitDocumentClosureAndFileBounds(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	t.Run("document", func(t *testing.T) {
		limits := lock.DefaultLimits()
		limits.MaxDocumentBytes = int64(len(nonEmptyLockPayload))
		_, err := lock.Parse(key.RepositoryPath(), []byte(nonEmptyLockPayload+"\n"), limits)
		assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "document_bytes_exceeded", "", lock.StageParse)
	})
	t.Run("closure", func(t *testing.T) {
		limits := lock.DefaultLimits()
		limits.MaxProfileClosure = 1
		_, err := lock.Parse(key.RepositoryPath(), []byte(nonEmptyLockPayload+"\n"), limits)
		assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "profile_closure_invalid", "/profileClosure", lock.StageParse)
	})
	t.Run("tracked files", func(t *testing.T) {
		data := mutateCanonicalLock(t, nonEmptyLockPayload, func(document map[string]any) {
			first := firstLockFile(document)
			second := cloneLockMap(first)
			first["path"], second["path"] = "a", "b"
			document["trackedFiles"] = []any{first, second}
		})
		limits := lock.DefaultLimits()
		limits.MaxTrackedFiles = 1
		_, err := lock.Parse(key.RepositoryPath(), data, limits)
		assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "tracked_file_count_exceeded", "/trackedFiles", lock.StageParse)
	})
}

func TestDeriveParseResolveVerifyUsesOnlyOwnerBaseline(t *testing.T) {
	provider, ref := lockProvider(t, "sample.derive", "owner base", "owner main")
	resolved := resolveLockProvider(t, provider, ref)
	verified, err := lock.Derive(ref, resolved, "default", "services/sample", lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if verified.APIVersion() != lock.APIVersion || verified.Kind() != lock.Kind || verified.ProfileID() != "default" ||
		verified.Target() != "services/sample" || len(verified.ProfileClosure()) != 2 || len(verified.TrackedFiles()) != 2 || !verified.Release().Equal(ref) {
		t.Fatalf("verified = %#v", verified)
	}
	files := verified.TrackedFiles()
	for _, file := range files {
		treeFile, ok := resolved.Tree().Lookup(file.Path())
		if !ok || file.Mode() != treeFile.Mode() || file.Size() != treeFile.Size() || file.Digest() != treeFile.Digest() {
			t.Fatalf("baseline %q did not come from owner tree", file.Path())
		}
	}
	canonical, err := verified.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := lock.Parse(verified.Key().RepositoryPath(), canonical, lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source() != verified.Key().RepositoryPath() || !snapshot.Release().Equal(ref) || snapshot.Digest() != verified.Digest() {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := resolver.Resolve(context.Background(), snapshot.Release())
	if err != nil {
		t.Fatal(err)
	}
	reverified, err := lock.Verify(snapshot, verified.Key(), owner, lock.DefaultLimits())
	if err != nil || reverified.Digest() != verified.Digest() || !bytes.Equal(mustVerifiedCanonical(t, reverified), canonical) {
		t.Fatalf("reverified = %#v, err=%v", reverified, err)
	}

	other, otherRef := lockProvider(t, "sample.derive", "changed base", "changed main")
	otherResolved := resolveLockProvider(t, other, otherRef)
	_, err = lock.Verify(snapshot, verified.Key(), otherResolved, lock.DefaultLimits())
	assertLockError(t, err, lock.ErrLockConflict, "source_lock_conflict", "release_mismatch", "/release/manifestDigest", lock.StageVerify)
	wrongKey, _ := lock.NewKey("sample.derive", "services/other")
	_, err = lock.Verify(snapshot, wrongKey, owner, lock.DefaultLimits())
	assertLockError(t, err, lock.ErrLockConflict, "source_lock_conflict", "key_mismatch", "/key", lock.StageVerify)
}

func TestDeriveSupportsEmptyProfileAndLimitsAreExplicit(t *testing.T) {
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.empty-lock", ModulePath: "example.test/empty-lock", PackagePath: "example.test/empty-lock/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{}, Profiles: []sourceplugin.ProfileSpec{{ID: "empty", Files: []string{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sourceplugin.NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := lock.Derive(ref, resolveLockProvider(t, provider, ref), "empty", "services/empty", lock.DefaultLimits())
	if err != nil || len(verified.TrackedFiles()) != 0 || len(verified.ProfileClosure()) != 1 {
		t.Fatalf("empty verified = %#v, err=%v", verified, err)
	}
	limits := lock.DefaultLimits()
	limits.MaxTrackedFiles = 1
	provider, ref = lockProvider(t, "sample.limit", "a", "b")
	_, err = lock.Derive(ref, resolveLockProvider(t, provider, ref), "default", "services/sample", limits)
	assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "tracked_file_count_exceeded", "/trackedFiles", lock.StageDerive)
	_, err = lock.Derive(ref, resolveLockProvider(t, provider, ref), "missing", "services/sample", lock.DefaultLimits())
	assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "profile_not_found", "/profileId", lock.StageDerive)
}

func TestLockVerifyClosedConflictAndPrecedenceMatrix(t *testing.T) {
	provider, ref := lockProvider(t, "sample.verify-matrix", "owner base", "owner main")
	resolved := resolveLockProvider(t, provider, ref)
	verified, err := lock.Derive(ref, resolved, "default", "services/sample", lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	source := verified.Key().RepositoryPath()
	base := string(mustVerifiedCanonical(t, verified))
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		reason  string
		pointer string
	}{
		{"closure", func(document map[string]any) { document["profileClosure"] = []any{"other", "default"} }, "profile_closure_mismatch", "/profileClosure"},
		{"missing", func(document map[string]any) { document["trackedFiles"] = document["trackedFiles"].([]any)[1:] }, "baseline_file_missing", "/trackedFiles/0"},
		{"extra", func(document map[string]any) {
			files := document["trackedFiles"].([]any)
			files = append(files, map[string]any{"path": "zz-extra", "mode": "0644", "size": float64(1), "digest": provenance.SHA256([]byte("x")).String()})
			document["trackedFiles"] = files
		}, "baseline_file_extra", "/trackedFiles/2"},
		{"mode", func(document map[string]any) { firstLockFile(document)["mode"] = "0755" }, "baseline_file_mode_mismatch", "/trackedFiles/0/mode"},
		{"size", func(document map[string]any) {
			firstLockFile(document)["size"] = firstLockFile(document)["size"].(float64) + 1
		}, "baseline_file_size_mismatch", "/trackedFiles/0/size"},
		{"digest", func(document map[string]any) {
			firstLockFile(document)["digest"] = provenance.SHA256([]byte("wrong")).String()
		}, "baseline_file_digest_mismatch", "/trackedFiles/0/digest"},
		{"closure before file", func(document map[string]any) {
			document["profileClosure"] = []any{"other", "default"}
			firstLockFile(document)["digest"] = provenance.SHA256([]byte("wrong")).String()
		}, "profile_closure_mismatch", "/profileClosure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := mutateCanonicalLock(t, base, test.mutate)
			snapshot, err := lock.Parse(source, data, lock.DefaultLimits())
			if err != nil {
				t.Fatalf("fixture parse: %v", err)
			}
			_, err = lock.Verify(snapshot, verified.Key(), resolved, lock.DefaultLimits())
			assertLockError(t, err, lock.ErrLockConflict, "source_lock_conflict", test.reason, test.pointer, lock.StageVerify)
		})
	}
}

func TestLockLimitsUseCheckedArithmetic(t *testing.T) {
	key, _ := lock.NewKey("sample", "services/sample")
	valid := lock.DefaultLimits()
	tests := []struct {
		name    string
		mutate  func(*lock.Limits)
		pointer string
	}{
		{"document zero", func(l *lock.Limits) { l.MaxDocumentBytes = 0 }, "/limits/maxDocumentBytes"},
		{"closure zero", func(l *lock.Limits) { l.MaxProfileClosure = 0 }, "/limits/maxProfileClosure"},
		{"files zero", func(l *lock.Limits) { l.MaxTrackedFiles = 0 }, "/limits/maxTrackedFiles"},
		{"target zero", func(l *lock.Limits) { l.MaxTargetBytes = 0 }, "/limits/maxTargetBytes"},
		{"document overflow", func(l *lock.Limits) { l.MaxDocumentBytes = int64(^uint64(0) >> 1) }, "/limits/maxDocumentBytes"},
		{"closure overflow", func(l *lock.Limits) { l.MaxProfileClosure = int(^uint(0) >> 1) }, "/limits/maxProfileClosure"},
		{"files overflow", func(l *lock.Limits) { l.MaxTrackedFiles = int(^uint(0) >> 1) }, "/limits/maxTrackedFiles"},
		{"target overflow", func(l *lock.Limits) { l.MaxTargetBytes = int(^uint(0) >> 1) }, "/limits/maxTargetBytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := valid
			test.mutate(&limits)
			_, err := lock.Parse(key.RepositoryPath(), []byte(nonEmptyLockPayload+"\n"), limits)
			assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "lock_limit_invalid", test.pointer, lock.StageParse)
		})
	}
}

func TestLockCanonicalExactEscapedLengthAndPreflightAllocation(t *testing.T) {
	provider, ref := lockProvider(t, "sample.length", "base", "main")
	resolved := resolveLockProvider(t, provider, ref)
	target := `services/雪"quoted`
	verified, err := lock.Derive(ref, resolved, "default", target, lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	canonical := mustVerifiedCanonical(t, verified)
	exact := lock.DefaultLimits()
	exact.MaxDocumentBytes = int64(len(canonical))
	if _, err := lock.Derive(ref, resolved, "default", target, exact); err != nil {
		t.Fatalf("exact newline-inclusive boundary failed: %v", err)
	}
	exact.MaxDocumentBytes--
	_, err = lock.Derive(ref, resolved, "default", target, exact)
	assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "document_bytes_exceeded", "", lock.StageDerive)

	largeTarget := "services/" + strings.Repeat("x", 1<<20)
	low := lock.DefaultLimits()
	low.MaxTargetBytes = len(largeTarget)
	low.MaxDocumentBytes = 64
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			_, _ = lock.Derive(ref, resolved, "default", largeTarget, low)
		}
	})
	if result.AllocedBytesPerOp() > 256<<10 {
		t.Fatalf("low-policy Derive allocated %d bytes/op before rejecting canonical output", result.AllocedBytesPerOp())
	}
	_, err = lock.Derive(ref, resolved, "default", largeTarget, low)
	assertLockError(t, err, lock.ErrLockInput, "source_lock_invalid", "document_bytes_exceeded", "", lock.StageDerive)
}

func TestExternalConsumerCacheLoadDerivesOwnerBaseline(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("persistent cache unsupported")
	}
	provider, ref := lockProvider(t, "sample.cache-lock", "owner base", "owner main")
	resolved := resolveLockProvider(t, provider, ref)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := release.OpenDirectoryCache(filepath.Join(parent, "cache"), release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := lock.Derive(ref, loaded, "default", "services/sample", lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range verified.TrackedFiles() {
		ownerFile, ok := loaded.Tree().Lookup(baseline.Path())
		if !ok || baseline.Digest() != ownerFile.Digest() || baseline.Size() != ownerFile.Size() || baseline.Mode() != ownerFile.Mode() {
			t.Fatalf("baseline %q did not come from cached owner tree", baseline.Path())
		}
	}
	local := filepath.Join(parent, "local-main")
	if err := os.WriteFile(local, []byte("local merged bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if verified.TrackedFiles()[1].Digest() == provenance.SHA256([]byte("local merged bytes")) {
		t.Fatal("local bytes became baseline owner")
	}
}

func lockProvider(t *testing.T, providerID, baseContent, mainContent string) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	base, main := []byte(baseContent), []byte(mainContent)
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: providerID, ModulePath: "example.test/lock", PackagePath: "example.test/lock/source", Version: "v0.1.0"},
		Files: []sourceplugin.FileSpec{
			{Path: "base.txt", Size: int64(len(base)), Digest: provenance.SHA256(base), Mode: sourceplugin.Mode0644},
			{Path: "bin/main", Size: int64(len(main)), Digest: provenance.SHA256(main), Mode: sourceplugin.Mode0755},
		},
		Profiles: []sourceplugin.ProfileSpec{
			{ID: "base", Files: []string{"base.txt"}},
			{ID: "default", Files: []string{"bin/main"}, RequiresProfiles: []string{"base"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: "base.txt", Content: base}, {Path: "bin/main", Content: main}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sourceplugin.NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	return provider, ref
}

func resolveLockProvider(t *testing.T, provider sourceplugin.Provider, ref release.Ref) release.ResolvedRelease {
	t.Helper()
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func mustCanonical(t *testing.T, snapshot lock.Snapshot) []byte {
	t.Helper()
	result, err := snapshot.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustVerifiedCanonical(t *testing.T, verified lock.VerifiedLock) []byte {
	t.Helper()
	result, err := verified.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mutateCanonicalLock(t *testing.T, source string, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(source), &document); err != nil {
		t.Fatal(err)
	}
	mutate(document)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	return append(canonical, '\n')
}

func canonicalLockWithTrackedFiles(t *testing.T, count int) (string, []byte, lock.Limits) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(nonEmptyLockPayload), &document); err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	files := make([]any, count)
	for index := range files {
		files[index] = map[string]any{
			"path": fmt.Sprintf("files/%08d.txt", index), "mode": "0644", "size": 0, "digest": digest,
		}
	}
	document["trackedFiles"] = files
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical = append(canonical, '\n')
	key, err := lock.NewKey("sample", "services/sample")
	if err != nil {
		t.Fatal(err)
	}
	limits := lock.DefaultLimits()
	limits.MaxTrackedFiles = count
	return key.RepositoryPath(), canonical, limits
}

func canonicalLockWithPaths(t *testing.T, paths []string) (string, []byte, lock.Limits) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(nonEmptyLockPayload), &document); err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	files := make([]any, len(paths))
	for index, filePath := range paths {
		files[index] = map[string]any{"path": filePath, "mode": "0644", "size": 0, "digest": digest}
	}
	document["trackedFiles"] = files
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical = append(canonical, '\n')
	key, err := lock.NewKey("sample", "services/sample")
	if err != nil {
		t.Fatal(err)
	}
	limits := lock.DefaultLimits()
	limits.MaxTrackedFiles = len(paths)
	return key.RepositoryPath(), canonical, limits
}

func firstLockFile(document map[string]any) map[string]any {
	return document["trackedFiles"].([]any)[0].(map[string]any)
}

func cloneLockMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func assertLockError(t *testing.T, err error, class lock.ErrorClass, code, reason, pointer string, stage lock.Stage) *lock.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s", reason)
	}
	var projected *lock.Error
	if !errors.As(err, &projected) || projected.Class() != class || projected.Code() != code || projected.Reason() != reason ||
		projected.Pointer() != pointer || projected.Stage() != stage || projected.Error() != class.Error() ||
		projected.Line() < 0 || projected.Column() < 0 || !errors.Is(projected, class) || errors.Unwrap(projected) != nil {
		t.Fatalf("error = %#v", err)
	}
	return projected
}

func codeForLockClass(class lock.ErrorClass) string {
	if class == lock.ErrLockConflict {
		return "source_lock_conflict"
	}
	return "source_lock_invalid"
}
