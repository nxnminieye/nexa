package consumer_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"example.com/core-iam-consumer/coreapp"
)

func TestCoreIAMPostgres(t *testing.T) {
	dsn := os.Getenv("NEXA_CORE_IAM_TEST_DSN")
	if dsn == "" {
		t.Fatal("NEXA_CORE_IAM_TEST_DSN is not configured")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := context.Background()
	schema := fmt.Sprintf("nexa_core_iam_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	databaseURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := databaseURL.Query()
	query.Set("search_path", schema)
	databaseURL.RawQuery = query.Encode()
	db, err := sql.Open("pgx", databaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	if applied := applyMigrations(t, ctx, db); applied != 2 {
		t.Fatalf("fresh migrations = %d", applied)
	}
	if applied := applyMigrations(t, ctx, db); applied != 0 {
		t.Fatalf("second migrations = %d", applied)
	}

	store, err := coreapp.NewPostgresStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO tenants(code,name,status) VALUES('bootstrap','Bootstrap','enabled')`); err != nil {
		t.Fatal(err)
	}
	var bootstrapTenantID string
	if err = db.QueryRowContext(ctx, `SELECT id::text FROM tenants WHERE code='bootstrap'`).Scan(&bootstrapTenantID); err != nil {
		t.Fatal(err)
	}
	hasher, err := coreapp.NewArgon2idHasher(coreapp.Argon2idOptions{MemoryKiB: 8192, Iterations: 1, Parallelism: 1, SaltBytes: 16, KeyBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	clock := coreapp.ClockFunc(func() time.Time { return time.Now().UTC() })
	auth, err := coreapp.NewLocalAuthenticator(store, hasher, coreapp.SessionOptions{AccessTTL: time.Minute, RefreshTTL: time.Hour, TokenBytes: 16, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	account, err := auth.Register(ctx, coreapp.LocalRegistration{Tenant: "bootstrap", Username: "owner", Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.Login(ctx, coreapp.LocalLogin{Tenant: "bootstrap", Username: "owner", Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := auth.Refresh(ctx, session.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.Revoke(ctx, rotated.ID); err != nil {
		t.Fatal(err)
	}

	reconciler := reconcileFunc(func(context.Context, coreapp.PolicyReconcileInput) error { return nil })
	iam, err := coreapp.NewIAMService(store, hasher, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	provision, err := iam.ProvisionTenant(ctx, coreapp.ProvisionTenantInput{TenantCode: "tenant-a", DisplayName: "Tenant A", OwnerAccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	again, err := iam.ProvisionTenant(ctx, coreapp.ProvisionTenantInput{TenantCode: "tenant-a", DisplayName: "Tenant A", OwnerAccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	if provision.Owner.ID != again.Owner.ID {
		t.Fatal("provision was not idempotent")
	}

	tenantID := provision.Tenant.ID
	role, err := iam.CreateTenantRole(ctx, coreapp.CreateTenantRoleInput{TenantID: tenantID, Code: "operator", DisplayName: "Operator"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := iam.ReplaceManualRoles(ctx, coreapp.ReplaceManualRolesInput{TenantID: tenantID, MemberID: provision.Owner.ID, RoleCodes: []string{"operator"}, ExpectedVersion: provision.Owner.Version})
	if err != nil {
		t.Fatal(err)
	}
	if len(member.ManualRoleCodes) != 1 || !member.ManagedOwnerGrant {
		t.Fatalf("grant isolation = %#v", member)
	}
	_, err = iam.ReplaceManualRoles(ctx, coreapp.ReplaceManualRolesInput{TenantID: tenantID, MemberID: provision.Owner.ID, RoleCodes: []string{"operator", "missing"}, ExpectedVersion: member.Version})
	assertCode(t, err, coreapp.CodeInvalidInput)
	afterRollback, err := iam.GetTenantMember(ctx, coreapp.TenantMemberKey{TenantID: tenantID, MemberID: provision.Owner.ID})
	if err != nil || len(afterRollback.ManualRoleCodes) != 1 || afterRollback.ManualRoleCodes[0] != "operator" {
		t.Fatalf("failed grant replacement was not rolled back: %#v %v", afterRollback, err)
	}
	_, err = iam.GetTenantMember(ctx, coreapp.TenantMemberKey{TenantID: bootstrapTenantID, MemberID: provision.Owner.ID})
	assertCode(t, err, coreapp.CodeNotFound)
	_, err = iam.SetTenantMemberStatus(ctx, coreapp.SetTenantMemberStatusInput{TenantID: tenantID, MemberID: provision.Owner.ID, Status: coreapp.IAMStatusDisabled, ExpectedVersion: member.Version})
	assertCode(t, err, coreapp.CodeFailedPrecondition)
	var managedRoleID string
	var managedRoleVersion uint64
	if err = db.QueryRowContext(ctx, `SELECT r.id::text,r.version FROM roles r JOIN tenants t ON t.id=r.tenant_id WHERE t.code='tenant-a' AND r.managed=TRUE`).Scan(&managedRoleID, &managedRoleVersion); err != nil {
		t.Fatal(err)
	}
	_, err = iam.UpdateTenantRole(ctx, coreapp.UpdateTenantRoleInput{TenantID: tenantID, RoleID: coreapp.TenantRoleID(managedRoleID), DisplayName: "Changed", ExpectedVersion: managedRoleVersion})
	assertCode(t, err, coreapp.CodePermissionDenied)
	if err = store.ReplaceExternalRoleGrants(ctx, coreapp.ReplaceExternalRoleGrantsInput{Tenant: "tenant-a", MemberID: provision.Owner.ID, SourceCode: "external-a", RoleCodes: []string{"tenant-owner"}}); !errors.Is(err, coreapp.ErrStorePermissionDenied) {
		t.Fatalf("external source granted managed owner role: %v", err)
	}
	for _, source := range []string{"external-a", "external-b"} {
		if err = store.ReplaceExternalRoleGrants(ctx, coreapp.ReplaceExternalRoleGrantsInput{Tenant: "tenant-a", MemberID: provision.Owner.ID, SourceCode: source, RoleCodes: []string{"operator"}}); err != nil {
			t.Fatalf("external grant %s: %v", source, err)
		}
	}
	ownerReadback, err := iam.GetTenantMember(ctx, coreapp.TenantMemberKey{TenantID: tenantID, MemberID: provision.Owner.ID})
	if err != nil || !ownerReadback.ManagedOwnerGrant || len(ownerReadback.ManualRoleCodes) != 1 {
		t.Fatalf("owner/source isolation readback=%#v err=%v", ownerReadback, err)
	}
	var ownerGrants, externalAGrants, externalBGrants, manualGrants int
	if err = db.QueryRowContext(ctx, `SELECT
  count(*) FILTER (WHERE g.source_owner='core.tenant-provision' AND r.source_owner='core.tenant-provision' AND r.source_key='tenant-owner'),
  count(*) FILTER (WHERE g.source_owner='external-a'),
  count(*) FILTER (WHERE g.source_owner='external-b')
FROM managed_tenant_member_roles g JOIN roles r ON r.id=g.role_id AND r.tenant_id=g.tenant_id
WHERE g.tenant_id=$1 AND g.tenant_member_id=$2`, tenantID, provision.Owner.ID).Scan(&ownerGrants, &externalAGrants, &externalBGrants); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tenant_member_roles WHERE tenant_id=$1 AND tenant_member_id=$2`, tenantID, provision.Owner.ID).Scan(&manualGrants); err != nil {
		t.Fatal(err)
	}
	if ownerGrants != 1 || externalAGrants != 1 || externalBGrants != 1 || manualGrants != 1 {
		t.Fatalf("grant sources owner=%d external-a=%d external-b=%d manual=%d", ownerGrants, externalAGrants, externalBGrants, manualGrants)
	}

	catalog, err := coreapp.NewCatalogService(store, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	sourceAPermissions := []coreapp.PermissionCatalogEntry{{Code: "read"}, {Code: "legacy"}}
	sourceAMenus := []coreapp.MenuCatalogEntry{{Code: "home", DisplayName: "Home"}, {Code: "legacy-menu", DisplayName: "Legacy"}}
	sourceADigest := catalogDigest(t, sourceAPermissions, sourceAMenus)
	first, err := catalog.Sync(ctx, coreapp.CatalogSyncInput{SourceID: "source-a", Digest: sourceADigest, Permissions: sourceAPermissions, Menus: sourceAMenus})
	if err != nil {
		t.Fatal(err)
	}
	sourceBPermissions := []coreapp.PermissionCatalogEntry{{Code: "write"}}
	sourceBMenus := []coreapp.MenuCatalogEntry{{Code: "settings", DisplayName: "Settings"}}
	if _, err = catalog.Sync(ctx, coreapp.CatalogSyncInput{SourceID: "source-b", Digest: catalogDigest(t, sourceBPermissions, sourceBMenus), Permissions: sourceBPermissions, Menus: sourceBMenus}); err != nil {
		t.Fatal(err)
	}
	second, err := catalog.Sync(ctx, coreapp.CatalogSyncInput{SourceID: "source-a", Digest: sourceADigest, Permissions: sourceAPermissions, Menus: sourceAMenus})
	if err != nil {
		t.Fatal(err)
	}
	if first.PermissionsDisabled != 0 || second.PermissionsUpserted != 0 || second.MenusUpserted != 0 || second.PermissionsDisabled != 0 || second.MenusDisabled != 0 {
		t.Fatal("repeat catalog sync was not stable")
	}
	upgradedPermissions := []coreapp.PermissionCatalogEntry{{Code: "read"}}
	upgradedMenus := []coreapp.MenuCatalogEntry{{Code: "home", DisplayName: "Home"}}
	upgradedDigest := catalogDigest(t, upgradedPermissions, upgradedMenus)
	upgraded, err := catalog.Sync(ctx, coreapp.CatalogSyncInput{SourceID: "source-a", Digest: upgradedDigest, Permissions: upgradedPermissions, Menus: upgradedMenus})
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.PermissionsDisabled != 1 || upgraded.MenusDisabled != 1 {
		t.Fatalf("catalog stale result=%#v", upgraded)
	}
	sameUpgrade, err := catalog.Sync(ctx, coreapp.CatalogSyncInput{SourceID: "source-a", Digest: upgradedDigest, Permissions: upgradedPermissions, Menus: upgradedMenus})
	if err != nil {
		t.Fatal(err)
	}
	if sameUpgrade.PermissionsUpserted != 0 || sameUpgrade.MenusUpserted != 0 || sameUpgrade.PermissionsDisabled != 0 || sameUpgrade.MenusDisabled != 0 {
		t.Fatalf("same digest changed catalog: %#v", sameUpgrade)
	}
	_, err = catalog.Sync(ctx, coreapp.CatalogSyncInput{SourceID: "source-a", Digest: upgradedDigest, Permissions: []coreapp.PermissionCatalogEntry{{Code: "different"}}, Menus: upgradedMenus})
	assertCode(t, err, coreapp.CodeInvalidInput)
	var sourceAStale, sourceBEnabled int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM permission_actions WHERE source_owner='source-a' AND code='legacy' AND status='disabled'`).Scan(&sourceAStale); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM permission_actions WHERE source_owner='source-b' AND code='write' AND status='enabled')+(SELECT count(*) FROM menus WHERE source_owner='source-b' AND code='settings' AND status='enabled')`).Scan(&sourceBEnabled); err != nil {
		t.Fatal(err)
	}
	if sourceAStale != 1 || sourceBEnabled != 2 {
		t.Fatalf("catalog source isolation stale=%d other-enabled=%d", sourceAStale, sourceBEnabled)
	}
	role, err = iam.ReplaceRolePermissions(ctx, coreapp.ReplaceRolePermissionsInput{TenantID: tenantID, RoleID: role.ID, PermissionCodes: []string{"read"}, ExpectedVersion: role.Version})
	if err != nil {
		t.Fatal(err)
	}
	if len(role.PermissionCodes) != 1 {
		t.Fatalf("permissions=%#v", role.PermissionCodes)
	}
	role, err = iam.ReplaceRoleMenus(ctx, coreapp.ReplaceRoleMenusInput{TenantID: tenantID, RoleID: role.ID, MenuCodes: []string{"home"}, ExpectedVersion: role.Version})
	if err != nil {
		t.Fatal(err)
	}
	if len(role.MenuCodes) != 1 || role.MenuCodes[0] != "home" {
		t.Fatalf("menus=%#v", role.MenuCodes)
	}

	type provisionOutcome struct {
		result coreapp.ProvisionTenantResult
		err    error
	}
	outcomes := make(chan provisionOutcome, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := iam.ProvisionTenant(ctx, coreapp.ProvisionTenantInput{TenantCode: "tenant-concurrent", DisplayName: "Concurrent", OwnerAccountID: account.ID})
			outcomes <- provisionOutcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(outcomes)
	var concurrent []coreapp.ProvisionTenantResult
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent provision: %v", outcome.err)
		}
		concurrent = append(concurrent, outcome.result)
	}
	if len(concurrent) != 2 || concurrent[0].Tenant.ID != concurrent[1].Tenant.ID || concurrent[0].Owner.ID != concurrent[1].Owner.ID || concurrent[0].Owner.AccountID != concurrent[1].Owner.AccountID {
		t.Fatalf("concurrent provision IDs differ: %#v", concurrent)
	}

	active, err := auth.Login(ctx, coreapp.LocalLogin{Tenant: "bootstrap", Username: "owner", Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	if err = iam.ResetAccountPassword(ctx, coreapp.ResetAccountPasswordInput{Actor: coreapp.SystemActor{System: true}, AccountID: account.ID, Password: []byte("new-password")}); err != nil {
		t.Fatal(err)
	}
	_, err = auth.Refresh(ctx, active.RefreshToken)
	assertCode(t, err, coreapp.CodeSessionReplayed)
	_, err = auth.Login(ctx, coreapp.LocalLogin{Tenant: "bootstrap", Username: "owner", Password: []byte("password")})
	assertCode(t, err, coreapp.CodeInvalidCredentials)
	active, err = auth.Login(ctx, coreapp.LocalLogin{Tenant: "bootstrap", Username: "owner", Password: []byte("new-password")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = iam.SetAccountStatus(ctx, coreapp.SetAccountStatusInput{Actor: coreapp.SystemActor{System: true}, AccountID: account.ID, Status: coreapp.IAMStatusDisabled}); err != nil {
		t.Fatal(err)
	}
	_, err = auth.Refresh(ctx, active.RefreshToken)
	assertCode(t, err, coreapp.CodeInvalidCredentials)
	_, err = auth.Login(ctx, coreapp.LocalLogin{Tenant: "bootstrap", Username: "owner", Password: []byte("new-password")})
	assertCode(t, err, coreapp.CodeInvalidCredentials)

}

func applyMigrations(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY,digest TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	applied := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		var existing string
		err = db.QueryRowContext(ctx, `SELECT digest FROM schema_migrations WHERE name=$1`, filepath.Base(path)).Scan(&existing)
		if err == nil {
			if existing != digest {
				t.Fatalf("migration drift %s", path)
			}
			continue
		}
		if err != sql.ErrNoRows {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, string(data)); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(name,digest) VALUES($1,$2)`, filepath.Base(path), digest); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		applied++
	}
	return applied
}
func assertCode(t *testing.T, err error, want coreapp.ErrorCode) {
	t.Helper()
	if coreapp.CodeOf(err) != want {
		t.Fatalf("code=%q want=%q err=%v", coreapp.CodeOf(err), want, err)
	}
}

func catalogDigest(t *testing.T, permissions []coreapp.PermissionCatalogEntry, menus []coreapp.MenuCatalogEntry) string {
	t.Helper()
	digest, err := coreapp.CanonicalCatalogDigest(permissions, menus)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

type reconcileFunc func(context.Context, coreapp.PolicyReconcileInput) error

func (f reconcileFunc) ReconcilePolicy(ctx context.Context, input coreapp.PolicyReconcileInput) error {
	return f(ctx, input)
}
