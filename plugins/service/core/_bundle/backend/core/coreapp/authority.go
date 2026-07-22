package coreapp

import (
	"context"
	"sort"
	"strings"
)

type ExternalRoleMappingInput struct {
	Tenant   string
	Identity NormalizedIdentity
	Account  IdentityAccount
	Member   TenantMember
}

type ExternalRoleMapper interface {
	MapExternalRoles(context.Context, ExternalRoleMappingInput) ([]string, error)
}

func canonicalExternalRoles(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, role := range values {
		if role == "" || role != strings.TrimSpace(role) {
			return nil, invalid("external-login.roles")
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	sort.Strings(result)
	return result, nil
}
