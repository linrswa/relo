# Agent Workflow

Relo is the task runtime for main-agent/subagent work. Agents must use the `relo` CLI for all project, task, dependency, runtime, graph, status, validation, and milestone changes. Never read or write `.relo/relo.db` directly. Milestones are non-executing checkpoint markers whose scope is their anchors and transitive dependency closure; they never alter task-DAG legality or dispatch.

## Handoff loop

1. The main agent reads the PRD and initializes the project with `relo init --prd <path> --goal <goal>`.
2. The main agent creates the task graph through CLI commands only.
3. The main agent runs `relo validate` and `relo graph` before dispatching work.
4. The main agent chooses one or more IDs from `relo task ready`.
5. The main agent starts selected tasks with `relo task start TASK-ID...` and hands each subagent a task ID (optionally with the title for human context).
6. A subagent fetches its assignment with `relo task get TASK-ID` or, for read-only lookup only, `relo task get --title <exact title>`.
7. The subagent reports completion or blockage to the main agent. The main agent records the result with `pass`, `fail`, `stop`, `retry`, or `reopen`.
8. If work discovers a missing dependency or content change, the main agent stops/reopens the affected task, updates dependencies or task fields by ID, validates again, and continues from the ready frontier.

## Milestone checkpoints

A milestone is a main-agent checkpoint, not a task or dispatch barrier. When `relo milestone ready` returns a checkpoint, the main agent reads its scope and free-form recommendations, then autonomously weighs the combined diff, architecture and integration risk, test coverage, remaining work, and execution budget. It may run a reviewer or read-only sweeper, run broader tests, reorder future work, add another check, skip low-value checks, or mark immediately.

If review shows that an original task missed its own acceptance criteria, reopen that task. If a sweeper identifies non-trivial changes or review finds other distinct work, create a normal new task with acceptance criteria and complete it through ordinary task attempts; IDs remain monotonic and existing task IDs never change. Update dependency edges and add the new task as a milestone anchor when it must complete before the checkpoint is marked. Before inserting a downstream dependency, obey task mutation safety: stop its worker and `task stop` it if it is running, or safely reopen passed downstream work from downstream to upstream. If that cannot be done safely, the CLI must reject the mutation; milestones never bypass task constraints. A pending anchor removes the milestone from `ready` until it passes; milestones never themselves block `task ready`.

Only the main agent, with full DAG and integration context, decides whether to mark a milestone. Workers should complete and report only their assigned task. Mark only when the checkpoint is sufficiently stable, and summarize which recommendations were followed or skipped and why.

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
- `relo milestone create --title "..." --reason "..." --anchor TASK-ID [--recommend "..."]`: create a planned checkpoint.
- `relo milestone get MILESTONE-ID [--json]`, `relo milestone list [--status planned|marked] [--json]`, `relo milestone ready [--details] [--json]`, `relo milestone update MILESTONE-ID [--title "..."] [--reason "..."]`, `relo milestone delete MILESTONE-ID`: inspect and manage planned checkpoints.
- `relo milestone anchor add|remove MILESTONE-ID TASK-ID...`: manage planned checkpoint anchors.
- `relo milestone recommendation add MILESTONE-ID --text "..."`, `relo milestone recommendation update MILESTONE-ID REC-ID --text "..."`, `relo milestone recommendation remove MILESTONE-ID REC-ID`: manage free-form recommendations.
- `relo milestone mark MILESTONE-ID --summary "..." [--reference "..."]`: capture an immutable snapshot once every scope task has passed.
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
