// Package crudproto builds immutable CRUD protocol contracts from EntityIR.
package crudproto

import (
	"encoding/json"

	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	APIVersion     = crudbuild.APIVersion
	Kind           = crudbuild.Kind
	LockAPIVersion = crudbuild.LockAPIVersion
	LockKind       = crudbuild.LockKind
)

type BuildOptions struct {
	ServiceID    string
	ProtoPackage string
	GoPackage    string
	ExistingLock *Lock
	MultiTenant  MultiTenantConfig
}

type Document struct{ state crudbuild.Document }
type Message struct{ state crudbuild.Message }
type Field struct{ state crudbuild.Field }
type Enum struct{ state crudbuild.Enum }
type EnumValue struct{ state crudbuild.EnumValue }
type Service struct{ state crudbuild.Service }
type Method struct{ state crudbuild.Method }
type RPCContext struct{ state crudbuild.RPCContext }
type ContextBinding struct{ state crudbuild.ContextBinding }
type Snapshot struct{ state crudbuild.Snapshot }

type ContextValue = crudbuild.ContextValue

const ContextTenantID = crudbuild.ContextTenantID

type Lock struct{ state crudbuild.Lock }
type SchemaLock struct{ state crudbuild.LockSchema }
type LockMessage struct{ state crudbuild.LockMessage }
type LockProposal struct{ state crudbuild.LockProposal }

func (d Document) APIVersion() string           { return d.state.APIVersion() }
func (d Document) ServiceID() string            { return d.state.ServiceID() }
func (d Document) ProtoPackage() string         { return d.state.ProtoPackage() }
func (d Document) GoPackage() string            { return d.state.GoPackage() }
func (d Document) Imports() []string            { return d.state.Imports() }
func (d Document) Sources() []provenance.Source { return d.state.Sources() }
func (d Document) Messages() []Message {
	values := d.state.Messages()
	result := make([]Message, len(values))
	for i, value := range values {
		result[i] = Message{state: value}
	}
	return result
}
func (d Document) Message(name string) (Message, bool) {
	value, ok := d.state.Message(name)
	return Message{state: value}, ok
}
func (d Document) Enums() []Enum {
	values := d.state.Enums()
	result := make([]Enum, len(values))
	for i, value := range values {
		result[i] = Enum{state: value}
	}
	return result
}
func (d Document) Services() []Service {
	values := d.state.Services()
	result := make([]Service, len(values))
	for i, value := range values {
		result[i] = Service{state: value}
	}
	return result
}
func (d Document) TenantEntityIDs() []string { return d.state.TenantEntityIDs() }
func (d Document) HasTenantEntities() bool   { return d.state.HasTenantEntities() }

func (m Message) ID() string   { return m.state.ID() }
func (m Message) Name() string { return m.state.Name() }
func (m Message) Fields() []Field {
	values := m.state.Fields()
	result := make([]Field, len(values))
	for i, value := range values {
		result[i] = Field{state: value}
	}
	return result
}
func (m Message) ReservedNames() []string  { return m.state.ReservedNames() }
func (m Message) ReservedNumbers() []int32 { return m.state.ReservedNumbers() }

func (f Field) ID() string                { return f.state.ID() }
func (f Field) Name() string              { return f.state.Name() }
func (f Field) Type() string              { return f.state.Type() }
func (f Field) Number() int32             { return f.state.Number() }
func (f Field) Repeated() bool            { return f.state.Repeated() }
func (f Field) Optional() bool            { return f.state.Optional() }
func (f Field) Internal() bool            { return f.state.Internal() }
func (f Field) IsTenantContext() bool     { return f.state.IsTenantContext() }
func (f Field) Source() provenance.Source { return f.state.Source() }

func (e Enum) ID() string   { return e.state.ID() }
func (e Enum) Name() string { return e.state.Name() }
func (e Enum) Values() []EnumValue {
	values := e.state.Values()
	result := make([]EnumValue, len(values))
	for i, value := range values {
		result[i] = EnumValue{state: value}
	}
	return result
}
func (e Enum) ReservedNames() []string  { return e.state.ReservedNames() }
func (e Enum) ReservedNumbers() []int32 { return e.state.ReservedNumbers() }
func (v EnumValue) Name() string        { return v.state.Name() }
func (v EnumValue) Number() int32       { return v.state.Number() }

func (s Service) ID() string   { return s.state.ID() }
func (s Service) Name() string { return s.state.Name() }
func (s Service) Methods() []Method {
	values := s.state.Methods()
	result := make([]Method, len(values))
	for i, value := range values {
		result[i] = Method{state: value}
	}
	return result
}
func (m Method) ID() string             { return m.state.ID() }
func (m Method) Name() string           { return m.state.Name() }
func (m Method) Input() string          { return m.state.Input() }
func (m Method) Output() string         { return m.state.Output() }
func (m Method) RPCContext() RPCContext { return RPCContext{state: m.state.RPCContext()} }
func (c RPCContext) ContextFields() []ContextBinding {
	values := c.state.ContextFields()
	result := make([]ContextBinding, len(values))
	for i, value := range values {
		result[i] = ContextBinding{state: value}
	}
	return result
}
func (b ContextBinding) Source() ContextValue { return b.state.Source() }
func (b ContextBinding) RPCField() string     { return b.state.RPCField() }

func (s Snapshot) APIVersion() string {
	if !s.state.Valid() {
		return ""
	}
	return APIVersion
}
func (s Snapshot) Messages() []Message {
	values := s.state.Messages()
	result := make([]Message, len(values))
	for i, value := range values {
		result[i] = Message{state: value}
	}
	return result
}
func (s Snapshot) EnumNames() []string {
	var document struct {
		Enums []struct {
			Name string `json:"name"`
		} `json:"enums"`
	}
	if !s.state.Valid() || json.Unmarshal(s.state.CanonicalJSON(), &document) != nil {
		return nil
	}
	result := make([]string, len(document.Enums))
	for index, value := range document.Enums {
		result[index] = value.Name
	}
	return result
}
func (s Snapshot) Services() []Service {
	values := s.state.Services()
	result := make([]Service, len(values))
	for i, value := range values {
		result[i] = Service{state: value}
	}
	return result
}
func (s Snapshot) TenantEntityIDs() []string { return s.state.TenantEntityIDs() }
func (s Snapshot) HasTenantEntities() bool   { return s.state.HasTenantEntities() }

func (l Lock) APIVersion() string { return l.state.APIVersion() }
func (l Lock) ServiceID() string  { return l.state.ServiceID() }
func (l Lock) Schemas() []SchemaLock {
	values := l.state.Schemas()
	result := make([]SchemaLock, len(values))
	for i, value := range values {
		result[i] = SchemaLock{state: value}
	}
	return result
}
func (l Lock) Message(id string) LockMessage { return LockMessage{state: l.state.Message(id)} }
func (s SchemaLock) ID() string              { return s.state.ID() }
func (s SchemaLock) Messages() []LockMessage {
	values := s.state.Messages()
	result := make([]LockMessage, len(values))
	for i, value := range values {
		result[i] = LockMessage{state: value}
	}
	return result
}
func (m LockMessage) ID() string               { return m.state.ID() }
func (m LockMessage) Active() bool             { return m.state.Active() }
func (m LockMessage) ReservedNames() []string  { return m.state.ReservedNames() }
func (m LockMessage) ReservedNumbers() []int32 { return m.state.ReservedNumbers() }

func (p LockProposal) Before() *Lock {
	value := p.state.Before()
	if value == nil {
		return nil
	}
	return &Lock{state: *value}
}
func (p LockProposal) After() Lock               { return Lock{state: p.state.After()} }
func (p LockProposal) Digest() provenance.Digest { return p.state.Digest() }
func (p LockProposal) Changed() bool             { return p.state.Changed() }
