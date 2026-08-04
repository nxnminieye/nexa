package frontend

import (
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	LocaleAPIVersion   = "nexa.dev/frontend-locale/v1"
	APIVersion         = "nexa.dev/frontend-ir/v1"
	RendererAPIVersion = "nexa.dev/frontend-renderer/v1"
)

const (
	localeKind   = "FrontendLocale"
	documentKind = "FrontendIR"
	renderKind   = "FrontendRenderRequest"
)

type PageSpec struct{ state *pageSpecState }
type Locale struct{ state *localeState }
type Document struct{ state *documentState }

type pageSpecState struct {
	facts     sourcecomment.FactGraph
	sourceRef provenance.SourceRef
	digest    provenance.Digest
}

type documentState struct {
	wire  wireDocument
	facts sourcecomment.FactGraph
}

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

type RenderRequest struct {
	FrontendIR               Document
	RepositoryRoot           string
	GeneratedScope           string
	ExtensionScopes          []string
	FrontendSourceLockDigest provenance.Digest
}

// Route is the typed frontend route projected from source facts.
type Route struct {
	Name string
	Path string
}

// Menu is the typed menu metadata projected from source facts.
type Menu struct {
	Icon     string
	ID       string
	Order    int
	ParentID string
	Path     string
	TitleKey string
}

// PageOperations identifies the canonical operations selected by a page.
type PageOperations struct {
	Create string
	Delete string
	Get    string
	List   string
	Update string
}

// Page is a typed readback view of one canonical frontend page.
type Page struct {
	ExtensionComponent string
	ID                 string
	Menu               *Menu
	Operations         PageOperations
	Route              Route
	TitleKey           string
}

// Operation is a typed readback view of one canonical frontend operation.
type Operation struct {
	ClientName string
	ID         string
	Permission string
}

func (p PageSpec) ID() string {
	if p.state == nil {
		return ""
	}
	return pageFactID(p.state.facts)
}

func pageFactID(facts sourcecomment.FactGraph) string {
	for _, fact := range facts.Facts() {
		if fact.ID().Key == "ui.entity" {
			return fact.ID().SemanticID
		}
	}
	return ""
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

func (d Document) FactGraph() sourcecomment.FactGraph {
	if d.state == nil {
		return sourcecomment.FactGraph{}
	}
	return d.state.facts
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

func (d Document) OperationCount() int {
	if d.state == nil {
		return 0
	}
	return len(d.state.wire.Operations)
}

// Pages returns an immutable typed view of the pages in the canonical FrontendIR.
func (d Document) Pages() []Page {
	if d.state == nil {
		return nil
	}
	result := make([]Page, len(d.state.wire.Pages))
	for index, source := range d.state.wire.Pages {
		page := Page{
			ExtensionComponent: source.ExtensionComponent,
			ID:                 source.ID,
			Operations: PageOperations{
				Create: source.Operations.Create,
				Delete: source.Operations.Delete,
				Get:    source.Operations.Get,
				List:   source.Operations.List,
				Update: source.Operations.Update,
			},
			Route:    Route{Name: source.Route.Name, Path: source.Route.Path},
			TitleKey: source.TitleKey,
		}
		if source.Menu != nil {
			page.Menu = &Menu{Icon: source.Menu.Icon, ID: source.Menu.ID, Order: source.Menu.Order, ParentID: source.Menu.ParentID, Path: source.Menu.Path, TitleKey: source.Menu.TitleKey}
		}
		result[index] = page
	}
	return result
}

// Operations returns an immutable typed view of operations in the canonical FrontendIR.
func (d Document) Operations() []Operation {
	if d.state == nil {
		return nil
	}
	result := make([]Operation, len(d.state.wire.Operations))
	for index, source := range d.state.wire.Operations {
		result[index] = Operation{ClientName: source.ClientName, ID: source.ID, Permission: source.Permission}
	}
	return result
}
