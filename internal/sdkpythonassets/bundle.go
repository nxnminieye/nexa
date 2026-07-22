package sdkpythonassets

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	api "github.com/nxnminieye/nexa/sdk/api"
)

const (
	BundleAPIVersion      = "nexa.dev/python-sdk-asset-bundle/v1"
	AbsentDigest          = "absent"
	packageRelativeDir    = "sdk/python/nexa"
	generatedRelativeDir  = packageRelativeDir + "/_generated"
	objectsRelativeDir    = generatedRelativeDir + "/objects/sha256"
	indexRelativePath     = generatedRelativeDir + "/bundle-index.json"
	bootstrapRelativePath = packageRelativeDir + "/_bootstrap.py"
	resourceRawBytes      = 65_536
	resourceJSONDepth     = 8
	resourceJSONNodes     = 512
	roleJSONDepth         = 64
	roleJSONNodes         = 65_536
)

var closedRoleIDs = []string{
	"bundle-index-schema",
	"remote-error-limits",
	"remote-error-limits-schema",
	"remote-error-schema",
	"runtime-adapter-result-schema",
	"runtime-contract-limits",
	"runtime-contract-limits-schema",
	"runtime-contract-schema",
	"runtime-corpus",
	"runtime-corpus-schema",
	"runtime-limits",
	"runtime-limits-schema",
}

type Role struct {
	ID         string `json:"id"`
	APIVersion string `json:"apiVersion"`
	MediaType  string `json:"mediaType"`
	Path       string `json:"path"`
	Digest     string `json:"digest"`
	SchemaRole string `json:"schemaRole,omitempty"`
}

type indexRole struct {
	APIVersion string `json:"apiVersion"`
	MediaType  string `json:"mediaType"`
	Path       string `json:"path"`
	Digest     string `json:"digest"`
	SchemaRole string `json:"schemaRole,omitempty"`
}

type indexDocument struct {
	APIVersion string               `json:"apiVersion"`
	Roles      map[string]indexRole `json:"roles"`
}

type AssetBundleBootstrapContract struct {
	IndexPath        string
	BundleAPIVersion string
	RawBytes         int
	JSONDepth        int
	JSONNodes        int
	RoleRawBytes     int
	RoleJSONDepth    int
	RoleJSONNodes    int
}

type AssetBundle struct {
	roles     []Role
	objects   map[string][]byte
	index     []byte
	bootstrap []byte
	contract  AssetBundleBootstrapContract
}

func NewAssetBundle() (AssetBundle, error) {
	indexSchema := bundleIndexSchemaBytes()
	runtimeCorpus, err := api.RuntimeCorpusBytes()
	if err != nil {
		return AssetBundle{}, fmt.Errorf("runtime corpus: %w", err)
	}
	resources := []struct {
		id, apiVersion, mediaType, schemaRole string
		data                                  []byte
	}{
		{"bundle-index-schema", schemaIdentity(indexSchema), "application/schema+json", "", indexSchema},
		{"remote-error-limits", api.RemoteErrorLimitsAPIVersion, "application/json", "remote-error-limits-schema", mustCanonicalValue(api.RemoteErrorLimits())},
		{"remote-error-limits-schema", schemaIdentity(api.RemoteErrorLimitsSchema()), "application/schema+json", "", api.RemoteErrorLimitsSchema()},
		{"remote-error-schema", schemaIdentity(api.RemoteErrorSchema()), "application/schema+json", "", api.RemoteErrorSchema()},
		{"runtime-adapter-result-schema", schemaIdentity(api.RuntimeAdapterResultSchema()), "application/schema+json", "", api.RuntimeAdapterResultSchema()},
		{"runtime-contract-limits", api.RuntimeContractLimitsAPIVersion, "application/json", "runtime-contract-limits-schema", mustCanonicalValue(api.RuntimeContractLimits())},
		{"runtime-contract-limits-schema", schemaIdentity(api.RuntimeContractLimitsSchema()), "application/schema+json", "", api.RuntimeContractLimitsSchema()},
		{"runtime-contract-schema", schemaIdentity(api.RuntimeContractSchema()), "application/schema+json", "", api.RuntimeContractSchema()},
		{"runtime-corpus", api.RuntimeCorpusAPIVersion, "application/json", "runtime-corpus-schema", runtimeCorpus},
		{"runtime-corpus-schema", schemaIdentity(api.RuntimeCorpusSchema()), "application/schema+json", "", api.RuntimeCorpusSchema()},
		{"runtime-limits", api.RuntimeLimitsAPIVersion, "application/json", "runtime-limits-schema", mustCanonicalValue(api.RuntimeLimits())},
		{"runtime-limits-schema", schemaIdentity(api.RuntimeLimitsSchema()), "application/schema+json", "", api.RuntimeLimitsSchema()},
	}
	roles := make([]Role, 0, len(resources))
	objects := make(map[string][]byte, len(resources))
	indexRoles := make(map[string]indexRole, len(resources))
	for _, resource := range resources {
		canonical, err := canonicalJSON(resource.data)
		if err != nil {
			return AssetBundle{}, fmt.Errorf("%s: %w", resource.id, err)
		}
		digest := digestBytes(canonical)
		path := "objects/sha256/" + strings.TrimPrefix(digest, "sha256:") + ".json"
		role := Role{ID: resource.id, APIVersion: resource.apiVersion, MediaType: resource.mediaType, Path: path, Digest: digest, SchemaRole: resource.schemaRole}
		roles = append(roles, role)
		objects[resource.id] = append([]byte(nil), canonical...)
		indexRoles[resource.id] = indexRole{APIVersion: role.APIVersion, MediaType: role.MediaType, Path: role.Path, Digest: role.Digest, SchemaRole: role.SchemaRole}
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].ID < roles[j].ID })
	if len(roles) != len(closedRoleIDs) {
		return AssetBundle{}, errors.New("closed role count mismatch")
	}
	for i := range roles {
		if roles[i].ID != closedRoleIDs[i] {
			return AssetBundle{}, errors.New("closed role set mismatch")
		}
	}
	index := mustCanonicalValue(indexDocument{APIVersion: BundleAPIVersion, Roles: indexRoles})
	contract := AssetBundleBootstrapContract{IndexPath: "_generated/bundle-index.json", BundleAPIVersion: BundleAPIVersion, RawBytes: resourceRawBytes, JSONDepth: resourceJSONDepth, JSONNodes: resourceJSONNodes, RoleRawBytes: resourceRawBytes, RoleJSONDepth: roleJSONDepth, RoleJSONNodes: roleJSONNodes}
	bootstrap := generateBootstrap(contract)
	return AssetBundle{roles: roles, objects: objects, index: index, bootstrap: bootstrap, contract: contract}, nil
}

func (b AssetBundle) Roles() []Role                                   { return append([]Role(nil), b.roles...) }
func (b AssetBundle) IndexBytes() []byte                              { return append([]byte(nil), b.index...) }
func (b AssetBundle) BootstrapBytes() []byte                          { return append([]byte(nil), b.bootstrap...) }
func (b AssetBundle) BootstrapContract() AssetBundleBootstrapContract { return b.contract }
func (b AssetBundle) Object(id string) []byte                         { return append([]byte(nil), b.objects[id]...) }

func canonicalJSON(data []byte) ([]byte, error) {
	if _, err := strictdoc.ParseJSON("sdk-python-asset.json", data); err != nil {
		return nil, err
	}
	canonical, err := jcs.Transform(data)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func mustCanonicalValue(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		panic(err)
	}
	return canonical
}

func digestBytes(data []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }

func schemaIdentity(data []byte) string {
	var value struct {
		ID string `json:"$id"`
	}
	if json.Unmarshal(data, &value) == nil && value.ID != "" {
		return value.ID
	}
	return "nexa.dev/schema/unknown"
}

func bundleIndexSchemaBytes() []byte {
	properties := make(map[string]any, len(closedRoleIDs))
	for _, id := range closedRoleIDs {
		properties[id] = map[string]any{"$ref": "#/$defs/role"}
	}
	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://nexa.dev/schemas/python-sdk-asset-bundle-index-v1.schema.json",
		"type": "object", "additionalProperties": false, "required": []string{"apiVersion", "roles"},
		"properties": map[string]any{"apiVersion": map[string]any{"const": BundleAPIVersion}, "roles": map[string]any{"type": "object", "additionalProperties": false, "required": closedRoleIDs, "properties": properties}},
		"$defs": map[string]any{"role": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"apiVersion", "mediaType", "path", "digest"}, "properties": map[string]any{
			"apiVersion": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "mediaType": map[string]any{"enum": []string{"application/json", "application/schema+json"}},
			"path": map[string]any{"type": "string", "pattern": "^objects/sha256/[0-9a-f]{64}\\.json$"}, "digest": map[string]any{"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"}, "schemaRole": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
		}},
		},
	}
	return mustCanonicalValue(schema)
}
