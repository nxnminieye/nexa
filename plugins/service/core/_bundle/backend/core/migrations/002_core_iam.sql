ALTER TABLE identity_accounts
  ADD COLUMN status TEXT NOT NULL DEFAULT 'enabled'
    CHECK (status IN ('enabled', 'disabled')),
  ADD COLUMN credential_version BIGINT NOT NULL DEFAULT 1
    CHECK (credential_version >= 1);

DROP INDEX identity_accounts_external_identity;
CREATE UNIQUE INDEX identity_accounts_external_identity
  ON identity_accounts (identity_source_code, external_subject)
  WHERE external_subject <> '';

ALTER TABLE roles
  ADD COLUMN description TEXT,
  ADD COLUMN status TEXT NOT NULL DEFAULT 'enabled'
    CHECK (status IN ('enabled', 'disabled')),
  ADD COLUMN managed BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN source_owner TEXT NOT NULL DEFAULT '',
  ADD COLUMN source_key TEXT NOT NULL DEFAULT '',
  ADD COLUMN source_digest TEXT NOT NULL DEFAULT '',
  ADD COLUMN version BIGINT NOT NULL DEFAULT 1
    CHECK (version >= 1),
  ADD CONSTRAINT roles_managed_owner_check CHECK (
    (managed = FALSE AND source_owner = '' AND source_key = '' AND source_digest = '') OR
    (managed = TRUE AND source_owner <> '' AND source_key <> '' AND source_digest <> '')
  ),
  ADD CONSTRAINT roles_tenant_id_id_unique UNIQUE (tenant_id, id);

CREATE UNIQUE INDEX roles_tenant_source_owner_key
  ON roles (tenant_id, source_owner, source_key)
  WHERE managed = TRUE;

ALTER TABLE tenant_members
  ADD COLUMN version BIGINT NOT NULL DEFAULT 1
    CHECK (version >= 1),
  ADD CONSTRAINT tenant_members_tenant_id_id_unique UNIQUE (tenant_id, id);

ALTER TABLE tenants
  ADD COLUMN version BIGINT NOT NULL DEFAULT 1
    CHECK (version >= 1);

ALTER TABLE tenant_member_roles
  ADD COLUMN id BIGSERIAL,
  ADD COLUMN tenant_id BIGINT;

UPDATE tenant_member_roles grant_row
SET tenant_id = member_row.tenant_id
FROM tenant_members member_row
WHERE member_row.id = grant_row.tenant_member_id;

ALTER TABLE tenant_member_roles
  ALTER COLUMN tenant_id SET NOT NULL,
  DROP CONSTRAINT tenant_member_roles_pkey,
  ADD PRIMARY KEY (id),
  ADD CONSTRAINT tenant_member_roles_tenant_member_role_unique
    UNIQUE (tenant_id, tenant_member_id, role_id),
  ADD CONSTRAINT tenant_member_roles_member_tenant_fk
    FOREIGN KEY (tenant_id, tenant_member_id)
    REFERENCES tenant_members (tenant_id, id) ON DELETE CASCADE,
  ADD CONSTRAINT tenant_member_roles_role_tenant_fk
    FOREIGN KEY (tenant_id, role_id)
    REFERENCES roles (tenant_id, id) ON DELETE CASCADE;

CREATE TABLE managed_tenant_member_roles (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL,
  tenant_member_id BIGINT NOT NULL,
  role_id BIGINT NOT NULL,
  source_owner TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  CHECK (source_owner <> '' AND source_digest <> ''),
  UNIQUE (tenant_id, tenant_member_id, role_id, source_owner),
  FOREIGN KEY (tenant_id, tenant_member_id)
    REFERENCES tenant_members (tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, role_id)
    REFERENCES roles (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE menus (
  id BIGSERIAL PRIMARY KEY,
  source_owner TEXT NOT NULL,
  source_key TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  code TEXT NOT NULL UNIQUE,
  parent_code TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  path TEXT NOT NULL DEFAULT '',
  component TEXT NOT NULL DEFAULT '',
  icon TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'enabled'
    CHECK (status IN ('enabled', 'disabled')),
  CHECK (source_owner <> '' AND source_key <> '' AND source_digest <> ''),
  UNIQUE (source_owner, source_key)
);

CREATE TABLE permission_resources (
  id BIGSERIAL PRIMARY KEY,
  source_owner TEXT NOT NULL,
  source_key TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  code TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'enabled'
    CHECK (status IN ('enabled', 'disabled')),
  CHECK (source_owner <> '' AND source_key <> '' AND source_digest <> ''),
  UNIQUE (source_owner, source_key)
);

CREATE TABLE permission_actions (
  id BIGSERIAL PRIMARY KEY,
  source_owner TEXT NOT NULL,
  source_key TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  permission_resource_id BIGINT NOT NULL REFERENCES permission_resources(id),
  code TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'enabled'
    CHECK (status IN ('enabled', 'disabled')),
  CHECK (source_owner <> '' AND source_key <> '' AND source_digest <> ''),
  UNIQUE (source_owner, source_key)
);

CREATE TABLE catalog_source_states (
  id BIGSERIAL PRIMARY KEY,
  source_id TEXT NOT NULL UNIQUE,
  source_digest TEXT NOT NULL
);

CREATE TABLE role_menus (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL,
  role_id BIGINT NOT NULL,
  menu_id BIGINT NOT NULL REFERENCES menus(id),
  UNIQUE (tenant_id, role_id, menu_id),
  FOREIGN KEY (tenant_id, role_id)
    REFERENCES roles (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE role_permission_actions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL,
  role_id BIGINT NOT NULL,
  permission_action_id BIGINT NOT NULL REFERENCES permission_actions(id),
  UNIQUE (tenant_id, role_id, permission_action_id),
  FOREIGN KEY (tenant_id, role_id)
    REFERENCES roles (tenant_id, id) ON DELETE CASCADE
);
