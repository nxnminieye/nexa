// Package crudlogic renders and validates runnable go-zero CRUD logic.
package crudlogic

import (
	"fmt"

	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	transactionGeneratorID = "crud-proto"
	artifactIDPrefix       = "crud-logic"
	generatorOwner         = "nexa.dev/generator/crud-logic/v1"
)

type ServiceLayout struct {
	ServiceID    string
	EntSchemaDir string
	LogicRoot    string
}

type BuildOptions struct{ OverwriteExisting bool }

type ValidationInput struct {
	RepositoryRoot string
	StagingRoot    string
	RPCGoTool      toolchain.Tool
	GoTool         toolchain.Tool
	Runner         toolchain.Runner
	Environment    []toolchain.EnvVar
}

type Plan struct{ state *planState }
type ValidatedPlan struct{ state *validatedPlanState }

type candidate struct {
	id, path, owner string
	content         []byte
	digest          provenance.Digest
	sources         []provenance.SourceRef
	manual          bool
	overwrite       bool
}

type protoGoNameSet struct {
	messages map[string]protoMessageGoNames
	enums    map[string]protoEnumGoNames
}

type protoMessageGoNames struct {
	goName string
	fields map[string]protoFieldGoNames
}

type protoFieldGoNames struct {
	goName   string
	enumName string
}

type protoEnumGoNames struct {
	goName string
	values map[string]string
}

func (n protoGoNameSet) message(name string) (protoMessageGoNames, bool) {
	value, ok := n.messages[name]
	return value, ok
}

func (n protoGoNameSet) field(message, field string) (protoFieldGoNames, bool) {
	owner, ok := n.message(message)
	if !ok {
		return protoFieldGoNames{}, false
	}
	value, ok := owner.fields[field]
	return value, ok
}

func (n protoGoNameSet) enum(name string) (protoEnumGoNames, bool) {
	value, ok := n.enums[name]
	return value, ok
}

func (p *planState) protoMessageName(name string) string {
	if p == nil {
		panic("crudlogic: protobuf name plan is nil")
	}
	value, ok := p.protoNames.message(name)
	if !ok || value.goName == "" {
		panic(fmt.Sprintf("crudlogic: protobuf message name missing for %q", name))
	}
	return value.goName
}

func (p *planState) protoFieldName(message, field string) string {
	if p == nil {
		panic("crudlogic: protobuf name plan is nil")
	}
	value, ok := p.protoNames.field(message, field)
	if !ok || value.goName == "" {
		panic(fmt.Sprintf("crudlogic: protobuf field name missing for %q.%q", message, field))
	}
	return value.goName
}

func (p *planState) protoEnumName(message, field string) string {
	if p == nil {
		panic("crudlogic: protobuf name plan is nil")
	}
	value, ok := p.protoNames.field(message, field)
	if !ok || value.enumName == "" {
		panic(fmt.Sprintf("crudlogic: protobuf enum field missing for %q.%q", message, field))
	}
	enum, ok := p.protoNames.enum(value.enumName)
	if !ok || enum.goName == "" {
		panic(fmt.Sprintf("crudlogic: protobuf enum name missing for %q.%q", message, field))
	}
	return enum.goName
}

func (p *planState) protoEnumProtoName(message, field string) string {
	if p == nil {
		panic("crudlogic: protobuf name plan is nil")
	}
	value, ok := p.protoNames.field(message, field)
	if !ok || value.enumName == "" {
		panic(fmt.Sprintf("crudlogic: protobuf enum field missing for %q.%q", message, field))
	}
	return value.enumName
}

func (p *planState) protoEnumValueName(message, field, value string) string {
	if p == nil {
		panic("crudlogic: protobuf name plan is nil")
	}
	resolvedField, ok := p.protoNames.field(message, field)
	if !ok || resolvedField.enumName == "" {
		panic(fmt.Sprintf("crudlogic: protobuf enum field missing for %q.%q", message, field))
	}
	enum, ok := p.protoNames.enum(resolvedField.enumName)
	if !ok {
		panic(fmt.Sprintf("crudlogic: protobuf enum name missing for %q.%q", message, field))
	}
	resolved, ok := enum.values[value]
	if !ok || resolved == "" {
		panic(fmt.Sprintf("crudlogic: protobuf enum value missing for %q.%q.%q", message, field, value))
	}
	return resolved
}

type planState struct {
	layout         ServiceLayout
	serviceRoot    string
	serviceImport  string
	pbImport       string
	pbPackage      string
	digest         provenance.Digest
	verifiedDigest provenance.Digest
	protoPath      string
	protoContent   []byte
	protoDigest    provenance.Digest
	protoNames     protoGoNameSet
	entitySnapshot entity.Snapshot
	crudSnapshot   crudproto.Snapshot
	candidates     []candidate
}

type validatedPlanState struct {
	plan             *planState
	digest           provenance.Digest
	transactionInput []transaction.ArtifactInput
}

type validatedFile struct {
	Path   string
	Digest provenance.Digest
}

type validationCanonicalInput struct {
	PlanDigest       provenance.Digest
	RPCGoTool        toolchain.Tool
	GoTool           toolchain.Tool
	Environment      []validationEnvironment
	CandidateDigests []provenance.Digest
	WiringDigest     provenance.Digest
	ReadFiles        []validatedFile
}

type validationEnvironment struct {
	Name   string
	Source toolchain.EnvironmentValueSource
	Value  string
}

func (p Plan) Digest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.digest
}

func (p ValidatedPlan) ValidationDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.digest
}

func (p ValidatedPlan) TransactionInputs(emit func(string, []byte) error) ([]transaction.ArtifactInput, error) {
	if p.state == nil || emit == nil {
		return nil, invalid("validated_plan_invalid", "/plan", nil)
	}
	result := append([]transaction.ArtifactInput(nil), p.state.transactionInput...)
	for index, input := range result {
		candidate := p.state.plan.candidates[index]
		if err := emit(input.Path, candidate.content); err != nil {
			return nil, invalid("candidate_emit_failed", "/artifacts", err)
		}
		input.Sources = append([]provenance.SourceRef(nil), input.Sources...)
		result[index] = input
	}
	return result, nil
}

func (p ValidatedPlan) StaleOwnershipProbes() ([]transaction.OwnershipProbe, error) {
	if p.state == nil || p.state.plan == nil {
		return nil, invalid("validated_plan_invalid", "/plan", nil)
	}
	if _, err := validateServiceLayout(p.state.plan.layout); err != nil {
		return nil, invalid("validated_plan_invalid", "/plan", err)
	}
	id, artifactPath := tenantHelperIdentity(p.state.plan.layout)
	return []transaction.OwnershipProbe{tenantHelperOwnershipProbe{id: id, path: artifactPath}}, nil
}
