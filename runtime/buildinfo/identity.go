package buildinfo

import "regexp"

const APIVersion = "nexa.dev/build-info/v1"
const Kind = "BuildInfo"

const lowerIdentifier = `^[a-z][a-z0-9]*(?:[-.][a-z0-9]+)*$`
const contractIdentifier = `^[A-Za-z][A-Za-z0-9]*(?:[-.][A-Za-z0-9]+)*$`

type Identity struct {
	service         string
	kind            string
	contractVersion string
}

func NewIdentity(service, kind, contractVersion string) (Identity, error) {
	identity := Identity{service: service, kind: kind, contractVersion: contractVersion}
	if err := validateIdentity(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (i Identity) Service() string         { return i.service }
func (i Identity) Kind() string            { return i.kind }
func (i Identity) ContractVersion() string { return i.contractVersion }

func validateIdentity(identity Identity) *Error {
	if matched, _ := regexp.MatchString(lowerIdentifier, identity.service); !matched {
		return invalid("identity_service_invalid", "/service", "build identity service is invalid")
	}
	if matched, _ := regexp.MatchString(lowerIdentifier, identity.kind); !matched {
		return invalid("identity_kind_invalid", "/serviceKind", "build identity kind is invalid")
	}
	if matched, _ := regexp.MatchString(contractIdentifier, identity.contractVersion); !matched {
		return invalid("identity_contract_version_invalid", "/contractVersion", "build identity contract version is invalid")
	}
	return nil
}
