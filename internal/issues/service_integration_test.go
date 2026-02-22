package issues_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/satyaki-up/issuetracker/internal/db"
	"github.com/satyaki-up/issuetracker/internal/issues"
)

func newTestService(t *testing.T) *issues.Service {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "issues.db")
	database, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return issues.NewService(database)
}

func TestHierarchyIntegration(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	project, err := svc.CreateIssue(ctx, "cat", issues.CategoryProject, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create project issue: %v", err)
	}

	workstream, err := svc.CreateIssue(ctx, "cat", issues.CategoryWorkstream, "Backend", "", &project.ID, nil)
	if err != nil {
		t.Fatalf("create workstream issue: %v", err)
	}

	task, err := svc.CreateIssue(ctx, "cat", issues.CategoryTask, "Build API", "", &workstream.ID, nil)
	if err != nil {
		t.Fatalf("create task issue: %v", err)
	}

	_, err = svc.CreateIssue(ctx, "cat", issues.CategoryTask, "Invalid root task", "", nil, nil)
	if err == nil {
		t.Fatal("expected error when creating task without workstream parent")
	}
	if !errors.Is(err, issues.ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got %v", err)
	}

	_, err = svc.CreateIssue(ctx, "cat", issues.CategoryWorkstream, "Invalid parent", "", &task.ID, nil)
	if err == nil {
		t.Fatal("expected error when workstream parent is task")
	}
	if !errors.Is(err, issues.ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got %v", err)
	}

	tree, err := svc.Tree(ctx, "cat")
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("expected one root, got %d", len(tree))
	}
	if len(tree[0].Children) != 1 || len(tree[0].Children[0].Children) != 1 {
		t.Fatalf("expected project->workstream->task structure, got %+v", tree)
	}
}

func TestLifecycleWithParentsAndStatesIntegration(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	rootProject, err := svc.CreateIssue(ctx, "cat", issues.CategoryProject, "Catalog Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create project issue: %v", err)
	}

	wsBackend, err := svc.CreateIssue(ctx, "cat", issues.CategoryWorkstream, "Backend", "", &rootProject.ID, nil)
	if err != nil {
		t.Fatalf("create backend workstream: %v", err)
	}
	wsFrontend, err := svc.CreateIssue(ctx, "cat", issues.CategoryWorkstream, "Frontend", "", &rootProject.ID, nil)
	if err != nil {
		t.Fatalf("create frontend workstream: %v", err)
	}

	taskAPI, err := svc.CreateIssue(ctx, "cat", issues.CategoryTask, "Build API", "", &wsBackend.ID, nil)
	if err != nil {
		t.Fatalf("create task API: %v", err)
	}
	taskUI, err := svc.CreateIssue(ctx, "cat", issues.CategoryTask, "Build UI", "", &wsFrontend.ID, nil)
	if err != nil {
		t.Fatalf("create task UI: %v", err)
	}
	taskAuth, err := svc.CreateIssue(ctx, "cat", issues.CategoryTask, "Add auth", "", &wsBackend.ID, nil)
	if err != nil {
		t.Fatalf("create task auth: %v", err)
	}

	if _, err := svc.TransitionState(ctx, taskAPI.ID, issues.StateInProgress, nil); err != nil {
		t.Fatalf("taskAPI in_progress: %v", err)
	}
	if _, err := svc.TransitionState(ctx, taskAPI.ID, issues.StateDone, nil); err != nil {
		t.Fatalf("taskAPI done: %v", err)
	}
	if _, err := svc.TransitionState(ctx, taskUI.ID, issues.StateBlocked, nil); err != nil {
		t.Fatalf("taskUI blocked: %v", err)
	}
	if _, err := svc.TransitionState(ctx, taskAuth.ID, issues.StateInProgress, nil); err != nil {
		t.Fatalf("taskAuth in_progress: %v", err)
	}

	// Re-parenting moves the task from one workstream to another.
	moved, err := svc.SetParent(ctx, taskAuth.ID, &wsFrontend.ID, nil)
	if err != nil {
		t.Fatalf("move taskAuth parent: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != wsFrontend.ID {
		t.Fatalf("expected taskAuth parent %s, got %+v", wsFrontend.ID, moved.ParentID)
	}

	// Removing parent is invalid for task category in this strict hierarchy.
	_, err = svc.SetParent(ctx, taskAuth.ID, nil, nil)
	if err == nil {
		t.Fatal("expected error when removing parent from task")
	}
	if !errors.Is(err, issues.ErrInvalidInput) {
		t.Fatalf("expected invalid input on parent removal, got %v", err)
	}

	allIssues, err := svc.ListIssues(ctx, "cat", nil)
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(allIssues) != 6 {
		t.Fatalf("expected 6 issues total, got %d", len(allIssues))
	}

	tree, err := svc.Tree(ctx, "cat")
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("expected one project root, got %d", len(tree))
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("expected two workstreams under project, got %d", len(tree[0].Children))
	}
}

func TestOptimisticConcurrencyIntegration(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	project, err := svc.CreateIssue(ctx, "cat", issues.CategoryProject, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	expected := project.Version
	updated, err := svc.TransitionState(ctx, project.ID, issues.StateInProgress, &expected)
	if err != nil {
		t.Fatalf("first transition: %v", err)
	}
	if updated.Version <= expected {
		t.Fatalf("expected version to increase, old=%d new=%d", expected, updated.Version)
	}

	_, err = svc.TransitionState(ctx, project.ID, issues.StateBlocked, &expected)
	if err == nil {
		t.Fatal("expected conflict on stale expected version")
	}
	if !errors.Is(err, issues.ErrConflict) {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestBlockedByDependenciesIntegration(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	root, err := svc.CreateIssue(ctx, "cat", issues.CategoryProject, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	ws, err := svc.CreateIssue(ctx, "cat", issues.CategoryWorkstream, "Backend", "", &root.ID, nil)
	if err != nil {
		t.Fatalf("create workstream: %v", err)
	}
	dep, err := svc.CreateIssue(ctx, "cat", issues.CategoryTask, "Foundation", "", &ws.ID, nil)
	if err != nil {
		t.Fatalf("create dep: %v", err)
	}
	target, err := svc.CreateIssue(ctx, "cat", issues.CategoryTask, "Feature", "", &ws.ID, []string{dep.ID})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	if _, err := svc.TransitionState(ctx, target.ID, issues.StateInProgress, nil); err == nil {
		t.Fatal("expected in_progress to fail while dependency is not done")
	}

	if _, err := svc.TransitionState(ctx, dep.ID, issues.StateInProgress, nil); err != nil {
		t.Fatalf("dep in_progress: %v", err)
	}
	if _, err := svc.TransitionState(ctx, dep.ID, issues.StateDone, nil); err != nil {
		t.Fatalf("dep done: %v", err)
	}

	if _, err := svc.TransitionState(ctx, target.ID, issues.StateInProgress, nil); err != nil {
		t.Fatalf("target in_progress after dep done: %v", err)
	}

	updated, err := svc.SetBlockedBy(ctx, target.ID, nil, nil)
	if err != nil {
		t.Fatalf("clear blocked_by: %v", err)
	}
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected empty blocked_by, got %+v", updated.BlockedBy)
	}
}
