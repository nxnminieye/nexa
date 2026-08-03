package crudbuild

import (
	"fmt"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

// ProtocolProjection binds the structurally generated CRUD Proto nodes to the
// Ent nodes that produced them. The caller supplies a Proto document compiled
// from the same rendered bytes with only generated $source directives removed;
// its descriptor canonical form is the expected inherited structure.
func ProtocolProjection(document Document, source string, baseline protocol.Document, upstream sourcecomment.FactGraph) (protocol.SourceProjection, error) {
	if document.state == nil || source == "" || !upstream.Valid() || baseline.ServiceID() != document.state.serviceID {
		return protocol.SourceProjection{}, fmt.Errorf("CRUD Proto source projection input is invalid")
	}
	result := protocol.SourceProjection{Upstream: upstream}
	appendMessage := func(message *messageState) error {
		fullName := document.state.protoPackage + "." + message.name
		compiled, present := baseline.Message(fullName)
		if !present || !message.firstSource.Valid() {
			return fmt.Errorf("generated Proto message %q is unavailable", fullName)
		}
		downstream, err := sourcecomment.ParseSourceRef("proto://" + source + "#" + fullName)
		if err != nil {
			return err
		}
		result.Nodes = append(result.Nodes, sourcecomment.ProjectionExpectation{
			Downstream: downstream, Upstream: message.firstSource, SemanticID: fullName, Kind: sourcecomment.NodeMessage,
			ExpectedNativeCanonical: compiled.CanonicalSourceJSON(),
		})
		for _, field := range message.fields {
			fieldName := fullName + "." + field.name
			compiledField, fieldPresent := baseline.Message(fullName)
			if !fieldPresent || !field.firstSource.Valid() {
				return fmt.Errorf("generated Proto field %q is unavailable", fieldName)
			}
			var matched protocol.Field
			for _, candidate := range compiledField.Fields() {
				if candidate.FullName() == fieldName {
					matched = candidate
					break
				}
			}
			if matched.FullName() == "" {
				return fmt.Errorf("generated Proto field %q is unavailable", fieldName)
			}
			fieldDownstream, fieldErr := sourcecomment.ParseSourceRef("proto://" + source + "#" + fieldName)
			if fieldErr != nil {
				return fieldErr
			}
			result.Nodes = append(result.Nodes, sourcecomment.ProjectionExpectation{
				Downstream: fieldDownstream, Upstream: field.firstSource, SemanticID: fieldName, Kind: sourcecomment.NodeProtoField,
				ExpectedNativeCanonical: matched.CanonicalSourceJSON(),
			})
		}
		return nil
	}
	for _, message := range document.state.messages {
		if err := appendMessage(message); err != nil {
			return protocol.SourceProjection{}, err
		}
	}
	for _, service := range document.state.services {
		for _, method := range service.methods {
			fullName := document.state.protoPackage + "." + service.name + "." + method.name
			compiled, present := baseline.Method(fullName)
			if !present || !method.firstSource.Valid() {
				return protocol.SourceProjection{}, fmt.Errorf("generated Proto method %q is unavailable", fullName)
			}
			operationID, err := sourcecomment.CanonicalRPCOperationID(document.state.serviceID, fullName)
			if err != nil {
				return protocol.SourceProjection{}, err
			}
			downstream, err := sourcecomment.ParseSourceRef("proto://" + source + "#" + fullName)
			if err != nil {
				return protocol.SourceProjection{}, err
			}
			result.Nodes = append(result.Nodes, sourcecomment.ProjectionExpectation{
				Downstream: downstream, Upstream: method.firstSource, SemanticID: operationID, Kind: sourcecomment.NodeRPC,
				ExpectedNativeCanonical: compiled.CanonicalSourceJSON(),
			})
		}
	}
	lock, err := sourcecomment.NewProjectionLock(result.Nodes, result.InheritedFacts)
	if err != nil {
		return protocol.SourceProjection{}, fmt.Errorf("CRUD Proto projection lock is invalid: %w", err)
	}
	result.Lock = &lock
	return result, nil
}
