package sdkpythonassets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type CheckRequest struct {
	RepoRoot string
	Mode     LoaderMode
}
type Snapshot struct{ bundle AssetBundle }

func (s Snapshot) Roles() []Role           { return s.bundle.Roles() }
func (s Snapshot) IndexBytes() []byte      { return s.bundle.IndexBytes() }
func (s Snapshot) BootstrapBytes() []byte  { return s.bundle.BootstrapBytes() }
func (s Snapshot) Object(id string) []byte { return s.bundle.Object(id) }

type Owner struct {
	bundle    AssetBundle
	builder   WheelBuilder
	bundleErr error
}

func NewOwner(builder WheelBuilder) *Owner {
	bundle, err := NewAssetBundle()
	return &Owner{bundle: bundle, builder: builder, bundleErr: err}
}

func (o *Owner) Check(ctx context.Context, request CheckRequest) (CheckResult, error) {
	if request.Mode == "" {
		request.Mode = SourceTreeMode
	}
	if request.Mode != SourceTreeMode {
		return CheckResult{}, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", "read-only")
	}
	root, err := openRepoRoot(request.RepoRoot, "read-only")
	if err != nil {
		return CheckResult{}, err
	}
	defer root.Close()
	if err := requireManagedDirectories(root); err != nil {
		return CheckResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CheckResult{}, ownerError(ReasonOperationCanceled, "/context", "check", "read-only")
	}
	snapshot, err := o.checkSnapshot(root)
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{APIVersion: "nexa.dev/sdk-python-assets-check-result/v1", IndexDigest: digestBytes(snapshot.IndexBytes()), BootstrapDigest: digestBytes(snapshot.BootstrapBytes()), Status: "clean", Roles: snapshot.Roles(), ObjectCount: len(snapshot.Roles())}, nil
}

func (o *Owner) checkSnapshot(root *os.Root) (Snapshot, error) {
	if o == nil || o.bundleErr != nil {
		return Snapshot{}, ownerError(ReasonIOFailed, "/bundleIndex", "check", "read-only")
	}
	bootstrap, err := readRegularMode(root, bootstrapRelativePath, resourceRawBytes, 0o644)
	if err != nil || !bytes.Equal(bootstrap, o.bundle.bootstrap) {
		return Snapshot{}, ownerError(ReasonBootstrapProjectionDrift, "/bootstrap", "check", "read-only")
	}
	index, err := readRegularMode(root, indexRelativePath, resourceRawBytes, 0o644)
	if os.IsNotExist(err) {
		return Snapshot{}, ownerError(ReasonBundleIndexMissing, "/bundleIndex", "check", "read-only")
	}
	if err != nil || !bytes.Equal(index, o.bundle.index) {
		return Snapshot{}, ownerError(ReasonBundleIndexDrift, "/bundleIndex", "check", "read-only")
	}
	if _, err := boundedCanonicalWith(index, resourceRawBytes, resourceJSONDepth, resourceJSONNodes); err != nil {
		return Snapshot{}, ownerError(ReasonBundleIndexDrift, "/bundleIndex", "check", "read-only")
	}
	var doc indexDocument
	if json.Unmarshal(index, &doc) != nil || doc.APIVersion != BundleAPIVersion || len(doc.Roles) != len(o.bundle.roles) {
		return Snapshot{}, ownerError(ReasonBundleRoleSetDrift, "/roles", "check", "read-only")
	}
	objects := make(map[string][]byte, len(o.bundle.roles))
	for _, want := range o.bundle.roles {
		got, ok := doc.Roles[want.ID]
		if !ok {
			return Snapshot{}, ownerError(ReasonBundleRoleSetDrift, "/roles", "check", "read-only")
		}
		if got.APIVersion != want.APIVersion || got.MediaType != want.MediaType || got.Path != want.Path || got.Digest != want.Digest || got.SchemaRole != want.SchemaRole {
			return Snapshot{}, ownerError(ReasonBundleRoleDrift, "/roles/"+want.ID, "check", "read-only")
		}
		data, err := readRegularMode(root, generatedRelativeDir+"/"+want.Path, resourceRawBytes, 0o644)
		if err != nil || digestBytes(data) != want.Digest || !bytes.Equal(data, o.bundle.Object(want.ID)) {
			return Snapshot{}, ownerError(ReasonBundleRoleDrift, "/roles/"+want.ID, "check", "read-only")
		}
		if _, err := boundedCanonicalWith(data, resourceRawBytes, roleJSONDepth, roleJSONNodes); err != nil {
			return Snapshot{}, ownerError(ReasonBundleRoleDrift, "/roles/"+want.ID, "check", "read-only")
		}
		objects[want.ID] = data
	}
	if role, err := validateRoleSchemas(o.bundle.roles, objects); err != nil {
		return Snapshot{}, ownerError(ReasonBundleRoleDrift, "/roles/"+role, "check", "read-only")
	}
	return Snapshot{bundle: o.bundle}, nil
}

func validateRoleSchemas(roles []Role, objects map[string][]byte) (string, error) {
	compiler := jsonschema.NewCompiler()
	for _, role := range roles {
		if !strings.HasSuffix(role.ID, "-schema") {
			continue
		}
		var value any
		if json.Unmarshal(objects[role.ID], &value) != nil {
			return role.ID, errors.New("schema JSON invalid")
		}
		if err := compiler.AddResource(role.APIVersion, value); err != nil {
			return role.ID, err
		}
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, role := range roles {
		if !strings.HasSuffix(role.ID, "-schema") {
			continue
		}
		schema, err := compiler.Compile(role.APIVersion)
		if err != nil {
			return role.ID, err
		}
		compiled[role.ID] = schema
	}
	for _, role := range roles {
		if role.SchemaRole == "" {
			continue
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(objects[role.ID]))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return role.ID, errors.New("value JSON invalid")
		}
		schema := compiled[role.SchemaRole]
		if schema == nil || schema.Validate(value) != nil {
			return role.ID, errors.New("value schema invalid")
		}
	}
	return "", nil
}

func boundedCanonicalWith(data []byte, rawLimit, depthLimit, nodeLimit int) (any, error) {
	if len(data) > rawLimit {
		return nil, errors.New("raw")
	}
	canonical, err := canonicalJSON(data)
	if err != nil || !bytes.Equal(canonical, data) {
		return nil, errors.New("canonical")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, errors.New("json")
	}
	nodes := 0
	if !withinBounds(value, 0, &nodes, depthLimit, nodeLimit) {
		return nil, errors.New("bounds")
	}
	return value, nil
}
func withinBounds(value any, depth int, nodes *int, depthLimit, nodeLimit int) bool {
	*nodes = *nodes + 1
	if depth > depthLimit || *nodes > nodeLimit {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			if !withinBounds(child, depth+1, nodes, depthLimit, nodeLimit) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !withinBounds(child, depth+1, nodes, depthLimit, nodeLimit) {
				return false
			}
		}
	}
	return true
}
