# Relo command reference

This file records the public command contract implemented by the current repository. When operating another installed version, run `relo --help` and the relevant command's `--help`; the installed executable is authoritative.

## Contents

- [Executable, version, and upgrade](#executable-version-and-upgrade)
- [Project discovery and metadata](#project-discovery-and-metadata)
- [Task definitions](#task-definitions)
- [Acceptance criteria and notes](#acceptance-criteria-and-notes)
- [Dependencies](#dependencies)
- [Task runtime](#task-runtime)
- [Milestones](#milestones)
- [Graph, status, and validation](#graph-status-and-validation)
- [JSON and process errors](#json-and-process-errors)

## Executable, version, and upgrade

```text
relo --help
relo COMMAND --help
relo -v
relo --version
relo upgrade --check
relo upgrade
relo upgrade --version VERSION
```

`-v` and `--version` print the build version. The current command is a root flag; there is no `relo version` subcommand.

Version and upgrade operations do not require an initialized project. Upgrade behavior is:

- `upgrade --check` queries the latest stable official GitHub Release without replacing the executable.
- `upgrade` installs the latest stable release only when it is newer; it does not downgrade.
- `upgrade --version VERSION` selects that exact release, with or without a leading `v`. An exact prerelease or older version is allowed; requesting the installed version is a no-op.
- `--check` and `--version` are mutually exclusive.
- Installation supports release archives for Linux, macOS, and Windows on `amd64` and `arm64`. Other platform combinations are rejected.
- Installation downloads the official OS/architecture archive and `checksums.txt`, verifies SHA-256, and only then replaces a manually installed executable.
- If relo detects package-manager ownership, it refuses replacement. Use the manager and command named in the error when provided.
- Upgrade has human output only and no `--json` mode.

## Project discovery and metadata

Run initialization from the intended project root:

```text
relo init --prd PATH --goal TEXT
```

`PATH` must name a readable PRD and `TEXT` must be non-empty. If no initialized ancestor exists, init creates `.relo/relo.db` in the current directory and stores the supplied PRD path, its content hash, and the goal. It refuses to initialize inside an existing relo project; there is no force-reinitialize flag.

All other project-state commands search upward from the current directory for `.relo/relo.db`. An empty `.relo/` directory is not initialized. The database is private state and is not a supported API.

```text
relo project show [--json]
relo project update --goal TEXT
relo project update --prd PATH
relo project update --goal TEXT --prd PATH
relo project refresh-prd
relo project remove --force
```

- `project update` requires at least one of `--goal` or `--prd`; supplied values must be non-empty. Changing the PRD also stores its current hash.
- `project refresh-prd` recomputes the hash of the current PRD path. It does not revise tasks automatically.
- `project remove --force` is irreversible and must run from the discovered project root, not a descendant. It removes `relo.db` and managed `-wal`/`-shm` sidecars while preserving the PRD, source files, and unknown `.relo` contents. It refuses removal while another relo process holds the project lock. If final cleanup fails after files are staged, follow the reported recovery-directory instruction before reinitializing. Run it only on explicit request.

## Task definitions

Create a pending task with exactly one objective source and at least one acceptance criterion:

```text
relo task create --title TEXT --objective TEXT
  --accept TEXT [--accept TEXT]... [--priority N]

relo task create --title TEXT --objective-file PATH
  --accept TEXT [--accept TEXT]... [--priority N]
```

The title, objective, and every acceptance criterion must be non-empty. Priority defaults to `100`, must be non-negative, and sorts lower values first. Creation prints the generated canonical task ID.

Read tasks with:

```text
relo task get TASK-ID [--json]
relo task get --title EXACT-TITLE [--json]
relo task list [--status pending|running|passed|failed] [--json]
```

Title lookup is exact and read-only. It fails when more than one task has that exact title. List status filters use stored states; `ready` and `blocked` are not stored-state filters.

Update or delete pending and failed tasks only:

```text
relo task update TASK-ID --title TEXT
relo task update TASK-ID --objective TEXT
relo task update TASK-ID --objective-file PATH
relo task update TASK-ID --priority N --reason TEXT
relo task update TASK-ID FLAG...                 # combine supported changes
relo task delete TASK-ID
```

- Update requires at least one changed field. Objective and objective-file cannot be combined.
- A priority change requires a non-empty audit reason.
- Delete is also rejected when downstream tasks depend on the task or a planned milestone anchors it.
- Stop a running task before editing or deleting. Reopen passed work before editing; the reopen guards are described under [Task runtime](#task-runtime).

Use canonical task IDs for mutations. Preserve other generated IDs exactly as printed by the CLI.

## Acceptance criteria and notes

```text
relo task acceptance add TASK-ID --text TEXT
relo task acceptance update TASK-ID AC-ID --text TEXT
relo task acceptance remove TASK-ID AC-ID

relo task note add TASK-ID --text TEXT
relo task note update TASK-ID NOTE-ID --text TEXT
relo task note remove TASK-ID NOTE-ID
```

These are task-definition mutations and therefore require the task to be pending or failed. Text values must be non-empty. A task must retain at least one acceptance criterion. Use `task get` to obtain the current `AC-ID` and `NOTE-ID` values.

## Dependencies

The first task ID is the target; all following IDs are prerequisites that it must wait for:

```text
relo task dependency add TASK-ID DEP-ID... --reason TEXT

relo task dependency add TASK-ID DEP-ID...
  --reason-for DEP-ID=TEXT [--reason-for DEP-ID=TEXT]...

relo task dependency remove TASK-ID DEP-ID... --reason TEXT
relo task dependency reason TASK-ID DEP-ID --text TEXT
```

- `--reason` applies one non-empty reason to every new edge.
- `--reason-for` supplies one mapping for each requested prerequisite. It cannot be combined with `--reason`.
- The target task must be pending or failed before its edges can change.
- Add rejects missing tasks, existing edges, self-dependencies, and cycles.
- Remove requires a non-empty audit reason. Changing an edge reason requires non-empty text.
- Batch add/remove operations are atomic.

For `relo task dependency add TASK-003 TASK-001 TASK-002 ...`, `TASK-003` waits for `TASK-001` and `TASK-002`.

## Task runtime

```text
relo task ready [--details] [--json]
relo task start TASK-ID...
relo task stop TASK-ID... --reason TEXT
relo task pass TASK-ID [--summary TEXT]
relo task fail TASK-ID --reason TEXT
relo task retry TASK-ID
relo task reopen TASK-ID --reason TEXT
```

Stored states and legal transitions are:

```text
pending --start--> running --pass--> passed
                         \--fail--> failed --retry--> pending
                         \--stop-------------------> pending
passed  --reopen-------------------------------> pending
```

A pending task is ready only when every prerequisite is passed; otherwise it is blocked. `start` accepts ready pending tasks. `pass`, `fail`, and `stop` accept running tasks with open attempts. `retry` accepts failed tasks and `reopen` accepts passed tasks. Stop, fail, and reopen require non-empty reasons; pass summary is optional.

Reopening a passed task is rejected while any downstream task is running or passed. To make an upstream task legally reopenable, stop running descendants and reopen passed descendants from leaves toward the upstream target. Multi-task start and stop are atomic.

## Milestones

Create a planned milestone with a non-empty title, reason, and at least one existing anchor task:

```text
relo milestone create --title TEXT --reason TEXT
  --anchor TASK-ID [--anchor TASK-ID]...
  [--recommend TEXT]...
```

Anchors may be in any stored task state. Recommendations are optional, non-binding text.

Read milestones with:

```text
relo milestone get MILESTONE-ID [--json]
relo milestone list [--status planned|marked] [--json]
relo milestone ready [--details] [--json]
```

Maintain planned milestones with:

```text
relo milestone update MILESTONE-ID [--title TEXT] [--reason TEXT]
relo milestone delete MILESTONE-ID
relo milestone anchor add MILESTONE-ID TASK-ID...
relo milestone anchor remove MILESTONE-ID TASK-ID...
relo milestone recommendation add MILESTONE-ID --text TEXT
relo milestone recommendation update MILESTONE-ID REC-ID --text TEXT
relo milestone recommendation remove MILESTONE-ID REC-ID
relo milestone mark MILESTONE-ID --summary TEXT [--reference TEXT]
```

- Update requires at least one non-empty changed field.
- Anchor removal must leave at least one anchor.
- Mark requires a non-empty summary and accepts an optional non-empty external reference.
- Readiness is derived: every anchor must be passed.
- Marking revalidates each anchor and its transitive dependency closure, requires the complete scope to be passed, and stores an immutable snapshot.
- Marked milestones cannot be updated, deleted, re-marked, or have anchors/recommendations changed. Later task changes do not rewrite the snapshot.
- Milestones never affect dependency legality, task readiness, or task start.

## Graph, status, and validation

```text
relo validate
relo status [--json]
relo graph [--format tree|json]
relo graph --tasks-only
relo graph --all-milestones
```

- `status` reports running, ready, blocked, and failed task IDs plus milestones ready to mark.
- Human tree graph output includes planned and ready milestone checkpoint anchors by default. `--all-milestones` also includes marked history; `--tasks-only` omits all milestone overlays.
- `--tasks-only` and `--all-milestones` are mutually exclusive and are valid only with tree output.
- Graph JSON always contains only task-DAG nodes, dependency edges, and task summary state. Milestones never become graph nodes or edges.
- `validate` prints warnings and errors to stderr. It prints `OK` to stdout whenever there are no errors, even when warnings exist.

## JSON and process errors

JSON is supported only for:

```text
relo project show --json
relo graph --format json
relo status --json
relo task list [--status pending|running|passed|failed] --json
relo task get TASK-ID --json
relo task get --title EXACT-TITLE --json
relo task ready [--details] --json
relo milestone get MILESTONE-ID --json
relo milestone list [--status planned|marked] --json
relo milestone ready [--details] --json
```

Do not assume mutations, `validate`, version, upgrade, or other commands accept `--json`.

Successful JSON uses:

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": true,
  "data": {}
}
```

Recognized JSON-mode failures use a non-zero process exit and an error envelope on stdout:

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": false,
  "error": {"code": "...", "message": "...", "details": {}}
}
```

Unknown command paths can fail during root discovery before JSON mode is established and write plain text to stderr. Scripts must check the exit code, capture stdout and stderr separately, verify `schemaVersion`, and branch on `ok` before reading `data`.

Important `data` contracts:

- `project show`: `project` with `goal`, `prd_path`, and `prd_hash`.
- `graph`: `project_goal`, task `nodes`, dependency `edges`, and task-only `summary`.
- `status`: `summary` with `running`, `ready`, `blocked`, `failed`, and `milestones_ready_to_mark`.
- `task list`: summary-only `tasks` including attempt count and last failure/completion values.
- `task get`: nested `project` plus detailed `task`; `last_failure_reason`, `last_completion_summary`, and `current_attempt` are nullable. A running attempt has nullable completion, summary, and reason fields.
- `task ready`: detailed `tasks` in the current ready frontier.
- `milestone get`: detailed `milestone`; list and ready return `milestones` arrays.
