package coreapp

import (
	"context"
	"sort"
	"strings"
)

type RoleGrant struct {
	RoleRef     string
	Permissions []string
}

type Authorizer struct {
	permissions map[string][]string
}

func NewAuthorizer(grants []RoleGrant) (*Authorizer, error) {
	permissions := make(map[string][]string, len(grants))
	for _, grant := range grants {
		role := strings.TrimSpace(grant.RoleRef)
		if role == "" {
			return nil, invalid("authorization.new")
		}
		if _, exists := permissions[role]; exists {
			return nil, coreError("authorization.new", CodeConflict, nil)
		}
		seen := make(map[string]struct{}, len(grant.Permissions))
		values := make([]string, 0, len(grant.Permissions))
		for _, value := range grant.Permissions {
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, invalid("authorization.new")
			}
			if _, duplicate := seen[value]; duplicate {
				return nil, coreError("authorization.new", CodeConflict, nil)
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
		sort.Strings(values)
		permissions[role] = values
	}
	return &Authorizer{permissions: permissions}, nil
}

func (a *Authorizer) Allowed(ctx context.Context, roles []string, permission string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, canceled("authorization.check", err)
	}
	if a == nil || strings.TrimSpace(permission) == "" {
		return false, invalid("authorization.check")
	}
	permission = strings.TrimSpace(permission)
	for _, role := range roles {
		values := a.permissions[role]
		index := sort.SearchStrings(values, permission)
		if index < len(values) && values[index] == permission {
			return true, nil
		}
	}
	return false, nil
}
