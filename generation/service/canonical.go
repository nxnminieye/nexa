package service

import "encoding/json"

type canonicalManifest struct {
	APIVersion      string            `json:"apiVersion"`
	Kind            string            `json:"kind"`
	ServiceID       string            `json:"serviceId"`
	ServiceKind     string            `json:"serviceKind"`
	ModulePath      string            `json:"modulePath"`
	ContractSources []canonicalSource `json:"contractSources"`
	ContractDigest  string            `json:"contractDigest"`
}

func CanonicalJSON(manifest Manifest) ([]byte, error) {
	validated, err := New(Spec{
		ServiceID: manifest.serviceID, ServiceKind: manifest.serviceKind, ModulePath: manifest.modulePath,
		ContractSources: manifest.contractSources, ContractDigest: manifest.contractDigest,
	})
	if err != nil {
		return nil, err
	}
	sources := make([]canonicalSource, len(validated.contractSources))
	for index, source := range validated.contractSources {
		sources[index] = canonicalSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	data, err := json.Marshal(canonicalManifest{
		APIVersion: APIVersion, Kind: Kind, ServiceID: validated.serviceID, ServiceKind: validated.serviceKind,
		ModulePath: validated.modulePath, ContractSources: sources, ContractDigest: validated.contractDigest.String(),
	})
	if err != nil {
		return nil, invalid("document_invalid", "", "service manifest cannot be encoded")
	}
	return append(data, '\n'), nil
}
