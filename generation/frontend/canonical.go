package frontend

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
)

type wireSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type wireOperation struct {
	ID              string               `json:"id"`
	Role            string               `json:"role"`
	OperationID     string               `json:"operationId"`
	Permission      string               `json:"permission"`
	RequestType     string               `json:"requestType"`
	ResponseType    string               `json:"responseType"`
	ContextBindings []wireContextBinding `json:"contextBindings"`
	Result          *resultDocument      `json:"result,omitempty"`
	Pagination      *paginationDocument  `json:"pagination,omitempty"`
}

type wireValueType struct {
	Kind    string         `json:"kind"`
	Name    string         `json:"name,omitempty"`
	Element *wireValueType `json:"element,omitempty"`
}
type wireContextBinding struct {
	Context   string        `json:"context"`
	Path      []string      `json:"path"`
	ValueType wireValueType `json:"valueType"`
}
type wireBinding struct {
	Operation string        `json:"operation"`
	Direction string        `json:"direction"`
	Path      []string      `json:"path"`
	ValueType wireValueType `json:"valueType"`
	Required  bool          `json:"required"`
}
type wireColumn struct {
	ID        string        `json:"id"`
	LabelKey  string        `json:"labelKey"`
	Path      []string      `json:"path"`
	ValueType wireValueType `json:"valueType"`
	Required  bool          `json:"required"`
}
type wireField struct {
	ID       string           `json:"id"`
	LabelKey string           `json:"labelKey"`
	Surfaces []string         `json:"surfaces"`
	Control  string           `json:"control,omitempty"`
	Bindings []wireBinding    `json:"bindings"`
	Options  *optionsDocument `json:"options,omitempty"`
	Choices  []choiceDocument `json:"choices,omitempty"`
	Columns  []wireColumn     `json:"columns,omitempty"`
}

type wireLocale struct {
	Locale    string            `json:"locale"`
	Messages  map[string]string `json:"messages"`
	SourceRef string            `json:"sourceRef"`
}

type wirePage struct {
	ID               string           `json:"id"`
	TitleKey         string           `json:"titleKey"`
	Mode             string           `json:"mode"`
	AccessOperation  string           `json:"accessOperation"`
	AccessPermission string           `json:"accessPermission"`
	Route            routeDocument    `json:"route"`
	Menu             *menuDocument    `json:"menu,omitempty"`
	Operations       []wireOperation  `json:"operations"`
	Fields           []wireField      `json:"fields"`
	Actions          []actionDocument `json:"actions"`
	ExtensionPoints  []string         `json:"extensionPoints"`
	SpecSourceRef    string           `json:"specSourceRef"`
}

type wireDocument struct {
	APIVersion   string          `json:"apiVersion"`
	Kind         string          `json:"kind"`
	APIDigest    string          `json:"apiDigest"`
	SourceDigest string          `json:"sourceDigest"`
	Sources      []wireSource    `json:"sources"`
	API          json.RawMessage `json:"api"`
	Locales      []wireLocale    `json:"locales"`
	Pages        []wirePage      `json:"pages"`
}

type sourceSetEnvelope struct {
	APIVersion string       `json:"apiVersion"`
	APIDigest  string       `json:"apiDigest"`
	Sources    []wireSource `json:"sources"`
}

func CanonicalJSON(document Document) ([]byte, error) {
	if document.state == nil || document.state.wire.APIVersion != APIVersion || document.state.wire.Kind != documentKind {
		return nil, buildError("document_invalid", "/document", "frontend IR document is invalid")
	}
	if err := validateWireSchema(irSchemaURL, IRSchema(), document.state.wire); err != nil {
		return nil, buildError("schema_invalid", "/document", fmt.Sprintf("frontend IR does not match its schema: %v", err))
	}
	return canonicalize(document.state.wire, func() *Error {
		return buildError("canonical_invalid", "/document", "frontend IR cannot be canonicalized")
	})
}

func wireSources(sources []provenance.Source) []wireSource {
	result := make([]wireSource, len(sources))
	for index, source := range sources {
		result[index] = wireSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	return result
}

func canonicalize(value any, failure func() *Error) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, failure()
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, failure()
	}
	return canonical, nil
}

func cloneWireDocument(input wireDocument) wireDocument {
	result := input
	result.API = append(json.RawMessage(nil), input.API...)
	result.Sources = make([]wireSource, len(input.Sources))
	copy(result.Sources, input.Sources)
	result.Pages = make([]wirePage, len(input.Pages))
	copy(result.Pages, input.Pages)
	for index := range result.Pages {
		page := &result.Pages[index]
		if input.Pages[index].Menu != nil {
			menu := *input.Pages[index].Menu
			page.Menu = &menu
		}
		page.Operations = make([]wireOperation, len(input.Pages[index].Operations))
		copy(page.Operations, input.Pages[index].Operations)
		for operation := range page.Operations {
			page.Operations[operation].Result = cloneResult(input.Pages[index].Operations[operation].Result)
			if input.Pages[index].Operations[operation].Pagination != nil {
				value := clonePagination(input.Pages[index].Operations[operation].Pagination)
				page.Operations[operation].Pagination = &value
			}
			page.Operations[operation].ContextBindings = make([]wireContextBinding, len(input.Pages[index].Operations[operation].ContextBindings))
			copy(page.Operations[operation].ContextBindings, input.Pages[index].Operations[operation].ContextBindings)
			for binding := range page.Operations[operation].ContextBindings {
				page.Operations[operation].ContextBindings[binding].Path = clonePath(input.Pages[index].Operations[operation].ContextBindings[binding].Path)
				page.Operations[operation].ContextBindings[binding].ValueType = cloneWireValue(input.Pages[index].Operations[operation].ContextBindings[binding].ValueType)
			}
		}
		page.Fields = append([]wireField(nil), input.Pages[index].Fields...)
		for field := range page.Fields {
			page.Fields[field].Surfaces = make([]string, len(input.Pages[index].Fields[field].Surfaces))
			copy(page.Fields[field].Surfaces, input.Pages[index].Fields[field].Surfaces)
			page.Fields[field].Bindings = make([]wireBinding, len(input.Pages[index].Fields[field].Bindings))
			copy(page.Fields[field].Bindings, input.Pages[index].Fields[field].Bindings)
			for binding := range page.Fields[field].Bindings {
				page.Fields[field].Bindings[binding].Path = clonePath(input.Pages[index].Fields[field].Bindings[binding].Path)
				page.Fields[field].Bindings[binding].ValueType = cloneWireValue(input.Pages[index].Fields[field].Bindings[binding].ValueType)
			}
			page.Fields[field].Choices = append([]choiceDocument(nil), input.Pages[index].Fields[field].Choices...)
			page.Fields[field].Columns = append([]wireColumn(nil), input.Pages[index].Fields[field].Columns...)
			for column := range page.Fields[field].Columns {
				page.Fields[field].Columns[column].Path = clonePath(input.Pages[index].Fields[field].Columns[column].Path)
				page.Fields[field].Columns[column].ValueType = cloneWireValue(input.Pages[index].Fields[field].Columns[column].ValueType)
			}
			if input.Pages[index].Fields[field].Options != nil {
				options := *input.Pages[index].Fields[field].Options
				options.ValuePath = clonePath(options.ValuePath)
				options.LabelPath = clonePath(options.LabelPath)
				page.Fields[field].Options = &options
			}
		}
		page.Actions = make([]actionDocument, len(input.Pages[index].Actions))
		copy(page.Actions, input.Pages[index].Actions)
		for action := range page.Actions {
			page.Actions[action].Fields = make([]string, len(input.Pages[index].Actions[action].Fields))
			copy(page.Actions[action].Fields, input.Pages[index].Actions[action].Fields)
		}
		page.ExtensionPoints = make([]string, len(input.Pages[index].ExtensionPoints))
		copy(page.ExtensionPoints, input.Pages[index].ExtensionPoints)
	}
	result.Locales = make([]wireLocale, len(input.Locales))
	for index, locale := range input.Locales {
		result.Locales[index] = wireLocale{Locale: locale.Locale, SourceRef: locale.SourceRef, Messages: map[string]string{}}
		for key, value := range locale.Messages {
			result.Locales[index].Messages[key] = value
		}
	}
	return result
}

func cloneWireValue(input wireValueType) wireValueType {
	result := input
	if input.Element != nil {
		element := cloneWireValue(*input.Element)
		result.Element = &element
	}
	return result
}
