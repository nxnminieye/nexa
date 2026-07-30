package coreapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIAMProvisionTenantNormalizesAndReconcilesAfterAtomicStoreSuccess(t *testing.T) {
	events := []string{}
	store := &fakeIAMStore{events: &events}
	reconciler := &recordingReconciler{events: &events}
	service := newTestIAMService(t, store, reconciler)

	input := ProvisionTenantInput{
		TenantCode: " tenant-a ", DisplayName: " Tenant A ", OwnerAccountID: "account-a",
		OwnerUsername: " owner ", OwnerEmail: " owner@example.test ", OwnerName: " Owner ",
		DefaultRouter: " /home ",
	}
	first, err := service.ProvisionTenant(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ProvisionTenant(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("idempotent result changed: %#v != %#v", first, second)
	}
	if got := store.provisionInputs[0]; got.TenantCode != "tenant-a" || got.DisplayName != "Tenant A" || got.OwnerUsername != "owner" || got.DefaultRouter != "/home" {
		t.Fatalf("unnormalized store input: %#v", got)
	}
	if !reflect.DeepEqual(events, []string{"store.provision", "reconcile.tenant", "store.provision", "reconcile.tenant"}) {
		t.Fatalf("sequencing = %#v", events)
	}

	events = nil
	store.err = errors.New("private database detail")
	_, err = service.ProvisionTenant(context.Background(), input)
	assertIAMCodeAndRedaction(t, err, CodeStoreFailure, "private database detail")
	if len(events) != 1 || events[0] != "store.provision" {
		t.Fatalf("failed mutation reconciled: %#v", events)
	}
}

func TestIAMMemberTenantAndOwnerGuards(t *testing.T) {
	store := &fakeIAMStore{}
	service := newTestIAMService(t, store, &recordingReconciler{})

	store.member = TenantMember{ID: "member-a", TenantID: "102", AccountID: "account-a", Status: IAMStatusEnabled, Version: 1}
	_, err := service.GetTenantMember(context.Background(), TenantMemberKey{TenantID: "101", MemberID: "member-a"})
	assertCode(t, err, CodeFailedPrecondition)

	store.member = TenantMember{ID: "member-a", TenantID: "101", AccountID: "account-a", Status: IAMStatusEnabled, ManagedOwnerGrant: true, Version: 1}
	_, err = service.ReplaceManualRoles(context.Background(), ReplaceManualRolesInput{TenantID: "101", MemberID: "member-a", RoleCodes: []string{" role-b ", "role-a", "role-a"}, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !store.member.ManagedOwnerGrant {
		t.Fatal("manual role replacement removed managed owner grant")
	}
	if got := store.lastManualInput.RoleCodes; !reflect.DeepEqual(got, []string{"role-a", "role-b"}) {
		t.Fatalf("role ids = %#v", got)
	}
	if !store.lastManualInput.PreserveManagedGrants {
		t.Fatal("atomic managed-owner guard not requested")
	}

	store.err = ErrStoreFailedPrecondition
	_, err = service.SetTenantMemberStatus(context.Background(), SetTenantMemberStatusInput{TenantID: "101", MemberID: "member-a", Status: IAMStatusDisabled, ExpectedVersion: 1})
	assertCode(t, err, CodeFailedPrecondition)
	if !store.lastStatusInput.PreserveLastEnabledOwner {
		t.Fatal("last enabled owner guard not requested")
	}
}

func TestIAMRoleManagedGuardAndCanonicalReplacements(t *testing.T) {
	store := &fakeIAMStore{role: TenantRole{ID: "role-a", TenantID: "101", Managed: true, Version: 2}}
	service := newTestIAMService(t, store, &recordingReconciler{})

	_, err := service.ReplaceRolePermissions(context.Background(), ReplaceRolePermissionsInput{TenantID: "101", RoleID: "role-a", PermissionCodes: []string{"write"}, ExpectedVersion: 2})
	assertCode(t, err, CodePermissionDenied)
	if store.replacePermissionCalls != 0 {
		t.Fatal("managed role reached mutation")
	}

	store.role.Managed = false
	store.role.PermissionCodes = []string{"read", "write"}
	_, err = service.ReplaceRolePermissions(context.Background(), ReplaceRolePermissionsInput{
		TenantID: "101", RoleID: "role-a", PermissionCodes: []string{" write ", "read", "write"}, ExpectedVersion: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.lastPermissionInput.PermissionCodes; !reflect.DeepEqual(got, []string{"read", "write"}) {
		t.Fatalf("permission codes = %#v", got)
	}
	if !store.lastPermissionInput.RejectManaged {
		t.Fatal("atomic managed role guard not requested")
	}

	store.err = ErrStoreConcurrentWrite
	_, err = service.UpdateTenantRole(context.Background(), UpdateTenantRoleInput{TenantID: "101", RoleID: "role-a", DisplayName: "new", ExpectedVersion: 2})
	assertCode(t, err, CodeConcurrentWrite)
}

func TestIAMSystemAccountMutationsHashResetAndRevokeSessions(t *testing.T) {
	store := &fakeIAMStore{}
	hasher := &iamHasher{hash: "encoded-hash"}
	service, err := NewIAMService(store, hasher, &recordingReconciler{})
	if err != nil {
		t.Fatal(err)
	}

	err = service.ResetAccountPassword(context.Background(), ResetAccountPasswordInput{Actor: SystemActor{}, AccountID: "account-a", Password: []byte("secret")})
	assertCode(t, err, CodePermissionDenied)
	if store.resetCalls != 0 || hasher.calls != 0 {
		t.Fatal("non-system actor reached password mutation")
	}

	err = service.ResetAccountPassword(context.Background(), ResetAccountPasswordInput{Actor: SystemActor{System: true}, AccountID: "account-a", Password: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if store.lastReset.PasswordHash != "encoded-hash" || !store.lastReset.RevokeSessions {
		t.Fatalf("reset command = %#v", store.lastReset)
	}

	_, err = service.SetAccountStatus(context.Background(), SetAccountStatusInput{Actor: SystemActor{}, AccountID: "account-a", Status: IAMStatusDisabled})
	assertCode(t, err, CodePermissionDenied)
}

func TestIAMSetTenantStatusRequiresSystemActorAndReconciles(t *testing.T) {
	store := &fakeIAMStore{}
	reconciler := &recordingReconciler{}
	service := newTestIAMService(t, store, reconciler)

	_, err := service.SetTenantStatus(context.Background(), SetTenantStatusInput{TenantID: "101", Status: IAMStatusDisabled, ExpectedVersion: 1})
	assertCode(t, err, CodePermissionDenied)
	if store.setTenantStatusCalls != 0 {
		t.Fatal("non-system actor reached tenant mutation")
	}
	tenant, err := service.SetTenantStatus(context.Background(), SetTenantStatusInput{Actor: SystemActor{System: true}, TenantID: " 101 ", Status: IAMStatusDisabled, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tenant.ID != "101" || len(reconciler.calls) != 1 || reconciler.calls[0].Kind != PolicyResourceTenant {
		t.Fatalf("tenant/reconcile = %#v %#v", tenant, reconciler.calls)
	}
}

func TestIAMManagementQueriesNormalizeFiltersAndPreserveStructuredReadback(t *testing.T) {
	account := IdentityAccount{ID: "account-a", SourceCode: "local", Username: "alice", Email: "alice@example.test", DisplayName: "Alice", Status: IAMStatusEnabled}
	member := TenantMember{
		ID: "member-a", TenantID: "101", TenantCode: "tenant-a", AccountID: account.ID,
		AccountUsername: account.Username, AccountEmail: account.Email, AccountDisplayName: account.DisplayName,
		AccountSourceCode: account.SourceCode, AccountExternalSubject: account.ExternalSubject,
		Status: IAMStatusEnabled, ManualRoleCodes: []string{"operator"}, Version: 3,
	}
	role := TenantRole{ID: "role-a", TenantID: "101", Code: "operator", DisplayName: "Operator", Status: IAMStatusEnabled, PermissionCodes: []string{"read"}, MenuCodes: []string{"home"}, Version: 2}
	store := &fakeIAMStore{
		accountPage: IdentityAccountPage{Total: 1, Items: []IdentityAccount{account}},
		tenantPage:  TenantPage{Total: 1, Items: []Tenant{{ID: "101", Code: "tenant-a", DisplayName: "Tenant A", Status: IAMStatusEnabled, Version: 1}}},
		memberPage:  TenantMemberPage{Total: 1, Items: []TenantMember{member}}, member: member,
		rolePage: TenantRolePage{Total: 1, Items: []TenantRole{role}}, role: role,
		menuPage:       MenuPage{Total: 1, Items: []Menu{{Code: "home", DisplayName: "Home", Status: IAMStatusEnabled}}},
		permissionPage: PermissionPage{Total: 1, Items: []Permission{{Code: "read", DisplayName: "read", Status: IAMStatusEnabled}}},
	}
	service := newTestIAMService(t, store, &recordingReconciler{})

	accounts, err := service.ListIdentityAccounts(context.Background(), ListIdentityAccountsInput{ListQuery: ListQuery{Keyword: " alice ", Status: IAMStatusEnabled, Offset: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if accounts.Total != 1 || !reflect.DeepEqual(accounts.Items, []IdentityAccount{account}) || store.lastAccountList.Keyword != "alice" || store.lastAccountList.Limit != 50 || store.lastAccountList.Offset != 4 {
		t.Fatalf("accounts=%#v input=%#v", accounts, store.lastAccountList)
	}

	members, err := service.ListTenantMembers(context.Background(), ListTenantMembersInput{TenantID: " 101 ", ListQuery: ListQuery{Keyword: " Alice ", Status: IAMStatusEnabled, Limit: 25}})
	if err != nil {
		t.Fatal(err)
	}
	if members.Total != 1 || members.Items[0].AccountUsername != "alice" || !reflect.DeepEqual(members.Items[0].ManualRoleCodes, []string{"operator"}) || store.lastMemberList.TenantID != "101" || store.lastMemberList.Keyword != "Alice" {
		t.Fatalf("members=%#v input=%#v", members, store.lastMemberList)
	}

	roles, err := service.ListTenantRoles(context.Background(), ListTenantRolesInput{TenantID: "101", ListQuery: ListQuery{Limit: 201}})
	assertCode(t, err, CodeInvalidInput)
	if roles.Total != 0 {
		t.Fatalf("invalid role query returned %#v", roles)
	}
	readRole, err := service.GetTenantRole(context.Background(), TenantRoleKey{TenantID: "101", RoleID: "role-a"})
	if err != nil || !reflect.DeepEqual(readRole.PermissionCodes, []string{"read"}) || !reflect.DeepEqual(readRole.MenuCodes, []string{"home"}) {
		t.Fatalf("role=%#v err=%v", readRole, err)
	}

	menus, err := service.ListMenus(context.Background(), ListMenusInput{ListQuery: ListQuery{Status: IAMStatusEnabled}})
	if err != nil || menus.Total != 1 {
		t.Fatalf("menus=%#v err=%v", menus, err)
	}
	permissions, err := service.ListPermissions(context.Background(), ListPermissionsInput{})
	if err != nil || permissions.Total != 1 {
		t.Fatalf("permissions=%#v err=%v", permissions, err)
	}
}

func TestIAMUpdateTenantDisplayNameRequiresSystemActorAndVersion(t *testing.T) {
	store := &fakeIAMStore{}
	reconciler := &recordingReconciler{}
	service := newTestIAMService(t, store, reconciler)

	_, err := service.UpdateTenant(context.Background(), UpdateTenantInput{TenantID: "101", DisplayName: "New name", ExpectedVersion: 1})
	assertCode(t, err, CodePermissionDenied)
	if store.updateTenantCalls != 0 {
		t.Fatal("non-system actor reached tenant update")
	}
	tenant, err := service.UpdateTenant(context.Background(), UpdateTenantInput{Actor: SystemActor{System: true}, TenantID: " 101 ", DisplayName: " New name ", ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tenant.ID != "101" || tenant.DisplayName != "New name" || tenant.Version != 2 || store.lastUpdateTenant.DisplayName != "New name" || len(reconciler.calls) != 1 {
		t.Fatalf("tenant=%#v input=%#v reconcile=%#v", tenant, store.lastUpdateTenant, reconciler.calls)
	}
}

func TestIAMStableStoreErrorsAreRedacted(t *testing.T) {
	cases := []struct {
		err  error
		code ErrorCode
	}{
		{ErrStoreNotFound, CodeNotFound}, {ErrStoreConflict, CodeConflict},
		{ErrStoreInvalidInput, CodeInvalidInput},
		{ErrStoreConcurrentWrite, CodeConcurrentWrite}, {ErrStorePermissionDenied, CodePermissionDenied},
		{ErrStoreFailedPrecondition, CodeFailedPrecondition},
	}
	for _, test := range cases {
		store := &fakeIAMStore{err: errors.Join(test.err, errors.New("private detail"))}
		service := newTestIAMService(t, store, &recordingReconciler{})
		_, err := service.CreateTenantRole(context.Background(), CreateTenantRoleInput{TenantID: "101", Code: "role", DisplayName: "Role"})
		assertIAMCodeAndRedaction(t, err, test.code, "private detail")
	}
}

func TestLocalLoginMapsUnavailableCredentialToInvalidCredentials(t *testing.T) {
	store := unavailableIdentityStore{memoryStore: newMemoryStore()}
	auth, err := NewLocalAuthenticator(store, &iamHasher{}, SessionOptions{
		AccessTTL: 1, RefreshTTL: 2, TokenBytes: 16, Clock: ClockFunc(func() time.Time { return time.Time{} }),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.Login(context.Background(), LocalLogin{Tenant: "tenant-a", Username: "alice", Password: []byte("secret")})
	assertCode(t, err, CodeInvalidCredentials)
}

func newTestIAMService(t *testing.T, store IAMStore, reconciler PolicyReconciler) *IAMService {
	t.Helper()
	service, err := NewIAMService(store, &iamHasher{hash: "hash"}, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func assertIAMCodeAndRedaction(t *testing.T, err error, code ErrorCode, forbidden string) {
	t.Helper()
	assertCode(t, err, code)
	if strings.Contains(err.Error(), forbidden) {
		t.Fatalf("error leaked store detail: %q", err)
	}
}

type iamHasher struct {
	hash  string
	calls int
}

func (h *iamHasher) Hash([]byte) (string, error) { h.calls++; return h.hash, nil }
func (h *iamHasher) Verify(string, []byte) error { return nil }

type recordingReconciler struct {
	events *[]string
	calls  []PolicyReconcileInput
	err    error
}

func (r *recordingReconciler) ReconcilePolicy(_ context.Context, input PolicyReconcileInput) error {
	r.calls = append(r.calls, input)
	if r.events != nil {
		*r.events = append(*r.events, "reconcile."+string(input.Kind))
	}
	return r.err
}

type unavailableIdentityStore struct{ *memoryStore }

func (s unavailableIdentityStore) FindLocalAccount(context.Context, LocalAccountKey) (LocalCredential, error) {
	return LocalCredential{}, ErrStoreCredentialUnavailable
}

type fakeIAMStore struct {
	err                    error
	events                 *[]string
	provisionInputs        []ProvisionTenantStoreInput
	member                 TenantMember
	role                   TenantRole
	lastStatusInput        SetTenantMemberStatusStoreInput
	lastManualInput        ReplaceManualRolesStoreInput
	lastPermissionInput    ReplaceRolePermissionsStoreInput
	lastReset              ResetLocalPasswordStoreInput
	replaceManualCalls     int
	replacePermissionCalls int
	resetCalls             int
	setTenantStatusCalls   int
	updateTenantCalls      int
	accountPage            IdentityAccountPage
	tenantPage             TenantPage
	memberPage             TenantMemberPage
	rolePage               TenantRolePage
	menuPage               MenuPage
	permissionPage         PermissionPage
	lastAccountList        ListIdentityAccountsInput
	lastMemberList         ListTenantMembersInput
	lastUpdateTenant       UpdateTenantStoreInput
}

func (s *fakeIAMStore) event(value string) {
	if s.events != nil {
		*s.events = append(*s.events, value)
	}
}
func (s *fakeIAMStore) ProvisionTenant(_ context.Context, input ProvisionTenantStoreInput) (ProvisionTenantResult, error) {
	s.event("store.provision")
	s.provisionInputs = append(s.provisionInputs, input)
	if s.err != nil {
		return ProvisionTenantResult{}, s.err
	}
	return ProvisionTenantResult{Tenant: Tenant{ID: "101", Code: input.TenantCode, DisplayName: input.DisplayName, Status: IAMStatusEnabled, Version: 1}, Owner: TenantMember{ID: "owner", TenantID: "101", TenantCode: input.TenantCode, AccountID: input.OwnerAccountID, Status: IAMStatusEnabled, ManagedOwnerGrant: true, Version: 1}}, nil
}
func (s *fakeIAMStore) SetTenantStatus(_ context.Context, input SetTenantStatusStoreInput) (Tenant, error) {
	s.setTenantStatusCalls++
	if s.err != nil {
		return Tenant{}, s.err
	}
	return Tenant{ID: input.TenantID, Status: input.Status, Version: input.ExpectedVersion + 1}, nil
}
func (s *fakeIAMStore) ListIdentityAccounts(_ context.Context, input ListIdentityAccountsInput) (IdentityAccountPage, error) {
	s.lastAccountList = input
	return s.accountPage, s.err
}
func (s *fakeIAMStore) GetIdentityAccount(_ context.Context, id IdentityAccountID) (IdentityAccount, error) {
	if s.err != nil {
		return IdentityAccount{}, s.err
	}
	if len(s.accountPage.Items) == 0 {
		return IdentityAccount{ID: id}, nil
	}
	return s.accountPage.Items[0], nil
}
func (s *fakeIAMStore) ListTenants(context.Context, ListTenantsInput) (TenantPage, error) {
	return s.tenantPage, s.err
}
func (s *fakeIAMStore) GetTenant(_ context.Context, id string) (Tenant, error) {
	if s.err != nil {
		return Tenant{}, s.err
	}
	if len(s.tenantPage.Items) == 0 {
		return Tenant{ID: id}, nil
	}
	return s.tenantPage.Items[0], nil
}
func (s *fakeIAMStore) UpdateTenant(_ context.Context, input UpdateTenantStoreInput) (Tenant, error) {
	s.updateTenantCalls++
	s.lastUpdateTenant = input
	if s.err != nil {
		return Tenant{}, s.err
	}
	return Tenant{ID: input.TenantID, DisplayName: input.DisplayName, Status: IAMStatusEnabled, Version: input.ExpectedVersion + 1}, nil
}
func (s *fakeIAMStore) ListTenantMembers(_ context.Context, input ListTenantMembersInput) (TenantMemberPage, error) {
	s.lastMemberList = input
	if s.err != nil {
		return TenantMemberPage{}, s.err
	}
	return s.memberPage, nil
}
func (s *fakeIAMStore) GetTenantMember(context.Context, TenantMemberKey) (TenantMember, error) {
	if s.err != nil {
		return TenantMember{}, s.err
	}
	return s.member, nil
}
func (s *fakeIAMStore) SetTenantMemberStatus(_ context.Context, input SetTenantMemberStatusStoreInput) (TenantMember, error) {
	s.lastStatusInput = input
	if s.err != nil {
		return TenantMember{}, s.err
	}
	s.member.Status = input.Status
	return s.member, nil
}
func (s *fakeIAMStore) ReplaceManualRoleGrants(_ context.Context, input ReplaceManualRolesStoreInput) (TenantMember, error) {
	s.replaceManualCalls++
	s.lastManualInput = input
	if s.err != nil {
		return TenantMember{}, s.err
	}
	s.member.ManualRoleCodes = append([]string(nil), input.RoleCodes...)
	return s.member, nil
}
func (s *fakeIAMStore) GetTenantRole(context.Context, TenantRoleKey) (TenantRole, error) {
	if s.err != nil {
		return TenantRole{}, s.err
	}
	return s.role, nil
}
func (s *fakeIAMStore) ListTenantRoles(context.Context, ListTenantRolesInput) (TenantRolePage, error) {
	return s.rolePage, s.err
}
func (s *fakeIAMStore) ListMenus(context.Context, ListMenusInput) (MenuPage, error) {
	return s.menuPage, s.err
}
func (s *fakeIAMStore) GetMenu(_ context.Context, code string) (Menu, error) {
	if s.err != nil {
		return Menu{}, s.err
	}
	if len(s.menuPage.Items) == 0 {
		return Menu{Code: code}, nil
	}
	return s.menuPage.Items[0], nil
}
func (s *fakeIAMStore) ListPermissions(context.Context, ListPermissionsInput) (PermissionPage, error) {
	return s.permissionPage, s.err
}
func (s *fakeIAMStore) GetPermission(_ context.Context, code string) (Permission, error) {
	if s.err != nil {
		return Permission{}, s.err
	}
	if len(s.permissionPage.Items) == 0 {
		return Permission{Code: code}, nil
	}
	return s.permissionPage.Items[0], nil
}
func (s *fakeIAMStore) CreateTenantRole(_ context.Context, input CreateTenantRoleStoreInput) (TenantRole, error) {
	if s.err != nil {
		return TenantRole{}, s.err
	}
	return TenantRole{ID: "role", TenantID: input.TenantID, Code: input.Code, DisplayName: input.DisplayName, Status: IAMStatusEnabled, Version: 1}, nil
}
func (s *fakeIAMStore) UpdateTenantRole(_ context.Context, input UpdateTenantRoleStoreInput) (TenantRole, error) {
	if s.err != nil {
		return TenantRole{}, s.err
	}
	s.role.DisplayName = input.DisplayName
	return s.role, nil
}
func (s *fakeIAMStore) SetTenantRoleStatus(_ context.Context, input SetTenantRoleStatusStoreInput) (TenantRole, error) {
	if s.err != nil {
		return TenantRole{}, s.err
	}
	s.role.Status = input.Status
	return s.role, nil
}
func (s *fakeIAMStore) ReplaceRolePermissions(_ context.Context, input ReplaceRolePermissionsStoreInput) (TenantRole, error) {
	s.replacePermissionCalls++
	s.lastPermissionInput = input
	if s.err != nil {
		return TenantRole{}, s.err
	}
	s.role.PermissionCodes = append([]string(nil), input.PermissionCodes...)
	return s.role, nil
}
func (s *fakeIAMStore) ReplaceRoleMenus(_ context.Context, input ReplaceRoleMenusStoreInput) (TenantRole, error) {
	if s.err != nil {
		return TenantRole{}, s.err
	}
	s.role.MenuCodes = append([]string(nil), input.MenuCodes...)
	return s.role, nil
}
func (s *fakeIAMStore) SetIdentityAccountStatus(_ context.Context, input SetAccountStatusStoreInput) (IdentityAccount, error) {
	if s.err != nil {
		return IdentityAccount{}, s.err
	}
	return IdentityAccount{ID: input.AccountID, Status: input.Status}, nil
}
func (s *fakeIAMStore) ResetLocalPassword(_ context.Context, input ResetLocalPasswordStoreInput) error {
	s.resetCalls++
	s.lastReset = input
	return s.err
}
