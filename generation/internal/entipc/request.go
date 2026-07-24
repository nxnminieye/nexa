package entipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	RequestAPIVersion   = "nexa.dev/ent-graph-request/v1"
	RequestKind         = "EntGraphRequest"
	requestInputVersion = "nexa.dev/ent-graph-request-input/v1"
)

var (
	serviceIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)
	protoPackagePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	goPackagePattern    = regexp.MustCompile(`^[A-Za-z0-9_.~/-]+(;[A-Za-z_][A-Za-z0-9_]*)?$`)
	toolTokenPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
)

type ProtoDestination struct{ EntryPath, ArtifactPath, LockPath string }
type ToolIdentity struct{ ID, Version, ExecutableVersion string }
type MultiTenantConfig struct{ Enabled bool }
type ExistingLockInput struct {
	Source provenance.Source
	Value  crudbuild.Lock
}
type PublishedArtifact struct {
	ID             string
	Digest         provenance.Digest
	ManifestSource provenance.Source
}

type RequestSpec struct {
	RepositoryRoot                      string
	SchemaDir                           provenance.DomainSource
	BuildTags                           []string
	ModuleGraphDigest, BuildInputDigest provenance.Digest
	ModuleSources                       []provenance.Source
	ServiceID, ProtoPackage, GoPackage  string
	ProtoDestination                    ProtoDestination
	Tool                                ToolIdentity
	ExistingLock                        *ExistingLockInput
	PublishedArtifact                   *PublishedArtifact
	MultiTenant                         MultiTenantConfig
}

type EntitySpec struct {
	RepositoryRoot                                      string
	SchemaDir                                           provenance.DomainSource
	BuildTags                                           []string
	ExpectedModuleGraphDigest, ExpectedBuildInputDigest provenance.Digest
}

type Request struct{ state *requestState }
type requestState struct {
	repositoryRoot                      string
	schemaDir                           provenance.DomainSource
	buildTags                           []string
	moduleGraphDigest, buildInputDigest provenance.Digest
	moduleSources                       []provenance.Source
	serviceID, protoPackage, goPackage  string
	protoDestination                    ProtoDestination
	tool                                ToolIdentity
	existingLock                        *ExistingLockInput
	publishedArtifact                   *PublishedArtifact
	multiTenant                         MultiTenantConfig
	requestDigest                       provenance.Digest
	canonical                           []byte
}

type requestWire struct {
	APIVersion        string                 `json:"apiVersion"`
	Kind              string                 `json:"kind"`
	RepositoryRoot    string                 `json:"repositoryRoot"`
	SchemaDir         string                 `json:"schemaDir"`
	BuildTags         []string               `json:"buildTags"`
	ModuleGraphDigest string                 `json:"moduleGraphDigest"`
	BuildInputDigest  string                 `json:"buildInputDigest"`
	ModuleSources     []sourceWire           `json:"moduleSources"`
	ServiceID         string                 `json:"serviceId"`
	ProtoPackage      string                 `json:"protoPackage"`
	GoPackage         string                 `json:"goPackage"`
	ProtoDestination  destinationWire        `json:"protoDestination"`
	Tool              toolWire               `json:"tool"`
	ExistingLock      *existingLockWire      `json:"existingLock,omitempty"`
	PublishedArtifact *publishedArtifactWire `json:"publishedArtifact,omitempty"`
	MultiTenant       multiTenantWire        `json:"multiTenant"`
	RequestDigest     string                 `json:"requestDigest"`
}
type multiTenantWire struct {
	Enabled bool `json:"enabled"`
}
type destinationWire struct {
	EntryPath    string `json:"entryPath"`
	ArtifactPath string `json:"artifactPath"`
	LockPath     string `json:"lockPath"`
}
type toolWire struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	ExecutableVersion string `json:"executableVersion"`
}
type sourceWire struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type existingLockWire struct {
	Source sourceWire      `json:"source"`
	Value  json.RawMessage `json:"value"`
}
type publishedArtifactWire struct {
	ID             string     `json:"id"`
	Digest         string     `json:"digest"`
	ManifestSource sourceWire `json:"manifestSource"`
}
type requestDigestWire struct {
	APIVersion         string                 `json:"apiVersion"`
	SchemaDir          string                 `json:"schemaDir"`
	BuildTags          []string               `json:"buildTags"`
	ModuleGraphDigest  string                 `json:"moduleGraphDigest"`
	BuildInputDigest   string                 `json:"buildInputDigest"`
	ModuleSources      []sourceWire           `json:"moduleSources"`
	ServiceID          string                 `json:"serviceId"`
	ProtoPackage       string                 `json:"protoPackage"`
	GoPackage          string                 `json:"goPackage"`
	ProtoDestination   destinationWire        `json:"protoDestination"`
	Tool               toolWire               `json:"tool"`
	ExistingLockDigest string                 `json:"existingLockDigest,omitempty"`
	PublishedArtifact  *publishedArtifactWire `json:"publishedArtifact,omitempty"`
	MultiTenant        multiTenantWire        `json:"multiTenant"`
}

func NewRequest(spec RequestSpec) (Request, error) {
	state, err := validateRequestSpec(spec, "")
	if err != nil {
		return Request{}, err
	}
	wire, err := requestWireFromState(state)
	if err != nil {
		return Request{}, requestError("canonical_invalid", "", "")
	}
	state.canonical, err = canonicalJSON(wire)
	if err != nil {
		return Request{}, requestError("canonical_invalid", "", "")
	}
	return Request{state: state}, nil
}

func ParseRequest(source provenance.DomainSource, data []byte) (Request, error) {
	if source.String() == "" {
		return Request{}, requestError("document_invalid", "", "")
	}
	if !utf8.Valid(data) {
		return Request{}, requestError("unicode_invalid", "", source.String())
	}
	document, err := strictdoc.ParseJSON(source.String(), data)
	if err != nil {
		return Request{}, projectDocumentError(err, source.String())
	}
	root, ok := decodeObject(document.JSON())
	if !ok {
		return Request{}, requestError("document_type_invalid", "", source.String())
	}
	if reason, pointer := requestShapeIssue(root); reason != "" {
		return Request{}, requestError(reason, pointer, source.String())
	}
	var wire requestWire
	if err := json.Unmarshal(document.JSON(), &wire); err != nil {
		return Request{}, requestError("document_type_invalid", "", source.String())
	}
	spec, err := specFromWire(wire, source)
	if err != nil {
		return Request{}, err
	}
	request, err := NewRequest(spec)
	if err != nil {
		return Request{}, withSource(err, source.String())
	}
	if wire.APIVersion != RequestAPIVersion {
		return Request{}, requestError("version_unsupported", "/apiVersion", source.String())
	}
	if wire.Kind != RequestKind {
		return Request{}, requestError("kind_invalid", "/kind", source.String())
	}
	if wire.RequestDigest != request.state.requestDigest.String() {
		return Request{}, requestError("request_digest_mismatch", "/requestDigest", source.String())
	}
	if !bytes.Equal(data, request.state.canonical) {
		return Request{}, requestError("canonical_invalid", "", source.String())
	}
	return request, nil
}

func CanonicalRequest(request Request) ([]byte, error) {
	if request.state == nil || len(request.state.canonical) == 0 {
		return nil, requestError("canonical_invalid", "", "")
	}
	return append([]byte(nil), request.state.canonical...), nil
}
func (r Request) RequestDigest() provenance.Digest {
	if r.state == nil {
		return provenance.Digest{}
	}
	return r.state.requestDigest
}
func (r Request) EntitySpec() (EntitySpec, error) {
	if r.state == nil {
		return EntitySpec{}, requestError("canonical_invalid", "", "")
	}
	return EntitySpec{RepositoryRoot: r.state.repositoryRoot, SchemaDir: r.state.schemaDir, BuildTags: append([]string(nil), r.state.buildTags...), ExpectedModuleGraphDigest: r.state.moduleGraphDigest, ExpectedBuildInputDigest: r.state.buildInputDigest}, nil
}
func (r Request) BuildSpec() (crudbuild.Spec, error) {
	if r.state == nil {
		return crudbuild.Spec{}, requestError("canonical_invalid", "", "")
	}
	result := crudbuild.Spec{ServiceID: r.state.serviceID, ProtoPackage: r.state.protoPackage, GoPackage: r.state.goPackage, ProtoArtifactPath: r.state.protoDestination.ArtifactPath, LockPath: r.state.protoDestination.LockPath, RequestDigest: r.state.requestDigest, MultiTenant: crudbuild.MultiTenantConfig{Enabled: r.state.multiTenant.Enabled}}
	if r.state.existingLock != nil {
		value := r.state.existingLock.Value
		source := r.state.existingLock.Source
		result.ExistingLock = &value
		result.ExistingLockSource = &source
	}
	if r.state.publishedArtifact != nil {
		value := r.state.publishedArtifact
		result.PublishedArtifact = &crudbuild.PublishedArtifact{ID: value.ID, Digest: value.Digest, ManifestSource: value.ManifestSource}
	}
	return result, nil
}

func HelperRequestSource() provenance.DomainSource {
	source, _ := provenance.ParseDomainSource("stdin/ent-graph-request.json")
	return source
}

func validateRequestSpec(spec RequestSpec, source string) (*requestState, error) {
	if !filepath.IsAbs(spec.RepositoryRoot) || filepath.Clean(spec.RepositoryRoot) != spec.RepositoryRoot {
		return nil, requestError("document_type_invalid", "/repositoryRoot", source)
	}
	if spec.SchemaDir.String() == "" {
		return nil, requestError("document_type_invalid", "/schemaDir", source)
	}
	tags := append([]string{}, spec.BuildTags...)
	seen := map[string]struct{}{}
	for index, value := range tags {
		if !validBuildTag(value) {
			return nil, requestError("document_type_invalid", "/buildTags/"+itoa(index), source)
		}
		if _, ok := seen[value]; ok {
			return nil, requestError("build_tags_mismatch", "/buildTags/"+itoa(index), source)
		}
		seen[value] = struct{}{}
	}
	sort.Strings(tags)
	if spec.ModuleGraphDigest.String() == "" {
		return nil, requestError("module_graph_digest_invalid", "/moduleGraphDigest", source)
	}
	if spec.BuildInputDigest.String() == "" {
		return nil, requestError("build_input_digest_invalid", "/buildInputDigest", source)
	}
	moduleSources := append([]provenance.Source(nil), spec.ModuleSources...)
	for index, value := range moduleSources {
		if value.Ref.String() == "" {
			return nil, requestError("module_graph_digest_invalid", "/moduleSources/"+itoa(index)+"/ref", source)
		}
		if value.Digest.String() == "" {
			return nil, requestError("module_graph_digest_invalid", "/moduleSources/"+itoa(index)+"/digest", source)
		}
	}
	if len(moduleSources) == 0 {
		return nil, requestError("module_graph_digest_invalid", "/moduleSources", source)
	}
	sort.Slice(moduleSources, func(i, j int) bool { return moduleSources[i].Ref.String() < moduleSources[j].Ref.String() })
	for index := 1; index < len(moduleSources); index++ {
		if moduleSources[index-1].Ref == moduleSources[index].Ref {
			return nil, requestError("module_graph_digest_invalid", "/moduleSources/"+itoa(index)+"/ref", source)
		}
	}
	if !serviceIDPattern.MatchString(spec.ServiceID) {
		return nil, requestError("document_type_invalid", "/serviceId", source)
	}
	if !protoPackagePattern.MatchString(spec.ProtoPackage) {
		return nil, requestError("document_type_invalid", "/protoPackage", source)
	}
	if !goPackagePattern.MatchString(spec.GoPackage) {
		return nil, requestError("document_type_invalid", "/goPackage", source)
	}
	for name, value := range map[string]string{"entryPath": spec.ProtoDestination.EntryPath, "artifactPath": spec.ProtoDestination.ArtifactPath, "lockPath": spec.ProtoDestination.LockPath} {
		ref, err := provenance.RepositoryRef(value, "")
		if err != nil || ref.Path() != value {
			return nil, requestError("proto_destination_invalid", "/protoDestination/"+name, source)
		}
	}
	if !toolTokenPattern.MatchString(spec.Tool.ID) || !toolTokenPattern.MatchString(spec.Tool.Version) || strings.TrimSpace(spec.Tool.ExecutableVersion) == "" || len(spec.Tool.ExecutableVersion) > 1024 {
		return nil, requestError("tool_invalid", "/tool", source)
	}
	state := &requestState{repositoryRoot: spec.RepositoryRoot, schemaDir: spec.SchemaDir, buildTags: tags, moduleGraphDigest: spec.ModuleGraphDigest, buildInputDigest: spec.BuildInputDigest, moduleSources: moduleSources, serviceID: spec.ServiceID, protoPackage: spec.ProtoPackage, goPackage: spec.GoPackage, protoDestination: spec.ProtoDestination, tool: spec.Tool, multiTenant: spec.MultiTenant}
	if spec.ExistingLock != nil {
		if !spec.ExistingLock.Value.Valid() {
			return nil, requestError("existing_lock_invalid", "/existingLock/value", source)
		}
		lockBytes := spec.ExistingLock.Value.CanonicalJSON()
		if spec.ExistingLock.Source.Ref.String() == "" || spec.ExistingLock.Source.Digest != provenance.SHA256(lockBytes) {
			return nil, requestError("lock_digest_mismatch", "/existingLock/source/digest", source)
		}
		value := *spec.ExistingLock
		state.existingLock = &value
	}
	if spec.PublishedArtifact != nil {
		if spec.PublishedArtifact.ID == "" || spec.PublishedArtifact.Digest.String() == "" || spec.PublishedArtifact.ManifestSource.Ref.String() == "" || spec.PublishedArtifact.ManifestSource.Digest.String() == "" {
			return nil, requestError("published_artifact_invalid", "/publishedArtifact", source)
		}
		value := *spec.PublishedArtifact
		state.publishedArtifact = &value
	}
	preimage := requestDigestWire{APIVersion: requestInputVersion, SchemaDir: state.schemaDir.String(), BuildTags: state.buildTags, ModuleGraphDigest: state.moduleGraphDigest.String(), BuildInputDigest: state.buildInputDigest.String(), ModuleSources: sourcesToWire(state.moduleSources), ServiceID: state.serviceID, ProtoPackage: state.protoPackage, GoPackage: state.goPackage, ProtoDestination: destinationWire{state.protoDestination.EntryPath, state.protoDestination.ArtifactPath, state.protoDestination.LockPath}, Tool: toolWire{state.tool.ID, state.tool.Version, state.tool.ExecutableVersion}, MultiTenant: multiTenantWire{Enabled: state.multiTenant.Enabled}}
	if state.existingLock != nil {
		preimage.ExistingLockDigest = provenance.SHA256(state.existingLock.Value.CanonicalJSON()).String()
	}
	if state.publishedArtifact != nil {
		preimage.PublishedArtifact = publishedWire(state.publishedArtifact)
	}
	canonical, err := canonicalJSON(preimage)
	if err != nil {
		return nil, requestError("canonical_invalid", "", source)
	}
	state.requestDigest = provenance.SHA256(canonical)
	return state, nil
}

func requestWireFromState(state *requestState) (requestWire, error) {
	wire := requestWire{APIVersion: RequestAPIVersion, Kind: RequestKind, RepositoryRoot: state.repositoryRoot, SchemaDir: state.schemaDir.String(), BuildTags: append([]string{}, state.buildTags...), ModuleGraphDigest: state.moduleGraphDigest.String(), BuildInputDigest: state.buildInputDigest.String(), ModuleSources: sourcesToWire(state.moduleSources), ServiceID: state.serviceID, ProtoPackage: state.protoPackage, GoPackage: state.goPackage, ProtoDestination: destinationWire{state.protoDestination.EntryPath, state.protoDestination.ArtifactPath, state.protoDestination.LockPath}, Tool: toolWire{state.tool.ID, state.tool.Version, state.tool.ExecutableVersion}, MultiTenant: multiTenantWire{Enabled: state.multiTenant.Enabled}, RequestDigest: state.requestDigest.String()}
	if state.existingLock != nil {
		wire.ExistingLock = &existingLockWire{Source: sourceToWire(state.existingLock.Source), Value: append(json.RawMessage(nil), state.existingLock.Value.CanonicalJSON()...)}
	}
	if state.publishedArtifact != nil {
		wire.PublishedArtifact = publishedWire(state.publishedArtifact)
	}
	return wire, nil
}
func specFromWire(w requestWire, source provenance.DomainSource) (RequestSpec, error) {
	schema, err := provenance.ParseDomainSource(w.SchemaDir)
	if err != nil {
		return RequestSpec{}, requestError("document_type_invalid", "/schemaDir", source.String())
	}
	graph, err := provenance.ParseDigest(w.ModuleGraphDigest)
	if err != nil {
		return RequestSpec{}, requestError("module_graph_digest_invalid", "/moduleGraphDigest", source.String())
	}
	input, err := provenance.ParseDigest(w.BuildInputDigest)
	if err != nil {
		return RequestSpec{}, requestError("build_input_digest_invalid", "/buildInputDigest", source.String())
	}
	moduleSources := make([]provenance.Source, len(w.ModuleSources))
	for index, value := range w.ModuleSources {
		moduleSources[index], err = parseSource(value)
		if err != nil {
			return RequestSpec{}, requestError("module_graph_digest_invalid", "/moduleSources/"+itoa(index), source.String())
		}
	}
	spec := RequestSpec{RepositoryRoot: w.RepositoryRoot, SchemaDir: schema, BuildTags: w.BuildTags, ModuleGraphDigest: graph, BuildInputDigest: input, ModuleSources: moduleSources, ServiceID: w.ServiceID, ProtoPackage: w.ProtoPackage, GoPackage: w.GoPackage, ProtoDestination: ProtoDestination{w.ProtoDestination.EntryPath, w.ProtoDestination.ArtifactPath, w.ProtoDestination.LockPath}, Tool: ToolIdentity{w.Tool.ID, w.Tool.Version, w.Tool.ExecutableVersion}, MultiTenant: MultiTenantConfig{Enabled: w.MultiTenant.Enabled}}
	if w.ExistingLock != nil {
		lockSource, err := parseSource(w.ExistingLock.Source)
		if err != nil {
			return RequestSpec{}, requestError("existing_lock_source_invalid", "/existingLock/source", source.String())
		}
		lockSourceName, _ := provenance.ParseDomainSource("request/existing-lock.json")
		lock, err := crudbuild.ParseLock(lockSourceName, w.ExistingLock.Value)
		if err != nil {
			return RequestSpec{}, requestError("existing_lock_invalid", "/existingLock/value", source.String())
		}
		spec.ExistingLock = &ExistingLockInput{Source: lockSource, Value: lock}
	}
	if w.PublishedArtifact != nil {
		digest, err := provenance.ParseDigest(w.PublishedArtifact.Digest)
		if err != nil {
			return RequestSpec{}, requestError("published_artifact_invalid", "/publishedArtifact/digest", source.String())
		}
		manifest, err := parseSource(w.PublishedArtifact.ManifestSource)
		if err != nil {
			return RequestSpec{}, requestError("published_artifact_source_invalid", "/publishedArtifact/manifestSource", source.String())
		}
		spec.PublishedArtifact = &PublishedArtifact{ID: w.PublishedArtifact.ID, Digest: digest, ManifestSource: manifest}
	}
	return spec, nil
}
func sourceToWire(s provenance.Source) sourceWire {
	return sourceWire{Ref: s.Ref.String(), Digest: s.Digest.String()}
}
func sourcesToWire(sources []provenance.Source) []sourceWire {
	result := make([]sourceWire, len(sources))
	for index, source := range sources {
		result[index] = sourceToWire(source)
	}
	return result
}
func parseSource(w sourceWire) (provenance.Source, error) {
	ref, err := provenance.ParseSourceRef(w.Ref)
	if err != nil {
		return provenance.Source{}, err
	}
	digest, err := provenance.ParseDigest(w.Digest)
	if err != nil {
		return provenance.Source{}, err
	}
	return provenance.Source{Ref: ref, Digest: digest}, nil
}
func publishedWire(p *PublishedArtifact) *publishedArtifactWire {
	return &publishedArtifactWire{ID: p.ID, Digest: p.Digest.String(), ManifestSource: sourceToWire(p.ManifestSource)}
}
func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}
func decodeObject(data []byte) (map[string]any, bool) {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return nil, false
	}
	result, ok := value.(map[string]any)
	return result, ok
}

func requestShapeIssue(root map[string]any) (string, string) {
	required := []string{"apiVersion", "kind", "repositoryRoot", "schemaDir", "buildTags", "moduleGraphDigest", "buildInputDigest", "moduleSources", "serviceId", "protoPackage", "goPackage", "protoDestination", "tool", "multiTenant", "requestDigest"}
	allowed := append(append([]string(nil), required...), "existingLock", "publishedArtifact")
	if reason, pointer := exactObjectIssue(root, "", allowed, required); reason != "" {
		return reason, pointer
	}
	for _, name := range []string{"apiVersion", "kind", "repositoryRoot", "schemaDir", "moduleGraphDigest", "buildInputDigest", "serviceId", "protoPackage", "goPackage", "requestDigest"} {
		if _, ok := root[name].(string); !ok {
			return "document_type_invalid", "/" + name
		}
	}
	tags, ok := root["buildTags"].([]any)
	if !ok {
		return "document_type_invalid", "/buildTags"
	}
	for index, item := range tags {
		if _, ok := item.(string); !ok {
			return "document_type_invalid", "/buildTags/" + itoa(index)
		}
	}
	moduleSources, ok := root["moduleSources"].([]any)
	if !ok {
		return "document_type_invalid", "/moduleSources"
	}
	for index, item := range moduleSources {
		if reason, pointer := sourceShapeIssue(item, "/moduleSources/"+itoa(index)); reason != "" {
			return reason, pointer
		}
	}
	destination, ok := root["protoDestination"].(map[string]any)
	if !ok {
		return "document_type_invalid", "/protoDestination"
	}
	if reason, pointer := exactObjectIssue(destination, "/protoDestination", []string{"entryPath", "artifactPath", "lockPath"}, []string{"entryPath", "artifactPath", "lockPath"}); reason != "" {
		return reason, pointer
	}
	for _, name := range []string{"entryPath", "artifactPath", "lockPath"} {
		if _, ok := destination[name].(string); !ok {
			return "document_type_invalid", "/protoDestination/" + name
		}
	}
	tool, ok := root["tool"].(map[string]any)
	if !ok {
		return "document_type_invalid", "/tool"
	}
	if reason, pointer := exactObjectIssue(tool, "/tool", []string{"id", "version", "executableVersion"}, []string{"id", "version", "executableVersion"}); reason != "" {
		return reason, pointer
	}
	for _, name := range []string{"id", "version", "executableVersion"} {
		if _, ok := tool[name].(string); !ok {
			return "document_type_invalid", "/tool/" + name
		}
	}
	multiTenant, ok := root["multiTenant"].(map[string]any)
	if !ok {
		return "document_type_invalid", "/multiTenant"
	}
	if reason, pointer := exactObjectIssue(multiTenant, "/multiTenant", []string{"enabled"}, []string{"enabled"}); reason != "" {
		return reason, pointer
	}
	if _, ok := multiTenant["enabled"].(bool); !ok {
		return "document_type_invalid", "/multiTenant/enabled"
	}
	if raw, present := root["existingLock"]; present {
		value, ok := raw.(map[string]any)
		if !ok {
			return "document_type_invalid", "/existingLock"
		}
		if reason, pointer := exactObjectIssue(value, "/existingLock", []string{"source", "value"}, []string{"source", "value"}); reason != "" {
			return reason, pointer
		}
		if reason, pointer := sourceShapeIssue(value["source"], "/existingLock/source"); reason != "" {
			return reason, pointer
		}
		if _, ok := value["value"].(map[string]any); !ok {
			return "document_type_invalid", "/existingLock/value"
		}
	}
	if raw, present := root["publishedArtifact"]; present {
		value, ok := raw.(map[string]any)
		if !ok {
			return "document_type_invalid", "/publishedArtifact"
		}
		if reason, pointer := exactObjectIssue(value, "/publishedArtifact", []string{"id", "digest", "manifestSource"}, []string{"id", "digest", "manifestSource"}); reason != "" {
			return reason, pointer
		}
		for _, name := range []string{"id", "digest"} {
			if _, ok := value[name].(string); !ok {
				return "document_type_invalid", "/publishedArtifact/" + name
			}
		}
		if reason, pointer := sourceShapeIssue(value["manifestSource"], "/publishedArtifact/manifestSource"); reason != "" {
			return reason, pointer
		}
	}
	return "", ""
}

func exactObjectIssue(value map[string]any, base string, allowed, required []string) (string, string) {
	allow := map[string]struct{}{}
	for _, name := range allowed {
		allow[name] = struct{}{}
	}
	for name := range value {
		if _, ok := allow[name]; !ok {
			return "document_unknown_field", base + "/" + name
		}
	}
	for _, name := range required {
		if _, ok := value[name]; !ok {
			return "document_required_missing", base + "/" + name
		}
	}
	return "", ""
}
func sourceShapeIssue(raw any, base string) (string, string) {
	value, ok := raw.(map[string]any)
	if !ok {
		return "document_type_invalid", base
	}
	if reason, pointer := exactObjectIssue(value, base, []string{"ref", "digest"}, []string{"ref", "digest"}); reason != "" {
		return reason, pointer
	}
	for _, name := range []string{"ref", "digest"} {
		if _, ok := value[name].(string); !ok {
			return "document_type_invalid", base + "/" + name
		}
	}
	return "", ""
}
func projectDocumentError(err error, source string) error {
	var typed *strictdoc.Error
	if errors.As(err, &typed) {
		return requestError(typed.Code, typed.Pointer, source)
	}
	return requestError("document_invalid", "", source)
}
func withSource(err error, source string) error {
	var typed *Error
	if errors.As(err, &typed) {
		return requestError(typed.reason, typed.pointer, source)
	}
	return err
}
func validBuildTag(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range []byte(value) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var data [20]byte
	index := len(data)
	for value > 0 {
		index--
		data[index] = byte('0' + value%10)
		value /= 10
	}
	return string(data[index:])
}
