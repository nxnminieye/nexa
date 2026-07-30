// Package protocol compiles Proto sources and source-comment facts into immutable ProtocolIR values.
package protocol

import (
	"context"
	"io"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	APIVersion          = "nexa.dev/protocol-ir/v3"
	Kind                = "ProtocolIR"
	SourceSetAPIVersion = "nexa.dev/protocol-source-set/v1"
)

type CompileOptions struct {
	ServiceID        string
	EntryFiles       []string
	Resolver         Resolver
	SourceProjection *SourceProjection
}

// SourceProjection is compiler-produced input for extending an earlier
// validated FactGraph. It is not a Proto authoring surface.
type SourceProjection struct {
	Upstream       sourcecomment.FactGraph
	Nodes          []sourcecomment.ProjectionExpectation
	InheritedFacts []sourcecomment.InheritedFactExpectation
	Lock           *sourcecomment.ProjectionLock
}

type Resolver interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

type Cardinality string

const (
	CardinalitySingular Cardinality = "singular"
	CardinalityRepeated Cardinality = "repeated"
)

type Presence string

const (
	PresenceImplicit Presence = "implicit"
	PresenceExplicit Presence = "explicit"
	PresenceOneof    Presence = "oneof"
	PresenceMap      Presence = "map"
)

type TypeKind string

const (
	TypeScalar  TypeKind = "scalar"
	TypeEnum    TypeKind = "enum"
	TypeMessage TypeKind = "message"
	TypeMap     TypeKind = "map"
)

type Document struct{ state *documentState }
type File struct{ state *fileState }
type Message struct{ state *messageState }
type Field struct{ state *fieldState }
type Enum struct{ state *enumState }
type EnumValue struct{ state *enumValueState }
type Service struct{ state *serviceState }
type Method struct{ state *methodState }
type Type struct{ state *typeState }
type Location struct{ state *locationState }

type documentState struct {
	serviceID    string
	files        []*fileState
	messages     map[string]*messageState
	enums        map[string]*enumState
	services     map[string]*serviceState
	methods      map[string]*methodState
	sources      []provenance.Source
	sourceIndex  map[string]int
	sourceDigest provenance.Digest
	factGraph    sourcecomment.FactGraph
}
type fileState struct {
	path     string
	messages []*messageState
	enums    []*enumState
	services []*serviceState
}
type messageState struct {
	fullName, filePath string
	fields             []*fieldState
	location           locationState
	source             provenance.Source
	canonicalSource    []byte
}
type fieldState struct {
	fullName, filePath, name, jsonName, oneof string
	number                                    int
	cardinality                               Cardinality
	presence                                  Presence
	typeValue                                 *typeState
	location                                  locationState
	source                                    provenance.Source
	canonicalSource                           []byte
}
type enumState struct {
	fullName, filePath string
	values             []*enumValueState
	location           locationState
}
type enumValueState struct {
	name     string
	number   int
	location locationState
}
type serviceState struct {
	fullName, filePath string
	methods            []*methodState
	location           locationState
}
type methodState struct {
	fullName, filePath, name, input, output string
	clientStreaming, serverStreaming        bool
	location                                locationState
	source                                  provenance.Source
	canonicalSource                         []byte
}
type typeState struct {
	kind       TypeKind
	name       string
	key, value *typeState
}
type locationState struct {
	file         string
	line, column int
}

func (d Document) ServiceID() string {
	if d.state == nil {
		return ""
	}
	return d.state.serviceID
}
func (d Document) Files() []File {
	if d.state == nil {
		return nil
	}
	result := make([]File, len(d.state.files))
	for i, value := range d.state.files {
		result[i] = File{state: value}
	}
	return result
}
func (d Document) Message(fullName string) (Message, bool) {
	if d.state == nil {
		return Message{}, false
	}
	value, ok := d.state.messages[fullName]
	return Message{state: value}, ok
}
func (d Document) Enum(fullName string) (Enum, bool) {
	if d.state == nil {
		return Enum{}, false
	}
	value, ok := d.state.enums[fullName]
	return Enum{state: value}, ok
}
func (d Document) Service(fullName string) (Service, bool) {
	if d.state == nil {
		return Service{}, false
	}
	value, ok := d.state.services[fullName]
	return Service{state: value}, ok
}
func (d Document) Method(fullName string) (Method, bool) {
	if d.state == nil {
		return Method{}, false
	}
	value, ok := d.state.methods[fullName]
	return Method{state: value}, ok
}

func (d Document) FactGraph() sourcecomment.FactGraph {
	if d.state == nil {
		return sourcecomment.FactGraph{}
	}
	return d.state.factGraph
}

func (f File) Path() string {
	if f.state == nil {
		return ""
	}
	return f.state.path
}
func (f File) Messages() []Message {
	if f.state == nil {
		return nil
	}
	result := make([]Message, len(f.state.messages))
	for i, value := range f.state.messages {
		result[i] = Message{state: value}
	}
	return result
}
func (f File) Enums() []Enum {
	if f.state == nil {
		return nil
	}
	result := make([]Enum, len(f.state.enums))
	for i, value := range f.state.enums {
		result[i] = Enum{state: value}
	}
	return result
}
func (f File) Services() []Service {
	if f.state == nil {
		return nil
	}
	result := make([]Service, len(f.state.services))
	for i, value := range f.state.services {
		result[i] = Service{state: value}
	}
	return result
}

func (m Message) FullName() string {
	if m.state == nil {
		return ""
	}
	return m.state.fullName
}
func (m Message) FilePath() string {
	if m.state == nil {
		return ""
	}
	return m.state.filePath
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
func (m Message) Location() Location {
	if m.state == nil {
		return Location{}
	}
	return Location{state: &m.state.location}
}

func (f Field) FullName() string {
	if f.state == nil {
		return ""
	}
	return f.state.fullName
}
func (f Field) FilePath() string {
	if f.state == nil {
		return ""
	}
	return f.state.filePath
}
func (f Field) Name() string {
	if f.state == nil {
		return ""
	}
	return f.state.name
}
func (f Field) JSONName() string {
	if f.state == nil {
		return ""
	}
	return f.state.jsonName
}
func (f Field) Number() int {
	if f.state == nil {
		return 0
	}
	return f.state.number
}
func (f Field) Cardinality() Cardinality {
	if f.state == nil {
		return ""
	}
	return f.state.cardinality
}
func (f Field) Presence() Presence {
	if f.state == nil {
		return ""
	}
	return f.state.presence
}
func (f Field) Oneof() string {
	if f.state == nil {
		return ""
	}
	return f.state.oneof
}
func (f Field) Type() Type {
	if f.state == nil {
		return Type{}
	}
	return Type{state: f.state.typeValue}
}
func (f Field) Location() Location {
	if f.state == nil {
		return Location{}
	}
	return Location{state: &f.state.location}
}

func (e Enum) FullName() string {
	if e.state == nil {
		return ""
	}
	return e.state.fullName
}
func (e Enum) FilePath() string {
	if e.state == nil {
		return ""
	}
	return e.state.filePath
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
func (e Enum) Location() Location {
	if e.state == nil {
		return Location{}
	}
	return Location{state: &e.state.location}
}
func (v EnumValue) Name() string {
	if v.state == nil {
		return ""
	}
	return v.state.name
}
func (v EnumValue) Number() int {
	if v.state == nil {
		return 0
	}
	return v.state.number
}
func (v EnumValue) Location() Location {
	if v.state == nil {
		return Location{}
	}
	return Location{state: &v.state.location}
}

func (s Service) FullName() string {
	if s.state == nil {
		return ""
	}
	return s.state.fullName
}
func (s Service) FilePath() string {
	if s.state == nil {
		return ""
	}
	return s.state.filePath
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
func (s Service) Location() Location {
	if s.state == nil {
		return Location{}
	}
	return Location{state: &s.state.location}
}

func (m Method) FullName() string {
	if m.state == nil {
		return ""
	}
	return m.state.fullName
}
func (m Method) FilePath() string {
	if m.state == nil {
		return ""
	}
	return m.state.filePath
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
func (m Method) ClientStreaming() bool { return m.state != nil && m.state.clientStreaming }
func (m Method) ServerStreaming() bool { return m.state != nil && m.state.serverStreaming }
func (m Method) Location() Location {
	if m.state == nil {
		return Location{}
	}
	return Location{state: &m.state.location}
}

func (t Type) Kind() TypeKind {
	if t.state == nil {
		return ""
	}
	return t.state.kind
}
func (t Type) Name() string {
	if t.state == nil {
		return ""
	}
	return t.state.name
}
func (t Type) Key() Type {
	if t.state == nil {
		return Type{}
	}
	return Type{state: t.state.key}
}
func (t Type) Value() Type {
	if t.state == nil {
		return Type{}
	}
	return Type{state: t.state.value}
}
func (l Location) File() string {
	if l.state == nil {
		return ""
	}
	return l.state.file
}
func (l Location) Line() int {
	if l.state == nil {
		return 0
	}
	return l.state.line
}
func (l Location) Column() int {
	if l.state == nil {
		return 0
	}
	return l.state.column
}
