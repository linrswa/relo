---
name: relo-cli-guide
description: Use the relo CLI correctly to initialize projects, create and update tasks, manage acceptance criteria and notes, build dependency DAGs, inspect ready/blocked work, perform legal task state transitions, validate or render project state, consume JSON output, and manage milestone checkpoints. Use whenever the user asks to operate, explain, script, troubleshoot, or integrate the relo command-line interface. This skill teaches CLI usage only; do not launch subagents or impose a delivery-orchestration workflow merely because it is active.
compatibility: Requires access to a relo executable and, except for init, a repository initialized with .relo metadata.
---

# Relo CLI guide

Use `relo` as a task and checkpoint runtime. Apply the public command contract accurately without inventing orchestration policy.

## Operating rules

1. Locate the executable from the user or environment and use the same path consistently. If uncertain, run `command -v relo` and `relo --help`.
2. Run the relevant `<command> --help` before guessing syntax or flags. Read [references/command-reference.md](references/command-reference.md) when exact syntax or command coverage matters.
3. Run commands from the project directory or one of its descendants. Relo discovers the nearest parent containing the regular file `.relo/relo.db`; an empty `.relo/` directory is not an initialized project.
4. Never read, edit, query, copy as an API, or otherwise depend on `.relo/relo.db`. It is private SQLite state; use CLI commands only.
5. Use canonical IDs such as `TASK-001`, `MILESTONE-001`, `AC-001`, `NOTE-001`, and `REC-001`. Task titles are supported only for exact, read-only lookup with `task get --title`; use IDs for mutations.
6. Treat command failures as authoritative. Do not work around rejected state transitions by editing persistence.
7. Inspect current state and command help before mutating. Never use a mutation as a probe to discover whether it is legal; a rejected mutation is an execution error, not a discovery strategy. If an unexpected rejection occurs, preserve it in the report, recover through supported commands, and do not present the rejected command as part of the recommended sequence.
8. This skill does not require or authorize launching subagents. Only do so when the user or another applicable workflow explicitly requests it.

## Understand the model

- A **project** records one goal, a PRD path, and the PRD content hash.
- A **task definition** contains an objective, at least one acceptance criterion, optional notes, a non-negative priority, and dependency edges.
- Lower priority numbers sort first. Priority affects display order, not dependency legality.
- Stored task states are `pending`, `running`, `passed`, and `failed`.
- `ready` and `blocked` are derived states for pending tasks. A task is ready only when all dependencies are passed.
- Starting a task opens an attempt. Passing, failing, or stopping it closes that attempt.
- A **milestone** is an optional, non-executing checkpoint over anchor tasks and their transitive dependencies. It never blocks `task ready` or `task start`.

## Initialize and inspect a project

Before initializing, confirm the PRD exists and the requested goal is clear:

```bash
relo init --prd docs/prd.md --goal "Deliver the project goal"
```

Do not retry initialization with a force option; none exists. For an initialized project:

```bash
relo project show
relo status
relo graph
relo validate
```

Update project metadata deliberately:

```bash
relo project update --goal "Revised goal"
relo project update --prd docs/new-prd.md
```

When the contents of the existing PRD change and have been reviewed, acknowledge the new hash without changing tasks automatically:

```bash
relo project refresh-prd
```

## Create useful tasks

A task requires a title, an objective (inline or from a file), and at least one acceptance criterion:

```bash
relo task create \
  --title "Add storage layer" \
  --objective "Persist catalog entries in SQLite" \
  --accept "Create and retrieve entries" \
  --accept "Storage tests pass" \
  --priority 20
```

Capture observable completion conditions in `--accept`; use notes for implementation context:

```bash
relo task note add TASK-001 --text "Reuse the existing database helper"
relo task acceptance add TASK-001 --text "Invalid records are rejected"
```

Inspect tasks with:

```bash
relo task get TASK-001
relo task get --title "Add storage layer"
relo task list
relo task list --status pending
```

Exact-title lookup fails when titles are duplicated. Mutations always use the ID.

## Build and revise dependencies

The first positional ID is the target task; every following ID is something it must wait for:

```bash
relo task dependency add TASK-003 TASK-001 TASK-002 \
  --reason "Both foundations are required"
```

For distinct edge reasons, use one mapping per dependency instead of relying on argument order:

```bash
relo task dependency add TASK-003 TASK-001 TASK-002 \
  --reason-for 'TASK-001=Provides the schema' \
  --reason-for 'TASK-002=Provides the service API'
```

`--reason` and `--reason-for` are mutually exclusive. Every dependency needs a non-empty reason. Dependency additions are atomic and reject self-dependencies, duplicates, missing tasks, and cycles.

Revise edges with an audit reason:

```bash
relo task dependency reason TASK-003 TASK-001 --text "Updated rationale"
relo task dependency remove TASK-003 TASK-002 --reason "The API is no longer used"
```

After graph changes, run:

```bash
relo validate
relo graph
relo task ready --details
```

## Follow legal task transitions

```text
pending --start--> running --pass--> passed
                         \--fail--> failed --retry--> pending
                         \--stop--> pending
passed  --reopen-----------------> pending
```

Use the current ready frontier rather than assuming a pending task can start:

```bash
relo task ready --details
relo task start TASK-001
```

Close running attempts explicitly:

```bash
relo task pass TASK-001 --summary "Implemented storage and passed package tests"
relo task fail TASK-001 --reason "Required upstream API is missing"
relo task stop TASK-001 --reason "Definition must be revised"
```

Then recover as appropriate:

```bash
relo task retry TASK-001
relo task reopen TASK-002 --reason "Acceptance behavior must change"
```

Important constraints:

- `start` accepts only ready pending tasks.
- `pass`, `fail`, and `stop` accept only running tasks with one open attempt.
- `fail`, `stop`, and `reopen` require a non-empty reason. A pass summary is supported and should describe verified work.
- `retry` accepts only failed tasks.
- `reopen` accepts only passed tasks and is rejected while downstream tasks are running or passed.
- Definitions, acceptance criteria, notes, dependencies, and priority can be changed only while the task is pending or failed.
- Stop a running task before changing its definition. If passed upstream work must change, inspect the graph first, stop any running descendants, then reopen passed descendants from leaves toward the target before reopening it. After the update, rerun affected tasks in dependency order.
- Multi-task `start` and `stop` are atomic: if one requested transition is invalid, none are changed.

Update pending or failed definitions with:

```bash
relo task update TASK-001 --title "Revised title"
relo task update TASK-001 --objective-file docs/task-001-objective.md
relo task update TASK-001 --priority 10 --reason "Needed before integration"
```

Changing priority requires `--reason`.

## Use milestones as checkpoints

Create a planned milestone with at least one task anchor:

```bash
relo milestone create \
  --title "Storage foundation" \
  --reason "The persistence boundary is ready to inspect" \
  --anchor TASK-002 \
  --recommend "Run storage integration tests"
```

Inspect and maintain planned milestones:

```bash
relo milestone get MILESTONE-001
relo milestone list --status planned
relo milestone ready --details
relo milestone anchor add MILESTONE-001 TASK-003
relo milestone recommendation add MILESTONE-001 --text "Review migration behavior"
```

A planned milestone becomes ready when all anchors pass. Its scope is the anchors plus their transitive dependency closure. Marking revalidates that complete scope and stores an immutable snapshot:

```bash
relo milestone mark MILESTONE-001 \
  --summary "Checkpoint checks completed" \
  --reference "optional-external-reference"
```

Whenever explaining milestone behavior, distinguish all three facts explicitly:

1. Readiness is derived from the anchor tasks being passed.
2. Marking validates the complete anchor-plus-transitive-dependency scope.
3. Milestones are non-gating: they do not change `task ready`, dependency legality, or `task start`.

Marked milestones cannot be updated or deleted. Later task changes do not rewrite their snapshots. If the user wants to revise a marked checkpoint, preserve it and create a new planned milestone as a successor rather than probing immutable mutation commands.

## Choose human or JSON output

Use human output for interactive work. JSON is available only for:

```bash
relo project show --json
relo graph --format json
relo status --json
relo task list [--status pending|running|passed|failed] --json
relo task get TASK-001 --json
relo task ready --json
relo milestone get MILESTONE-001 --json
relo milestone list --json
relo milestone ready --json
```

JSON uses a versioned envelope:

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": true,
  "data": {}
}
```

Recognized JSON-mode command failures return `ok: false` on stdout with a non-zero exit code. Root-level parsing failures, such as an unknown command, can occur before JSON mode is established and may use stderr. Do not assume unsupported commands accept `--json`.

## Verify the resulting state

After meaningful mutations, select the narrowest relevant checks and finish with:

```bash
relo validate
relo graph
relo status
```

Interpret warnings rather than hiding them. Successful completion means the CLI state matches the user's requested task graph and lifecycle—not that an external implementation has been verified unless the user separately provided that evidence.

## Current UX contracts

Use `relo --help` for root discovery and `relo version` for a plain build version. Project lookup walks upward to `.relo/relo.db`. `task create` requires exactly one of `--objective` and `--objective-file`; `task update` accepts either but rejects both. Dependency commands are `target prerequisite...`: later IDs are prerequisites of the first. Stop running tasks before editing and reopen passed tasks before editing. `validate` prints `OK` to stdout whenever there are no errors, even if warnings are on stderr. Only listed JSON commands use `relo.output/v1`: project show has `goal`, `prd_path`, `prd_hash`; task list supplies summaries; task get retains nested project `goal`/`prd_path` and adds runtime fields. `last_failure_reason`, `last_completion_summary`, and `current_attempt` are nullable; the running current attempt also has nullable completion, summary, and reason. Milestone readiness requires passed anchors; marking validates anchor transitive scope and never changes task dependency or ready/start legality.
