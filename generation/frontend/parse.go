package frontend

import (
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

const messageKeyExpression = `^[a-z][A-Za-z0-9]*(?:[.-][A-Za-z0-9]+)*$`

var messageKeyPattern = regexp.MustCompile(messageKeyExpression)

func ParsePageSpec(source string, data []byte) (PageSpec, error) {
	domainSource, err := provenance.ParseDomainSource(source)
	if err != nil {
		return PageSpec{}, pageError("source_invalid", source, "", "frontend page source must be a canonical repository-relative path")
	}
	sourceRef, err := provenance.RepositoryRef(domainSource.String(), "")
	if err != nil {
		return PageSpec{}, pageError("source_invalid", source, "", "frontend page source must be a canonical repository-relative path")
	}
	facts, diagnostics, err := sourcecomment.ParseFrontendSource(source, data)
	if err != nil {
		return PageSpec{}, pageError("document_invalid", source, "", err.Error())
	}
	if len(diagnostics) > 0 {
		diagnostic := diagnostics[0]
		failure := pageError(string(diagnostic.Code), source, "", diagnostic.Suggestion)
		failure.line, failure.column = diagnostic.Line, diagnostic.Column
		return PageSpec{}, failure
	}
	pageID := ""
	for _, fact := range facts.Facts() {
		if fact.ID().Key != "ui.entity" {
			continue
		}
		if pageID != "" && pageID != fact.ID().SemanticID {
			return PageSpec{}, pageError("page_identity_ambiguous", source, "", "frontend page source contains multiple page identities")
		}
		pageID = fact.ID().SemanticID
	}
	if pageID == "" {
		return PageSpec{}, pageError("page_entity_missing", source, "", "frontend page source must declare ui.entity")
	}
	canonical, err := facts.CanonicalJSON()
	if err != nil {
		return PageSpec{}, pageError("canonical_invalid", source, "", "frontend page facts cannot be canonicalized")
	}
	return PageSpec{state: &pageSpecState{facts: facts, sourceRef: sourceRef, digest: provenance.SHA256(canonical)}}, nil
}

func ParseLocale(source string, data []byte) (Locale, error) {
	sourceRef, strict, err := parseFrontendSource(source, data)
	if err != nil {
		if typed, ok := err.(*Error); ok {
			typed.code = "frontend_locale_invalid"
		}
		return Locale{}, err
	}
	var normalized any
	if err := json.Unmarshal(strict.JSON(), &normalized); err != nil {
		return Locale{}, localeError("document_invalid", source, "", "frontend locale document is invalid")
	}
	if err := validateLocaleSchema(normalized); err != nil {
		failures := localeSchemaFailures(source, err)
		if len(failures) > 0 {
			return Locale{}, withLocation(strict, failures[0])
		}
	}
	var document localeDocument
	if err := strict.DecodeExact(&document); err != nil {
		return Locale{}, projectLocaleDocumentError(source, err)
	}
	for key := range document.Messages {
		if !messageKeyPattern.MatchString(key) {
			return Locale{}, withLocation(strict, localeError("message_key_invalid", source, "/messages/"+escapePointer(key), "frontend locale message key is invalid"))
		}
	}
	canonical, err := jcs.Transform(strict.JSON())
	if err != nil {
		return Locale{}, localeError("canonical_invalid", source, "", "frontend locale cannot be canonicalized")
	}
	return Locale{state: &localeState{document: cloneLocaleDocument(document), sourceRef: sourceRef, digest: provenance.SHA256(canonical)}}, nil
}

func parseFrontendSource(source string, data []byte) (provenance.SourceRef, strictdoc.Document, error) {
	domainSource, err := provenance.ParseDomainSource(source)
	if err != nil {
		return provenance.SourceRef{}, strictdoc.Document{}, pageError("source_invalid", source, "", "frontend source must be a canonical repository-relative path")
	}
	sourceRef, err := provenance.RepositoryRef(domainSource.String(), "")
	if err != nil {
		return provenance.SourceRef{}, strictdoc.Document{}, pageError("source_invalid", source, "", "frontend source must be a canonical repository-relative path")
	}
	var document strictdoc.Document
	switch strings.ToLower(path.Ext(source)) {
	case ".json":
		document, err = strictdoc.ParseJSON(source, data)
	case ".yaml", ".yml":
		document, err = strictdoc.ParseYAML(source, data)
	default:
		return provenance.SourceRef{}, strictdoc.Document{}, pageError("format_unsupported", source, "", "frontend source must use .json, .yaml, or .yml")
	}
	if err != nil {
		return provenance.SourceRef{}, strictdoc.Document{}, projectDocumentError(source, err)
	}
	return sourceRef, document, nil
}

func projectDocumentError(source string, err error) *Error {
	var strict *strictdoc.Error
	if !errors.As(err, &strict) {
		return pageError("document_invalid", source, "", "frontend page spec document is invalid")
	}
	result := pageError(strict.Code, strict.Source, strict.Pointer, "frontend page spec document is invalid")
	result.line, result.column = strict.Line, strict.Column
	return result
}

func projectLocaleDocumentError(source string, err error) *Error {
	var strict *strictdoc.Error
	if !errors.As(err, &strict) {
		return localeError("document_invalid", source, "", "frontend locale document is invalid")
	}
	result := localeError(strict.Code, strict.Source, strict.Pointer, "frontend locale document is invalid")
	result.line, result.column = strict.Line, strict.Column
	return result
}

func withLocation(document strictdoc.Document, err *Error) *Error {
	if err == nil {
		return nil
	}
	if line, column, ok := document.Location(err.pointer); ok {
		err.line, err.column = line, column
	}
	return err
}

func cloneLocaleDocument(input localeDocument) localeDocument {
	result := input
	result.Messages = make(map[string]string, len(input.Messages))
	for key, value := range input.Messages {
		result.Messages[key] = value
	}
	return result
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
