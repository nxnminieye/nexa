package frontend

import "github.com/nxnminieye/nexa/provenance"

const (
	PageSpecAPIVersion = "nexa.dev/frontend-page-spec/v1"
	LocaleAPIVersion   = "nexa.dev/frontend-locale/v1"
	APIVersion         = "nexa.dev/frontend-ir/v1"
	RendererAPIVersion = "nexa.dev/frontend-renderer/v1"
)

const (
	pageSpecKind = "FrontendPageSpec"
	localeKind   = "FrontendLocale"
	documentKind = "FrontendIR"
	renderKind   = "FrontendRenderRequest"
	sourceSetAPI = "nexa.dev/frontend-source-set/v1"
)

type PageSpec struct{ state *pageSpecState }
type Locale struct{ state *localeState }
type Document struct{ state *documentState }

type pageSpecState struct {
	document  pageDocument
	sourceRef provenance.SourceRef
	digest    provenance.Digest
}

type documentState struct{ wire wireDocument }

type localeState struct {
	document  localeDocument
	sourceRef provenance.SourceRef
	digest    provenance.Digest
}

type localeDocument struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Locale     string            `json:"locale"`
	Messages   map[string]string `json:"messages"`
}

type pageDocument struct {
	APIVersion      string              `json:"apiVersion"`
	Kind            string              `json:"kind"`
	ID              string              `json:"id"`
	TitleKey        string              `json:"titleKey"`
	Mode            string              `json:"mode"`
	AccessOperation string              `json:"accessOperation"`
	Route           routeDocument       `json:"route"`
	Menu            *menuDocument       `json:"menu,omitempty"`
	Operations      []operationDocument `json:"operations"`
	Fields          []fieldDocument     `json:"fields"`
	Actions         []actionDocument    `json:"actions"`
	ExtensionPoints []string            `json:"extensionPoints"`
}

type routeDocument struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type menuDocument struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	TitleKey string `json:"titleKey"`
	Icon     string `json:"icon,omitempty"`
	Order    int    `json:"order"`
}

type operationDocument struct {
	ID              string                   `json:"id"`
	Role            string                   `json:"role"`
	OperationID     string                   `json:"operationId"`
	Result          *resultDocument          `json:"result,omitempty"`
	Pagination      *paginationDocument      `json:"pagination,omitempty"`
	ContextBindings []contextBindingDocument `json:"contextBindings"`
}

type contextBindingDocument struct {
	Context string   `json:"context"`
	Path    []string `json:"path"`
}

type paginationDocument struct {
	Mode       string   `json:"mode"`
	LimitPath  []string `json:"limitPath"`
	OffsetPath []string `json:"offsetPath"`
	TotalPath  []string `json:"totalPath"`
	PageSize   int      `json:"pageSize"`
}

type resultDocument struct {
	ItemsPath  []string `json:"itemsPath,omitempty"`
	ItemPath   []string `json:"itemPath,omitempty"`
	TotalPath  []string `json:"totalPath,omitempty"`
	RowKeyPath []string `json:"rowKeyPath,omitempty"`
}

type fieldDocument struct {
	ID       string            `json:"id"`
	LabelKey string            `json:"labelKey"`
	Surfaces []string          `json:"surfaces"`
	Control  string            `json:"control,omitempty"`
	Bindings []bindingDocument `json:"bindings"`
	Options  *optionsDocument  `json:"options,omitempty"`
	Choices  []choiceDocument  `json:"choices,omitempty"`
	Columns  []columnDocument  `json:"columns,omitempty"`
}

type optionsDocument struct {
	Operation string   `json:"operation"`
	ValuePath []string `json:"valuePath"`
	LabelPath []string `json:"labelPath"`
}

type choiceDocument struct {
	Value    string `json:"value"`
	LabelKey string `json:"labelKey"`
}

type columnDocument struct {
	ID       string   `json:"id"`
	LabelKey string   `json:"labelKey"`
	Path     []string `json:"path"`
}

type bindingDocument struct {
	Operation string   `json:"operation"`
	Direction string   `json:"direction"`
	Path      []string `json:"path"`
}

type actionDocument struct {
	ID         string   `json:"id"`
	LabelKey   string   `json:"labelKey"`
	Operation  string   `json:"operation"`
	Effect     string   `json:"effect"`
	Fields     []string `json:"fields"`
	Placement  string   `json:"placement"`
	ConfirmKey string   `json:"confirmKey,omitempty"`
}

type RenderRequest struct {
	FrontendIR               Document
	RepositoryRoot           string
	GeneratedScope           string
	ExtensionScopes          []string
	FrontendSourceLockDigest provenance.Digest
}

func (p PageSpec) ID() string {
	if p.state == nil {
		return ""
	}
	return p.state.document.ID
}

func (p PageSpec) SourceRef() provenance.SourceRef {
	if p.state == nil {
		return provenance.SourceRef{}
	}
	return p.state.sourceRef
}

func (p PageSpec) Digest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.digest
}

func (l Locale) Locale() string {
	if l.state == nil {
		return ""
	}
	return l.state.document.Locale
}

func (l Locale) SourceRef() provenance.SourceRef {
	if l.state == nil {
		return provenance.SourceRef{}
	}
	return l.state.sourceRef
}

func (l Locale) Digest() provenance.Digest {
	if l.state == nil {
		return provenance.Digest{}
	}
	return l.state.digest
}

func (d Document) PageCount() int {
	if d.state == nil {
		return 0
	}
	return len(d.state.wire.Pages)
}
