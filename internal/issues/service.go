package issues

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var projectPrefixRe = regexp.MustCompile(`^[a-z0-9]{3}$`)
var issueIDRe = regexp.MustCompile(`^[a-z0-9]{3}-[0-9]+$`)

const sqliteTimeLayout = "2006-01-02 15:04:05"

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) CreateIssue(ctx context.Context, projectPrefix string, category Category, title, body string, parentID *string, blockedBy []string) (*Issue, error) {
	projectPrefix = strings.TrimSpace(strings.ToLower(projectPrefix))
	title = strings.TrimSpace(title)
	if !projectPrefixRe.MatchString(projectPrefix) {
		return nil, fmt.Errorf("%w: project prefix must be exactly 3 lowercase alphanumeric chars", ErrInvalidInput)
	}
	if !IsValidCategory(category) {
		return nil, fmt.Errorf("%w: unknown category %q", ErrInvalidInput, category)
	}
	if title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}

	const maxCreateAttempts = 8
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}

		number, err := randomIssueNumber()
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		issueID := fmt.Sprintf("%s-%d", projectPrefix, number)

		var cleanParent any
		requiredParentCategory, needsParent := expectedParentCategory(category)
		hasParent := parentID != nil && strings.TrimSpace(*parentID) != ""
		if needsParent && !hasParent {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: category %q requires parent category %q", ErrInvalidInput, category, requiredParentCategory)
		}
		if !needsParent && hasParent {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: category %q cannot have a parent", ErrInvalidInput, category)
		}
		if hasParent {
			pid := strings.TrimSpace(*parentID)
			parent, err := getIssueByIDTx(ctx, tx, pid)
			if err != nil {
				_ = tx.Rollback()
				if errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("%w: parent issue %q not found", ErrNotFound, pid)
				}
				return nil, err
			}
			if parent.ProjectPrefix != projectPrefix {
				_ = tx.Rollback()
				return nil, fmt.Errorf("%w: parent issue must be in same project", ErrInvalidInput)
			}
			if parent.Category != requiredParentCategory {
				_ = tx.Rollback()
				return nil, fmt.Errorf("%w: category %q requires parent category %q", ErrInvalidInput, category, requiredParentCategory)
			}
			cleanParent = pid
		}

		normalizedBlockedBy, err := normalizeBlockedByTx(ctx, tx, issueID, projectPrefix, blockedBy)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		blockedByJSON, err := json.Marshal(normalizedBlockedBy)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("marshal blocked_by: %w", err)
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO issues(id, category, title, body, state, parent_id, version, blocked_by)
			VALUES (?, ?, ?, ?, 'todo', ?, 1, ?)
		`, issueID, string(category), title, body, cleanParent, string(blockedByJSON))
		if err != nil {
			if isUniqueViolation(err) {
				_ = tx.Rollback()
				continue
			}
			_ = tx.Rollback()
			return nil, err
		}

		issue, err := getIssueByIDTx(ctx, tx, issueID)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}

		if err := tx.Commit(); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return nil, err
		}
		return issue, nil
	}
	return nil, fmt.Errorf("%w: failed to allocate issue number after retries", ErrConflict)
}

func (s *Service) GetIssue(ctx context.Context, id string) (*Issue, error) {
	return getIssueByIDDB(ctx, s.db, strings.TrimSpace(id))
}

func (s *Service) ListIssues(ctx context.Context, projectPrefix string, state *State) ([]Issue, error) {
	conds := []string{"1=1"}
	args := make([]any, 0, 2)
	if p := strings.TrimSpace(projectPrefix); p != "" {
		conds = append(conds, "id LIKE ?")
		args = append(args, strings.ToLower(p)+"-%")
	}
	if state != nil {
		if !IsValidState(*state) {
			return nil, fmt.Errorf("%w: unknown state %q", ErrInvalidInput, *state)
		}
		conds = append(conds, "state = ?")
		args = append(args, string(*state))
	}
	query := fmt.Sprintf(`
		SELECT id, category, title, body, state, parent_id, version, blocked_by, created_at, last_updated_at, closed_at
		FROM issues
		WHERE %s
		ORDER BY created_at ASC, id ASC
	`, strings.Join(conds, " AND "))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) TransitionState(ctx context.Context, id string, to State, expectedVersion *int64) (*Issue, error) {
	if !IsValidState(to) {
		return nil, fmt.Errorf("%w: unknown target state %q", ErrInvalidInput, to)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	issue, err := getIssueByIDTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(issue.State, to); err != nil {
		return nil, err
	}

	if to == StateInProgress {
		unresolved, err := unresolvedBlockedByTx(ctx, tx, issue)
		if err != nil {
			return nil, err
		}
		if len(unresolved) > 0 {
			return nil, fmt.Errorf("%w: blocked_by not done: %s", ErrInvalidInput, strings.Join(unresolved, ","))
		}
	}

	params := []any{string(to)}
	setParts := []string{"state = ?", "version = version + 1", "last_updated_at = CURRENT_TIMESTAMP"}
	if to == StateDone || to == StateCanceled {
		setParts = append(setParts, "closed_at = CURRENT_TIMESTAMP")
	} else {
		setParts = append(setParts, "closed_at = NULL")
	}

	query := fmt.Sprintf("UPDATE issues SET %s WHERE id = ?", strings.Join(setParts, ", "))
	params = append(params, id)
	if expectedVersion != nil {
		query += " AND version = ?"
		params = append(params, *expectedVersion)
	}

	res, err := tx.ExecContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		if expectedVersion != nil {
			return nil, fmt.Errorf("%w: stale write; expected version %d", ErrConflict, *expectedVersion)
		}
		return nil, fmt.Errorf("%w: issue %q not found", ErrNotFound, id)
	}

	updated, err := getIssueByIDTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) SetParent(ctx context.Context, id string, parentID *string, expectedVersion *int64) (*Issue, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	issue, err := getIssueByIDTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	requiredParentCategory, needsParent := expectedParentCategory(issue.Category)
	var newParent any
	if needsParent && (parentID == nil || strings.TrimSpace(*parentID) == "") {
		return nil, fmt.Errorf("%w: category %q requires parent category %q", ErrInvalidInput, issue.Category, requiredParentCategory)
	}
	if !needsParent && parentID != nil && strings.TrimSpace(*parentID) != "" {
		return nil, fmt.Errorf("%w: category %q cannot have a parent", ErrInvalidInput, issue.Category)
	}

	if needsParent {
		pid := strings.TrimSpace(*parentID)
		parent, err := getIssueByIDTx(ctx, tx, pid)
		if err != nil {
			return nil, err
		}
		if parent.ProjectPrefix != issue.ProjectPrefix {
			return nil, fmt.Errorf("%w: parent must be in same project", ErrInvalidInput)
		}
		if parent.Category != requiredParentCategory {
			return nil, fmt.Errorf("%w: category %q requires parent category %q", ErrInvalidInput, issue.Category, requiredParentCategory)
		}
		newParent = pid
	}

	query := "UPDATE issues SET parent_id = ?, version = version + 1, last_updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	args := []any{newParent, id}
	if expectedVersion != nil {
		query += " AND version = ?"
		args = append(args, *expectedVersion)
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		if expectedVersion != nil {
			return nil, fmt.Errorf("%w: stale write; expected version %d", ErrConflict, *expectedVersion)
		}
		return nil, fmt.Errorf("%w: issue %q not found", ErrNotFound, id)
	}

	updated, err := getIssueByIDTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) SetBlockedBy(ctx context.Context, id string, blockedBy []string, expectedVersion *int64) (*Issue, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	issue, err := getIssueByIDTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeBlockedByTx(ctx, tx, issue.ID, issue.ProjectPrefix, blockedBy)
	if err != nil {
		return nil, err
	}
	blockedByJSON, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal blocked_by: %w", err)
	}

	query := "UPDATE issues SET blocked_by = ?, version = version + 1, last_updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	args := []any{string(blockedByJSON), id}
	if expectedVersion != nil {
		query += " AND version = ?"
		args = append(args, *expectedVersion)
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		if expectedVersion != nil {
			return nil, fmt.Errorf("%w: stale write; expected version %d", ErrConflict, *expectedVersion)
		}
		return nil, fmt.Errorf("%w: issue %q not found", ErrNotFound, id)
	}

	updated, err := getIssueByIDTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) Tree(ctx context.Context, projectPrefix string) ([]TreeNode, error) {
	issuesList, err := s.ListIssues(ctx, projectPrefix, nil)
	if err != nil {
		return nil, err
	}

	children := make(map[string][]Issue)
	roots := make([]Issue, 0)
	for _, is := range issuesList {
		if is.ParentID == nil {
			roots = append(roots, is)
			continue
		}
		children[*is.ParentID] = append(children[*is.ParentID], is)
	}

	var build func(Issue) TreeNode
	build = func(root Issue) TreeNode {
		node := TreeNode{Issue: root}
		for _, ch := range children[root.ID] {
			node.Children = append(node.Children, build(ch))
		}
		return node
	}

	out := make([]TreeNode, 0, len(roots))
	for _, root := range roots {
		out = append(out, build(root))
	}
	return out, nil
}

func randomIssueNumber() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return 0, err
	}
	return 100000 + n.Int64(), nil
}

func getIssueByIDDB(ctx context.Context, db *sql.DB, id string) (*Issue, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, category, title, body, state, parent_id, version, blocked_by, created_at, last_updated_at, closed_at
		FROM issues
		WHERE id = ?
	`, id)
	issue, err := scanIssue(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: issue %q not found", ErrNotFound, id)
		}
		return nil, err
	}
	return &issue, nil
}

func getIssueByIDTx(ctx context.Context, tx *sql.Tx, id string) (*Issue, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, category, title, body, state, parent_id, version, blocked_by, created_at, last_updated_at, closed_at
		FROM issues
		WHERE id = ?
	`, id)
	issue, err := scanIssue(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: issue %q not found", ErrNotFound, id)
		}
		return nil, err
	}
	return &issue, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanIssue(row scanner) (Issue, error) {
	var is Issue
	var parent sql.NullString
	var blockedByRaw sql.NullString
	var created string
	var lastUpdated string
	var closed sql.NullString
	if err := row.Scan(
		&is.ID,
		&is.Category,
		&is.Title,
		&is.Body,
		&is.State,
		&parent,
		&is.Version,
		&blockedByRaw,
		&created,
		&lastUpdated,
		&closed,
	); err != nil {
		return Issue{}, err
	}

	if parent.Valid {
		p := parent.String
		is.ParentID = &p
	}
	if blockedByRaw.Valid && strings.TrimSpace(blockedByRaw.String) != "" {
		if err := json.Unmarshal([]byte(blockedByRaw.String), &is.BlockedBy); err != nil {
			return Issue{}, fmt.Errorf("parse blocked_by for %s: %w", is.ID, err)
		}
	} else {
		is.BlockedBy = []string{}
	}

	if prefix, ok := projectPrefixFromIssueID(is.ID); ok {
		is.ProjectPrefix = prefix
	}

	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return Issue{}, err
	}
	lastUpdatedAt, err := parseSQLiteTime(lastUpdated)
	if err != nil {
		return Issue{}, err
	}
	is.CreatedAt = createdAt
	is.LastUpdatedAt = lastUpdatedAt

	if closed.Valid {
		closedAt, err := parseSQLiteTime(closed.String)
		if err != nil {
			return Issue{}, err
		}
		is.ClosedAt = &closedAt
	}

	return is, nil
}

func parseSQLiteTime(value string) (time.Time, error) {
	t, err := time.ParseInLocation(sqliteTimeLayout, value, time.UTC)
	if err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func isUniqueViolation(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}

func projectPrefixFromIssueID(id string) (string, bool) {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(id)), "-", 2)
	if len(parts) != 2 {
		return "", false
	}
	if !projectPrefixRe.MatchString(parts[0]) {
		return "", false
	}
	return parts[0], true
}

func normalizeBlockedByTx(ctx context.Context, tx *sql.Tx, issueID, projectPrefix string, blockedBy []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(blockedBy))
	for _, raw := range blockedBy {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		if !issueIDRe.MatchString(id) {
			return nil, fmt.Errorf("%w: invalid blocked_by issue id %q", ErrInvalidInput, id)
		}
		if id == issueID {
			return nil, fmt.Errorf("%w: blocked_by cannot include self", ErrInvalidInput)
		}
		if seen[id] {
			continue
		}
		seen[id] = true

		prefix, ok := projectPrefixFromIssueID(id)
		if !ok || prefix != projectPrefix {
			return nil, fmt.Errorf("%w: blocked_by issue must be in same project: %q", ErrInvalidInput, id)
		}
		dep, err := getIssueByIDTx(ctx, tx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w: blocked_by issue %q not found", ErrNotFound, id)
			}
			return nil, err
		}
		if dep.ProjectPrefix != projectPrefix {
			return nil, fmt.Errorf("%w: blocked_by issue must be in same project: %q", ErrInvalidInput, id)
		}
		out = append(out, id)
	}
	return out, nil
}

func unresolvedBlockedByTx(ctx context.Context, tx *sql.Tx, issue *Issue) ([]string, error) {
	unresolved := make([]string, 0)
	for _, depID := range issue.BlockedBy {
		dep, err := getIssueByIDTx(ctx, tx, depID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w: blocked_by issue %q not found", ErrNotFound, depID)
			}
			return nil, err
		}
		if dep.State != StateDone {
			unresolved = append(unresolved, dep.ID)
		}
	}
	return unresolved, nil
}
