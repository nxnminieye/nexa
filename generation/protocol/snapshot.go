package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

type Snapshot struct {
	state  *snapshotState
	marker snapshotMarker
}
type snapshotMarker struct{ _ [0]func() }
type SnapshotMethod struct{ state *snapshotMethodState }
type snapshotState struct {
	canonical    []byte
	methods      map[string]*snapshotMethodState
	sources      []provenance.Source
	sourceDigest provenance.Digest
	usedSources  map[string]struct{}
	descriptors  *snapshotDescriptorIndex
}
type snapshotMethodState struct {
	fullName string
}

func ParseSnapshot(source provenance.DomainSource, data []byte) (Snapshot, error) {
	if source.String() == "" {
		return Snapshot{}, snapshotError("document_invalid", "", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire canonicalDocument
	if err := decoder.Decode(&wire); err != nil {
		return Snapshot{}, snapshotError("document_invalid", source.String(), "")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Snapshot{}, snapshotError("document_invalid", source.String(), "")
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil || validateSnapshotSchema(schemaValue) != nil {
		return Snapshot{}, snapshotError("document_invalid", source.String(), "")
	}
	if wire.APIVersion != APIVersion {
		return Snapshot{}, snapshotError("version_unsupported", source.String(), "/apiVersion")
	}
	if wire.Kind != Kind {
		return Snapshot{}, snapshotError("kind_invalid", source.String(), "/kind")
	}
	canonical, err := canonicalize(wire)
	if err != nil || !bytes.Equal(canonical, data) {
		return Snapshot{}, snapshotError("canonical_order_invalid", source.String(), "")
	}
	storedDigest, err := provenance.ParseDigest(wire.SourceDigest)
	if err != nil {
		return Snapshot{}, snapshotError("source_digest_invalid", source.String(), "/sourceDigest")
	}
	sources := make([]provenance.Source, len(wire.Sources))
	sourceIndex := make(map[string]provenance.Source, len(wire.Sources))
	previous := ""
	for i, item := range wire.Sources {
		ref, refErr := provenance.ParseSourceRef(item.Ref)
		digest, digestErr := provenance.ParseDigest(item.Digest)
		if refErr != nil || digestErr != nil || previous != "" && item.Ref <= previous {
			return Snapshot{}, snapshotError("source_invalid", source.String(), "/sources")
		}
		previous = item.Ref
		sources[i] = provenance.Source{Ref: ref, Digest: digest}
		sourceIndex[item.Ref] = sources[i]
	}
	setBytes, err := canonicalize(canonicalSourceSet{APIVersion: SourceSetAPIVersion, Sources: wire.Sources})
	if err != nil || provenance.SHA256(setBytes) != storedDigest {
		return Snapshot{}, snapshotError("source_digest_mismatch", source.String(), "/sourceDigest")
	}
	descriptors, err := buildSnapshotDescriptorIndex(wire)
	if err != nil {
		return Snapshot{}, snapshotError(err.(*Error).Reason(), source.String(), err.(*Error).Pointer())
	}
	state := &snapshotState{canonical: append([]byte(nil), canonical...), methods: map[string]*snapshotMethodState{}, sources: sources, sourceDigest: storedDigest, usedSources: map[string]struct{}{}, descriptors: descriptors}
	previousFile := ""
	for _, file := range wire.Files {
		if previousFile != "" && file.Path <= previousFile {
			return Snapshot{}, snapshotError("canonical_order_invalid", source.String(), "/files")
		}
		previousFile = file.Path
		if err := validateSnapshotFile(file, sourceIndex, state); err != nil {
			return Snapshot{}, snapshotError(err.(*Error).Reason(), source.String(), err.(*Error).Pointer())
		}
	}
	if len(state.usedSources) != len(sources) {
		return Snapshot{}, snapshotError("source_closure_invalid", source.String(), "/sources")
	}
	return Snapshot{state: state}, nil
}

func validateSnapshotFile(file canonicalFile, sources map[string]provenance.Source, state *snapshotState) error {
	previous := ""
	for _, message := range file.Messages {
		if previous != "" && message.FullName <= previous {
			return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/messages", "Protocol snapshot order is invalid")
		}
		previous = message.FullName
		if err := validateProjectedSource(file.Path, "message:"+message.FullName, message.SourceRef, canonicalMessageNode{APIVersion: messageNodeAPIVersion, Kind: "message", FullName: message.FullName}, sources, state); err != nil {
			return err
		}
		lastNumber := 0
		for _, field := range message.Fields {
			if field.Number <= lastNumber {
				return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/messages/fields", "Protocol snapshot order is invalid")
			}
			lastNumber = field.Number
			node := canonicalFieldNode{APIVersion: fieldNodeAPIVersion, Kind: "field", FullName: field.FullName, Number: field.Number, JSONName: field.JSONName, Cardinality: field.Cardinality, Presence: field.Presence, Type: field.Type, Oneof: field.Oneof}
			if err := validateSnapshotField(message.FullName, field, state.descriptors, file.Path); err != nil {
				return err
			}
			if err := validateProjectedSource(file.Path, "field:"+field.FullName, field.SourceRef, node, sources, state); err != nil {
				return err
			}
		}
	}
	previous = ""
	for _, enum := range file.Enums {
		if previous != "" && enum.FullName <= previous {
			return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/enums", "Protocol snapshot order is invalid")
		}
		previous = enum.FullName
	}
	previous = ""
	for _, service := range file.Services {
		if previous != "" && service.FullName <= previous {
			return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/services", "Protocol snapshot order is invalid")
		}
		previous = service.FullName
		previousMethod := ""
		for _, method := range service.Methods {
			if previousMethod != "" && method.FullName <= previousMethod {
				return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/services/methods", "Protocol snapshot order is invalid")
			}
			previousMethod = method.FullName
			if !strings.HasPrefix(method.FullName, service.FullName+".") || state.descriptors.messages[method.Input] == nil || state.descriptors.messages[method.Output] == nil {
				return protocolError("protocol_snapshot_invalid", "method_descriptor_invalid", file.Path, "/services/methods", "Protocol snapshot method descriptor is invalid")
			}
			node := canonicalMethodNode{APIVersion: methodNodeAPIVersion, Kind: "method", FullName: method.FullName, Input: method.Input, Output: method.Output, ClientStreaming: method.ClientStreaming, ServerStreaming: method.ServerStreaming}
			if err := validateProjectedSource(file.Path, "method:"+method.FullName, method.SourceRef, node, sources, state); err != nil {
				return err
			}
			if _, duplicate := state.methods[method.FullName]; duplicate {
				return protocolError("protocol_snapshot_invalid", "method_duplicate", file.Path, "/services/methods", "Protocol snapshot method is duplicated")
			}
			state.methods[method.FullName] = &snapshotMethodState{fullName: method.FullName}
		}
	}
	return nil
}

func validateProjectedSource(filePath, fragment, sourceRef string, node any, sources map[string]provenance.Source, state *snapshotState) error {
	ref, err := provenance.RepositoryRef(filePath, fragment)
	if err != nil || ref.String() != sourceRef {
		return protocolError("protocol_snapshot_invalid", "source_ref_invalid", filePath, "/sourceRef", "Protocol snapshot source reference is invalid")
	}
	source, ok := sources[sourceRef]
	if !ok {
		return protocolError("protocol_snapshot_invalid", "source_closure_invalid", filePath, "/sourceRef", "Protocol snapshot source closure is invalid")
	}
	state.usedSources[sourceRef] = struct{}{}
	encoded, err := canonicalize(node)
	if err != nil || provenance.SHA256(encoded) != source.Digest {
		return protocolError("protocol_snapshot_invalid", "source_digest_mismatch", filePath, "/sourceRef", "Protocol snapshot owner digest is invalid")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return err
	}
	return nil
}
func (s Snapshot) CanonicalJSON() ([]byte, error) {
	if s.state == nil {
		return nil, snapshotError("document_invalid", "", "")
	}
	return append([]byte(nil), s.state.canonical...), nil
}
func (s Snapshot) ProjectedSources() []provenance.Source {
	if s.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), s.state.sources...)
}
func (s Snapshot) SourceDigest() provenance.Digest {
	if s.state == nil {
		return provenance.Digest{}
	}
	return s.state.sourceDigest
}
func (s Snapshot) Method(fullName string) (SnapshotMethod, bool) {
	if s.state == nil {
		return SnapshotMethod{}, false
	}
	value, ok := s.state.methods[fullName]
	return SnapshotMethod{state: value}, ok
}
func (m SnapshotMethod) FullName() string {
	if m.state == nil {
		return ""
	}
	return m.state.fullName
}
func snapshotError(reason, source, pointer string) *Error {
	return protocolError("protocol_snapshot_invalid", reason, source, pointer, "Protocol snapshot is invalid")
}
