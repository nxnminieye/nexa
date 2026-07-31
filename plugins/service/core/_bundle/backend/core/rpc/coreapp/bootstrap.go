package coreapp

import (
	"context"
	"strings"
)

type BootstrapInput struct {
	TenantCode    string
	TenantName    string
	Username      string
	Password      []byte
	Email         string
	DisplayName   string
	DefaultRouter string
	CatalogSource string
	Permissions   []PermissionCatalogEntry
	Menus         []MenuCatalogEntry
}

type BootstrapResult struct {
	Account IdentityAccount
	Tenant  ProvisionTenantResult
	Catalog CatalogSyncResult
}

// Bootstrap establishes the consumer-selected first tenant and synchronizes
// the Core catalog plus consumer catalog additions.
func Bootstrap(ctx context.Context, store *PostgresStore, auth *LocalAuthenticator, iam *IAMService, catalog *CatalogService, input BootstrapInput) (BootstrapResult, error) {
	const operation = "bootstrap"
	if store == nil || auth == nil || iam == nil || catalog == nil {
		return BootstrapResult{}, invalid(operation)
	}
	input.TenantCode = strings.TrimSpace(input.TenantCode)
	input.TenantName = strings.TrimSpace(input.TenantName)
	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.TrimSpace(input.Email)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.DefaultRouter = strings.TrimSpace(input.DefaultRouter)
	input.CatalogSource = strings.TrimSpace(input.CatalogSource)
	if input.TenantCode == "" || input.TenantName == "" || input.Username == "" || len(input.Password) == 0 ||
		!strings.HasPrefix(input.DefaultRouter, "/") || input.CatalogSource == "" || input.CatalogSource == coreCatalogSourceID {
		return BootstrapResult{}, invalid(operation)
	}
	for _, permission := range input.Permissions {
		if isCorePermissionCode(strings.TrimSpace(permission.Code)) {
			return BootstrapResult{}, invalid(operation)
		}
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO tenants(code,name,status,version) VALUES($1,$2,'enabled',1) ON CONFLICT(code) DO NOTHING`, input.TenantCode, input.TenantName); err != nil {
		return BootstrapResult{}, storeFailure(operation, err)
	}
	account, err := auth.Register(ctx, LocalRegistration{Tenant: input.TenantCode, Username: input.Username, Password: input.Password, Email: input.Email, DisplayName: input.DisplayName})
	if CodeOf(err) == CodeConflict {
		credential, lookupErr := store.FindLocalAccount(ctx, LocalAccountKey{Tenant: input.TenantCode, Username: input.Username})
		if lookupErr != nil || credential.Account.Email != input.Email || credential.Account.DisplayName != input.DisplayName || credential.Account.Status != IAMStatusEnabled || auth.hasher.Verify(credential.PasswordHash, input.Password) != nil {
			return BootstrapResult{}, coreError(operation, CodeConflict, lookupErr)
		}
		account = credential.Account
	} else if err != nil {
		return BootstrapResult{}, err
	}
	provisioned, err := iam.ProvisionTenant(ctx, ProvisionTenantInput{
		TenantCode: input.TenantCode, DisplayName: input.TenantName, DefaultRouter: input.DefaultRouter,
		OwnerAccountID: account.ID, OwnerUsername: account.Username, OwnerEmail: account.Email, OwnerName: account.DisplayName,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	corePermissions := corePermissionCatalog()
	coreDigest, err := CanonicalCatalogDigest(corePermissions, nil)
	if err != nil {
		return BootstrapResult{}, invalid(operation)
	}
	if _, err := catalog.Sync(ctx, CatalogSyncInput{SourceID: coreCatalogSourceID, Digest: coreDigest, Permissions: corePermissions}); err != nil {
		return BootstrapResult{}, err
	}
	digest, err := CanonicalCatalogDigest(input.Permissions, input.Menus)
	if err != nil {
		return BootstrapResult{}, invalid(operation)
	}
	synced, err := catalog.Sync(ctx, CatalogSyncInput{SourceID: input.CatalogSource, Digest: digest, Permissions: input.Permissions, Menus: input.Menus})
	if err != nil {
		return BootstrapResult{}, err
	}
	if account.ID == "" || provisioned.Tenant.ID == "" || synced.SourceID != input.CatalogSource {
		return BootstrapResult{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	return BootstrapResult{Account: account, Tenant: provisioned, Catalog: synced}, nil
}
