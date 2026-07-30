package sourcecomment

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

var lowerDotPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:_[a-z0-9]+)*)*$`)
var canonicalIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
var fieldIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var httpPathPattern = regexp.MustCompile(`^/(?:[a-z][a-z0-9-]*|\{[A-Za-z_][A-Za-z0-9_]*\})(?:/(?:[a-z][a-z0-9-]*|\{[A-Za-z_][A-Za-z0-9_]*\}))*$|^/$`)
var routePathPattern = regexp.MustCompile(`^/(?:[a-z][a-z0-9]*(?:-[a-z0-9]+)*)(?:/[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$|^/$`)
var routeNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:[._-][A-Za-z0-9]+)*$`)

type Consumer string

const (
	ConsumerEntity   Consumer = "entity"
	ConsumerCRUD     Consumer = "crud"
	ConsumerProtocol Consumer = "protocol"
	ConsumerHTTP     Consumer = "http"
	ConsumerFrontend Consumer = "frontend"
)

type entry struct {
	key       string
	kind      ValueKind
	element   ValueKind
	enums     map[string]struct{}
	enumOrder []string
	targets   map[NodeKind]struct{}
	earliest  Stage
	propagate bool
	consumer  Consumer
	security  bool
}

type Entry struct{ state *entry }

func (e Entry) Key() string {
	if e.state == nil {
		return ""
	}
	return e.state.key
}
func (e Entry) ValueKind() ValueKind {
	if e.state == nil {
		return ""
	}
	return e.state.kind
}
func (e Entry) EarliestStage() Stage {
	if e.state == nil {
		return ""
	}
	return e.state.earliest
}
func (e Entry) Propagates() bool { return e.state != nil && e.state.propagate }
func (e Entry) Consumer() Consumer {
	if e.state == nil {
		return ""
	}
	return e.state.consumer
}
func (e Entry) SecuritySensitive() bool { return e.state != nil && e.state.security }

type Registry struct{ entries map[string]*entry }

func StandardRegistry() Registry {
	display := []NodeKind{NodeSchema, NodeField, NodeMessage, NodeProtoField, NodeAPIType, NodeAPIField, NodePageField, NodeRPC, NodeAPIOperation, NodePage}
	fields := []NodeKind{NodeField, NodeProtoField, NodeAPIField, NodePageField}
	operations := []NodeKind{NodeRPC, NodeAPIOperation}
	values := []*entry{
		stringEntry("label.zh-CN", display, StageEnt, ConsumerFrontend, false), stringEntry("label.en-US", display, StageEnt, ConsumerFrontend, false),
		stringEntry("description.zh-CN", display, StageEnt, ConsumerFrontend, false), stringEntry("description.en-US", display, StageEnt, ConsumerFrontend, false),
		listEntry("crud.operations", []NodeKind{NodeSchema, NodeMessage, NodeAPIType}, StageEnt, ConsumerCRUD, "list", "get", "create", "update", "delete"),
		enumEntry("crud.read", fields, StageEnt, ConsumerCRUD, false, "include", "exclude"), enumEntry("crud.mutation", fields, StageEnt, ConsumerCRUD, false, "none", "create", "update", "create-update"),
		enumEntry("scope", []NodeKind{NodeSchema, NodeMessage, NodeAPIType}, StageEnt, ConsumerEntity, true, "global", "tenant"), enumEntry("visibility", fields, StageEnt, ConsumerEntity, true, "public", "internal", "sensitive"),
		enumEntry("auth", operations, StageProto, ConsumerHTTP, true, "required", "none"), stringEntry("permission", operations, StageProto, ConsumerHTTP, true),
		enumEntry("http.method", []NodeKind{NodeRPC}, StageProto, ConsumerHTTP, true, "GET", "POST", "PUT", "DELETE"), stringEntry("http.path", []NodeKind{NodeRPC}, StageProto, ConsumerHTTP, true),
		enumEntry("ui.control", fields, StageEnt, ConsumerFrontend, false, "text", "textarea", "number", "switch", "select", "multi-select", "datetime", "readonly", "sensitive", "member", "reference", "attachment", "tags", "component", "i18n", "iconify", "permission", "route", "scope", "http-method", "http-path", "module", "locale", "timezone"),
		objectEntry("ui.reference", fields, StageEnt, ConsumerFrontend),
		stringEntry("ui.entity", []NodeKind{NodePage}, StagePage, ConsumerFrontend, false), integerEntry("ui.pageSize", []NodeKind{NodePage}, StagePage, ConsumerFrontend), stringEntry("ui.extensionComponent", []NodeKind{NodePage}, StagePage, ConsumerFrontend, false),
		stringEntry("route.path", []NodeKind{NodePage}, StagePage, ConsumerFrontend, false), stringEntry("route.name", []NodeKind{NodePage}, StagePage, ConsumerFrontend, false), stringEntry("route.icon", []NodeKind{NodePage}, StagePage, ConsumerFrontend, false), integerEntry("menu.order", []NodeKind{NodePage}, StagePage, ConsumerFrontend),
	}
	result := Registry{entries: make(map[string]*entry, len(values))}
	for _, value := range values {
		result.entries[value.key] = value
	}
	return result
}

func (r Registry) Lookup(key string) (Entry, bool) {
	value, ok := r.lookup(key)
	return Entry{state: value}, ok
}
func (r Registry) Entries() []Entry {
	keys := make([]string, 0, len(r.entries))
	for key := range r.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Entry, len(keys))
	for index, key := range keys {
		result[index] = Entry{state: r.entries[key]}
	}
	return result
}
func (r Registry) lookup(key string) (*entry, bool) { value, ok := r.entries[key]; return value, ok }

func makeEntry(key string, kind, element ValueKind, targets []NodeKind, earliest Stage, consumer Consumer, security bool, enums ...string) *entry {
	value := &entry{key: key, kind: kind, element: element, targets: map[NodeKind]struct{}{}, earliest: earliest, propagate: true, consumer: consumer, security: security, enums: map[string]struct{}{}}
	for _, target := range targets {
		value.targets[target] = struct{}{}
	}
	for _, item := range enums {
		value.enums[item] = struct{}{}
		value.enumOrder = append(value.enumOrder, item)
	}
	return value
}
func stringEntry(key string, targets []NodeKind, earliest Stage, consumer Consumer, security bool) *entry {
	return makeEntry(key, ValueString, "", targets, earliest, consumer, security)
}
func integerEntry(key string, targets []NodeKind, earliest Stage, consumer Consumer) *entry {
	return makeEntry(key, ValueInteger, "", targets, earliest, consumer, false)
}
func objectEntry(key string, targets []NodeKind, earliest Stage, consumer Consumer) *entry {
	return makeEntry(key, ValueObject, "", targets, earliest, consumer, false)
}
func enumEntry(key string, targets []NodeKind, earliest Stage, consumer Consumer, security bool, values ...string) *entry {
	return makeEntry(key, ValueString, "", targets, earliest, consumer, security, values...)
}
func listEntry(key string, targets []NodeKind, earliest Stage, consumer Consumer, values ...string) *entry {
	return makeEntry(key, ValueList, ValueString, targets, earliest, consumer, false, values...)
}

func (e *entry) canonicalize(value Value) Value {
	result := cloneValue(value)
	if result.kind != ValueList || len(e.enumOrder) == 0 {
		return result
	}
	rank := make(map[string]int, len(e.enumOrder))
	for index, item := range e.enumOrder {
		rank[item] = index
	}
	sort.SliceStable(result.elements, func(i, j int) bool {
		left, _ := result.elements[i].String()
		right, _ := result.elements[j].String()
		return rank[left] < rank[right]
	})
	return result
}

func (e *entry) validate(target Target, value Value) (Code, string) {
	if _, ok := e.targets[target.Kind]; !ok || target.Stage.order() < e.earliest.order() || target.Stage == StageGenerated {
		return CodeInvalidTarget, "move the directive to a registered semantic node"
	}
	if (e.key == "crud.operations" || e.key == "scope") && target.Stage != StageEnt && target.Stage != StageProto && target.Stage != StageAPI {
		return CodeInvalidTarget, "declare this resource fact at Ent, Proto, or native API when no earlier source exists"
	}
	if (e.key == "http.method" || e.key == "http.path") && target.Stage != StageProto {
		return CodeInvalidTarget, "declare HTTP projection facts on a Proto RPC"
	}
	if (e.key == "auth" || e.key == "permission") && !((target.Kind == NodeRPC && target.Stage == StageProto) || (target.Kind == NodeAPIOperation && target.Stage == StageAPI)) {
		return CodeInvalidTarget, "declare operation security at the RPC or native API operation first stage"
	}
	if value.kind != e.kind {
		return CodeInvalidValue, "use the value type required by the fact registry"
	}
	if value.kind == ValueList {
		if len(value.elements) == 0 {
			return CodeInvalidValue, "use a non-empty list"
		}
		seen := map[string]bool{}
		for _, item := range value.elements {
			if item.kind != e.element {
				return CodeInvalidValue, "use a homogeneous list required by the fact registry"
			}
			text, _ := item.String()
			if seen[text] {
				return CodeInvalidValue, "remove duplicate list values"
			}
			seen[text] = true
			if _, ok := e.enums[text]; !ok {
				return CodeInvalidValue, "use a closed value from the fact registry"
			}
		}
	} else if len(e.enums) > 0 {
		text, _ := value.String()
		if _, ok := e.enums[text]; !ok {
			return CodeInvalidValue, "use a closed value from the fact registry"
		}
	}
	if value.kind == ValueString {
		text, _ := value.String()
		if strings.TrimSpace(text) == "" {
			return CodeInvalidValue, "use a non-empty string value"
		}
		switch e.key {
		case "permission":
			if !lowerDotPattern.MatchString(text) {
				return CodeInvalidValue, "use a lower-dot permission identifier with lower_snake_case segments"
			}
		case "http.path":
			if !httpPathPattern.MatchString(text) {
				return CodeInvalidValue, "use a canonical HTTP path without query or aliases"
			}
		case "ui.entity":
			if !canonicalIdentifierPattern.MatchString(text) {
				return CodeInvalidValue, "use a canonical schema or message reference"
			}
		case "ui.extensionComponent":
			if !validComponentID(text) {
				return CodeInvalidValue, "use a canonical repository-relative .vue component id"
			}
		case "route.path":
			if !routePathPattern.MatchString(text) {
				return CodeInvalidValue, "use an absolute lower-kebab route path"
			}
		case "route.name":
			if !routeNamePattern.MatchString(text) {
				return CodeInvalidValue, "use a canonical route identifier"
			}
		}
	}
	if value.kind == ValueObject {
		reference, _ := value.Reference()
		if !canonicalIdentifierPattern.MatchString(reference.Target) || !fieldIdentifierPattern.MatchString(reference.Display) {
			return CodeInvalidValue, "use exact canonical target and display identifiers"
		}
	}
	if value.kind == ValueInteger && e.key == "ui.pageSize" {
		number, _ := value.Integer()
		if number < 1 || number > 100 {
			return CodeInvalidValue, "use a page size from 1 through 100"
		}
	}
	return "", ""
}

func validComponentID(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.ContainsAny(value, "?#") || path.Clean(value) != value || !strings.HasSuffix(value, ".vue") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
