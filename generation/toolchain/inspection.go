package toolchain

import (
	"context"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/provenance"
)

type OptionalDigest = entexec.OptionalDigest

type EntityInputInspectionSpec struct {
	RepositoryRoot            string
	SchemaDir                 provenance.DomainSource
	BuildTags                 []string
	ExpectedModuleGraphDigest OptionalDigest
	ExpectedBuildInputDigest  OptionalDigest
}

type EntityInputInspection struct {
	value entexec.Inspection
	valid bool
}

func InspectEntityInputs(ctx context.Context, spec EntityInputInspectionSpec) (EntityInputInspection, error) {
	value, err := entexec.Inspect(ctx, entexec.Spec{
		RepositoryRoot: spec.RepositoryRoot, SchemaDir: spec.SchemaDir, BuildTags: append([]string(nil), spec.BuildTags...),
		ExpectedModuleGraphDigest: spec.ExpectedModuleGraphDigest, ExpectedBuildInputDigest: spec.ExpectedBuildInputDigest,
	})
	if err != nil {
		return EntityInputInspection{}, projectEntExecError(err)
	}
	return EntityInputInspection{value: value, valid: true}, nil
}

func (i EntityInputInspection) ModuleGraphDigest() (provenance.Digest, error) {
	if !i.valid {
		return provenance.Digest{}, newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	value, err := i.value.ModuleGraphDigest()
	return value, projectEntExecError(err)
}

func (i EntityInputInspection) BuildInputDigest() (provenance.Digest, error) {
	if !i.valid {
		return provenance.Digest{}, newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	value, err := i.value.BuildInputDigest()
	return value, projectEntExecError(err)
}

func (i EntityInputInspection) ModuleSources() ([]provenance.Source, error) {
	if !i.valid {
		return nil, newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	value, err := i.value.ModuleSources()
	return value, projectEntExecError(err)
}

func (i EntityInputInspection) ExecutableVersion() (string, error) {
	if !i.valid {
		return "", newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	value, err := i.value.ExecutableVersion()
	return value, projectEntExecError(err)
}
