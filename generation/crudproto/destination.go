package crudproto

import (
	"path"
	"regexp"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

var destinationServiceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)

type ProtoDestination struct{ state *protoDestinationState }

type protoDestinationState struct {
	serviceID, entryPath, artifactPath, lockPath, artifactID, manifestPath string
}

func ProjectProtoDestination(serviceID, entryPath string) (ProtoDestination, error) {
	if !destinationServiceIDPattern.MatchString(serviceID) {
		return ProtoDestination{}, newHostError("project", "service_id_invalid", "/serviceId", "")
	}
	ref, err := provenance.RepositoryRef(entryPath, "")
	if err != nil || ref.Path() != entryPath || path.Ext(entryPath) != ".proto" || strings.HasSuffix(entryPath, ".crud.generated.proto") {
		return ProtoDestination{}, newHostError("project", "proto_destination_invalid", "/entryPath", "")
	}
	base := strings.TrimSuffix(entryPath, ".proto")
	return ProtoDestination{state: &protoDestinationState{
		serviceID: serviceID, entryPath: entryPath,
		artifactPath: base + ".crud.generated.proto",
		lockPath:     base + ".crud-protocol.lock.json",
		artifactID:   "crud-proto." + serviceID,
		manifestPath: ".nexa/generation/crud-proto." + serviceID + ".manifest.json",
	}}, nil
}

func (d ProtoDestination) ServiceID() string {
	if d.state == nil {
		return ""
	}
	return d.state.serviceID
}

func (d ProtoDestination) EntryPath() string {
	if d.state == nil {
		return ""
	}
	return d.state.entryPath
}

func (d ProtoDestination) ArtifactPath() string {
	if d.state == nil {
		return ""
	}
	return d.state.artifactPath
}

func (d ProtoDestination) LockPath() string {
	if d.state == nil {
		return ""
	}
	return d.state.lockPath
}

func (d ProtoDestination) ArtifactID() string {
	if d.state == nil {
		return ""
	}
	return d.state.artifactID
}

func (d ProtoDestination) ManifestPath() string {
	if d.state == nil {
		return ""
	}
	return d.state.manifestPath
}
