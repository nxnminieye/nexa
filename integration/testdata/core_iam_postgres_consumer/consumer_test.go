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
	if applied := applyMigrations(t, ctx, db); applied != 3 {
		t.Fatalf("fresh migrations = %d", applied)
	}
	if applied := applyMigrations(t, ctx, db); applied != 0 {
		t.Fatalf("second migrations = %d", applied)
	}
	assertAccessHashMigrationRejectsDuplicates(t, ctx, db)

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
	account, err := auth.Register(ctx, coreapp.LocalRegistration{Tenant: "bootstrap", Username: "owner", Email: "owner@example.test", DisplayName: "Bootstrap Owner", Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	assertUniqueAccessTokenHash(t, ctx, db, account.ID)
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
	accounts, err := iam.ListIdentityAccounts(ctx, coreapp.ListIdentityAccountsInput{ListQuery: coreapp.ListQuery{Keyword: "owner", Status: coreapp.IAMStatusEnabled}})
	if err != nil || accounts.Total != 1 || len(accounts.Items) != 1 || accounts.Items[0].ID != account.ID || accounts.Items[0].Email != "owner@example.test" || accounts.Items[0].DisplayName != "Bootstrap Owner" {
		t.Fatalf("account list=%#v err=%v", accounts, err)
	}
	accountReadback, err := iam.GetIdentityAccount(ctx, account.ID)
	if err != nil || accountReadback.Username != "owner" || accountReadback.Email != "owner@example.test" {
		t.Fatalf("account readback=%#v err=%v", accountReadback, err)
	}
	bob, err := auth.Register(ctx, coreapp.LocalRegistration{Tenant: "bootstrap", Username: "bob", Email: "bob@example.test", DisplayName: "Bob", Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := iam.ProvisionTenant(ctx, coreapp.ProvisionTenantInput{TenantCode: "tenant-b", DisplayName: "Tenant B", OwnerAccountID: bob.ID})
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := iam.ListTenants(ctx, coreapp.ListTenantsInput{ListQuery: coreapp.ListQuery{Keyword: "tenant-", Status: coreapp.IAMStatusEnabled, Limit: 10}})
	if err != nil || tenants.Total != 2 || len(tenants.Items) != 2 || tenants.Items[0].ID != tenantID || tenants.Items[1].ID != other.Tenant.ID {
		t.Fatalf("tenant list=%#v err=%v", tenants, err)
	}
	tenantPage, err := iam.ListTenants(ctx, coreapp.ListTenantsInput{ListQuery: coreapp.ListQuery{Keyword: "tenant-", Limit: 1, Offset: 1}})
	if err != nil || tenantPage.Total != 2 || len(tenantPage.Items) != 1 || tenantPage.Items[0].ID != other.Tenant.ID {
		t.Fatalf("tenant pagination=%#v err=%v", tenantPage, err)
	}
	updatedTenant, err := iam.UpdateTenant(ctx, coreapp.UpdateTenantInput{Actor: coreapp.SystemActor{System: true}, TenantID: tenantID, DisplayName: "Tenant A renamed", ExpectedVersion: provision.Tenant.Version})
	if err != nil || updatedTenant.DisplayName != "Tenant A renamed" || updatedTenant.Version != provision.Tenant.Version+1 {
		t.Fatalf("tenant update=%#v err=%v", updatedTenant, err)
	}
	tenantReadback, err := iam.GetTenant(ctx, tenantID)
	if err != nil || tenantReadback.DisplayName != "Tenant A renamed" {
		t.Fatalf("tenant readback=%#v err=%v", tenantReadback, err)
	}
	members, err := iam.ListTenantMembers(ctx, coreapp.ListTenantMembersInput{TenantID: tenantID, ListQuery: coreapp.ListQuery{Keyword: "Bootstrap", Status: coreapp.IAMStatusEnabled}})
	if err != nil || members.Total != 1 || len(members.Items) != 1 || members.Items[0].AccountID != account.ID || members.Items[0].AccountUsername != "owner" || members.Items[0].AccountEmail != "owner@example.test" || members.Items[0].AccountDisplayName != "Bootstrap Owner" {
		t.Fatalf("member list=%#v err=%v", members, err)
	}
	otherMembers, err := iam.ListTenantMembers(ctx, coreapp.ListTenantMembersInput{TenantID: other.Tenant.ID})
	if err != nil || otherMembers.Total != 1 || len(otherMembers.Items) != 1 || otherMembers.Items[0].AccountID != bob.ID || otherMembers.Items[0].AccountID == members.Items[0].AccountID {
		t.Fatalf("tenant member isolation first=%#v other=%#v err=%v", members, otherMembers, err)
	}
	role, err := iam.CreateTenantRole(ctx, coreapp.CreateTenantRoleInput{TenantID: tenantID, Code: "operator", DisplayName: "Operator"})
	if err != nil {
		t.Fatal(err)
	}
	role, err = iam.UpdateTenantRole(ctx, coreapp.UpdateTenantRoleInput{TenantID: tenantID, RoleID: role.ID, DisplayName: "Operator updated", ExpectedVersion: role.Version})
	if err != nil {
		t.Fatal(err)
	}
	roleReadback, err := iam.GetTenantRole(ctx, coreapp.TenantRoleKey{TenantID: tenantID, RoleID: role.ID})
	if err != nil || roleReadback.DisplayName != "Operator updated" || roleReadback.Version != role.Version {
		t.Fatalf("role update readback=%#v role=%#v err=%v", roleReadback, role, err)
	}
	role, err = iam.SetTenantRoleStatus(ctx, coreapp.SetTenantRoleStatusInput{TenantID: tenantID, RoleID: role.ID, Status: coreapp.IAMStatusDisabled, ExpectedVersion: role.Version})
	if err != nil {
		t.Fatal(err)
	}
	roleReadback, err = iam.GetTenantRole(ctx, coreapp.TenantRoleKey{TenantID: tenantID, RoleID: role.ID})
	if err != nil || roleReadback.Status != coreapp.IAMStatusDisabled || roleReadback.Version != role.Version {
		t.Fatalf("role status readback=%#v role=%#v err=%v", roleReadback, role, err)
	}
	role, err = iam.SetTenantRoleStatus(ctx, coreapp.SetTenantRoleStatusInput{TenantID: tenantID, RoleID: role.ID, Status: coreapp.IAMStatusEnabled, ExpectedVersion: role.Version})
	if err != nil {
		t.Fatal(err)
	}
	roleReadback, err = iam.GetTenantRole(ctx, coreapp.TenantRoleKey{TenantID: tenantID, RoleID: role.ID})
	if err != nil || roleReadback.Status != coreapp.IAMStatusEnabled || roleReadback.Version != role.Version {
		t.Fatalf("role restore readback=%#v role=%#v err=%v", roleReadback, role, err)
	}
	if role.Version <= 1 {
		t.Fatalf("role version = %d, want a prior version for stale-write coverage", role.Version)
	}
	staleVersion := role.Version - 1
	_, err = iam.UpdateTenantRole(ctx, coreapp.UpdateTenantRoleInput{TenantID: tenantID, RoleID: role.ID, DisplayName: "Stale update", ExpectedVersion: staleVersion})
	assertCode(t, err, coreapp.CodeConcurrentWrite)
	_, err = iam.SetTenantRoleStatus(ctx, coreapp.SetTenantRoleStatusInput{TenantID: tenantID, RoleID: role.ID, Status: coreapp.IAMStatusDisabled, ExpectedVersion: staleVersion})
	assertCode(t, err, coreapp.CodeConcurrentWrite)
	_, err = iam.UpdateTenantRole(ctx, coreapp.UpdateTenantRoleInput{TenantID: other.Tenant.ID, RoleID: role.ID, DisplayName: "Wrong tenant", ExpectedVersion: role.Version})
	assertCode(t, err, coreapp.CodeNotFound)
	_, err = iam.SetTenantRoleStatus(ctx, coreapp.SetTenantRoleStatusInput{TenantID: other.Tenant.ID, RoleID: role.ID, Status: coreapp.IAMStatusDisabled, ExpectedVersion: role.Version})
	assertCode(t, err, coreapp.CodeNotFound)
	roleReadback, err = iam.GetTenantRole(ctx, coreapp.TenantRoleKey{TenantID: tenantID, RoleID: role.ID})
	if err != nil || roleReadback.DisplayName != role.DisplayName || roleReadback.Status != role.Status || roleReadback.Version != role.Version {
		t.Fatalf("rejected role mutations changed state: readback=%#v role=%#v err=%v", roleReadback, role, err)
	}
	member, err := iam.ReplaceManualRoles(ctx, coreapp.ReplaceManualRolesInput{TenantID: tenantID, MemberID: provision.Owner.ID, RoleCodes: []string{"operator"}, ExpectedVersion: provision.Owner.Version})
	if err != nil {
		t.Fatal(err)
	}
	if len(member.ManualRoleCodes) != 1 || !member.ManagedOwnerGrant {
		t.Fatalf("grant isolation = %#v", member)
	}
	memberPage, err := iam.ListTenantMembers(ctx, coreapp.ListTenantMembersInput{TenantID: tenantID})
	if err != nil || memberPage.Total != 1 || len(memberPage.Items) != 1 || len(memberPage.Items[0].ManualRoleCodes) != 1 || memberPage.Items[0].ManualRoleCodes[0] != "operator" {
		t.Fatalf("member role readback=%#v err=%v", memberPage, err)
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
	roles, err := iam.ListTenantRoles(ctx, coreapp.ListTenantRolesInput{TenantID: tenantID, ListQuery: coreapp.ListQuery{Keyword: "operator"}})
	if err != nil || roles.Total != 1 || len(roles.Items) != 1 || roles.Items[0].ID != role.ID || len(roles.Items[0].PermissionCodes) != 1 || roles.Items[0].PermissionCodes[0] != "read" || len(roles.Items[0].MenuCodes) != 1 || roles.Items[0].MenuCodes[0] != "home" {
		t.Fatalf("role grant readback=%#v err=%v", roles, err)
	}
	roleReadback, err = iam.GetTenantRole(ctx, coreapp.TenantRoleKey{TenantID: tenantID, RoleID: role.ID})
	if err != nil || len(roleReadback.PermissionCodes) != 1 || len(roleReadback.MenuCodes) != 1 {
		t.Fatalf("role get=%#v err=%v", roleReadback, err)
	}
	otherRoles, err := iam.ListTenantRoles(ctx, coreapp.ListTenantRolesInput{TenantID: other.Tenant.ID, ListQuery: coreapp.ListQuery{Keyword: "operator"}})
	if err != nil || otherRoles.Total != 0 || len(otherRoles.Items) != 0 {
		t.Fatalf("role tenant isolation=%#v err=%v", otherRoles, err)
	}
	_, err = iam.GetTenantRole(ctx, coreapp.TenantRoleKey{TenantID: other.Tenant.ID, RoleID: role.ID})
	assertCode(t, err, coreapp.CodeNotFound)
	menus, err := iam.ListMenus(ctx, coreapp.ListMenusInput{ListQuery: coreapp.ListQuery{Status: coreapp.IAMStatusEnabled}})
	if err != nil || menus.Total != 2 || len(menus.Items) != 2 {
		t.Fatalf("menu list=%#v err=%v", menus, err)
	}
	menuReadback, err := iam.GetMenu(ctx, "home")
	if err != nil || menuReadback.DisplayName != "Home" || menuReadback.SourceID != "source-a" {
		t.Fatalf("menu get=%#v err=%v", menuReadback, err)
	}
	permissions, err := iam.ListPermissions(ctx, coreapp.ListPermissionsInput{ListQuery: coreapp.ListQuery{Status: coreapp.IAMStatusEnabled}})
	if err != nil || permissions.Total != 2 || len(permissions.Items) != 2 {
		t.Fatalf("permission list=%#v err=%v", permissions, err)
	}
	permissionReadback, err := iam.GetPermission(ctx, "read")
	if err != nil || permissionReadback.SourceID != "source-a" || permissionReadback.ResourceCode != "read" {
		t.Fatalf("permission get=%#v err=%v", permissionReadback, err)
	}
	access, err := coreapp.NewAccessAuthenticator(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	exerciseAccessPrincipal(t, ctx, db, auth, access, iam, account, bob, provision, other)

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

func assertUniqueAccessTokenHash(t *testing.T, ctx context.Context, db *sql.DB, accountID coreapp.IdentityAccountID) {
	t.Helper()
	const statement = `INSERT INTO auth_sessions(session_id,tenant_id,identity_account_id,access_token_hash,refresh_token_hash,access_expires_at,refresh_expires_at)
SELECT $1,id,$2,'duplicate-access-hash',$3,now()+interval '1 minute',now()+interval '1 hour' FROM tenants WHERE code='bootstrap'`
	if _, err := db.ExecContext(ctx, statement, "unique-access-1", accountID, "unique-refresh-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, statement, "unique-access-2", accountID, "unique-refresh-2"); err == nil {
		t.Fatal("duplicate access token hash was accepted")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE access_token_hash='duplicate-access-hash'`); err != nil {
		t.Fatal(err)
	}
}

func exerciseAccessPrincipal(t *testing.T, ctx context.Context, db *sql.DB, auth *coreapp.LocalAuthenticator, access *coreapp.AccessAuthenticator, iam *coreapp.IAMService, account, bob coreapp.IdentityAccount, provision, other coreapp.ProvisionTenantResult) {
	t.Helper()
	session, err := auth.Login(ctx, coreapp.LocalLogin{Tenant: provision.Tenant.Code, Username: account.Username, Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := access.Authenticate(ctx, session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.SessionID != session.ID || principal.TenantID != provision.Tenant.ID || principal.TenantCode != provision.Tenant.Code || principal.MemberID != provision.Owner.ID || principal.Account.ID != account.ID {
		t.Fatalf("access principal identity=%#v", principal)
	}
	if fmt.Sprint(principal.RoleCodes) != "[operator tenant-owner]" || fmt.Sprint(principal.PermissionCodes) != "[read]" || fmt.Sprint(principal.MenuCodes) != "[home]" {
		t.Fatalf("access principal grants=%#v", principal)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO permissions(code,description) VALUES('legacy-rbac','must not authorize')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.tenant_id=$1 AND r.code='operator' AND p.code='legacy-rbac'`, provision.Tenant.ID); err != nil {
		t.Fatal(err)
	}
	principal, err = access.Authenticate(ctx, session.AccessToken)
	if err != nil || fmt.Sprint(principal.PermissionCodes) != "[read]" {
		t.Fatalf("legacy role_permissions affected access principal=%#v err=%v", principal, err)
	}

	if _, err = db.ExecContext(ctx, `
INSERT INTO menus(source_owner,source_key,source_digest,code,parent_code,name,status)
VALUES ('access-test','admin','v1','admin','root','Admin','enabled'),
       ('access-test','root','v1','root','admin','Root','enabled');
UPDATE menus SET parent_code='admin' WHERE code='home'`); err != nil {
		t.Fatal(err)
	}
	principal, err = access.Authenticate(ctx, session.AccessToken)
	if err != nil || fmt.Sprint(principal.MenuCodes) != "[admin home root]" {
		t.Fatalf("menu ancestors/cycle principal=%#v err=%v", principal, err)
	}

	for _, disabled := range []struct {
		name, disable, restore                string
		wantRoles, wantPermissions, wantMenus string
	}{
		{name: "role", disable: `UPDATE roles SET status='disabled' WHERE tenant_id=$1 AND code='operator'`, restore: `UPDATE roles SET status='enabled' WHERE tenant_id=$1 AND code='operator'`, wantRoles: "[tenant-owner]", wantPermissions: "[]", wantMenus: "[]"},
		{name: "action", disable: `UPDATE permission_actions SET status='disabled' WHERE code='read'`, restore: `UPDATE permission_actions SET status='enabled' WHERE code='read'`, wantRoles: "[operator tenant-owner]", wantPermissions: "[]", wantMenus: "[admin home root]"},
		{name: "resource", disable: `UPDATE permission_resources SET status='disabled' WHERE code='read'`, restore: `UPDATE permission_resources SET status='enabled' WHERE code='read'`, wantRoles: "[operator tenant-owner]", wantPermissions: "[]", wantMenus: "[admin home root]"},
		{name: "menu", disable: `UPDATE menus SET status='disabled' WHERE code='admin'`, restore: `UPDATE menus SET status='enabled' WHERE code='admin'`, wantRoles: "[operator tenant-owner]", wantPermissions: "[read]", wantMenus: "[home]"},
	} {
		t.Run("access-disabled-"+disabled.name, func(t *testing.T) {
			arguments := []any(nil)
			if disabled.name == "role" {
				arguments = append(arguments, provision.Tenant.ID)
			}
			if _, err := db.ExecContext(ctx, disabled.disable, arguments...); err != nil {
				t.Fatal(err)
			}
			got, err := access.Authenticate(ctx, session.AccessToken)
			if err != nil || fmt.Sprint(got.RoleCodes) != disabled.wantRoles || fmt.Sprint(got.PermissionCodes) != disabled.wantPermissions || fmt.Sprint(got.MenuCodes) != disabled.wantMenus {
				t.Fatalf("principal=%#v err=%v", got, err)
			}
			if _, err = db.ExecContext(ctx, disabled.restore, arguments...); err != nil {
				t.Fatal(err)
			}
		})
	}

	withoutBindings, err := auth.Login(ctx, coreapp.LocalLogin{Tenant: other.Tenant.Code, Username: bob.Username, Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := access.Authenticate(ctx, withoutBindings.AccessToken)
	if err != nil || len(empty.PermissionCodes) != 0 || len(empty.MenuCodes) != 0 {
		t.Fatalf("empty grants principal=%#v err=%v", empty, err)
	}

	exerciseIAMHTTPTransport(t, iam, access, session.AccessToken, withoutBindings.AccessToken, provision.Tenant.ID, other.Tenant.ID, provision.Owner.ID, account.ID)

	assertAccessUnavailable := func(name, statement string, arguments ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, arguments...); err != nil {
			t.Fatal(err)
		}
		_, err := access.Authenticate(ctx, session.AccessToken)
		assertCode(t, err, coreapp.CodeInvalidCredentials)
	}
	assertAccessUnavailable("tenant mismatch", `UPDATE auth_sessions SET tenant_id=$1 WHERE session_id=$2`, other.Tenant.ID, session.ID)
	if _, err = db.ExecContext(ctx, `UPDATE auth_sessions SET tenant_id=$1 WHERE session_id=$2`, provision.Tenant.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct {
		table, predicate string
		id               any
	}{
		{table: "tenants", predicate: "id", id: provision.Tenant.ID},
		{table: "tenant_members", predicate: "id", id: provision.Owner.ID},
		{table: "identity_accounts", predicate: "id", id: account.ID},
	} {
		assertAccessUnavailable(state.table, `UPDATE `+state.table+` SET status='disabled' WHERE `+state.predicate+`=$1`, state.id)
		if _, err = db.ExecContext(ctx, `UPDATE `+state.table+` SET status='enabled' WHERE `+state.predicate+`=$1`, state.id); err != nil {
			t.Fatal(err)
		}
	}

	rotated, err := auth.Refresh(ctx, session.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	_, err = access.Authenticate(ctx, session.AccessToken)
	assertCode(t, err, coreapp.CodeInvalidCredentials)
	if _, err = access.Authenticate(ctx, rotated.AccessToken); err != nil {
		t.Fatal(err)
	}
	expired, err := auth.Login(ctx, coreapp.LocalLogin{Tenant: provision.Tenant.Code, Username: account.Username, Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE auth_sessions SET access_expires_at=now()-interval '1 second' WHERE session_id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	_, err = access.Authenticate(ctx, expired.AccessToken)
	assertCode(t, err, coreapp.CodeInvalidCredentials)
	if err = auth.Revoke(ctx, rotated.ID); err != nil {
		t.Fatal(err)
	}
	_, err = access.Authenticate(ctx, rotated.AccessToken)
	assertCode(t, err, coreapp.CodeInvalidCredentials)
}

func assertAccessHashMigrationRejectsDuplicates(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	data, err := os.ReadFile("migrations/003_auth_session_access_hash_unique.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE duplicate_auth_sessions(access_token_hash TEXT NOT NULL); INSERT INTO duplicate_auth_sessions VALUES('same'),('same'); ALTER TABLE auth_sessions RENAME TO real_auth_sessions; ALTER TABLE duplicate_auth_sessions RENAME TO auth_sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, string(data)); err == nil {
		t.Fatal("access hash migration accepted existing duplicates")
	}
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
