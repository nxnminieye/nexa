package coreapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errPostgresStoreFailure = errors.New("core postgres store: failure")

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) (*PostgresStore, error) {
	if db == nil {
		return nil, invalid("postgres-store.new")
	}
	return &PostgresStore{db: db}, nil
}

var _ IdentityStore = (*PostgresStore)(nil)
var _ IAMStore = (*PostgresStore)(nil)
var _ CatalogStore = (*PostgresStore)(nil)
var _ ExternalIdentityLookup = (*PostgresStore)(nil)
var _ ExternalRoleGrantStore = (*PostgresStore)(nil)
var _ AccessSessionStore = (*PostgresStore)(nil)

func (s *PostgresStore) CreateLocalAccount(ctx context.Context, input CreateLocalAccountInput) (IdentityAccount, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	defer tx.Rollback()
	var tenantID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tenants WHERE code=$1 AND status='enabled'`, input.Tenant).Scan(&tenantID); err != nil {
		return IdentityAccount{}, postgresCredentialError(err)
	}
	var accountID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO identity_accounts(username,email,display_name,password_hash,status) VALUES($1,NULLIF($2,''),NULLIF($3,''),$4,'enabled') RETURNING id`, input.Username, input.Email, input.DisplayName, input.PasswordHash).Scan(&accountID)
	if err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tenant_members(tenant_id,identity_account_id,status) VALUES($1,$2,'enabled')`, tenantID, accountID); err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	if err = tx.Commit(); err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	return IdentityAccount{ID: accountIDString(accountID), Username: input.Username, Email: input.Email, DisplayName: input.DisplayName, Status: IAMStatusEnabled}, nil
}

func (s *PostgresStore) FindLocalAccount(ctx context.Context, key LocalAccountKey) (LocalCredential, error) {
	var id int64
	var account IdentityAccount
	var email, display sql.NullString
	var passwordHash string
	err := s.db.QueryRowContext(ctx, `SELECT a.id,a.identity_source_code,a.external_subject,a.username,a.email,a.display_name,a.password_hash,a.status
FROM identity_accounts a JOIN tenant_members m ON m.identity_account_id=a.id JOIN tenants t ON t.id=m.tenant_id
WHERE t.code=$1 AND a.username=$2 AND t.status='enabled' AND m.status='enabled' AND a.status='enabled'`, key.Tenant, key.Username).
		Scan(&id, &account.SourceCode, &account.ExternalSubject, &account.Username, &email, &display, &passwordHash, &account.Status)
	if err != nil {
		return LocalCredential{}, postgresCredentialError(err)
	}
	account.ID, account.Email, account.DisplayName = accountIDString(id), email.String, display.String
	return LocalCredential{Account: account, PasswordHash: passwordHash}, nil
}

func (s *PostgresStore) CreateSession(ctx context.Context, session StoredSession) error {
	accountID, err := numericID(string(session.AccountID))
	if err != nil {
		return ErrStoreFailedPrecondition
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO auth_sessions(session_id,tenant_id,identity_account_id,access_token_hash,refresh_token_hash,access_expires_at,refresh_expires_at,revoked)
SELECT $1,t.id,$2,$3,$4,$5,$6,$7 FROM tenants t JOIN tenant_members m ON m.tenant_id=t.id AND m.identity_account_id=$2
JOIN identity_accounts a ON a.id=$2 WHERE t.code=$8 AND t.status='enabled' AND m.status='enabled' AND a.status='enabled'`, session.ID, accountID, session.AccessTokenHash, session.RefreshTokenHash, session.AccessExpiresAt, session.RefreshExpiresAt, session.Revoked, session.Tenant)
	if err != nil {
		return postgresError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrStoreCredentialUnavailable
	}
	return nil
}

func (s *PostgresStore) FindSessionByRefreshHash(ctx context.Context, hash string) (StoredSession, error) {
	var value StoredSession
	var accountID int64
	err := s.db.QueryRowContext(ctx, `SELECT s.session_id,s.identity_account_id,t.code,s.access_token_hash,s.refresh_token_hash,s.access_expires_at,s.refresh_expires_at,s.revoked
FROM auth_sessions s JOIN tenants t ON t.id=s.tenant_id JOIN tenant_members m ON m.tenant_id=s.tenant_id AND m.identity_account_id=s.identity_account_id
JOIN identity_accounts a ON a.id=s.identity_account_id WHERE s.refresh_token_hash=$1 AND t.status='enabled' AND m.status='enabled' AND a.status='enabled'`, hash).
		Scan(&value.ID, &accountID, &value.Tenant, &value.AccessTokenHash, &value.RefreshTokenHash, &value.AccessExpiresAt, &value.RefreshExpiresAt, &value.Revoked)
	if err != nil {
		return StoredSession{}, postgresCredentialError(err)
	}
	value.AccountID = accountIDString(accountID)
	return value, nil
}

func (s *PostgresStore) FindAccessPrincipal(ctx context.Context, hash string, now time.Time) (AccessPrincipal, error) {
	var value AccessPrincipal
	var accountID int64
	var email, display sql.NullString
	err := s.db.QueryRowContext(ctx, `WITH RECURSIVE
principal AS (
  SELECT s.session_id,t.id::text AS tenant_id,t.code AS tenant_code,m.id::text AS member_id,
         a.id AS account_id,a.identity_source_code,a.external_subject,a.username,a.email,a.display_name,a.status
  FROM auth_sessions s
  JOIN tenants t ON t.id=s.tenant_id AND t.status='enabled'
  JOIN identity_accounts a ON a.id=s.identity_account_id AND a.status='enabled'
  JOIN tenant_members m ON m.tenant_id=s.tenant_id AND m.identity_account_id=s.identity_account_id AND m.status='enabled'
  WHERE s.access_token_hash=$1 AND s.revoked=FALSE AND s.access_expires_at>$2
),
principal_roles AS (
  SELECT r.id,r.code
  FROM principal p
  JOIN tenant_member_roles g ON g.tenant_id=p.tenant_id::bigint AND g.tenant_member_id=p.member_id::bigint
  JOIN roles r ON r.id=g.role_id AND r.tenant_id=g.tenant_id AND r.status='enabled'
  UNION
  SELECT r.id,r.code
  FROM principal p
  JOIN managed_tenant_member_roles g ON g.tenant_id=p.tenant_id::bigint AND g.tenant_member_id=p.member_id::bigint
  JOIN roles r ON r.id=g.role_id AND r.tenant_id=g.tenant_id AND r.status='enabled'
),
menu_tree(id,code,parent_code,visited) AS (
  SELECT m.id,m.code,m.parent_code,ARRAY[m.id]
  FROM principal_roles r
  JOIN role_menus g ON g.role_id=r.id
  JOIN principal p ON p.tenant_id::bigint=g.tenant_id
  JOIN menus m ON m.id=g.menu_id AND m.status='enabled'
  UNION
  SELECT parent.id,parent.code,parent.parent_code,tree.visited||parent.id
  FROM menu_tree tree
  JOIN menus parent ON parent.code=tree.parent_code AND parent.status='enabled'
  WHERE NOT parent.id=ANY(tree.visited)
)
SELECT p.session_id,p.tenant_id,p.tenant_code,p.member_id,p.account_id,p.identity_source_code,p.external_subject,
       p.username,p.email,p.display_name,p.status,
       COALESCE((SELECT array_agg(DISTINCT code ORDER BY code) FROM principal_roles),ARRAY[]::text[]),
       COALESCE((SELECT array_agg(DISTINCT a.code ORDER BY a.code)
                 FROM principal_roles r
                 JOIN role_permission_actions g ON g.role_id=r.id
                 JOIN principal tenant ON tenant.tenant_id::bigint=g.tenant_id
                 JOIN permission_actions a ON a.id=g.permission_action_id AND a.status='enabled'
                 JOIN permission_resources resource ON resource.id=a.permission_resource_id AND resource.status='enabled'),ARRAY[]::text[]),
       COALESCE((SELECT array_agg(DISTINCT code ORDER BY code) FROM menu_tree),ARRAY[]::text[])
FROM principal p`, hash, now).Scan(
		&value.SessionID, &value.TenantID, &value.TenantCode, &value.MemberID, &accountID,
		&value.Account.SourceCode, &value.Account.ExternalSubject, &value.Account.Username, &email, &display, &value.Account.Status,
		(*stringArray)(&value.RoleCodes), (*stringArray)(&value.PermissionCodes), (*stringArray)(&value.MenuCodes),
	)
	if err != nil {
		return AccessPrincipal{}, postgresCredentialError(err)
	}
	value.Account.ID = accountIDString(accountID)
	value.Account.Email = email.String
	value.Account.DisplayName = display.String
	return value, nil
}

func (s *PostgresStore) RotateSession(ctx context.Context, input RotateSessionInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return postgresError(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked=TRUE WHERE session_id=$1 AND refresh_token_hash=$2 AND revoked=FALSE`, input.PreviousID, input.PreviousRefreshHash)
	if err != nil {
		return postgresError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrStoreConflict
	}
	if err := createSessionTx(ctx, tx, input.Replacement); err != nil {
		return err
	}
	return postgresErrorOrNil(tx.Commit())
}

func (s *PostgresStore) RevokeSession(ctx context.Context, id SessionID) error {
	result, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET revoked=TRUE WHERE session_id=$1 AND revoked=FALSE`, id)
	if err != nil {
		return postgresError(err)
	}
	return requireAffected(result)
}

func (s *PostgresStore) FindExternalAccount(ctx context.Context, key ExternalIdentityKey) (IdentityAccount, error) {
	var value IdentityAccount
	var id int64
	var email, display sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,identity_source_code,external_subject,username,email,display_name,status FROM identity_accounts WHERE identity_source_code=$1 AND external_subject=$2 AND status='enabled'`, key.SourceCode, key.ExternalSubject).
		Scan(&id, &value.SourceCode, &value.ExternalSubject, &value.Username, &email, &display, &value.Status)
	if err != nil {
		return IdentityAccount{}, postgresCredentialError(err)
	}
	value.ID, value.Email, value.DisplayName = accountIDString(id), email.String, display.String
	return value, nil
}

func (s *PostgresStore) ListIdentityAccounts(ctx context.Context, input ListIdentityAccountsInput) (IdentityAccountPage, error) {
	const filter = ` FROM identity_accounts a WHERE ($1='' OR a.username ILIKE '%'||$1||'%' OR COALESCE(a.email,'') ILIKE '%'||$1||'%' OR COALESCE(a.display_name,'') ILIKE '%'||$1||'%' OR a.identity_source_code ILIKE '%'||$1||'%') AND ($2='' OR a.status=$2)`
	var page IdentityAccountPage
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+filter, input.Keyword, input.Status).Scan(&page.Total); err != nil {
		return IdentityAccountPage{}, postgresError(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.identity_source_code,a.external_subject,a.username,a.email,a.display_name,a.status`+filter+` ORDER BY a.id LIMIT $3 OFFSET $4`, input.Keyword, input.Status, input.Limit, input.Offset)
	if err != nil {
		return IdentityAccountPage{}, postgresError(err)
	}
	defer rows.Close()
	for rows.Next() {
		account, err := scanIdentityAccount(rows)
		if err != nil {
			return IdentityAccountPage{}, err
		}
		page.Items = append(page.Items, account)
	}
	if err = rows.Err(); err != nil {
		return IdentityAccountPage{}, postgresError(err)
	}
	return page, nil
}

func (s *PostgresStore) GetIdentityAccount(ctx context.Context, accountID IdentityAccountID) (IdentityAccount, error) {
	id, err := numericID(string(accountID))
	if err != nil {
		return IdentityAccount{}, ErrStoreNotFound
	}
	return scanIdentityAccount(s.db.QueryRowContext(ctx, `SELECT id,identity_source_code,external_subject,username,email,display_name,status FROM identity_accounts WHERE id=$1`, id))
}

func (s *PostgresStore) ReplaceExternalRoleGrants(ctx context.Context, input ReplaceExternalRoleGrantsInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return postgresError(err)
	}
	defer tx.Rollback()
	tenantID, memberID, err := lockMemberByCode(ctx, tx, input.Tenant, input.MemberID)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM managed_tenant_member_roles WHERE tenant_id=$1 AND tenant_member_id=$2 AND source_owner=$3`, tenantID, memberID, input.SourceCode); err != nil {
		return postgresError(err)
	}
	roles := append([]string(nil), input.RoleCodes...)
	sort.Strings(roles)
	digestBytes := sha256.Sum256([]byte(strings.Join(roles, "\x00")))
	digest := hex.EncodeToString(digestBytes[:])
	for _, role := range roles {
		result, err := tx.ExecContext(ctx, `INSERT INTO managed_tenant_member_roles(tenant_id,tenant_member_id,role_id,source_owner,source_digest)
SELECT $1,$2,r.id,$3,$4 FROM roles r WHERE r.tenant_id=$1 AND r.code=$5 AND r.status='enabled' AND r.managed=FALSE
ON CONFLICT(tenant_id,tenant_member_id,role_id,source_owner) DO UPDATE SET source_digest=EXCLUDED.source_digest`, tenantID, memberID, input.SourceCode, digest, role)
		if err != nil {
			return postgresError(err)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return postgresError(affectedErr)
		}
		if affected != 1 {
			var managed bool
			lookupErr := tx.QueryRowContext(ctx, `SELECT managed FROM roles WHERE tenant_id=$1 AND code=$2 AND status='enabled'`, tenantID, role).Scan(&managed)
			if lookupErr == nil && managed {
				return ErrStorePermissionDenied
			}
			if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
				return postgresError(lookupErr)
			}
			return ErrStoreInvalidInput
		}
	}
	return postgresErrorOrNil(tx.Commit())
}

func (s *PostgresStore) ProvisionTenant(ctx context.Context, input ProvisionTenantStoreInput) (ProvisionTenantResult, error) {
	accountID, err := numericID(string(input.OwnerAccountID))
	if err != nil {
		return ProvisionTenantResult{}, ErrStoreFailedPrecondition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, input.TenantCode); err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tenants(code,name,status,version) VALUES($1,$2,'enabled',1) ON CONFLICT(code) DO NOTHING`, input.TenantCode, input.DisplayName); err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	var tenantID int64
	var tenant Tenant
	if err = tx.QueryRowContext(ctx, `SELECT id,id::text,code,name,status,version FROM tenants WHERE code=$1 FOR UPDATE`, input.TenantCode).Scan(&tenantID, &tenant.ID, &tenant.Code, &tenant.DisplayName, &tenant.Status, &tenant.Version); err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	if tenant.DisplayName != input.DisplayName {
		return ProvisionTenantResult{}, ErrStoreConflict
	}
	var existingOwner int64
	err = tx.QueryRowContext(ctx, `SELECT m.identity_account_id FROM managed_tenant_member_roles g JOIN tenant_members m ON m.id=g.tenant_member_id JOIN roles r ON r.id=g.role_id
WHERE g.tenant_id=$1 AND g.source_owner='core.tenant-provision' AND r.source_owner='core.tenant-provision' AND r.source_key='tenant-owner' FOR UPDATE`, tenantID).Scan(&existingOwner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ProvisionTenantResult{}, postgresError(err)
	}
	if err == nil && existingOwner != accountID {
		return ProvisionTenantResult{}, ErrStoreConflict
	}
	var memberID int64
	var memberVersion uint64
	var memberStatus IAMStatus
	err = tx.QueryRowContext(ctx, `INSERT INTO tenant_members(tenant_id,identity_account_id,status,version) SELECT $1,$2,'enabled',1 FROM identity_accounts WHERE id=$2 AND status='enabled'
ON CONFLICT(tenant_id,identity_account_id) DO UPDATE SET identity_account_id=EXCLUDED.identity_account_id RETURNING id,status,version`, tenantID, accountID).Scan(&memberID, &memberStatus, &memberVersion)
	if err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	var roleID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO roles(tenant_id,code,name,status,managed,source_owner,source_key,source_digest,version)
VALUES($1,'tenant-owner','Tenant owner','enabled',TRUE,'core.tenant-provision','tenant-owner','v1',1)
ON CONFLICT(tenant_id,source_owner,source_key) WHERE managed=TRUE DO UPDATE SET source_digest=EXCLUDED.source_digest RETURNING id`, tenantID).Scan(&roleID)
	if err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO managed_tenant_member_roles(tenant_id,tenant_member_id,role_id,source_owner,source_digest) VALUES($1,$2,$3,'core.tenant-provision','v1') ON CONFLICT DO NOTHING`, tenantID, memberID, roleID); err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	if err = tx.Commit(); err != nil {
		return ProvisionTenantResult{}, postgresError(err)
	}
	return ProvisionTenantResult{Tenant: tenant, Owner: TenantMember{ID: memberIDString(memberID), TenantID: tenant.ID, TenantCode: input.TenantCode, AccountID: input.OwnerAccountID, Status: memberStatus, ManagedOwnerGrant: true, Version: memberVersion}}, nil
}

func (s *PostgresStore) ListTenants(ctx context.Context, input ListTenantsInput) (TenantPage, error) {
	const filter = ` FROM tenants t WHERE ($1='' OR t.code ILIKE '%'||$1||'%' OR t.name ILIKE '%'||$1||'%') AND ($2='' OR t.status=$2)`
	var page TenantPage
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+filter, input.Keyword, input.Status).Scan(&page.Total); err != nil {
		return TenantPage{}, postgresError(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id::text,t.code,t.name,t.status,t.version`+filter+` ORDER BY t.id LIMIT $3 OFFSET $4`, input.Keyword, input.Status, input.Limit, input.Offset)
	if err != nil {
		return TenantPage{}, postgresError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var tenant Tenant
		if err = rows.Scan(&tenant.ID, &tenant.Code, &tenant.DisplayName, &tenant.Status, &tenant.Version); err != nil {
			return TenantPage{}, postgresError(err)
		}
		page.Items = append(page.Items, tenant)
	}
	if err = rows.Err(); err != nil {
		return TenantPage{}, postgresError(err)
	}
	return page, nil
}

func (s *PostgresStore) GetTenant(ctx context.Context, tenantID string) (Tenant, error) {
	id, err := numericID(tenantID)
	if err != nil {
		return Tenant{}, ErrStoreNotFound
	}
	var tenant Tenant
	err = s.db.QueryRowContext(ctx, `SELECT id::text,code,name,status,version FROM tenants WHERE id=$1`, id).Scan(&tenant.ID, &tenant.Code, &tenant.DisplayName, &tenant.Status, &tenant.Version)
	if err != nil {
		return Tenant{}, postgresError(err)
	}
	return tenant, nil
}

func (s *PostgresStore) UpdateTenant(ctx context.Context, input UpdateTenantStoreInput) (Tenant, error) {
	id, err := numericID(input.TenantID)
	if err != nil {
		return Tenant{}, ErrStoreNotFound
	}
	var tenant Tenant
	err = s.db.QueryRowContext(ctx, `UPDATE tenants SET name=$1,version=version+1 WHERE id=$2 AND version=$3 RETURNING id::text,code,name,status,version`, input.DisplayName, id, input.ExpectedVersion).Scan(&tenant.ID, &tenant.Code, &tenant.DisplayName, &tenant.Status, &tenant.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, distinguishTenantVersion(ctx, s.db, id)
	}
	if err != nil {
		return Tenant{}, postgresError(err)
	}
	return tenant, nil
}

func (s *PostgresStore) SetTenantStatus(ctx context.Context, input SetTenantStatusStoreInput) (Tenant, error) {
	var value Tenant
	tenantID, parseErr := numericID(input.TenantID)
	if parseErr != nil {
		return Tenant{}, ErrStoreNotFound
	}
	err := s.db.QueryRowContext(ctx, `UPDATE tenants SET status=$1,version=version+1 WHERE id=$2 AND version=$3 RETURNING id::text,code,name,status,version`, input.Status, tenantID, input.ExpectedVersion).Scan(&value.ID, &value.Code, &value.DisplayName, &value.Status, &value.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, distinguishTenantVersion(ctx, s.db, tenantID)
	}
	if err != nil {
		return Tenant{}, postgresError(err)
	}
	return value, nil
}

func (s *PostgresStore) ListTenantMembers(ctx context.Context, input ListTenantMembersInput) (TenantMemberPage, error) {
	tenantID, parseErr := numericID(input.TenantID)
	if parseErr != nil {
		return TenantMemberPage{}, ErrStoreNotFound
	}
	const filter = ` WHERE m.tenant_id=$1 AND ($2='' OR a.username ILIKE '%'||$2||'%' OR COALESCE(a.email,'') ILIKE '%'||$2||'%' OR COALESCE(a.display_name,'') ILIKE '%'||$2||'%') AND ($3='' OR m.status=$3)`
	var page TenantMemberPage
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM tenant_members m JOIN identity_accounts a ON a.id=m.identity_account_id`+filter, tenantID, input.Keyword, input.Status).Scan(&page.Total); err != nil {
		return TenantMemberPage{}, postgresError(err)
	}
	rows, err := s.db.QueryContext(ctx, memberSelect+filter+` ORDER BY m.id LIMIT $4 OFFSET $5`, tenantID, input.Keyword, input.Status, input.Limit, input.Offset)
	if err != nil {
		return TenantMemberPage{}, postgresError(err)
	}
	defer rows.Close()
	for rows.Next() {
		member, err := scanMember(rows)
		if err != nil {
			return TenantMemberPage{}, postgresError(err)
		}
		page.Items = append(page.Items, member)
	}
	if err := rows.Err(); err != nil {
		return TenantMemberPage{}, postgresError(err)
	}
	return page, nil
}

func (s *PostgresStore) GetTenantMember(ctx context.Context, key TenantMemberKey) (TenantMember, error) {
	id, err := numericID(string(key.MemberID))
	if err != nil {
		return TenantMember{}, ErrStoreNotFound
	}
	tenantID, err := numericID(key.TenantID)
	if err != nil {
		return TenantMember{}, ErrStoreNotFound
	}
	return scanMember(s.db.QueryRowContext(ctx, memberSelect+` WHERE m.tenant_id=$1 AND m.id=$2`, tenantID, id))
}

func (s *PostgresStore) SetTenantMemberStatus(ctx context.Context, input SetTenantMemberStatusStoreInput) (TenantMember, error) {
	id, err := numericID(string(input.Key.MemberID))
	if err != nil {
		return TenantMember{}, ErrStoreNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TenantMember{}, postgresError(err)
	}
	defer tx.Rollback()
	if input.Status == IAMStatusDisabled && input.PreserveLastEnabledOwner {
		tenantID, parseErr := numericID(input.Key.TenantID)
		if parseErr != nil {
			return TenantMember{}, ErrStoreNotFound
		}
		rows, queryErr := tx.QueryContext(ctx, `SELECT m.id FROM tenant_members m
WHERE m.tenant_id=$1 AND m.status='enabled' AND EXISTS(SELECT 1 FROM managed_tenant_member_roles g JOIN roles r ON r.id=g.role_id AND r.tenant_id=g.tenant_id WHERE g.tenant_member_id=m.id AND g.tenant_id=m.tenant_id AND g.source_owner='core.tenant-provision' AND r.source_owner='core.tenant-provision' AND r.source_key='tenant-owner') FOR UPDATE OF m`, tenantID)
		if queryErr != nil {
			return TenantMember{}, postgresError(queryErr)
		}
		owners, targetOwner := 0, false
		for rows.Next() {
			var ownerID int64
			if err = rows.Scan(&ownerID); err != nil {
				rows.Close()
				return TenantMember{}, postgresError(err)
			}
			owners++
			targetOwner = targetOwner || ownerID == id
		}
		if err = rows.Close(); err != nil {
			return TenantMember{}, postgresError(err)
		}
		if err = rows.Err(); err != nil {
			return TenantMember{}, postgresError(err)
		}
		if targetOwner && owners <= 1 {
			return TenantMember{}, ErrStoreFailedPrecondition
		}
	}
	tenantID, parseErr := numericID(input.Key.TenantID)
	if parseErr != nil {
		return TenantMember{}, ErrStoreNotFound
	}
	result, err := tx.ExecContext(ctx, `UPDATE tenant_members SET status=$1,version=version+1 WHERE tenant_id=$2 AND id=$3 AND version=$4`, input.Status, tenantID, id, input.ExpectedVersion)
	if err != nil {
		return TenantMember{}, postgresError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return TenantMember{}, distinguishMemberVersion(ctx, tx, tenantID, id)
	}
	member, err := scanMember(tx.QueryRowContext(ctx, memberSelect+` WHERE m.tenant_id=$1 AND m.id=$2`, tenantID, id))
	if err != nil {
		return TenantMember{}, err
	}
	if err = tx.Commit(); err != nil {
		return TenantMember{}, postgresError(err)
	}
	return member, nil
}

func (s *PostgresStore) ReplaceManualRoleGrants(ctx context.Context, input ReplaceManualRolesStoreInput) (TenantMember, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TenantMember{}, postgresError(err)
	}
	defer tx.Rollback()
	tenantID, memberID, err := lockMember(ctx, tx, input.Key.TenantID, input.Key.MemberID)
	if err != nil {
		return TenantMember{}, err
	}
	var version uint64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM tenant_members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, memberID).Scan(&version); err != nil {
		return TenantMember{}, postgresError(err)
	}
	if version != input.ExpectedVersion {
		return TenantMember{}, ErrStoreConcurrentWrite
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM tenant_member_roles WHERE tenant_id=$1 AND tenant_member_id=$2`, tenantID, memberID); err != nil {
		return TenantMember{}, postgresError(err)
	}
	for _, roleCode := range input.RoleCodes {
		result, err := tx.ExecContext(ctx, `INSERT INTO tenant_member_roles(tenant_id,tenant_member_id,role_id) SELECT $1,$2,id FROM roles WHERE tenant_id=$1 AND code=$3 AND status='enabled'`, tenantID, memberID, roleCode)
		if err != nil {
			return TenantMember{}, postgresError(err)
		}
		if err = requireAffected(result); err != nil {
			return TenantMember{}, ErrStoreInvalidInput
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tenant_members SET version=version+1 WHERE tenant_id=$1 AND id=$2`, tenantID, memberID); err != nil {
		return TenantMember{}, postgresError(err)
	}
	member, err := scanMember(tx.QueryRowContext(ctx, memberSelect+` WHERE m.tenant_id=$1 AND m.id=$2`, tenantID, memberID))
	if err != nil {
		return TenantMember{}, err
	}
	if err = tx.Commit(); err != nil {
		return TenantMember{}, postgresError(err)
	}
	return member, nil
}

func (s *PostgresStore) GetTenantRole(ctx context.Context, key TenantRoleKey) (TenantRole, error) {
	return s.getRole(ctx, s.db, key)
}

func (s *PostgresStore) ListTenantRoles(ctx context.Context, input ListTenantRolesInput) (TenantRolePage, error) {
	tenantID, err := numericID(input.TenantID)
	if err != nil {
		return TenantRolePage{}, ErrStoreNotFound
	}
	const filter = ` WHERE r.tenant_id=$1 AND ($2='' OR r.code ILIKE '%'||$2||'%' OR r.name ILIKE '%'||$2||'%') AND ($3='' OR r.status=$3)`
	var page TenantRolePage
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM roles r`+filter, tenantID, input.Keyword, input.Status).Scan(&page.Total); err != nil {
		return TenantRolePage{}, postgresError(err)
	}
	rows, err := s.db.QueryContext(ctx, roleSelect+filter+` ORDER BY r.id LIMIT $4 OFFSET $5`, tenantID, input.Keyword, input.Status, input.Limit, input.Offset)
	if err != nil {
		return TenantRolePage{}, postgresError(err)
	}
	defer rows.Close()
	for rows.Next() {
		role, scanErr := scanRole(rows)
		if scanErr != nil {
			return TenantRolePage{}, scanErr
		}
		page.Items = append(page.Items, role)
	}
	if err = rows.Err(); err != nil {
		return TenantRolePage{}, postgresError(err)
	}
	return page, nil
}

func (s *PostgresStore) CreateTenantRole(ctx context.Context, input CreateTenantRoleStoreInput) (TenantRole, error) {
	var value TenantRole
	var id int64
	tenantID, parseErr := numericID(input.TenantID)
	if parseErr != nil {
		return TenantRole{}, ErrStoreNotFound
	}
	err := s.db.QueryRowContext(ctx, `INSERT INTO roles(tenant_id,code,name,status,version) SELECT id,$2,$3,'enabled',1 FROM tenants WHERE id=$1 AND status='enabled' RETURNING id`, tenantID, input.Code, input.DisplayName).Scan(&id)
	if err != nil {
		return TenantRole{}, postgresError(err)
	}
	value, err = s.getRole(ctx, s.db, TenantRoleKey{TenantID: input.TenantID, RoleID: roleIDString(id)})
	return value, err
}

func (s *PostgresStore) UpdateTenantRole(ctx context.Context, input UpdateTenantRoleStoreInput) (TenantRole, error) {
	return s.mutateRole(ctx, input.Key, input.ExpectedVersion, input.RejectManaged, `name=$1`, input.DisplayName)
}
func (s *PostgresStore) SetTenantRoleStatus(ctx context.Context, input SetTenantRoleStatusStoreInput) (TenantRole, error) {
	return s.mutateRole(ctx, input.Key, input.ExpectedVersion, input.RejectManaged, `status=$1`, input.Status)
}

func (s *PostgresStore) ReplaceRolePermissions(ctx context.Context, input ReplaceRolePermissionsStoreInput) (TenantRole, error) {
	return s.replaceRoleCatalog(ctx, input.Key, input.ExpectedVersion, input.RejectManaged, input.PermissionCodes, true)
}
func (s *PostgresStore) ReplaceRoleMenus(ctx context.Context, input ReplaceRoleMenusStoreInput) (TenantRole, error) {
	return s.replaceRoleCatalog(ctx, input.Key, input.ExpectedVersion, input.RejectManaged, input.MenuCodes, false)
}

func (s *PostgresStore) ListMenus(ctx context.Context, input ListMenusInput) (MenuPage, error) {
	const filter = ` FROM menus m WHERE ($1='' OR m.code ILIKE '%'||$1||'%' OR m.name ILIKE '%'||$1||'%') AND ($2='' OR m.status=$2)`
	var page MenuPage
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+filter, input.Keyword, input.Status).Scan(&page.Total); err != nil {
		return MenuPage{}, postgresError(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id::text,m.source_owner,m.code,m.parent_code,m.name,m.path,m.component,m.icon,m.sort_order,m.status`+filter+` ORDER BY m.sort_order,m.code LIMIT $3 OFFSET $4`, input.Keyword, input.Status, input.Limit, input.Offset)
	if err != nil {
		return MenuPage{}, postgresError(err)
	}
	defer rows.Close()
	for rows.Next() {
		menu, scanErr := scanMenu(rows)
		if scanErr != nil {
			return MenuPage{}, scanErr
		}
		page.Items = append(page.Items, menu)
	}
	if err = rows.Err(); err != nil {
		return MenuPage{}, postgresError(err)
	}
	return page, nil
}

func (s *PostgresStore) GetMenu(ctx context.Context, code string) (Menu, error) {
	return scanMenu(s.db.QueryRowContext(ctx, `SELECT id::text,source_owner,code,parent_code,name,path,component,icon,sort_order,status FROM menus WHERE code=$1`, code))
}

func (s *PostgresStore) ListPermissions(ctx context.Context, input ListPermissionsInput) (PermissionPage, error) {
	const from = ` FROM permission_actions a JOIN permission_resources r ON r.id=a.permission_resource_id`
	const filter = ` WHERE ($1='' OR a.code ILIKE '%'||$1||'%' OR a.name ILIKE '%'||$1||'%' OR COALESCE(a.description,'') ILIKE '%'||$1||'%') AND ($2='' OR a.status=$2)`
	var page PermissionPage
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+from+filter, input.Keyword, input.Status).Scan(&page.Total); err != nil {
		return PermissionPage{}, postgresError(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id::text,a.source_owner,r.code,a.code,a.name,a.description,a.status`+from+filter+` ORDER BY a.code LIMIT $3 OFFSET $4`, input.Keyword, input.Status, input.Limit, input.Offset)
	if err != nil {
		return PermissionPage{}, postgresError(err)
	}
	defer rows.Close()
	for rows.Next() {
		permission, scanErr := scanPermission(rows)
		if scanErr != nil {
			return PermissionPage{}, scanErr
		}
		page.Items = append(page.Items, permission)
	}
	if err = rows.Err(); err != nil {
		return PermissionPage{}, postgresError(err)
	}
	return page, nil
}

func (s *PostgresStore) GetPermission(ctx context.Context, code string) (Permission, error) {
	return scanPermission(s.db.QueryRowContext(ctx, `SELECT a.id::text,a.source_owner,r.code,a.code,a.name,a.description,a.status FROM permission_actions a JOIN permission_resources r ON r.id=a.permission_resource_id WHERE a.code=$1`, code))
}

func (s *PostgresStore) SetIdentityAccountStatus(ctx context.Context, input SetAccountStatusStoreInput) (IdentityAccount, error) {
	id, err := numericID(string(input.AccountID))
	if err != nil {
		return IdentityAccount{}, ErrStoreNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	defer tx.Rollback()
	var value IdentityAccount
	var databaseID int64
	var email, display sql.NullString
	err = tx.QueryRowContext(ctx, `UPDATE identity_accounts SET status=$1 WHERE id=$2 RETURNING id,identity_source_code,external_subject,username,email,display_name,status`, input.Status, id).Scan(&databaseID, &value.SourceCode, &value.ExternalSubject, &value.Username, &email, &display, &value.Status)
	if err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	if input.RevokeSessions {
		if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked=TRUE WHERE identity_account_id=$1 AND revoked=FALSE`, id); err != nil {
			return IdentityAccount{}, postgresError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	value.ID, value.Email, value.DisplayName = accountIDString(databaseID), email.String, display.String
	return value, nil
}

func (s *PostgresStore) ResetLocalPassword(ctx context.Context, input ResetLocalPasswordStoreInput) error {
	id, err := numericID(string(input.AccountID))
	if err != nil {
		return ErrStoreNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return postgresError(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE identity_accounts SET password_hash=$1,credential_version=credential_version+1 WHERE id=$2`, input.PasswordHash, id)
	if err != nil {
		return postgresError(err)
	}
	if err = requireAffected(result); err != nil {
		return err
	}
	if input.RevokeSessions {
		if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked=TRUE WHERE identity_account_id=$1 AND revoked=FALSE`, id); err != nil {
			return postgresError(err)
		}
	}
	return postgresErrorOrNil(tx.Commit())
}

func (s *PostgresStore) SyncCatalog(ctx context.Context, input CatalogSyncStoreInput) (CatalogSyncResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return CatalogSyncResult{}, postgresError(err)
	}
	defer tx.Rollback()
	var existingDigest string
	err = tx.QueryRowContext(ctx, `SELECT source_digest FROM catalog_source_states WHERE source_id=$1 FOR UPDATE`, input.SourceID).Scan(&existingDigest)
	if err == nil && existingDigest == input.Digest {
		if err = tx.Commit(); err != nil {
			return CatalogSyncResult{}, postgresError(err)
		}
		return CatalogSyncResult{SourceID: input.SourceID, Digest: input.Digest}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CatalogSyncResult{}, postgresError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_source_states(source_id,source_digest) VALUES($1,$2) ON CONFLICT(source_id) DO UPDATE SET source_digest=EXCLUDED.source_digest`, input.SourceID, input.Digest); err != nil {
		return CatalogSyncResult{}, postgresError(err)
	}
	result := CatalogSyncResult{SourceID: input.SourceID, Digest: input.Digest}
	permissionCodes := make([]string, 0, len(input.Permissions))
	for _, entry := range input.Permissions {
		permissionCodes = append(permissionCodes, entry.Code)
		var resourceID int64
		err = tx.QueryRowContext(ctx, `INSERT INTO permission_resources(source_owner,source_key,source_digest,code,name,description,status) VALUES($1,$2,$3,$2,$2,NULLIF($4,''),'enabled')
ON CONFLICT(source_owner,source_key) DO UPDATE SET source_digest=EXCLUDED.source_digest,description=EXCLUDED.description,status='enabled' RETURNING id`, input.SourceID, entry.Code, input.Digest, entry.Description).Scan(&resourceID)
		if err != nil {
			return CatalogSyncResult{}, postgresError(err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO permission_actions(source_owner,source_key,source_digest,permission_resource_id,code,name,description,status) VALUES($1,$2,$3,$4,$2,$2,NULLIF($5,''),'enabled')
ON CONFLICT(source_owner,source_key) DO UPDATE SET source_digest=EXCLUDED.source_digest,permission_resource_id=EXCLUDED.permission_resource_id,description=EXCLUDED.description,status='enabled'`, input.SourceID, entry.Code, input.Digest, resourceID, entry.Description)
		if err != nil {
			return CatalogSyncResult{}, postgresError(err)
		}
		result.PermissionsUpserted++
	}
	menuCodes := make([]string, 0, len(input.Menus))
	for _, entry := range input.Menus {
		menuCodes = append(menuCodes, entry.Code)
		_, err = tx.ExecContext(ctx, `INSERT INTO menus(source_owner,source_key,source_digest,code,parent_code,name,path,status) VALUES($1,$2,$3,$2,$4,$5,$6,'enabled')
ON CONFLICT(source_owner,source_key) DO UPDATE SET source_digest=EXCLUDED.source_digest,parent_code=EXCLUDED.parent_code,name=EXCLUDED.name,path=EXCLUDED.path,status='enabled'`, input.SourceID, entry.Code, input.Digest, entry.ParentCode, entry.DisplayName, entry.Path)
		if err != nil {
			return CatalogSyncResult{}, postgresError(err)
		}
		result.MenusUpserted++
	}
	if input.DisableStale {
		permissionResult, err := tx.ExecContext(ctx, `UPDATE permission_actions SET status='disabled' WHERE source_owner=$1 AND NOT(code=ANY($2::text[])) AND status<>'disabled'`, input.SourceID, pqTextArray(permissionCodes))
		if err != nil {
			return CatalogSyncResult{}, postgresError(err)
		}
		disabled, _ := permissionResult.RowsAffected()
		result.PermissionsDisabled = int(disabled)
		if _, err = tx.ExecContext(ctx, `UPDATE permission_resources SET status='disabled' WHERE source_owner=$1 AND NOT(code=ANY($2::text[]))`, input.SourceID, pqTextArray(permissionCodes)); err != nil {
			return CatalogSyncResult{}, postgresError(err)
		}
		menuResult, err := tx.ExecContext(ctx, `UPDATE menus SET status='disabled' WHERE source_owner=$1 AND NOT(code=ANY($2::text[])) AND status<>'disabled'`, input.SourceID, pqTextArray(menuCodes))
		if err != nil {
			return CatalogSyncResult{}, postgresError(err)
		}
		disabled, _ = menuResult.RowsAffected()
		result.MenusDisabled = int(disabled)
	}
	if err = tx.Commit(); err != nil {
		return CatalogSyncResult{}, postgresError(err)
	}
	return result, nil
}

const memberSelect = `SELECT m.id,m.tenant_id::text,t.code,m.identity_account_id,a.username,a.email,a.display_name,a.identity_source_code,a.external_subject,m.status,m.version,
EXISTS(SELECT 1 FROM managed_tenant_member_roles mg JOIN roles mr ON mr.id=mg.role_id AND mr.tenant_id=mg.tenant_id WHERE mg.tenant_id=m.tenant_id AND mg.tenant_member_id=m.id AND mg.source_owner='core.tenant-provision' AND mr.source_owner='core.tenant-provision' AND mr.source_key='tenant-owner'),
COALESCE((SELECT array_agg(r.code ORDER BY r.code) FROM tenant_member_roles g JOIN roles r ON r.id=g.role_id AND r.tenant_id=g.tenant_id WHERE g.tenant_id=m.tenant_id AND g.tenant_member_id=m.id),ARRAY[]::text[])
FROM tenant_members m JOIN tenants t ON t.id=m.tenant_id JOIN identity_accounts a ON a.id=m.identity_account_id`

type rowScanner interface{ Scan(...any) error }

func scanMember(row rowScanner) (TenantMember, error) {
	var value TenantMember
	var id, accountID int64
	var email, display sql.NullString
	var roles stringArray
	err := row.Scan(&id, &value.TenantID, &value.TenantCode, &accountID, &value.AccountUsername, &email, &display, &value.AccountSourceCode, &value.AccountExternalSubject, &value.Status, &value.Version, &value.ManagedOwnerGrant, &roles)
	if err != nil {
		return TenantMember{}, postgresError(err)
	}
	value.ID, value.AccountID, value.AccountEmail, value.AccountDisplayName = memberIDString(id), accountIDString(accountID), email.String, display.String
	value.ManualRoleCodes = append([]string(nil), roles...)
	return value, nil
}

func scanIdentityAccount(row rowScanner) (IdentityAccount, error) {
	var value IdentityAccount
	var id int64
	var email, display sql.NullString
	if err := row.Scan(&id, &value.SourceCode, &value.ExternalSubject, &value.Username, &email, &display, &value.Status); err != nil {
		return IdentityAccount{}, postgresError(err)
	}
	value.ID, value.Email, value.DisplayName = accountIDString(id), email.String, display.String
	return value, nil
}

const roleSelect = `SELECT r.id,r.tenant_id::text,r.code,r.name,r.status,r.managed,r.version,
COALESCE((SELECT array_agg(a.code ORDER BY a.code) FROM role_permission_actions g JOIN permission_actions a ON a.id=g.permission_action_id WHERE g.tenant_id=r.tenant_id AND g.role_id=r.id),ARRAY[]::text[]),
COALESCE((SELECT array_agg(m.code ORDER BY m.code) FROM role_menus g JOIN menus m ON m.id=g.menu_id WHERE g.tenant_id=r.tenant_id AND g.role_id=r.id),ARRAY[]::text[])
FROM roles r`

func scanRole(row rowScanner) (TenantRole, error) {
	var value TenantRole
	var id int64
	if err := row.Scan(&id, &value.TenantID, &value.Code, &value.DisplayName, &value.Status, &value.Managed, &value.Version, (*stringArray)(&value.PermissionCodes), (*stringArray)(&value.MenuCodes)); err != nil {
		return TenantRole{}, postgresError(err)
	}
	value.ID = roleIDString(id)
	return value, nil
}

func scanMenu(row rowScanner) (Menu, error) {
	var value Menu
	if err := row.Scan(&value.ID, &value.SourceID, &value.Code, &value.ParentCode, &value.DisplayName, &value.Path, &value.Component, &value.Icon, &value.SortOrder, &value.Status); err != nil {
		return Menu{}, postgresError(err)
	}
	return value, nil
}

func scanPermission(row rowScanner) (Permission, error) {
	var value Permission
	var description sql.NullString
	if err := row.Scan(&value.ID, &value.SourceID, &value.ResourceCode, &value.Code, &value.DisplayName, &description, &value.Status); err != nil {
		return Permission{}, postgresError(err)
	}
	value.Description = description.String
	return value, nil
}

func (s *PostgresStore) getRole(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key TenantRoleKey) (TenantRole, error) {
	id, err := numericID(string(key.RoleID))
	if err != nil {
		return TenantRole{}, ErrStoreNotFound
	}
	tenantID, err := numericID(key.TenantID)
	if err != nil {
		return TenantRole{}, ErrStoreNotFound
	}
	return scanRole(queryer.QueryRowContext(ctx, roleSelect+` WHERE r.tenant_id=$1 AND r.id=$2`, tenantID, id))
}

func (s *PostgresStore) mutateRole(ctx context.Context, key TenantRoleKey, version uint64, rejectManaged bool, set string, value any) (TenantRole, error) {
	id, err := numericID(string(key.RoleID))
	if err != nil {
		return TenantRole{}, ErrStoreNotFound
	}
	tenantID, err := numericID(key.TenantID)
	if err != nil {
		return TenantRole{}, ErrStoreNotFound
	}
	query := `UPDATE roles SET ` + set + `,version=version+1 WHERE tenant_id=$2 AND id=$3 AND version=$4`
	if rejectManaged {
		query += ` AND managed=FALSE`
	}
	result, err := s.db.ExecContext(ctx, query, value, tenantID, id, version)
	if err != nil {
		return TenantRole{}, postgresError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		var managed bool
		var current uint64
		err = s.db.QueryRowContext(ctx, `SELECT managed,version FROM roles WHERE tenant_id=$1 AND id=$2`, tenantID, id).Scan(&managed, &current)
		if err != nil {
			return TenantRole{}, postgresError(err)
		}
		if managed && rejectManaged {
			return TenantRole{}, ErrStorePermissionDenied
		}
		return TenantRole{}, ErrStoreConcurrentWrite
	}
	return s.getRole(ctx, s.db, key)
}

func (s *PostgresStore) replaceRoleCatalog(ctx context.Context, key TenantRoleKey, version uint64, rejectManaged bool, codes []string, permissions bool) (TenantRole, error) {
	id, err := numericID(string(key.RoleID))
	if err != nil {
		return TenantRole{}, ErrStoreNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TenantRole{}, postgresError(err)
	}
	defer tx.Rollback()
	tenantID, err := numericID(key.TenantID)
	if err != nil {
		return TenantRole{}, ErrStoreNotFound
	}
	var current uint64
	var managed bool
	err = tx.QueryRowContext(ctx, `SELECT version,managed FROM roles WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, id).Scan(&current, &managed)
	if err != nil {
		return TenantRole{}, postgresError(err)
	}
	if managed && rejectManaged {
		return TenantRole{}, ErrStorePermissionDenied
	}
	if current != version {
		return TenantRole{}, ErrStoreConcurrentWrite
	}
	table, column, catalogTable := "role_menus", "menu_id", "menus"
	if permissions {
		table, column, catalogTable = "role_permission_actions", "permission_action_id", "permission_actions"
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE tenant_id=$1 AND role_id=$2`, tenantID, id); err != nil {
		return TenantRole{}, postgresError(err)
	}
	for _, code := range codes {
		result, err := tx.ExecContext(ctx, `INSERT INTO `+table+`(tenant_id,role_id,`+column+`) SELECT $1,$2,id FROM `+catalogTable+` WHERE code=$3 AND status='enabled'`, tenantID, id, code)
		if err != nil {
			return TenantRole{}, postgresError(err)
		}
		if err = requireAffected(result); err != nil {
			return TenantRole{}, ErrStoreInvalidInput
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE roles SET version=version+1 WHERE tenant_id=$1 AND id=$2`, tenantID, id); err != nil {
		return TenantRole{}, postgresError(err)
	}
	role, err := s.getRole(ctx, tx, key)
	if err != nil {
		return TenantRole{}, err
	}
	if err = tx.Commit(); err != nil {
		return TenantRole{}, postgresError(err)
	}
	return role, nil
}

func lockMember(ctx context.Context, tx *sql.Tx, tenant string, member TenantMemberID) (int64, int64, error) {
	tenantID, err := numericID(tenant)
	if err != nil {
		return 0, 0, ErrStoreNotFound
	}
	memberID, err := numericID(string(member))
	if err != nil {
		return 0, 0, ErrStoreNotFound
	}
	err = tx.QueryRowContext(ctx, `SELECT tenant_id FROM tenant_members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, memberID).Scan(&tenantID)
	if err != nil {
		return 0, 0, postgresError(err)
	}
	return tenantID, memberID, nil
}

func lockMemberByCode(ctx context.Context, tx *sql.Tx, tenant string, member TenantMemberID) (int64, int64, error) {
	memberID, err := numericID(string(member))
	if err != nil {
		return 0, 0, ErrStoreNotFound
	}
	var tenantID int64
	err = tx.QueryRowContext(ctx, `SELECT m.tenant_id FROM tenant_members m JOIN tenants t ON t.id=m.tenant_id WHERE t.code=$1 AND m.id=$2 FOR UPDATE`, tenant, memberID).Scan(&tenantID)
	if err != nil {
		return 0, 0, postgresError(err)
	}
	return tenantID, memberID, nil
}
func distinguishTenantVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, tenantID int64) error {
	var exists bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1)`, tenantID).Scan(&exists); err != nil {
		return postgresError(err)
	}
	if !exists {
		return ErrStoreNotFound
	}
	return ErrStoreConcurrentWrite
}
func distinguishMemberVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, tenantID int64, id int64) error {
	var exists bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tenant_members WHERE tenant_id=$1 AND id=$2)`, tenantID, id).Scan(&exists); err != nil {
		return postgresError(err)
	}
	if !exists {
		return ErrStoreNotFound
	}
	return ErrStoreConcurrentWrite
}
func createSessionTx(ctx context.Context, tx *sql.Tx, session StoredSession) error {
	accountID, err := numericID(string(session.AccountID))
	if err != nil {
		return ErrStoreFailedPrecondition
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(session_id,tenant_id,identity_account_id,access_token_hash,refresh_token_hash,access_expires_at,refresh_expires_at,revoked)
SELECT $1,t.id,$2,$3,$4,$5,$6,$7 FROM tenants t JOIN tenant_members m ON m.tenant_id=t.id AND m.identity_account_id=$2 JOIN identity_accounts a ON a.id=$2
WHERE t.code=$8 AND t.status='enabled' AND m.status='enabled' AND a.status='enabled'`, session.ID, accountID, session.AccessTokenHash, session.RefreshTokenHash, session.AccessExpiresAt, session.RefreshExpiresAt, session.Revoked, session.Tenant)
	if err != nil {
		return postgresError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrStoreCredentialUnavailable
	}
	return nil
}

type sqlStateError interface{ SQLState() string }

func postgresError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStoreNotFound
	}
	var state sqlStateError
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "23505":
			return ErrStoreConflict
		case "40001", "40P01":
			return ErrStoreConcurrentWrite
		case "23503", "23514", "23P01":
			return ErrStoreFailedPrecondition
		}
	}
	return errPostgresStoreFailure
}
func postgresCredentialError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStoreCredentialUnavailable
	}
	return postgresError(err)
}
func postgresErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return postgresError(err)
}
func requireAffected(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return postgresError(err)
	}
	if count != 1 {
		return ErrStoreNotFound
	}
	return nil
}
func numericID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrStoreNotFound
	}
	return id, nil
}
func accountIDString(id int64) IdentityAccountID { return IdentityAccountID(strconv.FormatInt(id, 10)) }
func memberIDString(id int64) TenantMemberID     { return TenantMemberID(strconv.FormatInt(id, 10)) }
func roleIDString(id int64) TenantRoleID         { return TenantRoleID(strconv.FormatInt(id, 10)) }

type stringArray []string

func (a *stringArray) Scan(src any) error {
	var raw string
	switch value := src.(type) {
	case string:
		raw = value
	case []byte:
		raw = string(value)
	case nil:
		*a = nil
		return nil
	default:
		return errPostgresStoreFailure
	}
	if raw == "{}" {
		*a = nil
		return nil
	}
	raw = strings.TrimSuffix(strings.TrimPrefix(raw, "{"), "}")
	if raw == "" {
		*a = nil
		return nil
	}
	*a = strings.Split(raw, ",")
	return nil
}

type textArray []string

func (a textArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	return "{" + strings.Join(a, ",") + "}", nil
}
func pqTextArray(values []string) any { return textArray(values) }
