package coreapp

import (
	"context"
	"database/sql"
	"errors"
)

// newPostgresPolicyReconciler owns the durable Core role bindings. Consumers
// provide catalog additions, but never write Core policy tables themselves.
func newPostgresPolicyReconciler(database *sql.DB) (PolicyReconciler, error) {
	if database == nil {
		return nil, errors.New("policy database is unavailable")
	}
	return &postgresPolicyReconciler{database: database}, nil
}

type postgresPolicyReconciler struct{ database *sql.DB }

func (r *postgresPolicyReconciler) ReconcilePolicy(ctx context.Context, _ PolicyReconcileInput) error {
	if r == nil || r.database == nil {
		return errors.New("policy database is unavailable")
	}
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `INSERT INTO role_permission_actions(tenant_id,role_id,permission_action_id)
SELECT r.tenant_id,r.id,p.id FROM roles r CROSS JOIN permission_actions p
WHERE r.managed=TRUE AND r.source_owner='core.tenant-provision' AND r.source_key='tenant-owner' AND r.status='enabled' AND p.status='enabled'
ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM role_permission_actions g USING roles r,permission_actions p
WHERE g.role_id=r.id AND g.permission_action_id=p.id AND r.managed=TRUE AND r.source_owner='core.tenant-provision' AND r.source_key='tenant-owner'
AND (r.status<>'enabled' OR p.status<>'enabled')`); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO role_menus(tenant_id,role_id,menu_id)
SELECT r.tenant_id,r.id,m.id FROM roles r CROSS JOIN menus m
WHERE r.managed=TRUE AND r.source_owner='core.tenant-provision' AND r.source_key='tenant-owner' AND r.status='enabled' AND m.status='enabled'
ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM role_menus g USING roles r,menus m
WHERE g.role_id=r.id AND g.menu_id=m.id AND r.managed=TRUE AND r.source_owner='core.tenant-provision' AND r.source_key='tenant-owner'
AND (r.status<>'enabled' OR m.status<>'enabled')`); err != nil {
		return err
	}
	return transaction.Commit()
}
