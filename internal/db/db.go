package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

func DefaultPath() string {
	if env := os.Getenv("IT_DB_PATH"); env != "" {
		return env
	}
	return filepath.Join(".it", "issues.db")
}

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply %q: %w", p, err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(schema)); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if err := ensureIssuesTableMinimalShape(ctx, db); err != nil {
		return err
	}
	if err := dropLegacyTables(ctx, db); err != nil {
		return err
	}
	// Re-apply schema to ensure indexes/triggers exist after any table rebuild.
	if _, err := db.ExecContext(ctx, string(schema)); err != nil {
		return fmt.Errorf("re-apply schema: %w", err)
	}
	return nil
}

func issueColumns(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='issues'`).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("inspect issues table: %w", err)
	}

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(issues)`)
	if err != nil {
		return nil, fmt.Errorf("inspect issues table: %w", err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan table info: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read table info: %w", err)
	}
	return columns, nil
}

func ensureIssuesTableMinimalShape(ctx context.Context, db *sql.DB) error {
	columns, err := issueColumns(ctx, db)
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}

	required := []string{
		"id", "category", "title", "body", "state", "parent_id", "version", "blocked_by", "created_at", "last_updated_at", "closed_at",
	}
	if hasAllAndOnly(columns, required) {
		_, _ = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS trg_issues_updated_at`)
		return nil
	}

	lastUpdatedExpr := "CURRENT_TIMESTAMP"
	switch {
	case columns["last_updated_at"]:
		lastUpdatedExpr = "last_updated_at"
	case columns["updated_at"]:
		lastUpdatedExpr = "updated_at"
	case columns["created_at"]:
		lastUpdatedExpr = "created_at"
	}

	categoryExpr := "'task'"
	if columns["category"] {
		categoryExpr = "category"
	}
	bodyExpr := "''"
	if columns["body"] {
		bodyExpr = "body"
	}
	stateExpr := "'todo'"
	if columns["state"] {
		stateExpr = "state"
	}
	versionExpr := "1"
	if columns["version"] {
		versionExpr = "version"
	}
	blockedByExpr := "'[]'"
	if columns["blocked_by"] {
		blockedByExpr = "blocked_by"
	}
	parentExpr := "NULL"
	if columns["parent_id"] {
		parentExpr = "parent_id"
	}
	closedExpr := "NULL"
	if columns["closed_at"] {
		closedExpr = "closed_at"
	}
	createdExpr := "CURRENT_TIMESTAMP"
	if columns["created_at"] {
		createdExpr = "created_at"
	}

	stmts := []string{
		"PRAGMA foreign_keys = OFF",
		"BEGIN IMMEDIATE",
		`CREATE TABLE issues_new (
			id TEXT PRIMARY KEY,
			category TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			parent_id TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			blocked_by TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
			last_updated_at TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
			closed_at TEXT,
			FOREIGN KEY (parent_id) REFERENCES issues_new(id) ON DELETE SET NULL
		)`,
		fmt.Sprintf(`
			INSERT INTO issues_new(
				id, category, title, body, state, parent_id, version, blocked_by, created_at, last_updated_at, closed_at
			)
			SELECT
				id,
				%s,
				title,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s
			FROM issues
		`, categoryExpr, bodyExpr, stateExpr, parentExpr, versionExpr, blockedByExpr, createdExpr, lastUpdatedExpr, closedExpr),
		"DROP TABLE issues",
		"ALTER TABLE issues_new RENAME TO issues",
		"COMMIT",
		"PRAGMA foreign_keys = ON",
	}

	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_, _ = db.ExecContext(ctx, "ROLLBACK")
			_, _ = db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
			return fmt.Errorf("migrate issues table to minimal shape: %w", err)
		}
	}
	_, _ = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS trg_issues_updated_at`)
	return nil
}

func dropLegacyTables(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		"DROP TABLE IF EXISTS issue_state_history",
		"DROP TABLE IF EXISTS projects",
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("drop legacy table: %w", err)
		}
	}
	return nil
}

func hasAllAndOnly(columns map[string]bool, wanted []string) bool {
	if len(columns) != len(wanted) {
		return false
	}
	for _, c := range wanted {
		if !columns[c] {
			return false
		}
	}
	return true
}
