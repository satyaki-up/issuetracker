PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS issues (
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
  FOREIGN KEY (parent_id) REFERENCES issues(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_issues_parent ON issues(parent_id);

CREATE TRIGGER IF NOT EXISTS trg_issues_last_updated_at
AFTER UPDATE ON issues
FOR EACH ROW
BEGIN
  UPDATE issues SET last_updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
