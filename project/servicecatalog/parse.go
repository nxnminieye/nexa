package servicecatalog

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

type catalogDocument struct {
	APIVersion *string           `json:"apiVersion,omitempty"`
	Kind       *string           `json:"kind,omitempty"`
	Services   []serviceDocument `json:"services"`
}

type serviceDocument struct {
	ID                 *string                     `json:"id,omitempty"`
	Root               *string                     `json:"root,omitempty"`
	DependsOn          []string                    `json:"dependsOn,omitempty"`
	CapabilityBindings []capabilityBindingDocument `json:"capabilityBindings"`
}

type capabilityBindingDocument struct {
	ID         *string `json:"id,omitempty"`
	APIVersion *string `json:"apiVersion,omitempty"`
}

func Parse(source string, data []byte) (Catalog, error) {
	if !validSourcePath(source) {
		return Catalog{}, newError(
			"service_catalog_invalid", "source_identity_invalid", "", "", "service catalog source identity is invalid",
		)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return Catalog{}, newError(
			"service_catalog_empty", "", source, "", "service catalog is empty",
		)
	}

	var document catalogDocument
	var strictDocument strictdoc.Document
	var err error
	if strings.EqualFold(path.Ext(source), ".json") {
		strictDocument, err = strictdoc.ParseJSON(source, data)
	} else {
		strictDocument, err = strictdoc.ParseYAML(source, data)
	}
	if err != nil {
		return Catalog{}, documentFailure(source, err)
	}
	documentJSON := strictDocument.JSON()
	normalized, err := normalizedDocument(documentJSON)
	if err != nil {
		return Catalog{}, newError(
			"service_catalog_invalid", "document_invalid", source, "",
			"service catalog document is invalid",
		)
	}
	var failures []*Error
	if err := validateDocumentSchema(normalized); err != nil {
		failures = append(failures, schemaValidationErrors(source, err)...)
	}
	if err := strictDocument.Decode(&document); err != nil {
		failures = append(failures, documentFailure(source, err))
		// Recover safe earlier fields for candidate selection; the decode failure still prevents success.
		_ = json.Unmarshal(documentJSON, &document)
	}
	if document.APIVersion != nil && *document.APIVersion != APIVersion {
		failures = append(failures, newError(
			"service_catalog_invalid", "version_unsupported", source, "/apiVersion",
			"service catalog version is not supported",
		))
	}
	if document.Kind != nil && *document.Kind != Kind {
		failures = append(failures, newError(
			"service_catalog_invalid", "kind_invalid", source, "/kind",
			"service catalog kind is invalid",
		))
	}
	failures = append(failures, validateCatalog(source, document)...)
	if err := selectCatalogError(failures, normalized); err != nil {
		return Catalog{}, err
	}
	catalog, err := catalogFromDocument(source, document)
	if err != nil {
		return Catalog{}, newError(
			"service_catalog_invalid", "document_invalid", source, "", "service catalog document is invalid",
		)
	}
	return catalog, nil
}

func Load(root *os.Root, source string) (Catalog, error) {
	if root == nil || !validSourcePath(source) {
		return Catalog{}, newError(
			"fact_source_read_failed", "", source, "", "fact source could not be read",
		)
	}
	data, err := root.ReadFile(source)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Catalog{}, newError(
				"fact_source_missing", "", source, "", "fact source is missing",
			)
		}
		return Catalog{}, newError(
			"fact_source_read_failed", "", source, "", "fact source could not be read",
		)
	}
	return Parse(source, data)
}

func validSourcePath(source string) bool {
	if !fs.ValidPath(source) || source == "." {
		return false
	}
	_, err := provenance.RepositoryRef(source, "service-catalog")
	return err == nil
}

func documentFailure(source string, err error) *Error {
	var documentError *strictdoc.Error
	if !errors.As(err, &documentError) {
		return newError(
			"service_catalog_invalid", "document_invalid", source, "",
			"service catalog document is invalid",
		)
	}
	projected := newError(
		"service_catalog_invalid", documentError.Code, documentError.Source, documentError.Pointer,
		"service catalog document is invalid",
	)
	projected.line = documentError.Line
	projected.column = documentError.Column
	return projected
}

func selectCatalogError(failures []*Error, normalized any) *Error {
	if len(failures) == 0 {
		return nil
	}
	selected := failures[0]
	for _, failure := range failures[1:] {
		comparison := compareLocations(
			pointerLocation(failure.pointer), pointerLocation(selected.pointer), normalized,
		)
		if comparison < 0 || comparison == 0 && errorPriority(failure) < errorPriority(selected) {
			selected = failure
		}
	}
	return selected
}

func pointerLocation(pointer string) []string {
	if pointer == "" {
		return nil
	}
	components := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for index, component := range components {
		component = strings.ReplaceAll(component, "~1", "/")
		components[index] = strings.ReplaceAll(component, "~0", "~")
	}
	return components
}

func errorPriority(err *Error) int {
	switch err.reason {
	case "version_unsupported", "kind_invalid":
		return 0
	case "document_invalid", "document_unknown_field", "document_duplicate_key",
		"document_trailing_input", "document_alias_forbidden", "document_merge_key_forbidden",
		"document_tag_forbidden":
		return 1
	default:
		return 2
	}
}
