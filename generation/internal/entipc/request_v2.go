package entipc

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/module"
)

const (
	RequestV2APIVersion = "nexa.dev/ent-graph-request/v2"
	RequestV2Kind       = "EntGraphRequest"
)

var buildTagV2Pattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

type RequestV2Spec struct {
	ModuleDir  string
	ModulePath string
	SchemaDir  string
	BuildTags  []string
}

type RequestV2 struct{ state *requestV2State }
type requestV2State struct {
	moduleDir, modulePath, schemaDir string
	buildTags                        []string
	digest                           provenance.Digest
	canonical                        []byte
}

type requestV2Wire struct {
	APIVersion    string   `json:"apiVersion"`
	Kind          string   `json:"kind"`
	ModuleDir     string   `json:"moduleDir"`
	ModulePath    string   `json:"modulePath"`
	SchemaDir     string   `json:"schemaDir"`
	BuildTags     []string `json:"buildTags"`
	RequestDigest string   `json:"requestDigest"`
}

type requestV2Preimage struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	ModuleDir  string   `json:"moduleDir"`
	ModulePath string   `json:"modulePath"`
	SchemaDir  string   `json:"schemaDir"`
	BuildTags  []string `json:"buildTags"`
}

func NewRequestV2(spec RequestV2Spec) (RequestV2, error) {
	state, err := validateRequestV2Spec(spec, "")
	if err != nil {
		return RequestV2{}, err
	}
	preimage := requestV2Preimage{RequestV2APIVersion, RequestV2Kind, state.moduleDir, state.modulePath, state.schemaDir, append([]string{}, state.buildTags...)}
	encoded, err := json.Marshal(preimage)
	if err != nil {
		return RequestV2{}, requestError("canonical_invalid", "", "")
	}
	canonicalPreimage, err := jcs.Transform(encoded)
	if err != nil {
		return RequestV2{}, requestError("canonical_invalid", "", "")
	}
	state.digest = provenance.SHA256(canonicalPreimage)
	wire := requestV2Wire{RequestV2APIVersion, RequestV2Kind, state.moduleDir, state.modulePath, state.schemaDir, append([]string{}, state.buildTags...), state.digest.String()}
	encoded, err = json.Marshal(wire)
	if err != nil {
		return RequestV2{}, requestError("canonical_invalid", "", "")
	}
	state.canonical, err = jcs.Transform(encoded)
	if err != nil {
		return RequestV2{}, requestError("canonical_invalid", "", "")
	}
	return RequestV2{state: state}, nil
}

func ParseRequestV2(source provenance.DomainSource, data []byte) (RequestV2, error) {
	if source.String() == "" {
		return RequestV2{}, requestError("document_invalid", "", "")
	}
	if !utf8.Valid(data) {
		return RequestV2{}, requestError("unicode_invalid", "", source.String())
	}
	document, err := strictdoc.ParseJSON(source.String(), data)
	if err != nil {
		return RequestV2{}, projectDocumentError(err, source.String())
	}
	root, ok := decodeObject(document.JSON())
	if !ok {
		return RequestV2{}, requestError("document_type_invalid", "", source.String())
	}
	allowed := map[string]bool{"apiVersion": true, "kind": true, "moduleDir": true, "modulePath": true, "schemaDir": true, "buildTags": true, "requestDigest": true}
	for key := range root {
		if !allowed[key] {
			return RequestV2{}, requestError("document_unknown_field", "/"+key, source.String())
		}
	}
	for key := range allowed {
		if _, present := root[key]; !present {
			return RequestV2{}, requestError("document_required_missing", "/"+key, source.String())
		}
	}
	var wire requestV2Wire
	if err := json.Unmarshal(document.JSON(), &wire); err != nil {
		return RequestV2{}, requestError("document_type_invalid", "", source.String())
	}
	if wire.APIVersion != RequestV2APIVersion {
		return RequestV2{}, requestError("version_unsupported", "/apiVersion", source.String())
	}
	if wire.Kind != RequestV2Kind {
		return RequestV2{}, requestError("kind_invalid", "/kind", source.String())
	}
	request, err := NewRequestV2(RequestV2Spec{wire.ModuleDir, wire.ModulePath, wire.SchemaDir, wire.BuildTags})
	if err != nil {
		return RequestV2{}, withSource(err, source.String())
	}
	if wire.RequestDigest != request.state.digest.String() {
		return RequestV2{}, requestError("request_digest_mismatch", "/requestDigest", source.String())
	}
	if !bytes.Equal(data, request.state.canonical) {
		return RequestV2{}, requestError("canonical_invalid", "", source.String())
	}
	return request, nil
}

func validateRequestV2Spec(spec RequestV2Spec, source string) (*requestV2State, error) {
	if !cleanRelativeV2(spec.ModuleDir, true) {
		return nil, requestError("module_root_invalid", "/moduleDir", source)
	}
	if module.CheckPath(spec.ModulePath) != nil {
		return nil, requestError("module_path_mismatch", "/modulePath", source)
	}
	if !cleanRelativeV2(spec.SchemaDir, false) || spec.ModuleDir != "." && spec.SchemaDir != spec.ModuleDir && !strings.HasPrefix(spec.SchemaDir, spec.ModuleDir+"/") {
		return nil, requestError("schema_dir_escape", "/schemaDir", source)
	}
	tags := append([]string{}, spec.BuildTags...)
	seen := make(map[string]struct{}, len(tags))
	for index, tag := range tags {
		if !buildTagV2Pattern.MatchString(tag) {
			return nil, requestError("document_type_invalid", "/buildTags/"+itoa(index), source)
		}
		if _, duplicate := seen[tag]; duplicate {
			return nil, requestError("build_tags_mismatch", "/buildTags/"+itoa(index), source)
		}
		seen[tag] = struct{}{}
	}
	sort.Strings(tags)
	return &requestV2State{moduleDir: spec.ModuleDir, modulePath: spec.ModulePath, schemaDir: spec.SchemaDir, buildTags: tags}, nil
}

func cleanRelativeV2(value string, dot bool) bool {
	if value == "" || strings.Contains(value, `\`) || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	if value == "." {
		return dot
	}
	return value != ".." && !strings.HasPrefix(value, "../")
}

func CanonicalRequestV2(request RequestV2) ([]byte, error) {
	if request.state == nil {
		return nil, requestError("canonical_invalid", "", "")
	}
	return append([]byte(nil), request.state.canonical...), nil
}
func (r RequestV2) ModuleDir() string {
	if r.state == nil {
		return ""
	}
	return r.state.moduleDir
}
func (r RequestV2) ModulePath() string {
	if r.state == nil {
		return ""
	}
	return r.state.modulePath
}
func (r RequestV2) SchemaDir() string {
	if r.state == nil {
		return ""
	}
	return r.state.schemaDir
}
func (r RequestV2) BuildTags() []string {
	if r.state == nil {
		return nil
	}
	return append([]string{}, r.state.buildTags...)
}
func (r RequestV2) RequestDigest() provenance.Digest {
	if r.state == nil {
		return provenance.Digest{}
	}
	return r.state.digest
}

func HelperRequestV2Source() provenance.DomainSource {
	source, _ := provenance.ParseDomainSource("stdin/ent-graph-request-v2.json")
	return source
}
