package frontend

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

const maxRenderRequestBytes = 1 << 20

type wireRenderRequest struct {
	APIVersion               string          `json:"apiVersion"`
	Kind                     string          `json:"kind"`
	FrontendIR               json.RawMessage `json:"frontendIR"`
	RepositoryRoot           string          `json:"repositoryRoot"`
	GeneratedScope           string          `json:"generatedScope"`
	ExtensionScopes          []string        `json:"extensionScopes"`
	FrontendSourceLockDigest string          `json:"frontendSourceLockDigest"`
}

func CanonicalRenderRequest(request RenderRequest) ([]byte, error) {
	frontendIR, err := CanonicalJSON(request.FrontendIR)
	if err != nil {
		return nil, renderError("frontend_ir_invalid", "/frontendIR", "frontend IR is invalid")
	}
	if request.RepositoryRoot == "" || !filepath.IsAbs(request.RepositoryRoot) || filepath.Clean(request.RepositoryRoot) != request.RepositoryRoot {
		return nil, renderError("repository_root_invalid", "/repositoryRoot", "repository root must be an absolute normalized path")
	}
	if !validWireScope(request.GeneratedScope) {
		return nil, renderError("generated_scope_invalid", "/generatedScope", "generated scope must be a canonical repository-relative path outside .git")
	}
	extensions := make([]string, len(request.ExtensionScopes))
	copy(extensions, request.ExtensionScopes)
	for index, scope := range extensions {
		if !validWireScope(scope) {
			return nil, renderError("extension_scope_invalid", "/extensionScopes/"+itoa(index), "extension scope must be a canonical repository-relative path outside .git")
		}
		if relation := scopeRelation(request.GeneratedScope, scope); relation != "" {
			return nil, renderError("scope_"+relation, "/extensionScopes/"+itoa(index), "generated and extension scopes must not collide or overlap")
		}
		for previous := 0; previous < index; previous++ {
			if relation := scopeRelation(extensions[previous], scope); relation != "" {
				return nil, renderError("scope_"+relation, "/extensionScopes/"+itoa(index), "extension scopes must not collide or overlap")
			}
		}
	}
	sort.Strings(extensions)
	if _, err := provenance.ParseDigest(request.FrontendSourceLockDigest.String()); err != nil {
		return nil, renderError("frontend_source_lock_digest_invalid", "/frontendSourceLockDigest", "frontend source lock digest is invalid")
	}
	wire := wireRenderRequest{APIVersion: RendererAPIVersion, Kind: renderKind, FrontendIR: frontendIR, RepositoryRoot: request.RepositoryRoot, GeneratedScope: request.GeneratedScope, ExtensionScopes: extensions, FrontendSourceLockDigest: request.FrontendSourceLockDigest.String()}
	if err := validateWireSchema(renderSchemaURL, RenderRequestSchema(), wire); err != nil {
		return nil, renderError("schema_invalid", "", "frontend render request does not match its schema")
	}
	encoded, err := canonicalize(wire, func() *Error {
		return renderError("canonical_invalid", "", "frontend render request cannot be canonicalized")
	})
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxRenderRequestBytes {
		return nil, renderError("request_too_large", "", "frontend render request exceeds 1 MiB")
	}
	return encoded, nil
}

func validWireScope(value string) bool {
	if _, err := provenance.RepositoryRef(value, ""); err != nil {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

func scopeRelation(left, right string) string {
	leftParts, rightParts := strings.Split(left, "/"), strings.Split(right, "/")
	limit := len(leftParts)
	if len(rightParts) < limit {
		limit = len(rightParts)
	}
	for index := 0; index < limit; index++ {
		if !strings.EqualFold(leftParts[index], rightParts[index]) {
			return ""
		}
	}
	if len(leftParts) == len(rightParts) {
		return "collision"
	}
	return "overlap"
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
