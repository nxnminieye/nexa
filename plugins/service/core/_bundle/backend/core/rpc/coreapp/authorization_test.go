package coreapp

import (
	"context"
	"testing"
)

func TestHealthAndAuthorization(t *testing.T) {
	health, err := CheckHealth(context.Background())
	if err != nil || !health.Ready {
		t.Fatalf("health = %#v, %v", health, err)
	}
	grants := []RoleGrant{{RoleRef: "operator", Permissions: []string{"core.read", "core.write"}}}
	authorizer, err := NewAuthorizer(grants)
	if err != nil {
		t.Fatal(err)
	}
	grants[0].Permissions[0] = "mutated"
	allowed, err := authorizer.Allowed(context.Background(), []string{"operator"}, "core.read")
	if err != nil || !allowed {
		t.Fatalf("Allowed() = %v, %v", allowed, err)
	}
	allowed, err = authorizer.Allowed(context.Background(), []string{"operator"}, "core.delete")
	if err != nil || allowed {
		t.Fatalf("unknown permission = %v, %v", allowed, err)
	}
}
