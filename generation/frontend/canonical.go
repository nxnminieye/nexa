package frontend

import (
	"encoding/json"
	"fmt"

	"github.com/gowebpki/jcs"
)

type wireField struct {
	Name     string   `json:"name"`
	LabelKey string   `json:"labelKey"`
	Surfaces []string `json:"surfaces"`
	Control  string   `json:"control,omitempty"`
}

type wireRoute struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type wireMenu struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	TitleKey string `json:"titleKey"`
	Path     string `json:"path"`
	Icon     string `json:"icon,omitempty"`
	Order    int    `json:"order"`
}

type wirePageOperations struct {
	List   string `json:"list"`
	Create string `json:"create,omitempty"`
	Get    string `json:"get,omitempty"`
	Update string `json:"update,omitempty"`
	Delete string `json:"delete,omitempty"`
}

type wirePage struct {
	ID                 string             `json:"id"`
	TitleKey           string             `json:"titleKey"`
	Route              wireRoute          `json:"route"`
	ExtensionComponent string             `json:"extensionComponent,omitempty"`
	Menu               *wireMenu          `json:"menu,omitempty"`
	PageSize           int                `json:"pageSize"`
	Operations         wirePageOperations `json:"operations"`
	Fields             []wireField        `json:"fields"`
}

type wireLocale struct {
	Locale   string            `json:"locale"`
	Messages map[string]string `json:"messages"`
}

type wireDocument struct {
	APIVersion     string                 `json:"apiVersion"`
	Kind           string                 `json:"kind"`
	HTTPConvention string                 `json:"httpConvention"`
	Types          []wireClosureType      `json:"types"`
	Operations     []wireClosureOperation `json:"operations"`
	Locales        []wireLocale           `json:"locales"`
	Pages          []wirePage             `json:"pages"`
}

type wireClosureValue struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name,omitempty"`
	Element *wireClosureValue `json:"element,omitempty"`
}

type wireClosureField struct {
	Path      []string         `json:"path"`
	Required  bool             `json:"required"`
	ValueType wireClosureValue `json:"valueType"`
}

type wireClosureType struct {
	Name           string             `json:"name"`
	TypeScriptName string             `json:"typescriptName"`
	Shape          *wireClosureValue  `json:"shape,omitempty"`
	Fields         []wireClosureField `json:"fields"`
}

type wireClosureOperation struct {
	ClientName   string `json:"clientName"`
	ID           string `json:"id"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Auth         string `json:"auth"`
	Permission   string `json:"permission"`
	RequestType  string `json:"requestType"`
	ResponseType string `json:"responseType"`
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
	result.Types = make([]wireClosureType, len(input.Types))
	copy(result.Types, input.Types)
	for index := range result.Types {
		if input.Types[index].Shape != nil {
			shape := cloneWireClosureValue(*input.Types[index].Shape)
			result.Types[index].Shape = &shape
		}
		result.Types[index].Fields = make([]wireClosureField, len(input.Types[index].Fields))
		copy(result.Types[index].Fields, input.Types[index].Fields)
		for field := range result.Types[index].Fields {
			result.Types[index].Fields[field].Path = append([]string(nil), input.Types[index].Fields[field].Path...)
			result.Types[index].Fields[field].ValueType = cloneWireClosureValue(input.Types[index].Fields[field].ValueType)
		}
	}
	result.Operations = append([]wireClosureOperation{}, input.Operations...)
	result.Pages = make([]wirePage, len(input.Pages))
	copy(result.Pages, input.Pages)
	for index := range result.Pages {
		page := &result.Pages[index]
		if input.Pages[index].Menu != nil {
			menu := *input.Pages[index].Menu
			page.Menu = &menu
		}
		page.Fields = make([]wireField, len(input.Pages[index].Fields))
		copy(page.Fields, input.Pages[index].Fields)
		for field := range page.Fields {
			page.Fields[field].Surfaces = append([]string{}, input.Pages[index].Fields[field].Surfaces...)
		}
	}
	result.Locales = make([]wireLocale, len(input.Locales))
	for index, locale := range input.Locales {
		result.Locales[index] = wireLocale{Locale: locale.Locale, Messages: map[string]string{}}
		for key, value := range locale.Messages {
			result.Locales[index].Messages[key] = value
		}
	}
	return result
}

func cloneWireClosureValue(input wireClosureValue) wireClosureValue {
	result := input
	if input.Element != nil {
		element := cloneWireClosureValue(*input.Element)
		result.Element = &element
	}
	return result
}
