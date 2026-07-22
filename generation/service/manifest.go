// Package service defines the immutable manifest of one consumer-owned service contract.
package service

import (
	"encoding/json"
	"regexp"
	"sort"

	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/module"
)

const APIVersion = "nexa.dev/service-manifest/v1"
const Kind = "ServiceManifest"

const contractSourceSetAPIVersion = "nexa.dev/service-contract-source-set/v1"

var (
	serviceIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	serviceKindPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

type Spec struct {
	ServiceID       string
	ServiceKind     string
	ModulePath      string
	ContractSources []provenance.Source
	ContractDigest  provenance.Digest
}

type Manifest struct {
	serviceID       string
	serviceKind     string
	modulePath      string
	contractSources []provenance.Source
	contractDigest  provenance.Digest
}

func New(spec Spec) (Manifest, error) {
	if !serviceIDPattern.MatchString(spec.ServiceID) {
		return Manifest{}, invalid("service_id_invalid", "/serviceId", "service id is invalid")
	}
	if !serviceKindPattern.MatchString(spec.ServiceKind) {
		return Manifest{}, invalid("service_kind_invalid", "/serviceKind", "service kind is invalid")
	}
	if module.CheckPath(spec.ModulePath) != nil {
		return Manifest{}, invalid("module_path_invalid", "/modulePath", "module path is invalid")
	}
	sources, err := validateSources(spec.ContractSources)
	if err != nil {
		return Manifest{}, err
	}
	if _, err := provenance.ParseDigest(spec.ContractDigest.String()); err != nil {
		return Manifest{}, invalid("contract_digest_invalid", "/contractDigest", "contract digest is invalid")
	}
	computed, err := computeContractDigestUnchecked(sources)
	if err != nil {
		return Manifest{}, err
	}
	if spec.ContractDigest != computed {
		return Manifest{}, digestMismatch("contract_digest_mismatch", "/contractDigest", "contract digest does not match contract sources")
	}
	return Manifest{
		serviceID: spec.ServiceID, serviceKind: spec.ServiceKind, modulePath: spec.ModulePath,
		contractSources: sources, contractDigest: spec.ContractDigest,
	}, nil
}

func (m Manifest) APIVersion() string  { return APIVersion }
func (m Manifest) ServiceID() string   { return m.serviceID }
func (m Manifest) ServiceKind() string { return m.serviceKind }
func (m Manifest) ModulePath() string  { return m.modulePath }
func (m Manifest) ContractSources() []provenance.Source {
	return append([]provenance.Source(nil), m.contractSources...)
}
func (m Manifest) ContractDigest() provenance.Digest { return m.contractDigest }

func validateSources(input []provenance.Source) ([]provenance.Source, error) {
	if len(input) == 0 {
		return nil, invalid("contract_sources_missing", "/contractSources", "contract sources are required")
	}
	result := append([]provenance.Source(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i].Ref.String() < result[j].Ref.String() })
	for index, source := range result {
		pointer := "/contractSources/" + jsonIndex(index)
		ref, refErr := provenance.ParseSourceRef(source.Ref.String())
		digest, digestErr := provenance.ParseDigest(source.Digest.String())
		if refErr != nil || ref != source.Ref {
			return nil, invalid("contract_source_ref_invalid", pointer+"/ref", "contract source reference is invalid")
		}
		if digestErr != nil || digest != source.Digest {
			return nil, invalid("contract_source_digest_invalid", pointer+"/digest", "contract source digest is invalid")
		}
		if index > 0 && result[index-1].Ref == source.Ref {
			return nil, invalid("contract_source_duplicate", pointer+"/ref", "contract source is duplicated")
		}
	}
	return result, nil
}

type canonicalSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

func ComputeContractDigest(sources []provenance.Source) (provenance.Digest, error) {
	validated, err := validateSources(sources)
	if err != nil {
		return provenance.Digest{}, err
	}
	return computeContractDigestUnchecked(validated)
}

func computeContractDigestUnchecked(sources []provenance.Source) (provenance.Digest, error) {
	values := make([]canonicalSource, len(sources))
	for index, source := range sources {
		values[index] = canonicalSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	data, err := json.Marshal(struct {
		APIVersion string            `json:"apiVersion"`
		Sources    []canonicalSource `json:"sources"`
	}{APIVersion: contractSourceSetAPIVersion, Sources: values})
	if err != nil {
		return provenance.Digest{}, invalid("document_invalid", "", "contract source set cannot be encoded")
	}
	return provenance.SHA256(data), nil
}
