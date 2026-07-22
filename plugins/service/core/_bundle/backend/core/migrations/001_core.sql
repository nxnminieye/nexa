CREATE TABLE tenants (
  id BIGSERIAL PRIMARY KEY,
  code TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('enabled', 'disabled'))
);

CREATE TABLE identity_accounts (
  id BIGSERIAL PRIMARY KEY,
  identity_source_code TEXT NOT NULL DEFAULT '',
  external_subject TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL UNIQUE,
  email TEXT,
  display_name TEXT,
  password_hash TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX identity_accounts_external_identity
  ON identity_accounts (identity_source_code, external_subject)
  WHERE external_subject <> '';

CREATE TABLE tenant_members (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  identity_account_id BIGINT NOT NULL REFERENCES identity_accounts(id),
  status TEXT NOT NULL CHECK (status IN ('enabled', 'disabled')),
  UNIQUE (tenant_id, identity_account_id)
);

CREATE TABLE roles (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  code TEXT NOT NULL,
  name TEXT NOT NULL,
  UNIQUE (tenant_id, code)
);

CREATE TABLE permissions (
  id BIGSERIAL PRIMARY KEY,
  code TEXT NOT NULL UNIQUE,
  description TEXT
);

CREATE TABLE tenant_member_roles (
  tenant_member_id BIGINT NOT NULL REFERENCES tenant_members(id) ON DELETE CASCADE,
  role_id BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  PRIMARY KEY (tenant_member_id, role_id)
);

CREATE TABLE role_permissions (
  role_id BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  permission_id BIGINT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
  PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE auth_sessions (
  id BIGSERIAL PRIMARY KEY,
  session_id TEXT NOT NULL UNIQUE,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  identity_account_id BIGINT NOT NULL REFERENCES identity_accounts(id),
  access_token_hash TEXT NOT NULL,
  refresh_token_hash TEXT NOT NULL UNIQUE,
  access_expires_at TIMESTAMPTZ NOT NULL,
  refresh_expires_at TIMESTAMPTZ NOT NULL,
  revoked BOOLEAN NOT NULL DEFAULT FALSE
);
