package coreapp

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestLocalRegistrationProjectsHasherFailureAsRedactedStoreFailure(t *testing.T) {
	const secret = "sensitive hasher failure"
	auth, err := NewLocalAuthenticator(newMemoryStore(), failingPasswordHasher{err: errors.New(secret)}, SessionOptions{
		AccessTTL: time.Minute, RefreshTTL: time.Hour, TokenBytes: 32,
		Clock: ClockFunc(func() time.Time { return time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC) }),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = auth.Register(context.Background(), LocalRegistration{
		Tenant: "tenant-a", Username: "alice", Password: []byte("valid-password"),
	})
	assertCode(t, err, CodeStoreFailure)
	if got := err.Error(); got != "local-auth.register: store_failure" {
		t.Fatalf("stable redacted error = %q", got)
	}
}

func TestLocalAuthLifecycleAndTenantIsolation(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	hasher, err := NewArgon2idHasher(Argon2idOptions{
		MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltBytes: 16, KeyBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewLocalAuthenticator(store, hasher, SessionOptions{
		AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour, TokenBytes: 32,
		Clock: ClockFunc(func() time.Time { return now }),
	})
	if err != nil {
		t.Fatal(err)
	}

	accountA, err := auth.Register(context.Background(), LocalRegistration{
		Tenant: "tenant-a", Username: "alice", Password: []byte("tenant-a-password"),
	})
	if err != nil {
		t.Fatal(err)
	}
	accountB, err := auth.Register(context.Background(), LocalRegistration{
		Tenant: "tenant-b", Username: "bob", Password: []byte("tenant-b-password"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if accountA.ID == accountB.ID {
		t.Fatal("local accounts must have stable independent identities")
	}
	_, err = auth.Login(context.Background(), LocalLogin{
		Tenant: "tenant-b", Username: "alice", Password: []byte("tenant-a-password"),
	})
	assertCode(t, err, CodeInvalidCredentials)
	_, err = auth.Register(context.Background(), LocalRegistration{
		Tenant: "tenant-a", Username: "alice", Password: []byte("another-password"),
	})
	assertCode(t, err, CodeConflict)

	_, err = auth.Login(context.Background(), LocalLogin{
		Tenant: "tenant-a", Username: "alice", Password: []byte("wrong-password"),
	})
	assertCode(t, err, CodeInvalidCredentials)
	if got := err.Error(); got != "local-auth.login: invalid_credentials" {
		t.Fatalf("stable redacted error = %q", got)
	}

	session, err := auth.Login(context.Background(), LocalLogin{
		Tenant: "tenant-a", Username: "alice", Password: []byte("tenant-a-password"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatal("login must return both tokens")
	}
	stored := store.session(session.ID)
	if stored.AccessTokenHash == session.AccessToken || stored.RefreshTokenHash == string(session.RefreshToken) {
		t.Fatal("store received a raw token")
	}

	second, err := auth.Login(context.Background(), LocalLogin{
		Tenant: "tenant-a", Username: "alice", Password: []byte("tenant-a-password"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == session.ID || second.RefreshToken == session.RefreshToken {
		t.Fatal("concurrent sessions must be independent")
	}

	refreshed, err := auth.Refresh(context.Background(), session.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.ID == session.ID || refreshed.RefreshToken == session.RefreshToken {
		t.Fatal("refresh must rotate the session and token")
	}
	_, err = auth.Refresh(context.Background(), session.RefreshToken)
	assertCode(t, err, CodeSessionReplayed)

	if err := auth.Revoke(context.Background(), refreshed.ID); err != nil {
		t.Fatal(err)
	}
	_, err = auth.Refresh(context.Background(), refreshed.RefreshToken)
	assertCode(t, err, CodeSessionReplayed)

	now = now.Add(2 * time.Hour)
	_, err = auth.Refresh(context.Background(), second.RefreshToken)
	assertCode(t, err, CodeSessionExpired)
}

func TestProviderSet(t *testing.T) {
	empty, err := NewProviderSet()
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.Descriptors(); len(got) != 0 {
		t.Fatalf("provider absence descriptors = %#v", got)
	}
	_, err = empty.Exchange(context.Background(), "missing", ExchangeInput{})
	assertCode(t, err, CodeCapabilityUnavailable)

	provider := &fakeProvider{
		descriptor: ProviderDescriptor{
			ID: "oidc-a", Protocol: "oidc",
			Capabilities: ProviderCapabilities{Authenticate: true, AutoProvision: true, GroupClaims: true},
		},
		identity: NormalizedIdentity{
			SourceCode: "oidc-a", ExternalSubject: "subject-1", Username: "alice",
			Email: "same@example.test", CandidateSubjects: []string{"subject-old"}, ExternalGroups: []string{"operators"},
		},
	}
	providers, err := NewProviderSet(provider)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewProviderSet(provider, provider)
	assertCode(t, err, CodeConflict)
	var typedNil *fakeProvider
	_, err = NewProviderSet(typedNil)
	assertCode(t, err, CodeInvalidInput)

	identity, err := providers.Exchange(context.Background(), "oidc-a", ExchangeInput{Code: "code"})
	if err != nil {
		t.Fatal(err)
	}
	identity.ExternalGroups[0] = "mutated"
	identity.CandidateSubjects[0] = "mutated"
	again, err := providers.Exchange(context.Background(), "oidc-a", ExchangeInput{Code: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ExternalGroups[0] != "operators" || again.CandidateSubjects[0] != "subject-old" {
		t.Fatal("provider result slices were not defensively copied")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = providers.Exchange(canceled, "oidc-a", ExchangeInput{Code: "secret-code"})
	assertCode(t, err, CodeCanceled)
	if got := err.Error(); got != "provider.exchange: canceled" {
		t.Fatalf("provider error leaked input: %q", got)
	}
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s", want)
	}
	if got := CodeOf(err); got != want {
		t.Fatalf("error code = %q, want %q: %v", got, want, err)
	}
}

type fakeProvider struct {
	descriptor ProviderDescriptor
	identity   NormalizedIdentity
}

type failingPasswordHasher struct{ err error }

func (h failingPasswordHasher) Hash([]byte) (string, error) { return "", h.err }
func (h failingPasswordHasher) Verify(string, []byte) error { return h.err }

func (p *fakeProvider) Descriptor() ProviderDescriptor { return p.descriptor }

func (p *fakeProvider) Authorize(ctx context.Context, _ AuthorizeInput) (AuthorizeResult, error) {
	if err := ctx.Err(); err != nil {
		return AuthorizeResult{}, err
	}
	return AuthorizeResult{URL: "https://identity.example.test/authorize"}, nil
}

func (p *fakeProvider) Exchange(ctx context.Context, _ ExchangeInput) (NormalizedIdentity, error) {
	if err := ctx.Err(); err != nil {
		return NormalizedIdentity{}, err
	}
	return p.identity, nil
}

type memoryStore struct {
	mu            sync.Mutex
	next          int
	local         map[string]LocalCredential
	localTenants  map[string]map[string]struct{}
	sessions      map[SessionID]StoredSession
	refreshLookup map[string]SessionID
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		local: make(map[string]LocalCredential), localTenants: make(map[string]map[string]struct{}),
		sessions: make(map[SessionID]StoredSession), refreshLookup: make(map[string]SessionID),
	}
}

func (s *memoryStore) CreateLocalAccount(ctx context.Context, input CreateLocalAccountInput) (IdentityAccount, error) {
	if err := ctx.Err(); err != nil {
		return IdentityAccount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.local[input.Username]; exists {
		return IdentityAccount{}, ErrStoreConflict
	}
	s.next++
	account := IdentityAccount{ID: IdentityAccountID(id("account", s.next)), Username: input.Username, Email: input.Email, DisplayName: input.DisplayName}
	s.local[input.Username] = LocalCredential{Account: account, PasswordHash: input.PasswordHash}
	s.localTenants[input.Username] = map[string]struct{}{input.Tenant: {}}
	return account, nil
}

func (s *memoryStore) FindLocalAccount(ctx context.Context, key LocalAccountKey) (LocalCredential, error) {
	if err := ctx.Err(); err != nil {
		return LocalCredential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, exists := s.local[key.Username]
	if !exists {
		return LocalCredential{}, ErrStoreNotFound
	}
	if _, member := s.localTenants[key.Username][key.Tenant]; !member {
		return LocalCredential{}, ErrStoreNotFound
	}
	return credential, nil
}

func (s *memoryStore) CreateSession(ctx context.Context, session StoredSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.ID]; exists {
		return ErrStoreConflict
	}
	s.sessions[session.ID] = session
	s.refreshLookup[session.RefreshTokenHash] = session.ID
	return nil
}

func (s *memoryStore) FindSessionByRefreshHash(ctx context.Context, hash string) (StoredSession, error) {
	if err := ctx.Err(); err != nil {
		return StoredSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, exists := s.refreshLookup[hash]
	if !exists {
		return StoredSession{}, ErrStoreNotFound
	}
	return s.sessions[id], nil
}

func (s *memoryStore) RotateSession(ctx context.Context, input RotateSessionInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.sessions[input.PreviousID]
	if !exists || current.Revoked || current.RefreshTokenHash != input.PreviousRefreshHash {
		return ErrStoreConflict
	}
	current.Revoked = true
	s.sessions[current.ID] = current
	if _, exists := s.sessions[input.Replacement.ID]; exists {
		return ErrStoreConflict
	}
	s.sessions[input.Replacement.ID] = input.Replacement
	s.refreshLookup[input.Replacement.RefreshTokenHash] = input.Replacement.ID
	return nil
}

func (s *memoryStore) RevokeSession(ctx context.Context, sessionID SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[sessionID]
	if !exists {
		return ErrStoreNotFound
	}
	session.Revoked = true
	s.sessions[sessionID] = session
	return nil
}

func (s *memoryStore) session(id SessionID) StoredSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func id(prefix string, value int) string {
	return prefix + "-" + strconv.Itoa(value)
}

var _ IdentityStore = (*memoryStore)(nil)
var _ IdentityProvider = (*fakeProvider)(nil)
