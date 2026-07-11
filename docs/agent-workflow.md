# Agent Workflow

Relo is the task runtime for main-agent/subagent work. Agents must use the `relo` CLI for all project, task, dependency, runtime, graph, status, and validation changes. Never read or write `.relo/relo.db` directly.

## Handoff loop

1. The main agent reads the PRD and initializes the project with `relo init --prd <path> --goal <goal>`.
2. The main agent creates the task graph through CLI commands only.
3. The main agent runs `relo validate` and `relo graph` before dispatching work.
4. The main agent chooses one or more IDs from `relo task ready`.
5. The main agent starts selected tasks with `relo task start TASK-ID...` and hands each subagent a task ID (optionally with the title for human context).
6. A subagent fetches its assignment with `relo task get TASK-ID` or, for read-only lookup only, `relo task get --title <exact title>`.
7. The subagent reports completion or blockage to the main agent. The main agent records the result with `pass`, `fail`, `stop`, `retry`, or `reopen`.
8. If work discovers a missing dependency or content change, the main agent stops/reopens the affected task, updates dependencies or task fields by ID, validates again, and continues from the ready frontier.

## IDs vs titles

Mutation commands require canonical task IDs such as `TASK-001`. Titles are allowed only for read-only lookup via `task get --title` because titles can be duplicated, renamed, or ambiguous. Handoffs should include the task ID as the source of truth.

## Core commands

- `relo init --prd prd.md --goal "<goal>"`: create `.relo/` metadata for an existing PRD.
- `relo project update --goal "..." [--prd docs/prd.md]`: update project metadata; PRD paths are resolved from the project root and hashed atomically.
- `relo project refresh-prd`: acknowledge current PRD contents by recomputing and storing the PRD hash; review tasks separately because refresh does not mutate them.
- `relo task create --title "..." --objective "..." --accept "..." [--priority N]`: create a task. At least one acceptance criterion is required. Lower priority numbers sort earlier; default is `100`.
- `relo task dependency add TASK-ID DEP-ID... --reason "..."`: make `TASK-ID` wait for dependencies. Use `--reason-for DEP-ID=text` when each edge needs a different reason.
- `relo task dependency remove TASK-ID DEP-ID... --reason "..."`: remove dependency edges.
- `relo task dependency reason TASK-ID DEP-ID --text "..."`: update one edge reason.
- `relo task ready [--details] [--json]`: list the complete ready frontier.
- `relo task get TASK-ID [--json]` or `relo task get --title "..."`: print project context, objective, acceptance criteria, dependencies, and notes for subagent handoff.
- `relo task start TASK-ID...`: atomically mark ready tasks running and open attempts.
- `relo task pass TASK-ID --summary "..."`: close the running attempt as passed.
- `relo task fail TASK-ID --reason "..."`: close the running attempt as failed.
- `relo task stop TASK-ID... --reason "..."`: atomically stop running work back to pending when the main agent needs to revise the graph or content.
- `relo task retry TASK-ID`: move a failed task back to pending.
- `relo task reopen TASK-ID --reason "..."`: move a passed task back to pending when safe.
- `relo graph [--format tree|json]`: inspect deterministic graph shape and status summary.
- `relo status [--json]`: inspect running, ready, blocked, and failed buckets.
- `relo validate`: check project/task data, dependency integrity, cycles, and runtime invariants.

## JSON mode and errors

Commands that support `--json` or `--format json` emit a `relo.output/v1` envelope on stdout. Successful envelopes have `ok: true` and a `data` object. JSON errors also go to stdout with `ok: false`; stderr remains empty. Non-JSON failures write human-readable errors to stderr and return a non-zero exit code.
