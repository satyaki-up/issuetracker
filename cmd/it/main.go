package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/satyaki-up/issuetracker/internal/config"
	"github.com/satyaki-up/issuetracker/internal/db"
	"github.com/satyaki-up/issuetracker/internal/issues"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx := context.Background()

	defaultDBPath := db.DefaultPath()
	defaultProject := ""
	cfgPath := ""
	cwd, err := os.Getwd()
	if err == nil {
		cfg, err := config.Discover(cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: load itconfig: %v\n", err)
			return 1
		}
		if cfg != nil {
			cfgPath = cfg.Path
			if cfg.DBPath != "" {
				defaultDBPath = cfg.DBPath
			}
			defaultProject = cfg.Project
		}
	}

	root := flag.NewFlagSet("it", flag.ContinueOnError)
	root.SetOutput(os.Stderr)
	dbPath := root.String("db", "", "SQLite database path")
	if err := root.Parse(os.Args[1:]); err != nil {
		return 1
	}
	args := root.Args()
	if len(args) == 0 {
		printUsage(cfgPath, defaultProject, defaultDBPath)
		return 1
	}
	if strings.TrimSpace(*dbPath) == "" {
		*dbPath = defaultDBPath
	}

	database, err := db.Open(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		return 1
	}
	defer database.Close()

	svc := issues.NewService(database)

	switch args[0] {
	case "create":
		return handleCreate(ctx, svc, args[1:], defaultProject)
	case "show":
		return handleShow(ctx, svc, args[1:])
	case "list":
		return handleList(ctx, svc, args[1:], defaultProject)
	case "state":
		return handleState(ctx, svc, args[1:])
	case "parent":
		return handleParent(ctx, svc, args[1:])
	case "blocked-by":
		return handleBlockedBy(ctx, svc, args[1:])
	case "tree":
		return handleTree(ctx, svc, args[1:], defaultProject)
	case "help", "-h", "--help":
		printUsage(cfgPath, defaultProject, *dbPath)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n", args[0])
		printUsage(cfgPath, defaultProject, *dbPath)
		return 1
	}
}

func handleCreate(ctx context.Context, svc *issues.Service, args []string, defaultProject string) int {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "3-char project prefix (lowercase alphanumeric)")
	categoryShort := fs.String("c", "", "short category: t|w|p")
	title := fs.String("title", "", "issue title")
	body := fs.String("body", "", "issue description")
	parent := fs.String("p", "", "parent issue id")
	blockedBy := fs.String("blocked-by", "", "comma-separated dependency issue ids")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if strings.TrimSpace(*project) == "" {
		*project = defaultProject
	}
	categoryValue, err := parseCategoryArg(*categoryShort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	var parentID *string
	if strings.TrimSpace(*parent) != "" {
		v := strings.TrimSpace(*parent)
		parentID = &v
	}
	issue, err := svc.CreateIssue(ctx, *project, categoryValue, *title, *body, parentID, parseCSV(*blockedBy))
	if err != nil {
		return renderError(err)
	}

	if *jsonOut {
		printJSON(issue)
		return 0
	}
	fmt.Printf("created %s (v%d)\n", issue.ID, issue.Version)
	return 0
}

func handleShow(ctx context.Context, svc *issues.Service, args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "issue id")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	issue, err := svc.GetIssue(ctx, *id)
	if err != nil {
		return renderError(err)
	}
	if *jsonOut {
		printJSON(issue)
		return 0
	}
	printIssue(*issue)
	return 0
}

func handleList(ctx context.Context, svc *issues.Service, args []string, defaultProject string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "project prefix")
	stateArg := fs.String("state", "", "state filter")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if strings.TrimSpace(*project) == "" {
		*project = defaultProject
	}

	var state *issues.State
	if strings.TrimSpace(*stateArg) != "" {
		s := issues.State(strings.TrimSpace(*stateArg))
		state = &s
	}

	list, err := svc.ListIssues(ctx, *project, state)
	if err != nil {
		return renderError(err)
	}
	if *jsonOut {
		printJSON(list)
		return 0
	}
	for _, is := range list {
		fmt.Printf("%s\t%s\t%s\tv%d\t%s\n", is.ID, is.Category, is.State, is.Version, is.Title)
	}
	return 0
}

func handleState(ctx context.Context, svc *issues.Service, args []string) int {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "issue id")
	to := fs.String("to", "", "target state")
	expectedVersion := fs.Int64("expected-version", -1, "optimistic concurrency check")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	var expectedPtr *int64
	if *expectedVersion >= 0 {
		ev := *expectedVersion
		expectedPtr = &ev
	}

	updated, err := svc.TransitionState(ctx, *id, issues.State(strings.TrimSpace(*to)), expectedPtr)
	if err != nil {
		return renderError(err)
	}
	if *jsonOut {
		printJSON(updated)
		return 0
	}
	fmt.Printf("updated %s to %s (v%d)\n", updated.ID, updated.State, updated.Version)
	return 0
}

func handleParent(ctx context.Context, svc *issues.Service, args []string) int {
	fs := flag.NewFlagSet("parent", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "issue id")
	parent := fs.String("p", "", "parent issue id")
	clear := fs.Bool("clear", false, "remove parent")
	expectedVersion := fs.Int64("expected-version", -1, "optimistic concurrency check")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *clear && strings.TrimSpace(*parent) != "" {
		fmt.Fprintln(os.Stderr, "error: use either -p or --clear")
		return 2
	}

	var parentID *string
	if !*clear && strings.TrimSpace(*parent) != "" {
		v := strings.TrimSpace(*parent)
		parentID = &v
	}
	var expectedPtr *int64
	if *expectedVersion >= 0 {
		ev := *expectedVersion
		expectedPtr = &ev
	}

	updated, err := svc.SetParent(ctx, *id, parentID, expectedPtr)
	if err != nil {
		return renderError(err)
	}
	if *jsonOut {
		printJSON(updated)
		return 0
	}
	if updated.ParentID == nil {
		fmt.Printf("cleared parent for %s (v%d)\n", updated.ID, updated.Version)
		return 0
	}
	fmt.Printf("set parent of %s to %s (v%d)\n", updated.ID, *updated.ParentID, updated.Version)
	return 0
}

func handleTree(ctx context.Context, svc *issues.Service, args []string, defaultProject string) int {
	fs := flag.NewFlagSet("tree", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "project prefix")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if strings.TrimSpace(*project) == "" {
		*project = defaultProject
	}

	tree, err := svc.Tree(ctx, *project)
	if err != nil {
		return renderError(err)
	}
	if *jsonOut {
		printJSON(tree)
		return 0
	}
	for _, node := range tree {
		printTree(node, 0)
	}
	return 0
}

func handleBlockedBy(ctx context.Context, svc *issues.Service, args []string) int {
	fs := flag.NewFlagSet("blocked-by", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "issue id")
	set := fs.String("set", "", "comma-separated dependency issue ids")
	clear := fs.Bool("clear", false, "remove all dependencies")
	expectedVersion := fs.Int64("expected-version", -1, "optimistic concurrency check")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *clear && strings.TrimSpace(*set) != "" {
		fmt.Fprintln(os.Stderr, "error: use either --set or --clear")
		return 2
	}

	blockedBy := []string{}
	if !*clear {
		blockedBy = parseCSV(*set)
	}

	var expectedPtr *int64
	if *expectedVersion >= 0 {
		ev := *expectedVersion
		expectedPtr = &ev
	}

	updated, err := svc.SetBlockedBy(ctx, *id, blockedBy, expectedPtr)
	if err != nil {
		return renderError(err)
	}
	if *jsonOut {
		printJSON(updated)
		return 0
	}
	fmt.Printf("updated blocked_by for %s (v%d)\n", updated.ID, updated.Version)
	return 0
}

func renderError(err error) int {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	switch {
	case errors.Is(err, issues.ErrInvalidInput), errors.Is(err, issues.ErrInvalidStateTransition):
		return 2
	case errors.Is(err, issues.ErrNotFound):
		return 3
	case errors.Is(err, issues.ErrConflict), errors.Is(err, issues.ErrDepthExceeded), errors.Is(err, issues.ErrCycleDetected):
		return 4
	default:
		return 1
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func printIssue(is issues.Issue) {
	fmt.Printf("id: %s\n", is.ID)
	fmt.Printf("project: %s\n", is.ProjectPrefix)
	fmt.Printf("category: %s\n", is.Category)
	fmt.Printf("state: %s\n", is.State)
	fmt.Printf("version: %d\n", is.Version)
	if is.ParentID != nil {
		fmt.Printf("parent: %s\n", *is.ParentID)
	}
	fmt.Printf("title: %s\n", is.Title)
	if is.Body != "" {
		fmt.Printf("body: %s\n", is.Body)
	}
	if len(is.BlockedBy) > 0 {
		fmt.Printf("blocked_by: %s\n", strings.Join(is.BlockedBy, ","))
	}
	fmt.Printf("created_at: %s\n", is.CreatedAt.Format(time.RFC3339))
	fmt.Printf("last_updated_at: %s\n", is.LastUpdatedAt.Format(time.RFC3339))
	if is.ClosedAt != nil {
		fmt.Printf("closed_at: %s\n", is.ClosedAt.Format(time.RFC3339))
	}
}

func printTree(node issues.TreeNode, level int) {
	indent := strings.Repeat("  ", level)
	fmt.Printf("%s- %s (%s) [%s] v%d %s\n", indent, node.Issue.ID, node.Issue.Category, node.Issue.State, node.Issue.Version, node.Issue.Title)
	for _, child := range node.Children {
		printTree(child, level+1)
	}
}

func printUsage(configPath, defaultProject, defaultDB string) {
	fmt.Fprint(os.Stderr, `Usage:
  it [--db PATH] create --project cat [-c t|w|p] --title "..." [--body "..."] [-p cat-1] [--blocked-by cat-2,cat-3] [--json]
  it [--db PATH] show --id cat-1 [--json]
  it [--db PATH] list [--project cat] [--state todo] [--json]
  it [--db PATH] state --id cat-1 --to in_progress [--expected-version N] [--json]
  it [--db PATH] parent --id cat-2 [-p cat-1|--clear] [--expected-version N] [--json]
  it [--db PATH] blocked-by --id cat-2 [--set cat-1,cat-3|--clear] [--expected-version N] [--json]
  it [--db PATH] tree --project cat [--json]
`)
	if configPath != "" {
		fmt.Fprintf(os.Stderr, "\nDiscovered itconfig: %s\n", configPath)
	}
	if defaultProject != "" {
		fmt.Fprintf(os.Stderr, "Default project from itconfig: %s\n", defaultProject)
	}
	if defaultDB != "" {
		fmt.Fprintf(os.Stderr, "Default DB path: %s\n", defaultDB)
	}
	fmt.Fprint(os.Stderr, `
itconfig format:
  db=.it/issues.db
  project=cat
`)
}

func parseCategoryArg(shortValue string) (issues.Category, error) {
	raw := strings.TrimSpace(strings.ToLower(shortValue))
	if raw == "" {
		return issues.CategoryTask, nil
	}

	switch raw {
	case "task", "t":
		return issues.CategoryTask, nil
	case "workstream", "w":
		return issues.CategoryWorkstream, nil
	case "project", "p":
		return issues.CategoryProject, nil
	default:
		return "", fmt.Errorf("invalid category %q (use task|workstream|project or t|w|p)", raw)
	}
}

func parseCSV(value string) []string {
	raw := strings.Split(strings.TrimSpace(value), ",")
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}
