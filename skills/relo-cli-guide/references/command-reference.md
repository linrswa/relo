# Relo command reference

Use this reference for the implemented command surface. Run the relevant command with `--help` when the installed executable may differ from this repository.

## Project and global views

```text
relo init --prd PATH --goal TEXT
relo project show [--json]
relo version
relo project update [--goal TEXT] [--prd PATH]
relo project refresh-prd
relo project remove --force
relo validate
relo graph [--format tree|json] [--include-milestones]
relo status [--json]
```

`init` creates `.relo/relo.db`. Other commands discover the nearest initialized parent directory. `project remove` is irreversible, requires `--force`, and must run from the project root. It removes only the relo-managed database and sidecars; the PRD, source files, and unknown `.relo` contents are preserved.

## Task definitions

```text
relo task create --title TEXT [--objective TEXT] [--objective-file PATH]
  --accept TEXT [--accept TEXT]... [--priority N]
relo task get TASK-ID [--json]
relo task get --title EXACT-TITLE [--json]
relo task list [--status pending|running|passed|failed] [--json]
relo task update TASK-ID [--title TEXT]
  [--objective TEXT | --objective-file PATH]
  [--priority N --reason TEXT]
relo task delete TASK-ID
```

Priority defaults to `100` and must be non-negative. Lower values sort first. Creating a task requires at least one acceptance criterion and exactly one of `--objective` or `--objective-file`; update accepts either source but rejects both. Update/delete requires a pending or failed task.

## Acceptance criteria and notes

```text
relo task acceptance add TASK-ID --text TEXT
relo task acceptance update TASK-ID AC-ID --text TEXT
relo task acceptance remove TASK-ID AC-ID

relo task note add TASK-ID --text TEXT
relo task note update TASK-ID NOTE-ID --text TEXT
relo task note remove TASK-ID NOTE-ID
```

A task must retain at least one acceptance criterion.

## Dependencies

```text
relo task dependency add TASK-ID DEP-ID... --reason TEXT
relo task dependency add TASK-ID DEP-ID...
  --reason-for TASK-ID=TEXT [--reason-for TASK-ID=TEXT ...]
relo task dependency remove TASK-ID DEP-ID... --reason TEXT
relo task dependency reason TASK-ID DEP-ID --text TEXT
```

The target task is first and waits for every following dependency. Batch mutations are atomic. Add rejects cycles. `--reason` and `--reason-for` cannot be combined.

## Runtime

```text
relo task ready [--details] [--json]
relo task start TASK-ID...
relo task stop TASK-ID... --reason TEXT
relo task pass TASK-ID [--summary TEXT]
relo task fail TASK-ID --reason TEXT
relo task retry TASK-ID
relo task reopen TASK-ID --reason TEXT
```

Stored states and valid transitions:

```text
pending -> running
running -> pending | passed | failed
failed  -> pending
passed  -> pending
```

`ready` and `blocked` are derived from pending state and dependencies. Multi-task start/stop is atomic.

## Milestones

```text
relo milestone create --title TEXT --reason TEXT
  --anchor TASK-ID [--anchor TASK-ID]...
  [--recommend TEXT]...
relo milestone get MILESTONE-ID [--json]
relo milestone list [--status planned|marked] [--json]
relo milestone ready [--details] [--json]
relo milestone update MILESTONE-ID [--title TEXT] [--reason TEXT]
relo milestone delete MILESTONE-ID
relo milestone anchor add MILESTONE-ID TASK-ID...
relo milestone anchor remove MILESTONE-ID TASK-ID...
relo milestone recommendation add MILESTONE-ID --text TEXT
relo milestone recommendation update MILESTONE-ID REC-ID --text TEXT
relo milestone recommendation remove MILESTONE-ID REC-ID
relo milestone mark MILESTONE-ID --summary TEXT [--reference TEXT]
```

Milestones are `planned` or `marked`. A planned milestone is ready to mark when all anchors pass. It never changes task readiness. Marking snapshots the passed anchor/dependency scope and makes the milestone immutable. `graph --include-milestones` appends a non-gating checkpoint overlay to tree output. It is rejected with `--format json`, which remains the task-DAG contract.

## IDs and output

Mutation commands require canonical IDs such as:

```text
TASK-001
MILESTONE-001
AC-001
NOTE-001
REC-001
```

Supported structured output uses the `relo.output/v1` JSON envelope. Only commands shown with `--json` (or graph's `--format json`) accept JSON; mutations remain human-output only.

## UX and JSON contracts

`relo --help` is the root discovery entry point and project discovery walks upward to `.relo/relo.db`. `relo version` prints a plain version string. Dependency syntax is target then prerequisite IDs. `project show --json` returns `goal`, `prd_path`, and `prd_hash`; `task list --json` is summary-only; `task get --json` retains nested project `goal`/`prd_path` and adds runtime detail. Its `last_failure_reason`, `last_completion_summary`, and `current_attempt` are nullable; a running `current_attempt` has `attempt_number`, `status`, `started_at`, and nullable `completed_at`, `summary`, and `reason`. `validate` prints `OK` on stdout whenever no errors exist, independently of warnings on stderr. Milestones are ready when anchors passed, marking checks anchor transitive scope, and milestones never gate tasks.
