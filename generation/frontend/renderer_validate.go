package frontend

import (
	"bytes"
	"encoding/json"
	"path/filepath"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

// ValidateRendererInput validates only the renderer request boundary. Frontend
// semantics are compiled and validated once by Build before this request exists.
func ValidateRendererInput(data []byte) error {
	if len(data) > maxRenderRequestBytes {
		return renderError("request_too_large", "", "frontend render request exceeds 1 MiB")
	}
	document, err := strictdoc.ParseJSON("frontend-render-request.json", data)
	if err != nil {
		return renderError("document_invalid", "", "frontend render request is not valid JSON")
	}
	var normalized any
	if err := json.Unmarshal(document.JSON(), &normalized); err != nil {
		return renderError("document_invalid", "", "frontend render request is not valid JSON")
	}
	if err := validateWireSchema(renderSchemaURL, RenderRequestSchema(), normalized); err != nil {
		return renderError("schema_invalid", "", "frontend render request does not match its schema")
	}
	canonical, err := jcs.Transform(data)
	if err != nil || !bytes.Equal(canonical, data) {
		return renderError("noncanonical_input", "", "frontend render request must be canonical JSON")
	}

	var request wireRenderRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return renderError("document_invalid", "", "frontend render request is invalid")
	}
	if request.RepositoryRoot == "" || !filepath.IsAbs(request.RepositoryRoot) || filepath.Clean(request.RepositoryRoot) != request.RepositoryRoot {
		return renderError("repository_root_invalid", "/repositoryRoot", "repository root must be absolute")
	}
	if !validWireScope(request.GeneratedScope) {
		return renderError("generated_scope_invalid", "/generatedScope", "generated scope is invalid")
	}
	for index, scope := range request.ExtensionScopes {
		if !validWireScope(scope) {
			return renderError("extension_scope_invalid", "/extensionScopes/"+itoa(index), "extension scope is invalid")
		}
		if relation := scopeRelation(request.GeneratedScope, scope); relation != "" {
			return renderError("scope_"+relation, "/extensionScopes/"+itoa(index), "generated and extension scopes overlap")
		}
		for previous := 0; previous < index; previous++ {
			if relation := scopeRelation(request.ExtensionScopes[previous], scope); relation != "" {
				return renderError("scope_"+relation, "/extensionScopes/"+itoa(index), "extension scopes overlap")
			}
		}
	}
	if _, err := provenance.ParseDigest(request.FrontendSourceLockDigest); err != nil {
		return renderError("frontend_source_lock_digest_invalid", "/frontendSourceLockDigest", "frontend source lock digest is invalid")
	}
	return nil
}
