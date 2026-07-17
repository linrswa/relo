# relo

`relo` is a Go command-line task runtime for coding agents and humans. It stores project context, task definitions, dependency constraints, execution attempts, and optional milestone checkpoints behind a stable CLI.

The CLI enforces task and DAG invariants; it does not choose tasks, launch workers, or implement project work automatically.

## Features

- Associate one project goal with an existing PRD and track its content hash
- Create tasks with objectives, acceptance criteria, notes, and priorities
- Build and validate an acyclic dependency graph
- Derive complete ready and blocked task sets
- Track `pending`, `running`, `passed`, and `failed` states with attempts
- Atomically start or stop multiple tasks
- Render deterministic tree and versioned JSON views
- Define optional milestone checkpoints with immutable marked snapshots
- Persist state in a private SQLite database without requiring CGO

## Install

### Release binaries

Download a prebuilt archive from [GitHub Releases](https://github.com/linrswa/relo/releases). Release archives are available for:

- Linux: amd64 and arm64
- macOS: Intel and Apple silicon
- Windows: amd64 and arm64

Each release includes `checksums.txt` with SHA-256 checksums for its archives.

### Go install

Go 1.26.5 or newer is required:

```bash
go install github.com/linrswa/relo/cmd/relo@latest
```

### Build from source

```bash
git clone https://github.com/linrswa/relo.git
cd relo
mkdir -p ./bin
go build -o ./bin/relo ./cmd/relo
./bin/relo --help
```

## Quick start

Create or select an existing PRD:

```bash
cat > prd.md <<'EOF'
# Example project

Build a small service in two steps.
EOF
```

Initialize relo in the repository:

```bash
relo init --prd prd.md --goal "Build the example service"
```

Create two tasks. Each task needs an objective and at least one acceptance criterion:

```bash
relo task create \
  --title "Create service model" \
  --objective "Define and test the service data model" \
  --accept "The model package tests pass" \
  --priority 10
# TASK-001

relo task create \
  --title "Add service API" \
  --objective "Expose the service model through an API" \
  --accept "API tests cover create and read operations" \
  --priority 20
# TASK-002
```

Make the API task wait for the model task:

```bash
relo task dependency add TASK-002 TASK-001 \
  --reason "The API uses the service model"
```

Validate and inspect the graph:

```bash
relo validate
relo graph
relo task ready --details
```

`relo graph` renders the dependency tree and current execution frontier:

```text
🎯 Goal: Build the example service
│
└── 🔵 TASK-001  Create service model
    └── ⏸ TASK-002  Add service API

Running: none
Ready:   TASK-001
Blocked: TASK-002
Failed:  none
```

Start and complete the ready work:

```bash
relo task start TASK-001
relo task pass TASK-001 --summary "Model implemented and tested"

relo task ready --details
relo task start TASK-002
relo task pass TASK-002 --summary "API implemented and tested"

relo status
```

Initialization creates `.relo/relo.db`. The database is ignored by Git and is an internal implementation detail—use the CLI rather than reading or editing it directly.

To permanently remove relo state, run `relo project remove --force` from the project root. This preserves the PRD, source files, and any unknown files under `.relo`; only the relo-managed database and its sidecars are removed.

## Core model

### Tasks and dependencies

A task contains:

- A generated ID such as `TASK-001`
- A title and objective
- One or more acceptance criteria
- Optional notes
- A non-negative priority; lower numbers sort first
- Zero or more dependencies with reasons

For this command:

```bash
relo task dependency add TASK-003 TASK-001 TASK-002 --reason "Required inputs"
```

`TASK-003` is the target and must wait for both `TASK-001` and `TASK-002`. Dependency changes are transactional and reject missing tasks, duplicate edges, self-dependencies, and cycles.

### Task lifecycle

```text
pending --start--> running --pass--> passed
                         \--fail--> failed --retry--> pending
                         \--stop--> pending
passed  --reopen-----------------> pending
```

A pending task is **ready** when all dependencies have passed; otherwise it is **blocked**. Ready and blocked are derived and are not stored task states.

Task definitions can be changed only while pending or failed. Stop running work before revising it. Reopening passed work is rejected while downstream tasks are running or passed.

### Milestones

Milestones are optional, non-executing checkpoints. Their scope consists of explicitly selected anchor tasks and those tasks' transitive dependencies.

```bash
relo milestone create \
  --title "Service foundation" \
  --reason "The base service is ready for an integration check" \
  --anchor TASK-002 \
  --recommend "Run the integration suite"

relo milestone ready --details
relo milestone mark MILESTONE-001 \
  --summary "Integration checks passed"
```

A planned milestone becomes ready when all anchors pass. Milestones never block task readiness or execution. Marking verifies the complete scope and stores an immutable snapshot.

## Command overview

### Project and views

```text
relo init --prd PATH --goal TEXT
relo project show [--json]
relo version
relo project update [--goal TEXT] [--prd PATH]
relo project refresh-prd
relo project remove --force
relo validate
relo graph [--format tree|json]
relo status [--json]
```

### Task definitions

```text
relo task create ...
relo task get TASK-ID [--json]
relo task get --title EXACT-TITLE [--json]
relo task list [--status pending|running|passed|failed] [--json]
relo task update TASK-ID ...
relo task delete TASK-ID
relo task acceptance add|update|remove ...
relo task note add|update|remove ...
relo task dependency add|remove|reason ...
```

Titles are available only for exact, read-only `task get` lookup. Mutation commands require canonical task IDs.

### Runtime

```text
relo task ready [--details] [--json]
relo task start TASK-ID...
relo task stop TASK-ID... --reason TEXT
relo task pass TASK-ID [--summary TEXT]
relo task fail TASK-ID --reason TEXT
relo task retry TASK-ID
relo task reopen TASK-ID --reason TEXT
```

Multi-task start and stop operations are all-or-nothing.

### Milestones

```text
relo milestone create ...
relo milestone get MILESTONE-ID [--json]
relo milestone list [--status planned|marked] [--json]
relo milestone ready [--details] [--json]
relo milestone update|delete|mark ...
relo milestone anchor add|remove ...
relo milestone recommendation add|update|remove ...
```

Run `relo <command> --help` for the current executable's exact syntax.

## JSON output

Structured reads use a versioned envelope:

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": true,
  "data": {}
}
```

JSON is implemented for:

```text
relo project show --json
relo graph --format json
relo status --json
relo task list [--status ...] --json
relo task get ... --json
relo task ready --json
relo milestone get ... --json
relo milestone list --json
relo milestone ready --json
```

Most mutations and other reads use human-readable output only. Recognized JSON-mode errors return an `ok: false` envelope on stdout and a non-zero exit code.

## Development

Run the test suite:

```bash
go test ./...
```

Additional checks:

```bash
go test -race ./...
go vet ./...
test -z "$(gofmt -l cmd internal)"
```

Tags matching `v*` trigger the GitHub Actions release workflow. GoReleaser builds the supported platform archives, injects the release version into `relo version`, generates SHA-256 checksums, and publishes a GitHub Release.

## Documentation

- [Agent workflow and command reference](docs/agent-workflow.md)
- [End-to-end task lifecycle scenario](docs/e2e-agent-scenario.md)
- [Original MVP design plan](plan.md)
- [Milestone checkpoint design](milestone_plan.md)

## Agent skills

Tool-neutral Agent Skills packages live under [`skills/`](skills/):

- [`relo-cli-guide`](skills/relo-cli-guide/SKILL.md) documents the public CLI contract and legal state transitions.
- [`relo-delivery-workflow`](skills/relo-delivery-workflow/SKILL.md) guides PRD-to-DAG planning, proportional execution, and deliberate milestone checkpoint decisions.

The repository intentionally does not duplicate these packages into harness-specific discovery directories. Install the complete desired skill folder in the location used by your agent—for example `.agents/skills/` for Codex, `.claude/skills/` for Claude Code, or `.kiro/skills/` for Kiro. Keep bundled references and other files with `SKILL.md` when copying a package.

## Current scope

The repository contains an implemented and tested MVP. It intentionally does not provide:

- Automatic task selection or agent launching
- Worker lifecycle or concurrency management
- Worktree, container, or file-overlap isolation
- Git commit, merge, or conflict tooling
- MCP, daemon, or server modes
- Mermaid rendering or database export/import

Multiple tasks may be running at once, but process isolation and safe parallel editing remain the caller's responsibility.

## CLI UX notes

Run `relo --help` to discover commands; an initialized project is discovered from the current directory or a parent containing `.relo/relo.db`. `relo version` prints the human-readable build version.

Task creation requires exactly one of `--objective` or `--objective-file`; update accepts either source but rejects both. In dependency commands, the first ID is the dependent task and later IDs are prerequisites. Running tasks must be stopped before definition changes, and passed tasks must be reopened first.

`relo project show --json`, `relo task list --json`, and `relo task get --json` use the `relo.output/v1` envelope. Project show includes `goal`, `prd_path`, and `prd_hash`; list output is summary-only. Task get's nested project remains `goal` and `prd_path`, and its task detail includes creation/runtime fields; `last_failure_reason`, `last_completion_summary`, and `current_attempt` are nullable. `current_attempt` is present only while running and has nullable `completed_at`, `summary`, and `reason`. `relo validate` writes warnings to stderr and always writes `OK` to stdout when it has no errors.

Milestone readiness means every anchor passed. Marking validates anchors and their transitive dependency scope; milestones are non-gating and never affect task readiness, dependency legality, or start.

## License

`relo` is available under the [MIT License](LICENSE).
