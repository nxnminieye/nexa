package entipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	ResultV2APIVersion = "nexa.dev/ent-graph-result/v2"
	ResultV2Kind       = "EntGraphResult"
)

type ResultV2 struct {
	requestDigest provenance.Digest
	snapshot      entity.Snapshot
	domain        *DomainFailure
	canonical     []byte
}

type resultV2Wire struct {
	APIVersion     string          `json:"apiVersion"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status,omitempty"`
	RequestDigest  string          `json:"requestDigest,omitempty"`
	EntitySnapshot json.RawMessage `json:"entitySnapshot,omitempty"`
	Error          *domainWire     `json:"error,omitempty"`
}

func NewProjectedResultV2(request RequestV2, document entity.Document) (ResultV2, error) {
	if request.state == nil {
		return ResultV2{}, resultError("request_invalid", "/requestDigest", "")
	}
	snapshotBytes, err := entity.CanonicalJSON(document)
	if err != nil {
		return ResultV2{}, resultError("entity_snapshot_invalid", "/entitySnapshot", "")
	}
	source, _ := provenance.ParseDomainSource("result/entity-snapshot.json")
	snapshot, err := entity.ParseSnapshot(source, snapshotBytes)
	if err != nil {
		return ResultV2{}, resultError("entity_snapshot_invalid", "/entitySnapshot", "")
	}
	return encodeResultV2(request.state.digest, snapshot, nil)
}

func NewDomainResultV2(owner, code, reason, pointer, source string) (ResultV2, error) {
	failure := &DomainFailure{owner: owner, code: code, reason: reason, pointer: pointer, source: source}
	if !validDomainFailureV2(*failure) {
		return ResultV2{}, resultError("error_invalid", "/error", "")
	}
	return encodeResultV2(provenance.Digest{}, entity.Snapshot{}, failure)
}

func encodeResultV2(digest provenance.Digest, snapshot entity.Snapshot, failure *DomainFailure) (ResultV2, error) {
	result := ResultV2{requestDigest: digest, snapshot: snapshot, domain: failure}
	wire := resultV2Wire{APIVersion: ResultV2APIVersion, Kind: ResultV2Kind}
	if failure != nil {
		wire.Error = &domainWire{failure.owner, failure.code, failure.reason, failure.pointer, failure.source}
	} else {
		canonical, err := snapshot.CanonicalJSON()
		if err != nil || digest.String() == "" {
			return ResultV2{}, resultError("entity_snapshot_invalid", "/entitySnapshot", "")
		}
		wire.Status, wire.RequestDigest, wire.EntitySnapshot = "projected", digest.String(), canonical
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return ResultV2{}, resultError("canonical_invalid", "", "")
	}
	result.canonical, err = jcs.Transform(encoded)
	if err != nil {
		return ResultV2{}, resultError("canonical_invalid", "", "")
	}
	return result, nil
}

func ParseResultV2(source provenance.DomainSource, request RequestV2, data []byte) (ResultV2, error) {
	if source.String() == "" || request.state == nil {
		return ResultV2{}, resultError("document_invalid", "", source.String())
	}
	if !utf8.Valid(data) {
		return ResultV2{}, resultError("unicode_invalid", "", source.String())
	}
	document, err := strictdoc.ParseJSON(source.String(), data)
	if err != nil {
		return ResultV2{}, resultError("document_invalid", "", source.String())
	}
	var root map[string]any
	if json.Unmarshal(document.JSON(), &root) != nil || root == nil {
		return ResultV2{}, resultError("document_type_invalid", "", source.String())
	}
	var wire resultV2Wire
	if json.Unmarshal(document.JSON(), &wire) != nil || wire.APIVersion != ResultV2APIVersion || wire.Kind != ResultV2Kind {
		return ResultV2{}, resultError("version_unsupported", "/apiVersion", source.String())
	}
	var result ResultV2
	if wire.Error != nil {
		if len(root) != 3 || root["error"] == nil || !validDomainFailureV2(DomainFailure{wire.Error.Owner, wire.Error.Code, wire.Error.Reason, wire.Error.Pointer, wire.Error.Source}) {
			return ResultV2{}, resultError("error_invalid", "/error", source.String())
		}
		if (wire.Error.Owner == "entityload" && wire.Error.Code == "entity_graph_load_failed" && wire.Error.Source != request.SchemaDir()) || (wire.Error.Owner == "entity" && wire.Error.Source != "" && wire.Error.Source != request.SchemaDir()) {
			return ResultV2{}, resultError("domain_source_mismatch", "/error/source", source.String())
		}
		result, err = NewDomainResultV2(wire.Error.Owner, wire.Error.Code, wire.Error.Reason, wire.Error.Pointer, wire.Error.Source)
	} else {
		if len(root) != 5 || wire.Status != "projected" || wire.RequestDigest != request.state.digest.String() || len(wire.EntitySnapshot) == 0 {
			return ResultV2{}, resultError("result_branch_invalid", "", source.String())
		}
		snapshotSource, _ := provenance.ParseDomainSource("stdout/entity-snapshot.json")
		snapshot, parseErr := entity.ParseSnapshot(snapshotSource, wire.EntitySnapshot)
		if parseErr != nil {
			return ResultV2{}, resultError("entity_snapshot_invalid", "/entitySnapshot", source.String())
		}
		result, err = encodeResultV2(request.state.digest, snapshot, nil)
	}
	if err != nil || !bytes.Equal(data, result.canonical) {
		return ResultV2{}, resultError("canonical_invalid", "", source.String())
	}
	return result, nil
}

func validDomainFailureV2(value DomainFailure) bool {
	for _, item := range []string{value.owner, value.code, value.reason, value.pointer, value.source} {
		if !utf8.ValidString(item) || strings.IndexFunc(item, unicode.IsControl) >= 0 {
			return false
		}
	}
	if value.owner == "entityload" {
		input := value.code == "entity_input_invalid" && value.source == "" && ((value.reason == "module_root_invalid" && value.pointer == "/moduleDir") || (value.reason == "module_path_mismatch" && value.pointer == "/modulePath") || ((value.reason == "schema_dir_escape" || value.reason == "schema_source_invalid" || value.reason == "importer_visibility_invalid") && value.pointer == "/schemaDir"))
		load := value.code == "entity_graph_load_failed" && value.pointer == "" && value.source != "" && (value.reason == "graph_load_failed" || value.reason == "source_projection_failed" || value.reason == "helper_prepare_failed" || value.reason == "helper_execution_failed" || value.reason == "helper_cleanup_failed")
		return input || load
	}
	if value.owner == "entity" {
		_, validation := entity.ParseEntHelperErrorProjection(value.code, value.reason, value.pointer, value.source)
		return validation == nil
	}
	if value.owner == "nexaent" {
		_, validation := nexaent.ParseEntHelperErrorProjection(value.code, value.reason, value.pointer, value.source)
		return validation == nil
	}
	return false
}

func CanonicalResultV2(result ResultV2) ([]byte, error) {
	if len(result.canonical) == 0 {
		return nil, resultError("canonical_invalid", "", "")
	}
	return append([]byte(nil), result.canonical...), nil
}
func (r ResultV2) Projected() (entity.Snapshot, bool) {
	return r.snapshot, r.domain == nil && len(r.canonical) != 0
}
func (r ResultV2) DomainFailure() (DomainFailure, bool) {
	if r.domain == nil {
		return DomainFailure{}, false
	}
	return *r.domain, true
}

func HelperResultV2Source() provenance.DomainSource {
	source, _ := provenance.ParseDomainSource("stdout/ent-graph-result-v2.json")
	return source
}
