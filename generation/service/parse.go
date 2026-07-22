package service

import (
	"bytes"
	"errors"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

type manifestDocument struct {
	APIVersion      string            `json:"apiVersion"`
	Kind            string            `json:"kind"`
	ServiceID       string            `json:"serviceId"`
	ServiceKind     string            `json:"serviceKind"`
	ModulePath      string            `json:"modulePath"`
	ContractSources []canonicalSource `json:"contractSources"`
	ContractDigest  string            `json:"contractDigest"`
}

func Parse(source string, data []byte) (Manifest, error) {
	if _, err := provenance.RepositoryRef(source, "service-manifest"); err != nil {
		return Manifest{}, sourceInvalid("source_identity_invalid", "", "service manifest source is invalid")
	}
	document, err := strictdoc.ParseJSON(source, data)
	if err != nil {
		return Manifest{}, projectDocumentError(err)
	}
	var wire manifestDocument
	if err := document.Decode(&wire); err != nil {
		return Manifest{}, projectDocumentError(err)
	}
	if wire.APIVersion != APIVersion {
		return Manifest{}, sourceError(source, "version_unsupported", "/apiVersion", "service manifest version is not supported")
	}
	if wire.Kind != Kind {
		return Manifest{}, sourceError(source, "kind_invalid", "/kind", "service manifest kind is invalid")
	}
	sources := make([]provenance.Source, len(wire.ContractSources))
	for index, value := range wire.ContractSources {
		ref, refErr := provenance.ParseSourceRef(value.Ref)
		if refErr != nil {
			return Manifest{}, sourceError(source, "contract_source_ref_invalid", "/contractSources/"+jsonIndex(index)+"/ref", "contract source reference is invalid")
		}
		digest, digestErr := provenance.ParseDigest(value.Digest)
		if digestErr != nil {
			return Manifest{}, sourceError(source, "contract_source_digest_invalid", "/contractSources/"+jsonIndex(index)+"/digest", "contract source digest is invalid")
		}
		sources[index] = provenance.Source{Ref: ref, Digest: digest}
	}
	digest, err := provenance.ParseDigest(wire.ContractDigest)
	if err != nil {
		return Manifest{}, sourceError(source, "contract_digest_invalid", "/contractDigest", "contract digest is invalid")
	}
	manifest, err := New(Spec{ServiceID: wire.ServiceID, ServiceKind: wire.ServiceKind, ModulePath: wire.ModulePath, ContractSources: sources, ContractDigest: digest})
	if err != nil {
		return Manifest{}, withSource(err, source)
	}
	canonical, err := CanonicalJSON(manifest)
	if err != nil {
		return Manifest{}, withSource(err, source)
	}
	if !bytes.Equal(data, canonical) {
		return Manifest{}, sourceError(source, "document_noncanonical", "", "service manifest must use canonical JSON")
	}
	return manifest, nil
}

func projectDocumentError(err error) error {
	var documentError *strictdoc.Error
	if !errors.As(err, &documentError) {
		return invalid("document_invalid", "", "service manifest document is invalid")
	}
	return &Error{code: "service_manifest_invalid", reason: documentError.Code, source: documentError.Source, pointer: documentError.Pointer, line: documentError.Line, column: documentError.Column, message: "service manifest document is invalid"}
}
