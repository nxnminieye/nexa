package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"example.com/nexa-generation-consumer/backend/core/coreapp"
)

type memoryStore struct {
	mu            sync.Mutex
	next          int
	nextTenantID  int64
	tenantIDs     map[string]int64
	tenantCodes   map[int64]string
	local         map[string]coreapp.LocalCredential
	external      map[string]coreapp.IdentityAccount
	members       map[string]coreapp.TenantMember
	localRoles    map[coreapp.TenantMemberID][]string
	externalRoles map[string][]string
	sessions      map[coreapp.SessionID]coreapp.StoredSession
	accessLookup  map[string]coreapp.SessionID
	refreshLookup map[string]coreapp.SessionID
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		tenantIDs: make(map[string]int64), tenantCodes: make(map[int64]string),
		local: make(map[string]coreapp.LocalCredential), external: make(map[string]coreapp.IdentityAccount),
		members: make(map[string]coreapp.TenantMember), localRoles: make(map[coreapp.TenantMemberID][]string), externalRoles: make(map[string][]string),
		sessions: make(map[coreapp.SessionID]coreapp.StoredSession), accessLookup: make(map[string]coreapp.SessionID), refreshLookup: make(map[string]coreapp.SessionID),
	}
}

func (store *memoryStore) tenantID(code string) int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.tenantIDLocked(code)
}

func (store *memoryStore) tenantIDLocked(code string) int64 {
	if id := store.tenantIDs[code]; id > 0 {
		return id
	}
	store.nextTenantID++
	id := store.nextTenantID
	store.tenantIDs[code] = id
	store.tenantCodes[id] = code
	return id
}

func (store *memoryStore) nextID(prefix string) string {
	store.next++
	return prefix + "-" + strconv.Itoa(store.next)
}

func localKey(tenant, username string) string   { return tenant + "\x00" + username }
func externalKey(source, subject string) string { return source + "\x00" + subject }

func (store *memoryStore) CreateLocalAccount(ctx context.Context, input coreapp.CreateLocalAccountInput) (coreapp.IdentityAccount, error) {
	if err := ctx.Err(); err != nil {
		return coreapp.IdentityAccount{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	key := localKey(input.Tenant, input.Username)
	if _, exists := store.local[key]; exists {
		return coreapp.IdentityAccount{}, coreapp.ErrStoreConflict
	}
	account := coreapp.IdentityAccount{ID: coreapp.IdentityAccountID(store.nextID("account")), Username: input.Username, Email: input.Email, DisplayName: input.DisplayName}
	store.local[key] = coreapp.LocalCredential{Account: account, PasswordHash: input.PasswordHash}
	return account, nil
}

func (store *memoryStore) FindLocalAccount(ctx context.Context, key coreapp.LocalAccountKey) (coreapp.LocalCredential, error) {
	if err := ctx.Err(); err != nil {
		return coreapp.LocalCredential{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	credential, exists := store.local[localKey(key.Tenant, key.Username)]
	if !exists {
		return coreapp.LocalCredential{}, coreapp.ErrStoreNotFound
	}
	return credential, nil
}

func (store *memoryStore) FindExternalAccount(ctx context.Context, key coreapp.ExternalIdentityKey) (coreapp.IdentityAccount, error) {
	if err := ctx.Err(); err != nil {
		return coreapp.IdentityAccount{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	account, exists := store.external[externalKey(key.SourceCode, key.ExternalSubject)]
	if !exists {
		return coreapp.IdentityAccount{}, coreapp.ErrStoreNotFound
	}
	return account, nil
}

func (store *memoryStore) bindExternalIdentity(ctx context.Context, identity coreapp.NormalizedIdentity) (coreapp.IdentityAccount, error) {
	if err := ctx.Err(); err != nil {
		return coreapp.IdentityAccount{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	key := externalKey(identity.SourceCode, identity.ExternalSubject)
	if _, exists := store.external[key]; exists {
		return coreapp.IdentityAccount{}, coreapp.ErrStoreConflict
	}
	account := coreapp.IdentityAccount{
		ID: coreapp.IdentityAccountID(store.nextID("account")), SourceCode: identity.SourceCode, ExternalSubject: identity.ExternalSubject,
		Username: identity.Username, Email: identity.Email, DisplayName: identity.DisplayName,
	}
	store.external[key] = account
	return account, nil
}

func (store *memoryStore) admitTenant(ctx context.Context, tenant string, accountID coreapp.IdentityAccountID) (coreapp.TenantMember, error) {
	if err := ctx.Err(); err != nil {
		return coreapp.TenantMember{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tenantIDLocked(tenant)
	key := tenant + "\x00" + string(accountID)
	if member, exists := store.members[key]; exists {
		return member, nil
	}
	member := coreapp.TenantMember{ID: coreapp.TenantMemberID(store.nextID("member")), Tenant: tenant, AccountID: accountID}
	store.members[key] = member
	return member, nil
}

func (store *memoryStore) replaceLocalRoles(ctx context.Context, memberID coreapp.TenantMemberID, roles []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.localRoles[memberID] = append([]string(nil), roles...)
	return nil
}

func (store *memoryStore) ReplaceExternalRoleGrants(ctx context.Context, input coreapp.ReplaceExternalRoleGrantsInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	key := externalRoleKey(input.MemberID, input.SourceCode)
	if len(input.RoleRefs) == 0 {
		delete(store.externalRoles, key)
		return nil
	}
	store.externalRoles[key] = append([]string(nil), input.RoleRefs...)
	return nil
}

func (store *memoryStore) CreateSession(ctx context.Context, session coreapp.StoredSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.sessions[session.ID]; exists {
		return coreapp.ErrStoreConflict
	}
	store.sessions[session.ID] = session
	store.accessLookup[session.AccessTokenHash] = session.ID
	store.refreshLookup[session.RefreshTokenHash] = session.ID
	return nil
}

func (store *memoryStore) FindSessionByRefreshHash(ctx context.Context, hash string) (coreapp.StoredSession, error) {
	if err := ctx.Err(); err != nil {
		return coreapp.StoredSession{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	id, exists := store.refreshLookup[hash]
	if !exists {
		return coreapp.StoredSession{}, coreapp.ErrStoreNotFound
	}
	return store.sessions[id], nil
}

func (store *memoryStore) RotateSession(ctx context.Context, input coreapp.RotateSessionInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, exists := store.sessions[input.PreviousID]
	if !exists || current.Revoked || current.RefreshTokenHash != input.PreviousRefreshHash {
		return coreapp.ErrStoreConflict
	}
	current.Revoked = true
	store.sessions[current.ID] = current
	store.sessions[input.Replacement.ID] = input.Replacement
	store.accessLookup[input.Replacement.AccessTokenHash] = input.Replacement.ID
	store.refreshLookup[input.Replacement.RefreshTokenHash] = input.Replacement.ID
	return nil
}

func (store *memoryStore) RevokeSession(ctx context.Context, sessionID coreapp.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, exists := store.sessions[sessionID]
	if !exists {
		return coreapp.ErrStoreNotFound
	}
	session.Revoked = true
	store.sessions[sessionID] = session
	return nil
}

func (store *memoryStore) roles(memberID coreapp.TenantMemberID) []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.rolesLocked(memberID)
}

func (store *memoryStore) rolesForMember(tenantID int64, memberID coreapp.TenantMemberID) ([]string, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	tenant, exists := store.tenantCodes[tenantID]
	if !exists {
		return nil, false
	}
	found := false
	for _, member := range store.members {
		if member.Tenant == tenant && member.ID == memberID {
			found = true
			break
		}
	}
	if !found {
		return nil, false
	}
	return store.rolesLocked(memberID), true
}

func (store *memoryStore) rolesLocked(memberID coreapp.TenantMemberID) []string {
	seen := make(map[string]struct{})
	for _, role := range store.localRoles[memberID] {
		seen[role] = struct{}{}
	}
	prefix := string(memberID) + "\x00"
	for key, roles := range store.externalRoles {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		for _, role := range roles {
			seen[role] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for role := range seen {
		result = append(result, role)
	}
	sort.Strings(result)
	return result
}

func externalRoleKey(memberID coreapp.TenantMemberID, source string) string {
	return string(memberID) + "\x00" + source
}

func (store *memoryStore) authenticateAccess(ctx context.Context, token string) (coreapp.StoredSession, coreapp.TenantMember, error) {
	if err := ctx.Err(); err != nil {
		return coreapp.StoredSession{}, coreapp.TenantMember{}, err
	}
	if token == "" {
		return coreapp.StoredSession{}, coreapp.TenantMember{}, errors.New("invalid_credentials")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	sessionID, exists := store.accessLookup[tokenHash(token)]
	session, found := store.sessions[sessionID]
	if !exists || !found || session.Revoked || !time.Now().Before(session.AccessExpiresAt) {
		return coreapp.StoredSession{}, coreapp.TenantMember{}, errors.New("invalid_credentials")
	}
	member, found := store.members[session.Tenant+"\x00"+string(session.AccountID)]
	if !found {
		return coreapp.StoredSession{}, coreapp.TenantMember{}, errors.New("invalid_credentials")
	}
	return session, member, nil
}

func (store *memoryStore) sessionBelongsTo(sessionID coreapp.SessionID, tenantID int64, memberID coreapp.TenantMemberID) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	tenant, found := store.tenantCodes[tenantID]
	if !found {
		return false
	}
	session, exists := store.sessions[sessionID]
	if !exists || session.Tenant != tenant {
		return false
	}
	member, exists := store.members[tenant+"\x00"+string(session.AccountID)]
	return exists && member.ID == memberID
}

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

var _ coreapp.IdentityStore = (*memoryStore)(nil)
var _ coreapp.ExternalIdentityLookup = (*memoryStore)(nil)
var _ coreapp.ExternalRoleGrantStore = (*memoryStore)(nil)
