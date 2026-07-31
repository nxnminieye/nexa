package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
)

var (
	ErrApply = errors.New("core migration apply failed")
	ErrDrift = errors.New("core migration digest drift")
)

//go:embed *.sql
var migrationFiles embed.FS

func Apply(ctx context.Context, database *sql.DB) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("%w: database", ErrApply)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY,digest TEXT NOT NULL)`); err != nil {
		return 0, fmt.Errorf("%w: initialize", ErrApply)
	}
	entries, err := migrationFiles.ReadDir(".")
	if err != nil {
		return 0, fmt.Errorf("%w: inventory", ErrApply)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && path.Ext(entry.Name()) == ".sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	applied := 0
	for _, name := range names {
		data, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			return applied, fmt.Errorf("%w: read", ErrApply)
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		var existing string
		lookupErr := database.QueryRowContext(ctx, `SELECT digest FROM schema_migrations WHERE name=$1`, name).Scan(&existing)
		if lookupErr == nil {
			if existing != digest {
				return applied, fmt.Errorf("%w: %s", ErrDrift, name)
			}
			continue
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return applied, fmt.Errorf("%w: lookup", ErrApply)
		}
		transaction, beginErr := database.BeginTx(ctx, nil)
		if beginErr != nil {
			return applied, fmt.Errorf("%w: begin", ErrApply)
		}
		if _, executeErr := transaction.ExecContext(ctx, string(data)); executeErr != nil {
			_ = transaction.Rollback()
			return applied, fmt.Errorf("%w: execute %s", ErrApply, name)
		}
		if _, recordErr := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(name,digest) VALUES($1,$2)`, name, digest); recordErr != nil {
			_ = transaction.Rollback()
			return applied, fmt.Errorf("%w: record", ErrApply)
		}
		if commitErr := transaction.Commit(); commitErr != nil {
			return applied, fmt.Errorf("%w: commit", ErrApply)
		}
		applied++
	}
	return applied, nil
}
