# Issue Tracker Manual (for Client Agents)

## 1) Project Setup

1. Build the CLI binary:
   ```bash
   go build -o it ./cmd/it
   ```
2. Put `it` on your `PATH` (or call it with full path).
3. In each client project, create an `itconfig` file at the project root.

## 2) `itconfig` File

Create a file named `itconfig` in the project directory:

```ini
db=.it/issues.db
project=cat
```

Rules:
- `db`: SQLite file path. Relative paths are resolved from the directory that contains `itconfig`.
- `project`: exactly 3 lowercase alphanumeric characters (example: `cat`, `a1b`).

With this file present, agents do not need to pass `--db` or `--project` repeatedly.

## 3) Issue Model

- ID format: `<project>-<number>` (example: `cat-123`)
- Categories:
  - `project`
  - `workstream`
  - `task`
- Parent rules:
  - `project` has no parent
  - `workstream` parent must be a `project`
  - `task` parent must be a `workstream`
- States:
  - `todo`
  - `in_progress`
  - `blocked`
  - `done`
  - `canceled`
- `blocked_by`:
  - List of dependency issue IDs.
  - Issue cannot move to `in_progress` until all dependencies are `done`.

## 4) Commands

### Create issues

```bash
it create -c p --title "Catalog Platform"
it create -c w --title "Backend" -p cat-1
it create -c t --title "Build API" -p cat-2 --blocked-by cat-7,cat-8
```

Flags:
- `-c`: category shortcut
  - `p` = project
  - `w` = workstream
  - `t` = task
- `--title`: required
- `--body`: optional description
- `-p`: optional parent issue id (required for `w` and `t`, not allowed for `p`)
- `--blocked-by`: optional comma-separated dependency issue IDs
- `--json`: machine-readable output
- `--project`: optional override of `itconfig` project

### Show one issue

```bash
it show --id cat-3
```

### List issues

```bash
it list
it list --state todo
it list --project cat
```

### Change state

```bash
it state --id cat-3 --to in_progress
it state --id cat-3 --to blocked --blocked-reason "Waiting on API schema"
it state --id cat-3 --to done
```

Optional:
- `--expected-version N` for optimistic concurrency.

### Change parent

```bash
it parent --id cat-3 -p cat-2
it parent --id cat-3 --clear
```

Note: `--clear` may fail for categories that require a parent (`task`, `workstream`).

### Show tree

```bash
it tree --project cat
```

### Manage blocked_by dependencies

```bash
it blocked-by --id cat-3 --set cat-7,cat-8
it blocked-by --id cat-3 --clear
```

## 5) Agent Usage Tips

- Prefer `--json` for agent-to-agent automation.
- Use `--expected-version` on writes (`state`, `parent`) to avoid stale updates.

## 6) Quick Start Example

```bash
it create -c p --title "Catalog Platform"
it create -c w --title "Backend" -p cat-1
it create -c t --title "Build API" -p cat-2
it state --id cat-3 --to in_progress
it list
it tree --project cat
```
