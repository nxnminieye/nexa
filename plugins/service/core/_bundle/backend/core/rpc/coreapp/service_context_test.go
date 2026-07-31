package coreapp

import "testing"

func TestConfigValidationUsesTheDeclaredRuntimeShape(t *testing.T) {
	valid := Config{
		ListenAddress: "127.0.0.1:8080",
		DatabaseURL:   "postgres://core@localhost/core",
		TenantCode:    "tenant-a",
		DefaultRouter: "/home",
		AccessTTL:     "5m",
		RefreshTTL:    "1h",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseConfig([]byte(`{"listenAddress":"127.0.0.1:8080","databaseUrl":"postgres://core@localhost/core","tenantCode":"tenant-a","defaultRouter":"/home","accessTtl":"5m","refreshTtl":"1h"}`))
	if err != nil || parsed != valid {
		t.Fatalf("parsed config = %#v, err=%v", parsed, err)
	}

	for name, invalid := range map[string]Config{
		"missing-router":        {ListenAddress: valid.ListenAddress, DatabaseURL: valid.DatabaseURL, TenantCode: valid.TenantCode, AccessTTL: valid.AccessTTL, RefreshTTL: valid.RefreshTTL},
		"relative-router":       {ListenAddress: valid.ListenAddress, DatabaseURL: valid.DatabaseURL, TenantCode: valid.TenantCode, DefaultRouter: "home", AccessTTL: valid.AccessTTL, RefreshTTL: valid.RefreshTTL},
		"refresh-before-access": {ListenAddress: valid.ListenAddress, DatabaseURL: valid.DatabaseURL, TenantCode: valid.TenantCode, DefaultRouter: valid.DefaultRouter, AccessTTL: "1h", RefreshTTL: "5m"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := invalid.Validate(); err == nil {
				t.Fatal("expected invalid config")
			}
		})
	}
}
