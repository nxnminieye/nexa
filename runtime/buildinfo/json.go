package buildinfo

import (
	_ "embed"
	"encoding/json"
	"time"
)

//go:embed build-info-v1.schema.json
var embeddedSchema string

func Schema() []byte { return []byte(embeddedSchema) }

type wireInfo struct {
	APIVersion      string `json:"apiVersion"`
	Kind            string `json:"kind"`
	Service         string `json:"service"`
	ServiceKind     string `json:"serviceKind"`
	ContractVersion string `json:"contractVersion"`
	Available       bool   `json:"available"`
	Commit          string `json:"commit"`
	Dirty           bool   `json:"dirty"`
	VCSTime         string `json:"vcsTime"`
	GoVersion       string `json:"goVersion"`
	ModulePath      string `json:"modulePath"`
	ModuleVersion   string `json:"moduleVersion"`
}

func (i Info) CanonicalJSON() ([]byte, error) {
	if err := validateInfo(i); err != nil {
		return nil, err
	}
	document := wireInfo{
		APIVersion: APIVersion, Kind: Kind,
		Service: i.identity.service, ServiceKind: i.identity.kind, ContractVersion: i.identity.contractVersion,
		Available: i.available, Commit: i.commit, Dirty: i.dirty, VCSTime: i.vcsTime,
		GoVersion: i.goVersion, ModulePath: i.modulePath, ModuleVersion: i.moduleVersion,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, invalid("info_state_invalid", "", "build info cannot be encoded")
	}
	return append(encoded, '\n'), nil
}

func validateInfo(info Info) *Error {
	if err := validateIdentity(info.identity); err != nil {
		return err
	}
	if info.available {
		if !validRevision(info.commit) {
			return invalid("info_state_invalid", "/commit", "build info state is invalid")
		}
	} else {
		if info.commit != "unknown" {
			return invalid("info_state_invalid", "/commit", "build info state is invalid")
		}
		if !info.dirty {
			return invalid("info_state_invalid", "/dirty", "build info state is invalid")
		}
	}
	if info.vcsTime != "" {
		parsed, err := time.Parse(time.RFC3339, info.vcsTime)
		if err != nil || info.vcsTime != parsed.UTC().Format(time.RFC3339Nano) {
			return invalid("vcs_time_invalid", "/vcsTime", "build VCS time is invalid")
		}
	}
	if !validProjectedText(info.goVersion) {
		return invalid("go_version_invalid", "/goVersion", "Go version is invalid")
	}
	if !validModulePath(info.modulePath) {
		return invalid("module_path_invalid", "/modulePath", "module path is invalid")
	}
	if !validModuleVersion(info.moduleVersion) {
		return invalid("module_version_invalid", "/moduleVersion", "module version is invalid")
	}
	return nil
}
