// Package crudbuild owns the immutable CRUD protocol value model and build kernels.
package crudbuild

import (
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	APIVersion     = "nexa.dev/crud-protocol-ir/v2"
	Kind           = "CRUDProtocolIR"
	LockAPIVersion = "nexa.dev/crud-protocol-lock/v1"
	LockKind       = "CRUDProtocolCompatibilityLock"
)

type MultiTenantConfig struct{ Enabled bool }

type Spec struct {
	ServiceID          string
	ProtoPackage       string
	GoPackage          string
	ProtoArtifactPath  string
	LockPath           string
	RequestDigest      provenance.Digest
	ExistingLock       *Lock
	ExistingLockSource *provenance.Source
	PublishedArtifact  *PublishedArtifact
	MultiTenant        MultiTenantConfig
}

type PublishedArtifact struct {
	ID             string
	Digest         provenance.Digest
	ManifestSource provenance.Source
}

type Document struct{ state *documentState }
type Message struct{ state *messageState }
type Field struct{ state *fieldState }
type Enum struct{ state *enumState }
type EnumValue struct{ state *enumValueState }
type Service struct{ state *serviceState }
type Method struct{ state *methodState }
type Snapshot struct{ state *snapshotState }

type Lock struct{ state *lockState }
type LockSchema struct{ state *lockSchemaState }
type LockMessage struct{ state *lockMessageState }
type LockProposal struct{ state *lockProposalState }

type documentState struct {
	serviceID, protoPackage, goPackage string
	imports                            []string
	enums                              []*enumState
	messages                           []*messageState
	services                           []*serviceState
	tenantEntityIDs                    []string
	sources                            []provenance.Source
	canonical                          []byte
}

type messageState struct {
	id, name        string
	firstSource     sourcecomment.SourceRef
	fields          []*fieldState
	reservedNames   []string
	reservedNumbers []int32
}

type fieldState struct {
	id, name, wireType string
	number             int32
	repeated, optional bool
	source             provenance.Source
	firstSource        sourcecomment.SourceRef
}

type enumState struct {
	id, name        string
	values          []*enumValueState
	reservedNames   []string
	reservedNumbers []int32
}

type enumValueState struct {
	id, name, semantic string
	number             int32
}

type serviceState struct {
	id, name    string
	firstSource sourcecomment.SourceRef
	methods     []*methodState
}

type methodState struct {
	id, name, input, output string
	firstSource             sourcecomment.SourceRef
}

type snapshotState struct {
	document  *documentState
	canonical []byte
}

type assignmentState struct {
	fieldID, wireName, wireType string
	number                      int32
	source                      provenance.Source
}

type lockMessageState struct {
	id               string
	active           bool
	current, retired []*assignmentState
	reservedNames    []string
	reservedNumbers  []int32
}

type lockSchemaState struct {
	id       string
	enums    []*lockEnumState
	messages []*lockMessageState
}

type enumAssignmentState struct {
	valueID, wireName, semantic string
	number                      int32
}

type lockEnumState struct {
	id               string
	active           bool
	current, retired []*enumAssignmentState
	reservedNames    []string
	reservedNumbers  []int32
}

type lockState struct {
	serviceID string
	schemas   []*lockSchemaState
	canonical []byte
}

type lockProposalState struct {
	before  *lockState
	after   *lockState
	digest  provenance.Digest
	changed bool
}

func (d Document) Valid() bool { return d.state != nil }
func (d Document) APIVersion() string {
	if d.state == nil {
		return ""
	}
	return APIVersion
}
func (d Document) ServiceID() string {
	if d.state == nil {
		return ""
	}
	return d.state.serviceID
}
func (d Document) ProtoPackage() string {
	if d.state == nil {
		return ""
	}
	return d.state.protoPackage
}
func (d Document) GoPackage() string {
	if d.state == nil {
		return ""
	}
	return d.state.goPackage
}
func (d Document) Imports() []string {
	if d.state == nil {
		return nil
	}
	return append([]string(nil), d.state.imports...)
}
func (d Document) CanonicalJSON() []byte {
	if d.state == nil {
		return nil
	}
	return append([]byte(nil), d.state.canonical...)
}
func (d Document) Sources() []provenance.Source {
	if d.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), d.state.sources...)
}
func (d Document) Messages() []Message {
	if d.state == nil {
		return nil
	}
	result := make([]Message, len(d.state.messages))
	for i, value := range d.state.messages {
		result[i] = Message{state: value}
	}
	return result
}
func (d Document) Message(name string) (Message, bool) {
	if d.state == nil {
		return Message{}, false
	}
	for _, value := range d.state.messages {
		if value.name == name {
			return Message{state: value}, true
		}
	}
	return Message{}, false
}
func (d Document) Enums() []Enum {
	if d.state == nil {
		return nil
	}
	result := make([]Enum, len(d.state.enums))
	for i, value := range d.state.enums {
		result[i] = Enum{state: value}
	}
	return result
}
func (d Document) Services() []Service {
	if d.state == nil {
		return nil
	}
	result := make([]Service, len(d.state.services))
	for i, value := range d.state.services {
		result[i] = Service{state: value}
	}
	return result
}
func (d Document) TenantEntityIDs() []string {
	if d.state == nil {
		return nil
	}
	return append([]string(nil), d.state.tenantEntityIDs...)
}
func (d Document) HasTenantEntities() bool {
	return d.state != nil && len(d.state.tenantEntityIDs) != 0
}

func (m Message) ID() string {
	if m.state == nil {
		return ""
	}
	return m.state.id
}
func (m Message) Name() string {
	if m.state == nil {
		return ""
	}
	return m.state.name
}
func (m Message) Fields() []Field {
	if m.state == nil {
		return nil
	}
	result := make([]Field, len(m.state.fields))
	for i, value := range m.state.fields {
		result[i] = Field{state: value}
	}
	return result
}
func (m Message) ReservedNames() []string {
	if m.state == nil {
		return nil
	}
	return append([]string(nil), m.state.reservedNames...)
}
func (m Message) ReservedNumbers() []int32 {
	if m.state == nil {
		return nil
	}
	return append([]int32(nil), m.state.reservedNumbers...)
}

func (f Field) ID() string {
	if f.state == nil {
		return ""
	}
	return f.state.id
}
func (f Field) Name() string {
	if f.state == nil {
		return ""
	}
	return f.state.name
}
func (f Field) Type() string {
	if f.state == nil {
		return ""
	}
	return f.state.wireType
}
func (f Field) Number() int32 {
	if f.state == nil {
		return 0
	}
	return f.state.number
}
func (f Field) Repeated() bool { return f.state != nil && f.state.repeated }
func (f Field) Optional() bool { return f.state != nil && f.state.optional }
func (f Field) Source() provenance.Source {
	if f.state == nil {
		return provenance.Source{}
	}
	return f.state.source
}

func (e Enum) ID() string {
	if e.state == nil {
		return ""
	}
	return e.state.id
}
func (e Enum) Name() string {
	if e.state == nil {
		return ""
	}
	return e.state.name
}
func (e Enum) Values() []EnumValue {
	if e.state == nil {
		return nil
	}
	result := make([]EnumValue, len(e.state.values))
	for i, value := range e.state.values {
		result[i] = EnumValue{state: value}
	}
	return result
}
func (e Enum) ReservedNames() []string {
	if e.state == nil {
		return nil
	}
	return append([]string(nil), e.state.reservedNames...)
}
func (e Enum) ReservedNumbers() []int32 {
	if e.state == nil {
		return nil
	}
	return append([]int32(nil), e.state.reservedNumbers...)
}
func (v EnumValue) Name() string {
	if v.state == nil {
		return ""
	}
	return v.state.name
}
func (v EnumValue) Number() int32 {
	if v.state == nil {
		return 0
	}
	return v.state.number
}

func (s Service) ID() string {
	if s.state == nil {
		return ""
	}
	return s.state.id
}
func (s Service) Name() string {
	if s.state == nil {
		return ""
	}
	return s.state.name
}
func (s Service) Methods() []Method {
	if s.state == nil {
		return nil
	}
	result := make([]Method, len(s.state.methods))
	for i, value := range s.state.methods {
		result[i] = Method{state: value}
	}
	return result
}
func (m Method) ID() string {
	if m.state == nil {
		return ""
	}
	return m.state.id
}
func (m Method) Name() string {
	if m.state == nil {
		return ""
	}
	return m.state.name
}
func (m Method) Input() string {
	if m.state == nil {
		return ""
	}
	return m.state.input
}
func (m Method) Output() string {
	if m.state == nil {
		return ""
	}
	return m.state.output
}
func (s Snapshot) Valid() bool { return s.state != nil }
func (s Snapshot) CanonicalJSON() []byte {
	if s.state == nil {
		return nil
	}
	return append([]byte(nil), s.state.canonical...)
}
func (s Snapshot) Messages() []Message {
	if s.state == nil {
		return nil
	}
	return Document{state: s.state.document}.Messages()
}
func (s Snapshot) Services() []Service {
	if s.state == nil {
		return nil
	}
	return Document{state: s.state.document}.Services()
}
func (s Snapshot) TenantEntityIDs() []string {
	if s.state == nil {
		return nil
	}
	return Document{state: s.state.document}.TenantEntityIDs()
}
func (s Snapshot) HasTenantEntities() bool {
	return s.state != nil && len(s.state.document.tenantEntityIDs) != 0
}

func (l Lock) Valid() bool { return l.state != nil }
func (l Lock) APIVersion() string {
	if l.state == nil {
		return ""
	}
	return LockAPIVersion
}
func (l Lock) ServiceID() string {
	if l.state == nil {
		return ""
	}
	return l.state.serviceID
}
func (l Lock) CanonicalJSON() []byte {
	if l.state == nil {
		return nil
	}
	return append([]byte(nil), l.state.canonical...)
}
func (l Lock) Schemas() []LockSchema {
	if l.state == nil {
		return nil
	}
	result := make([]LockSchema, len(l.state.schemas))
	for i, value := range l.state.schemas {
		result[i] = LockSchema{state: value}
	}
	return result
}
func (l Lock) Message(id string) LockMessage {
	if l.state == nil {
		return LockMessage{}
	}
	for _, schema := range l.state.schemas {
		for _, message := range schema.messages {
			if message.id == id {
				return LockMessage{state: message}
			}
		}
	}
	return LockMessage{}
}
func (s LockSchema) ID() string {
	if s.state == nil {
		return ""
	}
	return s.state.id
}
func (s LockSchema) Messages() []LockMessage {
	if s.state == nil {
		return nil
	}
	result := make([]LockMessage, len(s.state.messages))
	for i, value := range s.state.messages {
		result[i] = LockMessage{state: value}
	}
	return result
}
func (m LockMessage) Valid() bool { return m.state != nil }
func (m LockMessage) ID() string {
	if m.state == nil {
		return ""
	}
	return m.state.id
}
func (m LockMessage) Active() bool { return m.state != nil && m.state.active }
func (m LockMessage) ReservedNames() []string {
	if m.state == nil {
		return nil
	}
	return append([]string(nil), m.state.reservedNames...)
}
func (m LockMessage) ReservedNumbers() []int32 {
	if m.state == nil {
		return nil
	}
	return append([]int32(nil), m.state.reservedNumbers...)
}

func (p LockProposal) Valid() bool { return p.state != nil }
func (p LockProposal) Before() *Lock {
	if p.state == nil || p.state.before == nil {
		return nil
	}
	value := Lock{state: p.state.before}
	return &value
}
func (p LockProposal) After() Lock {
	if p.state == nil {
		return Lock{}
	}
	return Lock{state: p.state.after}
}
func (p LockProposal) Digest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.digest
}
func (p LockProposal) Changed() bool { return p.state != nil && p.state.changed }
